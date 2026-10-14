package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtime"
)

// Host restart records. A restart command does not go through the queue:
// the server sends it straight to the connected agent, records it in
// job_history (QueueAgentCommands) under these job names before sending it,
// then records the agent's answer on the same row (handler/host_reboot.go).
// A row stays open (active, then waiting) while an answer may still come;
// the patch-run cleanup fails the rows still open RestartAnswerDeadline
// after the command was sent.
const (
	JobNameReboot       = "reboot"
	JobNameRebootCancel = "reboot_cancel"
)

// RestartJobNames returns the job names of host restart records.
func RestartJobNames() []string {
	return []string{JobNameReboot, JobNameRebootCancel}
}

// RestartAnswerDeadline bounds the wait for an agent's answer to a restart
// command. An agent answers within seconds; a record still open after this
// long gets no answer any more (agent disconnected or restarted, an agent
// older than restart commands, a command lost on the way).
const RestartAnswerDeadline = 10 * time.Minute

// msgRestartNoAnswer is recorded on the restart records the sweep closes.
var msgRestartNoAnswer = fmt.Sprintf("no answer from the agent within %d minutes", int(RestartAnswerDeadline/time.Minute))

// staleCommandCloser is the query the sweep runs (db.Queries).
type staleCommandCloser interface {
	CloseStaleAgentCommands(ctx context.Context, arg db.CloseStaleAgentCommandsParams) (int64, error)
}

// closeStaleRestartRecords fails the restart records sent more than
// RestartAnswerDeadline before now and still open: waiting for an answer
// that never came, or active after the server stopped mid-command.
func closeStaleRestartRecords(ctx context.Context, q staleCommandCloser, now time.Time) (int64, error) {
	return q.CloseStaleAgentCommands(ctx, db.CloseStaleAgentCommandsParams{
		ErrorMessage: msgRestartNoAnswer,
		QueueName:    QueueAgentCommands,
		JobNames:     RestartJobNames(),
		StaleBefore:  pgtime.From(now.Add(-RestartAnswerDeadline)),
	})
}

// sweepCommandRecords closes the stale restart and agent update records of
// one database, as part of the patch-run cleanup (every 10 minutes, every
// context). A failure is logged and leaves the patch-run sweep alone.
func (h *PatchRunCleanupHandler) sweepCommandRecords(ctx context.Context, d *database.DB, host string) {
	if d == nil {
		return
	}
	now := time.Now()
	for _, sweep := range []struct {
		what  string
		close func(context.Context, staleCommandCloser, time.Time) (int64, error)
	}{
		{"restart records", closeStaleRestartRecords},
		{"agent updates", closeStaleAgentUpdates},
	} {
		n, err := sweep.close(ctx, d.Queries, now)
		if err != nil {
			h.log.Warn("command record cleanup failed", "records", sweep.what, "host", host, "error", err)
			continue
		}
		if n > 0 {
			h.log.Info("command record cleanup: closed records without an answer", "records", sweep.what, "host", host, "count", n)
		}
	}
}
