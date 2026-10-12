package commands

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"patchmon-agent/internal/config"
	"patchmon-agent/internal/reboot"
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
}

func (s *scheduledRestart) report(runID string, _ time.Time, err error) {
	if err != nil {
		s.reports = append(s.reports, runID+" "+err.Error())
		return
	}
	s.reports = append(s.reports, runID+" ok")
}

func (s *scheduledRestart) schedule(d time.Duration, msg string) error {
	s.calls++
	s.delay, s.msg = d, msg
	return s.err
}

// resetRestartState clears what earlier tests left behind and fakes the
// operating system for the command paths.
func resetRestartState(t *testing.T) *scheduledRestart {
	t.Helper()
	restartGate.Lock()
	restartGate.pendingAt = time.Time{}
	restartGate.agentRestartWaiting = false
	restartGate.Unlock()
	patchRunCancels.Range(func(k, _ any) bool {
		patchRunCancels.Delete(k)
		return true
	})
	updateInFlight.Store(false)
	s := &scheduledRestart{}
	oldSchedule, oldCancel, oldPending, oldReport := scheduleHostRestart, cancelHostRestart, osPendingRestart, reportRunRestart
	oldAgentRestart, oldAllowed := restartAgentService, remoteRestartsAllowed
	remoteRestartsAllowed = func() bool { return true }
	scheduleHostRestart = s.schedule
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, false }
	reportRunRestart = s.report
	restartAgentService = func() error {
		s.agentRestarts.Add(1)
		return s.agentRestartErr
	}
	t.Cleanup(func() {
		scheduleHostRestart, cancelHostRestart, osPendingRestart, reportRunRestart = oldSchedule, oldCancel, oldPending, oldReport
		restartAgentService, remoteRestartsAllowed = oldAgentRestart, oldAllowed
		restartGate.Lock()
		restartGate.pendingAt = time.Time{}
		restartGate.agentRestartWaiting = false
		restartGate.Unlock()
	})
	return s
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
		// wantReport is what reaches the server: "" (nothing), "ok", or an error.
		wantReport string
	}{
		{name: "never: silent", opts: reboot.Options{Mode: reboot.ModeNever}, needed: true},
		{name: "unset: silent", opts: reboot.Options{}, needed: true},
		{name: "blocked", opts: reboot.Options{Mode: reboot.ModeAlways}, blocked: "the run was stopped", wantLine: "Restart: not scheduled, the run was stopped"},
		{name: "if needed, none pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded}, wantLine: "Restart: not needed"},
		{name: "if needed, pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded, DelayMinutes: 10}, needed: true, wantCalls: 1, wantLine: "Restart: scheduled for", wantReport: "ok"},
		{name: "always", opts: reboot.Options{Mode: reboot.ModeAlways}, wantCalls: 1, wantLine: "(in 5m0s)", wantReport: "ok"},
		{name: "something else already pending", opts: reboot.Options{Mode: reboot.ModeAlways}, failWith: reboot.ErrAlreadyScheduled, wantCalls: 1, wantLine: "already pending on this host (not by this run)"},
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
			}
		})
	}
}

// A run's restart never cuts off another run, and never stacks on a restart
// already scheduled.
func TestScheduleRestartRespectsOtherRunsAndPendingRestarts(t *testing.T) {
	resetRestartState(t)
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
}

