package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
)

// fakeAgentUpdates is an agent's job_history as the agent update queries
// see it.
type fakeAgentUpdates struct {
	open     *db.GetOpenAgentUpdateRow
	stages   []db.RecordAgentUpdateStageParams
	inserted []db.InsertJobHistoryStartedParams
	lookups  []db.GetOpenAgentUpdateParams
}

func (f *fakeAgentUpdates) GetOpenAgentUpdate(_ context.Context, arg db.GetOpenAgentUpdateParams) (db.GetOpenAgentUpdateRow, error) {
	f.lookups = append(f.lookups, arg)
	if f.open == nil {
		return db.GetOpenAgentUpdateRow{}, pgx.ErrNoRows
	}
	return *f.open, nil
}

func (f *fakeAgentUpdates) RecordAgentUpdateStage(_ context.Context, arg db.RecordAgentUpdateStageParams) (int64, error) {
	f.stages = append(f.stages, arg)
	return 1, nil
}

func (f *fakeAgentUpdates) InsertJobHistoryStarted(_ context.Context, arg db.InsertJobHistoryStartedParams) error {
	f.inserted = append(f.inserted, arg)
	return nil
}

func openUpdate(jobID, output string) *db.GetOpenAgentUpdateRow {
	return &db.GetOpenAgentUpdateRow{JobID: jobID, Status: JobActive, Output: []byte(output)}
}

func patchOf(t *testing.T, p db.RecordAgentUpdateStageParams) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(p.Patch, &m); err != nil {
		t.Fatalf("patch is not JSON: %v", err)
	}
	return m
}

var updateNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.FixedZone("CEST", 2*3600))

func TestRecordAgentUpdateSent(t *testing.T) {
	f := &fakeAgentUpdates{}
	if err := RecordAgentUpdateSent(context.Background(), f, "job-1", "api-1", "v1.4.0", updateNow); err != nil {
		t.Fatal(err)
	}
	s := f.stages[0]
	if s.JobID != "job-1" || s.ApiID != "api-1" || s.JobName != AgentUpdateJobName || s.Status != JobActive || s.Stage != AgentUpdateSent {
		t.Fatalf("stage = %+v", s)
	}
	if s.At != "2026-10-14T13:00:00Z" {
		t.Errorf("at = %q, want UTC", s.At)
	}
	p := patchOf(t, s)
	if p["from_version"] != "1.4.0" || p["trigger"] != "server" || p["stage"] != AgentUpdateSent {
		t.Errorf("patch = %v", p)
	}
}

func TestRecordAgentUpdateProgressOnTheOpenUpdate(t *testing.T) {
	f := &fakeAgentUpdates{open: openUpdate("job-1", `{"stage":"sent"}`)}
	pct := 40
	err := RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1",
		AgentUpdateProgress{Stage: AgentUpdateDownloading, Percent: &pct, FromVersion: "1.4.0", ToVersion: "v1.5.0"}, updateNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.inserted) != 0 || len(f.stages) != 1 {
		t.Fatalf("inserted %d, stages %d", len(f.inserted), len(f.stages))
	}
	l := f.lookups[0]
	if l.ApiID != "api-1" || l.QueueName != AgentUpdateQueue || l.JobName != AgentUpdateJobName ||
		!l.CreatedAfter.Time.Equal(updateNow.UTC().Add(-AgentUpdateWindow)) {
		t.Errorf("lookup = %+v", l)
	}
	s := f.stages[0]
	p := patchOf(t, s)
	if s.JobID != "job-1" || s.Status != JobActive || s.ErrorMessage != nil {
		t.Errorf("stage = %+v", s)
	}
	if p["percent"] != float64(40) || p["to_version"] != "1.5.0" || p["from_version"] != "1.4.0" {
		t.Errorf("patch = %v", p)
	}
}

// Another stage clears the download percentage.
func TestRecordAgentUpdateProgressClearsThePercent(t *testing.T) {
	f := &fakeAgentUpdates{open: openUpdate("job-1", `{}`)}
	_ = RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1", AgentUpdateProgress{Stage: AgentUpdateVerifying}, updateNow)
	p := patchOf(t, f.stages[0])
	if v, ok := p["percent"]; !ok || v != nil {
		t.Errorf("percent = %v (present %v), want null", v, ok)
	}
}

func TestRecordAgentUpdateProgressEnds(t *testing.T) {
	cases := []struct {
		p           AgentUpdateProgress
		status, msg string
	}{
		{AgentUpdateProgress{Stage: AgentUpdateUpToDate}, JobCompleted, ""},
		{AgentUpdateProgress{Stage: AgentUpdateSkipped, Message: "a patch run is in progress"}, JobFailed, "update skipped: a patch run is in progress"},
		{AgentUpdateProgress{Stage: AgentUpdateFailed, Message: "binary hash mismatch"}, JobFailed, "binary hash mismatch"},
		{AgentUpdateProgress{Stage: AgentUpdateFailed}, JobFailed, "the agent reported a failure"},
	}
	for _, c := range cases {
		f := &fakeAgentUpdates{open: openUpdate("job-1", `{}`)}
		if err := RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1", c.p, updateNow); err != nil {
			t.Fatal(err)
		}
		s := f.stages[0]
		got := ""
		if s.ErrorMessage != nil {
			got = *s.ErrorMessage
		}
		if s.Status != c.status || got != c.msg {
			t.Errorf("%s: status %q message %q, want %q %q", c.p.Stage, s.Status, got, c.status, c.msg)
		}
	}
}

