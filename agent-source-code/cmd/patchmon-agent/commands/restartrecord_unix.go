//go:build !windows

package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// The record sits in /etc/patchmon, next to the agent's configuration.
func defaultRestartRecordPath() string {
	return filepath.Join("/etc/patchmon", restartRecordFile)
}

// openRestartRecord opens the record for reading if it can be trusted: a
// regular file, not a link, owned by the agent's user (root) and readable by
// it alone, in a directory no other user can write to. A missing file is
// reported as fs.ErrNotExist.
func openRestartRecord(path string) (*os.File, error) {
	if err := checkPrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	// O_NOFOLLOW refuses a link; O_NONBLOCK keeps a FIFO from blocking.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil {
		err = checkPrivateFile(fi)
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// writeRestartRecordFile replaces the record: the content goes to a new
// temporary file, mode 0600, then is renamed over the old one, so a reader
// never sees a partial write and a file or link planted under that name is
// replaced, not written through.
func writeRestartRecordFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := checkPrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".restart-scheduled-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once renamed
	_, werr := tmp.Write(data)
	serr := tmp.Sync()
	cerr := tmp.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// removeRestartRecordFile deletes the record, in a directory no other user
// can write to; os.Remove deletes a link itself, never what it points to.
func removeRestartRecordFile(path string) error {
	if err := checkPrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	return os.Remove(path)
}

func checkPrivateFile(fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mode %04o lets other users read or write it", fi.Mode().Perm())
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return errors.New("not owned by the agent's user")
	}
	return nil
}

func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s can be written by other users", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is not owned by the agent's user", dir)
	}
	return nil
}
