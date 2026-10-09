package packages

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"patchmon-agent/internal/system"
	"patchmon-agent/internal/winexec"

	"github.com/sirupsen/logrus"
)

// ErrWinGetNotInstalled means the host has no winget.exe the agent can reach.
// Windows Server before 2025 ships without it, so for a patch_all run it only
// means there are no applications to upgrade.
var ErrWinGetNotInstalled = errors.New("WinGet is not installed on this host")

// powershellExe is the PowerShell the scripts run under. Tests point it at
// pwsh to exercise the scripts on any OS.
var powershellExe = "powershell"

// processWaitDelay bounds how long a step waits for its pipes once the process
// is gone: installers launched by winget can inherit and hold them.
const processWaitDelay = 30 * time.Second

// WindowsPatcher runs patch operations on a Windows host: WUA (through its COM
// API, driven from PowerShell) for Windows Updates and winget.exe for apps.
//
// Calls are not safe to run concurrently: WUA serialises installs itself and
// two winget upgrades race on the same installers. The caller holds a lock.
type WindowsPatcher struct {
	logger *logrus.Logger
}

// NewWindowsPatcher creates a Windows patcher.
func NewWindowsPatcher(logger *logrus.Logger) *WindowsPatcher {
	return &WindowsPatcher{logger: logger}
}

// psPrelude is shared by the patch scripts. Errors stop the script so a
// failure can never be reported as success, and Describe digs out the COM
// HRESULT, which PowerShell wraps in a MethodInvocationException.
const psPrelude = `
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
function OneLine([string]$s) { return (($s -replace '[\r\n\t]+', ' ').Trim()) }
function Describe($err) {
  $e = $err.Exception
  while ($e.InnerException) { $e = $e.InnerException }
  return ('{0} (0x{1:X8})' -f (OneLine $e.Message), [int32]$e.HResult)
}
`

// wuaScanScript searches Windows Update online with the collector's criteria,
// so the run sees the same set of updates the report did, and reads the
// installer state that decides whether anything can install right now.
const wuaScanScript = psPrelude + `
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $session.ClientApplicationID = 'PatchMon'
  $searcher = $session.CreateUpdateSearcher()
  $found = $searcher.Search('IsInstalled=0 AND IsHidden=0')
} catch {
  Write-Output ('PATCHMON_ERROR:' + (Describe $_))
  exit 0
}
$updates = @()
foreach ($u in $found.Updates) {
  $kbs = @($u.KBArticleIDs | ForEach-Object { 'KB' + $_ })
  $size = [int64]0
  try { $size = [int64]$u.MaxDownloadSize } catch {}
  $reboot = 0
  $needsInput = $false
  try {
    $reboot = [int]$u.InstallationBehavior.RebootBehavior
    $needsInput = [bool]$u.InstallationBehavior.CanRequestUserInput
  } catch {}
  $updates += [pscustomobject]@{
    guid        = [string]$u.Identity.UpdateID
    title       = [string]$u.Title
    kbs         = [string[]]$kbs
    size        = $size
    reboot      = $reboot
    downloaded  = [bool]$u.IsDownloaded
    eula        = [bool]$u.EulaAccepted
    needs_input = $needsInput
    severity    = [string]$u.MsrcSeverity
  }
}
$busy = $false
$rebootBefore = $false
try {
  $installer = $session.CreateUpdateInstaller()
  $busy = [bool]$installer.IsBusy
  $rebootBefore = [bool]$installer.RebootRequiredBeforeInstallation
} catch {}
$result = [pscustomobject]@{ updates = [object[]]$updates; busy = $busy; reboot_before = $rebootBefore }
Write-Output ('PATCHMON_RESULT:' + (ConvertTo-Json -InputObject $result -Compress -Depth 5))
`

