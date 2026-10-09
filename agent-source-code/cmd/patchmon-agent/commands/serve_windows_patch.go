package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"patchmon-agent/internal/client"
	"patchmon-agent/internal/logutil"
	"patchmon-agent/internal/packages"
)

// Windows patch runs.
//
// A run takes a fresh inventory on the host, maps what the server asked for
// onto it (internal/packages/windows_patch.go), prints a plan, and, unless it
// is a dry run, installs item by item: WinGet apps first, then Windows Updates,
// since an update can leave a restart pending that blocks app installers.
//
// Two things differ from the Linux path on purpose:
//   - Stop is graceful. Killing a Windows installer midway can leave an app or
//     the servicing stack half updated, so a stop lets the current item finish
//     and starts nothing else. The inventory is read-only, so a stop there
//     interrupts it at once.
//   - A single cumulative update can download and install for a long time
//     without printing anything, so a heartbeat line keeps the server's stall
//     detector (which watches for output) from timing the run out.

const (
	windowsPatchRunTimeout   = 4 * time.Hour
	windowsInventoryTimeout  = 20 * time.Minute
	windowsWUAItemTimeout    = 2 * time.Hour
	windowsWinGetItemTimeout = 45 * time.Minute
	windowsMinItemTimeout    = 10 * time.Minute
	windowsHeartbeatInterval = 2 * time.Minute
	windowsFlushInterval     = time.Second
)

// patchRunTimeout bounds a whole patch run on this platform.
func patchRunTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return windowsPatchRunTimeout
	}
	return 30 * time.Minute
}

// validPatchPackageName checks a package name received in run_patch.
func validPatchPackageName(name string) bool {
	return validPatchPackageNameFor(runtime.GOOS, name)
}

// validPatchPackageNameFor applies the platform's rule. Windows package names
// are display names ("Mozilla Firefox (x64 en-US)") and never reach a command
// line, so they only need to be printable; elsewhere names go to the package
// manager as arguments and must look like package names.
func validPatchPackageNameFor(goos, name string) bool {
	if goos == "windows" {
		return packages.ValidWindowsSelector(name)
	}
	return validAptPackagePattern.MatchString(name)
}

// windowsPatchBackend is the host side of a run. *packages.WindowsPatcher
// implements it; tests substitute a fake.
type windowsPatchBackend interface {
	ScanWUA(ctx context.Context) (packages.WUAScan, error)
	ListWinGetUpgrades(ctx context.Context) (packages.WinGetListing, error)
	InstallWUA(ctx context.Context, u packages.WUAUpdate, out io.Writer) (packages.ItemResult, error)
	UpgradeWinGet(ctx context.Context, wingetPath string, app packages.WinGetUpgrade, out io.Writer) (packages.ItemResult, error)
	RebootPending() (bool, string)
}

// windowsPatchServer is the server side of a run, besides the output stream.
type windowsPatchServer interface {
	KnownPendingWUAGUIDs(ctx context.Context) ([]string, error)
	ReportWUAResult(guid string, ok bool, detail string)
	ReportReboot(needed bool)
}

type windowsPatchRequest struct {
	patchType string
	names     []string
	dryRun    bool
}

type windowsPatchOptions struct {
	inventoryTimeout  time.Duration
	wuaItemTimeout    time.Duration
	wingetItemTimeout time.Duration
	heartbeat         time.Duration
	// flushEvery pushes buffered output to the server while a step runs, so
	// the last line before a long silent install shows up straight away.
	flushEvery time.Duration
}

var defaultWindowsPatchOptions = windowsPatchOptions{
	inventoryTimeout:  windowsInventoryTimeout,
	wuaItemTimeout:    windowsWUAItemTimeout,
	wingetItemTimeout: windowsWinGetItemTimeout,
	heartbeat:         windowsHeartbeatInterval,
	flushEvery:        windowsFlushInterval,
}

type windowsPatchOutcome struct {
	// err is set when the run must be reported as failed.
	err error
	// attempted counts items an installer was actually started for.
	attempted int
}

// windowsPatchMu keeps Windows patch runs from overlapping: WUA serialises
// installs on its own, but two winget upgrades would race on installers.
var windowsPatchMu sync.Mutex

