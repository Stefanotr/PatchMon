package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"patchmon-agent/internal/reboot"
)

type scheduledRestart struct {
	calls int
	delay time.Duration
	msg   string
	err   error
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
	restartGate.Unlock()
	patchRunCancels.Range(func(k, _ any) bool {
		patchRunCancels.Delete(k)
		return true
	})
	updateInFlight.Store(false)
	s := &scheduledRestart{}
	oldSchedule, oldCancel, oldPending := scheduleHostRestart, cancelHostRestart, osPendingRestart
	scheduleHostRestart = s.schedule
	osPendingRestart = func() (time.Time, bool) { return time.Time{}, false }
	t.Cleanup(func() {
		scheduleHostRestart, cancelHostRestart, osPendingRestart = oldSchedule, oldCancel, oldPending
		restartGate.Lock()
		restartGate.pendingAt = time.Time{}
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
	}{
		{name: "never: silent", opts: reboot.Options{Mode: reboot.ModeNever}, needed: true},
		{name: "unset: silent", opts: reboot.Options{}, needed: true},
		{name: "blocked", opts: reboot.Options{Mode: reboot.ModeAlways}, blocked: "the run was stopped", wantLine: "Restart: not scheduled, the run was stopped"},
		{name: "if needed, none pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded}, wantLine: "Restart: not needed"},
		{name: "if needed, pending", opts: reboot.Options{Mode: reboot.ModeIfNeeded, DelayMinutes: 10}, needed: true, wantCalls: 1, wantLine: "Restart: scheduled for"},
		{name: "always", opts: reboot.Options{Mode: reboot.ModeAlways}, wantCalls: 1, wantLine: "(in 5m0s)"},
		{name: "something else already pending", opts: reboot.Options{Mode: reboot.ModeAlways}, failWith: reboot.ErrAlreadyScheduled, wantCalls: 1, wantLine: "already pending on this host (not by this run)"},
		{name: "scheduling fails", opts: reboot.Options{Mode: reboot.ModeAlways}, failWith: errors.New("access denied"), wantCalls: 1, wantLine: "Restart: could not be scheduled: access denied", wantErr: true},
		{name: "time too far", opts: reboot.Options{Mode: reboot.ModeAlways, At: time.Now().Add(reboot.MaxDelay + time.Hour)}, wantLine: "more than", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetRestartState(t)
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
		})
	}
}

// A run's restart never cuts off another run, and never stacks on a restart
// already scheduled.
func TestScheduleRestartRespectsOtherRunsAndPendingRestarts(t *testing.T) {
	resetRestartState(t)
	always := reboot.Options{Mode: reboot.ModeAlways}

	patchRunCancels.Store("run-2", context.CancelFunc(func() {}))
	var out strings.Builder
	s := &scheduledRestart{}
	if err := scheduleRestart(&out, "run-1", always, false, "", s.schedule); err != nil || s.calls != 0 ||
		!strings.Contains(out.String(), "another patch run is in progress") {
		t.Fatalf("restarted under another run: calls %d, %q, %v", s.calls, out.String(), err)
	}
	patchRunCancels.Delete("run-2")

	patchRunCancels.Store("run-1", context.CancelFunc(func() {}))
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
	if err := registerPatchRun("real", noop, false); err == nil || !strings.Contains(err.Error(), "a restart is scheduled for") {
		t.Fatalf("real run started under a pending restart: %v", err)
	}
	if err := registerPatchRun("dry", noop, true); err != nil {
		t.Fatalf("dry run refused: %v", err)
	}
	patchRunCancels.Delete("dry")

	restartGate.pendingAt = time.Now().Add(patchRunTimeout() + time.Hour)
	if err := registerPatchRun("later", noop, false); err != nil {
		t.Fatalf("a restart after the run's time limit blocked it: %v", err)
	}
	patchRunCancels.Delete("later")

	// A restart long past its time did not happen and holds nothing back.
	restartGate.pendingAt = time.Now().Add(-time.Hour)
	if err := registerPatchRun("stale", noop, false); err != nil {
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
		err := registerPatchRun("real", noop, false)
		patchRunCancels.Delete("real")
		if (err != nil) != tt.refused {
			t.Errorf("%s: err = %v, refused want %v", tt.name, err, tt.refused)
		}
		if err := registerPatchRun("dry", noop, true); err != nil {
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
	if err := registerPatchRun("real", noop, false); err != nil {
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
		if err := registerPatchRun("run", noop, dry); err == nil || !strings.Contains(err.Error(), "updating itself") {
			t.Errorf("dry run %v started during an update: %v", dry, err)
		}
	}
	updateInFlight.Store(false)

	if err := patchRunBlocksUpdate(); err != nil {
		t.Fatalf("update refused with no run: %v", err)
	}
	if err := registerPatchRun("dry", noop, true); err != nil {
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

	patchRunCancels.Store("run", context.CancelFunc(func() {}))
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
