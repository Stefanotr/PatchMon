package config

import (
	"path/filepath"
	"testing"
)

func TestIsWithin(t *testing.T) {
	base := filepath.Join(string(filepath.Separator)+"data", "PatchMon")
	for path, want := range map[string]bool{
		base:                                 true,
		filepath.Join(base, "logs"):          true,
		filepath.Join(base, "logs", "deep"):  true,
		filepath.Join(base, ".."):            false,
		filepath.Join(base, "..", "Other"):   false,
		base + "Evil":                        false,
		filepath.Join(base+"..x", "logs"):    false,
		string(filepath.Separator) + "users": false,
	} {
		if got := IsWithin(path, base); got != want {
			t.Errorf("IsWithin(%q, %q) = %v, want %v", path, base, got, want)
		}
	}
}

// Credentials and logs next to config.yml are as trusted as the file that
// names them: no ACL check is asked for them.
func TestSaveNextToTheConfigNeedsNoPrivateCheck(t *testing.T) {
	m := newTestManager(t)
	if err := m.SaveConfig(); err != nil {
		t.Fatal(err)
	}
}
