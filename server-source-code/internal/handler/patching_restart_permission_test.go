package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
)

const testHostUUID = "6f1c2d3e-4a5b-4c6d-8e7f-9a0b1c2d3e4f"

func denyRestart(*http.Request) bool { return false }

func forbiddenRestart(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != errRestartNotAllowed {
		t.Fatalf("error %q", body["error"])
	}
}

func TestTriggerWithRestartNeedsThePermission(t *testing.T) {
	// No stores: the refusal must come before any lookup.
	h := &PatchingHandler{restartAllowed: denyRestart}
	body := `{"host_id":"` + testHostUUID + `","patch_type":"patch_all","reboot":{"mode":"if_needed","delay_minutes":10}}`
	rec := httptest.NewRecorder()
	h.Trigger(rec, httptest.NewRequest(http.MethodPost, "/patching/trigger", strings.NewReader(body)))
	forbiddenRestart(t, rec)
}

func TestApproveWithRestartNeedsThePermission(t *testing.T) {
	h := &PatchingHandler{restartAllowed: denyRestart}
	req := httptest.NewRequest(http.MethodPost, "/patching/runs/x/approve", strings.NewReader(`{"reboot":{"mode":"always"}}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", testHostUUID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h.ApproveRun(rec, req)
	forbiddenRestart(t, rec)
}

func TestRefuseRestart(t *testing.T) {
	opts := &queue.RebootOptions{Mode: "always", DelayMinutes: 5}
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	allow := &PatchingHandler{restartAllowed: func(*http.Request) bool { return true }}
	deny := &PatchingHandler{restartAllowed: denyRestart}
	unwired := &PatchingHandler{}

	for name, tc := range map[string]struct {
		h       *PatchingHandler
		opts    *queue.RebootOptions
		refused bool
	}{
		"no restart, no permission":      {deny, nil, false},
		"no restart, nothing wired":      {unwired, nil, false},
		"restart with the permission":    {allow, opts, false},
		"restart without the permission": {deny, opts, true},
		"restart, nothing wired":         {unwired, opts, true},
	} {
		rec := httptest.NewRecorder()
		if got := tc.h.refuseRestart(rec, req, tc.opts); got != tc.refused {
			t.Errorf("%s: refused = %v", name, got)
		}
		if tc.refused {
			forbiddenRestart(t, rec)
		} else if rec.Body.Len() != 0 {
			t.Errorf("%s: wrote %s", name, rec.Body.String())
		}
	}
}
