package packages

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// winget.exe ships in the App Installer package, which Windows keeps under
// <Program Files>\WindowsApps, a folder only TrustedInstaller and SYSTEM can
// write to. The agent runs as LocalSystem, so it only runs that copy: never a
// winget.exe found through PATH or in a user profile (the per-user
// LOCALAPPDATA alias), which an account other than an administrator could
// control. findWinGet is the one place that decides which winget.exe runs;
// the PowerShell scripts that need it receive its path in wingetPathEnv.

// wingetPathEnv carries the resolved winget.exe to the PowerShell scripts.
const wingetPathEnv = "PATCHMON_WINGET"

// wingetPackageDirPattern matches an App Installer package folder:
// Name_Version_Architecture_ResourceId_PublisherId, the publisher being
// Microsoft's Store publisher ID.
var wingetPackageDirPattern = regexp.MustCompile(`(?i)^Microsoft\.DesktopAppInstaller_(\d{1,9}(?:\.\d{1,9}){0,3})_[^_]+_[^_]*_8wekyb3d8bbwe$`)

// findWinGetIn returns winget.exe from the App Installer package folder with
// the highest version under windowsApps. Only a plain folder holding winget.exe
// as a regular file counts: on Windows, os.Lstat reports a symbolic link, a
// junction or any other reparse point as another type (apart from Data
// Deduplication files, which hold the file's own data). It returns an error
// wrapping ErrWinGetNotInstalled when there is no such folder, and the error
// met on the way when one kept it from telling.
func findWinGetIn(windowsApps string) (string, error) {
	entries, err := os.ReadDir(windowsApps)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w (%s does not exist)", ErrWinGetNotInstalled, windowsApps)
	}
	if err != nil {
		return "", fmt.Errorf("look for winget.exe in %s: %w", windowsApps, err)
	}
	var best string
	var bestVersion [4]int
	var lookErr error
	for _, e := range entries {
		m := wingetPackageDirPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		dir := filepath.Join(windowsApps, e.Name())
		exe := filepath.Join(dir, "winget.exe")
		ok, err := hasType(dir, fs.ModeDir)
		if ok {
			ok, err = hasType(exe, 0)
		}
		if err != nil && lookErr == nil {
			lookErr = err
		}
		if !ok {
			continue
		}
		// Entries come sorted by name, so a tie keeps the first one.
		if v := parseWinGetVersion(m[1]); best == "" || compareVersions(v, bestVersion) > 0 {
			best, bestVersion = exe, v
		}
	}
	switch {
	case best != "":
		return best, nil
	case lookErr != nil:
		return "", fmt.Errorf("look for winget.exe in %s: %w", windowsApps, lookErr)
	}
	return "", fmt.Errorf("%w (no App Installer package with winget.exe in %s)", ErrWinGetNotInstalled, windowsApps)
}

// hasType reports whether path, not followed when it is a link, has exactly
// the given type: fs.ModeDir for a directory, 0 for a regular file. A missing
// path is not an error.
func hasType(path string, want fs.FileMode) (bool, error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	}
	return fi.Mode().Type() == want, nil
}

// parseWinGetVersion reads the dotted version of a package folder; the
// pattern guarantees one to four numbers that fit an int.
func parseWinGetVersion(s string) [4]int {
	var v [4]int
	for i, part := range strings.SplitN(s, ".", 4) {
		v[i], _ = strconv.Atoi(part)
	}
	return v
}

func compareVersions(a, b [4]int) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] > b[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

// validWinGetPath accepts only the location findWinGet returns: an absolute
// path to winget.exe directly inside an App Installer package folder of a
// WindowsApps folder. It is checked again before winget.exe is executed.
// Both separators are accepted so the check is testable off Windows.
func validWinGetPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\"\x00") {
		return false
	}
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '\\' || r == '/' })
	for _, part := range parts {
		if part == "." || part == ".." {
			return false
		}
	}
	n := len(parts)
	if n < 4 || !strings.EqualFold(parts[n-1], "winget.exe") || !wingetPackageDirPattern.MatchString(parts[n-2]) ||
		!strings.EqualFold(parts[n-3], "WindowsApps") {
		return false
	}
	return filepath.IsAbs(p) || (len(p) > 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}