// An update the agent started on its own gets its row at the start of the
// download, and nothing else opens one.
func TestRecordAgentUpdateProgressWithoutAnOpenUpdate(t *testing.T) {
	f := &fakeAgentUpdates{}
	if err := RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1", AgentUpdateProgress{Stage: AgentUpdateDownloading, FromVersion: "1.4.0"}, updateNow); err != nil {
		t.Fatal(err)
	}
	if len(f.inserted) != 1 || len(f.stages) != 1 {
		t.Fatalf("inserted %d, stages %d", len(f.inserted), len(f.stages))
	}
	in := f.inserted[0]
	if in.QueueName != AgentUpdateQueue || in.JobName != AgentUpdateJobName || *in.ApiID != "api-1" || *in.HostID != "host-1" ||
		!strings.HasPrefix(in.JobID, "agent-update-") || string(in.Output) != `{"trigger":"agent"}` {
		t.Errorf("inserted = %+v", in)
	}
	if f.stages[0].JobID != in.JobID {
		t.Errorf("stage recorded on %q, not on the new row %q", f.stages[0].JobID, in.JobID)
	}

	pct := 50
	for _, p := range []AgentUpdateProgress{
		{Stage: AgentUpdateRestarting},
		{Stage: AgentUpdateDownloading, Percent: &pct},
		{Stage: AgentUpdateUpToDate},
		{Stage: AgentUpdateFailed, Message: "late"},
	} {
		f := &fakeAgentUpdates{}
		if err := RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1", p, updateNow); err != nil {
			t.Fatal(err)
		}
		if len(f.inserted) != 0 || len(f.stages) != 0 {
			t.Errorf("%+v opened or recorded an update", p)
		}
	}
}

func TestRecordAgentUpdateProgressRefusesUnknownStages(t *testing.T) {
	f := &fakeAgentUpdates{open: openUpdate("job-1", `{}`)}
	for _, stage := range []string{AgentUpdateSent, AgentUpdateDone, "rm -rf", ""} {
		if err := RecordAgentUpdateProgress(context.Background(), f, "api-1", "host-1", AgentUpdateProgress{Stage: stage}, updateNow); err == nil {
			t.Errorf("stage %q accepted", stage)
		}
	}
	if len(f.stages) != 0 {
		t.Fatal("an unknown stage was recorded")
	}
}

func TestRecordAgentUpdateConnected(t *testing.T) {
	cases := []struct {
		name, output, version string
		stage, status, msg    string
	}{
		{"new version", `{"stage":"restarting","from_version":"1.4.0"}`, "1.5.0", AgentUpdateDone, JobCompleted, ""},
		{"new version, older agent that said nothing", `{"stage":"sent","from_version":"v1.4.0"}`, "v1.5.0", AgentUpdateDone, JobCompleted, ""},
		{"back on the old version after restarting", `{"stage":"restarting","from_version":"1.4.0"}`, "1.4.0", AgentUpdateFailed, JobFailed, "the agent restarted on version 1.4.0, the one it was updating from"},
		{"version not known, restarted", `{"stage":"restarting"}`, "1.5.0", AgentUpdateDone, JobCompleted, ""},
		{"reconnect while downloading", `{"stage":"downloading","from_version":"1.4.0"}`, "1.4.0", "", "", ""},
		{"version not known, still downloading", `{"stage":"downloading"}`, "1.5.0", "", "", ""},
		{"output not JSON", `oops`, "1.5.0", "", "", ""},
	}
	for _, c := range cases {
		f := &fakeAgentUpdates{open: openUpdate("job-1", c.output)}
		if err := RecordAgentUpdateConnected(context.Background(), f, "api-1", c.version, updateNow); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.stage == "" {
			if len(f.stages) != 0 {
				t.Errorf("%s: recorded %+v, want nothing", c.name, f.stages[0])
			}
			continue
		}
		if len(f.stages) != 1 {
			t.Fatalf("%s: %d stages recorded", c.name, len(f.stages))
		}
		s := f.stages[0]
		got := ""
		if s.ErrorMessage != nil {
			got = *s.ErrorMessage
		}
		if s.Stage != c.stage || s.Status != c.status || got != c.msg || patchOf(t, s)["to_version"] != NormalizeAgentVersion(c.version) {
			t.Errorf("%s: %+v (message %q)", c.name, s, got)
		}
	}
}

func TestRecordAgentUpdateConnectedWithNothingToClose(t *testing.T) {
	f := &fakeAgentUpdates{}
	if err := RecordAgentUpdateConnected(context.Background(), f, "api-1", "1.5.0", updateNow); err != nil || len(f.stages) != 0 {
		t.Fatalf("err=%v stages=%d", err, len(f.stages))
	}
	// Older agents send no version: nothing is even looked up.
	f = &fakeAgentUpdates{open: openUpdate("job-1", `{"stage":"restarting"}`)}
	if err := RecordAgentUpdateConnected(context.Background(), f, "api-1", "", updateNow); err != nil || len(f.lookups) != 0 {
		t.Fatalf("err=%v lookups=%d", err, len(f.lookups))
	}
}
