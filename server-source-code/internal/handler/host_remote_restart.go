package handler

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// RemoteRestartHeader is the header in which an agent announces, on every
// WebSocket connection, whether its host allows remote restarts
// (remote-restart-enabled in its config.yml): "enabled" or "disabled".
// Older agents send nothing. The agent alone decides and refuses restarts
// when they are off; the server only keeps the answer so that the UI can
// grey the restart out on hosts that would refuse it.
const RemoteRestartHeader = "X-PatchMon-Remote-Restart"

// ParseRemoteRestartHeader reads the announcement: nil when the agent said
// nothing, or something it should not have.
func ParseRemoteRestartHeader(v string) *bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "enabled":
		on := true
		return &on
	case "disabled":
		off := false
		return &off
	}
	return nil
}

// OnRemoteRestartOptIn receives what a connecting agent announced.
type OnRemoteRestartOptIn func(ctx context.Context, hostID string, enabled *bool)

// WithOnRemoteRestartOptIn sets the callback for the remote restart
// announcement of each connecting agent.
func WithOnRemoteRestartOptIn(f OnRemoteRestartOptIn) AgentWSHandlerOption {
	return func(h *AgentWSHandler) {
		h.onRemoteRestartOptIn = f
	}
}

type remoteRestartRecorder interface {
	Record(ctx context.Context, hostID string, enabled *bool) error
}

// remoteRestartRecordTimeout bounds the write made before the agent's
// connection is upgraded.
const remoteRestartRecordTimeout = 3 * time.Second

// RecordRemoteRestartOptIn stores each announcement. A failure is logged and
// the connection goes on: the agent still enforces its own setting.
func RecordRemoteRestartOptIn(s remoteRestartRecorder) OnRemoteRestartOptIn {
	return func(ctx context.Context, hostID string, enabled *bool) {
		ctx, cancel := context.WithTimeout(ctx, remoteRestartRecordTimeout)
		defer cancel()
		if err := s.Record(ctx, hostID, enabled); err != nil {
			slog.Warn("remote restart opt-in not recorded", "host_id", hostID, "error", err)
		}
	}
}

type remoteRestartReader interface {
	Get(ctx context.Context, hostID string) (*bool, error)
	All(ctx context.Context) (map[string]bool, error)
}

// RemoteRestartHandler tells the UI which hosts allow remote restarts.
type RemoteRestartHandler struct {
	store remoteRestartReader
}

// NewRemoteRestartHandler creates the handler.
func NewRemoteRestartHandler(store remoteRestartReader) *RemoteRestartHandler {
	return &RemoteRestartHandler{store: store}
}

// Get handles GET /hosts/{hostId}/remote-restart: {"enabled": true, false or
// null}, null when the host's agent has not said.
func (h *RemoteRestartHandler) Get(w http.ResponseWriter, r *http.Request) {
	enabled, err := h.store.Get(r.Context(), chi.URLParam(r, "hostId"))
	if err != nil {
		Error(w, http.StatusInternalServerError, "Failed to read the host's remote restart setting")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

// List handles GET /hosts/remote-restart: {"hosts": {host ID: true or
// false}}, for the hosts whose agent has said.
func (h *RemoteRestartHandler) List(w http.ResponseWriter, r *http.Request) {
	all, err := h.store.All(r.Context())
	if err != nil {
		Error(w, http.StatusInternalServerError, "Failed to read the hosts' remote restart settings")
		return
	}
	JSON(w, http.StatusOK, map[string]any{"hosts": all})
}
