//go:build windows

package commands

import (
	"errors"
	"io/fs"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// secureDataDir restricts the agent's data folder (C:\ProgramData\PatchMon)
// to LocalSystem and Administrators. It holds credentials.yml, whose API key
// lets whoever reads it report as this host, and config.yml. Created under
// ProgramData, the folder inherits read access for every local user, and the
// 0600 mode the agent gives its files has no effect on Windows. The DACL set
// here is inherited by the files in the folder, so they lose that access
// too; objects with a protected DACL of their own (the state files, the
// update directory) keep theirs.
//
// A folder that is a reparse point, or that anyone but SYSTEM or
// Administrators owns, is left alone with a warning: its owner could reopen
// any DACL set on it. Reinstalling the agent repairs it.
func secureDataDir() {
	secureDataDirAt(filepath.Join(programDataDir(), "PatchMon"))
}

func secureDataDirAt(dir string) {
	h, err := openTrusted(dir, true)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.WithError(err).Warn("Agent data folder cannot be trusted; reinstall the agent to repair its permissions")
		}
		return
	}
	_ = windows.CloseHandle(h)
	if err := restrictToSystemAndAdmins(dir); err != nil {
		logger.WithError(err).Warn("Failed to restrict the agent data folder to SYSTEM and Administrators")
	}
}
