package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/handler"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// No database: RequireJSON answers first, and without a signed-in user the
// permission check stops the request before any lookup.
func TestRestartRoutesTakeJSONOnly(t *testing.T) {
	r := chi.NewRouter()
	registerRestartRoutes(r,
		store.NewPermissionsStore(nil),
		handler.NewHostsHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil),
		handler.NewPatchingHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil),
	)

	paths := []string{
		"/hosts/h1/reboot",
		"/hosts/h1/reboot/cancel",
		"/patching/trigger",
		"/patching/runs/6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f/approve",
	}
	for _, path := range paths {
		for _, contentType := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"reboot":{"mode":"always"}}`))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("POST %s with Content-Type %q: status %d, want 415", path, contentType, rec.Code)
			}
		}

		// JSON passes on to the permission check.
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("POST %s with JSON: status %d, want 401 from the permission check", path, rec.Code)
		}
	}
}
