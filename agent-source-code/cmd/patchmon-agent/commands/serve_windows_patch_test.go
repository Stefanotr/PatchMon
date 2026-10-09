package commands

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"patchmon-agent/internal/packages"
)

// fakeWindowsBackend stands in for the host. Every call is recorded so tests
// can assert on what a run did, and above all on what a dry run did not do.
type fakeWindowsBackend struct {
	mu    sync.Mutex
	calls []string

	scan       packages.WUAScan
	scanErr    error
	apps       []packages.WinGetUpgrade
	appsErr    error
	unreadable int
	wuaRes     map[string]packages.ItemResult
	wingetRes  map[string]packages.ItemResult
	pending    bool
	// onInstall runs inside an install call, before it returns.
	onInstall func(ctx context.Context, out io.Writer)
	// onScan runs inside ScanWUA, before it returns.
	onScan func(ctx context.Context) error
	// stateFile stands in for the state file on the host; stateErr fails
	// reading it.
	stateFile []byte
	stateErr  error
}

func (f *fakeWindowsBackend) record(c string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeWindowsBackend) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeWindowsBackend) ScanWUA(ctx context.Context) (packages.WUAScan, error) {
	f.record("scan")
	if f.onScan != nil {
		if err := f.onScan(ctx); err != nil {
			return packages.WUAScan{}, err
		}
	}
	return f.scan, f.scanErr
}

func (f *fakeWindowsBackend) ListWinGetUpgrades(context.Context) (packages.WinGetListing, error) {
	f.record("list")
	if f.appsErr != nil {
		return packages.WinGetListing{}, f.appsErr
	}
	return packages.WinGetListing{Path: `C:\Program Files\WindowsApps\winget.exe`, Apps: f.apps, Unreadable: f.unreadable}, nil
}

func (f *fakeWindowsBackend) InstallWUA(ctx context.Context, u packages.WUAUpdate, out io.Writer) (packages.ItemResult, error) {
	f.record("install-wua " + u.GUID)
	if f.onInstall != nil {
		f.onInstall(ctx, out)
	}
	if r, ok := f.wuaRes[u.GUID]; ok {
		return r, nil
	}
	return packages.ItemResult{Status: packages.StatusOK, Detail: "installed"}, nil
}

func (f *fakeWindowsBackend) UpgradeWinGet(ctx context.Context, _ string, app packages.WinGetUpgrade, out io.Writer) (packages.ItemResult, error) {
	f.record("upgrade-winget " + app.ID)
	if f.onInstall != nil {
		f.onInstall(ctx, out)
	}
	if r, ok := f.wingetRes[app.ID]; ok {
		return r, nil
	}
	return packages.ItemResult{Status: packages.StatusOK, Detail: "upgraded"}, nil
}

func (f *fakeWindowsBackend) LoadWinGetRefusals() (*packages.WinGetRefusals, error) {
	f.record("load-refusals")
	if f.stateErr != nil {
		return nil, f.stateErr
	}
	if f.stateFile == nil {
		return packages.NewWinGetRefusals(), nil
	}
	return packages.ReadWinGetRefusals(bytes.NewReader(f.stateFile))
}

func (f *fakeWindowsBackend) SaveWinGetRefusals(r *packages.WinGetRefusals) error {
	f.record("save-refusals")
	var b bytes.Buffer
	if err := r.Encode(&b); err != nil {
		return err
	}
	f.stateFile = b.Bytes()
	return nil
}

func (f *fakeWindowsBackend) RebootPending() (bool, string) {
	f.record("reboot-check")
	return f.pending, "Windows Update requires a restart"
}

type fakeWindowsServer struct {
	mu      sync.Mutex
	known   []string
	knowErr error
	results []string
	reboots []bool
}

func (s *fakeWindowsServer) KnownPendingWUAGUIDs(context.Context) ([]string, error) {
	return s.known, s.knowErr
}

