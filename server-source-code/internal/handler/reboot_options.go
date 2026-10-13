package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// Restart options, for a patch run (the wizard's Timing step) and for the
// restart command on a host. The agent bounds them again
// (agent internal/reboot), and only ever hands them to the operating
// system's own scheduler with an argument vector.

// The limits match the agent's (internal/reboot): it would refuse anything
// beyond them, failing a run that patched the host fine.
const (
	// rebootMinDelayMinutes leaves a run time to send its final status and
	// its inventory report before the host goes down.
	rebootMinDelayMinutes = 5
	rebootMaxDelayMinutes = 7 * 24 * 60
	rebootMaxAhead        = 7 * 24 * time.Hour
	rebootMaxMessageBytes = 200
)

// rebootRequest is the restart part of a request body.
type rebootRequest struct {
	// Mode is never (or empty), if_needed or always. The restart command
	// ignores it.
	Mode string `json:"mode"`
	// DelayMinutes after the run (or the command), unless At is set.
	DelayMinutes *int `json:"delay_minutes"`
	// At is an absolute time (RFC 3339); the browser turns a time of day
	// into the next occurrence in the user's own time zone.
	At      string `json:"at"`
	Message string `json:"message"`
}

// forRun validates the options of a patch run. It returns nil when the run
// does not restart the host.
func (r *rebootRequest) forRun(now time.Time) (*queue.RebootOptions, error) {
	if r == nil {
		return nil, nil
	}
	switch r.Mode {
	case "", "never":
		return nil, nil
	case "if_needed", "always":
	default:
		return nil, fmt.Errorf("reboot.mode must be never, if_needed or always")
	}
	return r.resolve(r.Mode, now)
}

// forCommand validates the options of a restart command.
func (r *rebootRequest) forCommand(now time.Time) (*queue.RebootOptions, error) {
	if r == nil {
		r = &rebootRequest{}
	}
	return r.resolve("always", now)
}

func (r *rebootRequest) resolve(mode string, now time.Time) (*queue.RebootOptions, error) {
	opts := &queue.RebootOptions{Mode: mode, DelayMinutes: 5}
	if r.DelayMinutes != nil {
		d := *r.DelayMinutes
		if d < rebootMinDelayMinutes || d > rebootMaxDelayMinutes {
			return nil, fmt.Errorf("reboot.delay_minutes must be between %d and %d", rebootMinDelayMinutes, rebootMaxDelayMinutes)
		}
		opts.DelayMinutes = d
	}
	if r.At != "" {
		at, err := time.Parse(time.RFC3339, r.At)
		if err != nil {
			return nil, errors.New("reboot.at must be an RFC 3339 time")
		}
		if at.Before(now.Add(-time.Minute)) || at.After(now.Add(rebootMaxAhead)) {
			return nil, fmt.Errorf("reboot.at must be in the future, at most %d days ahead", int(rebootMaxAhead/(24*time.Hour)))
		}
		utc := at.UTC()
		opts.At, opts.DelayMinutes = &utc, 0
	}
	msg := strings.TrimSpace(r.Message)
	if len(msg) > rebootMaxMessageBytes || !utf8.ValidString(msg) || strings.IndexFunc(msg, unicode.IsControl) >= 0 {
		return nil, fmt.Errorf("reboot.message must be one line of at most %d bytes", rebootMaxMessageBytes)
	}
	opts.Message = msg
	return opts, nil
}

// snapshotWithReboot records a run's restart options in its policy snapshot,
// where the run detail page reads them.
func snapshotWithReboot(snapshot []byte, opts *queue.RebootOptions) []byte {
	if opts == nil {
		return snapshot
	}
	m := map[string]any{}
	_ = json.Unmarshal(snapshot, &m)
	m["reboot"] = opts
	b, err := json.Marshal(m)
	if err != nil {
		return snapshot
	}
	return b
}

// permRestartHosts is the permission to restart hosts: with the restart
// command, and with a patch run that restarts the host once it is over.
const permRestartHosts = "can_restart_hosts"

// errRestartNotAllowed answers a patch run that asks for a restart the user
// may not order.
const errRestartNotAllowed = "Restarting hosts requires the can_restart_hosts permission: submit the run without a restart"

// WithRestartPermission makes a patch run that restarts the host require
// can_restart_hosts, on top of can_manage_patching.
func (h *PatchingHandler) WithRestartPermission(permissions *store.PermissionsStore) *PatchingHandler {
	h.restartAllowed = func(r *http.Request) bool {
		return middleware.RoleAllows(r, permissions, permRestartHosts)
	}
	return h
}

// refuseRestart answers 403 when the run asks for a restart (opts set) and
// the user may not order one; it reports whether it did. Runs without a
// restart are not concerned. Without a permission check wired, a restart is
// refused.
func (h *PatchingHandler) refuseRestart(w http.ResponseWriter, r *http.Request, opts *queue.RebootOptions) bool {
	if opts == nil || (h.restartAllowed != nil && h.restartAllowed(r)) {
		return false
	}
	JSON(w, http.StatusForbidden, map[string]string{"error": errRestartNotAllowed})
	return true
}
