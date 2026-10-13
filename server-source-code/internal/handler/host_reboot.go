package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// agent refused it (a patch run in progress, a restart already scheduled,
// remote restarts disabled on the host).
//
// Every command is recorded in the host's Agent Activity (job_history)
// before it is sent, with who asked and what (status active). The same row
// then gets the outcome: done, with the restart time; refused, with the
// agent's reason; not sent; or an unreadable answer. Without an answer within
// agentReplyTimeout the row stays open (status waiting): the agent may still
// carry the command out, and its late answer records the outcome
// (RecordRestartEvent). A row still open queue.RestartAnswerDeadline after
// the command was sent is failed by the patch-run cleanup. The restarts
// patch runs schedule are recorded too.
//
// The answer comes back on the server instance the agent is connected to.
// With several instances behind Redis, a command relayed to another one
// gets no answer here (202): the agent still carries it out, and its answer
// reaches that instance's hook, which records it on the same row.

// agentReplyTimeout bounds the wait for the agent's answer. Scheduling can
// take a few seconds (shutdown(8) on SysV init hosts is given five).
const agentReplyTimeout = 20 * time.Second

// Job names of restart records in job_history.
const (
	restartJobName       = queue.JobNameReboot
	restartCancelJobName = queue.JobNameRebootCancel
)

// Recorded while the outcome of a command is not known yet.
var (
	msgNoAnswerYet = fmt.Sprintf("no answer from the agent within %d s; the outcome is recorded if it answers later",
		int(agentReplyTimeout/time.Second))
	msgRequestEnded = "the request ended before the agent answered; the outcome is recorded if it answers later"
)

type rebootReply struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	RebootAt string `json:"reboot_at"`
}

// restartOutcome is what a restart record says about a command.
type restartOutcome struct {
	// status is store.JobCompleted, JobFailed or JobWaiting.
	status string
	// message is the failure, or why the outcome is still open.
	message string
	// rebootAt is when the agent scheduled the restart (RFC 3339, UTC).
	rebootAt string
	// refusal is the agent's reason, for the operator.
	refusal    string
	unreadable bool
}

func failedOutcome(message string) restartOutcome {
	return restartOutcome{status: store.JobFailed, message: message}
}

// commandReplyOutcome reads the agent's answer to a restart or cancel
// command.
func commandReplyOutcome(raw []byte) restartOutcome {
	var reply rebootReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		o := failedOutcome("unreadable answer from the agent")
		o.unreadable = true
		return o
	}
	if !reply.OK {
		reason := clipReason(reply.Error)
		o := failedOutcome("refused by the agent: " + reason)
		o.refusal = reason
		return o
	}
	o := restartOutcome{status: store.JobCompleted}
	if at, err := time.Parse(time.RFC3339, reply.RebootAt); err == nil {
		o.rebootAt = at.UTC().Format(time.RFC3339)
	}
	return o
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
	log := slog.With("host_id", host.ID, "user_id", userID, "command", msg["type"], "request_id", requestID)

	// The record comes first: a command that cannot be recorded is not sent.
	if err := h.startRestartRecord(r.Context(), host, requestID, job, userID, details); err != nil {
		log.Error("hosts: failed to record a restart command, not sent", "error", err)
		Error(w, http.StatusInternalServerError, "Failed to record the command, so it was not sent")
		return
	}
	finish := func(o restartOutcome) { h.finishRestartRecord(r.Context(), host, requestID, o) }

	if h.registry == nil || !h.registry.IsConnected(host.ApiID) {
		finish(failedOutcome("the agent was not connected"))
		Error(w, http.StatusConflict, "The agent is not connected")
		return
	}

	msg["request_id"] = requestID
	replies, done := agentReplies.expect(requestID, host.ApiID)
	if err := h.registry.SendJSON(host.ApiID, msg); err != nil {
		done()
		finish(failedOutcome("the command could not be sent to the agent"))
		Error(w, http.StatusBadGateway, "Failed to send the command to the agent")
		return
	}

	raw, answered := awaitReply(r.Context(), replies, done, agentReplyTimeout)
	if !answered {
		if r.Context().Err() != nil {
			finish(restartOutcome{status: store.JobWaiting, message: msgRequestEnded})
			return
		}
		log.Warn("hosts: no answer from the agent to a restart command in time")
		finish(restartOutcome{status: store.JobWaiting, message: msgNoAnswerYet})
		JSON(w, http.StatusAccepted, map[string]any{
			"message": "Sent to the agent, which did not answer in time: check the host. Agents older than this feature ignore restart commands.",
		})
		return
	}

	o := commandReplyOutcome(raw)
	finish(o)
	switch {
	case o.unreadable:
		Error(w, http.StatusBadGateway, "Unreadable answer from the agent")
	case o.status == store.JobFailed:
		log.Info("hosts: restart command refused by the agent", "reason", o.refusal)
		Error(w, http.StatusConflict, "The agent refused: "+o.refusal)
	default:
		log.Info("hosts: restart command done", "reboot_at", o.rebootAt)
		resp := map[string]any{"message": success}
		if o.rebootAt != "" {
			resp["reboot_at"] = o.rebootAt
		}
		JSON(w, http.StatusOK, resp)
	}
}

