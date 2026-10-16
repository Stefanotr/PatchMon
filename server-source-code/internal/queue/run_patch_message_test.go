package queue

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PatchMon/PatchMon/server-source-code/internal/db"
)

func TestRunPatchMessageCarriesRebootOnRealRunsOnly(t *testing.T) {
	at := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	p := RunPatchPayload{
		PatchRunID:   "run-1",
		PatchType:    "patch_package",
		PackageNames: []string{"curl"},
		Reboot:       &RebootOptions{Mode: "if_needed", At: &at, Message: "window"},
	}
	b, err := json.Marshal(buildRunPatchMessage(p, &db.GetPatchRunByIDRow{}, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"dry_run":false,"package_names":["curl"],"patch_run_id":"run-1","patch_type":"patch_package","reboot":{"mode":"if_needed","at":"2026-10-10T01:00:00Z","message":"window"},"type":"run_patch"}`
	if string(b) != want {
		t.Fatalf("message = %s\nwant      %s", b, want)
	}

	// Windows runs carry them too.
	if _, ok := buildRunPatchMessage(RunPatchPayload{PatchType: "patch_all", Reboot: p.Reboot}, &db.GetPatchRunByIDRow{}, true, nil)["reboot"]; !ok {
		t.Error("a Windows run lost its restart options")
	}

	p.DryRun = true
	if _, ok := buildRunPatchMessage(p, &db.GetPatchRunByIDRow{}, false, nil)["reboot"]; ok {
		t.Error("a dry run carries restart options")
	}

	// A task queued before the upgrade has no reboot field and must still decode.
	var old RunPatchPayload
	if err := json.Unmarshal([]byte(`{"hostId":"h","api_id":"a","patch_run_id":"r","patch_type":"patch_all"}`), &old); err != nil || old.Reboot != nil {
		t.Fatalf("old payload: %+v, %v", old, err)
	}
}
