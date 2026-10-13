//go:build !windows

package commands

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"patchmon-agent/internal/reboot"
)

// The restart record as a Unix file: mode, owner and directory checks. On
// Windows the record is a state file whose trust rests on its owner, which a
// test cannot fake without running as SYSTEM (see statefile_windows_test.go).

// restartAgentMemory makes the agent read its record again, as after the
// agent restarted.
func restartAgentMemory() {
	restartGate.Lock()
	defer restartGate.Unlock()
	restartGate.record, restartGate.recordLoaded = nil, false
}

// The restart the agent scheduled is recorded on disk, readable by the
// agent's user alone, and still holds after the agent restarted.
func TestRestartRecordSurvivesAgentRestart(t *testing.T) {
	s := resetRestartState(t)
	at, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways, DelayMinutes: 30})
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(restartRecordPath())
	if err != nil || fi.Mode().Perm() != 0o600 || !fi.Mode().IsRegular() {
		t.Fatalf("record file: %v, %v", fi, err)
	}
	b, _ := os.ReadFile(restartRecordPath())
	if !strings.HasPrefix(string(b), `{"version":1,"at":"`+at.UTC().Format(time.RFC3339Nano)+`","scheduled_at":"`) {
		t.Fatalf("record = %s", b)
	}

	restartAgentMemory()
	if _, err := restartCommand(context.Background(), reboot.Options{Mode: reboot.ModeAlways}); err == nil || !strings.Contains(err.Error(), "already scheduled") {
		t.Fatalf("the record was lost with the agent's memory: %v", err)
	}
	if _, err := registerPatchRun("real", func() {}, false); err == nil {
		t.Fatal("a run started under the recorded restart")
	}

	restartAgentMemory()
	if err := cancelRestartCommand(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.cancels) != 1 || !s.cancels[0].At.Equal(at) {
		t.Fatalf("cancelled %+v, want the restart scheduled for %s", s.cancels, at)
	}
	if _, err := os.Lstat(restartRecordPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("record kept after the cancel: %v", err)
	}
}

// A record that cannot be trusted, or does not hold together, is dropped
// and holds nothing back.
func TestUntrustedRestartRecordIsDropped(t *testing.T) {
	// Soon enough to hold a real run back.
	at := time.Now().Add(10 * time.Minute).UTC()
	valid := `{"version":1,"at":"` + at.Format(time.RFC3339) + `","scheduled_at":"` + at.Add(-time.Hour).Format(time.RFC3339) + `"}`
	for _, tt := range []struct {
		name    string
		content string
		mode    os.FileMode
		link    bool
		trusted bool
	}{
		{name: "valid", content: valid, mode: 0o600, trusted: true},
		{name: "readable by others", content: valid, mode: 0o644},
		{name: "a link", content: valid, mode: 0o600, link: true},
		{name: "not JSON", content: "restart soon", mode: 0o600},
		{name: "unknown field", content: strings.Replace(valid, `"version":1`, `"version":1,"cmd":"x"`, 1), mode: 0o600},
		{name: "unknown version", content: strings.Replace(valid, `"version":1`, `"version":2`, 1), mode: 0o600},
		{name: "no time", content: `{"version":1}`, mode: 0o600},
		{name: "scheduled after its time", content: `{"version":1,"at":"` + at.Format(time.RFC3339) + `","scheduled_at":"` + at.Add(time.Hour).Format(time.RFC3339) + `"}`, mode: 0o600},
		{name: "too far ahead", content: `{"version":1,"at":"` + at.Format(time.RFC3339) + `","scheduled_at":"` + at.Add(-reboot.MaxDelay-time.Hour).Format(time.RFC3339) + `"}`, mode: 0o600},
		{name: "trailing data", content: valid + `{}`, mode: 0o600},
		{name: "too large", content: valid + strings.Repeat(" ", restartRecordMaxBytes), mode: 0o600},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetRestartState(t)
			path := restartRecordPath()
			target := path
			if tt.link {
				target = filepath.Join(t.TempDir(), "elsewhere.json")
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(target, []byte(tt.content), tt.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, tt.mode); err != nil {
				t.Fatal(err)
			}
			_, err := registerPatchRun("real", func() {}, false)
			if refused := err != nil; refused != tt.trusted {
				t.Fatalf("run refused: %v (%v), want %v", refused, err, tt.trusted)
			}
			if _, err := os.Lstat(path); (err == nil) != tt.trusted {
				t.Fatalf("record file kept: %v, want %v", err == nil, tt.trusted)
			}
			if tt.link {
				if _, err := os.Stat(target); err != nil {
					t.Fatalf("the link's target was removed: %v", err)
				}
			}
		})
	}
}

// The record is read from, and written to, a directory only the agent's
// user can write to.
func TestRestartRecordNeedsAPrivateDirectory(t *testing.T) {
	resetRestartState(t)
	dir := filepath.Dir(restartRecordPath())
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := writeRestartRecordFile(restartRecordPath(), []byte("{}")); err == nil {
		t.Fatal("the record was written to a directory others can write to")
	}
	if _, err := readRestartRecord(restartRecordPath()); err == nil || errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read from a directory others can write to: %v", err)
	}
}
