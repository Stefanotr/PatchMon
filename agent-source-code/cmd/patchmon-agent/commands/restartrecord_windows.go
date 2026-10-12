//go:build windows

package commands

import "os"

// The record is a state file in C:\ProgramData\PatchMon, trusted like the
// others: a regular file owned by SYSTEM or Administrators, written with a
// DACL granting access to those two alone (see statefile_windows.go).

func defaultRestartRecordPath() string {
	return stateFilePath(restartRecordFile)
}

func openRestartRecord(path string) (*os.File, error) {
	return openTrustedStateFile(path)
}

func writeRestartRecordFile(path string, data []byte) error {
	return writeTrustedStateFile(path, data)
}

// removeRestartRecordFile deletes the record; os.Remove deletes a link
// itself, never what it points to.
func removeRestartRecordFile(path string) error {
	return os.Remove(path)
}