func (s *fakeWindowsServer) ReportWUAResult(guid string, ok bool, _ string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := "fail"
	if ok {
		state = "ok"
	}
	s.results = append(s.results, guid+"="+state)
}

func (s *fakeWindowsServer) ReportReboot(needed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reboots = append(s.reboots, needed)
}

// lockedBuffer is a concurrency-safe output sink that also counts flushes.
type lockedBuffer struct {
	mu      sync.Mutex
	b       strings.Builder
	flushes int
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushes++
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

var (
	testCU = packages.WUAUpdate{
		GUID: "11111111-2222-3333-4444-555555555555", Title: "2026-09 Cumulative Update for Windows Server 2022",
		KBs: []string{"KB5065432"}, SizeBytes: 700 << 20, RebootBehavior: 1, EulaAccepted: true,
	}
	testDefender = packages.WUAUpdate{
		GUID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Title: "Security Intelligence Update for Microsoft Defender Antivirus",
		KBs: []string{"KB2267602"}, SizeBytes: 90 << 20, EulaAccepted: true,
	}
	testFirefox = packages.WinGetUpgrade{Name: "Mozilla Firefox (x64 en-US)", ID: "Mozilla.Firefox", Version: "130.0", Available: "131.0.2", Source: "winget"}
	test7zip    = packages.WinGetUpgrade{Name: "7-Zip 24.08 (x64)", ID: "7zip.7zip", Version: "24.08", Available: "24.09", Source: "winget"}
)

func newFakeHost() *fakeWindowsBackend {
	return &fakeWindowsBackend{
		scan: packages.WUAScan{Updates: []packages.WUAUpdate{testCU, testDefender}},
		apps: []packages.WinGetUpgrade{testFirefox, test7zip},
	}
}

func fastWindowsOpts() windowsPatchOptions {
	return windowsPatchOptions{
		inventoryTimeout:  5 * time.Second,
		wuaItemTimeout:    5 * time.Second,
		wingetItemTimeout: 5 * time.Second,
		heartbeat:         time.Hour,
		flushEvery:        time.Hour,
	}
}

func runWindows(ctx context.Context, t *testing.T, req windowsPatchRequest, be *fakeWindowsBackend, srv *fakeWindowsServer, opts windowsPatchOptions) (windowsPatchOutcome, string) {
	t.Helper()
	var out lockedBuffer
	outcome := executeWindowsPatch(ctx, req, be, srv, &out, opts)
	return outcome, out.String()
}

func installCalls(calls []string) []string {
	var out []string
	for _, c := range calls {
		if strings.HasPrefix(c, "install-wua") || strings.HasPrefix(c, "upgrade-winget") {
			out = append(out, c)
		}
	}
	return out
}

func TestWindowsDryRunInstallsNothing(t *testing.T) {
	for _, patchType := range []string{"patch_package", "patch_all"} {
		t.Run(patchType, func(t *testing.T) {
			be := newFakeHost()
			srv := &fakeWindowsServer{known: []string{testCU.GUID, testDefender.GUID}}
			req := windowsPatchRequest{patchType: patchType, dryRun: true}
			if patchType == "patch_package" {
				req.names = []string{testCU.DisplayName(), "Mozilla Firefox (x64 en-US)"}
			}

			outcome, out := runWindows(context.Background(), t, req, be, srv, fastWindowsOpts())

			if got := installCalls(be.recorded()); len(got) != 0 {
				t.Fatalf("dry run called installers: %v", got)
			}
			if slices.Contains(be.recorded(), "reboot-check") || len(srv.results) != 0 || len(srv.reboots) != 0 {
				t.Errorf("dry run reported results to the server: calls %v, results %v, reboots %v", be.recorded(), srv.results, srv.reboots)
			}
			if outcome.err != nil || outcome.attempted != 0 {
				t.Errorf("outcome = %+v, want a clean dry run", outcome)
			}
			for _, want := range []string{
				"dry run: nothing will be installed",
				"[plan] " + testCU.DisplayName() + "\n",
				"[plan] Mozilla Firefox (x64 en-US)\n",
				"restart is expected afterwards",
				"Dry run:",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
		})
	}
}

// A dry run must fail for the same reasons the real run would, or approving
// it means nothing.
func TestWindowsDryRunFailsOnBlockers(t *testing.T) {
	be := newFakeHost()
	be.scan.RebootBeforeInstall = true
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"KB5065432", "7zip.7zip"}, dryRun: true}

	outcome, out := runWindows(context.Background(), t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	if outcome.err == nil || !strings.Contains(outcome.err.Error(), "KB5065432") {
		t.Fatalf("outcome.err = %v, want the blocked update named", outcome.err)
	}
	if !strings.Contains(out, "[fail] KB5065432\n") || !strings.Contains(out, "requires a restart before it will install") {
		t.Errorf("blocked update not reported:\n%s", out)
	}
	if !strings.Contains(out, "[plan] 7zip.7zip\n") {
		t.Errorf("unblocked app missing from the plan:\n%s", out)
	}
	if len(installCalls(be.recorded())) != 0 {
		t.Errorf("dry run installed: %v", be.recorded())
	}
}

func TestWindowsRealRunOrderAndReports(t *testing.T) {
	be := newFakeHost()
	be.pending = true
	be.wingetRes = map[string]packages.ItemResult{"7zip.7zip": {Status: packages.StatusFail, Detail: "the application is running"}}
	srv := &fakeWindowsServer{known: []string{testCU.GUID, testDefender.GUID}}
	req := windowsPatchRequest{patchType: "patch_all"}

	outcome, out := runWindows(context.Background(), t, req, be, srv, fastWindowsOpts())

	// WinGet first, then Windows Updates, each in inventory order.
	want := []string{"upgrade-winget Mozilla.Firefox", "upgrade-winget 7zip.7zip", "install-wua " + testCU.GUID, "install-wua " + testDefender.GUID}
	if got := installCalls(be.recorded()); !slices.Equal(got, want) {
		t.Fatalf("install order = %v, want %v", got, want)
	}
	if outcome.attempted != 4 || outcome.err == nil || !strings.Contains(outcome.err.Error(), "7-Zip") {
		t.Errorf("outcome = %+v, want 4 attempted and the 7-Zip failure", outcome)
	}
	if !slices.Equal(srv.results, []string{testCU.GUID + "=ok", testDefender.GUID + "=ok"}) {
		t.Errorf("WUA results = %v", srv.results)
	}
	if !slices.Equal(srv.reboots, []bool{true}) {
		t.Errorf("reboot reports = %v, want one true", srv.reboots)
	}
	for _, s := range []string{"[ok] Mozilla Firefox (x64 en-US)\n", "[fail] 7-Zip 24.08 (x64)\n", "[ok] " + testCU.DisplayName() + "\n", "Restart required: yes", "Done: 3 installed, 1 failed"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

// patch_all installs only updates PatchMon has shown as pending, so a run
// never installs something no operator was shown.
func TestWindowsPatchAllHoldsBackUnknownUpdates(t *testing.T) {
	be := newFakeHost()
	srv := &fakeWindowsServer{known: []string{testDefender.GUID}}

	_, out := runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_all"}, be, srv, fastWindowsOpts())

	if slices.Contains(be.recorded(), "install-wua "+testCU.GUID) {
		t.Fatalf("installed an update the server did not list: %v", be.recorded())
	}
	if !slices.Contains(be.recorded(), "install-wua "+testDefender.GUID) {
		t.Errorf("known update not installed: %v", be.recorded())
	}
	if !strings.Contains(out, "[skip] "+testCU.DisplayName()+"\n") {
		t.Errorf("held-back update not reported:\n%s", out)
	}
}

func TestWindowsPatchAllWithoutServerListInstallsNoUpdates(t *testing.T) {
	be := newFakeHost()
	srv := &fakeWindowsServer{knowErr: errors.New("503")}

	outcome, _ := runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_all"}, be, srv, fastWindowsOpts())

	for _, c := range be.recorded() {
		if strings.HasPrefix(c, "install-wua") {
			t.Fatalf("installed %s without the server's pending list", c)
		}
	}
	if outcome.err == nil {
		t.Error("run succeeded although Windows Updates could not be checked against the server")
	}
}

// A run naming only apps must not pay for the slow online WUA search.
func TestWindowsPackageRunSkipsWUAScanForApps(t *testing.T) {
	be := newFakeHost()
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"Mozilla Firefox (x64 en-US)"}}

	runWindows(context.Background(), t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	if slices.Contains(be.recorded(), "scan") {
		t.Errorf("WUA scanned for an app-only run: %v", be.recorded())
	}
}

func TestWindowsNotPendingIsSkipNotFailure(t *testing.T) {
	be := newFakeHost()
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"Notepad++"}}

	outcome, out := runWindows(context.Background(), t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	if outcome.err != nil {
		t.Errorf("err = %v, want success like apt's \"already the newest version\"", outcome.err)
	}
	if !strings.Contains(out, "[skip] Notepad++\n") || !strings.Contains(out, "Plan: nothing to install") {
		t.Errorf("output:\n%s", out)
	}
}

// Stopping lets the running installer finish and starts nothing else.
func TestWindowsStopIsGraceful(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	be := newFakeHost()
	var interrupted bool
	be.onInstall = func(stepCtx context.Context, _ io.Writer) {
		if len(installCalls(be.recorded())) == 1 {
			cancel()
			time.Sleep(20 * time.Millisecond)
			interrupted = stepCtx.Err() != nil
		}
	}
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"Mozilla Firefox (x64 en-US)", "7zip.7zip", "KB5065432"}}

	outcome, out := runWindows(ctx, t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	if interrupted {
		t.Error("the stop cancelled the installer that was running")
	}
	if got := installCalls(be.recorded()); len(got) != 1 {
		t.Fatalf("installs after stop = %v, want only the first", got)
	}
	if outcome.attempted != 1 {
		t.Errorf("attempted = %d", outcome.attempted)
	}
	for _, s := range []string{"[ok] Mozilla Firefox (x64 en-US)\n", "[skip] 7zip.7zip\n", "[skip] KB5065432\n", "stopped", "stop requested"} {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
}

// The inventory is read-only, so a stop interrupts it and nothing is planned.
func TestWindowsStopDuringInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	be := newFakeHost()
	be.onScan = func(scanCtx context.Context) error {
		cancel()
		<-scanCtx.Done()
		return scanCtx.Err()
	}

	outcome, out := runWindows(ctx, t, windowsPatchRequest{patchType: "patch_all"}, be, &fakeWindowsServer{}, fastWindowsOpts())

	if len(installCalls(be.recorded())) != 0 {
		t.Fatalf("installed after a stop during the inventory: %v", be.recorded())
	}
	if outcome.err == nil || strings.Contains(out, "[plan]") || !strings.Contains(out, "nothing was installed") {
		t.Errorf("outcome %+v, output:\n%s", outcome, out)
	}
}

