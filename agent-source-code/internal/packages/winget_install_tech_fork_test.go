package packages

import (
	"testing"

	"patchmon-agent/pkg/models"
)

// Microsoft Edge on Windows Server: winget refuses to upgrade it in place
// (0x8A15008E). The item is skipped with the reason, so the run does not fail
// on it every time, and a restart the run asked for still happens.
func TestWingetInstallTechnologyMismatchIsSkipped(t *testing.T) {
	for _, dry := range []bool{false, true} {
		got := classifyWingetUpgradeExit(wingetInstallTechMismatch, dry, "", "Microsoft.Edge")
		if got.Status != itemSkipped || got.Message != msgInstallTechMismatch {
			t.Errorf("dry=%v: %+v", dry, got)
		}
	}
	it := models.PatchRunItem{Title: "Microsoft Edge", Status: itemSkipped, Message: msgInstallTechMismatch}
	if want := "Microsoft Edge (other installer technology, not upgraded in place)"; skippedLabel(it) != want {
		t.Errorf("summary label = %q, want %q", skippedLabel(it), want)
	}
}
