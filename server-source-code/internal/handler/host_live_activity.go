package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgtype"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/models"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// What a host's agent is doing now, for the live part of its activity: the
// jobs still open (an agent update with its stages, a restart waiting for
// its answer), the tasks queued for it, its patch runs queued or running,
// its compliance scan in progress, and what ended a moment ago, with its
// outcome. The page polls it every few seconds while something is open.

// How long an open job counts as live: an agent update until the cleanup
// closes it (45 minutes), another job a few minutes (an answer that never
// came, a row a worker left open). The history keeps them all.
const (
	liveOpenWindow      = time.Hour
	liveOtherOpenWindow = 15 * time.Minute
)

// Kinds of live tasks.
const (
	liveKindJob        = "job"
	liveKindQueued     = "queued"
	liveKindPatchRun   = "patch_run"
	liveKindCompliance = "compliance_scan"
)

// LiveTask is something a host's agent is doing, or just finished.
type LiveTask struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"`
	// Finished: the task ended (Status says how) and is shown a moment.
	Finished bool `json:"finished"`
	// Running: work is going on, or about to (not a task scheduled for
	// later): the page polls fast while a task runs.
	Running bool `json:"running"`
	// Stage and Percent: where an agent update, a patch run or a scan is.
	Stage   string `json:"stage,omitempty"`
	Percent *int   `json:"percent,omitempty"`
	// Stages: when each stage of an agent update began (RFC 3339).
	Stages      map[string]string `json:"stages,omitempty"`
	FromVersion string            `json:"from_version,omitempty"`
	ToVersion   string            `json:"to_version,omitempty"`
	// Trigger: "agent" for an update the agent started on its own.
	Trigger string `json:"trigger,omitempty"`
	// Message: the last line of a patch run's output, a scan's progress.
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
	// Package: the package a patch run is for (one-package runs).
	Package     string     `json:"package,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
}

type liveActivityReader interface {
	Jobs(ctx context.Context, apiID string, openAfter, finishedAfter time.Time) ([]db.ListLiveJobHistoryRow, error)
	PatchRuns(ctx context.Context, hostID string, finishedAfter time.Time) ([]db.ListLivePatchRunsRow, error)
}

type liveHostReader interface {
	GetByID(ctx context.Context, id string) (*models.Host, error)
}

// HostLiveActivityHandler serves GET /dashboard/hosts/{hostId}/activity/live.
type HostLiveActivityHandler struct {
	hosts       liveHostReader
	live        liveActivityReader
	inspector   *asynq.Inspector
	compliance  *ComplianceProgressTracker
	permissions *store.PermissionsStore
	now         func() time.Time
}

// NewHostLiveActivityHandler creates the handler. inspector and compliance
// may be nil.
func NewHostLiveActivityHandler(hosts liveHostReader, live liveActivityReader, inspector *asynq.Inspector, compliance *ComplianceProgressTracker, permissions *store.PermissionsStore) *HostLiveActivityHandler {
	return &HostLiveActivityHandler{hosts: hosts, live: live, inspector: inspector, compliance: compliance, permissions: permissions, now: time.Now}
}

// Get returns the host's live tasks: open ones first, newest first.
func (h *HostLiveActivityHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hostID := chi.URLParam(r, "hostId")
	host, err := h.hosts.GetByID(ctx, hostID)
	if err != nil || host == nil {
		Error(w, http.StatusNotFound, "Host not found")
		return
	}
	now := h.now()
	finishedAfter := now.Add(-liveFinishedWindow)

	jobs, err := h.live.Jobs(ctx, host.ApiID, now.Add(-liveOpenWindow), finishedAfter)
	if err != nil {
		slog.Error("live activity: jobs", "host_id", hostID, "error", err)
		Error(w, http.StatusInternalServerError, "Failed to load live activity")
		return
	}
	tasks := liveTasksFromJobs(jobs, now)

	patching := hostctx.HasModule(ctx, "patching")
	if h.inspector != nil {
		if q, qerr := queue.GetHostJobs(ctx, h.inspector, hostID, host.ApiID, 50); qerr == nil && q != nil {
			// A patch run shows as itself, not as its queue task.
			var skip map[string]bool
			if patching {
				skip = map[string]bool{queue.TypeRunPatch: true}
			}
			tasks = append(tasks, liveTasksFromQueue(q.JobHistory, jobs, skip)...)
		}
	}
	if patching {
		runs, perr := h.live.PatchRuns(ctx, hostID, finishedAfter)
		if perr != nil {
			slog.Warn("live activity: patch runs", "host_id", hostID, "error", perr)
		} else {
			tasks = append(tasks, liveTasksFromPatchRuns(runs)...)
		}
	}
	if h.compliance != nil && hostctx.HasModule(ctx, "compliance") && middleware.RoleAllows(r, h.permissions, "can_view_reports") {
		if p, ok := h.compliance.Get(complianceKey(ctx, host.ApiID)); ok {
			tasks = append(tasks, liveTaskFromCompliance(p))
		}
	}
	sortLiveTasks(tasks)
	JSON(w, http.StatusOK, map[string]any{"tasks": tasks, "now": now.UTC()})
}

func liveTasksFromJobs(rows []db.ListLiveJobHistoryRow, now time.Time) []LiveTask {
	out := make([]LiveTask, 0, len(rows))
	for _, r := range rows {
		finished := r.Status == store.JobCompleted || r.Status == store.JobFailed
		if !finished && r.JobName != store.AgentUpdateJobName && r.CreatedAt.Valid &&
			now.Sub(r.CreatedAt.Time) > liveOtherOpenWindow {
			continue
		}
		t := LiveTask{
			ID:         r.JobID,
			Kind:       liveKindJob,
			Name:       r.JobName,
			Status:     r.Status,
			Finished:   finished,
			Running:    r.Status == store.JobActive || r.Status == store.JobWaiting,
			Attempt:    int(r.AttemptNumber),
			StartedAt:  timePtr(r.CreatedAt),
			UpdatedAt:  timePtr(r.UpdatedAt),
			FinishedAt: timePtr(r.CompletedAt),
		}
		if r.ErrorMessage != nil {
			t.Error = *r.ErrorMessage
		}
		if r.JobName == store.AgentUpdateJobName {
			applyAgentUpdateOutput(&t, r.Output)
		}
		out = append(out, t)
	}
	return out
}

// applyAgentUpdateOutput reads the stages recorded on an agent update.
func applyAgentUpdateOutput(t *LiveTask, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var o struct {
		Stage       string            `json:"stage"`
		Percent     *int              `json:"percent"`
		Stages      map[string]string `json:"stages"`
		FromVersion string            `json:"from_version"`
		ToVersion   string            `json:"to_version"`
		Trigger     string            `json:"trigger"`
		Message     string            `json:"message"`
	}
	if err := json.Unmarshal(raw, &o); err != nil {
		return
	}
	t.Stage, t.Percent, t.Stages = o.Stage, o.Percent, o.Stages
	t.FromVersion, t.ToVersion, t.Trigger = o.FromVersion, o.ToVersion, o.Trigger
	if t.Error == "" {
		t.Message = o.Message
	}
}

// liveTasksFromQueue: the tasks queued for the host that no job row shows
// yet (not picked up, waiting for a retry, scheduled), but those named in
// skip.
func liveTasksFromQueue(rows []queue.HostJobRow, jobs []db.ListLiveJobHistoryRow, skip map[string]bool) []LiveTask {
	known := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		known[j.JobID] = true
	}
	var out []LiveTask
	for _, r := range rows {
		if known[r.JobID] || skip[r.JobName] {
			continue
		}
		switch r.Status {
		case "waiting", "delayed", "active":
		default:
			continue
		}
		t := LiveTask{
			ID:      r.JobID,
			Kind:    liveKindQueued,
			Name:    r.JobName,
			Status:  r.Status,
			Running: r.Status != "delayed",
			Attempt: r.AttemptNumber,
		}
		// The queue gives a delayed task's next run, not when it was queued.
		if r.Status == "delayed" {
			t.ScheduledAt = r.CreatedAt
		} else {
			t.StartedAt = r.CreatedAt
		}
		if r.ErrorMessage != nil {
			t.Error = *r.ErrorMessage
		}
		out = append(out, t)
	}
	return out
}

func liveTasksFromPatchRuns(rows []db.ListLivePatchRunsRow) []LiveTask {
	out := make([]LiveTask, 0, len(rows))
	for _, r := range rows {
		open := r.Status == "queued" || r.Status == "running"
		t := LiveTask{
			ID:          r.ID,
			Kind:        liveKindPatchRun,
			Name:        r.PatchType,
			Status:      r.Status,
			Finished:    !open,
			Running:     r.Status == "running",
			DryRun:      r.DryRun,
			Message:     lastOutputLine(r.OutputTail),
			StartedAt:   timePtr(r.StartedAt),
			FinishedAt:  timePtr(r.CompletedAt),
			ScheduledAt: timePtr(r.ScheduledAt),
		}
		if t.StartedAt == nil {
			t.StartedAt = timePtr(r.CreatedAt)
		}
		if r.PackageName != nil {
			t.Package = *r.PackageName
		}
		if r.ErrorMessage != nil {
			t.Error = agentText(*r.ErrorMessage, 300)
		}
		out = append(out, t)
	}
	return out
}

func liveTaskFromCompliance(p ComplianceProgress) LiveTask {
	started, updated := p.StartedAt, p.UpdatedAt
	t := LiveTask{
		ID:        "compliance-scan",
		Kind:      liveKindCompliance,
		Name:      p.ProfileName,
		Stage:     p.Phase,
		Message:   p.Message,
		Error:     p.Error,
		StartedAt: &started,
		UpdatedAt: &updated,
	}
	pct := p.Percent
	t.Percent = &pct
	switch {
	case !p.Finished():
		t.Status, t.Running = "active", true
	case p.Phase == "completed":
		t.Status, t.Finished, t.FinishedAt = "completed", true, &updated
	default:
		t.Status, t.Finished, t.FinishedAt = p.Phase, true, &updated
	}
	return t
}

// complianceKey identifies an agent's scan across the contexts this process
// serves.
func complianceKey(ctx context.Context, apiID string) string {
	return hostctx.TenantHostKey(ctx) + "|" + apiID
}

// ansiRe matches terminal escape sequences in command output.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// lastOutputLine is the last non-empty line of a command's output, as a
// terminal would show it (a progress bar redraws its line after a carriage
// return).
func lastOutputLine(out string) string {
	out = ansiRe.ReplaceAllString(out, "")
	lines := strings.Split(out, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if j := strings.LastIndex(strings.TrimRight(line, "\r"), "\r"); j >= 0 {
			line = line[j+1:]
		}
		if line = agentText(line, 200); line != "" {
			return line
		}
	}
	return ""
}

// sortLiveTasks puts open tasks first, then the finished ones, each newest
// first.
func sortLiveTasks(tasks []LiveTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Finished != tasks[j].Finished {
			return !tasks[i].Finished
		}
		return liveSortTime(tasks[i]).After(liveSortTime(tasks[j]))
	})
}

func liveSortTime(t LiveTask) time.Time {
	for _, ts := range []*time.Time{t.FinishedAt, t.StartedAt, t.ScheduledAt} {
		if ts != nil {
			return *ts
		}
	}
	return time.Time{}
}

func timePtr(ts pgtype.Timestamp) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}
