package updater

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// Only an owned default command symlink to the exact prior executable is
// migrated. Custom wrappers, commands, directories and other links are kept.
func (l *LinuxInstallation) migrateBinLink(t Transaction) error {
	if l.BinRoot == "" {
		return nil
	}
	if e := ownedDirectory(l.BinRoot); e != nil {
		return nil
	}
	file := filepath.Join(l.BinRoot, "a-weather-app")
	info, e := os.Lstat(file)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSymlink == 0 || st.Uid != uint32(os.Geteuid()) {
		return nil
	}
	oldLink, e := os.Readlink(file)
	if e != nil {
		return e
	}
	resolved, e := filepath.EvalSymlinks(file)
	if e != nil {
		return nil
	}
	if resolved != filepath.Join(t.OldRuntime, "a-weather-app") && (t.RunningRoot == "" || resolved != filepath.Join(t.RunningRoot, "a-weather-app")) {
		return nil
	}
	tmp, e := os.MkdirTemp(l.BinRoot, ".weather-command-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	next := filepath.Join(tmp, "command")
	if e = os.Symlink(filepath.Join(l.DataRoot, "current/a-weather-app"), next); e != nil {
		return e
	}
	current, e := os.Lstat(file)
	link, linkErr := os.Readlink(file)
	if e != nil || linkErr != nil || !os.SameFile(info, current) || link != oldLink {
		return nil
	}
	if e = os.Rename(next, file); e != nil {
		return e
	}
	d, e := os.Open(l.BinRoot)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
