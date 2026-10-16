package commands

import (
	"runtime"
	"time"

	"patchmon-agent/internal/packages"
)

// patchRunTimeout bounds a whole patch run on this platform. On Windows it is
// the longest run packages.WindowsRunLimit allows, one that installs Windows
// Updates: a restart pending sooner than that could cut a run off.
func patchRunTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return packages.WindowsRunLimit(packages.WindowsPatchRequest{PatchAll: true, IncludeUpdates: true})
	}
	return 30 * time.Minute
}
