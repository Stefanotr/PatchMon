package handler

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	hostctx "github.com/PatchMon/PatchMon/server-source-code/internal/context"
	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/hostscope"
	"github.com/PatchMon/PatchMon/server-source-code/internal/middleware"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// HostScopeHandler serves the host scope API (fork, see internal/hostscope).
type HostScopeHandler struct {
	scopes      *hostscope.Store
	permissions *store.PermissionsStore
	audits      *store.AuditLogsStore
	log         *slog.Logger
}

// NewHostScopeHandler returns the handler.
func NewHostScopeHandler(scopes *hostscope.Store, permissions *store.PermissionsStore, audits *store.AuditLogsStore, log *slog.Logger) *HostScopeHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HostScopeHandler{scopes: scopes, permissions: permissions, audits: audits, log: log}
}

// Me handles GET /host-scope/me: whether the caller is limited to some host
// groups and to which, so that the interface hides what they cannot do.
func (h *HostScopeHandler) Me(w http.ResponseWriter, r *http.Request) {
	groups, limited := database.HostScope(r.Context())
	if groups == nil {
		groups = []string{}
	}
	JSON(w, http.StatusOK, map[string]any{"limited": limited, "host_group_ids": groups})
}

// ListRoles handles GET /host-scope/roles: the limited roles, whether limits
// can be enforced on this database, and the permissions a limited role cannot
// hold.
func (h *HostScopeHandler) ListRoles(w http.ResponseWriter, r *http.Request) {
	roles, err := h.scopes.List(r.Context())
	if err != nil {
		h.log.Error("host scope: list roles", "error", err)
		Error(w, http.StatusInternalServerError, "Failed to load host access")
		return
	}
	st, err := h.scopes.Status(r.Context())
	if err != nil {
		h.log.Error("host scope: status", "error", err)
		st = hostscope.Status{Reason: "could not check the database"}
	}
	JSON(w, http.StatusOK, map[string]any{
		"roles":                    roles,
		"status":                   st,
		"incompatible_permissions": hostscope.IncompatiblePermissions,
	})
}

// UpdateRole handles PUT /host-scope/roles/{role} with {"limited": bool,
// "host_group_ids": [...]}. Giving a role every host back always works;
// limiting it needs scoping set up and a role without incompatible
// permissions.
func (h *HostScopeHandler) UpdateRole(w http.ResponseWriter, r *http.Request) {
	role := decodedURLParam(r, "role")
	if role == "" {
		Error(w, http.StatusBadRequest, "Role is required")
		return
	}
	var req struct {
		Limited      bool     `json:"limited"`
		HostGroupIDs []string `json:"host_group_ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		Error(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	p, err := h.permissions.GetByRole(r.Context(), role)
	if err != nil || p == nil {
		Error(w, http.StatusNotFound, "Role not found")
		return
	}
	actor, _ := r.Context().Value(middleware.UserIDKey).(string)
	if !req.Limited {
		if err := h.scopes.Unlimit(r.Context(), role); err != nil {
			h.log.Error("host scope: unlimit role", "role", role, "error", err)
			Error(w, http.StatusInternalServerError, "Failed to update host access")
			return
		}
		h.audit(r, actor, role, nil)
		JSON(w, http.StatusOK, map[string]any{"role": role, "limited": false, "host_group_ids": []string{}})
		return
	}
	if hostscope.FullAccessRoles[role] {
		Error(w, http.StatusBadRequest, "The "+role+" role always has every host")
		return
	}
	if bad := hostscope.IncompatibleHeld(p); len(bad) > 0 {
		Error(w, http.StatusBadRequest, "A role limited to some host groups cannot hold: "+strings.Join(bad, ", "))
		return
	}
	groups := []string{}
	for _, id := range req.HostGroupIDs {
		id = strings.TrimSpace(id)
		if id == "" || slices.Contains(groups, id) {
			continue
		}
		if strings.Contains(id, ",") {
			Error(w, http.StatusBadRequest, "Invalid host group id")
			return
		}
		groups = append(groups, id)
	}
	if len(groups) > hostscope.MaxGroups {
		Error(w, http.StatusBadRequest, "Too many host groups")
		return
	}
	missing, err := h.scopes.MissingGroups(r.Context(), groups)
	if err != nil {
		h.log.Error("host scope: check groups", "error", err)
		Error(w, http.StatusInternalServerError, "Failed to update host access")
		return
	}
	if len(missing) > 0 {
		Error(w, http.StatusBadRequest, "Unknown host group: "+strings.Join(missing, ", "))
		return
	}
	st, err := h.scopes.Status(r.Context())
	if err != nil || !st.Available {
		reason := st.Reason
		if err != nil {
			reason = "could not check the database"
		}
		Error(w, http.StatusConflict, "Host scoping is not available on this server: "+reason)
		return
	}
	if err := h.scopes.Limit(r.Context(), role, groups, actor); err != nil {
		h.log.Error("host scope: limit role", "role", role, "error", err)
		Error(w, http.StatusInternalServerError, "Failed to update host access")
		return
	}
	h.audit(r, actor, role, groups)
	JSON(w, http.StatusOK, map[string]any{"role": role, "limited": true, "host_group_ids": groups})
}

// audit records a change of a role's host access (groups nil: every host).
func (h *HostScopeHandler) audit(r *http.Request, actor, role string, groups []string) {
	if h.audits == nil {
		return
	}
	meta := map[string]any{"role": role, "limited": groups != nil}
	if groups != nil {
		meta["host_group_ids"] = groups
	}
	if err := h.audits.Record(r.Context(), newAuditEntry(r, "role_host_scope_update", actor, "", true, meta)); err != nil {
		h.log.Warn("host scope: audit", "error", err)
	}
}

// serveScopedSummary is GET /ws/status/summary for a user limited to some
// host groups: the connected agents of the hosts they can see, rather than
// the whole fleet's count held in memory.
func (h *WSStatusHandler) serveScopedSummary(w http.ResponseWriter, r *http.Request) {
	connected := h.registry.GetConnectedApiIDs(hostctx.TenantHostKey(r.Context()))
	visible, err := h.hosts.ListExistingApiIDs(r.Context(), connected)
	if err != nil {
		Error(w, http.StatusInternalServerError, "failed to load host status")
		return
	}
	JSON(w, http.StatusOK, map[string]int{"connected": len(visible)})
}