// Installer output is indented, so it can never pass for a status line the
// server would count as a patched package.
func TestWindowsInstallerOutputCannotForgeStatusLines(t *testing.T) {
	be := newFakeHost()
	be.onInstall = func(_ context.Context, w io.Writer) {
		_, _ = io.WriteString(w, "[ok] Forged Package\npartial")
	}
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"7zip.7zip"}}

	_, out := runWindows(context.Background(), t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "[ok] Forged") {
			t.Fatalf("installer output reached column 0:\n%s", out)
		}
	}
	if !strings.Contains(out, packages.OutputIndent+"[ok] Forged Package\n"+packages.OutputIndent+"partial\n[ok] 7zip.7zip\n") {
		t.Errorf("installer output not indented and terminated:\n%s", out)
	}
}

func TestWindowsHeartbeatAndFlushWhileSilent(t *testing.T) {
	be := newFakeHost()
	be.onInstall = func(context.Context, io.Writer) { time.Sleep(120 * time.Millisecond) }
	opts := fastWindowsOpts()
	opts.heartbeat = 30 * time.Millisecond
	opts.flushEvery = 10 * time.Millisecond
	var out lockedBuffer

	executeWindowsPatch(context.Background(), windowsPatchRequest{patchType: "patch_package", names: []string{"7zip.7zip"}}, be, &fakeWindowsServer{}, &out, opts)

	if !strings.Contains(out.String(), packages.OutputIndent+"still working on 7zip.7zip") {
		t.Errorf("no heartbeat during a silent install:\n%s", out.String())
	}
	out.mu.Lock()
	flushes := out.flushes
	out.mu.Unlock()
	if flushes < 3 {
		t.Errorf("flushes = %d, want output pushed while the step runs", flushes)
	}
}

