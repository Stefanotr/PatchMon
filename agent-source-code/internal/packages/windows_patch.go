package packages

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Windows patching works on two inventories: pending Windows Updates (WUA) and
// WinGet applications with an upgrade available. The server only knows the
// names the collectors reported, so a patch run maps those names back to
// something installable (a WUA update GUID or a WinGet package ID) against a
// fresh inventory taken on the host itself. Nothing the server sends ever
// reaches a command line: only GUIDs and IDs this host's own inventory
// produced are acted on, and those are checked against strict patterns too.
//
// This file holds the platform-neutral half (parsing, matching, planning and
// the run output format) so it is unit-tested on every CI runner.

// TargetKind says which installer a WindowsTarget goes through.
type TargetKind string

// The installers a target can go through.
const (
	// TargetWUA is a Windows Update, installed through the Windows Update Agent.
	TargetWUA TargetKind = "wua"
	// TargetWinGet is an application, upgraded with winget.exe.
	TargetWinGet TargetKind = "winget"
)

// InstallationRebootBehavior values from the WUA API.
const (
	wuaNeverReboots         = 0
	wuaAlwaysRequiresReboot = 1
	wuaCanRequestReboot     = 2
)

// WUAUpdate is one pending update returned by a Windows Update Agent search.
type WUAUpdate struct {
	GUID                string     `json:"guid"`
	Title               string     `json:"title"`
	KBs                 stringList `json:"kbs"`
	SizeBytes           int64      `json:"size"`
	RebootBehavior      int        `json:"reboot"`
	Downloaded          bool       `json:"downloaded"`
	EulaAccepted        bool       `json:"eula"`
	CanRequestUserInput bool       `json:"needs_input"`
	Severity            string     `json:"severity"`
}

// DisplayName is the name the Windows Update collector reports for this
// update, so it is also the name the server sends back. It must stay in step
// with the $displayName expression in getWindowsUpdates.
func (u WUAUpdate) DisplayName() string {
	if len(u.KBs) == 0 {
		return u.Title
	}
	return fmt.Sprintf("%s (%s)", u.Title, strings.Join(u.KBs, ", "))
}

// WUAScan is the result of one online Windows Update search plus the state of
// the installer, which decides whether anything can be installed right now.
type WUAScan struct {
	Updates []WUAUpdate `json:"updates"`
	// InstallerBusy: another WUA installation is running (Windows Update itself,
	// an admin, another tool). Installs queue behind it or fail.
	InstallerBusy bool `json:"busy"`
	// RebootBeforeInstall: Windows refuses to install anything until the
	// pending restart has happened.
	RebootBeforeInstall bool `json:"reboot_before"`
}

// WinGetUpgrade is one row of `winget list --upgrade-available`.
type WinGetUpgrade struct {
	Name      string
	ID        string
	Version   string
	Available string
	Source    string
	// NameTruncated and IDTruncated record that winget shortened the column
	// with an ellipsis, so the value is only a prefix of the real one.
	NameTruncated bool
	IDTruncated   bool
	// ExplicitOnly: winget only upgrades this app when it is named (it is
	// pinned, or its manifest sets RequireExplicitUpgrade), so patch_all
	// leaves it alone the way `winget upgrade --all` does.
	ExplicitOnly bool
}

// WindowsInventory is what a patch run knows about the host. A nil error with
// an empty list means "nothing pending"; a non-nil error means the source
// could not be read, so absence from it proves nothing.
type WindowsInventory struct {
	WUA       WUAScan
	WUAErr    error
	WinGet    []WinGetUpgrade
	WinGetErr error
	// WinGetPath is the winget.exe that produced WinGet.
	WinGetPath string
	// WinGetUnreadable counts listing lines that could not be read, so an app
	// missing from WinGet may still be upgradable.
	WinGetUnreadable int
}

// WindowsTarget is one thing a Windows patch run installs.
type WindowsTarget struct {
	Kind TargetKind
	// Name is what the run reports back: the name the server asked for when
	// there was one, so packages_affected lines up with the package list.
	Name   string
	Update WUAUpdate
	App    WinGetUpgrade
}

// Ref identifies the target unambiguously in run output.
func (t WindowsTarget) Ref() string {
	if t.Kind == TargetWUA {
		return "wua " + t.Update.GUID
	}
	return "winget " + t.App.ID
}

