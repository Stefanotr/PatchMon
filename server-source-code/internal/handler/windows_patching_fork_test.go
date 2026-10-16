package handler

import (
	"strings"
	"testing"
)

func TestWindowsAgentVersionErrorRefusesTheForkFormerRunner(t *testing.T) {
	cases := []struct {
		version string
		want    string // "" when the run is allowed
	}{
		{"2.1.4-fork.14", forkFormerWindowsRunnerMessage},
		{"v2.1.4-fork.1", forkFormerWindowsRunnerMessage},
		{"2.1.4", ""},
		{"2.1.4-rc.2", ""},
		{"2.1.5-fork.1", ""},
		{"2.1.3", "Update the PatchMon agent on this host to v2.1.4 or later to patch Windows"},
		{"", "Update the PatchMon agent on this host to v2.1.4 or later to patch Windows"},
	}
	for _, c := range cases {
		v := c.version
		got := windowsAgentVersionError(&v)
		switch {
		case c.want == "" && got != nil:
			t.Errorf("%q refused: %v", v, got.Body["error"])
		case c.want != "" && (got == nil || !strings.Contains(got.Body["error"].(string), c.want)):
			t.Errorf("%q: got %v, want %q", v, got, c.want)
		}
	}
}
