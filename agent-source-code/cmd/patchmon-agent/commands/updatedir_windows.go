//go:build windows

package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// secureUpdateDir returns a directory that only SYSTEM and Administrators can
// write to, for staging the replacement binary and the update script.
//
// The service runs as LocalSystem, so os.TempDir() resolves to C:\Windows\TEMP.
// Staging there and then executing from it gives anyone who can write to that
// directory a window in which to swap the payload, which lands as SYSTEM. Go
// also maps only the read-only attribute on Windows, so a 0600 file mode is not
// a control here: the DACL has to be set explicitly.
//
// The owner of a directory can change its DACL at any time, so the DACL only
// restricts the directory when SYSTEM or Administrators own it, and its parent
// too. An update directory that is not a plain directory owned by them is
// removed and created again; a parent that is not owned by them is not
// repaired, and the update is refused.
func secureUpdateDir() (string, error) {
	return prepareUpdateDir(filepath.Join(programDataDir(), "PatchMon"))
}

// prepareUpdateDir returns parent\update, made as secureUpdateDir describes.
func prepareUpdateDir(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", parent, err)
	}
	h, err := openTrusted(parent, true)
	if err != nil {
		return "", fmt.Errorf("refusing to stage the update: %w", err)
	}
	_ = windows.CloseHandle(h)

	dir := filepath.Join(parent, "update")
	h, err = openTrusted(dir, true)
	switch {
	case err == nil:
		_ = windows.CloseHandle(h)
		if err := restrictToSystemAndAdmins(dir); err != nil {
			return "", fmt.Errorf("secure update directory: %w", err)
		}
		return dir, nil
	case !errors.Is(err, fs.ErrNotExist):
		logger.WithError(err).Warn("Update directory cannot be trusted; creating it again")
		// RemoveAll deletes a reparse point itself, not what it points to,
		// and does not follow the reparse points it meets inside the tree.
		if err := os.RemoveAll(dir); err != nil {
			return "", fmt.Errorf("remove the update directory that cannot be trusted: %w", err)
		}
	}
	if err := createRestrictedDir(dir); err != nil {
		return "", fmt.Errorf("create update directory: %w", err)
	}
	return dir, nil
}

// createRestrictedDir creates path with the DACL of restrictToSystemAndAdmins
// and a trusted owner, both set by CreateDirectory itself so the directory
// never exists with other permissions. The owner is SYSTEM when the agent runs
// as the service; an administrator running update-agent by hand cannot assign
// SYSTEM as owner, so the directory then belongs to Administrators.
func createRestrictedDir(path string) error {
	ownerSID := windows.WELL_KNOWN_SID_TYPE(windows.WinBuiltinAdministratorsSid)
	if runningAsSystem() {
		ownerSID = windows.WinLocalSystemSid
	}
	owner, err := windows.CreateWellKnownSid(ownerSID)
	if err != nil {
		return err
	}
	acl, err := systemAndAdminsACL()
	if err != nil {
		return err
	}
	abs, err := windows.NewSecurityDescriptor()
	if err != nil {
		return err
	}
	if err := abs.SetOwner(owner, false); err != nil {
		return err
	}
	if err := abs.SetDACL(acl, true, false); err != nil {
		return err
	}
	if err := abs.SetControl(windows.SE_DACL_PROTECTED, windows.SE_DACL_PROTECTED); err != nil {
		return err
	}
	sd, err := abs.ToSelfRelative()
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return windows.CreateDirectory(name, sa)
}

// runningAsSystem reports whether the agent runs as LocalSystem, as the
// service does.
func runningAsSystem() bool {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	return err == nil && u.User.Sid.IsWellKnown(windows.WinLocalSystemSid)
}

// restrictToSystemAndAdmins replaces the DACL with one granting full control to
// LocalSystem and Administrators only, and detaches it from inheritance so a
// permissive parent cannot widen it again.
func restrictToSystemAndAdmins(path string) error {
	acl, err := systemAndAdminsACL()
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
}

// systemAndAdminsACL grants full control to LocalSystem and Administrators,
// inherited by everything created below.
func systemAndAdminsACL() (*windows.ACL, error) {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return nil, err
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}

	entries := make([]windows.EXPLICIT_ACCESS, 0, 2)
	for _, sid := range []*windows.SID{system, admins} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_GROUP,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}

	return windows.ACLFromEntries(entries, nil)
}
