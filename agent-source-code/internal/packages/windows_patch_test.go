package packages

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var (
	cuUpdate = WUAUpdate{
		GUID: "11111111-2222-3333-4444-555555555555", Title: "2026-09 Cumulative Update for Windows 11 Version 24H2 for x64-based Systems",
		KBs: stringList{"KB5065426"}, SizeBytes: 712 << 20, RebootBehavior: wuaAlwaysRequiresReboot, Severity: "Critical", EulaAccepted: true,
	}
	netUpdate = WUAUpdate{
		GUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Title: "2026-09 Cumulative Update for .NET Framework 3.5 and 4.8.1",
		KBs: stringList{"KB5064401", "KB5064400"}, SizeBytes: 70 << 20,
	}
	driverUpdate = WUAUpdate{GUID: "99999999-8888-7777-6666-555555555555", Title: "Intel - Net - 1.2.3.4"}

	firefox = WinGetUpgrade{Name: "Mozilla Firefox (x64 en-US)", ID: "Mozilla.Firefox", Version: "130.0", Available: "131.0.2", Source: "winget"}
	vcX64   = WinGetUpgrade{Name: "Microsoft Visual C++ 2015-2022 Redist", ID: "Microsoft.VCRedist.2015+.x64", NameTruncated: true, Source: "winget"}
	vcX86   = WinGetUpgrade{Name: "Microsoft Visual C++ 2015-2022 Redist", ID: "Microsoft.VCRedist.2015+.x86", NameTruncated: true, Source: "winget"}
)

func testInventory() WindowsInventory {
	return WindowsInventory{
		WUA:        WUAScan{Updates: []WUAUpdate{cuUpdate, netUpdate, driverUpdate}},
		WinGet:     []WinGetUpgrade{firefox, vcX64, vcX86},
		WinGetPath: `C:\Program Files\WindowsApps\winget.exe`,
	}
}

func refs(ts []WindowsTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Ref())
	}
	return out
}

