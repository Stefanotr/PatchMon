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

// State files sit in C:\ProgramData\PatchMon, where the ProgramData ACL lets
// any local user create files. What they hold steers the agent, which runs as
// LocalSystem, so a state file is trusted only when it is a regular file (not
// a symlink or other reparse point) owned by LocalSystem or Administrators,
// and it is created with a DACL granting access to those two alone. A user
// can delete or plant a file at worst; neither is read as the agent's.

// stateFileSDDL is a protected DACL: full access for LocalSystem and
// Administrators, nothing inherited from the directory.
const stateFileSDDL = "D:P(A;;FA;;;SY)(A;;FA;;;BA)"

func programDataDir() string {
	if base := os.Getenv("ProgramData"); base != "" {
		return base
	}
	return `C:\ProgramData`
}

// stateFilePath returns the path of a state file in the agent's data folder.
func stateFilePath(name string) string {
	return filepath.Join(programDataDir(), "PatchMon", name)
}

// openTrustedStateFile opens a state file for reading if it can be trusted.
// A missing file is reported as fs.ErrNotExist.
func openTrustedStateFile(path string) (*os.File, error) {
	h, err := openTrusted(path, false)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}

// openTrusted opens path for reading without following it when it is a
// reparse point, and checks what it opened: a regular file (dir false) or a
// directory (dir true), not a reparse point (symbolic link, junction, ...),
// owned by LocalSystem or Administrators. The checks are made on the open
// handle, so they hold for the object the caller then reads through it. A
// missing path is reported as fs.ErrNotExist.
func openTrusted(path string, dir bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	// FILE_FLAG_BACKUP_SEMANTICS is what lets CreateFile open a directory.
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return windows.InvalidHandle, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	if err := checkTrustedObject(h, dir); err != nil {
		_ = windows.CloseHandle(h)
		return windows.InvalidHandle, fmt.Errorf("%s: %w", path, err)
	}
	return h, nil
}

func checkTrustedObject(h windows.Handle, dir bool) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return err
	}
	isDir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	switch {
	case info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0:
		return errors.New("it is a reparse point (symbolic link, junction or similar)")
	case dir && !isDir:
		return errors.New("it is not a directory")
	case !dir && isDir:
		return errors.New("it is not a regular file")
	}
	return checkTrustedOwner(h)
}

func checkTrustedOwner(h windows.Handle) error {
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner.IsWellKnown(windows.WinLocalSystemSid) || owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return nil
	}
	return fmt.Errorf("it is owned by %s, not by SYSTEM or Administrators", owner.String())
}

// writeTrustedStateFile replaces a state file. The new content goes to a
// temporary file created with the restricted DACL, then is renamed over the
// old one, so a reader never sees a partial write and a file planted under
// either name is replaced rather than written through.
func writeTrustedStateFile(path string, data []byte) error {
	tmp := path + ".tmp"
	// os.Remove deletes a link itself, never what it points to.
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString(stateFileSDDL)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	sa := &windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	f := os.NewFile(uintptr(h), tmp)
	_, werr := f.Write(data)
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
