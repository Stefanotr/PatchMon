package handler

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PatchMon/PatchMon/server-source-code/internal/store"
)

// Windows hosts report packages by display name ("Mozilla Firefox (x64 en-US)",
// "2026-09 Cumulative Update for Windows Server 2022 (KB5065432)"), so the
// apt-style packageNameRegex rejects nearly all of them. The agent never puts
// a Windows name on a command line: it resolves it against a fresh inventory
// of the host and only acts on the WUA GUIDs and WinGet IDs that inventory
// produced. A name therefore only has to be printable text of bounded size.

// maxWindowsPackageNameBytes matches the agent's limit (packages.ValidWindowsSelector).
const maxWindowsPackageNameBytes = 512

// isValidWindowsPackageName mirrors the agent's packages.ValidWindowsSelector:
// valid UTF-8, not blank, bounded, and free of control characters, which
// could otherwise forge lines in the run output or the logs.
func isValidWindowsPackageName(s string) bool {
	if len(s) == 0 || len(s) > maxWindowsPackageNameBytes || !utf8.ValidString(s) {
		return false
	}
	if strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// isValidPackageNameForHost applies the rule for the host's platform.
func isValidPackageNameForHost(osType, name string) bool {
	if store.IsWindowsOSType(osType) {
		return isValidWindowsPackageName(name)
	}
	return isValidPackageName(name)
}
