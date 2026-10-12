//go:build windows

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func sddlOf(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("read the DACL of %s: %v", path, err)
	}
	return sd.String()
}

// grantsUsers reports an ACE for Users, Authenticated Users, Everyone or
// interactive users in an SDDL string.
func grantsUsers(sddl string) bool {
	for _, sid := range []string{";;;BU)", ";;;AU)", ";;;WD)", ";;;IU)"} {
		if strings.Contains(sddl, sid) {
			return true
		}
	}
	return false
}

// A data folder created with ProgramData's inherited read access for Users
// loses it, and so do the files in it.
func TestSecureDataDirDropsUsersAccess(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "PatchMon")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	permissive, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;0x1200a9;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := permissive.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	creds := filepath.Join(dir, "credentials.yml")
	if err := os.WriteFile(creds, []byte("api_key: secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !grantsUsers(sddlOf(t, creds)) {
		t.Skip("the test folder does not hand Users an inherited ACE on this machine")
	}
	h, err := openTrusted(dir, true)
	if err != nil {
		t.Skipf("the test folder is not owned by SYSTEM or Administrators here: %v", err)
	}
	_ = windows.CloseHandle(h)

	secureDataDirAt(dir)

	for _, p := range []string{dir, creds} {
		if s := sddlOf(t, p); grantsUsers(s) {
			t.Errorf("%s still grants users access: %s", p, s)
		}
	}
	if _, err := os.ReadFile(creds); err != nil {
		t.Errorf("the agent can no longer read its credentials: %v", err)
	}
}

// A missing folder is not an error.
func TestSecureDataDirMissingFolder(t *testing.T) {
	secureDataDirAt(filepath.Join(t.TempDir(), "absent"))
}