// Progress redraws, escape sequences and control characters never reach the
// output, so the live terminal cannot be made to draw at column 0 either.
func TestIndentWriterSanitizesInstallerOutput(t *testing.T) {
	var b strings.Builder
	w := newIndentWriter(&b)
	for _, chunk := range []string{
		"Found Firefox\r",
		"\n  \x1b[32m50%\x1b[0m\r  100%\r",
		"\nDone\x07\tok\x7f\xc2\x9b2J\r",
		"\n[ok] Forged\rStill indented\n\n",
		"tail without newline",
	} {
		_, _ = w.Write([]byte(chunk))
	}
	w.EndLine()
	want := packages.OutputIndent + "Found Firefox\n" +
		packages.OutputIndent + "  100%\n" +
		packages.OutputIndent + "Done\tok2J\n" +
		packages.OutputIndent + "Still indented\n" +
		"\n" +
		packages.OutputIndent + "tail without newline\n"
	if got := b.String(); got != want {
		t.Fatalf("output = %q\nwant     %q", got, want)
	}
}

func TestIndentWriterDropsOSCAndSplitsLongLinesOnCharacters(t *testing.T) {
	var b strings.Builder
	w := newIndentWriter(&b)
	_, _ = w.Write([]byte("a\x1b]0;window title\x07b\x1b]8;;http://x\x1b\\c\n"))
	long := strings.Repeat("x", maxIndentLine-1) + "\u00e9" + "tail\n"
	_, _ = w.Write([]byte(long))
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if lines[0] != packages.OutputIndent+"abc" {
		t.Errorf("OSC payload kept: %q", lines[0])
	}
	for _, l := range lines[1:] {
		if !utf8.ValidString(l) || !strings.HasPrefix(l, packages.OutputIndent) {
			t.Errorf("long line split badly: %q", l)
		}
	}
	if got := strings.Join(lines[1:], ""); strings.ReplaceAll(got, packages.OutputIndent, "") != strings.TrimSuffix(long, "\n") {
		t.Errorf("long line content changed")
	}
}

