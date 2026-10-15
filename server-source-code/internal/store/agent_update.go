package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtime"
)

// Agent updates in job_history. The queue worker records the update_agent
// command it sends (stage sent), the agent reports each stage over its
// WebSocket on the same row (output.stage, output.stages: when each began),
// and the row ends when the agent says so (up to date, skipped, failed) or
// comes back on another version (done). An update the agent starts on its
// own, after a report, gets a row from its first stage.

// AgentUpdateQueue and AgentUpdateJobName are those of the update_agent
// queue task (queue.QueueAgentCommands, queue.TypeUpdateAgent).
const (
	AgentUpdateQueue   = "agent-commands"
	AgentUpdateJobName = "update_agent"
)

// Stages of an agent update. The server records sent and done; the agent
// reports the others.
const (
	AgentUpdateSent        = "sent"
	AgentUpdateChecking    = "checking"
	AgentUpdateUpToDate    = "up_to_date"
	AgentUpdateDownloading = "downloading"
	AgentUpdateVerifying   = "verifying"
	AgentUpdateInstalling  = "installing"
	AgentUpdateRestarting  = "restarting"
	AgentUpdateSkipped     = "skipped"
	AgentUpdateFailed      = "failed"
	AgentUpdateDone        = "done"
)

// IsAgentReportedUpdateStage reports whether an agent may report stage.
func IsAgentReportedUpdateStage(stage string) bool {
	switch stage {
	case AgentUpdateChecking, AgentUpdateUpToDate, AgentUpdateDownloading, AgentUpdateVerifying,
		AgentUpdateInstalling, AgentUpdateRestarting, AgentUpdateSkipped, AgentUpdateFailed:
		return true
	}
	return false
}

// AgentUpdateWindow is how long after it was sent an update still takes
// the agent's stages. The patch-run cleanup closes it before
// (AgentUpdateDeadline in the queue package).
const AgentUpdateWindow = time.Hour

// AgentUpdateProgress is a stage the agent reported.
type AgentUpdateProgress struct {
	Stage string
	// Percent of the download, nil when not known or another stage.
	Percent     *int
	FromVersion string
	ToVersion   string
	// Message is why the update was skipped or failed.
	Message string
}

// AgentUpdateQueries are the queries agent updates use (db.Queries).
type AgentUpdateQueries interface {
	GetOpenAgentUpdate(ctx context.Context, arg db.GetOpenAgentUpdateParams) (db.GetOpenAgentUpdateRow, error)
	RecordAgentUpdateStage(ctx context.Context, arg db.RecordAgentUpdateStageParams) (int64, error)
	InsertJobHistoryStarted(ctx context.Context, arg db.InsertJobHistoryStartedParams) error
}

// AgentUpdateStore records agent updates.
type AgentUpdateStore struct {
	db  database.DBProvider
	now func() time.Time
}

// NewAgentUpdateStore creates the store.
func NewAgentUpdateStore(db database.DBProvider) *AgentUpdateStore {
	return &AgentUpdateStore{db: db, now: time.Now}
}

// Open reports whether the agent has an update open.
func (s *AgentUpdateStore) Open(ctx context.Context, apiID string) (bool, error) {
	_, err := openAgentUpdate(ctx, s.db.DB(ctx).Queries, apiID, s.now())
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RecordProgress records a stage the agent of hostID reported.
func (s *AgentUpdateStore) RecordProgress(ctx context.Context, apiID, hostID string, p AgentUpdateProgress) error {
	return RecordAgentUpdateProgress(ctx, s.db.DB(ctx).Queries, apiID, hostID, p, s.now())
}

// RecordConnected closes the open update of an agent that connected on
// version.
func (s *AgentUpdateStore) RecordConnected(ctx context.Context, apiID, version string) error {
	return RecordAgentUpdateConnected(ctx, s.db.DB(ctx).Queries, apiID, version, s.now())
}

func openAgentUpdate(ctx context.Context, q AgentUpdateQueries, apiID string, now time.Time) (db.GetOpenAgentUpdateRow, error) {
	return q.GetOpenAgentUpdate(ctx, db.GetOpenAgentUpdateParams{
		ApiID:        apiID,
		QueueName:    AgentUpdateQueue,
		JobName:      AgentUpdateJobName,
		CreatedAfter: pgtime.From(now.Add(-AgentUpdateWindow)),
	})
}

// RecordAgentUpdateSent records on the worker's row that the command went
// out to an agent on fromVersion ("" when not known).
func RecordAgentUpdateSent(ctx context.Context, q AgentUpdateQueries, jobID, apiID, fromVersion string, now time.Time) error {
	patch := map[string]any{"stage": AgentUpdateSent, "trigger": "server"}
	if v := NormalizeAgentVersion(fromVersion); v != "" {
		patch["from_version"] = v
	}
	return recordAgentUpdateStage(ctx, q, jobID, apiID, AgentUpdateSent, JobActive, "", patch, now)
}

// RecordAgentUpdateProgress records a stage the agent reported on its open
// update, or on a new row for an update it started on its own.
func RecordAgentUpdateProgress(ctx context.Context, q AgentUpdateQueries, apiID, hostID string, p AgentUpdateProgress, now time.Time) error {
	if !IsAgentReportedUpdateStage(p.Stage) {
		return fmt.Errorf("unknown agent update stage %q", p.Stage)
	}
	open, err := openAgentUpdate(ctx, q, apiID, now)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No update was asked for: the agent started one after a report,
		// and reports it from the start of its download. Anything else
		// belongs to an update already closed (the agent came back on its
		// new version before its last stage was recorded, say).
		if p.Stage != AgentUpdateDownloading || p.Percent != nil {
			return nil
		}
		jobID := "agent-update-" + uuid.NewString()
		output, err := json.Marshal(map[string]any{"trigger": "agent"})
		if err != nil {
			return err
		}
		host, api := hostID, apiID
		if err := q.InsertJobHistoryStarted(ctx, db.InsertJobHistoryStartedParams{
			ID:        uuid.NewString(),
			JobID:     jobID,
			QueueName: AgentUpdateQueue,
			JobName:   AgentUpdateJobName,
			HostID:    &host,
			ApiID:     &api,
			Output:    output,
		}); err != nil {
			return err
		}
		open.JobID = jobID
	case err != nil:
		return err
	}
	status, failure := agentUpdateOutcome(p)
	patch := map[string]any{"stage": p.Stage, "percent": nil}
	if p.Percent != nil {
		patch["percent"] = *p.Percent
	}
	if v := NormalizeAgentVersion(p.FromVersion); v != "" {
		patch["from_version"] = v
	}
	if v := NormalizeAgentVersion(p.ToVersion); v != "" {
		patch["to_version"] = v
	}
	if p.Message != "" {
		patch["message"] = p.Message
	}
	return recordAgentUpdateStage(ctx, q, open.JobID, apiID, p.Stage, status, failure, patch, now)
}

