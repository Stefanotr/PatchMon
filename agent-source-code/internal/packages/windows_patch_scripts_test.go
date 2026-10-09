package packages

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// The patch scripts are Go string literals, so a PowerShell mistake still
// compiles. These tests execute them for real, under pwsh (preinstalled on the
// GitHub Ubuntu runners) or Windows PowerShell, against a fake of the WUA COM
// objects, so every branch of the install logic runs on every CI build without
// touching a real Windows Update.

// fakeWUAPrelude shadows New-Object with a fake Windows Update Agent driven by
// the JSON in PATCHMON_FAKE_WUA. Calls are echoed as CALL: lines.
const fakeWUAPrelude = `
$global:fake = $env:PATCHMON_FAKE_WUA | ConvertFrom-Json
function global:Fake-Call([string]$s) { [Console]::Out.WriteLine('CALL:' + $s); [Console]::Out.Flush() }
function global:Fake-Throw([int]$hr, [string]$msg) { throw [System.Runtime.InteropServices.COMException]::new($msg, $hr) }
function global:Fake-Update($spec) {
  $u = [pscustomobject]@{
    Title = [string]$spec.title
    KBArticleIDs = @($spec.kbs)
    MaxDownloadSize = [decimal]$spec.size
    IsDownloaded = [bool]$spec.downloaded
    EulaAccepted = [bool]$spec.eula
    IsInstalled = [bool]$spec.installed
    MsrcSeverity = $spec.severity
    Identity = [pscustomobject]@{ UpdateID = [string]$spec.guid }
    InstallationBehavior = [pscustomobject]@{ RebootBehavior = [int]$spec.reboot; CanRequestUserInput = [bool]$spec.needs_input }
  }
  $u | Add-Member -MemberType ScriptMethod -Name AcceptEula -Value { Fake-Call 'AcceptEula'; $this.EulaAccepted = $true }
  $u
}
function global:Fake-Result($props) {
  $r = [pscustomobject]$props
  $r | Add-Member -MemberType ScriptMethod -Name GetUpdateResult -Value { param($i) $this }
  $r
}
function global:New-Object {
  param([string]$ComObject)
  switch ($ComObject) {
    'Microsoft.Update.Session' {
      $s = [pscustomobject]@{ ClientApplicationID = '' }
      $s | Add-Member -MemberType ScriptMethod -Name CreateUpdateSearcher -Value {
        $q = [pscustomobject]@{ Online = $true }
        $q | Add-Member -MemberType ScriptMethod -Name Search -Value {
          param($criteria)
          Fake-Call ("Search online=$($this.Online) $criteria")
          if ($global:fake.search_hresult) { Fake-Throw $global:fake.search_hresult 'The search failed' }
          $all = @($global:fake.updates | Where-Object { $_ } | ForEach-Object { Fake-Update $_ })
          if ($criteria -like 'UpdateID=*') {
            if (-not $this.Online -and $global:fake.offline_miss) { return [pscustomobject]@{ Updates = @() } }
            $id = ($criteria -split "'")[1]
            return [pscustomobject]@{ Updates = @($all | Where-Object { $_.Identity.UpdateID -eq $id }) }
          }
          return [pscustomobject]@{ Updates = @($all | Where-Object { -not $_.IsInstalled }) }
        }
        $q
      }
      $s | Add-Member -MemberType ScriptMethod -Name CreateUpdateDownloader -Value {
        $d = [pscustomobject]@{ Updates = $null }
        $d | Add-Member -MemberType ScriptMethod -Name Download -Value {
          Fake-Call 'Download'
          Fake-Result @{ ResultCode = [int]$global:fake.download_code; HResult = [int]$global:fake.download_hresult }
        }
        $d
      }
      $s | Add-Member -MemberType ScriptMethod -Name CreateUpdateInstaller -Value {
        $i = [pscustomobject]@{
          Updates = $null; ClientApplicationID = ''; ForceQuiet = $false
          IsBusy = [bool]$global:fake.busy; RebootRequiredBeforeInstallation = [bool]$global:fake.reboot_before
        }
        $i | Add-Member -MemberType ScriptMethod -Name Install -Value {
          Fake-Call ("Install quiet=$($this.ForceQuiet) app=$($this.ClientApplicationID) count=$($this.Updates.Count)")
          Fake-Result @{ ResultCode = [int]$global:fake.install_code; HResult = [int]$global:fake.install_hresult; RebootRequired = [bool]$global:fake.reboot_required }
        }
        $i
      }
      return $s
    }
    'Microsoft.Update.UpdateColl' {
      $c = [pscustomobject]@{ Count = 0 }
      $c | Add-Member -MemberType ScriptMethod -Name Add -Value { param($u) Fake-Call ('Add ' + $u.Identity.UpdateID); $this.Count++; $this.Count - 1 }
      return $c
    }
  }
  throw "unexpected New-Object $ComObject"
}
`