// A spinner redrawing one line forever is not output: the heartbeat must
// still fire, or the server's stall detector would time the run out.
func TestIndentWriterSpinnerDoesNotCountAsOutput(t *testing.T) {
	var b strings.Builder
	w := newIndentWriter(&b)
	w.last = time.Now().Add(-time.Hour)
	_, _ = w.Write([]byte("\r - \r \\ \r | "))
	if w.IdleFor() < time.Minute || b.Len() != 0 {
		t.Fatalf("spinner frames counted as output: idle %s, output %q", w.IdleFor(), b.String())
	}
	w.Note("still working")
	if got := b.String(); got != packages.OutputIndent+" | \n"+packages.OutputIndent+"still working\n" {
		t.Fatalf("output = %q", got)
	}
}

// patch_all leaves alone the apps WinGet only upgrades when named, as
// `winget upgrade --all` does, and says so.
func TestWindowsPatchAllHoldsBackExplicitOnlyApps(t *testing.T) {
	be := newFakeHost()
	pinned := packages.WinGetUpgrade{Name: "Contoso Pinned", ID: "Contoso.Pinned", Version: "1", Available: "2", ExplicitOnly: true}
	be.apps = append(be.apps, pinned)
	srv := &fakeWindowsServer{known: []string{testCU.GUID, testDefender.GUID}}

	outcome, out := runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_all"}, be, srv, fastWindowsOpts())

	if slices.Contains(be.recorded(), "upgrade-winget Contoso.Pinned") {
		t.Fatalf("patch_all upgraded an explicit-only app: %v", be.recorded())
	}
	if outcome.err != nil || !strings.Contains(out, "[skip] Contoso Pinned\n") {
		t.Errorf("outcome %+v, output:\n%s", outcome, out)
	}

	// Named, it is upgraded.
	be = newFakeHost()
	be.apps = append(be.apps, pinned)
	runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_package", names: []string{"Contoso Pinned"}}, be, srv, fastWindowsOpts())
	if !slices.Contains(be.recorded(), "upgrade-winget Contoso.Pinned") {
		t.Errorf("explicit-only app not upgraded when named: %v", be.recorded())
	}
}