func TestResolveWindowsNames(t *testing.T) {
	tests := []struct {
		name       string
		names      []string
		inv        func(WindowsInventory) WindowsInventory
		wantRefs   []string
		wantUnres  []string
		wantFatal  []bool
		wantReason string
	}{
		{
			name:     "collector display name of an update",
			names:    []string{cuUpdate.DisplayName()},
			wantRefs: []string{"wua " + cuUpdate.GUID},
		},
		{
			name:     "re-titled update still matches on its KB list in any order",
			names:    []string{"Some new title (KB5064400, KB5064401)"},
			wantRefs: []string{"wua " + netUpdate.GUID},
		},
		{
			name:     "bare KB and GUID, deduplicated",
			names:    []string{"kb5065426", strings.ToUpper(cuUpdate.GUID)},
			wantRefs: []string{"wua " + cuUpdate.GUID},
		},
		{
			name:     "update without KB falls through WinGet to WUA",
			names:    []string{"Intel - Net - 1.2.3.4"},
			wantRefs: []string{"wua " + driverUpdate.GUID},
		},
		{
			name:     "app by display name, case and spacing insensitive",
			names:    []string{"mozilla  firefox (x64 en-us)"},
			wantRefs: []string{"winget Mozilla.Firefox"},
		},
		{
			name:     "app by WinGet ID",
			names:    []string{"mozilla.firefox"},
			wantRefs: []string{"winget Mozilla.Firefox"},
		},
		{
			// The collector reports both truncated rows as one package, so the
			// one name stands for both installs.
			name:     "identical truncated names upgrade every app carrying them",
			names:    []string{"Microsoft Visual C++ 2015-2022 Redist"},
			wantRefs: []string{"winget Microsoft.VCRedist.2015+.x64", "winget Microsoft.VCRedist.2015+.x86"},
		},
		{
			name:       "a longer name that two truncated rows prefix is refused",
			names:      []string{"Microsoft Visual C++ 2015-2022 Redistributable (x64) - 14.40"},
			wantUnres:  []string{"Microsoft Visual C++ 2015-2022 Redistributable (x64) - 14.40"},
			wantFatal:  []bool{true},
			wantReason: "Microsoft.VCRedist.2015+.x64, Microsoft.VCRedist.2015+.x86",
		},
		{
			// Only the x64 build is pending, but the x86 one was asked for: a
			// unique truncated prefix is still not proof.
			name: "a longer name that one truncated row prefixes is refused too",
			inv: func(inv WindowsInventory) WindowsInventory {
				inv.WinGet = []WinGetUpgrade{firefox, vcX64}
				return inv
			},
			names:      []string{"Microsoft Visual C++ 2015-2022 Redistributable (x86) - 14.36"},
			wantUnres:  []string{"Microsoft Visual C++ 2015-2022 Redistributable (x86) - 14.36"},
			wantFatal:  []bool{true},
			wantReason: "patch by WinGet ID",
		},
		{
			name:       "nothing pending is a skip, not a failure",
			names:      []string{"Notepad++"},
			wantUnres:  []string{"Notepad++"},
			wantFatal:  []bool{false},
			wantReason: "not pending",
		},
		{
			// The inventory can hold a name winget cut in a narrower table.
			// Neither guessed nor reported as not pending: the item fails.
			name:       "a name that only prefixes an upgradable app fails with a hint",
			names:      []string{"Mozilla Firefox"},
			wantUnres:  []string{"Mozilla Firefox"},
			wantFatal:  []bool{true},
			wantReason: "Mozilla.Firefox start with this name",
		},
		{
			name: "an unreadable WinGet listing makes an unmatched app name fatal",
			inv: func(inv WindowsInventory) WindowsInventory {
				inv.WinGetUnreadable = 2
				return inv
			},
			names:      []string{"Notepad++"},
			wantUnres:  []string{"Notepad++"},
			wantFatal:  []bool{true},
			wantReason: "2 line(s) of the WinGet listing could not be read",
		},
		{
			name: "an explicit-only app is upgraded when named",
			inv: func(inv WindowsInventory) WindowsInventory {
				pinned := firefox
				pinned.ExplicitOnly = true
				inv.WinGet = []WinGetUpgrade{pinned}
				return inv
			},
			names:    []string{"Mozilla.Firefox"},
			wantRefs: []string{"winget Mozilla.Firefox"},
		},
		{
			name: "a failed WinGet listing makes an app name fatal",
			inv: func(inv WindowsInventory) WindowsInventory {
				inv.WinGet, inv.WinGetErr = nil, ErrWinGetNotInstalled
				return inv
			},
			names:      []string{"Mozilla Firefox (x64 en-US)"},
			wantUnres:  []string{"Mozilla Firefox (x64 en-US)"},
			wantFatal:  []bool{true},
			wantReason: "WinGet is not installed",
		},
		{
			name: "a failed WinGet listing does not affect a KB name",
			inv: func(inv WindowsInventory) WindowsInventory {
				inv.WinGet, inv.WinGetErr = nil, errors.New("boom")
				return inv
			},
			names:    []string{"KB5065426"},
			wantRefs: []string{"wua " + cuUpdate.GUID},
		},
		{
			name: "a failed WUA scan makes an unmatched KB fatal",
			inv: func(inv WindowsInventory) WindowsInventory {
				inv.WUA, inv.WUAErr = WUAScan{}, errors.New("0x8024402C")
				return inv
			},
			names:      []string{"KB5065426"},
			wantUnres:  []string{"KB5065426"},
			wantFatal:  []bool{true},
			wantReason: "Windows Update scan failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := testInventory()
			if tt.inv != nil {
				inv = tt.inv(inv)
			}
			targets, unres := ResolveWindowsNames(tt.names, inv)
			if got := refs(targets); !reflect.DeepEqual(got, nilIfEmpty(tt.wantRefs)) && (len(got) != 0 || len(tt.wantRefs) != 0) {
				t.Fatalf("targets = %v, want %v", got, tt.wantRefs)
			}
			if len(unres) != len(tt.wantUnres) {
				t.Fatalf("unresolved = %+v, want names %v", unres, tt.wantUnres)
			}
			for i, u := range unres {
				if u.Name != tt.wantUnres[i] || u.Fatal != tt.wantFatal[i] {
					t.Errorf("unresolved[%d] = %+v, want name %q fatal %v", i, u, tt.wantUnres[i], tt.wantFatal[i])
				}
				if tt.wantReason != "" && !strings.Contains(u.Reason, tt.wantReason) {
					t.Errorf("unresolved[%d].Reason = %q, want it to mention %q", i, u.Reason, tt.wantReason)
				}
			}
			for _, tg := range targets {
				if tg.Name == "" {
					t.Errorf("target %s has no name", tg.Ref())
				}
			}
		})
	}
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func TestResolveWindowsNamesReportsTheRequestedName(t *testing.T) {
	targets, _ := ResolveWindowsNames([]string{"  KB5065426  "}, testInventory())
	if len(targets) != 1 || targets[0].Name != "KB5065426" {
		t.Fatalf("targets = %+v, want one target named after the trimmed request", targets)
	}
}

