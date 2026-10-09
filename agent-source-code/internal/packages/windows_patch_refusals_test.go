package packages

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

var (
	refusalNow = time.Date(2026, 10, 9, 13, 32, 0, 0, time.UTC)
	edge       = WinGetUpgrade{Name: "Microsoft Edge", ID: "Microsoft.Edge", Version: "154.0.4258.62", Available: "155.0.4283.45", Source: "winget"}
)

func TestWinGetRefusalAppliesWhileNothingMoves(t *testing.T) {
	r := NewWinGetRefusals()
	r.Record(edge, "other installer technology", refusalNow)
	if !r.Dirty() {
		t.Fatal("Record did not mark the set dirty")
	}

	ref, ok := r.Lookup(edge, refusalNow.Add(time.Hour))
	if !ok || ref.Reason != "other installer technology" || ref.Installed != edge.Version || ref.Available != edge.Available {
		t.Fatalf("Lookup = %+v, %v", ref, ok)
	}
	lower := edge
	lower.ID = "microsoft.edge"
	if _, ok := r.Lookup(lower, refusalNow); !ok {
		t.Error("lookup is not case-insensitive on the ID")
	}

	for name, app := range map[string]WinGetUpgrade{
		"installed version moved": func() WinGetUpgrade { a := edge; a.Version = "155.0.4283.45"; return a }(),
		"available version moved": func() WinGetUpgrade { a := edge; a.Available = "156.0.1"; return a }(),
		"another app":             firefox,
	} {
		if _, ok := r.Lookup(app, refusalNow); ok {
			t.Errorf("%s: refusal still applies", name)
		}
	}
	if _, ok := r.Lookup(edge, refusalNow.Add(RefusalTTL)); ok {
		t.Error("refusal outlived RefusalTTL")
	}
	if _, ok := r.Lookup(edge, refusalNow.Add(-2*time.Hour)); ok {
		t.Error("a refusal dated in the future (clock set back) still applies")
	}
}

func TestWinGetRefusalsPruneAndForget(t *testing.T) {
	moved := firefox
	r := NewWinGetRefusals()
	r.Record(edge, "x", refusalNow)
	r.Record(moved, "x", refusalNow)
	r.Record(WinGetUpgrade{ID: "Gone.App", Version: "1", Available: "2"}, "x", refusalNow)

	moved.Available = "999"
	clean, _ := ReadWinGetRefusals(encoded(t, r))
	clean.Prune([]WinGetUpgrade{edge, moved}, refusalNow)
	if clean.Len() != 1 || !clean.Dirty() {
		t.Fatalf("after prune: %d refusal(s), dirty %v; want only Edge", clean.Len(), clean.Dirty())
	}
	if _, ok := clean.Lookup(edge, refusalNow); !ok {
		t.Fatal("Edge refusal pruned although nothing moved")
	}

	untouched, _ := ReadWinGetRefusals(encoded(t, clean))
	untouched.Prune([]WinGetUpgrade{edge}, refusalNow)
	if untouched.Dirty() {
		t.Error("a prune that removed nothing marked the set dirty")
	}
	untouched.Forget("MICROSOFT.EDGE")
	if untouched.Len() != 0 || !untouched.Dirty() {
		t.Error("Forget did not drop the refusal")
	}
}

func TestWinGetRefusalsRoundTrip(t *testing.T) {
	r := NewWinGetRefusals()
	r.Record(edge, "line one\nline two", refusalNow)
	got, err := ReadWinGetRefusals(encoded(t, r))
	if err != nil {
		t.Fatal(err)
	}
	if got.Dirty() {
		t.Error("a freshly read set is dirty")
	}
	ref, ok := got.Lookup(edge, refusalNow)
	if !ok || ref.Reason != "line one line two" || !ref.At.Equal(refusalNow) {
		t.Fatalf("round trip lost the refusal: %+v, %v", ref, ok)
	}
}

// The file lives on the host and decides what runs skip, so nothing in it is
// taken on trust.
func TestReadWinGetRefusalsRejectsBadInput(t *testing.T) {
	for name, in := range map[string]string{
		"not JSON":       "{",
		"unknown format": `{"format":2,"winget_refusals":[]}`,
		"too large":      `{"format":1,"winget_refusals":[],"pad":"` + strings.Repeat("a", MaxRefusalsFileBytes) + `"}`,
	} {
		if _, err := ReadWinGetRefusals(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	at := refusalNow.Format(time.RFC3339)
	entry := func(id, installed, available, when string) string {
		return fmt.Sprintf(`{"id":%q,"installed":%q,"available":%q,"reason":"r","at":%q}`, id, installed, available, when)
	}
	in := `{"format":1,"winget_refusals":[` + strings.Join([]string{
		entry("Microsoft.Edge", edge.Version, edge.Available, at),
		entry("--force", "1", "2", at),
		entry("Bad ID", "1", "2", at),
		entry("Ok.App", "1\n2", "3", at),
		entry("Ok.App2", "", "3", at),
		entry("Ok.App3", "1", "2", "0001-01-01T00:00:00Z"),
	}, ",") + `]}`
	r, err := ReadWinGetRefusals(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 1 || !r.Dirty() {
		t.Fatalf("kept %d entries (dirty %v); want only the valid Edge entry, and the set marked for rewrite", r.Len(), r.Dirty())
	}
}

func TestWinGetRefusalsStayBounded(t *testing.T) {
	r := NewWinGetRefusals()
	for i := range maxRefusals + 10 {
		r.Record(WinGetUpgrade{ID: fmt.Sprintf("App.N%03d", i), Version: "1", Available: "2"}, strings.Repeat("x", 4*maxRefusalReason), refusalNow.Add(time.Duration(i)*time.Second))
	}
	if r.Len() != maxRefusals {
		t.Fatalf("held %d refusals, want %d", r.Len(), maxRefusals)
	}
	if _, ok := r.byID[refusalKey("App.N000")]; ok {
		t.Error("the oldest refusal was kept over newer ones")
	}
	for _, ref := range r.byID {
		if len(ref.Reason) > maxRefusalReason {
			t.Fatalf("reason not clipped: %d bytes", len(ref.Reason))
		}
	}
	r.Record(WinGetUpgrade{ID: "Cut.Id", IDTruncated: true, Version: "1", Available: "2"}, "x", refusalNow)
	if _, ok := r.byID[refusalKey("Cut.Id")]; ok {
		t.Error("recorded a refusal for a truncated ID")
	}
}

func TestClipKeepsCharactersWhole(t *testing.T) {
	if got := clip("abé", 3); got != "ab" {
		t.Errorf("clip split a character: %q", got)
	}
	if got := clip("abc", 5); got != "abc" {
		t.Errorf("clip changed a short string: %q", got)
	}
}

func TestOnlyTechMismatchIsRemembered(t *testing.T) {
	if r := ClassifyWinGetExit(wingetInstallTechMismatch); r.Status != StatusSkip || r.Refusal == "" {
		t.Errorf("tech mismatch: %+v, want a skip carrying a refusal", r)
	}
	// A pin can be lifted at any time without a version moving.
	for _, code := range []uint32{wingetPackageIsPinned, wingetPackageInUse, wingetNoApplicableUpdate, 1603} {
		if r := ClassifyWinGetExit(code); r.Refusal != "" {
			t.Errorf("0x%08X is remembered as a refusal", code)
		}
	}
}

func encoded(t *testing.T, r *WinGetRefusals) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	if err := r.Encode(&b); err != nil {
		t.Fatal(err)
	}
	return &b
}
