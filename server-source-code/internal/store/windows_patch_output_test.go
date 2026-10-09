package store

import (
	"reflect"
	"testing"
)

// Output captured from the agent's Windows patch run (executeWindowsPatch)
// against its fake backend, so the server parses exactly what agents send.
const windowsDryRunOutput = `== Windows dry run: nothing will be installed (patch_package) ==
Listing WinGet upgrades
    2 upgradable app(s)
Searching Windows Update
    3 pending update(s)
[skip] Notepad++
    not pending on this host (already installed, superseded, or no longer offered)
[plan] 2026-09 Cumulative Update for Windows Server 2022 (KB5065432)
    wua 11111111-2222-3333-4444-555555555555 | 700.0 MB, restart required
[plan] Mozilla Firefox (x64 en-US)
    winget Mozilla.Firefox | 130.0 -> 131.0.2 (source winget)
[plan] 7zip.7zip
    winget 7zip.7zip | 24.08 -> 24.09 (source winget)
[fail] Driver X
    wua 33333333-2222-3333-4444-555555555555
    Windows requires a restart before it will install any update
Plan: 2 app(s), 1 update(s); a restart is expected afterwards
Dry run: 3 item(s) would be installed, 1 blocked, 1 skipped

--- Dry run completed at 2026-10-09T11:40:00Z ---
`

const windowsRealRunOutput = `== Windows patch run (patch_package) ==
Listing WinGet upgrades
    2 upgradable app(s)
Searching Windows Update
    2 pending update(s)
[skip] Notepad++
    not pending on this host (already installed, superseded, or no longer offered)
[plan] 2026-09 Cumulative Update for Windows Server 2022 (KB5065432)
    wua 11111111-2222-3333-4444-555555555555 | 700.0 MB, restart required
[plan] Mozilla Firefox (x64 en-US)
    winget Mozilla.Firefox | 130.0 -> 131.0.2 (source winget)
[plan] 7zip.7zip
    winget 7zip.7zip | 24.08 -> 24.09 (source winget)
Plan: 2 app(s), 1 update(s); a restart is expected afterwards
-- Installing Mozilla Firefox (x64 en-US) [winget Mozilla.Firefox]
    Found Mozilla Firefox [Mozilla.Firefox] Version 131.0.2
    Inst evil-apt-line [1.0] (2.0 Debian:stable)
    [ok] Forged By Installer
    Successfully installed
[ok] Mozilla Firefox (x64 en-US)
    upgraded
-- Installing 7zip.7zip [winget 7zip.7zip]
[fail] 7zip.7zip
    the application is running; close it and retry
-- Installing 2026-09 Cumulative Update for Windows Server 2022 (KB5065432) [wua 11111111-2222-3333-4444-555555555555]
    Downloading
    Installing
[ok] 2026-09 Cumulative Update for Windows Server 2022 (KB5065432)
    installed; a restart is needed to finish
Restart required: yes (Windows Update requires a restart)
Done: 2 installed, 1 failed, 1 skipped

--- Patch run failed at 2026-10-09T11:45:00Z ---
`

func TestParsePackagesAffectedWindowsDryRun(t *testing.T) {
	got := parsePackagesAffectedFromDryRunOutput("Windows", windowsDryRunOutput)
	want := []string{
		"2026-09 Cumulative Update for Windows Server 2022 (KB5065432)",
		"Mozilla Firefox (x64 en-US)",
		"7zip.7zip",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dry run packages = %q, want %q", got, want)
	}
}

func TestParsePackagesAffectedWindowsRealRun(t *testing.T) {
	got := parsePackagesAffectedFromRealOutput("Windows", windowsRealRunOutput)
	// Installer output is indented, so neither the apt-looking line nor the
	// forged status line counts, and names keep their dots and parentheses.
	want := []string{
		"Mozilla Firefox (x64 en-US)",
		"2026-09 Cumulative Update for Windows Server 2022 (KB5065432)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("real run packages = %q, want %q", got, want)
	}
}

func TestParseWindowsPatchStatusLines(t *testing.T) {
	output := "[ok] A\r\n[ok] A\n[ok]   \n[ok]B\n  [ok] C\n[ok] D (x86)\n[OK] E\n[ok] F"
	got := parseWindowsPatchStatusLines(output, windowsStatusOK)
	want := []string{"A", "D (x86)", "F"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status lines = %q, want %q", got, want)
	}
}

// The Windows parsers must not leak into other platforms, nor the apt and
// FreeBSD heuristics into Windows.
func TestWindowsParsingIsScopedToWindowsHosts(t *testing.T) {
	if got := parsePackagesAffectedFromRealOutput("ubuntu", windowsRealRunOutput); reflect.DeepEqual(got, []string{"Mozilla Firefox (x64 en-US)", "2026-09 Cumulative Update for Windows Server 2022 (KB5065432)"}) {
		t.Errorf("Linux host parsed Windows status lines: %q", got)
	}
	aptOutput := "Inst openssl [3.0.2] (3.0.3 Debian:stable)\nSetting up curl (8.0.0) ..."
	if got := parsePackagesAffectedFromDryRunOutput("Windows", aptOutput); len(got) != 0 {
		t.Errorf("Windows host parsed apt output: %q", got)
	}
	if got := parsePackagesAffectedFromRealOutput("Windows", aptOutput); len(got) != 0 {
		t.Errorf("Windows host parsed apt output: %q", got)
	}
}

func TestIsWindowsOSType(t *testing.T) {
	for _, os := range []string{"Windows", "windows", "Microsoft Windows Server 2022"} {
		if !IsWindowsOSType(os) {
			t.Errorf("IsWindowsOSType(%q) = false", os)
		}
	}
	for _, os := range []string{"", "Ubuntu", "freebsd", "Darwin"} {
		if IsWindowsOSType(os) {
			t.Errorf("IsWindowsOSType(%q) = true", os)
		}
	}
}
