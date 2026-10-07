package updater

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
)

func sameBundle(a, b string) error {
	if e := release.Verify(a); e != nil {
		return e
	}
	if e := release.Verify(b); e != nil {
		return e
	}
	x, e := safeio.ReadFile(filepath.Join(a, "packaging/runtime.json"), release.MaxManifest)
	if e != nil {
		return e
	}
	y, e := safeio.ReadFile(filepath.Join(b, "packaging/runtime.json"), release.MaxManifest)
	if e != nil {
		return e
	}
	if !bytes.Equal(x, y) {
		return errors.New("existing managed release differs from the running bundle")
	}
	return nil
}

// importRuntime preserves an extracted download and places a verified copy in
// the managed release tree. Only that private copy participates in rollback.
func (l *LinuxInstallation) importRuntime() (root, selection, running string, err error) {
	if l.installLock == nil {
		return "", "", "", errors.New("installation lock is not held")
	}
	_, e := os.Lstat(filepath.Join(l.DataRoot, "current"))
	if e == nil {
		root, selection, err = l.current()
		if err != nil {
			return root, selection, running, err
		}
		if root == l.RuntimeRoot {
			return root, selection, running, err
		}
		if strings.TrimPrefix(filepath.Base(selection), "v") != l.Config.Installed {
			err = errors.New("reopen the installed version before updating")
			return root, selection, running, err
		}
		if err = sameBundle(root, l.RuntimeRoot); err != nil {
			return root, selection, running, err
		}
		running = l.RuntimeRoot
		return root, selection, running, err
	}
	if !errors.Is(e, os.ErrNotExist) {
		err = e
		return root, selection, running, err
	}
	if err = ownedDirectory(l.RuntimeRoot); err != nil {
		return root, selection, running, err
	}
	if err = release.Verify(l.RuntimeRoot); err != nil {
		return root, selection, running, err
	}
	if _, err = Compare(l.Config.Installed, l.Config.Installed); err != nil {
		return root, selection, running, err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	raw, e := safeio.ReadFile(filepath.Join(l.RuntimeRoot, "manifest.json"), 8192)
	if e != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Version != l.Config.Installed {
		err = errors.New("running runtime version does not match its manifest")
		return root, selection, running, err
	}
	selection = "releases/v" + l.Config.Installed
	root = filepath.Join(l.DataRoot, selection)
	if err = os.MkdirAll(filepath.Join(l.DataRoot, "releases"), 0700); err != nil {
		return root, selection, running, err
	}
	if err = ownedDirectory(filepath.Join(l.DataRoot, "releases")); err != nil {
		return root, selection, running, err
	}
	if _, e := os.Lstat(root); e == nil {
		if err = sameBundle(root, l.RuntimeRoot); err != nil {
			return root, selection, running, err
		}
	} else if errors.Is(e, os.ErrNotExist) {
		stage, e := os.MkdirTemp(l.DataRoot, ".import-")
		if e != nil {
			err = e
			return root, selection, running, err
		}
		defer os.RemoveAll(stage)
		bundle := filepath.Join(stage, "runtime")
		if err = release.CopyVerified(l.RuntimeRoot, bundle); err != nil {
			return root, selection, running, err
		}
		if err = os.Rename(bundle, root); err != nil {
			return root, selection, running, err
		}
	} else {
		err = e
		return root, selection, running, err
	}
	releases, e := safeio.OpenDir(filepath.Join(l.DataRoot, "releases"), false)
	if e != nil {
		err = e
		return root, selection, running, err
	}
	err = releasesSync(releases)
	if err != nil {
		return root, selection, running, err
	}
	if err = l.switchRuntime(selection); err != nil {
		return root, selection, running, err
	}
	running = l.RuntimeRoot
	return root, selection, running, err
}

func releasesSync(d *safeio.Directory) error {
	defer d.Close()
	return syscall.Fsync(d.FD)
}