// runPatchWindows wires a Windows patch run to the server.
func runPatchWindows(ctx context.Context, httpClient *client.Client, patchRunID, patchType string, packageNames []string, dryRun bool) error {
	if !windowsPatchMu.TryLock() {
		msg := "another Windows patch run is already in progress on this host"
		_ = httpClient.SendPatchOutput(ctx, patchRunID, "failed", "", msg)
		return errors.New(msg)
	}
	// Released before the post-patch report, which can take minutes and
	// must not make the next run fail as "already in progress".
	unlock := sync.OnceFunc(windowsPatchMu.Unlock)
	defer unlock()

	if patchType == "patch_package" && len(packageNames) == 0 {
		_ = httpClient.SendPatchOutput(ctx, patchRunID, "failed", "", "package_names required for patch_package")
		return fmt.Errorf("package_names required for patch_package")
	}

	if err := httpClient.SendPatchOutput(ctx, patchRunID, "started", "", ""); err != nil {
		logger.WithError(err).Warn("Failed to send patch started to server")
	}

	var fullOutput strings.Builder
	fullOutput.Grow(8192)
	sink := newStreamSink(httpClient, patchRunID, &fullOutput)

	outcome := executeWindowsPatch(ctx,
		windowsPatchRequest{patchType: patchType, names: packageNames, dryRun: dryRun},
		packages.NewWindowsPatcher(logger),
		windowsPatchServerClient{client: httpClient, patchRunID: patchRunID},
		sink, defaultWindowsPatchOptions)
	sink.Flush()

	_, wasStopped := patchRunStopped.LoadAndDelete(patchRunID)
	trailer := patchRunTrailer(wasStopped, outcome.err, dryRun)
	sink.WriteString(trailer)
	sink.Flush()

	// A background context, so a stopped run still reaches the server.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer finalCancel()

	var runErr error
	switch {
	case wasStopped:
		if err := httpClient.SendPatchOutput(finalCtx, patchRunID, "cancelled", fullOutput.String(), "stopped by user"); err != nil {
			logger.WithError(err).Warn("Failed to send patch cancelled output to server")
		}
		runErr = fmt.Errorf("patch run stopped by user")
	case outcome.err != nil:
		if err := httpClient.SendPatchOutput(finalCtx, patchRunID, "failed", fullOutput.String(), outcome.err.Error()); err != nil {
			logger.WithError(err).Warn("Failed to send patch failed output to server")
		}
		runErr = outcome.err
	default:
		stage := "completed"
		if dryRun {
			stage = "dry_run_completed"
		}
		if err := httpClient.SendPatchOutput(finalCtx, patchRunID, stage, fullOutput.String(), ""); err != nil {
			logger.WithError(err).Warn("Failed to send patch output to server")
			return err
		}
	}

	unlock()

	// Refresh the inventory whenever something was installed or attempted,
	// including on a partial failure or a stop: the host has changed either way.
	if !dryRun && outcome.attempted > 0 {
		sendPostPatchReport()
	}
	return runErr
}

// sendPostPatchReport refreshes the package lists after a patch run.
func sendPostPatchReport() {
	logger.Info("Sending post-patch report to refresh package lists...")
	reportDone := make(chan error, 1)
	go func() { reportDone <- sendReport(false) }()
	select {
	case err := <-reportDone:
		if err != nil {
			logger.WithError(err).Warn("Post-patch report failed")
		} else {
			logger.Info("Post-patch report sent successfully")
		}
	case <-time.After(2 * time.Minute):
		logger.Warn("Post-patch report timed out after 2 minutes; will retry on next scheduled report")
	}
}

// windowsPatchServerClient adapts the HTTP client. Result and reboot reports
// use their own context so they still go out after a stop.
type windowsPatchServerClient struct {
	client     *client.Client
	patchRunID string
}

func (s windowsPatchServerClient) KnownPendingWUAGUIDs(ctx context.Context) ([]string, error) {
	return s.client.GetApprovedWindowsUpdateGUIDs(ctx)
}

func (s windowsPatchServerClient) ReportWUAResult(guid string, ok bool, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res := client.WindowsUpdateResult{GUID: guid, Success: ok}
	if !ok {
		res.Error = detail
	}
	if err := s.client.SendWindowsUpdateResult(ctx, s.patchRunID, res); err != nil {
		logger.WithError(err).WithField("guid", logutil.Sanitize(guid)).Warn("Failed to report Windows Update result")
	}
}

func (s windowsPatchServerClient) ReportReboot(needed bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.client.SendWindowsRebootStatus(ctx, s.patchRunID, needed); err != nil {
		logger.WithError(err).Warn("Failed to report Windows reboot status")
	}
}

