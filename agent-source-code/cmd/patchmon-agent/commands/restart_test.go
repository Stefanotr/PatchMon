package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"patchmon-agent/internal/config"
	"patchmon-agent/internal/reboot"

	"github.com/gorilla/websocket"
)

type scheduledRestart struct {
	calls int
	delay time.Duration
	msg   string
	err   error
	// reports holds the restarts reported to the server: "ok" or the error.
	reports []string
	// agentRestarts counts restarts of the agent's own service, which fail
	// with agentRestartErr.
	agentRestarts   atomic.Int32
	agentRestartErr error
	// cancels holds the restarts cancelled on the host, which fail with
	// cancelErr.
	cancels   []reboot.Scheduled
	cancelErr error
}

func (s *scheduledRestart) cancel(r reboot.Scheduled) error {
	s.cancels = append(s.cancels, r)
	return s.cancelErr
}

func (s *scheduledRestart) report(runID string, _ time.Time, err error) {
	var skipped skippedRestart
	switch {
	case errors.As(err, &skipped):
		s.reports = append(s.reports, runID+" skipped: "+skipped.reason)
	case err != nil:
		s.reports = append(s.reports, runID+" "+err.Error())
	default:
		s.reports = append(s.reports, runID+" ok")
	}
}

func (s *scheduledRestart) schedule(d time.Duration, msg string) error {
	s.calls++
	s.delay, s.msg = d, msg
	return s.err
}

// resetRestartState clears what earlier tests left behind and fakes the
// operating system for the command paths: the restart record goes to a
// temporary directory, and the host has the recorded restart pending.
func resetRestartState(t *testing.T) *scheduledRestart {
	t.Helper()
	clearGate := func() {
		restartGate.Lock()
		restartGate.record, restartGate.recordLoaded = nil, false
		restartGate.agentRestartWaiting = false
		restartGate.Unlock()
	}
	clearGate()
	patchRunCancels.Range(func(k, _ any) bool {
		patchRunCancels.Delete(k)
		return true
	})
	updateInFlight.Store(false)
	s := &scheduledRestart{}
	oldSchedule, oldCancel, oldPending, oldReport := scheduleHostRestart, cancelHostRestart, osPendingRestart, reportRunRestart
	oldAgentRestart, oldAllowed := restartAgentService, remoteRestartsAllowed
	oldRecordPath, oldOwner := restartRecordPath, pendingRestartOwner
	oldCommandSchedule, oldWork, oldReply := scheduleCommandRestart, commandWorkLimit, commandReplyLimit
	recordPath := filepath.Join(t.TempDir(), restartRecordFile)
	restartRecordPath = func() string { return recordPath }
	pendingRestartOwner = func(context.Context, reboot.Scheduled) reboot.Owner { return reboot.OwnerPatchMon }
	remoteRestartsAllowed = func() bool { return true }
	scheduleHostRestart = s.schedule
	scheduleCommandRestart = func(_ context.Context, d time.Duration, msg string) error { return s.schedule(d, msg) }
	cancelHostRestart = func(_ context.Context, r reboot.Scheduled) error { return s.cancel(r) }
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, false }
	reportRunRestart = s.report
	restartAgentService = func() error {
		s.agentRestarts.Add(1)
		return s.agentRestartErr
	}
	t.Cleanup(func() {
		scheduleHostRestart, cancelHostRestart, osPendingRestart, reportRunRestart = oldSchedule, oldCancel, oldPending, oldReport
		restartAgentService, remoteRestartsAllowed = oldAgentRestart, oldAllowed
		restartRecordPath, pendingRestartOwner = oldRecordPath, oldOwner
		scheduleCommandRestart, commandWorkLimit, commandReplyLimit = oldCommandSchedule, oldWork, oldReply
		clearGate()
	})
	return s
}

// recordRestart records a restart this agent scheduled for at.
func recordRestart(at time.Time) {
	restartGate.Lock()
	defer restartGate.Unlock()
	setRestartRecordLocked(reboot.Scheduled{At: at, Since: at.Add(-time.Hour)})
}

// recordedRestart returns the restart the agent holds as its own.
func recordedRestart() (reboot.Scheduled, bool) {
	restartGate.Lock()
	defer restartGate.Unlock()
	if restartGate.record == nil {
		return reboot.Scheduled{}, false
	}
	return *restartGate.record, true
}

