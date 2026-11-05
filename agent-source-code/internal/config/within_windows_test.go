//go:build windows

package config

import (
	"path/filepath"
	"testing"
)

// Credentials outside the directory of config.yml go only to a folder
// restricted to SYSTEM and Administrators: a user's temporary folder is
// refused.
func TestCredentialsOutsideTheConfigDirMustBePrivate(t *testing.T) {
	m := newTestManager(t)
	m.GetConfig().CredentialsFile = filepath.Join(t.TempDir(), "credentials.yml")
	if err := m.SaveCredentials("patchmon_test", "key"); err == nil {
		t.Fatal("credentials were written to a folder other local users may write")
	}
}