// windowsRun carries the state of one execution.
type windowsRun struct {
	ctx  context.Context
	req  windowsPatchRequest
	be   windowsPatchBackend
	srv  windowsPatchServer
	out  io.Writer
	opts windowsPatchOptions

	failed    []string
	installed int
	skipped   int
	attempted int
	reboot    bool
}

// executeWindowsPatch runs the whole Windows patch flow and writes its output
// to out. It never returns early without saying why in the output.
func executeWindowsPatch(ctx context.Context, req windowsPatchRequest, be windowsPatchBackend, srv windowsPatchServer, out io.Writer, opts windowsPatchOptions) windowsPatchOutcome {
	r := &windowsRun{ctx: ctx, req: req, be: be, srv: srv, out: out, opts: opts}

	mode := "patch run"
	if req.dryRun {
		mode = "dry run: nothing will be installed"
	}
	r.printf("== Windows %s (%s) ==\n", mode, req.patchType)

	inv := r.inventory()
	if ctx.Err() != nil {
		// Stopped (or out of time) while reading the inventory: nothing has
		// been planned or installed, and a half-read inventory proves nothing.
		r.printf("Stopped during the inventory; nothing was installed\n")
		return windowsPatchOutcome{err: fmt.Errorf("stopped during the inventory: %w", ctx.Err())}
	}
	var targets []packages.WindowsTarget
	if req.patchType == "patch_all" {
		targets = r.selectAll(inv)
	} else {
		targets = r.selectNamed(inv)
	}
	runnable := r.plan(inv, targets)

	if !req.dryRun {
		r.install(inv, runnable)
		if r.attempted > 0 {
			pending, reason := be.RebootPending()
			needed := r.reboot || pending
			switch {
			case pending:
				r.printf("Restart required: yes (%s)\n", packages.OneLine(reason))
			case needed:
				r.printf("Restart required: yes (reported by an installer)\n")
			default:
				r.printf("Restart required: no\n")
			}
			srv.ReportReboot(needed)
		}
	}
	return r.finish(len(runnable))
}

func (r *windowsRun) printf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.out, format, a...)
}

func (r *windowsRun) write(s string) {
	_, _ = io.WriteString(r.out, s)
}

// flush pushes buffered output to the server when out supports it.
func (r *windowsRun) flush() {
	if f, ok := r.out.(interface{ Flush() }); ok {
		f.Flush()
	}
}

func (r *windowsRun) status(status, name string, details ...string) {
	r.write(packages.StatusLine(status, name))
	for _, d := range details {
		if d != "" {
			r.write(packages.DetailLine(d))
		}
	}
	switch status {
	case packages.StatusFail:
		r.failed = append(r.failed, packages.OneLine(name))
	case packages.StatusSkip:
		r.skipped++
	}
}

// inventory reads only what the request needs: patch_package runs naming
// apps alone skip the slow online Windows Update search.
func (r *windowsRun) inventory() packages.WindowsInventory {
	var inv packages.WindowsInventory
	all := r.req.patchType == "patch_all"

	if all || packages.NeedsWinGetInventory(r.req.names) {
		r.printf("Listing WinGet upgrades\n")
		var listing packages.WinGetListing
		err := r.step("the WinGet listing", r.opts.inventoryTimeout, false, func(ctx context.Context, _ io.Writer) error {
			var err error
			listing, err = r.be.ListWinGetUpgrades(ctx)
			return err
		})
		inv.WinGetErr = err
		if err == nil {
			inv.WinGetPath, inv.WinGet, inv.WinGetUnreadable = listing.Path, listing.Apps, listing.Unreadable
		}
		switch {
		case errors.Is(err, packages.ErrWinGetNotInstalled):
			r.write(packages.DetailLine("WinGet is not installed; no applications to upgrade"))
		case err != nil:
			r.write(packages.DetailLine("WinGet listing failed: " + err.Error()))
		default:
			r.write(packages.DetailLine(fmt.Sprintf("%d upgradable app(s)", len(inv.WinGet))))
			if inv.WinGetUnreadable > 0 {
				r.write(packages.DetailLine(fmt.Sprintf("warning: %d line(s) of the listing could not be read", inv.WinGetUnreadable)))
			}
		}
	}

	if r.ctx.Err() == nil && (all || packages.NeedsWUAScan(r.req.names, inv.WinGet)) {
		r.printf("Searching Windows Update\n")
		err := r.step("the Windows Update search", r.opts.inventoryTimeout, false, func(ctx context.Context, _ io.Writer) error {
			var err error
			inv.WUA, err = r.be.ScanWUA(ctx)
			return err
		})
		inv.WUAErr = err
		if err != nil {
			r.write(packages.DetailLine("Windows Update search failed: " + err.Error()))
		} else {
			r.write(packages.DetailLine(fmt.Sprintf("%d pending update(s)", len(inv.WUA.Updates))))
		}
	}
	return inv
}