func TestScheduleRestart(t *testing.T) {
	for _, tt := range []struct {
		name      string
		opts      reboot.Options
		needed    bool
		blocked   string
		failWith  error
		wantCalls int
		wantLine  string
		wantErr   bool
		// wantReport is what reaches the server: "" (nothing), "ok",
		// "skipped: " and the reason, or an error.
		wantReport string
	}{
		{name: "never: silent", opts: reboot.Options{Mode: reboot.ModeNever}, needed: true},
		{name: "unset: silent", opts: reboot.Options{}, needed: true},
		{name: "blocked", opts: reboot.Options{Mode: reboot.ModeAlways}, blocked: "the run was stopped", wantLine: "Restart: not scheduled, the run was stopped", wantReport: "skipped: the run was stopped"},
		{name: "blocked, if needed", opts: reboot.Options{Mode: reboot.ModeIfNeeded}, blocked: "the run failed", wantLine: "Restart: not scheduled, the run failed", wantReport: "skipped: the run failed"},
		{name: "if needed, none pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded}, wantLine: "Restart: not needed"},
		{name: "if needed, pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded, DelayMinutes: 10}, needed: true, wantCalls: 1, wantLine: "Restart: scheduled for", wantReport: "ok"},
		{name: "always", opts: reboot.Options{Mode: reboot.ModeAlways}, wantCalls: 1, wantLine: "(in 5m0s)", wantReport: "ok"},
		{name: "something else already pending", opts: reboot.Options{Mode: reboot.ModeAlways}, failWith: reboot.ErrAlreadyScheduled, wantCalls: 1, wantLine: "already pending on this host (not by this run)", wantReport: "skipped: a restart or shutdown is already pending on this host"},
		{name: "scheduling fails", opts: reboot.Options{Mode: reboot.ModeAlways}, failWith: errors.New("access denied"), wantCalls: 1, wantLine: "Restart: could not be scheduled: access denied", wantErr: true, wantReport: "access denied"},
		{name: "time too far", opts: reboot.Options{Mode: reboot.ModeAlways, At: time.Now().Add(reboot.MaxDelay + time.Hour)}, wantLine: "more than", wantErr: true, wantReport: "more than"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reported := resetRestartState(t)
			var out strings.Builder
			s := &scheduledRestart{err: tt.failWith}
			err := scheduleRestart(&out, "run-1", tt.opts, tt.needed, tt.blocked, s.schedule)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if s.calls != tt.wantCalls {
				t.Fatalf("schedule called %d time(s), want %d", s.calls, tt.wantCalls)
			}
			if tt.wantLine == "" && out.Len() != 0 {
				t.Errorf("unexpected output %q", out.String())
			}
			if !strings.Contains(out.String(), tt.wantLine) {
				t.Errorf("output %q lacks %q", out.String(), tt.wantLine)
			}
			if s.calls > 0 && (s.delay < reboot.MinDelay || !strings.HasPrefix(s.msg, "PatchMon: ")) {
				t.Errorf("scheduled with delay %s, message %q", s.delay, s.msg)
			}
			switch {
			case tt.wantReport == "" && len(reported.reports) != 0:
				t.Errorf("reported %v, want nothing", reported.reports)
			case tt.wantReport != "" && (len(reported.reports) != 1 || !strings.HasPrefix(reported.reports[0], "run-1 ") || !strings.Contains(reported.reports[0], tt.wantReport)):
				t.Errorf("reported %v, want one report with %q", reported.reports, tt.wantReport)
			case !strings.HasPrefix(tt.wantReport, "skipped: ") && slices.ContainsFunc(reported.reports, func(r string) bool { return strings.Contains(r, " skipped: ") }):
				t.Errorf("reported %v as skipped", reported.reports)
			}
		})
	}
}

// A run's restart never cuts off another run, and never stacks on a restart
// already scheduled.
func TestScheduleRestartRespectsOtherRunsAndPendingRestarts(t *testing.T) {
	reported := resetRestartState(t)
	always := reboot.Options{Mode: reboot.ModeAlways}

	patchRunCancels.Store("run-2", &patchRun{cancel: func() {}})
	var out strings.Builder
	s := &scheduledRestart{}
	if err := scheduleRestart(&out, "run-1", always, false, "", s.schedule); err != nil || s.calls != 0 ||
		!strings.Contains(out.String(), "another patch run is in progress") {
		t.Fatalf("restarted under another run: calls %d, %q, %v", s.calls, out.String(), err)
	}
	patchRunCancels.Delete("run-2")

	patchRunCancels.Store("run-1", &patchRun{cancel: func() {}})
	out.Reset()
	if err := scheduleRestart(&out, "run-1", always, false, "", s.schedule); err != nil || s.calls != 1 {
		t.Fatalf("the run's own registration blocked its restart: %q, %v", out.String(), err)
	}
	out.Reset()
	if err := scheduleRestart(&out, "run-1", always, false, "", s.schedule); err != nil || s.calls != 1 ||
		!strings.Contains(out.String(), "Restart: already scheduled for") {
		t.Fatalf("a second restart was scheduled: calls %d, %q", s.calls, out.String())
	}

	// Each wanted restart left out is reported as skipped, with the reason.
	want := []string{
		"run-1 skipped: another patch run is in progress on this host",
		"run-1 ok",
		"run-1 skipped: a restart is already scheduled for ",
	}
	if len(reported.reports) != len(want) {
		t.Fatalf("reported %q, want %q", reported.reports, want)
	}
	for i, w := range want {
		if !strings.HasPrefix(reported.reports[i], w) {
			t.Errorf("report %d = %q, want %q", i, reported.reports[i], w)
		}
	}
}

// The skipped event's exact shape, next to the scheduled and failed ones.
func TestRunRestartEventJSON(t *testing.T) {
	at := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		at   time.Time
		err  error
		want string
	}{
		{"scheduled", at, nil, `{"type":"reboot_result","action":"run","run_id":"run-1","ok":true,"reboot_at":"2026-10-12T01:00:00Z"}`},
		{"skipped", time.Time{}, skippedRestart{"another patch run is in progress on this host"}, `{"type":"reboot_result","action":"run","run_id":"run-1","ok":false,"skipped":true,"error":"another patch run is in progress on this host"}`},
		{"failed", time.Time{}, errors.New("access denied"), `{"type":"reboot_result","action":"run","run_id":"run-1","ok":false,"error":"access denied"}`},
	} {
		b, err := encodeRestartMessage(runRestartReply("run-1", tt.at, tt.err))
		if err != nil || string(b) != tt.want {
			t.Errorf("%s: %s, %v; want %s", tt.name, b, err, tt.want)
		}
	}
}

