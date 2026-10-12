//go:build !windows

package winexec

import "errors"

// powerShellPath: Windows PowerShell only exists on Windows, and the callers
// only start it there.
func powerShellPath() (string, error) {
	return "", errors.ErrUnsupported
}
