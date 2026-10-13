//go:build windows

package reboot

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeWindows fakes the host's boot time and its System event log.
func fakeWindows(t *testing.T, booted time.Time, events []shutdownEvent, err error) {
	t.Helper()
	oldBoot, oldEvents := bootTime, shutdownEvents
	bootTime = func() time.Time { return booted }
	shutdownEvents = func(context.Context, time.Time) ([]shutdownEvent, error) { return events, err }
	t.Cleanup(func() { bootTime, shutdownEvents = oldBoot, oldEvents })
}

// Ownership comes from the event log; none of these cases reaches
// AbortSystemShutdown.
func TestPendingOwnerAndCancelOnWindows(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s := Scheduled{At: now.Add(time.Hour), Since: now.Add(-time.Minute)}
	longAgo := now.Add(-24 * time.Hour)

	fakeWindows(t, longAgo, []shutdownEvent{initiated(10, patchMonComment)}, nil)
	if got := PendingOwner(ctx, s); got != OwnerPatchMon {
		t.Errorf("owner = %v, want PatchMon", got)
	}
	past := Scheduled{At: now.Add(-time.Minute), Since: now.Add(-time.Hour)}
	if err := CancelScheduled(ctx, past); !errors.Is(err, ErrNotScheduledByPatchMon) {
		t.Errorf("past restart: err = %v, want ErrNotScheduledByPatchMon", err)
	}

	// shutdown /a, then shutdown /r /t 7200 by an administrator.
	fakeWindows(t, longAgo, []shutdownEvent{initiated(10, patchMonComment), aborted(11), initiated(12, "")}, nil)
	if err := CancelScheduled(ctx, s); !errors.Is(err, ErrNotScheduledByPatchMon) {
		t.Errorf("administrator's restart: err = %v, want ErrNotScheduledByPatchMon", err)
	}

	fakeWindows(t, longAgo, nil, errors.New("access denied"))
	if got := PendingOwner(ctx, s); got != OwnerUnknown {
		t.Errorf("unreadable log: owner = %v, want unknown", got)
	}
	if err := CancelScheduled(ctx, s); !errors.Is(err, ErrOwnerUnconfirmed) || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("unreadable log: err = %v, want ErrOwnerUnconfirmed with the reason", err)
	}

	fakeWindows(t, now.Add(-time.Second), nil, errors.New("not read"))
	if got := PendingOwner(ctx, s); got != OwnerNone {
		t.Errorf("after a restart: owner = %v, want none", got)
	}
}

// Runs the query against the real System log. Set
// PATCHMON_WINDOWS_INTEGRATION=1.
func TestShutdownEventsLive(t *testing.T) {
	if os.Getenv("PATCHMON_WINDOWS_INTEGRATION") != "1" {
		t.Skip("set PATCHMON_WINDOWS_INTEGRATION=1 to query the real System event log")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	events, err := shutdownEvents(ctx, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.ID != eventShutdownInitiated && e.ID != eventShutdownAborted {
			t.Errorf("unexpected event %+v", e)
		}
	}
	t.Logf("%d event(s)", len(events))
}