type fakeWUA struct {
	Updates        []map[string]any `json:"updates"`
	SearchHResult  int32            `json:"search_hresult,omitempty"`
	OfflineMiss    bool             `json:"offline_miss,omitempty"`
	DownloadCode   int              `json:"download_code"`
	DownloadHRes   int32            `json:"download_hresult"`
	InstallCode    int              `json:"install_code"`
	InstallHRes    int32            `json:"install_hresult"`
	RebootRequired bool             `json:"reboot_required"`
	Busy           bool             `json:"busy"`
	RebootBefore   bool             `json:"reboot_before"`
}

// hr reinterprets an HRESULT as the signed value COM carries.
func hr(v uint32) int32 { return int32(v) }

const fakeGUID = "0b8a7c5e-1f2d-4c3b-9a8e-7d6c5b4a3f2e"

func fakeUpdate(overrides map[string]any) map[string]any {
	u := map[string]any{
		"guid": fakeGUID, "title": "2026-09 Cumulative Update for Windows Server 2022", "kbs": []string{"5065432"},
		"size": 734003200, "reboot": 1, "downloaded": false, "eula": true, "installed": false, "severity": "Critical",
	}
	for k, v := range overrides {
		u[k] = v
	}
	return u
}

func findPowerShell(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("powershell"); err == nil {
			return p
		}
	}
	t.Skip("no PowerShell available (install pwsh to run the patch script tests)")
	return ""
}

func runScript(t *testing.T, script string, env []string, out io.Writer) []byte {
	t.Helper()
	exe := findPowerShell(t)
	prev := powershellExe
	powershellExe = exe
	t.Cleanup(func() { powershellExe = prev })

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	raw, err := NewWindowsPatcher(logger).powershell(ctx, script, env, out)
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, raw)
	}
	return raw
}

func runFakeWUA(t *testing.T, script string, fake fakeWUA, extraEnv ...string) (string, []byte) {
	t.Helper()
	cfg, err := json.Marshal(fake)
	if err != nil {
		t.Fatal(err)
	}
	var shown strings.Builder
	raw := runScript(t, fakeWUAPrelude+script, append([]string{"PATCHMON_FAKE_WUA=" + string(cfg)}, extraEnv...), &shown)
	return shown.String(), raw
}

func TestWUAScanScript(t *testing.T) {
	_, raw := runFakeWUA(t, wuaScanScript, fakeWUA{
		Updates: []map[string]any{
			fakeUpdate(nil),
			fakeUpdate(map[string]any{"guid": "11111111-2222-3333-4444-555555555555", "title": "Intel - Net", "kbs": []string{}, "reboot": 0, "needs_input": true, "eula": false}),
			fakeUpdate(map[string]any{"guid": "22222222-2222-3333-4444-555555555555", "installed": true}),
		},
		Busy:         true,
		RebootBefore: true,
	})
	scan, err := ParseWUAScan(raw)
	if err != nil {
		t.Fatalf("ParseWUAScan: %v\n%s", err, raw)
	}
	if len(scan.Updates) != 2 {
		t.Fatalf("updates = %+v, want the two not installed", scan.Updates)
	}
	cu, drv := scan.Updates[0], scan.Updates[1]
	if cu.GUID != fakeGUID || cu.DisplayName() != "2026-09 Cumulative Update for Windows Server 2022 (KB5065432)" ||
		cu.SizeBytes != 734003200 || cu.RebootBehavior != 1 || cu.Severity != "Critical" || !cu.EulaAccepted || cu.Downloaded {
		t.Errorf("cumulative update = %+v", cu)
	}
	if len(drv.KBs) != 0 || drv.DisplayName() != "Intel - Net" || !drv.CanRequestUserInput || drv.EulaAccepted {
		t.Errorf("driver update = %+v", drv)
	}
	if !scan.InstallerBusy || !scan.RebootBeforeInstall {
		t.Errorf("installer state = busy %v, reboot before %v", scan.InstallerBusy, scan.RebootBeforeInstall)
	}
}

func TestWUAScanScriptEmptyResult(t *testing.T) {
	_, raw := runFakeWUA(t, wuaScanScript, fakeWUA{})
	scan, err := ParseWUAScan(raw)
	if err != nil || len(scan.Updates) != 0 {
		t.Fatalf("scan = %+v, err = %v\n%s", scan, err, raw)
	}
}

func TestWUAScanScriptReportsHResult(t *testing.T) {
	_, raw := runFakeWUA(t, wuaScanScript, fakeWUA{SearchHResult: hr(0x8024402C)})
	_, err := ParseWUAScan(raw)
	if err == nil || !strings.Contains(err.Error(), "0x8024402C") || !strings.Contains(err.Error(), "The search failed") {
		t.Fatalf("err = %v\n%s", err, raw)
	}
}

