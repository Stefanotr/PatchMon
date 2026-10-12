package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/models"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// Restart commands for a host. The agent must be connected: a restart is
// not queued for later, since nobody expects a host to go down hours after
// the button was pressed. The handler waits for the agent's answer, so the
// operator learns whether the restart was scheduled, and when, or why the
// agent refused it (a patch run in progress, a restart already scheduled).
//
// Every command is recorded in the host's Agent Activity (job_history), with
// who asked, the outcome and the restart time; so is every restart a patch
// run schedules (RecordRunRestart).
//
// The answer comes back on the server instance the agent is connected to.
// With several instances behind Redis, a command relayed to another one
// gets no answer here (202): the agent still carries it out.

// agentReplyTimeout bounds the wait for the agent's answer. Scheduling can
// take a few seconds (shutdown(8) on SysV init hosts is given five).
const agentReplyTimeout = 20 * time.Second

// Job names of restart records in job_history.
const (
	restartJobName       = "reboot"
	restartCancelJobName = "reboot_cancel"
)

type rebootReply struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	RebootAt string `json:"reboot_at"`
}

// RebootHost handles POST /hosts/{hostId}/reboot.
func (h *HostsHandler) RebootHost(w http.ResponseWriter, r *http.Request) {
	var body rebootRequest
	if err := decodeJSON(r, &body); err != nil && !errors.Is(err, io.EOF) {
		Error(w, http.StatusBadRequest, "Invalid JSON")
		return
	}
	opts, err := body.forCommand(time.Now())
	if err != nil {
		Error(w, http.StatusBadRequest, err.Error())
		return
	}
	details := map[string]any{}
	if opts.At != nil {
		details["at"] = opts.At.UTC().Format(time.RFC3339)
	} else {
		details["delay_minutes"] = opts.DelayMinutes
	}
	if opts.Message != "" {
		details["message"] = opts.Message
	}
	h.sendRebootCommand(w, r, restartJobName, map[string]any{"type": "reboot", "reboot": opts}, details, "Restart scheduled")
}

// CancelHostReboot handles POST /hosts/{hostId}/reboot/cancel.
func (h *HostsHandler) CancelHostReboot(w http.ResponseWriter, r *http.Request) {
	h.sendRebootCommand(w, r, restartCancelJobName, map[string]any{"type": "reboot_cancel"}, nil, "Scheduled restart cancelled")
}

func (h *HostsHandler) sendRebootCommand(w http.ResponseWriter, r *http.Request, job string, msg, details map[string]any, success string) {
	host, err := h.hosts.GetByID(r.Context(), chi.URLParam(r, "hostId"))
	if err != nil || host == nil {
		Error(w, http.StatusNotFound, "Host not found")
		return
	}
	requestID := uuid.NewString()
	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	log := slog.With("host_id", host.ID, "user_id", userID, "command", msg["type"])
	record := func(failure string, rebootAt string) {
		out := map[string]any{"source": "manual"}
		if who := h.userLabel(r.Context(), userID); who != "" {
			out["requested_by"] = who
		}
		for k, v := range details {
			out[k] = v
		}
		if rebootAt != "" {
			out["reboot_at"] = rebootAt
		}
		h.recordRestart(r.Context(), host, requestID, job, failure, out)
	}

	if h.registry == nil || !h.registry.IsConnected(host.ApiID) {
		record("the agent was not connected", "")
		Error(w, http.StatusConflict, "The agent is not connected")
		return
	}

	msg["request_id"] = requestID
	replies, done := agentReplies.expect(requestID, host.ApiID)
	defer done()
	if err := h.registry.SendJSON(host.ApiID, msg); err != nil {
		record("the command could not be sent to the agent", "")
		Error(w, http.StatusBadGateway, "Failed to send the command to the agent")
		return
	}

	select {
	case raw := <-replies:
		var reply rebootReply
		if err := json.Unmarshal(raw, &reply); err != nil {
			record("unreadable answer from the agent", "")
			Error(w, http.StatusBadGateway, "Unreadable answer from the agent")
			return
		}
		if !reply.OK {
			reason := clipReason(reply.Error)
			log.Info("hosts: restart command refused by the agent", "reason", reason)
			record("refused by the agent: "+reason, "")
			Error(w, http.StatusConflict, "The agent refused: "+reason)
			return
		}
		resp := map[string]any{"message": success}
		rebootAt := ""
		if at, err := time.Parse(time.RFC3339, reply.RebootAt); err == nil {
			rebootAt = at.UTC().Format(time.RFC3339)
			resp["reboot_at"] = rebootAt
		}
		log.Info("hosts: restart command done", "reboot_at", rebootAt)
		record("", rebootAt)
		JSON(w, http.StatusOK, resp)
	case <-time.After(agentReplyTimeout):
		log.Warn("hosts: no answer from the agent to a restart command")
		record("no answer from the agent in time; check the host", "")
		JSON(w, http.StatusAccepted, map[string]any{
			"message": "Sent to the agent, which did not answer in time: check the host. Agents older than this feature ignore restart commands.",
		})
	case <-r.Context().Done():
		record("the request ended before the agent answered", "")
	}
}