// UnresolvedName is a requested name that matched nothing installable.
// Fatal means the name could not be judged (ambiguous, or the inventory it
// needed failed); otherwise it is simply not pending any more, the Windows
// equivalent of apt reporting "already the newest version".
type UnresolvedName struct {
	Name   string
	Reason string
	Fatal  bool
}

const maxWindowsSelectorBytes = 512

// ValidWindowsSelector reports whether a package name sent by the server is
// acceptable for a Windows patch run. Windows names are display names with
// spaces and punctuation, so this rejects only what could corrupt the run
// output or logs. The server applies the same rule (isValidWindowsPackageName).
func ValidWindowsSelector(s string) bool {
	if len(s) == 0 || len(s) > maxWindowsSelectorBytes || !utf8.ValidString(s) {
		return false
	}
	if strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

var (
	wuaGUIDPattern  = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
	kbPattern       = regexp.MustCompile(`^(?i)KB\d{4,8}$`)
	kbSuffixPattern = regexp.MustCompile(`\(((?i:KB\d{4,8})(?:,\s*(?i:KB\d{4,8}))*)\)\s*$`)
	// WinGet IDs are dotted identifiers ("Mozilla.Firefox", "Notepad++.Notepad++")
	// or Store product IDs ("9NBLGGH4NNS1").
	wingetIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+\-]{0,255}$`)
	wingetSourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-]{0,63}$`)
)

// IsWUAGUID reports whether s is a Windows Update identity GUID.
func IsWUAGUID(s string) bool { return wuaGUIDPattern.MatchString(s) }

// looksLikeWUA reports whether a name can only refer to a Windows Update.
func looksLikeWUA(name string) bool {
	return IsWUAGUID(name) || kbPattern.MatchString(name) || kbSuffixPattern.MatchString(name)
}

// NeedsWUAScan reports whether some name could still be a Windows Update once
// the upgradable WinGet apps are known, which is the only case where a
// patch_package run pays for an online Windows Update search.
func NeedsWUAScan(names []string, apps []WinGetUpgrade) bool {
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if looksLikeWUA(name) {
			return true
		}
		if found, ambiguity := matchWinGet(name, apps); len(found) == 0 && ambiguity == "" {
			return true
		}
	}
	return false
}

// NeedsWinGetInventory reports whether resolving names requires listing WinGet.
func NeedsWinGetInventory(names []string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return !looksLikeWUA(strings.TrimSpace(n)) })
}

func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func kbSet(kbs []string) string {
	norm := make([]string, 0, len(kbs))
	for _, k := range kbs {
		norm = append(norm, strings.ToUpper(strings.TrimSpace(k)))
	}
	// Order-insensitive: WUA does not promise a stable KBArticleIDs order.
	slices.Sort(norm)
	return strings.Join(norm, ",")
}

// ResolveWindowsNames maps requested names onto installable targets.
//
// A name that can only be a Windows Update (GUID, KB number, or a display name
// ending in "(KBnnn)") is matched against WUA. Anything else is tried against
// WinGet first, then WUA, because updates without a KB (drivers) carry a bare
// title.
func ResolveWindowsNames(names []string, inv WindowsInventory) ([]WindowsTarget, []UnresolvedName) {
	var targets []WindowsTarget
	var unresolved []UnresolvedName
	seen := make(map[string]bool)
	add := func(t WindowsTarget) {
		key := strings.ToLower(t.Ref())
		if !seen[key] {
			seen[key] = true
			targets = append(targets, t)
		}
	}

	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		wuaOnly := looksLikeWUA(name)

		if !wuaOnly {
			apps, ambiguity := matchWinGet(name, inv.WinGet)
			if ambiguity != "" {
				unresolved = append(unresolved, UnresolvedName{Name: name, Reason: ambiguity, Fatal: true})
				continue
			}
			if len(apps) > 0 {
				for _, a := range apps {
					add(WindowsTarget{Kind: TargetWinGet, Name: name, App: a})
				}
				continue
			}
		}

		if ups := matchWUA(name, inv.WUA.Updates); len(ups) > 0 {
			for _, u := range ups {
				add(WindowsTarget{Kind: TargetWUA, Name: name, Update: u})
			}
			continue
		}

		// No match. That only proves the name is not pending if every source
		// it could have come from was actually read.
		var failed []string
		if inv.WUAErr != nil {
			failed = append(failed, "Windows Update scan failed: "+inv.WUAErr.Error())
		}
		if !wuaOnly && inv.WinGetErr != nil {
			failed = append(failed, "WinGet listing failed: "+inv.WinGetErr.Error())
		}
		if !wuaOnly && inv.WinGetErr == nil && inv.WinGetUnreadable > 0 {
			failed = append(failed, fmt.Sprintf("%d line(s) of the WinGet listing could not be read", inv.WinGetUnreadable))
		}
		if !wuaOnly {
			if ids := winGetNamePrefixedBy(name, inv.WinGet); len(ids) > 0 {
				// The inventory can hold a name winget cut short in a narrower
				// table. Guessing could upgrade the wrong app and reporting "not
				// pending" could hide a pending one, so the item fails instead.
				failed = append(failed, "the upgradable WinGet app(s) "+strings.Join(ids, ", ")+
					" start with this name, which may have been shortened in the inventory; patch it by WinGet ID")
			}
		}
		if len(failed) > 0 {
			unresolved = append(unresolved, UnresolvedName{Name: name, Reason: strings.Join(failed, "; "), Fatal: true})
			continue
		}
		unresolved = append(unresolved, UnresolvedName{
			Name:   name,
			Reason: "not pending on this host (already installed, superseded, or no longer offered)",
		})
	}
	return targets, unresolved
}

