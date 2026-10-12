//go:build !windows

package reboot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeShutdown installs a shutdown(8) stand-in running body after recording
// its arguments, one per line, so a test sees exactly the argument vector.
// The host looks like a SysV init one with nothing pending; fakeSystemd
// turns it into a systemd one.
func fakeShutdown(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> '" + log + "'; done\n" + body + "\n"
	bin := filepath.Join(dir, "shutdown")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	proc := filepath.Join(dir, "proc")
	if err := os.Mkdir(proc, 0o755); err != nil {
		t.Fatal(err)
	}
	oldCandidates, oldSettle, oldCancel := shutdownCandidates, shutdownSettle, cancelSettle
	oldRun, oldFile, oldProc := systemdRunDir, systemdScheduledFile, procDir
	shutdownCandidates = []string{filepath.Join(dir, "missing"), bin}
	shutdownSettle, cancelSettle = 2*time.Second, time.Second
	systemdRunDir = filepath.Join(dir, "no-systemd")
	systemdScheduledFile = filepath.Join(dir, "scheduled")
	procDir = proc
	t.Cleanup(func() {
		shutdownCandidates, shutdownSettle, cancelSettle = oldCandidates, oldSettle, oldCancel
		systemdRunDir, systemdScheduledFile, procDir = oldRun, oldFile, oldProc
	})
	return log
}

// fakeSystemd makes the host a systemd one; it returns logind's record of a
// scheduled shutdown, which does not exist yet.
func fakeSystemd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	systemdRunDir = dir
	return systemdScheduledFile
}

// fakeShutdownProcess shows a shutdown(8) process waiting for its time.
func fakeShutdownProcess(t *testing.T) {
	t.Helper()
	p := filepath.Join(procDir, "4242")
	if err := os.Mkdir(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "comm"), []byte("shutdown\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScheduleArgumentVector(t *testing.T) {
	log := fakeShutdown(t, "exit 0")
	msg := (Options{Message: "it's \"maintenance\"; $(id)"}).ShutdownMessage()
	if err := Schedule(context.Background(), 5*time.Minute+time.Second, msg); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	if want := "-r\n+6\n" + msg + "\n"; string(got) != want {
		t.Fatalf("argv = %q, want %q", got, want)
	}
}

func TestScheduleReportsFailure(t *testing.T) {
	fakeShutdown(t, "echo 'shutdown says no'; exit 1")
	err := Schedule(context.Background(), MinDelay, "PatchMon: x")
	if err == nil || !strings.Contains(err.Error(), "shutdown says no") {
		t.Fatalf("err = %v, want the failure with shutdown's output", err)
	}
}

// FreeBSD's shutdown forks a child that keeps its output open until the
// restart; the call must not wait for it.
func TestScheduleDoesNotWaitForAForkedChild(t *testing.T) {
	fakeShutdown(t, "(sleep 30; echo late) &\nexit 0")
	start := time.Now()
	if err := Schedule(context.Background(), MinDelay, "PatchMon: x"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Schedule waited %s for the forked child", elapsed)
	}
}

// SysV init's shutdown counts down in the foreground: still running after
// the settle time, it is the pending restart and is left alone.
func TestScheduleLeavesAForegroundCountdownRunning(t *testing.T) {
	log := fakeShutdown(t, "sleep 30")
	start := time.Now()
	if err := Schedule(context.Background(), MinDelay, "PatchMon: x"); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > shutdownSettle+time.Second {
		t.Fatalf("Schedule waited %s", elapsed)
	}
	if got, _ := os.ReadFile(log); !strings.HasPrefix(string(got), "-r\n") {
		t.Fatalf("shutdown not run: %q", got)
	}
}

func TestShutdownNotFound(t *testing.T) {
	old := shutdownCandidates
	shutdownCandidates = []string{filepath.Join(t.TempDir(), "none")}
	defer func() { shutdownCandidates = old }()
	if err := Schedule(context.Background(), MinDelay, "PatchMon: x"); err == nil {
		t.Fatal("no error without shutdown(8)")
	}
}

func TestScheduleRefusesWhenAShutdownIsPending(t *testing.T) {
	t.Run("sysv", func(t *testing.T) {
		log := fakeShutdown(t, "exit 0")
		fakeShutdownProcess(t)
		if err := Schedule(context.Background(), MinDelay, "PatchMon: x"); !errors.Is(err, ErrAlreadyScheduled) {
			t.Fatalf("err = %v, want ErrAlreadyScheduled", err)
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("shutdown(8) was run")
		}
	})
	t.Run("systemd", func(t *testing.T) {
		log := fakeShutdown(t, "exit 0")
		file := fakeSystemd(t)
		at := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
		body := fmt.Sprintf("USEC=%d\nWARN_WALL=1\nMODE=poweroff\n", at.UnixMicro())
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, ok := Pending(); !ok || !got.Equal(at) {
			t.Fatalf("Pending = %s, %v; want %s", got, ok, at)
		}
		if err := Schedule(context.Background(), MinDelay, "PatchMon: x"); !errors.Is(err, ErrAlreadyScheduled) {
			t.Fatalf("err = %v, want ErrAlreadyScheduled", err)
		}
		if _, err := os.Stat(log); err == nil {
			t.Fatal("shutdown(8) was run")
		}
	})
}

// On systemd, shutdown(8) must answer: one that hangs has not scheduled
// anything for sure.
func TestScheduleOnSystemdWaitsForTheAnswer(t *testing.T) {
	fakeShutdown(t, "sleep 30")
	fakeSystemd(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := Schedule(ctx, MinDelay, "PatchMon: x"); err == nil {
		t.Fatal("a shutdown(8) that never answered counted as scheduled")
	}
}

func TestCancelWithNothingPending(t *testing.T) {
	log := fakeShutdown(t, "exit 0")
	if err := Cancel(context.Background()); !errors.Is(err, ErrNothingScheduled) {
		t.Fatalf("err = %v, want ErrNothingScheduled", err)
	}
	if _, err := os.Stat(log); err == nil {
		t.Fatal("shutdown(8) was run")
	}
}

func TestCancelOnSystemd(t *testing.T) {
	file := ""
	t.Run("done", func(t *testing.T) {
		log := fakeShutdown(t, "")
		file = fakeSystemd(t)
		if err := os.WriteFile(file, []byte("USEC=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// The fake cancels the way logind does: by removing the record.
		if err := os.WriteFile(shutdownCandidates[1], []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> '"+log+"'\nrm -f '"+file+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Cancel(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(log); string(got) != "-c\n" {
			t.Fatalf("argv = %q", got)
		}
	})
	t.Run("still pending", func(t *testing.T) {
		fakeShutdown(t, "exit 0")
		file = fakeSystemd(t)
		if err := os.WriteFile(file, []byte("USEC=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Cancel(context.Background()); err == nil {
			t.Fatal("a restart still pending was reported cancelled")
		}
	})
	t.Run("no answer", func(t *testing.T) {
		fakeShutdown(t, "sleep 30")
		file = fakeSystemd(t)
		if err := os.WriteFile(file, []byte("USEC=1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := Cancel(ctx); err == nil {
			t.Fatal("a shutdown -c that never answered counted as done")
		}
	})
}