// A real run does not start when a scheduled restart would cut it off.
func TestRegisterPatchRunAndPendingRestart(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})

	recordRestart(time.Now().Add(10 * time.Minute))
	if _, err := registerPatchRun("real", noop, false); err == nil || !strings.Contains(err.Error(), "a restart is scheduled for") {
		t.Fatalf("real run started under a pending restart: %v", err)
	}
	if _, err := registerPatchRun("dry", noop, true); err != nil {
		t.Fatalf("dry run refused: %v", err)
	}
	patchRunCancels.Delete("dry")

	recordRestart(time.Now().Add(patchRunTimeout() + time.Hour))
	if _, err := registerPatchRun("later", noop, false); err != nil {
		t.Fatalf("a restart after the run's time limit blocked it: %v", err)
	}
	patchRunCancels.Delete("later")

	// A restart long past its time did not happen and holds nothing back.
	recordRestart(time.Now().Add(-time.Hour))
	if _, err := registerPatchRun("stale", noop, false); err != nil {
		t.Fatalf("a stale restart blocked the run: %v", err)
	}
	if _, ok := recordedRestart(); ok {
		t.Error("the stale record was kept")
	}
}

// A restart the operating system reports, scheduled by someone else or by
// this agent before it was restarted, holds real runs back too.
func TestRegisterPatchRunAndOSPendingRestart(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})
	for _, tt := range []struct {
		name    string
		at      time.Time
		pending bool
		refused bool
	}{
		{name: "time unknown", pending: true, refused: true},
		{name: "soon", at: time.Now().Add(patchRunTimeout() / 2), pending: true, refused: true},
		// The last install can overrun the run's time limit.
		{name: "just after the time limit", at: time.Now().Add(patchRunTimeout() + 5*time.Minute), pending: true, refused: true},
		{name: "well after the run", at: time.Now().Add(patchRunTimeout() + time.Hour), pending: true},
		{name: "long past", at: time.Now().Add(-time.Hour), pending: true},
		{name: "none"},
	} {
		osPendingRestart = func() (time.Time, bool) { return tt.at, tt.pending }
		_, err := registerPatchRun("real", noop, false)
		patchRunCancels.Delete("real")
		if (err != nil) != tt.refused {
			t.Errorf("%s: err = %v, refused want %v", tt.name, err, tt.refused)
		}
		if _, err := registerPatchRun("dry", noop, true); err != nil {
			t.Errorf("%s: dry run refused: %v", tt.name, err)
		}
		patchRunCancels.Delete("dry")
	}
}