// winGetNamePrefixedBy lists the IDs of upgradable apps whose display name
// starts with name and is longer than it, at most three.
func winGetNamePrefixedBy(name string, apps []WinGetUpgrade) []string {
	want := normalizeName(name)
	var ids []string
	for _, a := range apps {
		n := normalizeName(a.Name)
		if len(n) > len(want) && strings.HasPrefix(n, want) {
			ids = append(ids, a.ID)
			if len(ids) == 3 {
				break
			}
		}
	}
	return ids
}

// matchWinGet finds the upgradable apps a name refers to: by exact WinGet ID,
// else by exact display name (every app carrying it, since the collector
// reports identical names as one package). A name winget truncated in its
// table that is a prefix of the requested name is never acted on, even when
// it is the only one: "Microsoft Visual C++ 2015-2022 Redist..." can be the
// x64 build when the x86 one was asked for. The name is refused instead.
func matchWinGet(name string, apps []WinGetUpgrade) ([]WinGetUpgrade, string) {
	for _, a := range apps {
		if !a.IDTruncated && strings.EqualFold(a.ID, name) {
			return []WinGetUpgrade{a}, ""
		}
	}
	want := normalizeName(name)
	var exact, prefixed []WinGetUpgrade
	for _, a := range apps {
		n := normalizeName(a.Name)
		switch {
		case n == "":
		case n == want:
			exact = append(exact, a)
		case a.NameTruncated && strings.HasPrefix(want, n):
			prefixed = append(prefixed, a)
		}
	}
	if len(exact) > 0 {
		return exact, ""
	}
	if len(prefixed) > 0 {
		ids := make([]string, 0, len(prefixed))
		for _, a := range prefixed {
			ids = append(ids, a.ID)
		}
		return nil, "winget shortened the name of the matching app(s) in its listing (" + strings.Join(ids, ", ") +
			"), so the match is not certain; patch by WinGet ID instead"
	}
	return nil, ""
}

// matchWUA finds the pending updates a name refers to: an update GUID, a KB
// number, or the display name the collector reported. One KB can ship as
// several updates (one per product); every pending one applies.
func matchWUA(name string, pending []WUAUpdate) []WUAUpdate {
	if IsWUAGUID(name) {
		return filterWUA(pending, func(u WUAUpdate) bool { return strings.EqualFold(u.GUID, name) })
	}
	if kbPattern.MatchString(name) {
		return filterWUA(pending, func(u WUAUpdate) bool {
			return slices.ContainsFunc(u.KBs, func(k string) bool { return strings.EqualFold(strings.TrimSpace(k), name) })
		})
	}
	want := normalizeName(name)
	if m := filterWUA(pending, func(u WUAUpdate) bool { return normalizeName(u.DisplayName()) == want }); len(m) > 0 {
		return m
	}
	// Microsoft re-titles updates between a report and a run, but the KB list
	// in the trailing parentheses still identifies them.
	if sm := kbSuffixPattern.FindStringSubmatch(name); sm != nil {
		want := kbSet(strings.Split(sm[1], ","))
		return filterWUA(pending, func(u WUAUpdate) bool { return len(u.KBs) > 0 && kbSet(u.KBs) == want })
	}
	return nil
}

func filterWUA(pending []WUAUpdate, keep func(WUAUpdate) bool) []WUAUpdate {
	var out []WUAUpdate
	for _, u := range pending {
		if keep(u) {
			out = append(out, u)
		}
	}
	return out
}

