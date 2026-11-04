//go:build windows

package winsec

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrivateDirDescriptor(t *testing.T) {
	sd, err := windows.SecurityDescriptorFromString(privateDirSDDL)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateDescriptor(sd); err != nil {
		t.Fatalf("the agent's own descriptor fails its check: %v", err)
	}
	owner, _, _ := sd.Owner()
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		t.Errorf("owner = %s, want Administrators", owner)
	}
	for _, bad := range []string{
		"O:BAD:(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)",
		"O:BUD:P(A;OICI;FA;;;SY)",
		"O:BAD:P(A;OICI;FA;;;SY)(A;OICI;0x6;;;BU)",
		"O:BAD:P(A;OICI;FA;;;WD)",
	} {
		sd, err := windows.SecurityDescriptorFromString(bad)
		if err != nil {
			t.Fatal(err)
		}
		if checkPrivateDescriptor(sd) == nil {
			t.Errorf("%s passed the check", bad)
		}
	}
}

func TestCreatePrivateDirRefusesAnExistingDir(t *testing.T) {
	requireWindowsIntegration(t)
	dir := filepath.Join(t.TempDir(), "planted")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := CreatePrivateDir(dir); err == nil {
		t.Fatal("an existing directory was adopted")
	}
	if err := VerifySecureDir(dir); err == nil {
		t.Fatal("a directory with inherited, user-writable permissions passed verification")
	}
}

func TestSecureDirHealsAnExistingDir(t *testing.T) {
	requireWindowsIntegration(t)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := VerifySecureDir(dir); err == nil {
		t.Fatal("an inherited, user-writable directory passed verification before healing")
	}
	if err := secureDir(dir); err != nil {
		t.Fatalf("secureDir: %v (the test must run elevated)", err)
	}
	if err := VerifySecureDir(dir); err != nil {
		t.Errorf("directory not secure after healing: %v", err)
	}
}

func TestInDataDir(t *testing.T) {
	t.Setenv("ProgramData", `C:\ProgramData`)
	for path, want := range map[string]bool{
		`C:\ProgramData\PatchMon`:             true,
		`c:\programdata\patchmon\`:            true,
		`C:\ProgramData\PatchMon\logs`:        true,
		`C:\ProgramData\PatchMon\..\Other`:    false,
		`C:\ProgramData\PatchMonEvil`:         false,
		`C:\Users\Public`:                     false,
		`C:\ProgramData`:                      false,
		`C:\ProgramData\PatchMon.untrusted-1`: false,
	} {
		if got := InDataDir(path); got != want {
			t.Errorf("InDataDir(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestVerifyPrivateLocationRefusesAUserFolder(t *testing.T) {
	t.Setenv("ProgramData", filepath.Join(t.TempDir(), "pd"))
	dir := t.TempDir()
	if err := VerifyPrivateLocation(dir); err == nil {
		t.Fatalf("%s, a folder with the user's permissions, was accepted", dir)
	}
	if err := VerifyPrivateLocation(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a folder that does not exist was accepted")
	}
}

func TestVerifyPrivateLocationAcceptsPrivateFolders(t *testing.T) {
	requireWindowsIntegration(t)
	base := t.TempDir()
	t.Setenv("ProgramData", base)
	if err := EnsureDataDir(); err != nil {
		t.Fatal(err)
	}
	logs := filepath.Join(base, "PatchMon", "logs")
	if err := os.Mkdir(logs, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(base, "PatchMon"), logs} {
		if err := VerifyPrivateLocation(dir); err != nil {
			t.Errorf("%s was refused: %v", dir, err)
		}
	}
	elsewhere := filepath.Join(base, "elsewhere")
	if err := CreatePrivateDir(elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivateLocation(elsewhere); err != nil {
		t.Errorf("%s, created private, was refused: %v", elsewhere, err)
	}
}
