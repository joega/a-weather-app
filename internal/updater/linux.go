package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
)

// LinuxInstallation updates only our managed release directory and, when
// present, the unmodified Omarchy checkout for this one plugin.
type LinuxInstallation struct {
	Config          Config
	RuntimeRoot     string
	DataRoot        string
	PluginRoot      string
	Socket          string
	Source          GitHubSource
	command         func(context.Context, string, ...string) (string, error)
	installLock     *os.File
	recoveryWarning string
}

func (l *LinuxInstallation) RecoveryWarning() string { return l.recoveryWarning }

func DefaultDataRoot() string {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local/share")
	}
	return filepath.Join(base, "a-weather-app")
}
func DefaultPluginRoot() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "omarchy/plugins/a-weather-app.weather")
}

func (l *LinuxInstallation) run(ctx context.Context, name string, args ...string) (string, error) {
	if l.command != nil {
		return l.command(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -oBatchMode=yes")
	// Commands have bounded output; errors shown in the app never contain their
	// raw output (which may contain local paths or remote diagnostics).
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed", filepath.Base(name))
	}
	return strings.TrimSpace(output.String()), nil
}

type boundedOutput struct{ strings.Builder }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 16384 - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Builder.Write(p)
	}
	return n, nil
}
func (l *LinuxInstallation) git(ctx context.Context, args ...string) (string, error) {
	return l.run(ctx, "git", append([]string{"-C", l.PluginRoot}, args...)...)
}

func ownedDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errors.New("invalid installation path")
	}
	rootInfo, e := os.Stat("/")
	if e != nil {
		return e
	}
	system, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot identify system directory ownership")
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || (st.Uid != uint32(os.Geteuid()) && st.Uid != system.Uid) || info.Mode().Perm()&0022 != 0 && !(st.Uid == system.Uid && info.Mode()&os.ModeSticky != 0) {
			return errors.New("unsafe installation directory")
		}
		if current == path && st.Uid != uint32(os.Geteuid()) {
			return errors.New("installation is not owned by this user")
		}
	}
	return nil
}

func (l *LinuxInstallation) current() (string, string, error) {
	if e := ownedDirectory(l.DataRoot); e != nil {
		return "", "", e
	}
	selection, e := os.Readlink(filepath.Join(l.DataRoot, "current"))
	if e != nil {
		return "", "", errors.New("this installation does not use the managed release launcher")
	}
	if !regexp.MustCompile(`^releases/v0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$`).MatchString(selection) {
		return "", "", errors.New("unrecognized runtime selection")
	}
	root := filepath.Join(l.DataRoot, selection)
	if e := ownedDirectory(root); e != nil {
		return "", "", e
	}
	if e := release.Verify(root); e != nil {
		return "", "", e
	}
	return root, selection, nil
}

// Bootstrap selects the installed runtime independently of the newer helper's
// executable. The helper is staged and verified before the old app is stopped.
func (l *LinuxInstallation) Bootstrap() (Config, error) {
	if l.Config.Development {
		return l.Config, errors.New("development checkouts are built locally")
	}
	root, selection, e := l.current()
	if e != nil {
		return l.Config, e
	}
	version := strings.TrimPrefix(filepath.Base(selection), "v")
	manifest, e := safeio.ReadFile(filepath.Join(root, "manifest.json"), 8192)
	var value struct {
		Version string `json:"version"`
	}
	if e != nil || json.Unmarshal(manifest, &value) != nil || value.Version != version {
		return l.Config, errors.New("installed runtime version does not match its selector")
	}
	l.RuntimeRoot = root
	l.Config.Installed = version
	return l.Config, nil
}

