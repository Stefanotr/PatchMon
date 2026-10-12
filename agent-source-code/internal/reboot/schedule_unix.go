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
	"slices"
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
	// psPath lists processes on FreeBSD, which has no /proc by default.
	psPath = "/bin/ps"
	// processListTimeout bounds listing processes with ps(1).
	processListTimeout = 5 * time.Second
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

// usesLogind reports a host where logind keeps scheduled shutdowns.
func usesLogind() bool {
	return runtime.GOOS == "linux" && isSystemd()
}

// Pending reports a restart or shutdown the operating system has scheduled,
// whoever asked for it. at is its time when the system records it
// (systemd), zero otherwise. A state it cannot read counts as pending:
// guessing "none" could let a restart replace one an administrator
// scheduled.
func Pending() (at time.Time, ok bool) {
	if usesLogind() {
		at, _, exists, _ := logindSchedule()
		return at, exists
	}
	procs, err := shutdownProcesses()
	return time.Time{}, err != nil || len(procs) > 0
}

// PendingOwner reports whether the restart the operating system has pending
// is s, the one PatchMon recorded: on systemd, logind's record holds a
// restart within a couple of minutes of s.At; on SysV init and FreeBSD,
// every shutdown(8) process waiting for its time carries the PatchMon
// message, which Schedule always passes.
func PendingOwner(s Scheduled) Owner {
	owner, _ := pendingOwner(s)
	return owner
}

// pendingOwner is PendingOwner, with the shutdown(8) processes it judged.
func pendingOwner(s Scheduled) (Owner, []shutdownProcess) {
	switch {
	case usesLogind():
		return logindOwner(s), nil
	case runtime.GOOS == "linux" || runtime.GOOS == "freebsd":
		return processOwner()
	}
	return OwnerUnknown, nil
}

func logindOwner(s Scheduled) Owner {
	at, mode, exists, err := logindSchedule()
	switch {
	case !exists:
		return OwnerNone
	case err != nil || at.IsZero():
		return OwnerUnknown
	case mode != "" && mode != "reboot":
		return OwnerOther
	case at.Sub(s.At).Abs() <= scheduleTolerance:
		return OwnerPatchMon
	}
	return OwnerOther
}

// logindSchedule reads logind's record of a scheduled shutdown: its time
// (zero when the record does not say) and mode. exists is false when no
// shutdown is scheduled, true with err when the record cannot be read.
func logindSchedule() (at time.Time, mode string, exists bool, err error) {
	b, err := os.ReadFile(systemdScheduledFile)
	if errors.Is(err, fs.ErrNotExist) {
		return time.Time{}, "", false, nil
	}
	if err != nil {
		return time.Time{}, "", true, err
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "USEC":
			if us, err := strconv.ParseInt(value, 10, 64); err == nil && us > 0 {
				at = time.UnixMicro(us)
			}
		case "MODE":
			mode = value
		}
	}
	return at, mode, true, nil
}

// processOwner judges the shutdown(8) processes waiting for their time: the
// pending restart is PatchMon's when every one of them carries its message,
// someone else's when none does. Both at once (FreeBSD runs several) cannot
// be cancelled apart: "shutdown -c" or the wait for nothing to be pending
// would take the other one along.
func processOwner() (Owner, []shutdownProcess) {
	procs, err := shutdownProcesses()
	switch {
	case err != nil:
		return OwnerUnknown, nil
	case len(procs) == 0:
		return OwnerNone, nil
	}
	ours := 0
	for _, p := range procs {
		if p.patchMon {
			ours++
		}
	}
	switch ours {
	case len(procs):
		return OwnerPatchMon, procs
	case 0:
		return OwnerOther, procs
	}
	return OwnerUnknown, procs
}

// shutdownProcess is a shutdown(8) process waiting for its time: how SysV
// init and FreeBSD keep a scheduled shutdown.
type shutdownProcess struct {
	pid int
	// patchMon: its command line carries the PatchMon message.
	patchMon bool
}

func shutdownProcesses() ([]shutdownProcess, error) {
	switch runtime.GOOS {
	case "linux":
		return procShutdownProcesses()
	case "freebsd":
		return psShutdownProcesses()
	}
	return nil, nil
}

// procShutdownProcesses reads them from /proc.
func procShutdownProcesses() ([]shutdownProcess, error) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil, err
	}
	var procs []shutdownProcess
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join(procDir, e.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "shutdown" {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join(procDir, e.Name(), "cmdline"))
		args := strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")
		patchMon := len(args) > 1 && slices.ContainsFunc(args[1:], func(a string) bool {
			return strings.HasPrefix(a, messagePrefix)
		})
		procs = append(procs, shutdownProcess{pid: pid, patchMon: patchMon})
	}
	return procs, nil
}

// psShutdownProcesses reads them from ps(1).
func psShutdownProcesses() ([]shutdownProcess, error) {
	ctx, cancel := context.WithTimeout(context.Background(), processListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, psPath, "-ax", "-ww", "-o", "pid=", "-o", "comm=", "-o", "args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	return parseShutdownProcesses(out), nil
}

// parseShutdownProcesses reads ps(1) lines of pid, command name and
// arguments. ps joins the arguments with spaces, so the message is found as
// its first word.
func parseShutdownProcesses(out []byte) []shutdownProcess {
	word := strings.TrimSpace(messagePrefix)
	var procs []shutdownProcess
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[1] != "shutdown" {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil || pid <= 0 {
			continue
		}
		// f[2] is the program, the arguments follow.
		procs = append(procs, shutdownProcess{pid: pid, patchMon: len(f) > 3 && slices.Contains(f[3:], word)})
	}
	return procs
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
	countdown := !usesLogind()
	if err := runShutdown(ctx, countdown, "-r", fmt.Sprintf("+%d", minutes), msg); err != nil {
		return fmt.Errorf("shutdown -r +%d: %w", minutes, err)
	}
	return nil
}

// CancelScheduled cancels the restart s, PatchMon's, and nothing else (see
// PendingOwner): "shutdown -c" on Linux; on FreeBSD, where shutdown has no
// such option, by stopping the shutdown processes that carry the PatchMon
// message, as shutdown(8) documents. It returns ErrNotScheduledByPatchMon
// when what is pending is not s, or cannot be told apart, and only reports
// success once the system no longer has a restart pending.
func CancelScheduled(ctx context.Context, s Scheduled) error {
	owner, procs := pendingOwner(s)
	switch owner {
	case OwnerNone:
		return ErrNothingScheduled
	case OwnerPatchMon:
	default:
		return ErrNotScheduledByPatchMon
	}
	switch runtime.GOOS {
	case "linux":
		if err := runShutdown(ctx, false, "-c"); err != nil {
			return fmt.Errorf("shutdown -c: %w", err)
		}
	case "freebsd":
		for _, p := range procs {
			if err := syscall.Kill(p.pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("stop shutdown process %d: %w", p.pid, err)
			}
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
	// Out of time already: not started, rather than started and killed
	// before anyone knows what it did.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("not run, no time left: %w", err)
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
