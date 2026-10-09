//go:build !windows

package commands

import (
	"errors"
	"os"
)

// State files are Windows-only: they back the Windows patch runs.

func stateFilePath(string) string { return "" }

func openTrustedStateFile(string) (*os.File, error) { return nil, errors.ErrUnsupported }

func writeTrustedStateFile(string, []byte) error { return errors.ErrUnsupported }
