package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// winget refuses to upgrade some installs in place: the newer version is
// built with another installer technology than the one installed (Microsoft
// Edge on Windows Server, installed by the OS image, is the usual case). It
// says so only when asked to upgrade, and asking again changes nothing until a
// version moves. So a run remembers the refusal, and later runs, dry runs
// included, skip the app up front with the reason instead of asking again.
//
// A refusal applies while the app is listed with the same installed and
// available versions, and for at most RefusalTTL, after which winget is
// asked again in case something else changed (the app was reinstalled with
// another installer, a new manifest was published).

// RefusalTTL bounds how long a refusal is trusted.
const RefusalTTL = 30 * 24 * time.Hour

// MaxRefusalsFileBytes caps what ReadWinGetRefusals accepts.
const MaxRefusalsFileBytes = 1 << 20

const (
	refusalsFormat   = 1
	maxRefusals      = 256
	maxRefusalReason = 512
	maxRefusalField  = 128
)

// WinGetRefusal records that winget refused to upgrade an app in place.
type WinGetRefusal struct {
	ID        string    `json:"id"`
	Installed string    `json:"installed"`
	Available string    `json:"available"`
	Reason    string    `json:"reason"`
	At        time.Time `json:"at"`
}

// WinGetRefusals is the set of remembered refusals, one per WinGet ID.
type WinGetRefusals struct {
	byID  map[string]WinGetRefusal
	dirty bool
}

// NewWinGetRefusals returns an empty set.
func NewWinGetRefusals() *WinGetRefusals {
	return &WinGetRefusals{byID: make(map[string]WinGetRefusal)}
}

func refusalKey(id string) string { return strings.ToLower(id) }

// Lookup returns the refusal that still applies to app.
func (r *WinGetRefusals) Lookup(app WinGetUpgrade, now time.Time) (WinGetRefusal, bool) {
	ref, ok := r.byID[refusalKey(app.ID)]
	if !ok || !ref.appliesTo(app, now) {
		return WinGetRefusal{}, false
	}
	return ref, true
}

func (ref WinGetRefusal) appliesTo(app WinGetUpgrade, now time.Time) bool {
	return strings.EqualFold(ref.ID, app.ID) &&
		ref.Installed == app.Version &&
		ref.Available == app.Available &&
		now.Sub(ref.At) < RefusalTTL &&
		!ref.At.After(now.Add(time.Hour)) // a clock set back must not pin it forever
}

// Record remembers that winget refused to upgrade app.
func (r *WinGetRefusals) Record(app WinGetUpgrade, reason string, now time.Time) {
	if !validRefusalID(app) {
		return
	}
	r.byID[refusalKey(app.ID)] = WinGetRefusal{
		ID:        app.ID,
		Installed: app.Version,
		Available: app.Available,
		Reason:    clip(singleLine(reason), maxRefusalReason),
		At:        now.UTC(),
	}
	r.dirty = true
	r.trim()
}

// Forget drops the refusal for a WinGet ID, after an upgrade went through.
func (r *WinGetRefusals) Forget(id string) {
	if _, ok := r.byID[refusalKey(id)]; ok {
		delete(r.byID, refusalKey(id))
		r.dirty = true
	}
}

// Prune drops every refusal that no longer applies to an app in a complete
// listing of upgradable apps.
func (r *WinGetRefusals) Prune(apps []WinGetUpgrade, now time.Time) {
	listed := make(map[string]WinGetUpgrade, len(apps))
	for _, a := range apps {
		listed[refusalKey(a.ID)] = a
	}
	for key, ref := range r.byID {
		if app, ok := listed[key]; !ok || !ref.appliesTo(app, now) {
			delete(r.byID, key)
			r.dirty = true
		}
	}
}

// Dirty reports whether the set changed since it was read.
func (r *WinGetRefusals) Dirty() bool { return r.dirty }

// Len returns the number of refusals held.
func (r *WinGetRefusals) Len() int { return len(r.byID) }

// trim keeps the newest maxRefusals entries.
func (r *WinGetRefusals) trim() {
	if len(r.byID) <= maxRefusals {
		return
	}
	all := r.sorted()
	for _, ref := range all[:len(all)-maxRefusals] {
		delete(r.byID, refusalKey(ref.ID))
	}
}

// sorted returns the refusals oldest first, then by ID, for stable output.
func (r *WinGetRefusals) sorted() []WinGetRefusal {
	all := make([]WinGetRefusal, 0, len(r.byID))
	for _, ref := range r.byID {
		all = append(all, ref)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].At.Equal(all[j].At) {
			return all[i].At.Before(all[j].At)
		}
		return all[i].ID < all[j].ID
	})
	return all
}

type refusalsFile struct {
	Format   int             `json:"format"`
	Refusals []WinGetRefusal `json:"winget_refusals"`
}

// Encode writes the set as JSON.
func (r *WinGetRefusals) Encode(w io.Writer) error {
	return json.NewEncoder(w).Encode(refusalsFile{Format: refusalsFormat, Refusals: r.sorted()})
}

// ReadWinGetRefusals reads a set written by Encode. The file sits on the host
// and steers what runs skip, so it is bounded and every entry is checked;
// entries that do not hold up are dropped rather than trusted.
func ReadWinGetRefusals(rd io.Reader) (*WinGetRefusals, error) {
	data, err := io.ReadAll(io.LimitReader(rd, MaxRefusalsFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxRefusalsFileBytes {
		return nil, errors.New("refusals file is too large")
	}
	var f refusalsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("decode refusals: %w", err)
	}
	if f.Format != refusalsFormat {
		return nil, fmt.Errorf("unknown refusals format %d", f.Format)
	}
	r := NewWinGetRefusals()
	for _, ref := range f.Refusals {
		if !validRefusalID(WinGetUpgrade{ID: ref.ID}) || ref.At.IsZero() ||
			!validRefusalField(ref.Installed) || !validRefusalField(ref.Available) {
			r.dirty = true
			continue
		}
		ref.Reason = clip(singleLine(ref.Reason), maxRefusalReason)
		r.byID[refusalKey(ref.ID)] = ref
	}
	r.trim()
	return r, nil
}

func validRefusalID(app WinGetUpgrade) bool {
	return !app.IDTruncated && wingetIDPattern.MatchString(app.ID)
}

func validRefusalField(s string) bool {
	return s != "" && len(s) <= maxRefusalField && strings.IndexFunc(s, unicode.IsControl) < 0
}

// clip shortens s to at most n bytes without splitting a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
