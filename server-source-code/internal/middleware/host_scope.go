package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/PatchMon/PatchMon/server-source-code/internal/database"
	"github.com/PatchMon/PatchMon/server-source-code/internal/hostscope"
	"github.com/go-chi/chi/v5"
)

// Host scoping (fork, see internal/hostscope).

// HostScopeLimited reports whether the request is limited to some host groups.
func HostScopeLimited(r *http.Request) bool {
	_, limited := database.HostScope(r.Context())
	return limited
}

// HostScope limits the requests of users whose role is limited to some host
// groups, for every handler after it; it goes right after authentication. It
// fails closed: a limited user is refused when the limit cannot be read or
// cannot be enforced on the database.
func HostScope(scopes *hostscope.Store, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, _ := r.Context().Value(UserRoleKey).(string)
			if role == "" {
				next.ServeHTTP(w, r)
				return
			}
			groups, limited, err := scopes.ForRole(r.Context(), role)
			if err != nil {
				log.Error("host scope: read role scope", "role", role, "error", err)
				writeJSONError(w, http.StatusServiceUnavailable, "Could not check which hosts you can access")
				return
			}
			if !limited {
				next.ServeHTTP(w, r)
				return
			}
			st, err := scopes.Status(r.Context())
			if err != nil || !st.Available {
				log.Error("host scope: limited user refused, scoping is not set up on this database",
					"role", role, "reason", st.Reason, "error", err)
				writeJSONError(w, http.StatusServiceUnavailable, "Access limited to host groups is not available on this server; ask an administrator")
				return
			}
			next.ServeHTTP(w, r.WithContext(database.WithHostScope(r.Context(), groups)))
		})
	}
}

// RequireAllHosts refuses a route to limited users: it changes or shows what
// spans every host (host groups, policies, shared catalogs, job queues).
func RequireAllHosts(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if HostScopeLimited(r) {
			writeJSONError(w, http.StatusForbidden, "Not available to users limited to some host groups")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GuardLimitedRole refuses to give a limited role a permission it cannot hold
// (PUT /permissions/roles/{role}). The body is left for the handler.
func GuardLimitedRole(scopes *hostscope.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role, err := url.PathUnescape(chi.URLParam(r, "role"))
			if err != nil || role == "" {
				next.ServeHTTP(w, r)
				return
			}
			_, limited, err := scopes.ForRole(r.Context(), role)
			if err != nil {
				writeJSONError(w, http.StatusServiceUnavailable, "Could not check the role's host access")
				return
			}
			if !limited {
				next.ServeHTTP(w, r)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			// Decoded like the handler does (first JSON value, trailing data
			// ignored), and refused when that fails: never let through a body
			// this check could not read.
			var req map[string]any
			if json.NewDecoder(bytes.NewReader(body)).Decode(&req) != nil {
				writeJSONError(w, http.StatusBadRequest, "Invalid request body")
				return
			}
			// encoding/json matches field names case-insensitively, so
			// "CAN_MANAGE_USERS" sets can_manage_users: compare the same way.
			var bad []string
			for _, perm := range hostscope.IncompatiblePermissions {
				for key, value := range req {
					if v, ok := value.(bool); ok && v && strings.EqualFold(key, perm) {
						bad = append(bad, perm)
						break
					}
				}
			}
			if len(bad) > 0 {
				writeJSONError(w, http.StatusBadRequest,
					"This role is limited to some host groups and cannot hold: "+strings.Join(bad, ", "))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireVisibleHost answers 404, to a limited user, for a host outside their
// groups (URL parameter param), before the handler runs. Most handlers load
// the host from the database, which row security already filters; this also
// covers those that read job queues, Redis or the agent registry by host id.
func RequireVisibleHost(scopes *hostscope.Store, param string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !HostScopeLimited(r) {
				next.ServeHTTP(w, r)
				return
			}
			id, err := url.PathUnescape(chi.URLParam(r, param))
			if err != nil || id == "" {
				writeJSONError(w, http.StatusNotFound, "Host not found")
				return
			}
			ok, err := scopes.HostVisible(r.Context(), id)
			if err != nil {
				writeJSONError(w, http.StatusInternalServerError, "Failed to check host access")
				return
			}
			if !ok {
				writeJSONError(w, http.StatusNotFound, "Host not found")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
