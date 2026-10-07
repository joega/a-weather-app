package app

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func publicDirectory(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return -1, errors.New("data path")
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return -1, e
	}
	var rootInfo syscall.Stat_t
	if e = syscall.Fstat(fd, &rootInfo); e != nil {
		syscall.Close(fd)
		return -1, e
	}
	for _, p := range strings.Split(path[1:], "/") {
		if e = syscall.Mkdirat(fd, p, 0755); e != nil && e != syscall.EEXIST {
			syscall.Close(fd)
			return -1, e
		}
		next, e := syscall.Openat(fd, p, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
		var st syscall.Stat_t
		if e = syscall.Fstat(fd, &st); e != nil {
			syscall.Close(fd)
			return -1, e
		}
		if !publicAncestorSafe(st, rootInfo.Uid, uint32(os.Geteuid())) {
			syscall.Close(fd)
			return -1, errors.New("unsafe data directory")
		}
	}
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Geteuid()) {
		syscall.Close(fd)
		return -1, errors.New("data ownership")
	}
	return fd, nil
}
func publicAncestorSafe(st syscall.Stat_t, systemUID, currentUID uint32) bool {
	return (st.Uid == systemUID || st.Uid == currentUID) && (st.Mode&0022 == 0 || (st.Uid == systemUID && st.Mode&syscall.S_ISVTX != 0))
}
func publishFile(dir int, name string, raw []byte, alternatives ...[]byte) error {
	path := fmt.Sprintf("/proc/self/fd/%d/%s", dir, name)
	oldFD, e := syscall.Openat(dir, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e == nil {
		f := os.NewFile(uintptr(oldFD), name)
		defer f.Close()
		var before, after syscall.Stat_t
		if e = syscall.Fstat(oldFD, &before); e != nil {
			return e
		}
		if before.Mode&syscall.S_IFMT != syscall.S_IFREG || before.Uid != uint32(os.Geteuid()) || before.Nlink != 1 || before.Mode&0022 != 0 || before.Size < 0 || before.Size > 65536 {
			return os.ErrExist
		}
		old, e := io.ReadAll(io.LimitReader(f, 65537))
		if e != nil {
			return e
		}
		if e = syscall.Fstat(oldFD, &after); e != nil {
			return e
		}
		if before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Nlink != after.Nlink || before.Uid != after.Uid || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || len(old) > 65536 {
			return os.ErrExist
		}
		for _, accepted := range append([][]byte{raw}, alternatives...) {
			if bytes.Equal(old, accepted) {
				return nil
			}
		}
		return os.ErrExist
	}
	if !os.IsNotExist(e) {
		return e
	}
	entropy := make([]byte, 16)
	if _, e = rand.Read(entropy); e != nil {
		return e
	}
	tmp := ".a-weather-app-" + hex.EncodeToString(entropy)
	fd, e := syscall.Openat(dir, tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), tmp)
	defer f.Close()
	defer syscall.Unlinkat(dir, tmp)
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Chmod(0644); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = os.Link(fmt.Sprintf("/proc/self/fd/%d/%s", dir, tmp), path); e != nil {
		return e
	}
	if e = syscall.Unlinkat(dir, tmp); e != nil {
		return e
	}
	return syscall.Fsync(dir)
}
func InstallLauncher(root string) string {
	running, e := os.Executable()
	if e != nil {
		return "failed"
	}
	return installLauncher(root, running)
}
func installLauncher(root, running string) string {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "failed"
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, e := os.UserHomeDir()
		if e != nil {
			return "failed"
		}
		data = filepath.Join(home, ".local/share")
	}
	executable := filepath.Join(root, "a-weather-app")
	// Managed releases use a stable selector so future menu launches follow an
	// update or rollback instead of reopening the version that created the file.
	managed := filepath.Join(data, "a-weather-app", "current", "a-weather-app")
	if resolved, err := filepath.EvalSymlinks(managed); err == nil && resolved == executable {
		executable = managed
	}
	// Development launchers point directly to the compiled Go entry point.
	resolvedRunning, _ := filepath.EvalSymlinks(running)
	if resolvedRunning == filepath.Join(root, "build", "a-weather-app") {
		executable = resolvedRunning
	}
	for _, r := range data + executable {
		if r < 32 || r == 127 || strings.ContainsRune("\\\"`$%=", r) {
			return "unsupported_path"
		}
	}
	template, e := safeio.ReadFile(filepath.Join(root, "packaging/a-weather-app.desktop"), 8192)
	if e != nil {
		return "failed"
	}
	var alternatives [][]byte
	// Preserve a manually installed template only when PATH names this exact
	// running Go executable, never an unrelated or former script launcher.
	if current, e := exec.LookPath("a-weather-app"); e == nil && resolvedRunning == filepath.Join(root, "a-weather-app") {
		if resolved, e := filepath.EvalSymlinks(current); e == nil && resolved == resolvedRunning {
			alternatives = append(alternatives, template)
		}
	}
	icon, e := safeio.ReadFile(filepath.Join(root, "packaging/icons/a-weather-app.svg"), 65536)
	if e != nil {
		return "failed"
	}
	iconDir := filepath.Join(data, "icons/hicolor/scalable/apps")
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(template), "\n"), "\n") {
		if strings.HasPrefix(line, "TryExec=") {
			continue
		}
		if strings.HasPrefix(line, "Exec=") {
			line = "Exec=\"" + executable + "\""
		}
		if strings.HasPrefix(line, "Icon=") {
			line = "Icon=" + filepath.Join(iconDir, "a-weather-app.svg")
		}
		lines = append(lines, line)
	}
	icons, e := publicDirectory(iconDir)
	if e != nil {
		return "failed"
	}
	defer syscall.Close(icons)
	if e = publishFile(icons, "a-weather-app.svg", icon); e != nil {
		if errors.Is(e, os.ErrExist) {
			return "conflict"
		}
		return "failed"
	}
	apps, e := publicDirectory(filepath.Join(data, "applications"))
	if e != nil {
		return "failed"
	}
	defer syscall.Close(apps)
	if e = publishFile(apps, "a-weather-app.desktop", []byte(strings.Join(lines, "\n")+"\n"), alternatives...); e != nil {
		if errors.Is(e, os.ErrExist) {
			return "conflict"
		}
		return "failed"
	}
	return "installed"
}
