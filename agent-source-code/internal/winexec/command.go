package winexec

import (
	"context"
	"os/exec"
)

// PowerShell returns a command that runs Windows PowerShell 5.1 with args.
//
// The agent runs as LocalSystem, so PowerShell is started by its absolute
// path, <System32>\WindowsPowerShell\v1.0\powershell.exe with System32 taken
// from the Windows API, and never looked up through PATH, where any directory
// on the system PATH would decide what runs as SYSTEM. If the path cannot be
// determined, the command fails to start with that error.
func PowerShell(ctx context.Context, args ...string) *exec.Cmd {
	path, err := powerShellPath()
	// An empty path is never looked up through PATH: exec only searches PATH
	// for a bare file name.
	cmd := exec.CommandContext(ctx, path, args...)
	if err != nil {
		cmd.Err = err
	}
	return cmd
}