func (r *windowsRun) selectAll(inv packages.WindowsInventory) []packages.WindowsTarget {
	if inv.WUAErr != nil {
		r.status(packages.StatusFail, "Windows Update", "search failed: "+inv.WUAErr.Error())
	}
	if inv.WinGetErr != nil && !errors.Is(inv.WinGetErr, packages.ErrWinGetNotInstalled) {
		r.status(packages.StatusFail, "WinGet", "listing failed: "+inv.WinGetErr.Error())
	}
	if inv.WinGetUnreadable > 0 {
		// Apps on those lines are left out, so the run must not look complete.
		r.status(packages.StatusFail, "WinGet", fmt.Sprintf("%d line(s) of the listing could not be read; the apps on them are left out", inv.WinGetUnreadable))
	}

	var known []string
	if len(inv.WUA.Updates) > 0 {
		var err error
		if known, err = r.srv.KnownPendingWUAGUIDs(r.ctx); err != nil {
			r.status(packages.StatusFail, "Windows Update", "could not fetch the updates PatchMon lists as pending for this host: "+err.Error())
			inv.WUA.Updates = nil
		}
	}
	targets, heldUpdates, heldApps := packages.SelectPatchAllTargets(inv, known)
	for _, u := range heldUpdates {
		r.status(packages.StatusSkip, u.DisplayName(), "not in PatchMon's pending list for this host yet; it will be offered once the next report includes it")
	}
	for _, a := range heldApps {
		r.status(packages.StatusSkip, a.Name, "winget "+a.ID+" | WinGet only upgrades it when it is named (pinned or explicit-upgrade package); patch it on its own to upgrade it")
	}
	return targets
}

func (r *windowsRun) selectNamed(inv packages.WindowsInventory) []packages.WindowsTarget {
	targets, unresolved := packages.ResolveWindowsNames(r.req.names, inv)
	for _, u := range unresolved {
		status := packages.StatusSkip
		if u.Fatal {
			status = packages.StatusFail
		}
		r.status(status, u.Name, u.Reason)
	}
	return targets
}

// plan prints what the run will install and returns the targets that can be.
// A target with a blocker fails here in both modes, which is what makes a dry
// run worth approving: it fails for the same reasons the real run would.
func (r *windowsRun) plan(inv packages.WindowsInventory, targets []packages.WindowsTarget) []packages.WindowsTarget {
	if inv.WUA.InstallerBusy && slices.ContainsFunc(targets, func(t packages.WindowsTarget) bool { return t.Kind == packages.TargetWUA }) {
		r.write(packages.DetailLine("warning: Windows Update is busy with another installation; updates will wait for it or fail"))
	}
	var runnable []packages.WindowsTarget
	var apps, updates int
	restart := false
	for _, t := range targets {
		if b := t.Blocker(inv.WUA); b != "" {
			r.status(packages.StatusFail, t.Name, t.Ref(), b)
			continue
		}
		details := []string{t.Ref() + " | " + t.PlanDetail()}
		if w := t.Warning(); w != "" {
			details = append(details, "warning: "+w)
		}
		r.status(packages.StatusPlan, t.Name, details...)
		if t.Kind == packages.TargetWUA {
			updates++
		} else {
			apps++
		}
		restart = restart || t.MayNeedReboot()
		runnable = append(runnable, t)
	}
	switch {
	case len(runnable) == 0:
		r.printf("Plan: nothing to install\n")
	case restart:
		r.printf("Plan: %d app(s), %d update(s); a restart is expected afterwards\n", apps, updates)
	default:
		r.printf("Plan: %d app(s), %d update(s)\n", apps, updates)
	}
	return runnable
}

