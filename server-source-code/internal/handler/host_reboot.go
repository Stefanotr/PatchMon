package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
)

// Restart commands for a host. The agent must be connected: a restart is
// not queued for later, since nobody expects a host to go down hours after
// the button was pressed. The handler waits for the agent's answer, so the
// operator learns whether the restart was scheduled, and when, or why the
// agent refused it (a patch run in progress, a restart already scheduled).
//
// The answer comes back on the server instance the agent is connected to.
// With several instances behind Redis, a command relayed to another one
// gets no answer here (202): the agent still carries it out.

// agentReplyTimeout bounds the wait for the agent's answer. Scheduling can
// take a few seconds (shutdown(8) on SysV init hosts is given five).
const agentReplyTimeout = 20 * time.Second

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
	h.sendRebootCommand(w, r, map[string]any{"type": "reboot", "reboot": opts}, "Restart scheduled")
}

// CancelHostReboot handles POST /hosts/{hostId}/reboot/cancel.
func (h *HostsHandler) CancelHostReboot(w http.ResponseWriter, r *http.Request) {
	h.sendRebootCommand(w, r, map[string]any{"type": "reboot_cancel"}, "Scheduled restart cancelled")
}

func (h *HostsHandler) sendRebootCommand(w http.ResponseWriter, r *http.Request, msg map[string]any, success string) {
	host, err := h.hosts.GetByID(r.Context(), chi.URLParam(r, "hostId"))
	if err != nil || host == nil {
		Error(w, http.StatusNotFound, "Host not found")
		return
	}
	if h.registry == nil || !h.registry.IsConnected(host.ApiID) {
		Error(w, http.StatusConflict, "The agent is not connected")
		return
	}

	requestID := uuid.NewString()
	msg["request_id"] = requestID
	replies, done := agentReplies.expect(requestID, host.ApiID)
	defer done()
	if err := h.registry.SendJSON(host.ApiID, msg); err != nil {
		Error(w, http.StatusBadGateway, "Failed to send the command to the agent")
		return
	}
	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	log := slog.With("host_id", host.ID, "user_id", userID, "command", msg["type"])

	select {
	case raw := <-replies:
		var reply rebootReply
		if err := json.Unmarshal(raw, &reply); err != nil {
			Error(w, http.StatusBadGateway, "Unreadable answer from the agent")
			return
		}
		if !reply.OK {
			log.Info("hosts: restart command refused by the agent", "reason", reply.Error)
			Error(w, http.StatusConflict, "The agent refused: "+clipReason(reply.Error))
			return
		}
		resp := map[string]any{"message": success}
		if at, err := time.Parse(time.RFC3339, reply.RebootAt); err == nil {
			resp["reboot_at"] = at.UTC().Format(time.RFC3339)
		}
		log.Info("hosts: restart command done", "reboot_at", resp["reboot_at"])
		JSON(w, http.StatusOK, resp)
	case <-time.After(agentReplyTimeout):
		log.Warn("hosts: no answer from the agent to a restart command")
		JSON(w, http.StatusAccepted, map[string]any{
			"message": "Sent to the agent, which did not answer in time: check the host. Agents older than this feature ignore restart commands.",
		})
	case <-r.Context().Done():
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
