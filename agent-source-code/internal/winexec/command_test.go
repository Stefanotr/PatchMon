package winexec

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPowerShellIsStartedByAbsolutePath(t *testing.T) {
	cmd := PowerShell(context.Background(), "-NoProfile")
	if runtime.GOOS != "windows" {
		// Off Windows the command must fail to start, not fall back to a
		// powershell found through PATH.
		if cmd.Path != "" || !errors.Is(cmd.Start(), errors.ErrUnsupported) {
			t.Fatalf("path %q, err %v: want no path and errors.ErrUnsupported", cmd.Path, cmd.Err)
		}
		return
	}
	if cmd.Err != nil {
		t.Fatal(cmd.Err)
	}
	if !filepath.IsAbs(cmd.Path) || !strings.EqualFold(filepath.Base(cmd.Path), "powershell.exe") ||
		!strings.HasSuffix(strings.ToLower(cmd.Path), strings.ToLower(`\WindowsPowerShell\v1.0\powershell.exe`)) {
		t.Fatalf("path = %q, want <System32>\\WindowsPowerShell\\v1.0\\powershell.exe", cmd.Path)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != "-NoProfile" {
		t.Fatalf("args = %q", cmd.Args)
	}
}