// Apps on unreadable listing lines are left out, so the run cannot pass for
// complete, dry or not.
func TestWindowsUnreadableListingFailsTheRun(t *testing.T) {
	for _, dry := range []bool{true, false} {
		be := newFakeHost()
		be.unreadable = 1
		srv := &fakeWindowsServer{known: []string{testCU.GUID, testDefender.GUID}}

		outcome, out := runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_all", dryRun: dry}, be, srv, fastWindowsOpts())

		if outcome.err == nil || !strings.Contains(out, "[fail] WinGet\n") || !strings.Contains(out, "could not be read") {
			t.Errorf("dry=%v: outcome %+v, output:\n%s", dry, outcome, out)
		}
	}
}

func TestCapToRunBudget(t *testing.T) {
	if got := capToRunBudget(context.Background(), time.Hour); got != time.Hour {
		t.Errorf("no deadline: %s", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if got := capToRunBudget(ctx, 2*time.Hour); got > 30*time.Minute || got < 29*time.Minute {
		t.Errorf("capped to %s, want what is left of the run", got)
	}
	short, cancelShort := context.WithTimeout(context.Background(), time.Minute)
	defer cancelShort()
	if got := capToRunBudget(short, 2*time.Hour); got != windowsMinItemTimeout {
		t.Errorf("near the deadline: %s, want the floor", got)
	}
	if got := capToRunBudget(short, 5*time.Minute); got != 5*time.Minute {
		t.Errorf("a timeout under the floor is kept: %s", got)
	}
}

// Once winget has refused an upgrade in place, later runs and dry runs skip
// it up front with the reason, until a version moves.
func TestWindowsRememberedRefusal(t *testing.T) {
	edge := packages.WinGetUpgrade{Name: "Microsoft Edge", ID: "Microsoft.Edge", Version: "154.0.4258.62", Available: "155.0.4283.45", Source: "winget"}
	be := newFakeHost()
	be.apps = []packages.WinGetUpgrade{edge, test7zip}
	be.wingetRes = map[string]packages.ItemResult{"Microsoft.Edge": packages.ClassifyWinGetExit(0x8A15008E)}
	srv := &fakeWindowsServer{known: []string{testCU.GUID, testDefender.GUID}}
	edgeRun := windowsPatchRequest{patchType: "patch_package", names: []string{"Microsoft Edge"}}

	// 1. The first run asks winget, which refuses: a skip, remembered.
	outcome, out := runWindows(context.Background(), t, edgeRun, be, srv, fastWindowsOpts())
	if outcome.err != nil || !strings.Contains(out, "[skip] Microsoft Edge\n") || !slices.Contains(be.recorded(), "save-refusals") {
		t.Fatalf("first run: outcome %+v, calls %v\n%s", outcome, be.recorded(), out)
	}

	// 2. A dry run now predicts it, and saves nothing.
	be.calls = nil
	dry := edgeRun
	dry.dryRun = true
	outcome, out = runWindows(context.Background(), t, dry, be, srv, fastWindowsOpts())
	if outcome.err != nil || strings.Contains(out, "[plan] Microsoft Edge") ||
		!strings.Contains(out, "[skip] Microsoft Edge\n") || !strings.Contains(out, "winget refused this upgrade (154.0.4258.62 -> 155.0.4283.45)") {
		t.Fatalf("dry run did not predict the refusal:\n%s", out)
	}
	if slices.Contains(be.recorded(), "save-refusals") {
		t.Error("a dry run wrote the state file")
	}

	// 3. patch_all leaves Edge alone and upgrades the rest.
	be.calls = nil
	_, out = runWindows(context.Background(), t, windowsPatchRequest{patchType: "patch_all"}, be, srv, fastWindowsOpts())
	if slices.Contains(be.recorded(), "upgrade-winget Microsoft.Edge") || !slices.Contains(be.recorded(), "upgrade-winget 7zip.7zip") {
		t.Fatalf("patch_all calls %v\n%s", be.recorded(), out)
	}

	// 4. Edge updated itself to 155 and 156 is out: winget is asked again.
	be.calls = nil
	moved := edge
	moved.Version, moved.Available = "155.0.4283.45", "156.0.1"
	be.apps = []packages.WinGetUpgrade{moved}
	be.wingetRes = nil
	_, out = runWindows(context.Background(), t, edgeRun, be, srv, fastWindowsOpts())
	if !slices.Contains(be.recorded(), "upgrade-winget Microsoft.Edge") || !strings.Contains(out, "[ok] Microsoft Edge\n") {
		t.Fatalf("refusal outlived a version change: calls %v\n%s", be.recorded(), out)
	}
	if refusals, _ := be.LoadWinGetRefusals(); refusals.Len() != 0 {
		t.Errorf("%d refusal(s) left after the upgrade went through", refusals.Len())
	}
}

// A state file that cannot be read costs a retry, never the run.
func TestWindowsUnreadableStateFileRetries(t *testing.T) {
	be := newFakeHost()
	be.stateErr = errors.New("not owned by SYSTEM or Administrators")
	req := windowsPatchRequest{patchType: "patch_package", names: []string{"7zip.7zip"}}

	outcome, out := runWindows(context.Background(), t, req, be, &fakeWindowsServer{}, fastWindowsOpts())

	if outcome.err != nil || !slices.Contains(be.recorded(), "upgrade-winget 7zip.7zip") {
		t.Fatalf("outcome %+v, calls %v", outcome, be.recorded())
	}
	if !strings.Contains(out, "could not read the upgrades winget refused before (not owned by SYSTEM or Administrators)") {
		t.Errorf("warning missing:\n%s", out)
	}
}

func TestValidPatchPackageNameFor(t *testing.T) {
	windowsName := "2026-09 Cumulative Update for Windows Server 2022 (KB5065432)"
	if !validPatchPackageNameFor("windows", windowsName) {
		t.Error("Windows display name rejected on Windows")
	}
	if validPatchPackageNameFor("linux", windowsName) {
		t.Error("Windows display name accepted for a Linux package manager")
	}
	for _, bad := range []string{"", "   ", "evil\nname", "x\x1b[2J", strings.Repeat("a", 513)} {
		if validPatchPackageNameFor("windows", bad) {
			t.Errorf("accepted %q on Windows", bad)
		}
	}
}