// wuaInstallScript downloads and installs the one update named by
// PATCHMON_WUA_GUID. The GUID comes from this host's own scan and is checked
// again here before it is put in the search criteria.
//
// The search goes to the local datastore first (the scan that planned the run
// just refreshed it) and online only when that misses.
const wuaInstallScript = psPrelude + `
function Emit([string]$status, [string]$detail, [bool]$reboot) {
  $r = [pscustomobject]@{ status = $status; detail = (OneLine $detail); reboot = $reboot }
  Write-Output ('PATCHMON_RESULT:' + (ConvertTo-Json -InputObject $r -Compress))
  exit 0
}
function CodeName([int]$c) {
  switch ($c) {
    0 { 'not started' } 1 { 'in progress' } 2 { 'succeeded' }
    3 { 'succeeded with errors' } 4 { 'failed' } 5 { 'aborted' }
    default { "result $c" }
  }
}
$guid = [string]$env:PATCHMON_WUA_GUID
if ($guid -notmatch '^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$') {
  Emit 'fail' 'invalid update ID' $false
}
try {
  $session = New-Object -ComObject Microsoft.Update.Session
  $session.ClientApplicationID = 'PatchMon'
  $searcher = $session.CreateUpdateSearcher()
  $criteria = "UpdateID='" + $guid + "'"
  $found = $null
  try {
    $searcher.Online = $false
    $found = $searcher.Search($criteria)
  } catch {}
  if ($null -eq $found -or $found.Updates.Count -eq 0) {
    $searcher.Online = $true
    $found = $searcher.Search($criteria)
  }
} catch {
  Emit 'fail' ('Windows Update search failed: ' + (Describe $_)) $false
}
if ($found.Updates.Count -eq 0) {
  Emit 'skip' 'Windows Update no longer offers this update (superseded or withdrawn)' $false
}
$u = $found.Updates.Item(0)
if ($u.IsInstalled) {
  Emit 'skip' 'already installed' $false
}
try {
  if (-not $u.EulaAccepted) {
    Write-Output 'Accepting licence terms'
    $u.AcceptEula()
  }
  $coll = New-Object -ComObject Microsoft.Update.UpdateColl
  [void]$coll.Add($u)
  if (-not $u.IsDownloaded) {
    Write-Output 'Downloading'
    $downloader = $session.CreateUpdateDownloader()
    $downloader.Updates = $coll
    $dr = $downloader.Download().GetUpdateResult(0)
    if ([int]$dr.ResultCode -ne 2) {
      Emit 'fail' ('download {0} (0x{1:X8})' -f (CodeName ([int]$dr.ResultCode)), [int32]$dr.HResult) $false
    }
    Write-Output 'Download complete'
  }
  Write-Output 'Installing'
  $installer = $session.CreateUpdateInstaller()
  try { $installer.ClientApplicationID = 'PatchMon' } catch {}
  # Unattended: an update that would need to show UI fails instead of hanging.
  try { $installer.ForceQuiet = $true } catch {}
  $installer.Updates = $coll
  $ir = $installer.Install()
  $ur = $ir.GetUpdateResult(0)
  $reboot = [bool]($ir.RebootRequired -or $ur.RebootRequired)
  if ([int]$ur.ResultCode -eq 2) {
    if ($reboot) { Emit 'ok' 'installed; a restart is needed to finish' $true }
    Emit 'ok' 'installed' $false
  }
  Emit 'fail' ('install {0} (0x{1:X8})' -f (CodeName ([int]$ur.ResultCode)), [int32]$ur.HResult) $reboot
} catch {
  Emit 'fail' (Describe $_) $false
}
`

// wingetListScript resolves winget.exe the way the collector does (it is a
// per-user UWP app and not on the SYSTEM PATH) and lists upgradable apps. When
// several App Installer versions are staged, the newest one is used.
const wingetListScript = `
$ErrorActionPreference = 'SilentlyContinue'
$env:TERM = 'dumb'
$wingetPath = $null
$candidate = Get-Command winget.exe -ErrorAction SilentlyContinue
if ($candidate) {
  $wingetPath = $candidate.Source
} else {
  $patterns = @(
    "$env:LOCALAPPDATA\Microsoft\WindowsApps\winget.exe",
    "$env:ProgramFiles\WindowsApps\Microsoft.DesktopAppInstaller_*\winget.exe"
  )
  foreach ($pattern in $patterns) {
    $found = Get-Item $pattern -ErrorAction SilentlyContinue |
      Sort-Object -Descending { try { [version](($_.Directory.Name -split '_')[1]) } catch { [version]'0.0' } } |
      Select-Object -First 1
    if ($found) { $wingetPath = $found.FullName; break }
  }
}
if (-not $wingetPath) {
  Write-Output 'WINGET_NOT_FOUND'
  exit 0
}
Write-Output ('WINGET_PATH:' + $wingetPath)
$out = & $wingetPath list --upgrade-available --accept-source-agreements --disable-interactivity 2>&1
$code = $LASTEXITCODE
if ($out) { $out | Out-String }
Write-Output ('WINGET_EXIT:' + $code)
`