// awaitReply waits for the agent's answer, up to timeout or the end of the
// request. It stops waiting (done) before it returns, then takes an answer
// that came in meanwhile: from then on a late answer goes to the agent
// websocket hook (RecordRestartEvent), so none is lost.
func awaitReply(ctx context.Context, replies <-chan []byte, done func(), timeout time.Duration) ([]byte, bool) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case raw := <-replies:
		done()
		return raw, true
	case <-timer.C:
	case <-ctx.Done():
	}
	done()
	select {
	case raw := <-replies:
		return raw, true
	default:
		return nil, false
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

// startRestartRecord stores the record of a command about to be sent, with
// who asked and what.
func (h *HostsHandler) startRestartRecord(ctx context.Context, host *models.Host, jobID, job, userID string, details map[string]any) error {
	if h.db == nil {
		return errors.New("no database")
	}
	out := map[string]any{"source": "manual"}
	if who := h.userLabel(ctx, userID); who != "" {
		out["requested_by"] = who
	}
	for k, v := range details {
		out[k] = v
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return store.NewJobHistoryStore(h.db).RecordStarted(ctx, store.StartedJob{
		JobID:     jobID,
		QueueName: queue.QueueAgentCommands,
		JobName:   job,
		HostID:    host.ID,
		ApiID:     host.ApiID,
		Output:    out,
	})
}

// finishRestartRecord stores the outcome of a command on its record. The
// request may already be over (client gone), so it runs on a context that
// cannot be cancelled but keeps the request's values, which select the
// tenant database.
func (h *HostsHandler) finishRestartRecord(ctx context.Context, host *models.Host, jobID string, o restartOutcome) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	changed, err := recordCommandOutcome(ctx, store.NewJobHistoryStore(h.db), host.ApiID, jobID, o)
	switch {
	case err != nil:
		slog.Warn("hosts: failed to record the outcome of a restart command", "host_id", host.ID, "request_id", jobID, "error", err)
	case !changed:
		// A late answer, handled by the hook, already closed it.
		slog.Debug("hosts: restart record already closed", "host_id", host.ID, "request_id", jobID)
	}
}

// restartJournal is the part of the job history restart records use.
type restartJournal interface {
	RecordFinished(ctx context.Context, j store.FinishedJob) error
	RecordOutcomeOf(ctx context.Context, o store.CommandOutcome) (bool, error)
}

// recordCommandOutcome writes o on the record of the command jobID sent to
// apiID, if it is still open; changed reports whether it was.
func recordCommandOutcome(ctx context.Context, jobs restartJournal, apiID, jobID string, o restartOutcome) (bool, error) {
	var out map[string]any
	if o.rebootAt != "" {
		out = map[string]any{"reboot_at": o.rebootAt}
	}
	return jobs.RecordOutcomeOf(ctx, store.CommandOutcome{
		JobID:     jobID,
		ApiID:     apiID,
		QueueName: queue.QueueAgentCommands,
		JobNames:  queue.RestartJobNames(),
		Status:    o.status,
		Message:   o.message,
		Output:    out,
	})
}

// runIDPattern bounds the patch run ID an agent reports with a restart.
var runIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// requestIDPattern matches the request IDs restart commands carry (UUIDs).
var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// runRestartEvent is what an agent reports when a patch run schedules a
// restart, skips the one it was asked for, or fails to schedule it.
type runRestartEvent struct {
	Action   string `json:"action"`
	RunID    string `json:"run_id"`
	OK       bool   `json:"ok"`
	Skipped  bool   `json:"skipped"`
	Error    string `json:"error"`
	RebootAt string `json:"reboot_at"`
}

// parseRunRestart reads a run restart event; ok is false for any other
// message, or one with an invalid run ID. A skipped restart is a failure
// even if the event also says ok: the history must not claim a restart the
// agent did not schedule.
func parseRunRestart(message []byte) (runID, failure string, out map[string]any, ok bool) {
	var ev runRestartEvent
	if err := json.Unmarshal(message, &ev); err != nil || ev.Action != "run" || !runIDPattern.MatchString(ev.RunID) {
		return "", "", nil, false
	}
	out = map[string]any{"source": "patch_run", "patch_run_id": ev.RunID}
	switch {
	case ev.Skipped:
		return ev.RunID, "restart skipped: " + clipReason(ev.Error), out, true
	case !ev.OK:
		return ev.RunID, "the patch run could not schedule the restart: " + clipReason(ev.Error), out, true
	}
	if at, err := time.Parse(time.RFC3339, ev.RebootAt); err == nil {
		out["reboot_at"] = at.UTC().Format(time.RFC3339)
	}
	return ev.RunID, "", out, true
}

// parseLateCommandReply reads an answer to a restart command that is no
// longer awaited (an action other than "run", with a request ID); ok is
// false for any other message.
func parseLateCommandReply(message []byte) (requestID string, o restartOutcome, ok bool) {
	var head struct {
		Action    string `json:"action"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(message, &head); err != nil || head.Action == "run" || !requestIDPattern.MatchString(head.RequestID) {
		return "", restartOutcome{}, false
	}
	return head.RequestID, commandReplyOutcome(message), true
}

// hostByAPIID finds the host an agent connection belongs to.
type hostByAPIID interface {
	GetByApiID(ctx context.Context, apiID string) (*models.Host, error)
}

// RecordRestartEvent returns the agent websocket hook for the reboot_result
// messages no restart command is waiting for, recorded in the host's Agent
// Activity: the restart a patch run scheduled, skipped or failed to schedule
// (a new record), and a late answer to a restart command (the outcome, on
// the command's record). Other messages are ignored.
func RecordRestartEvent(hosts *store.HostsStore, jobs *store.JobHistoryStore) func(ctx context.Context, apiID string, message []byte) {
	return restartEventRecorder(hosts, jobs)
}

func restartEventRecorder(hosts hostByAPIID, jobs restartJournal) func(ctx context.Context, apiID string, message []byte) {
	return func(ctx context.Context, apiID string, message []byte) {
		// The agent connection may close right after the message: keep the
		// request's values (tenant database), not its cancellation.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if runID, failure, out, ok := parseRunRestart(message); ok {
			recordRunRestart(ctx, hosts, jobs, apiID, runID, failure, out)
			return
		}
		requestID, o, ok := parseLateCommandReply(message)
		if !ok {
			return
		}
		// apiID is the connection's own: an agent only ever completes the
		// records of commands sent to its host.
		changed, err := recordCommandOutcome(ctx, jobs, apiID, requestID, o)
		switch {
		case err != nil:
			slog.Warn("hosts: failed to record a late answer to a restart command", "api_id", apiID, "request_id", requestID, "error", err)
		case !changed:
			slog.Debug("hosts: late restart answer matches no open record", "api_id", apiID, "request_id", requestID)
		default:
			slog.Info("hosts: late answer to a restart command recorded", "api_id", apiID, "request_id", requestID, "status", o.status)
		}
	}
}

// recordRunRestart records a restart a patch run scheduled, skipped or
// failed to schedule.
func recordRunRestart(ctx context.Context, hosts hostByAPIID, jobs restartJournal, apiID, runID, failure string, out map[string]any) {
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
