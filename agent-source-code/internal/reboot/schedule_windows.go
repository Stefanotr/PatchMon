//go:build windows

package reboot

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

// AbortSystemShutdownW is not wrapped by x/sys/windows.
var procAbortSystemShutdown = windows.NewLazySystemDLL("advapi32.dll").NewProc("AbortSystemShutdownW")

// bootTime is when the host started, a variable so tests can fake it.
var bootTime = func() time.Time { return time.Now().Add(-windows.DurationSinceBoot()) }

// Pending reports a restart the operating system has scheduled. Windows has
// no call to ask: Schedule learns of one when InitiateSystemShutdownEx
// fails with ERROR_SHUTDOWN_IN_PROGRESS, and CancelScheduled from
// AbortSystemShutdown.
func Pending() (at time.Time, ok bool) {
	return time.Time{}, false
}

// PendingOwner reports whether the restart pending is s, the one PatchMon
// recorded. Windows does not tell what is pending: s is gone once the host
// restarted after scheduling it, and unknown otherwise.
func PendingOwner(s Scheduled) Owner {
	if bootTime().After(s.Since) {
		return OwnerNone
	}
	return OwnerUnknown
}

// Schedule asks Windows to restart the host after d. Windows shows msg and the
// countdown to signed-in users, then closes applications and restarts. The
// restart is recorded as planned, for a security fix, in the event log.
func Schedule(_ context.Context, d time.Duration, msg string) error {
	if err := enableShutdownPrivilege(); err != nil {
		return fmt.Errorf("enable the shutdown privilege: %w", err)
	}
	text, err := windows.UTF16PtrFromString(msg)
	if err != nil {
		return err
	}
	reason := uint32(windows.SHTDN_REASON_MAJOR_OPERATINGSYSTEM | windows.SHTDN_REASON_MINOR_SECURITYFIX | windows.SHTDN_REASON_FLAG_PLANNED)
	err = windows.InitiateSystemShutdownEx(nil, text, uint32(d/time.Second), true, true, reason)
	if errors.Is(err, windows.ERROR_SHUTDOWN_IN_PROGRESS) {
		return ErrAlreadyScheduled
	}
	return err
}

// CancelScheduled aborts the restart s, PatchMon's. InitiateSystemShutdownEx
// cannot be stacked, so until s.At, on a host that has not restarted since
// s was scheduled, the shutdown Windows has pending, if any, is s. Otherwise
// it returns ErrNotScheduledByPatchMon without aborting anything.
func CancelScheduled(ctx context.Context, s Scheduled) error {
	if PendingOwner(s) == OwnerNone || !time.Now().Before(s.At) {
		return ErrNotScheduledByPatchMon
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := enableShutdownPrivilege(); err != nil {
		return fmt.Errorf("enable the shutdown privilege: %w", err)
	}
	if ok, _, err := procAbortSystemShutdown.Call(0); ok == 0 {
		if errors.Is(err, windows.ERROR_NO_SHUTDOWN_IN_PROGRESS) {
			return ErrNothingScheduled
		}
		return err
	}
	return nil
}

// enableShutdownPrivilege enables SeShutdownPrivilege, which LocalSystem
// holds but not enabled. Without it the calls above fail with
// ERROR_PRIVILEGE_NOT_HELD, which is reported as is.
func enableShutdownPrivilege() error {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		return err
	}
	defer func() { _ = token.Close() }()
	name, err := windows.UTF16PtrFromString("SeShutdownPrivilege")
	if err != nil {
		return err
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return err
	}
	privileges := windows.Tokenprivileges{PrivilegeCount: 1}
	privileges.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED}
	return windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil)
}
