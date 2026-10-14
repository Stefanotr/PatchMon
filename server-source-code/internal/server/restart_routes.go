package server

import (
	"github.com/go-chi/chi/v5"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/handler"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// registerRestartRoutes registers every route that can order a host
// restart, all part of the patching module:
//
//   - the restart command and its cancel, which need can_restart_hosts:
//     restarting a host is a permission of its own, apart from patching and
//     from editing host records (separation of duties);
//   - the patch runs that may restart the host once they are over (trigger,
//     approval), which need can_manage_patching, plus can_restart_hosts when
//     they ask for a restart (checked by the handlers, from the body).
//
// They all take JSON only (middleware.RequireJSON), checked first: a
// cross-site form or simple request cannot send JSON without a CORS
// preflight, so another site cannot fire them with the user's cookies. A
// new route that can order a restart belongs here.
func registerRestartRoutes(r chi.Router, permissions *store.PermissionsStore, hosts *handler.HostsHandler, patching *handler.PatchingHandler) {
	jsonOnly := r.With(middleware.RequireJSON)
	restart := jsonOnly.With(middleware.RequirePermission("can_restart_hosts", permissions), hostctx.RequireModule("patching"))
	restart.Post("/hosts/{hostId}/reboot", hosts.RebootHost)
	restart.Post("/hosts/{hostId}/reboot/cancel", hosts.CancelHostReboot)

	patchRun := jsonOnly.With(middleware.RequirePermission("can_manage_patching", permissions), hostctx.RequireModule("patching"))
	patchRun.Post("/patching/trigger", patching.Trigger)
	patchRun.Post("/patching/runs/{id}/approve", patching.ApproveRun)
}

// registerRemoteRestartRoutes registers the routes that tell the UI which
// hosts allow remote restarts, read-only: whoever sees the hosts may know.
func registerRemoteRestartRoutes(r chi.Router, permissions *store.PermissionsStore, h *handler.RemoteRestartHandler) {
	read := r.With(middleware.RequirePermission("can_view_hosts", permissions), hostctx.RequireModule("patching"))
	read.Get("/hosts/remote-restart", h.List)
	read.Get("/hosts/{hostId}/remote-restart", h.Get)
}