func TestWUAInstallScript(t *testing.T) {
	guidEnv := "PATCHMON_WUA_GUID=" + fakeGUID
	tests := []struct {
		name       string
		fake       fakeWUA
		env        string
		wantStatus string
		wantDetail string
		wantReboot bool
		wantCalls  []string
		notCalls   []string
	}{
		{
			name:       "downloads, accepts the EULA and installs quietly",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(map[string]any{"eula": false})}, DownloadCode: 2, InstallCode: 2, RebootRequired: true},
			wantStatus: StatusOK, wantDetail: "restart", wantReboot: true,
			wantCalls: []string{"Search online=False UpdateID='" + fakeGUID + "'", "AcceptEula", "Add " + fakeGUID, "Download", "Install quiet=True app=PatchMon count=1"},
		},
		{
			name:       "an already downloaded update is not downloaded again",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(map[string]any{"downloaded": true})}, InstallCode: 2},
			wantStatus: StatusOK, wantDetail: "installed",
			wantCalls: []string{"Install quiet=True"},
			notCalls:  []string{"Download", "AcceptEula"},
		},
		{
			name:       "falls back to an online search when the datastore misses",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(map[string]any{"downloaded": true})}, OfflineMiss: true, InstallCode: 2},
			wantStatus: StatusOK,
			wantCalls:  []string{"Search online=False", "Search online=True"},
		},
		{
			name:       "an update gone from Windows Update is skipped",
			fake:       fakeWUA{},
			wantStatus: StatusSkip, wantDetail: "no longer offers",
			notCalls: []string{"Install", "Download"},
		},
		{
			name:       "an installed update is skipped",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(map[string]any{"installed": true})}},
			wantStatus: StatusSkip, wantDetail: "already installed",
			notCalls: []string{"Install"},
		},
		{
			name:       "a failed download stops before installing",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(nil)}, DownloadCode: 4, DownloadHRes: hr(0x80244018)},
			wantStatus: StatusFail, wantDetail: "download failed (0x80244018)",
			notCalls: []string{"Install"},
		},
		{
			name:       "a failed install carries the HRESULT",
			fake:       fakeWUA{Updates: []map[string]any{fakeUpdate(map[string]any{"downloaded": true})}, InstallCode: 4, InstallHRes: hr(0x80240022)},
			wantStatus: StatusFail, wantDetail: "install failed (0x80240022)",
		},
		{
			name:       "a search exception is a failure with its HRESULT",
			fake:       fakeWUA{SearchHResult: hr(0x8024402C)},
			wantStatus: StatusFail, wantDetail: "0x8024402C",
		},
		{
			name:       "the GUID is validated again inside the script",
			fake:       fakeWUA{},
			env:        "PATCHMON_WUA_GUID=x' OR '1'='1",
			wantStatus: StatusFail, wantDetail: "invalid update ID",
			notCalls: []string{"Search"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := guidEnv
			if tt.env != "" {
				env = tt.env
			}
			shown, raw := runFakeWUA(t, wuaInstallScript, tt.fake, env)
			res, err := ParseItemResult(raw)
			if err != nil {
				t.Fatalf("ParseItemResult: %v\n%s", err, raw)
			}
			if res.Status != tt.wantStatus || !strings.Contains(res.Detail, tt.wantDetail) || res.RebootRequired != tt.wantReboot {
				t.Errorf("result = %+v, want status %s detail ~%q reboot %v\n%s", res, tt.wantStatus, tt.wantDetail, tt.wantReboot, raw)
			}
			for _, c := range tt.wantCalls {
				if !strings.Contains(shown, "CALL:"+c) {
					t.Errorf("missing call %q in:\n%s", c, shown)
				}
			}
			for _, c := range tt.notCalls {
				if strings.Contains(shown, "CALL:"+c) {
					t.Errorf("unexpected call %q in:\n%s", c, shown)
				}
			}
			if strings.Contains(shown, psResultMarker) {
				t.Errorf("result line leaked into the run output:\n%s", shown)
			}
		})
	}
}

func TestWinGetListScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script standing in for winget.exe")
	}
	findPowerShell(t)
	dir := t.TempDir()
	table := "Name                         Id               Version  Available  Source\n" +
		"--------------------------------------------------------------------------\n" +
		"Mozilla Firefox (x64 en-US)  Mozilla.Firefox  130.0    131.0.2    winget\n" +
		"1 upgrades available.\n"
	if err := os.WriteFile(filepath.Join(dir, "table.txt"), []byte(table), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := "#!/bin/sh\ncat \"$(dirname \"$0\")/table.txt\"\n"
	if err := os.WriteFile(filepath.Join(dir, "winget.exe"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}

	raw := runScript(t, wingetListScript, []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}, nil)
	listing, err := parseWinGetListOutput(string(raw))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, raw)
	}
	if listing.Path != filepath.Join(dir, "winget.exe") {
		t.Errorf("path = %q", listing.Path)
	}
	if apps := listing.Apps; len(apps) != 1 || apps[0].ID != "Mozilla.Firefox" || apps[0].Available != "131.0.2" || listing.Unreadable != 0 {
		t.Errorf("listing = %+v\n%s", listing, raw)
	}

	raw = runScript(t, wingetListScript, []string{"PATH=" + t.TempDir(), "LOCALAPPDATA=" + t.TempDir(), "ProgramFiles=" + t.TempDir()}, nil)
	if _, err := parseWinGetListOutput(string(raw)); !errors.Is(err, ErrWinGetNotInstalled) {
		t.Errorf("without winget: err = %v\n%s", err, raw)
	}
}
