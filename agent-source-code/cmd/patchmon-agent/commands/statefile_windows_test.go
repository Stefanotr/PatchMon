//go:build windows

package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// Runs against the real Windows security APIs. Set
// PATCHMON_WINDOWS_INTEGRATION=1, from an elevated prompt; run it as SYSTEM
// (psexec -s) to also cover reading back a file the agent wrote.
func TestTrustedStateFileLive(t *testing.T) {
	if os.Getenv("PATCHMON_WINDOWS_INTEGRATION") != "1" {
		t.Skip("set PATCHMON_WINDOWS_INTEGRATION=1 to run the live state file test")
	}
	path := filepath.Join(t.TempDir(), "state.json")

	// A file planted by an ordinary account is not the agent's.
	if err := os.WriteFile(path, []byte(`{"planted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !runningAsSystem() {
		if f, err := openTrustedStateFile(path); err == nil {
			_ = f.Close()
			t.Fatal("a file owned by the test account was trusted")
		} else if !strings.Contains(err.Error(), "not by SYSTEM or Administrators") {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	// Writing replaces it with a file carrying only the restricted DACL.
	if err := writeTrustedStateFile(path, []byte(`{"format":1}`)); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if got := sd.String(); !strings.Contains(got, stateFileSDDL) {
		t.Errorf("DACL = %s, want %s", got, stateFileSDDL)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}

	if runningAsSystem() {
		f, err := openTrustedStateFile(path)
		if err != nil {
			t.Fatalf("the agent's own file is not trusted: %v", err)
		}
		defer func() { _ = f.Close() }()
		if b, _ := io.ReadAll(f); string(b) != `{"format":1}` {
			t.Errorf("read back %q", b)
		}
	}
}

func runningAsSystem() bool {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && u.User.Sid.IsWellKnown(windows.WinLocalSystemSid)
}
