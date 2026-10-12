//go:build windows

package reboot

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Windows does not tell what is pending: the recorded restart is gone once
// the host restarted, and is not aborted once its time has passed. Neither
// case reaches AbortSystemShutdown.
func TestPendingOwnerAndCancelOnWindows(t *testing.T) {
	old := bootTime
	defer func() { bootTime = old }()
	now := time.Now()
	s := Scheduled{At: now.Add(time.Hour), Since: now.Add(-time.Minute)}

	bootTime = func() time.Time { return now.Add(-24 * time.Hour) }
	if got := PendingOwner(s); got != OwnerUnknown {
		t.Errorf("owner = %v, want unknown", got)
	}
	past := Scheduled{At: now.Add(-time.Minute), Since: now.Add(-time.Hour)}
	if err := CancelScheduled(context.Background(), past); !errors.Is(err, ErrNotScheduledByPatchMon) {
		t.Errorf("past restart: err = %v, want ErrNotScheduledByPatchMon", err)
	}

	bootTime = func() time.Time { return now.Add(-time.Second) }
	if got := PendingOwner(s); got != OwnerNone {
		t.Errorf("after a restart: owner = %v, want none", got)
	}
	if err := CancelScheduled(context.Background(), s); !errors.Is(err, ErrNotScheduledByPatchMon) {
		t.Errorf("after a restart: err = %v, want ErrNotScheduledByPatchMon", err)
	}
}