// userLabel names the user for the record: the username, or the ID when it
// cannot be read.
func (h *HostsHandler) userLabel(ctx context.Context, userID string) string {
	if userID == "" || h.db == nil {
		return userID
	}
	if u, err := h.db.DB(ctx).Queries.GetUserByID(ctx, userID); err == nil {
		return u.Username
	}
	return userID
}

// recordRestart stores a restart record. The request may already be over
// (client gone), so it runs on a context that cannot be cancelled but keeps
// the request's values, which select the tenant database.
func (h *HostsHandler) recordRestart(ctx context.Context, host *models.Host, jobID, job, failure string, out map[string]any) {
	if h.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := store.NewJobHistoryStore(h.db).RecordFinished(ctx, store.FinishedJob{
		JobID:     jobID,
		QueueName: queue.QueueAgentCommands,
		JobName:   job,
		HostID:    host.ID,
		ApiID:     host.ApiID,
		Failure:   failure,
		Output:    out,
	})
	if err != nil {
		slog.Warn("hosts: failed to record a restart in the job history", "host_id", host.ID, "error", err)
	}
}

// runIDPattern bounds the patch run ID an agent reports with a restart.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// runRestartEvent is what an agent reports when a patch run schedules a
// restart, or fails to.
type runRestartEvent struct {
	Action   string `json:"action"`
	RunID    string `json:"run_id"`
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	RebootAt string `json:"reboot_at"`
}

// parseRunRestart reads a run restart event; ok is false for any other
// message, or one with an invalid run ID.
func parseRunRestart(message []byte) (runID, failure string, out map[string]any, ok bool) {
	var ev runRestartEvent
	if err := json.Unmarshal(message, &ev); err != nil || ev.Action != "run" || !runIDPattern.MatchString(ev.RunID) {
		return "", "", nil, false
	}
	out = map[string]any{"source": "patch_run", "patch_run_id": ev.RunID}
	if !ev.OK {
		return ev.RunID, "the patch run could not schedule the restart: " + clipReason(ev.Error), out, true
	}
	if at, err := time.Parse(time.RFC3339, ev.RebootAt); err == nil {
		out["reboot_at"] = at.UTC().Format(time.RFC3339)
	}
	return ev.RunID, "", out, true
}

// RecordRunRestart returns the agent websocket hook that records the restarts
// patch runs schedule in the host's Agent Activity, next to the restart
// commands. Messages that are not run restarts are ignored.
func RecordRunRestart(hosts *store.HostsStore, jobs *store.JobHistoryStore) func(ctx context.Context, apiID string, message []byte) {
	return func(ctx context.Context, apiID string, message []byte) {
		runID, failure, out, ok := parseRunRestart(message)
		if !ok {
			return
		}
		// The agent connection may close right after the message: keep the
		// request's values (tenant database), not its cancellation.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		host, err := hosts.GetByApiID(ctx, apiID)
		if err != nil || host == nil {
			return
		}
		if err := jobs.RecordFinished(ctx, store.FinishedJob{
			JobID:     runID,
			QueueName: queue.QueueAgentCommands,
			JobName:   restartJobName,
			HostID:    host.ID,
			ApiID:     host.ApiID,
			Failure:   failure,
			Output:    out,
		}); err != nil {
			slog.Warn("hosts: failed to record a patch run restart", "host_id", host.ID, "error", err)
		}
	}
}

// clipReason bounds a reason relayed from the agent.
func clipReason(s string) string {
	const n = 300
	if s == "" {
		return "no reason given"
	}
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
