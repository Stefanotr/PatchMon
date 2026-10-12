//go:build !windows

package reboot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Paths and timings, variables so tests can point them at fakes.
var (
	// shutdownCandidates are the places shutdown(8) lives. A fixed list
	// rather than a PATH lookup: the agent runs as root.
	shutdownCandidates = []string{"/sbin/shutdown", "/usr/sbin/shutdown", "/bin/shutdown", "/usr/bin/shutdown"}
	// shutdownSettle is how long a scheduling shutdown(8) gets to exit
	// where it may count down in the foreground instead (SysV init). One
	// still running after this is the pending restart and is left to run.
	shutdownSettle = 5 * time.Second
	// cancelSettle is how long a cancelled restart gets to disappear.
	cancelSettle = 5 * time.Second
	// systemdRunDir exists when systemd is the init system (sd_booted).
	systemdRunDir = "/run/systemd/system"
	// systemdScheduledFile is where logind records a scheduled shutdown.
	systemdScheduledFile = "/run/systemd/shutdown/scheduled"
	procDir              = "/proc"
	pgrepPath            = "/bin/pgrep"
)

func shutdownPath() (string, error) {
	for _, p := range shutdownCandidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return p, nil
		}
	}
	return "", errors.New("shutdown(8) not found")
}

func isSystemd() bool {
	fi, err := os.Stat(systemdRunDir)
	return err == nil && fi.IsDir()
}

// Pending reports a restart or shutdown the operating system has scheduled,
// whoever asked for it. at is its time when the system records it
// (systemd), zero otherwise.
func Pending() (at time.Time, ok bool) {
	if runtime.GOOS == "linux" && isSystemd() {
		return systemdPending()
	}
	return time.Time{}, shutdownProcessRunning()
}

// systemdPending reads logind's record of a scheduled shutdown. A record it
// cannot read counts as pending: guessing "none" could let a restart replace
// one an administrator scheduled.
func systemdPending() (time.Time, bool) {
	b, err := os.ReadFile(systemdScheduledFile)
	if err != nil {
		return time.Time{}, !errors.Is(err, fs.ErrNotExist)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, found := strings.CutPrefix(strings.TrimSpace(line), "USEC="); found {
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && us > 0 {
				return time.UnixMicro(us), true
			}
		}
	}
	return time.Time{}, true
}

// shutdownProcessRunning reports a shutdown(8) process waiting for its time:
// how SysV init and FreeBSD keep a scheduled shutdown.
func shutdownProcessRunning() bool {
	switch runtime.GOOS {
	case "linux":
		entries, err := os.ReadDir(procDir)
		if err != nil {
			return false
		}
		for _, e := range entries {
			if _, err := strconv.Atoi(e.Name()); err != nil {
				continue
			}
			comm, err := os.ReadFile(filepath.Join(procDir, e.Name(), "comm"))
			if err == nil && strings.TrimSpace(string(comm)) == "shutdown" {
				return true
			}
		}
		return false
	case "freebsd":
		// pgrep exits 0 when a process matched, 1 when none did.
		return exec.Command(pgrepPath, "-x", "shutdown").Run() == nil
	default:
		return false
	}
}

// Schedule runs "shutdown -r +N message", N being d in whole minutes rounded
// up. shutdown warns signed-in users with the message. It refuses when a
// restart or shutdown is already pending: systemd would silently replace it,
// FreeBSD would run both.
func Schedule(ctx context.Context, d time.Duration, msg string) error {
	if _, ok := Pending(); ok {
		return ErrAlreadyScheduled
	}
	minutes := int((d + time.Minute - 1) / time.Minute)
	// systemd hands the restart to logind and returns, so it must exit
	// cleanly. SysV init's shutdown counts down in the foreground; FreeBSD's
	// forks a child that does and returns at once.
	countdown := runtime.GOOS != "linux" || !isSystemd()
	if err := runShutdown(ctx, countdown, "-r", fmt.Sprintf("+%d", minutes), msg); err != nil {
		return fmt.Errorf("shutdown -r +%d: %w", minutes, err)
	}
	return nil
}

// Cancel cancels a pending restart, whoever scheduled it: "shutdown -c" on
// Linux; on FreeBSD, where shutdown has no such option, by stopping the
// shutdown process that waits for the time, as shutdown(8) documents. It
// only reports success once the system no longer has one pending.
func Cancel(ctx context.Context) error {
	if _, ok := Pending(); !ok {
		return ErrNothingScheduled
	}
	switch runtime.GOOS {
	case "linux":
		if err := runShutdown(ctx, false, "-c"); err != nil {
			return fmt.Errorf("shutdown -c: %w", err)
		}
	case "freebsd":
		err := exec.CommandContext(ctx, "/bin/pkill", "-TERM", "-x", "shutdown").Run()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return ErrNothingScheduled // pkill: no process matched
		}
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("cancelling a scheduled restart is not supported on %s", runtime.GOOS)
	}
	deadline := time.Now().Add(cancelSettle)
	for {
		if _, ok := Pending(); !ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the restart is still pending after the cancel")
		}
		select {
		case <-ctx.Done():
			return errors.New("the restart is still pending after the cancel")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// runShutdown runs shutdown(8) without waiting on its output: its output
// goes to a file, not a pipe, so a child that keeps it open (FreeBSD forks
// one that lives until the restart) cannot hold the call up, and it runs in
// its own process group, out of reach of signals sent to the agent's.
//
// countdown says that a shutdown still running after shutdownSettle is the
// pending restart. Otherwise it must exit cleanly before ctx is done, or
// the call fails.
func runShutdown(ctx context.Context, countdown bool, args ...string) error {
	bin, err := shutdownPath()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp("", "patchmon-shutdown-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = out.Close()
		_ = os.Remove(out.Name())
	}()
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var settled <-chan time.Time
	if countdown {
		settled = time.After(shutdownSettle)
	}
	select {
	case err = <-done:
	case <-settled:
		return nil // counting down in the foreground: that is the restart
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return fmt.Errorf("no answer in time%s", outputHint(out))
	}
	if err != nil {
		return fmt.Errorf("%w%s", err, outputHint(out))
	}
	return nil
}

func outputHint(f *os.File) string {
	b := make([]byte, 512)
	n, _ := f.ReadAt(b, 0)
	if n == 0 {
		return ""
	}
	s := strings.Join(strings.Fields(string(b[:n])), " ")
	if s == "" {
		return ""
	}
	return ": " + s
}