func (l *LinuxInstallation) pluginPreflight(ctx context.Context) (string, error) {
	if l.PluginRoot == "" {
		return "", nil
	}
	info, e := os.Lstat(l.PluginRoot)
	if errors.Is(e, os.ErrNotExist) {
		return "", errors.New("the plugin checkout is missing; reinstall it before updating")
	}
	if e != nil {
		return "", e
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("development or linked plugins must be updated locally")
	}
	if e = ownedDirectory(l.PluginRoot); e != nil {
		return "", e
	}
	info, e = os.Lstat(filepath.Join(l.PluginRoot, ".git"))
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("unsupported plugin checkout")
	}
	remote, e := l.git(ctx, "remote", "get-url", "origin")
	if e != nil {
		return "", e
	}
	if remote != "https://github.com/joega/a-weather-app.git" && remote != "https://github.com/joega/a-weather-app" {
		return "", errors.New("the plugin's origin is not the official repository")
	}
	branch, e := l.git(ctx, "branch", "--show-current")
	if e != nil || branch != "main" {
		return "", errors.New("the plugin checkout is on a custom branch; update it locally")
	}
	status, e := l.git(ctx, "status", "--porcelain", "--untracked-files=normal")
	if e != nil || status != "" {
		return "", errors.New("the plugin has local changes; no files were changed")
	}
	revision, e := l.git(ctx, "rev-parse", "HEAD")
	if e != nil || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(revision) {
		return "", errors.New("cannot identify the installed plugin revision")
	}
	return revision, nil
}

func (l *LinuxInstallation) Prepare(ctx context.Context, pin Pin, progress func(string)) (Transaction, error) {
	if l.Config.Development {
		return Transaction{}, errors.New("development builds must be updated locally")
	}
	oldCommit, e := l.pluginPreflight(ctx)
	if e != nil {
		return Transaction{}, e
	}
	old, current, running, e := l.importRuntime()
	if e != nil {
		return Transaction{}, e
	}
	t := Transaction{OldRuntime: old, RunningRoot: running, OldVersion: strings.TrimPrefix(filepath.Base(old), "v"), OldCurrent: current, PluginRoot: l.PluginRoot, OldCommit: oldCommit, Pin: pin}
	if l.PluginRoot != "" {
		if _, e = l.git(ctx, "fetch", "--quiet", "origin", "refs/heads/main"); e != nil {
			return t, e
		}
		t.NewCommit, e = l.git(ctx, "rev-parse", "FETCH_HEAD")
		if e != nil {
			return t, e
		}
		if _, e = l.git(ctx, "merge-base", "--is-ancestor", oldCommit, t.NewCommit); e != nil {
			return t, errors.New("plugin update cannot fast-forward; local history was preserved")
		}
		lock, e := l.git(ctx, "show", t.NewCommit+":packaging/release-lock.json")
		if e != nil {
			return t, e
		}
		var latestPin Pin
		if json.Unmarshal([]byte(lock), &latestPin) != nil || latestPin != pin {
			return t, errors.New("release pin changed during preparation; check for updates again")
		}
	}
	if l.installLock == nil {
		return t, errors.New("installation lock is not held")
	}
	if e = os.MkdirAll(filepath.Join(l.DataRoot, "releases"), 0700); e != nil {
		return t, e
	}
	if e = ownedDirectory(filepath.Join(l.DataRoot, "releases")); e != nil {
		return t, e
	}
	bundle, e := l.Source.Prepare(ctx, pin, l.DataRoot, progress)
	if e != nil {
		return t, e
	}
	defer os.RemoveAll(filepath.Dir(bundle))
	t.NewRuntime = filepath.Join(l.DataRoot, "releases", pin.Tag)
	if _, e = os.Lstat(t.NewRuntime); e == nil {
		if e = release.Verify(t.NewRuntime); e != nil {
			return t, errors.New("existing release directory failed verification")
		}
		metadata, e := safeio.ReadFile(filepath.Join(t.NewRuntime, "packaging/runtime.json"), release.MaxManifest)
		if e != nil {
			return t, e
		}
		prepared, e := safeio.ReadFile(filepath.Join(bundle, "packaging/runtime.json"), release.MaxManifest)
		if e != nil || string(metadata) != string(prepared) {
			return t, errors.New("existing release differs from verified download")
		}
	} else if errors.Is(e, os.ErrNotExist) {
		if e = os.Rename(bundle, t.NewRuntime); e != nil {
			return t, e
		}
	} else {
		return t, e
	}
	return t, l.Validate(t)
}

