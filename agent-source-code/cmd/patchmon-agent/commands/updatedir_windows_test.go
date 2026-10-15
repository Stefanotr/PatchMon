//go:build windows

package commands

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func wellKnownSID(t *testing.T, k windows.WELL_KNOWN_SID_TYPE) *windows.SID {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(k)
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func currentUserSID(t *testing.T) *windows.SID {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid
}

func setOwner(path string, sid *windows.SID) error {
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, sid, nil, nil, nil)
}

// trustedParent returns a folder owned by Administrators, standing in for
// C:\ProgramData\PatchMon. Only an elevated token can assign that owner (CI
// runs elevated), so the test is skipped otherwise.
func trustedParent(t *testing.T) string {
	t.Helper()
	parent := filepath.Join(t.TempDir(), "PatchMon")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := setOwner(parent, wellKnownSID(t, windows.WinBuiltinAdministratorsSid)); err != nil {
		t.Skipf("needs an elevated prompt to hand a folder to Administrators: %v", err)
	}
	return parent
}

// untrustedOwner hands path to the test account, which is neither SYSTEM nor
// Administrators unless the test runs as SYSTEM.
func untrustedOwner(t *testing.T, path string) {
	t.Helper()
	if runningAsSystem() {
		t.Skip("the test account is SYSTEM, which owns trusted folders")
	}
	if err := setOwner(path, currentUserSID(t)); err != nil {
		t.Fatal(err)
	}
}

var sddlACESID = regexp.MustCompile(`\(([^)]*)\)`)

// assertRestricted checks the update directory: a plain directory owned by
// SYSTEM or Administrators, with a protected DACL naming only those two.
func assertRestricted(t *testing.T, dir string) {
	t.Helper()
	fi, err := os.Lstat(dir)
	if err != nil || fi.Mode().Type() != fs.ModeDir {
		t.Fatalf("%s is not a plain directory: %v, %v", dir, fi, err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	if !owner.IsWellKnown(windows.WinLocalSystemSid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		t.Errorf("owner = %s, want SYSTEM or Administrators", owner)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the DACL inherits from the parent: %s", sd)
	}
	aces := sddlACESID.FindAllStringSubmatch(sd.String(), -1)
	if len(aces) == 0 {
		t.Errorf("no ACE in %s", sd)
	}
	for _, m := range aces {
		fields := strings.Split(m[1], ";")
		if sid := fields[len(fields)-1]; sid != "SY" && sid != "BA" {
			t.Errorf("ACE (%s) grants %s, want only SYSTEM and Administrators", m[1], sid)
		}
	}
}

func TestPrepareUpdateDirCreatesAndKeepsATrustedDir(t *testing.T) {
	parent := trustedParent(t)

	dir, err := prepareUpdateDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(parent, "update") {
		t.Fatalf("dir = %q", dir)
	}
	assertRestricted(t, dir)

	// A trusted directory is reused as it is.
	staged := filepath.Join(dir, "staged.txt")
	if err := os.WriteFile(staged, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareUpdateDir(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); err != nil {
		t.Errorf("a trusted update directory was emptied: %v", err)
	}
	assertRestricted(t, dir)
}

func TestPrepareUpdateDirReplacesADirWithAnotherOwner(t *testing.T) {
	parent := trustedParent(t)
	dir := filepath.Join(parent, "update")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(dir, "planted.txt")
	if err := os.WriteFile(planted, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	untrustedOwner(t, dir)

	if _, err := prepareUpdateDir(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(planted); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the directory was kept: %v", err)
	}
	assertRestricted(t, dir)
}

func TestPrepareUpdateDirReplacesALinkWithoutFollowingIt(t *testing.T) {
	parent := trustedParent(t)
	target := t.TempDir()
	kept := filepath.Join(target, "kept.txt")
	if err := os.WriteFile(kept, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(parent, "update")
	if err := os.Symlink(target, dir); err != nil {
		t.Skipf("cannot create a directory symlink here: %v", err)
	}

	if _, err := prepareUpdateDir(parent); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Errorf("the link target was touched: %v", err)
	}
	assertRestricted(t, dir)
}

func TestPrepareUpdateDirRefusesAParentWithAnotherOwner(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "PatchMon")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	untrustedOwner(t, parent)

	_, err := prepareUpdateDir(parent)
	if err == nil || !strings.Contains(err.Error(), "not by SYSTEM or Administrators") {
		t.Fatalf("err = %v, want the update refused over the parent's owner", err)
	}
	if _, err := os.Lstat(filepath.Join(parent, "update")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("update directory created under an untrusted parent: %v", err)
	}
}

func TestOpenTrustedChecksWhatItOpened(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "state.json")
	if err := os.WriteFile(file, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := openTrusted(file, true); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file opened as a directory: %v", err)
	}
	if _, err := openTrusted(base, false); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("directory opened as a file: %v", err)
	}
	if _, err := openTrusted(filepath.Join(base, "missing"), false); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: %v, want fs.ErrNotExist", err)
	}
	link := filepath.Join(base, "link.json")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
	if _, err := openTrusted(link, false); err == nil || !strings.Contains(err.Error(), "reparse point") {
		t.Errorf("symlink trusted: %v", err)
	}
}
