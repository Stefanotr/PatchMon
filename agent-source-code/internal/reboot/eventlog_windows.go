//go:build windows

package reboot

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"patchmon-agent/internal/winexec"
)

const (
	// eventLogMargin widens the search back from the time a restart was
	// scheduled, which was taken just before Windows logged it.
	eventLogMargin = 5 * time.Second
	// eventLogWaitDelay bounds the wait for PowerShell's output once its
	// time is up.
	eventLogWaitDelay = 2 * time.Second
)

// shutdownEventsScript lists the USER32 events 1074 (shutdown or restart
// initiated) and 1075 (aborted) of the System log written since
// PATCHMON_SINCE, read from the environment, never spliced into the script.
// Each comes with its record number and its named data values, read from
// the event's XML.
const shutdownEventsScript = `
$ErrorActionPreference = 'Stop'
try {
  $since = [DateTime]::Parse([string]$env:PATCHMON_SINCE, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::RoundtripKind)
  $filter = @{ LogName = 'System'; ProviderName = 'User32'; Id = 1074, 1075; StartTime = $since }
  $found = @()
  try {
    $found = @(Get-WinEvent -FilterHashtable $filter)
  } catch {
    if ([string]$_.FullyQualifiedErrorId -notlike 'NoMatchingEventsFound*') { throw }
  }
  $events = @(foreach ($e in $found) {
    $xml = [xml]$e.ToXml()
    $data = @(foreach ($d in $xml.GetElementsByTagName('Data')) {
      [pscustomobject]@{ name = [string]$d.GetAttribute('Name'); value = [string]$d.InnerText }
    })
    [pscustomobject]@{ id = [int]$e.Id; record = [long]$e.RecordId; data = $data }
  })
  Write-Output ('PATCHMON_RESULT:' + (ConvertTo-Json -InputObject $events -Compress -Depth 4))
} catch {
  Write-Output ('PATCHMON_ERROR:' + $_.Exception.Message)
}
`

// shutdownEvents runs the query within ctx; a variable so tests can fake it.
var shutdownEvents = func(ctx context.Context, since time.Time) ([]shutdownEvent, error) {
	var out bytes.Buffer
	// Windows PowerShell by absolute path, never looked up in PATH.
	cmd := winexec.PowerShell(ctx, "-NoProfile", "-NonInteractive", "-Command", winexec.Script(shutdownEventsScript))
	cmd.Env = append(os.Environ(), "PATCHMON_SINCE="+since.UTC().Format("2006-01-02T15:04:05Z"))
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.WaitDelay = eventLogWaitDelay
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("no answer in time: %w", ctx.Err())
		}
		return nil, fmt.Errorf("powershell: %w", err)
	}
	return parseShutdownEvents(winexec.TrimBOM(out.Bytes()))
}