// Where the system does not tell when, a pending restart this agent
// scheduled is judged by the time it recorded, once the system confirms it
// is that one. Unconfirmed (another shutdown pending as well), it could be
// sooner and holds the run back.
func TestRegisterPatchRunWithItsOwnRestartOfUnknownTime(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, true }
	recordRestart(time.Now().Add(patchRunTimeout() + time.Hour))
	run, err := registerPatchRun("real", noop, false)
	if err != nil {
		t.Fatalf("run refused under its own far restart: %v", err)
	}
	unregisterPatchRun("real", run)

	pendingRestartOwner = func(context.Context, reboot.Scheduled) reboot.Owner { return reboot.OwnerUnknown }
	if _, err := registerPatchRun("real", noop, false); err == nil || !strings.Contains(err.Error(), "a restart or shutdown is pending") {
		t.Fatalf("run started under an unconfirmed restart of unknown time: %v", err)
	}
	if _, ok := recordedRestart(); !ok {
		t.Fatal("an unconfirmed record was dropped")
	}
	// A recorded time before the run's end still holds it back.
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, false }
	recordRestart(time.Now().Add(10 * time.Minute))
	if _, err := registerPatchRun("real", noop, false); err == nil || !strings.Contains(err.Error(), "a restart is scheduled for") {
		t.Fatalf("run started before an unconfirmed restart: %v", err)
	}
}

// The self-update restarts the agent: it never runs under a patch run, and
// no patch run starts while it is in progress.
func TestAgentUpdateAndPatchRunsExcludeEachOther(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})

	updateInFlight.Store(true)
	for _, dry := range []bool{false, true} {
		if _, err := registerPatchRun("run", noop, dry); err == nil || !strings.Contains(err.Error(), "updating itself") {
			t.Errorf("dry run %v started during an update: %v", dry, err)
		}
	}
	updateInFlight.Store(false)

	if err := patchRunBlocksUpdate(); err != nil {
		t.Fatalf("update refused with no run: %v", err)
	}
	if _, err := registerPatchRun("dry", noop, true); err != nil {
		t.Fatal(err)
	}
	if err := patchRunBlocksUpdate(); err == nil {
		t.Fatal("update allowed during a dry run")
	}
	patchRunCancels.Delete("dry")
}

func TestRestartCommandWhenTheSystemHasOnePending(t *testing.T) {
	s := resetRestartState(t)
	s.err = reboot.ErrAlreadyScheduled
	_, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways})
	if !errors.Is(err, reboot.ErrAlreadyScheduled) || !strings.Contains(err.Error(), "cancel it first") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := recordedRestart(); ok {
		t.Error("a restart that was not scheduled is recorded as pending")
	}
}

func TestRestartCommand(t *testing.T) {
	s := resetRestartState(t)

	if _, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeIfNeeded}); err == nil {
		t.Error("a command without mode always was accepted")
	}

	patchRunCancels.Store("run", &patchRun{cancel: func() {}})
	if _, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways}); err == nil || !strings.Contains(err.Error(), "patch run is in progress") {
		t.Fatalf("restart scheduled during a run: %v", err)
	}
	patchRunCancels.Delete("run")

	at, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways, DelayMinutes: 30})
	if err != nil || s.calls != 1 || time.Until(at) < 29*time.Minute {
		t.Fatalf("restart: at %s, err %v, calls %d", at, err, s.calls)
	}
	if _, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways}); err == nil || !strings.Contains(err.Error(), "already scheduled") {
		t.Fatalf("second restart accepted: %v", err)
	}

	if err := cancelRestartCommand(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.cancels) != 1 || !s.cancels[0].At.Equal(at) {
		t.Fatalf("cancelled %+v, want the restart scheduled for %s", s.cancels, at)
	}
	if _, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways}); err != nil {
		t.Fatalf("restart refused after the cancel: %v", err)
	}

	s.cancelErr = errors.New("not supported")
	if err := cancelRestartCommand(context.Background()); err == nil {
		t.Fatal("cancel failure swallowed")
	}
	if _, ok := recordedRestart(); !ok {
		t.Error("a failed cancel forgot the pending restart")
	}
}

