//go:build !windows

package packages

// findWinGet: WinGet only exists on Windows.
func findWinGet() (string, error) {
	return "", ErrWinGetNotInstalled
}