// A real run does not start when a scheduled restart would cut it off.
func TestRegisterPatchRunAndPendingRestart(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})

	restartGate.pendingAt = time.Now().Add(10 * time.Minute)
	if _, err := registerPatchRun("real", noop, false); err == nil || !strings.Contains(err.Error(), "a restart is scheduled for") {
		t.Fatalf("real run started under a pending restart: %v", err)
	}
	if _, err := registerPatchRun("dry", noop, true); err != nil {
		t.Fatalf("dry run refused: %v", err)
	}
	patchRunCancels.Delete("dry")

	restartGate.pendingAt = time.Now().Add(patchRunTimeout() + time.Hour)
	if _, err := registerPatchRun("later", noop, false); err != nil {
		t.Fatalf("a restart after the run's time limit blocked it: %v", err)
	}
	patchRunCancels.Delete("later")

	// A restart long past its time did not happen and holds nothing back.
	restartGate.pendingAt = time.Now().Add(-time.Hour)
	if _, err := registerPatchRun("stale", noop, false); err != nil {
		t.Fatalf("a stale restart blocked the run: %v", err)
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
// scheduled is judged by the time it recorded.
func TestRegisterPatchRunWithItsOwnRestartOfUnknownTime(t *testing.T) {
	resetRestartState(t)
	noop := context.CancelFunc(func() {})
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, true }
	restartGate.pendingAt = time.Now().Add(patchRunTimeout() + time.Hour)
	if _, err := registerPatchRun("real", noop, false); err != nil {
		t.Fatalf("run refused under its own far restart: %v", err)
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
	_, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways})
	if !errors.Is(err, reboot.ErrAlreadyScheduled) || !strings.Contains(err.Error(), "cancel it first") {
		t.Fatalf("err = %v", err)
	}
	restartGate.Lock()
	defer restartGate.Unlock()
	if !restartGate.pendingAt.IsZero() {
		t.Error("a restart that was not scheduled is recorded as pending")
	}
}

func TestRestartCommand(t *testing.T) {
	s := resetRestartState(t)

	if _, err := restartCommand(reboot.Options{Mode: reboot.ModeIfNeeded}); err == nil {
		t.Error("a command without mode always was accepted")
	}

	patchRunCancels.Store("run", &patchRun{cancel: func() {}})
	if _, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways}); err == nil || !strings.Contains(err.Error(), "patch run is in progress") {
		t.Fatalf("restart scheduled during a run: %v", err)
	}
	patchRunCancels.Delete("run")

	at, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways, DelayMinutes: 30})
	if err != nil || s.calls != 1 || time.Until(at) < 29*time.Minute {
		t.Fatalf("restart: at %s, err %v, calls %d", at, err, s.calls)
	}
	if _, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways}); err == nil || !strings.Contains(err.Error(), "already scheduled") {
		t.Fatalf("second restart accepted: %v", err)
	}

	cancelHostRestart = func() error { return nil }
	if err := cancelRestartCommand(); err != nil {
		t.Fatal(err)
	}
	if _, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways}); err != nil {
		t.Fatalf("restart refused after the cancel: %v", err)
	}

	cancelHostRestart = func() error { return errors.New("not supported") }
	if err := cancelRestartCommand(); err == nil {
		t.Fatal("cancel failure swallowed")
	}
	restartGate.Lock()
	stillPending := !restartGate.pendingAt.IsZero()
	restartGate.Unlock()
	if !stillPending {
		t.Error("a failed cancel forgot the pending restart")
	}
}

// A run_patch delivered twice leaves the real run alone: its entry, what a
// stop request reaches, and what the server hears about it.
func TestDuplicatePatchRunIsIgnored(t *testing.T) {
	resetRestartState(t)
	stopped := false
	real, err := registerPatchRun("run-1", func() { stopped = true }, false)
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
	if !ok || got != real {
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
	unregisterPatchRun("run-1", real)
	if _, ok := lookupPatchRun("run-1"); ok {
		t.Fatal("the run's entry was not removed")
	}
	again, err := registerPatchRun("run-1", func() {}, false)
	if err != nil {
		t.Fatal(err)
	}
	unregisterPatchRun("run-1", real)
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
	real, err := registerPatchRun("real", noop, false)
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
	unregisterPatchRun("real", real)
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

	_, err := restartCommand(reboot.Options{Mode: reboot.ModeAlways})
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

	cancelHostRestart = func() error { return nil }
	if err := cancelRestartCommand(); err != nil {
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
