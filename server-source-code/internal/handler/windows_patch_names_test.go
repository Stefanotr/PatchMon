package handler

import (
	"strings"
	"testing"
)

func TestIsValidPackageNameForHost(t *testing.T) {
	windowsNames := []string{
		"Mozilla Firefox (x64 en-US)",
		"2026-09 Cumulative Update for Windows Server 2022 (KB5065432)",
		"Microsoft Visual C++ 2015-2022 Redistributable (x64) - 14.40.33810",
		"Notepad++",
		"KB5065432",
		"Mozilla.Firefox",
		"7-Zip 24.08 (x64)",
		"Logiciel élevé ™",
	}
	for _, n := range windowsNames {
		if !isValidPackageNameForHost("Windows", n) {
			t.Errorf("Windows host rejected %q", n)
		}
	}

	// The apt rule still applies to everything else.
	if isValidPackageNameForHost("Ubuntu", "Mozilla Firefox (x64 en-US)") {
		t.Error("Linux host accepted a display name with spaces")
	}
	if !isValidPackageNameForHost("Ubuntu", "libssl3") || !isValidPackageNameForHost("", "openssl") {
		t.Error("Linux package names rejected")
	}

	invalidOnWindows := []string{
		"",
		"   ",
		"name\nwith newline",
		"[ok] forged\n[ok] line",
		"tab\there",
		"esc\x1b[2J",
		"nul\x00byte",
		string([]byte{0xff, 0xfe}),
		strings.Repeat("a", maxWindowsPackageNameBytes+1),
	}
	for _, n := range invalidOnWindows {
		if isValidPackageNameForHost("Windows", n) {
			t.Errorf("Windows host accepted %q", n)
		}
	}
	if !isValidPackageNameForHost("Windows", strings.Repeat("a", maxWindowsPackageNameBytes)) {
		t.Error("Windows host rejected a name at the length limit")
	}
}