// SelectPatchAllTargets picks what a patch_all run installs: every pending
// update the server also lists as pending for this host, and every upgradable
// WinGet app. An update the server has not seen is held back, so a run never
// installs something no operator was shown; the next report surfaces it. An
// app WinGet only upgrades when named is held back too, as `winget upgrade
// --all` would; patching it by name upgrades it.
func SelectPatchAllTargets(inv WindowsInventory, knownGUIDs []string) (targets []WindowsTarget, heldUpdates []WUAUpdate, heldApps []WinGetUpgrade) {
	known := make(map[string]bool, len(knownGUIDs))
	for _, g := range knownGUIDs {
		known[strings.ToLower(strings.TrimSpace(g))] = true
	}
	for _, u := range inv.WUA.Updates {
		if known[strings.ToLower(u.GUID)] {
			targets = append(targets, WindowsTarget{Kind: TargetWUA, Name: u.DisplayName(), Update: u})
		} else {
			heldUpdates = append(heldUpdates, u)
		}
	}
	for _, a := range inv.WinGet {
		if a.ExplicitOnly {
			heldApps = append(heldApps, a)
			continue
		}
		targets = append(targets, WindowsTarget{Kind: TargetWinGet, Name: a.Name, App: a})
	}
	return targets, heldUpdates, heldApps
}

// Blocker returns why the target cannot be installed at all, or "". scan is
// the WUA installer state, which blocks every Windows Update at once.
func (t WindowsTarget) Blocker(scan WUAScan) string {
	switch t.Kind {
	case TargetWUA:
		if !IsWUAGUID(t.Update.GUID) {
			return "Windows Update returned an invalid update ID"
		}
		if scan.RebootBeforeInstall {
			return "Windows requires a restart before it will install any update"
		}
	case TargetWinGet:
		if t.App.IDTruncated {
			return "winget shortened the package ID in its listing (" + t.App.ID + "...), so it cannot be targeted exactly; upgrade it by hand"
		}
		if !wingetIDPattern.MatchString(t.App.ID) {
			return "unexpected WinGet package ID " + fmt.Sprintf("%q", t.App.ID)
		}
		if t.App.Source != "" && !wingetSourcePattern.MatchString(t.App.Source) {
			return "unexpected WinGet source " + fmt.Sprintf("%q", t.App.Source)
		}
	}
	return ""
}

// PlanDetail describes what installing the target involves, for run output.
func (t WindowsTarget) PlanDetail() string {
	if t.Kind == TargetWinGet {
		return fmt.Sprintf("%s -> %s (source %s)", orDefault(t.App.Version, "?"), orDefault(t.App.Available, "?"), orDefault(t.App.Source, "winget"))
	}
	u := t.Update
	parts := []string{formatBytes(u.SizeBytes)}
	switch u.RebootBehavior {
	case wuaAlwaysRequiresReboot:
		parts = append(parts, "restart required")
	case wuaCanRequestReboot:
		parts = append(parts, "may require a restart")
	}
	if u.Downloaded {
		parts = append(parts, "already downloaded")
	}
	if !u.EulaAccepted {
		parts = append(parts, "licence terms will be accepted")
	}
	if u.Severity != "" {
		parts = append(parts, "severity "+u.Severity)
	}
	return strings.Join(parts, ", ")
}

// Warning returns a non-blocking caveat about installing the target, or "".
func (t WindowsTarget) Warning() string {
	if t.Kind == TargetWUA && t.Update.CanRequestUserInput {
		return "this update can ask for user input and may fail unattended"
	}
	return ""
}

// MayNeedReboot reports whether installing the target is expected to need one.
func (t WindowsTarget) MayNeedReboot() bool {
	return t.Kind == TargetWUA && t.Update.RebootBehavior != wuaNeverReboots
}

// Patch run output carries one status line per item, starting at column 0,
// so the server can tell which packages a run touched (see
// parseWindowsPatchStatusLines on the server):
//
//	[plan] <name>
//	[ok] <name>
//	[fail] <name>
//	[skip] <name>
//
// Everything else, details and installer output alike, is indented, so a line
// an installer prints can never pass for a status line. The name is never
// empty and holds no control characters (ValidWindowsSelector, or OneLine on
// a name from this host's own inventory).
const (
	StatusPlan = "plan"
	StatusOK   = "ok"
	StatusFail = "fail"
	StatusSkip = "skip"
)

// OutputIndent prefixes every line of run output that is not a status line.
const OutputIndent = "    "

