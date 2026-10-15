//go:build windows

package packages

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// findWinGet returns the winget.exe of the newest App Installer package in
// <Program Files>\WindowsApps, with Program Files taken from the Windows API
// rather than from the environment.
func findWinGet() (string, error) {
	programFiles, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return "", fmt.Errorf("locate Program Files: %w", err)
	}
	return findWinGetIn(filepath.Join(programFiles, "WindowsApps"))
}
