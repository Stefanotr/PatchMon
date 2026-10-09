package store

import (
	"strings"
)

// Windows patch runs (agent: cmd/patchmon-agent/commands/serve_windows_patch.go)
// report one status line per item, at column 0:
//
//	[plan] <name>   dry run and real run: the item will be installed
//	[ok] <name>     installed
//	[fail] <name>   blocked or failed
//	[skip] <name>   nothing to do (not pending any more, or not started)
//
// Every other line, installer output included, is indented by the agent, so
// only these lines can name a package. <name> is the name the run was asked
// for (patch_package) or the name the inventory reports (patch_all), so it
// lines up with the host's package list.

const (
	windowsStatusPlan = "plan"
	windowsStatusOK   = "ok"
)

// IsWindowsOSType reports whether a host's os_type is Windows. Agents send
// "Windows"; the check is lenient the same way the frontend's is.
func IsWindowsOSType(osType string) bool {
	return strings.Contains(strings.ToLower(osType), "windows")
}

// parseWindowsPatchStatusLines returns the names on the status lines carrying
// status, in order and without duplicates. Two installs can share a display
// name (x86 and x64 builds), and they are one package to the server.
func parseWindowsPatchStatusLines(output, status string) []string {
	prefix := "[" + status + "] "
	var names []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}