func TestSelectPatchAllTargetsHoldsBackUnreportedUpdates(t *testing.T) {
	inv := testInventory()
	pinned := WinGetUpgrade{Name: "Pinned App", ID: "Contoso.Pinned", Version: "1", Available: "2", ExplicitOnly: true}
	inv.WinGet = append(inv.WinGet, pinned)
	targets, held, heldApps := SelectPatchAllTargets(inv, []string{strings.ToUpper(cuUpdate.GUID), "not-a-pending-guid"})
	want := []string{
		"wua " + cuUpdate.GUID,
		"winget Mozilla.Firefox",
		"winget Microsoft.VCRedist.2015+.x64",
		"winget Microsoft.VCRedist.2015+.x86",
	}
	if got := refs(targets); !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %v, want %v", got, want)
	}
	if len(held) != 2 || held[0].GUID != netUpdate.GUID || held[1].GUID != driverUpdate.GUID {
		t.Fatalf("held = %+v, want the .NET and driver updates", held)
	}
	if len(heldApps) != 1 || heldApps[0].ID != pinned.ID {
		t.Fatalf("held apps = %+v, want the explicit-only app, as winget upgrade --all would", heldApps)
	}
	if targets[0].Name != cuUpdate.DisplayName() {
		t.Errorf("patch_all target name = %q, want the collector's display name", targets[0].Name)
	}
}

func TestTargetBlocker(t *testing.T) {
	wua := WindowsTarget{Kind: TargetWUA, Name: "x", Update: cuUpdate}
	if b := wua.Blocker(WUAScan{}); b != "" {
		t.Errorf("clean update blocked: %q", b)
	}
	if b := wua.Blocker(WUAScan{RebootBeforeInstall: true}); !strings.Contains(b, "restart") {
		t.Errorf("pending restart not reported: %q", b)
	}
	if b := (WindowsTarget{Kind: TargetWUA, Update: WUAUpdate{GUID: "'; Remove-Item C:\\"}}).Blocker(WUAScan{}); b == "" {
		t.Error("malformed GUID not blocked")
	}

	app := WindowsTarget{Kind: TargetWinGet, Name: "x", App: firefox}
	if b := app.Blocker(WUAScan{RebootBeforeInstall: true}); b != "" {
		t.Errorf("WinGet app blocked by WUA state: %q", b)
	}
	cut := app
	cut.App.IDTruncated = true
	if b := cut.Blocker(WUAScan{}); !strings.Contains(b, "shortened") {
		t.Errorf("truncated ID not blocked: %q", b)
	}
	for _, id := range []string{"Foo Bar", "--force", "a;b", ""} {
		bad := app
		bad.App.ID = id
		if bad.Blocker(WUAScan{}) == "" {
			t.Errorf("ID %q not blocked", id)
		}
	}
	badSrc := app
	badSrc.App.Source = "--override"
	if badSrc.Blocker(WUAScan{}) == "" {
		t.Error("option-like source not blocked")
	}
}