func (p *WindowsPatcher) powershell(ctx context.Context, script string, env []string, out io.Writer) ([]byte, error) {
	var captured bytes.Buffer
	cmd := exec.CommandContext(ctx, powershellExe, "-NoProfile", "-NonInteractive", "-Command", winexec.Script(script))
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = processWaitDelay
	var filter *markerFilter
	if out != nil {
		filter = &markerFilter{dst: out}
		w := io.MultiWriter(&captured, filter)
		cmd.Stdout, cmd.Stderr = w, w
	} else {
		cmd.Stdout, cmd.Stderr = &captured, &captured
	}
	err := cmd.Run()
	if filter != nil {
		filter.Flush()
	}
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	if ctx.Err() != nil {
		return captured.Bytes(), fmt.Errorf("timed out: %w", ctx.Err())
	}
	return winexec.TrimBOM(captured.Bytes()), err
}

// ScanWUA searches Windows Update for pending updates.
func (p *WindowsPatcher) ScanWUA(ctx context.Context) (WUAScan, error) {
	out, err := p.powershell(ctx, wuaScanScript, nil, nil)
	scan, perr := ParseWUAScan(out)
	if perr == nil {
		return scan, nil
	}
	if err != nil {
		return WUAScan{}, fmt.Errorf("%w (%v)", err, perr)
	}
	return WUAScan{}, perr
}

// ListWinGetUpgrades returns the apps winget.exe can upgrade, and which
// winget.exe it asked. It returns ErrWinGetNotInstalled when there is none.
func (p *WindowsPatcher) ListWinGetUpgrades(ctx context.Context) (WinGetListing, error) {
	out, err := p.powershell(ctx, wingetListScript, nil, nil)
	if err != nil {
		return WinGetListing{}, err
	}
	return parseWinGetListOutput(string(out))
}

// parseWinGetListOutput reads the output of wingetListScript. The script
// prints WINGET_PATH before running winget and WINGET_EXIT after it, so the
// first of the one and the last of the other are its own, and only the lines
// between them, which winget printed, are parsed as tables.
func parseWinGetListOutput(out string) (WinGetListing, error) {
	out = strings.ReplaceAll(out, "\r\n", "\n")
	// winget redraws its spinner with bare carriage returns.
	out = strings.ReplaceAll(out, "\r", "\n")
	lines := strings.Split(out, "\n")

	var path string
	pathAt, exitAt := -1, -1
	exitCode := int64(-1)
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case pathAt < 0 && line == "WINGET_NOT_FOUND":
			return WinGetListing{}, ErrWinGetNotInstalled
		case pathAt < 0 && strings.HasPrefix(line, "WINGET_PATH:"):
			path, pathAt = strings.TrimPrefix(line, "WINGET_PATH:"), i
		case pathAt >= 0 && strings.HasPrefix(line, "WINGET_EXIT:"):
			if v, err := strconv.ParseInt(strings.TrimPrefix(line, "WINGET_EXIT:"), 10, 64); err == nil {
				exitCode, exitAt = v, i
			}
		}
	}
	if !validWinGetPath(path) {
		return WinGetListing{}, fmt.Errorf("unexpected winget.exe path %q", path)
	}
	if exitAt < 0 {
		return WinGetListing{}, fmt.Errorf("winget list did not finish%s", tailHint([]byte(out)))
	}
	body := lines[pathAt+1 : exitAt]
	apps, unreadable := parseWinGetUpgradeTables(body)
	// LASTEXITCODE is a signed 32-bit value in PowerShell.
	code := uint32(exitCode)
	if len(apps) == 0 && exitCode != 0 && code != wingetNoPackagesFound {
		return WinGetListing{}, fmt.Errorf("winget list exited with 0x%08X%s", code, tailHint([]byte(strings.Join(body, "\n"))))
	}
	return WinGetListing{Path: path, Apps: apps, Unreadable: unreadable}, nil
}