// A run_patch delivered twice leaves the real run alone: its entry, what a
// stop request reaches, and what the server hears about it.
func TestDuplicatePatchRunIsIgnored(t *testing.T) {
	resetRestartState(t)
	stopped := false
	first, err := registerPatchRun("run-1", func() { stopped = true }, false)
	if err != nil {
		t.Fatal(err)
	}
	var refused []string
	refuse := func(reason string) { refused = append(refused, reason) }

	dup, err := beginPatchRun("run-1", func() {}, false, refuse)
	if !errors.Is(err, errDuplicatePatchRun) || dup != nil {
		t.Fatalf("duplicate registered: %v, %v", dup, err)
	}
	// Even when a new run would be refused, a duplicate says nothing: the
	// refusal would mark the real run failed.
	updateInFlight.Store(true)
	if _, err := beginPatchRun("run-1", func() {}, true, refuse); !errors.Is(err, errDuplicatePatchRun) {
		t.Fatalf("err = %v, want errDuplicatePatchRun", err)
	}
	updateInFlight.Store(false)
	if len(refused) != 0 {
		t.Fatalf("a duplicate was reported to the server: %v", refused)
	}

	got, ok := lookupPatchRun("run-1")
	if !ok || got != first {
		t.Fatal("the duplicate replaced the real run's entry")
	}
	got.cancel()
	if !stopped {
		t.Fatal("a stop request did not reach the real run")
	}

	// Only the run's own entry is removed.
	unregisterPatchRun("run-1", &patchRun{})
	if _, ok := lookupPatchRun("run-1"); !ok {
		t.Fatal("another entry removed the real run's")
	}
	unregisterPatchRun("run-1", first)
	if _, ok := lookupPatchRun("run-1"); ok {
		t.Fatal("the run's entry was not removed")
	}
	again, err := registerPatchRun("run-1", func() {}, false)
	if err != nil {
		t.Fatal(err)
	}
	unregisterPatchRun("run-1", first)
	if got, ok := lookupPatchRun("run-1"); !ok || got != again {
		t.Fatal("a finished run removed the entry of a later run with the same id")
	}
}

// Deliveries racing each other register exactly one run.
func TestConcurrentDuplicatePatchRuns(t *testing.T) {
	resetRestartState(t)
	const n = 16
	var (
		wg       sync.WaitGroup
		won      atomic.Int32
		refusals atomic.Int32
	)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := beginPatchRun("run-1", func() {}, i%2 == 0, func(string) { refusals.Add(1) })
			switch {
			case err == nil:
				won.Add(1)
			case !errors.Is(err, errDuplicatePatchRun):
				t.Errorf("err = %v, want errDuplicatePatchRun", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won.Load() != 1 || refusals.Load() != 0 {
		t.Fatalf("%d run(s) registered, %d refusal(s) sent; want 1 and 0", won.Load(), refusals.Load())
	}
}

// apply_config saves the configuration at once but restarts the agent only
// once no patch run, dry runs included, is in progress; no run starts while
// the restart waits.
func TestApplyConfigWaitsForPatchRuns(t *testing.T) {
	s := resetRestartState(t)
	m := withTestConfig(t, "on-demand")
	noop := context.CancelFunc(func() {})

	if err := applyConfig(map[string]interface{}{"docker": true}); err != nil {
		t.Fatal(err)
	}
	if s.agentRestarts.Load() != 1 {
		t.Fatalf("agent restarted %d time(s) with no run in progress, want 1", s.agentRestarts.Load())
	}
	// The fake restart returns; the real one ends the process.
	restartGate.Lock()
	restartGate.agentRestartWaiting = false
	restartGate.Unlock()

	dry, err := registerPatchRun("dry", noop, true)
	if err != nil {
		t.Fatal(err)
	}
	realRun, err := registerPatchRun("real", noop, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyConfig(map[string]interface{}{"docker": false}); err != nil {
		t.Fatal(err)
	}
	if s.agentRestarts.Load() != 1 {
		t.Fatal("the agent restarted during a patch run")
	}
	saved := config.New()
	saved.SetConfigFile(m.GetConfigFile())
	if err := saved.LoadConfig(); err != nil || saved.IsIntegrationEnabled("docker") {
		t.Fatalf("the configuration was not saved straight away: docker %v, %v", saved.IsIntegrationEnabled("docker"), err)
	}
	for _, dryRun := range []bool{true, false} {
		if _, err := registerPatchRun("next", noop, dryRun); err == nil || !strings.Contains(err.Error(), "restarts to apply a new configuration") {
			t.Fatalf("a run (dry run %v) started while the agent restart waits: %v", dryRun, err)
		}
	}
	if err := applyConfig(map[string]interface{}{"docker": true}); err != nil {
		t.Fatal(err)
	}

	unregisterPatchRun("dry", dry)
	if s.agentRestarts.Load() != 1 {
		t.Fatal("the agent restarted while a run was still in progress")
	}
	unregisterPatchRun("real", realRun)
	if s.agentRestarts.Load() != 2 {
		t.Fatalf("agent restarted %d time(s) in all, want 2: once after the last run", s.agentRestarts.Load())
	}
}

// When the agent cannot restart itself, runs start again; the configuration
// applies at the agent's next start.
func TestFailedAgentRestartLetsRunsStart(t *testing.T) {
	s := resetRestartState(t)
	s.agentRestartErr = errors.New("no init system")
	if err := restartAgentForConfig(); err == nil {
		t.Fatal("the failure was swallowed")
	}
	if _, err := registerPatchRun("run", context.CancelFunc(func() {}), true); err != nil {
		t.Fatalf("runs still refused after a failed agent restart: %v", err)
	}
}

// A host that has not opted in refuses the server's restart command and
// skips the restart of patch runs; cancelling stays allowed.
func TestRemoteRestartsDisabled(t *testing.T) {
	s := resetRestartState(t)
	withTestConfig(t, "on-demand")
	remoteRestartsAllowed = remoteRestartsEnabledInConfig
	path := cfgManager.GetConfigFile()

	_, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways})
	want := `remote restarts are disabled on this host: set "remote-restart-enabled: true" under integrations in ` + path + ` to allow them`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}

	var out strings.Builder
	if err := scheduleRestart(&out, "run-1", reboot.Options{Mode: reboot.ModeAlways}, false, "", s.schedule); err != nil {
		t.Fatalf("a skipped restart failed the run: %v", err)
	}
	if line := "Restart: skipped, remote restarts are disabled on this host (remote-restart-enabled in " + path + ")\n"; out.String() != line {
		t.Fatalf("output %q, want %q", out.String(), line)
	}
	if s.calls != 0 {
		t.Fatal("the host was restarted")
	}
	if want := "run-1 skipped: remote restarts are disabled on this host (remote-restart-enabled in " + path + ")"; len(s.reports) != 1 || s.reports[0] != want {
		t.Fatalf("reported %q, want %q", s.reports, want)
	}

	recordRestart(time.Now().Add(time.Hour))
	if err := cancelRestartCommand(context.Background()); err != nil || len(s.cancels) != 1 {
		t.Fatalf("cancel refused: %v", err)
	}
}

