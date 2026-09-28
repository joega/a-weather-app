package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/nativebuild"
	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
)

var instancePattern = regexp.MustCompile(`^[a-f0-9]+_[0-9]+_[0-9]+$`)
var outputPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
var displayPattern = regexp.MustCompile(`^wayland-[0-9]{1,10}$`)

func socketOwned(path string) error {
	info, e := os.Lstat(path)
	if e != nil {
		return e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode()&os.ModeSocket == 0 || st.Uid != uint32(os.Geteuid()) {
		return errors.New("socket is not owned by this user")
	}
	return nil
}

func (a *audit) preflight(ctx context.Context) error {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(runtime) {
		return errors.New("real XDG_RUNTIME_DIR required")
	}
	directory, e := safeio.OpenDir(runtime, false)
	if e != nil {
		return e
	}
	directory.Close()
	display := strings.TrimPrefix(os.Getenv("WAYLAND_DISPLAY"), runtime+"/")
	if !displayPattern.MatchString(display) {
		return errors.New("real WAYLAND_DISPLAY required")
	}
	if e = socketOwned(filepath.Join(runtime, display)); e != nil {
		return e
	}
	fd, e := syscall.Open(filepath.Join(runtime, "hypr"), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	hypr := os.NewFile(uintptr(fd), "hyprland instances")
	names, readErr := hypr.Readdirnames(65)
	hypr.Close()
	if len(names) > 64 || len(names) == 0 {
		return errors.Join(errors.New("bounded compositor discovery failed"), readErr)
	}
	for _, name := range names {
		if !instancePattern.MatchString(name) {
			continue
		}
		base := filepath.Join(runtime, "hypr", name)
		raw, e := safeio.ReadFile(filepath.Join(base, "hyprland.lock"), 4096)
		if e != nil {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(fields) != 2 || fields[1] != display {
			continue
		}
		pid, e := strconv.Atoi(fields[0])
		if e != nil || pid < 1 || pid > 2147483647 {
			continue
		}
		identity, e := inspectProcess(pid)
		if e != nil || identity.State == "Z" || socketOwned(filepath.Join(base, ".socket.sock")) != nil {
			continue
		}
		expected, e := filepath.EvalSymlinks("/usr/bin/Hyprland")
		if e != nil || identity.Executable != expected {
			continue
		}
		if a.instance != "" {
			return errors.New("multiple matching Wayland compositors; refusing activation")
		}
		a.instance = name
		a.compositor = identity
	}
	if a.instance == "" {
		return errors.New("no unambiguous matching Wayland compositor")
	}
	value, e := a.ctl(ctx, "monitors", "all")
	if e != nil {
		return e
	}
	outputs, ok := value.([]any)
	if !ok || len(outputs) != 1 {
		return errors.New("audit requires one configured output; it will not reconfigure the desktop")
	}
	output := objectOf(outputs[0])
	a.output = stringOf(output["name"])
	if !outputPattern.MatchString(a.output) || output["disabled"] != false {
		return errors.New("unique enabled output required")
	}
	value, e = a.ctl(ctx, "plugin", "list")
	if e != nil {
		return e
	}
	inventory, ok := value.([]any)
	if !ok || len(inventory) != 0 {
		return errors.New("existing compositor plugins present; audit refuses to load or unload anything")
	}
	a.baseline = inventory
	if e = a.record("baseline", object{"compositor": a.compositor, "instance": a.instance, "wayland_display": display, "output": output, "plugin_inventory": inventory}); e != nil {
		return e
	}
	// A manifest, if present, must verify. Only a development build/ executable
	// may use a checkout without that installed-package manifest.
	e = release.Verify(a.root)
	if e == nil {
		a.executable = filepath.Join(a.root, "a-weather-app")
	} else if errors.Is(e, release.ErrMissingManifest) {
		a.executable = filepath.Join(a.root, "build", "a-weather-app")
	} else {
		return e
	}
	artifacts := []string{a.executable, filepath.Join(a.root, "native/frame-alignment/a-weather-app-frame-alignment.so"), filepath.Join(a.root, "native/atmosphere/a-weather-app-atmosphere")}
	for _, path := range artifacts {
		raw, e := safeio.ReadFile(path, 128*1024*1024)
		if e != nil {
			return e
		}
		if e = nativebuild.Hardening(path); e != nil {
			return fmt.Errorf("artifact hardening %s: %w", path, e)
		}
		if e = a.record("artifact", object{"path": path, "bytes": len(raw), "sha256": fmt.Sprintf("%x", sha256.Sum256(raw)), "hardening": "passed"}); e != nil {
			return e
		}
	}
	if e = nativebuild.Symbols(artifacts[1], "/usr/bin/Hyprland"); e != nil {
		return e
	}
	return a.record("preflight", object{"ok": true, "executable": a.executable, "root": a.root, "symbols": "passed"})
}

func (a *audit) ctl(ctx context.Context, args ...string) (any, error) {
	if a.instance == "" {
		return nil, errors.New("compositor not selected")
	}
	current, e := inspectProcess(a.compositor.PID)
	if e != nil || current.StartTime != a.compositor.StartTime || current.Executable != a.compositor.Executable || current.State == "Z" {
		return nil, errors.New("compositor identity changed; refusing further native commands")
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	command := append([]string{"/usr/bin/hyprctl", "-i", a.instance, "-j"}, args...)
	raw, e := supervision.Run(ctx, command, environment(), 2*1024*1024)
	if e != nil {
		return nil, e
	}
	value, e := safeio.Decode(raw, 2*1024*1024)
	if e != nil {
		return nil, e
	}
	if objectOf(value)["error"] != nil {
		return nil, errors.New("compositor returned an error")
	}
	if e = a.record("compositor-query", object{"command": command, "response": value}); e != nil {
		return nil, e
	}
	return value, nil
}

func (a *audit) inventoryRestored(ctx context.Context) error {
	value, e := a.ctl(ctx, "plugin", "list")
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(value, a.baseline) {
		return errors.New("plugin inventory differs from empty baseline; no manual unload attempted")
	}
	return nil
}
