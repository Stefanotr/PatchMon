package handler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

func TestParseAgentVersion(t *testing.T) {
	cases := map[string]string{
		"1.5.0":                 "1.5.0",
		" v2.1.4-fork.11 ":      "2.1.4-fork.11",
		"2.0.3-rc.137+meta":     "2.0.3-rc.137+meta",
		"":                      "",
		"1.0 ; DROP":            "",
		"<script>":              "",
		strings.Repeat("1", 65): "",
	}
	for in, want := range cases {
		if got := ParseAgentVersion(in); got != want {
			t.Errorf("ParseAgentVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseUpdateProgress(t *testing.T) {
	p, ok := ParseUpdateProgress([]byte(`{"type":"update_progress","stage":"downloading","percent":140.7,"from_version":"v1.4.0","to_version":"bad version","message":"a\nb\u0007c"}`))
	if !ok {
		t.Fatal("valid message refused")
	}
	if p.Stage != store.AgentUpdateDownloading || p.Percent == nil || *p.Percent != 100 {
		t.Errorf("stage/percent = %q/%v", p.Stage, p.Percent)
	}
	if p.FromVersion != "1.4.0" || p.ToVersion != "" {
		t.Errorf("versions = %q -> %q", p.FromVersion, p.ToVersion)
	}
	if p.Message != "a b c" {
		t.Errorf("message = %q, want one clean line", p.Message)
	}

	// A percent only goes with the download.
	p, _ = ParseUpdateProgress([]byte(`{"stage":"verifying","percent":50}`))
	if p.Percent != nil {
		t.Errorf("percent kept on %q", p.Stage)
	}
	p, _ = ParseUpdateProgress([]byte(`{"stage":"downloading","percent":-5}`))
	if p.Percent == nil || *p.Percent != 0 {
		t.Errorf("negative percent = %v, want 0", p.Percent)
	}

	p, _ = ParseUpdateProgress([]byte(`{"stage":"failed","message":"` + strings.Repeat("x", 500) + `"}`))
	if n := len([]rune(p.Message)); n > updateProgressMessageMax+3 {
		t.Errorf("message is %d runes", n)
	}

	for _, raw := range []string{`{"stage":"done"}`, `{"stage":"sent"}`, `{"stage":"whatever"}`, `not json`} {
		if _, ok := ParseUpdateProgress([]byte(raw)); ok {
			t.Errorf("%s accepted", raw)
		}
	}
}

func TestSkipUpdateProgress(t *testing.T) {
	pct := func(n int) *int { return &n }
	now := time.Now()
	dl := func(p *int) store.AgentUpdateProgress {
		return store.AgentUpdateProgress{Stage: store.AgentUpdateDownloading, Percent: p}
	}
	if !skipUpdateProgress(dl(pct(10)), now, dl(pct(20)), now.Add(100*time.Millisecond)) {
		t.Error("percent too soon after the last one recorded")
	}
	if skipUpdateProgress(dl(pct(10)), now, dl(pct(20)), now.Add(time.Second)) {
		t.Error("percent after the gap skipped")
	}
	if skipUpdateProgress(dl(pct(90)), now, dl(pct(100)), now) {
		t.Error("end of the download skipped")
	}
	if skipUpdateProgress(dl(pct(90)), now, store.AgentUpdateProgress{Stage: store.AgentUpdateVerifying}, now) {
		t.Error("stage change skipped")
	}
	if skipUpdateProgress(store.AgentUpdateProgress{Stage: store.AgentUpdateChecking}, now, dl(nil), now) {
		t.Error("download start skipped")
	}
}

// The stages are recorded in the order they came, even after the
// connection ended.
func TestUpdateProgressWorkerKeepsTheOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	record := func(ctx context.Context, apiID, hostID string, p store.AgentUpdateProgress) {
		if apiID != "api-1" || hostID != "host-1" {
			t.Errorf("recorded for %s/%s", apiID, hostID)
		}
		if ctx.Err() != nil {
			t.Errorf("context ended: %v", ctx.Err())
		}
		mu.Lock()
		got = append(got, p.Stage)
		mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := startUpdateProgressWorker(ctx, "api-1", "host-1", record)
	for _, stage := range []string{"checking", "downloading", "bogus", "verifying", "installing", "restarting"} {
		w.offer([]byte(`{"type":"update_progress","stage":"` + stage + `"}`))
	}
	cancel() // the connection closes right after the last stage
	w.stop()
	<-w.done
	want := "checking,downloading,verifying,installing,restarting"
	if strings.Join(got, ",") != want {
		t.Fatalf("recorded %v, want %s", got, want)
	}
}

func TestUpdateProgressWorkerDropsAFlood(t *testing.T) {
	block := make(chan struct{})
	var n int
	var mu sync.Mutex
	w := startUpdateProgressWorker(context.Background(), "api-1", "host-1", func(context.Context, string, string, store.AgentUpdateProgress) {
		<-block
		mu.Lock()
		n++
		mu.Unlock()
	})
	stages := []string{"checking", "downloading", "verifying", "installing"}
	for i := 0; i < updateProgressBacklog*4; i++ {
		w.offer([]byte(`{"stage":"` + stages[i%len(stages)] + `"}`)) // never blocks
	}
	close(block)
	w.stop()
	<-w.done
	if n > updateProgressBacklog+1 {
		t.Fatalf("recorded %d messages, want at most the backlog", n)
	}
}

// Records are rationed: a connection cannot write as fast as it sends.
func TestUpdateProgressWorkerRationsRecords(t *testing.T) {
	var mu sync.Mutex
	n := 0
	w := startUpdateProgressWorker(context.Background(), "api-1", "host-1", func(context.Context, string, string, store.AgentUpdateProgress) {
		mu.Lock()
		n++
		mu.Unlock()
	})
	stages := []string{"downloading", "failed"}
	for i := 0; i < 2000; i++ {
		w.offer([]byte(`{"stage":"` + stages[i%2] + `"}`))
		if i%updateProgressBacklog == 0 {
			time.Sleep(time.Millisecond) // let the worker drain the backlog
		}
	}
	w.stop()
	<-w.done
	// The burst, plus what refilled while the test ran (well under a second).
	if n > updateProgressBurst+2 {
		t.Fatalf("recorded %d of 2000 messages, want at most about %d", n, updateProgressBurst)
	}
	if n < updateProgressBurst/2 {
		t.Fatalf("recorded %d messages: the burst should go through", n)
	}
}

type fakeUpdateRecorder struct {
	progress []store.AgentUpdateProgress
	versions []string
}

func (f *fakeUpdateRecorder) RecordProgress(_ context.Context, _, _ string, p store.AgentUpdateProgress) error {
	f.progress = append(f.progress, p)
	return nil
}

func (f *fakeUpdateRecorder) RecordConnected(_ context.Context, _, version string) error {
	f.versions = append(f.versions, version)
	return nil
}

func TestRecordAgentVersionIgnoresOlderAgents(t *testing.T) {
	f := &fakeUpdateRecorder{}
	cb := RecordAgentVersion(f)
	cb(context.Background(), "api-1", "")
	cb(context.Background(), "api-1", "1.5.0")
	if strings.Join(f.versions, ",") != "1.5.0" {
		t.Fatalf("versions = %v", f.versions)
	}
	RecordAgentUpdateProgress(f)(context.Background(), "api-1", "host-1", store.AgentUpdateProgress{Stage: store.AgentUpdateChecking})
	if len(f.progress) != 1 {
		t.Fatal("progress not recorded")
	}
}