// The opt-in is local: apply_config cannot turn it, or the other local-only
// switches, on.
func TestApplyConfigCannotEnableRemoteRestarts(t *testing.T) {
	resetRestartState(t)
	m := withTestConfig(t, "on-demand")
	err := applyConfig(map[string]interface{}{
		"remote-restart-enabled": true,
		"ssh-proxy-enabled":      true,
		"rdp-proxy-enabled":      true,
		"integrations":           map[string]interface{}{"remote-restart-enabled": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := config.New()
	saved.SetConfigFile(m.GetConfigFile())
	if err := saved.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	for _, name := range config.LocalOnlyIntegrations {
		if m.IsIntegrationEnabled(name) || saved.IsIntegrationEnabled(name) {
			t.Errorf("apply_config turned %s on", name)
		}
	}
}

// A cancel touches the restart PatchMon scheduled, and nothing else.
func TestCancelOnlyWhatPatchMonScheduled(t *testing.T) {
	for _, tt := range []struct {
		name string
		// record says a restart was recorded; owner is what the host has
		// pending; cancelErr is the host's answer to the cancel.
		record     bool
		owner      reboot.Owner
		cancelErr  error
		wantErr    error
		wantCancel bool
		wantRecord bool
	}{
		{name: "nothing recorded", owner: reboot.OwnerOther, wantErr: reboot.ErrNotScheduledByPatchMon},
		{name: "scheduled outside PatchMon", record: true, owner: reboot.OwnerOther, wantErr: reboot.ErrNotScheduledByPatchMon},
		{name: "no longer pending", record: true, owner: reboot.OwnerNone, wantErr: reboot.ErrNotScheduledByPatchMon},
		{name: "PatchMon's", record: true, owner: reboot.OwnerPatchMon, wantCancel: true},
		// Unconfirmed: the host checks again and refuses unless it confirms.
		{name: "unconfirmed, the host confirms", record: true, owner: reboot.OwnerUnknown, wantCancel: true},
		{name: "unconfirmed, the host cannot confirm", record: true, owner: reboot.OwnerUnknown, cancelErr: reboot.ErrOwnerUnconfirmed, wantErr: reboot.ErrOwnerUnconfirmed, wantCancel: true, wantRecord: true},
		{name: "gone meanwhile", record: true, owner: reboot.OwnerPatchMon, cancelErr: reboot.ErrNothingScheduled, wantErr: reboot.ErrNothingScheduled, wantCancel: true},
		{name: "the host refuses", record: true, owner: reboot.OwnerUnknown, cancelErr: reboot.ErrNotScheduledByPatchMon, wantErr: reboot.ErrNotScheduledByPatchMon, wantCancel: true, wantRecord: true},
		{name: "the cancel fails", record: true, owner: reboot.OwnerPatchMon, cancelErr: errors.New("access denied"), wantCancel: true, wantRecord: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := resetRestartState(t)
			s.cancelErr = tt.cancelErr
			pendingRestartOwner = func(context.Context, reboot.Scheduled) reboot.Owner { return tt.owner }
			if tt.record {
				recordRestart(time.Now().Add(time.Hour))
			}
			err := cancelRestartCommand(context.Background())
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) || tt.wantErr == nil && (err == nil) != (tt.cancelErr == nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got := len(s.cancels) == 1; got != tt.wantCancel {
				t.Fatalf("cancelled on the host: %v, want %v", got, tt.wantCancel)
			}
			if _, got := recordedRestart(); got != tt.wantRecord {
				t.Fatalf("record kept: %v, want %v", got, tt.wantRecord)
			}
			if _, err := os.Lstat(restartRecordPath()); (err == nil) != tt.wantRecord {
				t.Fatalf("record file: %v, want kept %v", err, tt.wantRecord)
			}
		})
	}
	if want := "no restart scheduled by PatchMon is pending on this host; a restart or shutdown scheduled outside PatchMon is left alone"; reboot.ErrNotScheduledByPatchMon.Error() != want {
		t.Errorf("refusal = %q, want %q", reboot.ErrNotScheduledByPatchMon, want)
	}
}

// The server waits 20 seconds for the answer to a restart or cancel
// command: the answer leaves within 15, the work itself has less.
func TestCommandLimitsFitTheServersWait(t *testing.T) {
	if commandReplyLimit > 15*time.Second || commandWorkLimit >= commandReplyLimit {
		t.Fatalf("work limit %s, reply limit %s", commandWorkLimit, commandReplyLimit)
	}
}

// A command answers within its limit even when the operating system never
// does, and says the outcome is unknown.
func TestCommandAnswersWhenTheSystemHangs(t *testing.T) {
	resetRestartState(t)
	commandWorkLimit, commandReplyLimit = 50*time.Millisecond, 200*time.Millisecond
	release := make(chan struct{})
	scheduleCommandRestart = func(context.Context, time.Duration, string) error {
		<-release // ignores its deadline
		return nil
	}
	cancelHostRestart = func(context.Context, reboot.Scheduled) error {
		<-release
		return nil
	}
	// Let the hung calls finish before the next test takes the lock: taking
	// the gate waits for them to let go of it.
	defer func() {
		close(release)
		restartGate.Lock()
		defer restartGate.Unlock()
	}()

	start := time.Now()
	r := restartCommandReply(reboot.Options{Mode: reboot.ModeAlways})
	if elapsed := time.Since(start); elapsed > time.Second || r.OK || !strings.Contains(r.Error, "may still be scheduled") || r.Action != "reboot" {
		t.Fatalf("after %s: %+v", elapsed, r)
	}
}

// The work itself honours its deadline: an operating system call that
// takes too long is given up, and nothing is recorded.
func TestCommandWorkHonoursItsDeadline(t *testing.T) {
	resetRestartState(t)
	commandWorkLimit, commandReplyLimit = 50*time.Millisecond, 5*time.Second
	scheduleCommandRestart = func(ctx context.Context, _ time.Duration, _ string) error {
		<-ctx.Done()
		return errors.New("shutdown -r +5: no answer in time")
	}
	start := time.Now()
	r := restartCommandReply(reboot.Options{Mode: reboot.ModeAlways})
	if elapsed := time.Since(start); elapsed > time.Second || r.OK || !strings.Contains(r.Error, "no answer in time") {
		t.Fatalf("after %s: %+v", elapsed, r)
	}
	if _, ok := recordedRestart(); ok {
		t.Fatal("a restart that was not scheduled was recorded")
	}
}

// A command waits for the lock no longer than its limit.
func TestCommandDoesNotWaitForeverForTheLock(t *testing.T) {
	s := resetRestartState(t)
	commandWorkLimit, commandReplyLimit = 50*time.Millisecond, 5*time.Second
	recordRestart(time.Now().Add(time.Hour))
	restartGate.Lock()
	start := time.Now()
	restart := restartCommandReply(reboot.Options{Mode: reboot.ModeAlways})
	cancel := cancelCommandReply()
	restartGate.Unlock()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("waited %s", elapsed)
	}
	for _, r := range []restartReply{restart, cancel} {
		if r.OK || r.Error != errRestartBusy.Error() {
			t.Errorf("%s: %+v", r.Action, r)
		}
	}
	if s.calls != 0 || len(s.cancels) != 0 {
		t.Fatal("the host was touched without the lock")
	}
}

// The cancel's answers, as the server receives them.
func TestCancelCommandReply(t *testing.T) {
	s := resetRestartState(t)
	if r := cancelCommandReply(); r.OK || r.Action != "cancel" || r.Error != reboot.ErrNotScheduledByPatchMon.Error() {
		t.Fatalf("without a record: %+v", r)
	}
	recordRestart(time.Now().Add(time.Hour))
	s.cancelErr = reboot.ErrNothingScheduled
	if r := cancelCommandReply(); r.OK || r.Error != "no restart is scheduled on this host" {
		t.Fatalf("nothing pending: %+v", r)
	}
	recordRestart(time.Now().Add(time.Hour))
	s.cancelErr = nil
	if r := cancelCommandReply(); !r.OK || r.Error != "" {
		t.Fatalf("cancelled: %+v", r)
	}
}

// Restart commands are answered from the WebSocket reader itself, never
// through the service loop, so a busy loop neither delays nor drops them;
// the reader does not wait for the command either.
func TestRestartMessagesBypassTheServiceLoop(t *testing.T) {
	resetRestartState(t)
	replies := make(chan restartReply, 4)
	client := newWSTestClient(t, func(conn *websocket.Conn, _ <-chan struct{}) {
		for {
			_, b, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var r restartReply
			if json.Unmarshal(b, &r) == nil {
				replies <- r
			}
		}
	})
	globalWsConnMu.Lock()
	oldConn := globalWsConn
	globalWsConn = client
	globalWsConnMu.Unlock()
	t.Cleanup(func() {
		globalWsConnMu.Lock()
		globalWsConn = oldConn
		globalWsConnMu.Unlock()
	})
	next := func() restartReply {
		t.Helper()
		select {
		case r := <-replies:
			return r
		case <-time.After(5 * time.Second):
			t.Fatal("no answer")
			return restartReply{}
		}
	}

	handleRestartMessage("reboot", "req-1", &reboot.Options{Mode: reboot.ModeAlways, DelayMinutes: 30})
	if r := next(); r.Type != "reboot_result" || r.RequestID != "req-1" || r.Action != "reboot" || !r.OK {
		t.Fatalf("restart: %+v", r)
	}
	handleRestartMessage("reboot_cancel", "req-2", nil)
	if r := next(); r.RequestID != "req-2" || r.Action != "cancel" || !r.OK {
		t.Fatalf("cancel: %+v", r)
	}
	handleRestartMessage("reboot", "req-3", &reboot.Options{Mode: "now"})
	if r := next(); r.RequestID != "req-3" || r.OK || r.Error != "invalid restart options" {
		t.Fatalf("invalid options: %+v", r)
	}

	release := make(chan struct{})
	defer close(release)
	startRestartCommand = func(reboot.Options, string) { <-release }
	defer func() { startRestartCommand = handleRestartCommand }()
	done := make(chan struct{})
	go func() {
		handleRestartMessage("reboot", "req-4", &reboot.Options{Mode: reboot.ModeAlways})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the WebSocket reader waited for the restart command")
	}
}

// The cancel's refusal when the host cannot confirm the restart is
// PatchMon's carries the reason.
func TestCancelCommandReplyUnconfirmed(t *testing.T) {
	s := resetRestartState(t)
	recordRestart(time.Now().Add(time.Hour))
	s.cancelErr = fmt.Errorf("%w: the System event log could not be read: access denied", reboot.ErrOwnerUnconfirmed)
	r := cancelCommandReply()
	if r.OK || !strings.HasPrefix(r.Error, "PatchMon could not confirm that the restart pending on this host is the one it scheduled, so it is left alone") || !strings.Contains(r.Error, "access denied") {
		t.Fatalf("reply %+v", r)
	}
	if _, ok := recordedRestart(); !ok {
		t.Fatal("the record was dropped")
	}
}
