package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/pgtime"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// Agent update records (store/agent_update.go). The update_agent worker
// leaves its row open once the command is sent, and the agent's stages, then
// its reconnection on the new version, close it. An update still open after
// AgentUpdateDeadline gets no news any more: the patch-run cleanup fails it.

// AgentUpdateDeadline bounds an agent update: the agent may take 30 minutes
// to download the binary, then checks and installs it and restarts.
const AgentUpdateDeadline = 45 * time.Minute

// msgAgentUpdateNoNews is recorded on the updates the sweep closes.
var msgAgentUpdateNoNews = fmt.Sprintf("no news from the agent within %d minutes (agents older than this server do not report how an update went: check the agent version)", int(AgentUpdateDeadline/time.Minute))

// closeStaleAgentUpdates fails the agent updates sent more than
// AgentUpdateDeadline before now and still open.
func closeStaleAgentUpdates(ctx context.Context, q staleCommandCloser, now time.Time) (int64, error) {
	return q.CloseStaleAgentCommands(ctx, db.CloseStaleAgentCommandsParams{
		ErrorMessage: msgAgentUpdateNoNews,
		QueueName:    store.AgentUpdateQueue,
		JobNames:     []string{store.AgentUpdateJobName},
		StaleBefore:  pgtime.From(now.Add(-AgentUpdateDeadline)),
	})
}
