//go:build windows

package winexec

import (
	"fmt"
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// powerShellPath returns <System32>\WindowsPowerShell\v1.0\powershell.exe,
// with System32 from GetSystemDirectory rather than from the environment.
var powerShellPath = sync.OnceValues(func() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", fmt.Errorf("locate Windows PowerShell: %w", err)
	}
	return filepath.Join(dir, "WindowsPowerShell", "v1.0", "powershell.exe"), nil
})