func TestWinGetUpgradeArgs(t *testing.T) {
	got := WinGetUpgradeArgs(firefox)
	want := []string{"upgrade", "--id", "Mozilla.Firefox", "--exact", "--source", "winget", "--silent",
		"--accept-source-agreements", "--accept-package-agreements", "--disable-interactivity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	noSrc := firefox
	noSrc.Source = ""
	if got := WinGetUpgradeArgs(noSrc); slices.Contains(got, "--source") {
		t.Fatalf("empty source still passed: %v", got)
	}
}

func TestClassifyWinGetExit(t *testing.T) {
	tests := []struct {
		code       uint32
		status     string
		wantReboot bool
	}{
		{0, StatusOK, false},
		{0x8A150109, StatusOK, true},
		{0x8A15010B, StatusOK, true},
		{0x8A15002B, StatusSkip, false},
		{0x8A15004F, StatusSkip, false},
		{0x8A150014, StatusSkip, false},
		{0x8A15008E, StatusSkip, false},
		{0x8A150068, StatusSkip, false},
		{0x8A150101, StatusFail, false},
		{0x8A15010A, StatusFail, true},
		{0x8A150050, StatusFail, false},
		{1603, StatusFail, false},
	}
	for _, tt := range tests {
		r := ClassifyWinGetExit(tt.code)
		if r.Status != tt.status || r.RebootRequired != tt.wantReboot || r.Detail == "" {
			t.Errorf("ClassifyWinGetExit(0x%08X) = %+v, want status %s reboot %v", tt.code, r, tt.status, tt.wantReboot)
		}
	}
}

func TestStatusLinesCannotBeForged(t *testing.T) {
	line := StatusLine(StatusOK, "evil\n[ok] forged\r")
	if strings.Count(line, "\n") != 1 || !strings.HasPrefix(line, "[ok] evil") {
		t.Fatalf("StatusLine let a newline through: %q", line)
	}
	if got := DetailLine("x\ny"); got != OutputIndent+"x y\n" {
		t.Fatalf("DetailLine = %q", got)
	}
	if got := StatusLine(StatusSkip, "   "); got != "[skip] (unnamed)\n" {
		t.Fatalf("empty name rendered as %q", got)
	}
}

func TestValidWindowsSelector(t *testing.T) {
	good := []string{
		"Mozilla Firefox (x64 en-US)",
		"2026-09 Cumulative Update for Windows 11 Version 24H2 for x64-based Systems (KB5065426)",
		"Notepad++",
		"Mise à jour de sécurité",
		"KB5065426",
	}
	for _, s := range good {
		if !ValidWindowsSelector(s) {
			t.Errorf("rejected %q", s)
		}
	}
	bad := []string{"", "   ", "a\nb", "a\rb", "tab\there", "\x00", string([]byte{0xff, 0xfe}), strings.Repeat("a", maxWindowsSelectorBytes+1)}
	for _, s := range bad {
		if ValidWindowsSelector(s) {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestParseWUAScan(t *testing.T) {
	out := []byte("WARNING: noise\r\nPATCHMON_RESULT:" +
		`{"updates":[{"guid":"11111111-2222-3333-4444-555555555555","title":" A ","kbs":"KB1","size":10,"reboot":1,"needs_input":true},` +
		`{"guid":"","title":"no guid"}],"busy":true,"reboot_before":false}` + "\r\n")
	scan, err := ParseWUAScan(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Updates) != 1 || scan.Updates[0].Title != "A" || !reflect.DeepEqual([]string(scan.Updates[0].KBs), []string{"KB1"}) ||
		!scan.Updates[0].CanRequestUserInput || scan.Updates[0].RebootBehavior != 1 || !scan.InstallerBusy {
		t.Fatalf("scan = %+v", scan)
	}

	if _, err := ParseWUAScan([]byte("PATCHMON_ERROR:Exception from HRESULT (0x8024402C)\n")); err == nil || !strings.Contains(err.Error(), "0x8024402C") {
		t.Fatalf("script error not surfaced: %v", err)
	}
	if _, err := ParseWUAScan([]byte("At line:3 char:5\n+ Unexpected token\n")); err == nil || !strings.Contains(err.Error(), "Unexpected token") {
		t.Fatalf("missing result not reported with output tail: %v", err)
	}
}

func TestParseItemResult(t *testing.T) {
	r, err := ParseItemResult([]byte("Installing\nPATCHMON_RESULT:{\"status\":\"ok\",\"detail\":\"installed\",\"reboot\":true}\n"))
	if err != nil || r.Status != StatusOK || !r.RebootRequired {
		t.Fatalf("ParseItemResult = %+v, %v", r, err)
	}
	if _, err := ParseItemResult([]byte(`PATCHMON_RESULT:{"status":"maybe"}`)); err == nil {
		t.Fatal("unknown status accepted")
	}
}

// wingetTable renders a table the way winget's TableOutput does: each column
// as wide as its widest cell (header included), one space between columns,
// columns with no value at all left out entirely (header too), widths in
// display cells. Empty rows render nothing, as winget prints no header for an
// empty table.
func wingetTable(header []string, rows ...[]string) string {
	if len(rows) == 0 {
		return ""
	}
	width := func(s string) int { return toCells(s).width }
	widths := make([]int, len(header))
	for i := range header {
		for _, r := range rows {
			widths[i] = max(widths[i], width(r[i]))
		}
		if widths[i] > 0 {
			widths[i] = max(widths[i], width(header[i]))
		}
	}
	last := len(header) - 1
	for last > 0 && widths[last] == 0 {
		last--
	}
	total := 0
	for i, w := range widths {
		if w > 0 {
			total += w
			if i < last {
				total++
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i, c := range cells {
			if widths[i] == 0 {
				continue
			}
			b.WriteString(c)
			if i < last {
				b.WriteString(strings.Repeat(" ", widths[i]-width(c)+1))
			}
		}
		return b.String() + "\n"
	}
	out := line(header) + strings.Repeat("-", total) + "\n"
	for _, r := range rows {
		out += line(r)
	}
	return out
}

var wingetHeader = []string{"Name", "Id", "Version", "Available", "Source"}

const (
	testWinGetPath = `C:\Program Files\WindowsApps\Microsoft.DesktopAppInstaller_1.25.340.0_x64__8wekyb3d8bbwe\winget.exe`
	// What winget prints before its table while it opens its sources.
	wingetSpinner = "   - \r   \\ \r   | \r   / \r                                                                     \r"
	explicitIntro = "\nThe following packages have an upgrade available, but require explicit targeting for upgrade:\n"
	// Printed for a source on first use; --accept-source-agreements accepts it.
	sourceAgreements = "The `msstore` source requires that you view the following agreements before using.\n" +
		"Terms of Transaction: https://aka.ms/microsoft-store-terms-of-transaction\n" +
		"The source requires the current machine's 2-letter geographic region to be sent to the backend service to function properly (ex. \"US\").\n"
	// Columns shrunk to fit: Name 39 cells, Id 27, Version 12, Available 12.
	truncatedTable = "Name                                    Id                          Version      Available    Source\n" +
		"-----------------------------------------------------------------------------------------------------\n" +
		"Mozilla Firefox (x64 en-US)             Mozilla.Firefox             130.0        131.0.2      winget\n" +
		"Microsoft Visual C++ 2015-2022 Redistr… Microsoft.VCRedist.2015+.x… 14.36.32532  14.40.33810  winget\n"
)

func wingetScriptOutput(body string) string {
	return "WINGET_PATH:" + testWinGetPath + "\r\n" + body + "WINGET_EXIT:0\r\n"
}

func appIDs(apps []WinGetUpgrade) []string {
	var ids []string
	for _, a := range apps {
		id := a.ID
		if a.ExplicitOnly {
			id += " (explicit)"
		}
		ids = append(ids, id)
	}
	return ids
}

func TestParseWinGetListOutput(t *testing.T) {
	firefoxRow := []string{"Mozilla Firefox (x64 en-US)", "Mozilla.Firefox", "130.0", "131.0.2", "winget"}
	zipRow := []string{"7-Zip 24.08 (x64)", "7zip.7zip", "24.08", "24.09", "winget"}
	gitRow := []string{"Git", "Git.Git", "2.46.0", "2.47.0", "winget"}
	zoomRow := []string{"Zoom", "Zoom.Zoom", "6.1", "6.2", "winget"}
	pinnedRow := []string{"Contoso Pinned", "Contoso.Pinned", "1.0", "2.0", "winget"}
	// Name 13 cells, Id 8, Version 7, single source.
	alignedRow := []string{"Contoso Tools", "Ctso.App", "1.2.3.4", "1.2.3.5", ""}

	tests := []struct {
		name           string
		body           string
		want           []string
		wantUnreadable int
	}{
		{
			// Older winget shrinks the widest columns to 120 cells when output is
			// redirected; a cut cell ends in an ellipsis and a single space.
			name: "wide table with a truncated row and the footer",
			body: wingetSpinner + truncatedTable + "2 upgrades available.\n",
			want: []string{"Mozilla.Firefox", "Microsoft.VCRedist.2015+.x"},
		},
		{
			// The same table with a last row that does not fit its columns: on
			// its own it would pass for a footer line, but the footer says two.
			name: "a last row that does not fit is caught by the footer count",
			body: strings.Replace(truncatedTable, "x\u2026 ", "x\u2026  ", 1) + "2 upgrades available.\n",
			want: []string{"Mozilla.Firefox"}, wantUnreadable: 1,
		},
		{
			name: "a footer in another form is not counted",
			body: wingetTable(wingetHeader, gitRow) + "Upgrades available: 1\n",
			want: []string{"Git.Git"},
		},
		{
			// The footer cut at the column offsets used to read as an app
			// "1 upgrades availab" with the ID "le.".
			name: "footer of a narrow table is not an app",
			body: wingetTable(wingetHeader, zipRow) + "1 upgrades available.\n",
			want: []string{"7zip.7zip"},
		},
		{
			name: "short names: neither the footer nor the script markers are apps",
			body: wingetTable(wingetHeader, gitRow, zoomRow) + "2 upgrades available.\n",
			want: []string{"Git.Git", "Zoom.Zoom"},
		},
		{
			name: "explicit-targeting table after the regular one",
			body: wingetTable(wingetHeader, gitRow) + "2 upgrades available.\n" + explicitIntro +
				wingetTable(wingetHeader, pinnedRow),
			want: []string{"Git.Git", "Contoso.Pinned (explicit)"},
		},
		{
			name: "explicit-targeting table alone",
			body: wingetSpinner + "No installed package found matching input criteria.\n" + explicitIntro +
				wingetTable(wingetHeader, pinnedRow),
			want: []string{"Contoso.Pinned (explicit)"},
		},
		{
			// msstore fails under SYSTEM on most servers; its warning sits right
			// above the regular table.
			name: "a source warning above the regular table does not make it explicit",
			body: wingetSpinner + "Failed when searching source; results will not be included: msstore\n" +
				wingetTable(wingetHeader, gitRow) + "1 upgrades available.\n",
			want: []string{"Git.Git"},
		},
		{
			name: "single source: no Source column",
			body: wingetTable(wingetHeader, []string{"Git", "Git.Git", "2.46.0", "2.47.0", ""}),
			want: []string{"Git.Git"},
		},
		{
			name: "localized header is read by position",
			body: wingetTable([]string{"Nom", "ID", "Version", "Disponible", "Source"}, zipRow, firefoxRow) + "2 mises à niveau disponibles.\n",
			want: []string{"7zip.7zip", "Mozilla.Firefox"},
		},
		{
			name: "wide characters take two cells",
			body: wingetTable(wingetHeader, []string{"微信 WeChat", "Tencent.WeChat", "3.9", "4.0", "winget"}, gitRow),
			want: []string{"Tencent.WeChat", "Git.Git"},
		},
		{
			name: "a line that breaks the table between two rows is counted",
			body: strings.Replace(wingetTable(wingetHeader, gitRow, zoomRow), "Zoom ", "Garbage line\nZoom ", 1),
			want: []string{"Git.Git"}, wantUnreadable: 1,
		},
		{
			// The agreements block ends with a blank line, so with a source
			// warning under it the lines above the regular table look like the
			// explicit-targeting introduction. The footer settles it.
			name: "source agreements and a warning above the regular table",
			body: wingetSpinner + sourceAgreements + "\n" +
				"Failed when searching source; results will not be included: msstore\n" +
				wingetTable(wingetHeader, gitRow, zoomRow) + "2 upgrades available.\n",
			want: []string{"Git.Git", "Zoom.Zoom"},
		},
		{
			name: "explicit-targeting table alone, then the unknown-version message",
			body: "No installed package found matching input criteria.\n" + explicitIntro +
				wingetTable(wingetHeader, pinnedRow) +
				"1 package(s) have version numbers that cannot be determined. Use --include-unknown to see all results.\n",
			want: []string{"Contoso.Pinned (explicit)"},
		},
		{
			// With these column widths the introduction sentence lines up
			// with the columns: name "The following", ID "packages".
			name: "an introduction that lines up with the columns is not a row",
			body: wingetTable(wingetHeader, alignedRow) + "2 upgrades available.\n" + explicitIntro +
				wingetTable(wingetHeader, pinnedRow),
			want: []string{"Ctso.App", "Contoso.Pinned (explicit)"},
		},
		{
			name: "a message that lines up with the columns after the rows is never an app",
			body: wingetTable(wingetHeader, alignedRow) + "1 upgrades available.\n" +
				"The following packages have an upgrade available, but require explicit targeting for upgrade:\n",
			want: []string{"Ctso.App"}, wantUnreadable: 1,
		},
		{
			// The Korean "Available" header is two words, so the columns
			// cannot be told apart.
			name:           "a header that cannot be mapped is unreadable",
			body:           wingetTable([]string{"이름", "ID", "버전", "사용 가능", "원본"}, gitRow),
			wantUnreadable: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listing, err := parseWinGetListOutput(wingetScriptOutput(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if listing.Path != testWinGetPath {
				t.Errorf("path = %q", listing.Path)
			}
			if got := appIDs(listing.Apps); !reflect.DeepEqual(got, nilIfEmpty(tt.want)) {
				t.Errorf("apps = %v, want %v\n%s", got, tt.want, tt.body)
			}
			if listing.Unreadable != tt.wantUnreadable {
				t.Errorf("unreadable = %d, want %d", listing.Unreadable, tt.wantUnreadable)
			}
		})
	}

	t.Run("columns and truncation", func(t *testing.T) {
		listing, err := parseWinGetListOutput(wingetScriptOutput(tests[0].body))
		if err != nil {
			t.Fatal(err)
		}
		apps := listing.Apps
		if apps[0] != (WinGetUpgrade{Name: "Mozilla Firefox (x64 en-US)", ID: "Mozilla.Firefox", Version: "130.0", Available: "131.0.2", Source: "winget"}) {
			t.Errorf("apps[0] = %+v", apps[0])
		}
		if !apps[1].NameTruncated || !apps[1].IDTruncated || apps[1].Version != "14.36.32532" || apps[1].Name != "Microsoft Visual C++ 2015-2022 Redistr" {
			t.Errorf("apps[1] = %+v, want both columns flagged as truncated", apps[1])
		}
	})

	t.Run("script markers", func(t *testing.T) {
		if _, err := parseWinGetListOutput("WINGET_NOT_FOUND\n"); !errors.Is(err, ErrWinGetNotInstalled) {
			t.Errorf("not found: err = %v", err)
		}
		empty := "WINGET_PATH:C:\\x\\winget.exe\nNo installed package found matching input criteria.\nWINGET_EXIT:-1978335212\n"
		if l, err := parseWinGetListOutput(empty); err != nil || len(l.Apps) != 0 {
			t.Errorf("no upgrades: listing = %+v, err = %v", l, err)
		}
		broken := "WINGET_PATH:C:\\x\\winget.exe\nFailed when searching source: winget\nWINGET_EXIT:-1978335217\n"
		if _, err := parseWinGetListOutput(broken); err == nil || !strings.Contains(err.Error(), "0x8A15000F") {
			t.Errorf("source failure: err = %v", err)
		}
		if _, err := parseWinGetListOutput("WINGET_PATH:C:\\x\\evil.exe\nWINGET_EXIT:0\n"); err == nil {
			t.Error("non-winget path accepted")
		}
		if _, err := parseWinGetListOutput("WINGET_PATH:C:\\x\\winget.exe\n" + wingetTable(wingetHeader, gitRow)); err == nil {
			t.Error("output cut before WINGET_EXIT accepted")
		}
		// Only the script's first WINGET_PATH counts, whatever winget prints.
		spoof := "WINGET_PATH:" + testWinGetPath + "\nWINGET_PATH:C:\\Users\\Public\\winget.exe\n" + wingetTable(wingetHeader, gitRow) + "WINGET_EXIT:0\n"
		if l, err := parseWinGetListOutput(spoof); err != nil || l.Path != testWinGetPath {
			t.Errorf("path = %q, err = %v, want the first marker", l.Path, err)
		}
	})
}

func TestValidWinGetPath(t *testing.T) {
	for _, p := range []string{`C:\Program Files\WindowsApps\X\winget.exe`, `C:\Users\x\AppData\Local\Microsoft\WindowsApps\WinGet.exe`} {
		if !validWinGetPath(p) {
			t.Errorf("rejected %q", p)
		}
	}
	for _, p := range []string{"", "winget.exe", `C:\x\winget.exe.bat`, `C:\x\notwinget.cmd`, `C:\a"b\winget.exe`, `\\?\x\winget.exe2`} {
		if validWinGetPath(p) {
			t.Errorf("accepted %q", p)
		}
	}
}

func TestMarkerFilterDropsResultLines(t *testing.T) {
	var b strings.Builder
	f := &markerFilter{dst: &b}
	_, _ = f.Write([]byte("Down"))
	_, _ = f.Write([]byte("loading\r\nPATCHMON_RESULT:{}\r\n  PATCHMON_ERROR:x\nInstall"))
	f.Flush()
	if got := b.String(); got != "Downloading\r\nInstall" {
		t.Fatalf("filtered output = %q", got)
	}
}

func TestNeedsWUAScan(t *testing.T) {
	apps := []WinGetUpgrade{firefox}
	if NeedsWUAScan([]string{"Mozilla Firefox (x64 en-US)", "mozilla.firefox"}, apps) {
		t.Error("names that all resolve to WinGet apps should not trigger a Windows Update scan")
	}
	for _, names := range [][]string{{"KB5065426"}, {"Intel - Net - 1.2.3.4"}, {"Mozilla Firefox (x64 en-US)", cuUpdate.DisplayName()}} {
		if !NeedsWUAScan(names, apps) {
			t.Errorf("NeedsWUAScan(%q) = false", names)
		}
	}
}