// StatusLine renders one status line, newline included.
func StatusLine(status, name string) string {
	return "[" + status + "] " + OneLine(name) + "\n"
}

// DetailLine renders an indented detail line under a status line.
func DetailLine(text string) string {
	return OutputIndent + OneLine(text) + "\n"
}

// OneLine makes s safe to print as a single line of run output.
func OneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	if s = strings.TrimSpace(s); s == "" {
		return "(unnamed)"
	}
	return s
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func formatBytes(n int64) string {
	switch {
	case n <= 0:
		return "size unknown"
	case n < 1<<20:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
}

// stringList decodes either a JSON array of strings or a single string.
// ConvertTo-Json in Windows PowerShell 5.1 unrolls one-element arrays in some
// pipelines, so a lone KB can arrive as "KB123" rather than ["KB123"].
type stringList []string

func (l *stringList) UnmarshalJSON(b []byte) error {
	var many []string
	if err := json.Unmarshal(b, &many); err == nil {
		*l = many
		return nil
	}
	var one *string
	if err := json.Unmarshal(b, &one); err != nil {
		return err
	}
	if one == nil || *one == "" {
		*l = nil
		return nil
	}
	*l = []string{*one}
	return nil
}

// Scripts report back on a single line carrying one of these markers, so
// whatever else PowerShell or an installer prints cannot be mistaken for it.
const (
	psResultMarker = "PATCHMON_RESULT:"
	psErrorMarker  = "PATCHMON_ERROR:"
)

// extractMarkedJSON returns the payload of the last result line, or the
// error a script reported. Output with neither is an error too: the script
// died before it could say anything.
func extractMarkedJSON(out []byte) ([]byte, error) {
	var result []byte
	var scriptErr string
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		switch {
		case bytes.HasPrefix(line, []byte(psResultMarker)):
			result = bytes.TrimPrefix(line, []byte(psResultMarker))
		case bytes.HasPrefix(line, []byte(psErrorMarker)):
			scriptErr = string(bytes.TrimPrefix(line, []byte(psErrorMarker)))
		}
	}
	if result != nil {
		return result, nil
	}
	if scriptErr != "" {
		return nil, errors.New(OneLine(scriptErr))
	}
	return nil, errors.New("no result from PowerShell" + tailHint(out))
}

func tailHint(out []byte) string {
	s := strings.TrimSpace(string(out))
	if s == "" {
		return ""
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[len(r)-200:])
	}
	return ": " + OneLine(s)
}

// ParseWUAScan decodes the output of the WUA scan script.
func ParseWUAScan(out []byte) (WUAScan, error) {
	payload, err := extractMarkedJSON(out)
	if err != nil {
		return WUAScan{}, err
	}
	var scan WUAScan
	if err := json.Unmarshal(payload, &scan); err != nil {
		return WUAScan{}, fmt.Errorf("decode Windows Update scan: %w", err)
	}
	kept := scan.Updates[:0]
	for _, u := range scan.Updates {
		u.Title = strings.TrimSpace(u.Title)
		if u.GUID == "" || u.Title == "" {
			continue
		}
		kept = append(kept, u)
	}
	scan.Updates = kept
	return scan, nil
}

// ItemResult is the outcome of installing one target.
type ItemResult struct {
	Status         string `json:"status"` // StatusOK, StatusFail or StatusSkip
	Detail         string `json:"detail"`
	RebootRequired bool   `json:"reboot"`
}

// ParseItemResult decodes the result line of a WUA install script.
func ParseItemResult(out []byte) (ItemResult, error) {
	payload, err := extractMarkedJSON(out)
	if err != nil {
		return ItemResult{}, err
	}
	var r ItemResult
	if err := json.Unmarshal(payload, &r); err != nil {
		return ItemResult{}, fmt.Errorf("decode install result: %w", err)
	}
	switch r.Status {
	case StatusOK, StatusFail, StatusSkip:
		return r, nil
	default:
		return ItemResult{}, fmt.Errorf("unknown install status %q", r.Status)
	}
}

// WinGet exit codes that matter to a patch run (winget returnCodes.md).
const (
	wingetNoApplicableUpdate  uint32 = 0x8A15002B
	wingetNoPackagesFound     uint32 = 0x8A150014
	wingetPackageInUse        uint32 = 0x8A150101
	wingetInstallInProgress   uint32 = 0x8A150102
	wingetRebootToFinish      uint32 = 0x8A150109
	wingetRebootBeforeInstall uint32 = 0x8A15010A
	wingetRebootInitiated     uint32 = 0x8A15010B
	wingetUpgradeVersionNewer uint32 = 0x8A15004F
	wingetInstallDowngrade    uint32 = 0x8A15010E
	wingetVersionUnknown      uint32 = 0x8A150050
	wingetPackageIsPinned     uint32 = 0x8A150068
	wingetInstallTechMismatch uint32 = 0x8A15008E
)

