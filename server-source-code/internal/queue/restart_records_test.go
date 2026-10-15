package queue

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

type fakeCloser struct {
	got []db.CloseStaleAgentCommandsParams
}

func (f *fakeCloser) CloseStaleAgentCommands(_ context.Context, arg db.CloseStaleAgentCommandsParams) (int64, error) {
	f.got = append(f.got, arg)
	return 2, nil
}

func TestCloseStaleRestartRecords(t *testing.T) {
	f := &fakeCloser{}
	now := time.Date(2026, 10, 12, 18, 30, 0, 0, time.FixedZone("CEST", 2*3600))
	n, err := closeStaleRestartRecords(context.Background(), f, now)
	if err != nil || n != 2 || len(f.got) != 1 {
		t.Fatalf("n=%d err=%v calls=%d", n, err, len(f.got))
	}
	p := f.got[0]
	if p.ErrorMessage != "no answer from the agent within 10 minutes" {
		t.Errorf("message %q", p.ErrorMessage)
	}
	if p.QueueName != QueueAgentCommands {
		t.Errorf("queue %q", p.QueueName)
	}
	// Only restart records: never another job of the agent-commands queue.
	if strings.Join(p.JobNames, ",") != "reboot,reboot_cancel" {
		t.Errorf("job names %v", p.JobNames)
	}
	// Stored timestamps are UTC wall-clock.
	want := time.Date(2026, 10, 12, 16, 20, 0, 0, time.UTC)
	if !p.StaleBefore.Valid || !p.StaleBefore.Time.Equal(want) || p.StaleBefore.Time.Location() != time.UTC {
		t.Errorf("stale_before %v, want %v", p.StaleBefore.Time, want)
	}
}

func TestRestartJobNamesCannotBeChangedByCallers(t *testing.T) {
	names := RestartJobNames()
	names[0] = "run_scan"
	if RestartJobNames()[0] != JobNameReboot {
		t.Fatal("a caller changed the restart job names")
	}
}

func TestSweepCommandRecordsWithoutDatabase(t *testing.T) {
	h := NewPatchRunCleanupHandler(nil, nil, slog.Default(), nil)
	h.sweepCommandRecords(context.Background(), nil, "tenant.example") // must not panic
}

func TestCloseStaleAgentUpdates(t *testing.T) {
	f := &fakeCloser{}
	now := time.Date(2026, 10, 14, 18, 0, 0, 0, time.UTC)
	if _, err := closeStaleAgentUpdates(context.Background(), f, now); err != nil || len(f.got) != 1 {
		t.Fatalf("err=%v calls=%d", err, len(f.got))
	}
	p := f.got[0]
	if p.QueueName != QueueAgentCommands || strings.Join(p.JobNames, ",") != TypeUpdateAgent {
		t.Errorf("queue %q, job names %v: only agent updates", p.QueueName, p.JobNames)
	}
	if !strings.HasPrefix(p.ErrorMessage, "no news from the agent within 45 minutes") {
		t.Errorf("message %q", p.ErrorMessage)
	}
	if want := now.Add(-45 * time.Minute); !p.StaleBefore.Time.Equal(want) {
		t.Errorf("stale_before %v, want %v", p.StaleBefore.Time, want)
	}
}

// The store records agent updates under the queue task's names.
func TestAgentUpdateRecordsUseTheTaskNames(t *testing.T) {
	if store.AgentUpdateQueue != QueueAgentCommands || store.AgentUpdateJobName != TypeUpdateAgent {
		t.Fatalf("store records %s/%s, the task is %s/%s", store.AgentUpdateQueue, store.AgentUpdateJobName, QueueAgentCommands, TypeUpdateAgent)
	}
}