func (l *LinuxInstallation) Validate(t Transaction) error {
	if e := t.Pin.Validate(); e != nil {
		return e
	}
	if t.PluginRoot != l.PluginRoot || t.NewRuntime != filepath.Join(l.DataRoot, "releases", t.Pin.Tag) || t.OldRuntime != filepath.Join(l.DataRoot, t.OldCurrent) || !regexp.MustCompile(`^releases/v0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$`).MatchString(t.OldCurrent) {
		return errors.New("transaction does not belong to this installation")
	}
	if t.PluginRoot != "" && (!regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(t.OldCommit) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(t.NewCommit)) {
		return errors.New("invalid plugin revision in transaction")
	}
	if t.OldVersion != strings.TrimPrefix(filepath.Base(t.OldRuntime), "v") {
		return errors.New("invalid previous runtime version")
	}
	if t.RunningRoot != "" && (!filepath.IsAbs(t.RunningRoot) || filepath.Clean(t.RunningRoot) != t.RunningRoot || t.RunningRoot == "/" || t.RunningRoot == l.DataRoot) {
		return errors.New("invalid original runtime path")
	}
	for _, root := range []string{t.OldRuntime} {
		if e := ownedDirectory(root); e != nil {
			return e
		}
		if e := release.Verify(root); e != nil {
			return e
		}
	}
	return nil
}

func (l *LinuxInstallation) Stop(ctx context.Context, t Transaction) error {
	if e := l.Validate(t); e != nil {
		return e
	}
	if _, e := os.Lstat(l.Socket); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return e
	}
	reply, e := ipc.CallVerified(ctx, l.Socket, map[string]any{"op": "quit"}, func(pid int) error {
		exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if e != nil {
			return e
		}
		if exe != filepath.Join(t.OldRuntime, "a-weather-app") && exe != filepath.Join(t.NewRuntime, "a-weather-app") && (t.RunningRoot == "" || exe != filepath.Join(t.RunningRoot, "a-weather-app")) {
			return errors.New("unexpected weather service owns the socket")
		}
		return nil
	})
	if e != nil {
		return e
	}
	if reply["ok"] != true {
		return errors.New("weather effects did not stop cleanly")
	}
	deadline := time.NewTimer(90 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, e := os.Lstat(l.Socket); errors.Is(e, os.ErrNotExist) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("weather service did not exit")
		case <-ticker.C:
		}
	}
}

func (l *LinuxInstallation) switchRuntime(selection string) error {
	if l.installLock == nil {
		return errors.New("installation lock is not held")
	}
	d, e := safeio.OpenDir(l.DataRoot, false)
	if e != nil {
		return e
	}
	defer d.Close()
	// The final selector is always a relative link within the owned release tree.
	tmp, e := os.MkdirTemp(l.DataRoot, ".update-link-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	if e = os.Symlink(selection, filepath.Join(tmp, "current")); e != nil {
		return e
	}
	if e = os.Rename(filepath.Join(tmp, "current"), filepath.Join(l.DataRoot, "current")); e != nil {
		return e
	}
	return syscall.Fsync(d.FD)
}

func (l *LinuxInstallation) Activate(ctx context.Context, t Transaction) error {
	if e := l.Validate(t); e != nil {
		return e
	}
	if e := ownedDirectory(t.NewRuntime); e != nil {
		return e
	}
	if e := release.Verify(t.NewRuntime); e != nil {
		return e
	}
	_, selection, e := l.current()
	if e != nil {
		return e
	}
	if selection != t.OldCurrent {
		return errors.New("runtime selection changed while preparing the update")
	}
	if t.PluginRoot != "" {
		revision, e := l.pluginPreflight(ctx)
		if e != nil {
			return e
		}
		if revision != t.OldCommit {
			return errors.New("plugin changed while preparing the update")
		}
		if _, e = l.git(ctx, "merge", "--ff-only", t.NewCommit); e != nil {
			return e
		}
		if _, e = l.run(ctx, "omarchy-plugin-validate", l.PluginRoot); e != nil {
			return errors.New("updated plugin failed Omarchy validation")
		}
	}
	if e := l.switchRuntime("releases/" + t.Pin.Tag); e != nil {
		return e
	}
	return l.migrateLauncher(t)
}