// ClassifyWinGetExit turns a `winget upgrade` exit code into an item result.
//
// A package winget refuses before it downloads anything (a newer version
// built with another installer technology, a blocking pin) is a skip: the host
// is unchanged, and `winget upgrade --all` skips those packages the same way.
// Failing them would fail every patch_all run on a host that has one, such as
// Microsoft Edge on Windows Server, which the OS image installs and Edge's own
// updater keeps current.
func ClassifyWinGetExit(code uint32) ItemResult {
	switch code {
	case 0:
		return ItemResult{Status: StatusOK, Detail: "upgraded"}
	case wingetRebootToFinish:
		return ItemResult{Status: StatusOK, Detail: "upgraded; a restart is needed to finish", RebootRequired: true}
	case wingetRebootInitiated:
		return ItemResult{Status: StatusOK, Detail: "upgraded; the installer started a restart", RebootRequired: true}
	case wingetNoApplicableUpdate, wingetUpgradeVersionNewer, wingetInstallDowngrade:
		return ItemResult{Status: StatusSkip, Detail: "no applicable upgrade any more (already up to date)"}
	case wingetNoPackagesFound:
		return ItemResult{Status: StatusSkip, Detail: "package no longer installed"}
	case wingetInstallTechMismatch:
		return ItemResult{Status: StatusSkip, Detail: "winget cannot upgrade this install in place: the newer version uses another installer technology " +
			"(MSI, EXE, MSIX) than the installed one. Left as is, as winget upgrade --all does; let the app update itself, or reinstall it with winget"}
	case wingetPackageIsPinned:
		return ItemResult{Status: StatusSkip, Detail: "a winget pin on this host blocks the upgrade (see winget pin list)"}
	case wingetPackageInUse:
		return ItemResult{Status: StatusFail, Detail: "the application is running; close it and retry"}
	case wingetInstallInProgress:
		return ItemResult{Status: StatusFail, Detail: "another installation is in progress; retry later"}
	case wingetRebootBeforeInstall:
		return ItemResult{Status: StatusFail, Detail: "Windows needs a restart before this upgrade can install", RebootRequired: true}
	case wingetVersionUnknown:
		return ItemResult{Status: StatusFail, Detail: "winget cannot tell the installed version, so it will not upgrade it unattended"}
	default:
		return ItemResult{Status: StatusFail, Detail: fmt.Sprintf("winget exited with 0x%08X", code)}
	}
}

// WinGetUpgradeArgs builds the argv for upgrading one app. Each value is its
// own argument (no shell, no PowerShell), and the ID and source are checked
// against strict patterns by Blocker before this is called.
func WinGetUpgradeArgs(app WinGetUpgrade) []string {
	args := []string{"upgrade", "--id", app.ID, "--exact"}
	if app.Source != "" {
		args = append(args, "--source", app.Source)
	}
	return append(args,
		"--silent",
		"--accept-source-agreements",
		"--accept-package-agreements",
		"--disable-interactivity",
	)
}

// WinGetListing is the parsed output of `winget list --upgrade-available`.
type WinGetListing struct {
	// Path is the winget.exe that produced the listing.
	Path string
	Apps []WinGetUpgrade
	// Unreadable counts lines inside a table that did not fit its columns.
	// Those apps are missing from Apps, so their absence proves nothing.
	Unreadable int
}

