package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestParseRemoteRestartHeader(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want string
	}{
		{"enabled", "true"},
		{" Enabled ", "true"},
		{"disabled", "false"},
		{"DISABLED", "false"},
		{"", "nil"},
		{"yes", "nil"},
		{"true", "nil"},
	} {
		got := "nil"
		if v := ParseRemoteRestartHeader(tt.in); v != nil {
			got = map[bool]string{true: "true", false: "false"}[*v]
		}
		if got != tt.want {
			t.Errorf("ParseRemoteRestartHeader(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

type fakeRemoteRestartStore struct {
	recorded map[string]*bool
	values   map[string]bool
	err      error
}

func (f *fakeRemoteRestartStore) Record(_ context.Context, hostID string, enabled *bool) error {
	if f.recorded == nil {
		f.recorded = map[string]*bool{}
	}
	f.recorded[hostID] = enabled
	return f.err
}

func (f *fakeRemoteRestartStore) Get(_ context.Context, hostID string) (*bool, error) {
	if f.err != nil {
		return nil, f.err
	}
	v, ok := f.values[hostID]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (f *fakeRemoteRestartStore) All(context.Context) (map[string]bool, error) {
	return f.values, f.err
}

func TestRecordRemoteRestartOptIn(t *testing.T) {
	s := &fakeRemoteRestartStore{}
	record := RecordRemoteRestartOptIn(s)
	off := false
	record(context.Background(), "h1", &off)
	record(context.Background(), "h2", nil)
	if v, ok := s.recorded["h1"]; !ok || v == nil || *v {
		t.Errorf("h1 recorded %v, want false", v)
	}
	if v, ok := s.recorded["h2"]; !ok || v != nil {
		t.Errorf("h2 recorded %v, want nil (forget)", v)
	}

	// A failed write is only logged.
	s.err = errors.New("db down")
	record(context.Background(), "h3", &off)
}

func serveRemoteRestart(t *testing.T, s remoteRestartReader, path string) (int, map[string]any) {
	t.Helper()
	h := NewRemoteRestartHandler(s)
	r := chi.NewRouter()
	r.Get("/hosts/remote-restart", h.List)
	r.Get("/hosts/{hostId}/remote-restart", h.Get)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v (%s)", path, err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func TestRemoteRestartEndpoints(t *testing.T) {
	s := &fakeRemoteRestartStore{values: map[string]bool{"on": true, "off": false}}

	for host, want := range map[string]any{"on": true, "off": false, "unknown": nil} {
		code, body := serveRemoteRestart(t, s, "/hosts/"+host+"/remote-restart")
		if code != http.StatusOK || body["enabled"] != want {
			t.Errorf("host %s: %d %v, want enabled %v", host, code, body, want)
		}
	}

	code, body := serveRemoteRestart(t, s, "/hosts/remote-restart")
	hosts, _ := body["hosts"].(map[string]any)
	if code != http.StatusOK || hosts["on"] != true || hosts["off"] != false || len(hosts) != 2 {
		t.Errorf("list: %d %v", code, body)
	}

	s.err = errors.New("db down")
	if code, _ := serveRemoteRestart(t, s, "/hosts/on/remote-restart"); code != http.StatusInternalServerError {
		t.Errorf("failed read: status %d, want 500", code)
	}
}