func (l *LinuxInstallation) Restore(ctx context.Context, t Transaction) error {
	if e := l.Validate(t); e != nil {
		return e
	}
	selection, e := os.Readlink(filepath.Join(l.DataRoot, "current"))
	if e != nil || selection != t.OldCurrent && selection != "releases/"+t.Pin.Tag {
		return errors.New("the runtime selector changed outside this update; reopen your selected version")
	}
	if e := l.Stop(ctx, t); e != nil {
		return e
	}
	if t.PluginRoot != "" {
		revision, e := l.pluginPreflight(ctx)
		if e != nil || revision != t.OldCommit && revision != t.NewCommit {
			l.recoveryWarning = "Plugin changes were preserved. Review them before trying another update."
		} else if revision == t.NewCommit {
			// Reset only our exact fast-forward, after proving the checkout is clean.
			if _, e = l.git(ctx, "reset", "--hard", t.OldCommit); e != nil {
				l.recoveryWarning = "The runtime was restored, but the plugin could not be restored. Review the plugin before trying another update."
			}
		}
	}
	return l.switchRuntime(t.OldCurrent)
}

func (l *LinuxInstallation) Start(ctx context.Context, t Transaction, rollback bool) error {
	if e := l.Validate(t); e != nil {
		return e
	}
	if t.PluginRoot != "" {
		if _, e := l.run(ctx, "omarchy", "restart", "shell"); e != nil {
			if !rollback {
				return errors.New("could not refresh the Omarchy bar")
			}
			l.recoveryWarning += " The bar could not refresh. Reopen it after reviewing the plugin."
		}
	}
	root := t.NewRuntime
	if rollback {
		root = t.OldRuntime
	}
	if e := release.Verify(root); e != nil {
		return e
	}
	return StartDetached(filepath.Join(root, "a-weather-app"), []string{"--state-dir", l.Config.StatePath}, os.Environ())
}

func StartDetached(executable string, args, environment []string) error {
	cmd := exec.Command(executable, args...)
	cmd.Env = environment
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	null, e := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if e = cmd.Start(); e != nil {
		return e
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func (l *LinuxInstallation) Ready(ctx context.Context, t Transaction, rollback bool) error {
	root := t.NewRuntime
	version := t.Pin.Tag[1:]
	if rollback {
		root = t.OldRuntime
		version = strings.TrimPrefix(filepath.Base(root), "v")
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	stableSince := time.Time{}
	for {
		callCtx, cancel := context.WithTimeout(ctx, time.Second)
		reply, e := ipc.CallVerified(callCtx, l.Socket, map[string]any{"op": "snapshot"}, func(pid int) error {
			exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if e != nil {
				return e
			}
			if exe != filepath.Join(root, "a-weather-app") {
				return errors.New("wrong runtime answered after restart")
			}
			return nil
		})
		cancel()
		if e == nil && reply["ok"] == true {
			snapshot, _ := reply["snapshot"].(map[string]any)
			update, _ := snapshot["update"].(map[string]any)
			if rollback || update["installed"] == version && reply["frontend_ready"] == true {
				if stableSince.IsZero() {
					stableSince = time.Now()
				}
				if time.Since(stableSince) >= time.Second {
					return nil
				}
			} else {
				stableSince = time.Time{}
			}
		} else {
			stableSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("the new weather app did not start successfully")
		case <-ticker.C:
		}
	}
}

func (l *LinuxInstallation) LockInstallation() (func(), error) {
	if l.Config.Development {
		return nil, errors.New("development checkouts are built locally")
	}
	d, e := safeio.OpenDir(l.DataRoot, true)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	f, e := d.Lock(".install.lock")
	if e != nil {
		return nil, errors.New("another runtime installer is running")
	}
	l.installLock = f
	return func() { f.Close(); l.installLock = nil }, nil
}