// parseWinGetUpgradeTables reads the tables of `winget list
// --upgrade-available`. This decides what gets installed, so unlike the
// collector's lenient parser it only accepts lines that fit the table:
//
//   - A table is a header line followed by a line of dashes (winget prints
//     no header for an empty table). The regular upgrades come first. Packages
//     that "require explicit targeting for upgrade" (pinned by their manifest)
//     follow in a table of their own, introduced by a blank line and a
//     sentence; its rows are flagged ExplicitOnly. With no regular upgrade it
//     is the only table, so it is recognised by that introduction too.
//   - A row must start every column exactly where the header does, with the
//     separating space before it, and carry a name, an ID without spaces, a
//     version and an available version. Offsets are display cells, as winget
//     pads East Asian wide characters to two cells.
//   - Anything else (the "N upgrades available." footer, the sentence that
//     introduces the second table) is not a row. Such lines after the last row
//     of a table are expected. A row-shaped line after them (a message that
//     happens to line up with the columns), a line that breaks the table
//     between two rows, and a header whose columns cannot be told apart all
//     count as unreadable, and nothing on them is installed.
//   - Only the regular table carries the footer, which counts the upgrades of
//     both tables. When it starts with that number, as it does in English and
//     most display languages, rows it announces beyond those read count as
//     unreadable too, which catches a last row that did not fit. Its presence
//     also overrides the introduction check: a first table followed by a
//     footer is the regular one, whatever message stood above it.
func parseWinGetUpgradeTables(lines []string) (apps []WinGetUpgrade, unreadable int) {
	var cols *wingetColumns
	tables, junk, tableStart := 0, 0, 0
	explicit := false
	announced := -1
	footer := -1 // count on the first footer line after the rows of the table
	endTable := func() {
		if cols == nil {
			return
		}
		if explicit && tables == 1 && footer >= 0 {
			explicit = false
			for k := tableStart; k < len(apps); k++ {
				apps[k].ExplicitOnly = false
			}
		}
		if !explicit && announced < 0 {
			announced = footer
		}
	}
	for i := 0; i < len(lines); i++ {
		if i+1 < len(lines) && isWinGetSeparator(lines[i+1]) && strings.TrimSpace(lines[i]) != "" {
			endTable()
			tables++
			explicit = tables > 1 || introducedBySentence(lines[:i])
			cols, junk, footer, tableStart = parseWinGetHeader(lines[i]), 0, -1, len(apps)
			if cols == nil {
				unreadable++
			}
			i++ // skip the separator
			continue
		}
		if cols == nil {
			continue
		}
		// The line right above a header introduces that table, even when it
		// happens to line up with the columns of this one.
		intro := i+2 < len(lines) && isWinGetSeparator(lines[i+2]) && strings.TrimSpace(lines[i+1]) != ""
		app, ok := cols.parseRow(lines[i])
		if !ok || intro {
			if line := strings.TrimSpace(lines[i]); line != "" {
				if n, isFooter := winGetFooterCount(line); isFooter && footer < 0 && len(apps) > tableStart {
					footer = n
				}
				junk++
			}
			continue
		}
		if junk > 0 {
			// Past the end of the table: never an app.
			unreadable++
			continue
		}
		app.ExplicitOnly = explicit
		apps = append(apps, app)
	}
	endTable()
	if announced > len(apps) {
		unreadable += announced - len(apps)
	}
	return apps, unreadable
}

var wingetFooterCountPattern = regexp.MustCompile(`^(\d{1,4})\s`)

