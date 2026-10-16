package handler

import "strings"

// Fork agents built on PatchMon 2.1.3 (2.1.4-fork.1 to 2.1.4-fork.N) patch
// Windows with the fork's former runner. It ignores the Windows Updates /
// applications scope that PatchMon 2.1.4 sends, so a Patch all could install
// updates the user left out, and it refuses a Patch selected sent as targets.
// As pre-releases of 2.1.4 they pass PatchMon's 2.1.4 floor, hence this check.
const forkFormerWindowsRunnerPrefix = "2.1.4-fork."

const forkFormerWindowsRunnerMessage = "Update the PatchMon agent on this host to patch Windows: fork agents 2.1.4-fork.N use the fork's former Windows patching"

func forkAgentHasFormerWindowsRunner(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	return strings.HasPrefix(v, forkFormerWindowsRunnerPrefix)
}
