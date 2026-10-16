package packages

import (
	"context"
	"strings"
	"testing"
)

// A run that carries a restart option (this fork) must not tell the operator
// that PatchMon will not restart the host: the agent schedules it afterwards.
func TestRunRestartLineFollowsTheRestartOption(t *testing.T) {
	for _, requested := range []bool{false, true} {
		w := &fakeWUA{scan: wuaScan{Updates: []wuaUpdate{{ID: guidA, Title: "A", KBs: jsonStrings{"KB9"}, Type: 1}}}}
		rec := &recorder{reboot: true}
		hooks := rec.hooks()
		hooks.RestartRequested = requested
		newWindowsPatchRun(WindowsPatchRequest{PatchAll: true, IncludeUpdates: true}, hooks, w, &fakeWinget{}).run(context.Background(), context.Background())
		out := rec.output()
		if !strings.Contains(out, "[Restart required] A restart is required to finish installing updates.") {
			t.Errorf("requested=%v: no restart line in\n%s", requested, out)
		}
		if strings.Contains(out, "PatchMon does not restart the host") == requested {
			t.Errorf("requested=%v: wrong restart line in\n%s", requested, out)
		}
	}
}
