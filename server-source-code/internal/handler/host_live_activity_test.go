package handler

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/queue"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

func ts(t time.Time) pgtype.Timestamp { return pgtype.Timestamp{Time: t, Valid: true} }

func TestComplianceProgressTracker(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	tr := NewComplianceProgressTracker()
	tr.now = func() time.Time { return now }

	tr.Record("api-1", []byte(`{"type":"compliance_scan_progress","phase":"started","profile_name":"CIS L1","message":"Initializing","progress":5}`))
	started := now
	now = now.Add(2 * time.Minute)
	tr.Record("api-1", []byte(`{"phase":"evaluating","profile_name":"CIS L1","message":"Running OpenSCAP","progress":15.9}`))
	p, ok := tr.Get("api-1")
	if !ok || p.Phase != "evaluating" || p.Percent != 15 || !p.StartedAt.Equal(started) || p.Finished() {
		t.Fatalf("progress = %+v, ok %v", p, ok)
	}

	// Unknown phases and other agents change nothing.
	tr.Record("api-1", []byte(`{"phase":"pwned"}`))
	if p, _ := tr.Get("api-1"); p.Phase != "evaluating" {
		t.Fatalf("unknown phase recorded: %+v", p)
	}
	if _, ok := tr.Get("api-2"); ok {
		t.Fatal("another agent has a scan")
	}

	// A scan with no news for too long is no longer shown.
	now = now.Add(complianceProgressStale + time.Second)
	if _, ok := tr.Get("api-1"); ok {
		t.Fatal("stale scan still shown")
	}

	// An end is shown a moment, then dropped.
	tr.Record("api-1", []byte(`{"phase":"completed","profile_name":"CIS L1","progress":100}`))
	if p, ok := tr.Get("api-1"); !ok || !p.Finished() {
		t.Fatalf("finished scan = %+v, ok %v", p, ok)
	}
	now = now.Add(liveFinishedWindow + time.Second)
	if _, ok := tr.Get("api-1"); ok {
		t.Fatal("finished scan shown too long")
	}
}

func TestLiveTasksFromJobsReadsTheAgentUpdate(t *testing.T) {
	created := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	fail := "binary hash mismatch"
	rows := []db.ListLiveJobHistoryRow{
		{
			JobID: "job-1", JobName: store.AgentUpdateJobName, Status: "active", AttemptNumber: 1,
			Output:    []byte(`{"stage":"downloading","percent":40,"from_version":"1.4.0","to_version":"1.5.0","trigger":"server","stages":{"sent":"2026-10-14T13:00:00Z","downloading":"2026-10-14T13:00:02Z"}}`),
			CreatedAt: ts(created), UpdatedAt: ts(created.Add(3 * time.Second)),
		},
		{JobID: "job-2", JobName: "update_agent", Status: "failed", ErrorMessage: &fail, Output: []byte(`{"stage":"failed","message":"binary hash mismatch"}`), CreatedAt: ts(created), CompletedAt: ts(created.Add(time.Minute))},
		{JobID: "job-3", JobName: "report_now", Status: "completed", Output: []byte(`{"stage":"ignored"}`), CreatedAt: ts(created), CompletedAt: ts(created)},
	}
	tasks := liveTasksFromJobs(rows, created.Add(2*time.Minute))
	u := tasks[0]
	if u.Kind != liveKindJob || u.Finished || u.Stage != store.AgentUpdateDownloading || u.Percent == nil || *u.Percent != 40 ||
		u.FromVersion != "1.4.0" || u.ToVersion != "1.5.0" || u.Stages["downloading"] != "2026-10-14T13:00:02Z" {
		t.Fatalf("update = %+v", u)
	}
	if f := tasks[1]; !f.Finished || f.Error != fail || f.Message != "" || f.FinishedAt == nil {
		t.Fatalf("failed update = %+v", f)
	}
	if r := tasks[2]; r.Stage != "" || !r.Finished {
		t.Fatalf("other job = %+v: only agent updates have stages", r)
	}
}

func TestLiveTasksFromQueueSkipsWhatAJobRowShows(t *testing.T) {
	now := time.Now()
	rows := []queue.HostJobRow{
		{JobID: "job-1", JobName: "update_agent", Status: "active", CreatedAt: &now},
		{JobID: "q-1", JobName: "report_now", Status: "waiting", CreatedAt: &now},
		{JobID: "q-2", JobName: "run_scan", Status: "delayed", AttemptNumber: 2, CreatedAt: &now},
		{JobID: "q-3", JobName: "report_now", Status: "completed", CreatedAt: &now},
		{JobID: "q-4", JobName: "report_now", Status: "failed", CreatedAt: &now},
	}
	rows = append(rows, queue.HostJobRow{JobID: "q-5", JobName: queue.TypeRunPatch, Status: "waiting", CreatedAt: &now})
	tasks := liveTasksFromQueue(rows, []db.ListLiveJobHistoryRow{{JobID: "job-1"}}, map[string]bool{queue.TypeRunPatch: true})
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.ID)
		if task.Kind != liveKindQueued || task.Finished {
			t.Errorf("task %+v", task)
		}
	}
	if strings.Join(ids, ",") != "q-1,q-2" {
		t.Fatalf("queued = %v, want q-1,q-2 (the patch run shows as itself)", ids)
	}
	// Waiting is about to run; delayed is for later, at its next run.
	if w := tasks[0]; !w.Running || w.StartedAt == nil || w.ScheduledAt != nil {
		t.Errorf("waiting = %+v", w)
	}
	if d := tasks[1]; d.Running || d.StartedAt != nil || d.ScheduledAt == nil {
		t.Errorf("delayed = %+v", d)
	}
}