func (r *windowsRun) install(inv packages.WindowsInventory, runnable []packages.WindowsTarget) {
	// WinGet first: a Windows Update can leave a restart pending, and some app
	// installers refuse to run until it has happened.
	ordered := slices.Clone(runnable)
	slices.SortStableFunc(ordered, func(a, b packages.WindowsTarget) int {
		return kindOrder(a.Kind) - kindOrder(b.Kind)
	})

	for i, t := range ordered {
		if err := r.ctx.Err(); err != nil {
			why := "not started: the run was stopped"
			if errors.Is(err, context.DeadlineExceeded) {
				why = "not started: the run reached its time limit"
				r.failed = append(r.failed, "time limit reached")
			}
			for _, rest := range ordered[i:] {
				r.status(packages.StatusSkip, rest.Name, why)
			}
			return
		}
		r.installOne(inv, t)
	}
}

func kindOrder(k packages.TargetKind) int {
	if k == packages.TargetWinGet {
		return 0
	}
	return 1
}

func (r *windowsRun) installOne(inv packages.WindowsInventory, t packages.WindowsTarget) {
	r.printf("-- Installing %s [%s]\n", packages.OneLine(t.Name), t.Ref())
	r.attempted++

	timeout := r.opts.wingetItemTimeout
	if t.Kind == packages.TargetWUA {
		timeout = r.opts.wuaItemTimeout
	}
	timeout = capToRunBudget(r.ctx, timeout)
	var res packages.ItemResult
	err := r.step(packages.OneLine(t.Name), timeout, true, func(ctx context.Context, w io.Writer) error {
		var err error
		if t.Kind == packages.TargetWUA {
			res, err = r.be.InstallWUA(ctx, t.Update, w)
		} else {
			res, err = r.be.UpgradeWinGet(ctx, inv.WinGetPath, t.App, w)
		}
		return err
	})
	if err != nil {
		res = packages.ItemResult{Status: packages.StatusFail, Detail: err.Error()}
	}

	r.status(res.Status, t.Name, res.Detail)
	if res.Status == packages.StatusOK {
		r.installed++
	}
	r.reboot = r.reboot || res.RebootRequired
	if t.Kind == packages.TargetWUA && res.Status != packages.StatusSkip {
		r.srv.ReportWUAResult(t.Update.GUID, res.Status == packages.StatusOK, res.Detail)
	}
}

// capToRunBudget shortens an item timeout to what is left of the run's own
// deadline. An install step is detached from the run context (a stop must not
// kill an installer), so without this an item started late could outlive the
// run by its whole timeout. The floor gives a last item a fair chance; the
// run can overrun its budget by at most that much.
func capToRunBudget(ctx context.Context, timeout time.Duration) time.Duration {
	dl, ok := ctx.Deadline()
	if !ok {
		return timeout
	}
	return min(timeout, max(time.Until(dl), windowsMinItemTimeout))
}

// step runs fn under its own timeout. An install step is detached from the
// run's cancellation so a stop never interrupts an installer; an inventory
// step is read-only and stops with the run. While fn is silent, a heartbeat
// line keeps the run visibly alive.
func (r *windowsRun) step(label string, timeout time.Duration, detach bool, fn func(ctx context.Context, w io.Writer) error) error {
	parent := r.ctx
	if detach {
		parent = context.WithoutCancel(r.ctx)
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	r.flush()
	w := newIndentWriter(r.out)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		heartbeat := time.NewTicker(r.opts.heartbeat)
		defer heartbeat.Stop()
		flushEvery := r.opts.flushEvery
		if flushEvery <= 0 {
			flushEvery = windowsFlushInterval
		}
		flush := time.NewTicker(flushEvery)
		defer flush.Stop()
		var stopped <-chan struct{}
		if detach {
			stopped = r.ctx.Done()
		}
		for {
			select {
			case <-done:
				return
			case <-stopped:
				stopped = nil
				w.Note("stop requested: letting " + label + " finish; nothing else will start")
				r.flush()
			case <-heartbeat.C:
				if w.IdleFor() >= r.opts.heartbeat {
					w.Note(fmt.Sprintf("still working on %s (%s elapsed)", label, time.Since(start).Round(time.Second)))
					r.flush()
				}
			case <-flush.C:
				r.flush()
			}
		}
	}()

	err := fn(ctx, w)
	close(done)
	wg.Wait()
	w.EndLine()
	if !detach && r.ctx.Err() != nil && err != nil {
		return fmt.Errorf("interrupted: %w", r.ctx.Err())
	}
	if err != nil && ctx.Err() != nil && !strings.Contains(err.Error(), "timed out") {
		err = fmt.Errorf("timed out after %s: %w", timeout, err)
	}
	return err
}