// winGetFooterCount reads the count on an "N upgrades available." footer.
// The other messages winget can print under a table that start with a number
// (packages with an unknown version, packages held by a pin) all name an
// --include-* option, which is never translated.
func winGetFooterCount(line string) (int, bool) {
	m := wingetFooterCountPattern.FindStringSubmatch(line)
	if m == nil || strings.Contains(line, "--include-") {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

// introducedBySentence reports whether the lines before a header end the way
// winget introduces the explicit-targeting table when there is no regular
// one: a message ("No installed package found matching input criteria."), a
// blank line, then the sentence. The regular table is at most preceded by
// spinner frames, a progress bar, the blank line that erases them and source
// warnings, with no blank line between two messages. Text is told from
// spinner frames (one glyph) and progress bars (block characters) without
// relying on the display language.
func introducedBySentence(before []string) bool {
	n := len(before)
	return n >= 3 && isWinGetMessage(before[n-1]) && strings.TrimSpace(before[n-2]) == "" && isWinGetMessage(before[n-3])
}

func isWinGetMessage(line string) bool {
	text := strings.TrimSpace(line)
	return utf8.RuneCountInString(text) >= 10 && !strings.ContainsAny(text, "\u2588\u2592\u2591")
}

var wingetSeparatorPattern = regexp.MustCompile(`^-{3,}$`)

func isWinGetSeparator(line string) bool {
	return wingetSeparatorPattern.MatchString(strings.TrimSpace(line))
}

// wingetColumns locates the columns of one winget table, in display cells.
type wingetColumns struct {
	starts                               []int
	name, id, version, available, source int // index into starts; source is -1 when absent
}

// parseWinGetHeader maps a header line onto columns. The English column names
// are used when present; otherwise the position decides, since winget keeps
// the same columns in the same order (Name, Id, Version, Available, Source)
// in every display language.
func parseWinGetHeader(header string) *wingetColumns {
	l := toCells(header)
	var starts []int
	var words []string
	for i := 0; i < len(l.runes); {
		if unicode.IsSpace(l.runes[i]) {
			i++
			continue
		}
		start := i
		for i < len(l.runes) && !unicode.IsSpace(l.runes[i]) {
			i++
		}
		starts = append(starts, l.pos[start])
		words = append(words, strings.ToLower(string(l.runes[start:i])))
	}
	c := &wingetColumns{starts: starts, name: -1, id: -1, version: -1, available: -1, source: -1}
	for i, w := range words {
		switch w {
		case "name":
			c.name = i
		case "id":
			c.id = i
		case "version":
			c.version = i
		case "available":
			c.available = i
		case "source":
			c.source = i
		}
	}
	if c.name < 0 || c.id < 0 || c.version < 0 || c.available < 0 {
		switch len(starts) {
		case 5:
			c.name, c.id, c.version, c.available, c.source = 0, 1, 2, 3, 4
		case 4:
			c.name, c.id, c.version, c.available, c.source = 0, 1, 2, 3, -1
		default:
			return nil
		}
	}
	if c.name >= c.id || c.id >= c.version || c.version >= c.available {
		return nil
	}
	return c
}

// parseRow reads one table row, or reports that the line is not one.
func (c *wingetColumns) parseRow(line string) (WinGetUpgrade, bool) {
	l := toCells(strings.TrimRightFunc(line, unicode.IsSpace))
	if len(l.runes) == 0 || unicode.IsSpace(l.runes[0]) {
		return WinGetUpgrade{}, false
	}
	for k, start := range c.starts {
		if k == 0 || start >= l.width {
			continue
		}
		if r, ok := l.at(start - 1); !ok || r != ' ' {
			return WinGetUpgrade{}, false
		}
	}
	field := func(k int) (string, bool) {
		if k < 0 || c.starts[k] >= l.width {
			return "", false
		}
		if r, ok := l.at(c.starts[k]); !ok || unicode.IsSpace(r) {
			return "", false
		}
		end := -1
		if k+1 < len(c.starts) {
			end = c.starts[k+1]
		}
		return l.field(c.starts[k], end), true
	}
	name, ok1 := field(c.name)
	id, ok2 := field(c.id)
	version, ok3 := field(c.version)
	avail, ok4 := field(c.available)
	if !ok1 || !ok2 || !ok3 || !ok4 || strings.IndexFunc(id, unicode.IsSpace) >= 0 {
		return WinGetUpgrade{}, false
	}
	source, _ := field(c.source)
	app := WinGetUpgrade{Source: source}
	app.Name, app.NameTruncated = cutEllipsis(name)
	app.ID, app.IDTruncated = cutEllipsis(id)
	app.Version, _ = cutEllipsis(version)
	app.Available, _ = cutEllipsis(avail)
	if app.Name == "" || app.ID == "" {
		return WinGetUpgrade{}, false
	}
	return app, true
}

// cellLine is a line with the display column each rune starts at.
type cellLine struct {
	runes []rune
	pos   []int
	width int
}

func toCells(s string) cellLine {
	var l cellLine
	for _, r := range s {
		l.runes = append(l.runes, r)
		l.pos = append(l.pos, l.width)
		l.width += runeCells(r)
	}
	return l
}

// at returns the rune that starts at display column c; false when c falls
// inside a wide rune or past the end of the line.
func (l cellLine) at(c int) (rune, bool) {
	i, found := slices.BinarySearch(l.pos, c)
	if !found {
		return 0, false
	}
	return l.runes[i], true
}

// field returns the trimmed runes starting in [from, to); to < 0 means to
// the end of the line.
func (l cellLine) field(from, to int) string {
	var b strings.Builder
	for i, p := range l.pos {
		if p >= from && (to < 0 || p < to) {
			b.WriteRune(l.runes[i])
		}
	}
	return strings.TrimSpace(b.String())
}

// runeCells is the number of terminal cells winget gives a rune: two for East
// Asian wide and fullwidth characters, one otherwise.
func runeCells(r rune) int {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0x303E,
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF,
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE10 && r <= 0xFE19,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

func cutEllipsis(s string) (string, bool) {
	s = strings.TrimSpace(s)
	cut := strings.TrimSpace(stripEllipsis(s))
	return cut, cut != s
}