// A job other than an agent update left open for long is no longer live.
func TestLiveTasksFromJobsDropsStaleOpenJobs(t *testing.T) {
	now := time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)
	rows := []db.ListLiveJobHistoryRow{
		{JobID: "old-report", JobName: "report_now", Status: "active", CreatedAt: ts(now.Add(-20 * time.Minute))},
		{JobID: "new-report", JobName: "report_now", Status: "active", CreatedAt: ts(now.Add(-time.Minute))},
		{JobID: "update", JobName: store.AgentUpdateJobName, Status: "active", CreatedAt: ts(now.Add(-30 * time.Minute))},
	}
	var ids []string
	for _, task := range liveTasksFromJobs(rows, now) {
		ids = append(ids, task.ID)
		if !task.Running {
			t.Errorf("%s not running", task.ID)
		}
	}
	if strings.Join(ids, ",") != "new-report,update" {
		t.Fatalf("live = %v", ids)
	}
}

func TestLiveTasksFromPatchRuns(t *testing.T) {
	start := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	pkg := "openssl"
	rows := []db.ListLivePatchRunsRow{
		{ID: "run-1", PatchType: "patch_all", Status: "running", StartedAt: ts(start), OutputTail: "Reading package lists...\nUnpacking \x1b[1mlibssl3\x1b[0m (3.0.13) ...\n\n"},
		{ID: "run-2", PatchType: "patch_package", PackageName: &pkg, Status: "completed", DryRun: true, CreatedAt: ts(start), CompletedAt: ts(start.Add(time.Minute))},
	}
	tasks := liveTasksFromPatchRuns(rows)
	if r := tasks[0]; r.Finished || !r.Running || r.Message != "Unpacking libssl3 (3.0.13) ..." || r.StartedAt == nil {
		t.Fatalf("running = %+v", r)
	}
	if r := tasks[1]; !r.Finished || r.Name != "patch_package" || r.Package != "openssl" || !r.DryRun || r.StartedAt == nil || !r.StartedAt.Equal(start) {
		t.Fatalf("finished = %+v", r)
	}
}

func TestLastOutputLine(t *testing.T) {
	cases := map[string]string{
		"":                                   "",
		"one\ntwo\n":                         "two",
		"Progress: [ 10%]\rProgress: [ 60%]": "Progress: [ 60%]",
		"line\r\n":                           "line",
		"\x1b[32mdone\x1b[0m\n  \n":          "done",
	}
	for in, want := range cases {
		if got := lastOutputLine(in); got != want {
			t.Errorf("lastOutputLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLiveTaskFromCompliance(t *testing.T) {
	now := time.Now()
	open := liveTaskFromCompliance(ComplianceProgress{Phase: "evaluating", ProfileName: "CIS", Percent: 15, StartedAt: now, UpdatedAt: now})
	if open.Status != "active" || open.Finished || !open.Running || open.Percent == nil || *open.Percent != 15 {
		t.Fatalf("open = %+v", open)
	}
	done := liveTaskFromCompliance(ComplianceProgress{Phase: "failed", Error: "oscap missing", StartedAt: now, UpdatedAt: now})
	if done.Status != "failed" || !done.Finished || done.FinishedAt == nil || done.Error != "oscap missing" {
		t.Fatalf("done = %+v", done)
	}
}

func TestSortLiveTasks(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 13, 0, 0, 0, time.UTC)
	at := func(m int) *time.Time { v := t0.Add(time.Duration(m) * time.Minute); return &v }
	tasks := []LiveTask{
		{ID: "done-old", Finished: true, FinishedAt: at(1)},
		{ID: "open-old", StartedAt: at(0)},
		{ID: "done-new", Finished: true, FinishedAt: at(5)},
		{ID: "open-new", StartedAt: at(3)},
	}
	sortLiveTasks(tasks)
	var ids []string
	for _, task := range tasks {
		ids = append(ids, task.ID)
	}
	if strings.Join(ids, ",") != "open-new,open-old,done-new,done-old" {
		t.Fatalf("order = %v", ids)
	}
}