// validWinGetPath accepts only an absolute path to a file named winget.exe.
// Both separators are accepted so the check is testable off Windows.
func validWinGetPath(p string) bool {
	if p == "" || strings.ContainsAny(p, "\"\x00") {
		return false
	}
	base := p[strings.LastIndexAny(p, `\/`)+1:]
	if !strings.EqualFold(base, "winget.exe") {
		return false
	}
	return filepath.IsAbs(p) || (len(p) > 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'))
}

// InstallWUA downloads and installs one update, streaming progress to out.
func (p *WindowsPatcher) InstallWUA(ctx context.Context, u WUAUpdate, out io.Writer) (ItemResult, error) {
	if !IsWUAGUID(u.GUID) {
		return ItemResult{}, fmt.Errorf("invalid update ID %q", u.GUID)
	}
	raw, err := p.powershell(ctx, wuaInstallScript, []string{"PATCHMON_WUA_GUID=" + u.GUID}, out)
	res, perr := ParseItemResult(raw)
	if perr == nil {
		return res, nil
	}
	if err != nil {
		return ItemResult{}, fmt.Errorf("%w (%v)", err, perr)
	}
	return ItemResult{}, perr
}

// UpgradeWinGet upgrades one app by exact ID. winget.exe is executed directly
// with an argument vector, never through a shell or PowerShell.
func (p *WindowsPatcher) UpgradeWinGet(ctx context.Context, wingetPath string, app WinGetUpgrade, out io.Writer) (ItemResult, error) {
	if !validWinGetPath(wingetPath) {
		return ItemResult{}, fmt.Errorf("unexpected winget.exe path %q", wingetPath)
	}
	if !wingetIDPattern.MatchString(app.ID) || app.IDTruncated {
		return ItemResult{}, fmt.Errorf("refusing to target WinGet ID %q", app.ID)
	}
	if app.Source != "" && !wingetSourcePattern.MatchString(app.Source) {
		return ItemResult{}, fmt.Errorf("refusing WinGet source %q", app.Source)
	}
	cmd := exec.CommandContext(ctx, wingetPath, WinGetUpgradeArgs(app)...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = processWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return ItemResult{}, fmt.Errorf("timed out: %w", ctx.Err())
	}
	if err == nil || errors.Is(err, exec.ErrWaitDelay) {
		return ClassifyWinGetExit(0), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Windows exit codes are unsigned 32-bit; ExitCode carries them as int.
		return ClassifyWinGetExit(uint32(exitErr.ExitCode())), nil
	}
	return ItemResult{}, err
}

// RebootPending reports whether Windows is waiting for a restart, using the
// same checks as the inventory report.
func (p *WindowsPatcher) RebootPending() (bool, string) {
	return system.New(p.logger).CheckRebootRequired()
}

// markerFilter forwards script output line by line, dropping the machine
// readable result lines, which are noise in the run output.
type markerFilter struct {
	mu  sync.Mutex
	dst io.Writer
	buf []byte
}

func (f *markerFilter) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.buf = append(f.buf, p...)
	for {
		i := bytes.IndexByte(f.buf, '\n')
		if i < 0 {
			break
		}
		f.emit(f.buf[:i+1])
		f.buf = f.buf[i+1:]
	}
	return len(p), nil
}

// Flush forwards a trailing partial line.
func (f *markerFilter) Flush() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.buf) > 0 {
		f.emit(f.buf)
		f.buf = nil
	}
}

func (f *markerFilter) emit(line []byte) {
	t := bytes.TrimSpace(winexec.TrimBOM(line))
	if bytes.HasPrefix(t, []byte(psResultMarker)) || bytes.HasPrefix(t, []byte(psErrorMarker)) {
		return
	}
	_, _ = f.dst.Write(line)
}