// agentUpdateOutcome is the row status a stage leads to, and the failure to
// record with it.
func agentUpdateOutcome(p AgentUpdateProgress) (status, failure string) {
	switch p.Stage {
	case AgentUpdateUpToDate:
		return JobCompleted, ""
	case AgentUpdateSkipped:
		reason := p.Message
		if reason == "" {
			reason = "the agent did not start it"
		}
		return JobFailed, "update skipped: " + reason
	case AgentUpdateFailed:
		if p.Message == "" {
			return JobFailed, "the agent reported a failure"
		}
		return JobFailed, p.Message
	default:
		return JobActive, ""
	}
}

// RecordAgentUpdateConnected closes the open update of an agent that
// connected on version: done when the version changed, failed when the
// agent restarted for the update and came back on the one it left. Older
// agents send no version ("").
func RecordAgentUpdateConnected(ctx context.Context, q AgentUpdateQueries, apiID, version string, now time.Time) error {
	version = NormalizeAgentVersion(version)
	if version == "" {
		return nil
	}
	open, err := openAgentUpdate(ctx, q, apiID, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var out struct {
		Stage       string `json:"stage"`
		FromVersion string `json:"from_version"`
	}
	if len(open.Output) > 0 {
		_ = json.Unmarshal(open.Output, &out)
	}
	stage, status, failure, ok := agentUpdateOnReconnect(out.Stage, NormalizeAgentVersion(out.FromVersion), version)
	if !ok {
		return nil
	}
	patch := map[string]any{"stage": stage, "percent": nil, "to_version": version}
	return recordAgentUpdateStage(ctx, q, open.JobID, apiID, stage, status, failure, patch, now)
}

// agentUpdateOnReconnect decides what an agent connecting on version means
// for its open update, at stage, sent to an agent on from ("" when not
// known). ok is false when it tells nothing (a reconnect during the update).
func agentUpdateOnReconnect(stage, from, version string) (newStage, status, failure string, ok bool) {
	switch {
	case from != "" && version != from:
		return AgentUpdateDone, JobCompleted, "", true
	case stage != AgentUpdateRestarting:
		return "", "", "", false
	case from == "":
		return AgentUpdateDone, JobCompleted, "", true
	default:
		return AgentUpdateFailed, JobFailed, "the agent restarted on version " + version + ", the one it was updating from", true
	}
}

func recordAgentUpdateStage(ctx context.Context, q AgentUpdateQueries, jobID, apiID, stage, status, failure string, patch map[string]any, now time.Time) error {
	b, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	var msg *string
	if failure != "" {
		msg = &failure
	}
	_, err = q.RecordAgentUpdateStage(ctx, db.RecordAgentUpdateStageParams{
		Patch:        b,
		Stage:        stage,
		At:           now.UTC().Format(time.RFC3339Nano),
		Status:       status,
		ErrorMessage: msg,
		JobID:        jobID,
		ApiID:        apiID,
		JobName:      AgentUpdateJobName,
	})
	return err
}

// NormalizeAgentVersion drops a leading "v" and surrounding spaces.
func NormalizeAgentVersion(v string) string {
	return strings.TrimPrefix(strings.TrimSpace(v), "v")
}