func (r *windowsRun) finish(planned int) windowsPatchOutcome {
	out := windowsPatchOutcome{attempted: r.attempted}
	if r.req.dryRun {
		r.printf("Dry run: %d item(s) would be installed, %d blocked, %d skipped\n", planned, len(r.failed), r.skipped)
	} else {
		r.printf("Done: %d installed, %d failed, %d skipped\n", r.installed, len(r.failed), r.skipped)
	}
	if n := len(r.failed); n > 0 {
		names := r.failed
		if n > 5 {
			names = append(slices.Clone(names[:5]), "...")
		}
		out.err = fmt.Errorf("%d item(s) failed: %s", n, strings.Join(names, ", "))
	}
	return out
}

// maxIndentLine bounds a line held back while waiting for its end.
const maxIndentLine = 4096

// indentWriter passes installer output on as complete, indented lines, so no
// line an installer prints can start at column 0, where status lines live,
// in the stored output or in the live terminal:
//   - a bare carriage return redraws the line (winget's spinner and progress
//     bars), so the line it ends is dropped; CRLF ends a line as usual;
//   - escape sequences and other control characters are removed.
//
// It also records when a line last went out, for the heartbeat; a spinner
// that never completes a line does not count as output.
type indentWriter struct {
	mu   sync.Mutex
	dst  io.Writer
	line []byte
	cr   bool
	esc  int // escNone, escStart, escCSI or escOSC
	last time.Time
}

const (
	escNone  = iota
	escStart // after ESC
	escCSI   // inside ESC [ ... final byte
	escOSC   // inside ESC ] ... BEL or ESC \
)

func newIndentWriter(dst io.Writer) *indentWriter {
	return &indentWriter{dst: dst, last: time.Now()}
}

func (w *indentWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []byte
	for _, c := range p {
		switch w.esc {
		case escStart:
			switch c {
			case '[':
				w.esc = escCSI
			case ']':
				w.esc = escOSC
			default:
				w.esc = escNone
			}
			continue
		case escCSI:
			if c >= 0x40 && c <= 0x7e {
				w.esc = escNone
			}
			continue
		case escOSC:
			switch c {
			case 0x07:
				w.esc = escNone
			case 0x1b:
				w.esc = escStart // ESC \ ends it; the backslash is dropped there
			}
			continue
		}
		if w.cr {
			w.cr = false
			if c == '\n' {
				out = w.endLine(out)
				continue
			}
			w.line = w.line[:0] // redrawn
		}
		switch {
		case c == 0x1b:
			w.esc = escStart
		case c == '\r':
			w.cr = true
		case c == '\n':
			out = w.endLine(out)
		case c == '\t':
			w.line = append(w.line, c)
		case c < 0x20 || c == 0x7f:
		case c >= 0x80 && c <= 0x9f && len(w.line) > 0 && w.line[len(w.line)-1] == 0xc2:
			w.line = w.line[:len(w.line)-1] // UTF-8 encoded C1 control
		default:
			// Break an overlong line only where a character starts, so a
			// multi-byte one is never split.
			if len(w.line) >= maxIndentLine && (c < 0x80 || c >= 0xc0) {
				out = w.endLine(out)
			}
			w.line = append(w.line, c)
		}
	}
	w.emit(out)
	return len(p), nil
}

// endLine appends the pending line to out and clears it.
func (w *indentWriter) endLine(out []byte) []byte {
	if len(w.line) > 0 {
		out = append(out, packages.OutputIndent...)
		out = append(out, w.line...)
	}
	w.line = w.line[:0]
	return append(out, '\n')
}

func (w *indentWriter) emit(b []byte) {
	if len(b) == 0 {
		return
	}
	w.last = time.Now()
	_, _ = w.dst.Write(b)
}

// flushPartial writes out a line still waiting for its end. A line ended by
// a bare carriage return counts: nothing redrew it.
func (w *indentWriter) flushPartial() {
	w.cr = false
	if len(w.line) > 0 {
		w.emit(w.endLine(nil))
	}
}

// Note writes a line of its own, breaking off any partial installer line.
func (w *indentWriter) Note(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushPartial()
	w.emit([]byte(packages.OutputIndent + packages.OneLine(text) + "\n"))
}

// EndLine terminates a partial last line.
func (w *indentWriter) EndLine() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flushPartial()
}

func (w *indentWriter) IdleFor() time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Since(w.last)
}
