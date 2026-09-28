package effects

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/joega/a-weather-app/internal/elfsafe"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const pluginRelative = "native/frame-alignment/a-weather-app-frame-alignment.so"
const hostRelative = "native/atmosphere/a-weather-app-atmosphere"

func childEnvironment() []string {
	// The effects owner is I/O/compositor-bound; parallel Go Ps add scheduler
	// overhead here without accelerating the serialized native protocol.
	result := []string{"PATH=/usr/bin:/bin", "GOMAXPROCS=1", "MESA_SHADER_CACHE_DISABLE=true"}
	for _, key := range []string{"HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "LANG", "LC_ALL", "DBUS_SESSION_BUS_ADDRESS", "GSK_RENDERER"} {
		if v, ok := os.LookupEnv(key); ok {
			result = append(result, key+"="+v)
		}
	}
	return result
}
func metadata(pid int) (string, error) {
	raw, e := readRegular(fmt.Sprintf("/proc/%d/stat", pid), 4096)
	if e != nil {
		return "", e
	}
	i := strings.LastIndex(string(raw), ")")
	if i < 0 {
		return "", errors.New("invalid process metadata")
	}
	fields := strings.Fields(string(raw[i+1:]))
	if len(fields) < 20 || fields[0] == "Z" {
		return "", errors.New("process not live")
	}
	return fields[19], nil
}

type nativeBackend struct {
	root, instance, output, starttime, display string
	pid                                        int
	inventoryValue                             []any
	env                                        []string
}

func newBackend(root string) *nativeBackend {
	return &nativeBackend{root: root, env: childEnvironment()}
}
func (b *nativeBackend) identity() error {
	if b.pid <= 0 || !instancePattern.MatchString(b.instance) {
		return errors.New("compositor identity missing")
	}
	start, e := metadata(b.pid)
	if e != nil || start != b.starttime {
		return errors.New("compositor process changed")
	}
	exe, e := filepath.EvalSymlinks(fmt.Sprintf("/proc/%d/exe", b.pid))
	expected, x := filepath.EvalSymlinks("/usr/bin/Hyprland")
	if e != nil || x != nil || exe != expected {
		return errors.New("compositor executable changed")
	}
	return nil
}
func (b *nativeBackend) ctl(ctx context.Context, jsonOutput bool, args ...string) (any, error) {
	if e := b.identity(); e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var raw []byte
	var e error
	if request, readOnly := compositorReadRequest(args); jsonOutput && readOnly {
		base := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr", b.instance)
		raw, e = compositorQuery(ctx, base, b.pid, request, b.identity)
	} else {
		command := []string{"/usr/bin/hyprctl", "-i", b.instance}
		if jsonOutput {
			command = append(command, "-j")
		}
		command = append(command, args...)
		raw, e = supervision.Run(ctx, command, b.env, compositorReplyLimit)
	}
	if e != nil {
		return nil, e
	}
	if !jsonOutput {
		return strings.TrimSpace(string(raw)), nil
	}
	v, e := safeio.Decode(raw, 2*1024*1024)
	if e == nil && obj(v)["error"] != nil {
		return nil, errors.New("compositor returned error")
	}
	return v, e
}
func (b *nativeBackend) inventory(ctx context.Context) ([]any, error) {
	v, e := b.ctl(ctx, true, "plugin", "list")
	if e != nil {
		return nil, e
	}
	a, ok := v.([]any)
	if !ok || len(a) > 64 {
		return nil, errors.New("invalid plugin inventory")
	}
	return a, nil
}
func (b *nativeBackend) native(ctx context.Context, command string) (object, error) {
	// Status is strictly observational. Once this session captured the loaded
	// plugin inventory, use the generation-bearing native reply as the ownership
	// check instead of launching a second `plugin list` process every heartbeat.
	// Every command that can change state still verifies the complete inventory
	// immediately before dispatch, and teardown additionally rechecks generation.
	if command == "status" && b.inventoryValue != nil {
		v, e := b.ctl(ctx, true, "a-weather-app:rain", command)
		m, ok := v.(map[string]any)
		if e == nil && !ok {
			e = errors.New("invalid native reply")
		}
		return m, e
	}
	inventory, e := b.inventory(ctx)
	if e != nil {
		return nil, e
	}
	if b.inventoryValue == nil {
		if command == "status" && len(inventory) == 0 {
			return object{"enabled": false}, nil
		}
		return nil, errors.New("plugin ownership unknown")
	}
	if !reflect.DeepEqual(inventory, b.inventoryValue) {
		return nil, errors.New("plugin ownership changed")
	}
	v, e := b.ctl(ctx, true, "a-weather-app:rain", command)
	m, ok := v.(map[string]any)
	if e == nil && !ok {
		e = errors.New("invalid native reply")
	}
	return m, e
}
func socketAt(fd int, name string) error {
	info, e := os.Lstat(fmt.Sprintf("/proc/self/fd/%d/%s", fd, name))
	if e != nil {
		return e
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode()&os.ModeSocket == 0 || st.Uid != uint32(os.Getuid()) {
		return errors.New("invalid compositor socket")
	}
	return nil
}
func discover(explicit string) (string, int, string, error) {
	path, display := os.Getenv("XDG_RUNTIME_DIR"), os.Getenv("WAYLAND_DISPLAY")
	if !filepath.IsAbs(path) {
		return "", 0, "", errors.New("session_unavailable")
	}
	display = strings.TrimPrefix(display, path+"/")
	if !regexpWayland(display) {
		return "", 0, "", errors.New("wayland_required")
	}
	if explicit != "" && !instancePattern.MatchString(explicit) {
		return "", 0, "", errors.New("session_unavailable")
	}
	fd, e := openDirectory(path)
	if e != nil {
		return "", 0, "", e
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	syscall.Fstat(fd, &st)
	if st.Uid != uint32(os.Getuid()) || st.Mode&0777 != 0700 {
		return "", 0, "", errors.New("session_unavailable")
	}
	if e = socketAt(fd, display); e != nil {
		return "", 0, "", e
	}
	h, e := syscall.Openat(fd, "hypr", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return "", 0, "", e
	}
	dir := os.NewFile(uintptr(h), "hypr")
	names, e := dir.Readdirnames(65)
	dir.Close()
	if e != nil && len(names) == 0 {
		return "", 0, "", e
	}
	if len(names) > 64 {
		return "", 0, "", errors.New("session inventory bound")
	}
	found := ""
	pid := 0
	for _, name := range names {
		if !instancePattern.MatchString(name) || (explicit != "" && name != explicit) {
			continue
		}
		base := filepath.Join(path, "hypr", name)
		raw, e := readRegular(filepath.Join(base, "hyprland.lock"), 4096)
		if e != nil {
			continue
		}
		fields := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		if len(fields) != 2 || fields[1] != display {
			continue
		}
		candidate, e := strconv.Atoi(fields[0])
		if e != nil || candidate < 1 || candidate > 2147483647 {
			continue
		}
		socketFD, e := openDirectory(base)
		if e != nil {
			continue
		}
		e = socketAt(socketFD, ".socket.sock")
		syscall.Close(socketFD)
		if e != nil {
			continue
		}
		stamp, e := metadata(candidate)
		if e != nil {
			continue
		}
		b := newBackend("")
		b.instance = name
		b.pid = candidate
		b.starttime = stamp
		if b.identity() != nil {
			continue
		}
		if found != "" {
			return "", 0, "", errors.New("session_unavailable")
		}
		found = name
		pid = candidate
	}
	if found == "" {
		return "", 0, "", errors.New("session_unavailable")
	}
	return found, pid, display, nil
}
func regexpWayland(s string) bool {
	if !strings.HasPrefix(s, "wayland-") {
		return false
	}
	n := strings.TrimPrefix(s, "wayland-")
	if len(n) == 0 || len(n) > 10 {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func (b *nativeBackend) preflight(ctx context.Context, instance, output string) (int, error) {
	name, pid, display, e := discover(instance)
	if e != nil {
		return 0, e
	}
	b.instance = name
	b.pid = pid
	b.display = display
	b.output = output
	b.starttime, e = metadata(pid)
	if e != nil {
		return 0, e
	}
	inventory, e := b.inventory(ctx)
	if e != nil || len(inventory) != 0 {
		return 0, errors.New("plugin inventory must be empty")
	}
	v, e := b.ctl(ctx, true, "monitors", "all")
	if e != nil {
		return 0, e
	}
	monitor, e := selectedOutput(v, output)
	if e != nil {
		return 0, e
	}
	id, ok := integer(monitor["id"])
	if !ok || id > 2147483647 {
		return 0, errors.New("selected output id invalid")
	}
	if e = validateOutput(monitor); e != nil {
		return 0, e
	}
	if e = b.verifyArtifacts(ctx); e != nil {
		return 0, e
	}
	b.env = append(b.env, "WAYLAND_DISPLAY="+display, "HYPRLAND_INSTANCE_SIGNATURE="+name)
	return int(id), nil
}
func (b *nativeBackend) load(ctx context.Context) error {
	reply, loadError := b.ctl(ctx, false, "plugin", "load", filepath.Join(b.root, pluginRelative))
	if loadError == nil && strings.ToLower(text(reply)) != "ok" {
		loadError = errors.New("plugin load rejected")
	}
	inventory, e := b.inventory(ctx)
	if e != nil {
		return e
	}
	if len(inventory) != 1 || obj(inventory[0])["name"] != "a-weather-app-frame-alignment" {
		return errors.New("unexpected plugin inventory after load")
	}
	b.inventoryValue = inventory
	return loadError
}
func (b *nativeBackend) unload(ctx context.Context) error {
	inventory, e := b.inventory(ctx)
	if e != nil {
		return e
	}
	if len(inventory) == 0 {
		b.inventoryValue = nil
		return nil
	}
	if b.inventoryValue == nil || !reflect.DeepEqual(inventory, b.inventoryValue) {
		return errors.New("plugin ownership ambiguous; refusing unload")
	}
	reply, e := b.ctl(ctx, false, "plugin", "unload", filepath.Join(b.root, pluginRelative))
	if e != nil {
		return e
	}
	inventory, e = b.inventory(ctx)
	if e != nil || strings.ToLower(text(reply)) != "ok" || len(inventory) != 0 {
		return errors.New("owned plugin unload verification failed")
	}
	b.inventoryValue = nil
	return nil
}

var requiredSymbols = []string{"g_pHyprRenderer", "_ZN10NProtocols11sessionLockE", "_ZGV15g_pHyprRenderer", "_ZGVN10NProtocols11sessionLockE", "g_pEventLoopManager", "_ZGV19g_pEventLoopManager"}

func symbolCheck(plugin, host []byte) error {
	p, e := elfsafe.NewFile(plugin, 16*1024*1024)
	if e != nil {
		return e
	}
	defer p.Close()
	h, e := elfsafe.NewFile(host, 128*1024*1024)
	if e != nil {
		return e
	}
	defer h.Close()
	if p.Type != elf.ET_DYN || p.Class != h.Class || p.Machine != h.Machine {
		return errors.New("incompatible native ELF")
	}
	symbolic, _ := p.DynValue(elf.DT_SYMBOLIC)
	flags, _ := p.DynValue(elf.DT_FLAGS)
	if len(symbolic) > 0 {
		return errors.New("plugin binds compositor globals locally")
	}
	for _, v := range flags {
		if v&2 != 0 {
			return errors.New("plugin binds compositor globals locally")
		}
	}
	pm, e := elfsafe.Symbols(p, requiredSymbols)
	if e != nil {
		return e
	}
	hm, e := elfsafe.Symbols(h, requiredSymbols)
	if e != nil {
		return e
	}
	for _, name := range requiredSymbols {
		candidate, cok := pm[name]
		exported, eok := hm[name]
		binding := elf.ST_BIND(candidate.Info)
		exportBinding := elf.ST_BIND(exported.Info)
		if !cok || !eok || elf.ST_TYPE(candidate.Info) != elf.STT_OBJECT || elf.ST_VISIBILITY(candidate.Other) != elf.STV_DEFAULT || (binding != elf.STB_GLOBAL && binding != elf.STB_WEAK) || elf.ST_TYPE(exported.Info) != elf.STT_OBJECT || elf.ST_VISIBILITY(exported.Other) != elf.STV_DEFAULT || (exportBinding != elf.STB_GLOBAL && exportBinding != elf.STB_WEAK && exportBinding != 10) || exported.Section == elf.SHN_UNDEF || (candidate.Section != elf.SHN_UNDEF && candidate.Size != exported.Size) {
			return fmt.Errorf("incompatible compositor export %s", name)
		}
	}
	return nil
}
func (b *nativeBackend) verifyArtifacts(ctx context.Context) error {
	assets := map[string][]byte{}
	for _, name := range []string{pluginRelative, hostRelative} {
		raw, e := safeio.ReadFile(filepath.Join(b.root, name), 16*1024*1024)
		if e != nil || len(raw) == 0 {
			return errors.New("prebuilt native effects unavailable")
		}
		assets[name] = raw
	}
	info, e := os.Stat(filepath.Join(b.root, hostRelative))
	if e != nil || info.Mode()&0111 == 0 {
		return errors.New("native host not executable")
	}
	raw, e := safeio.ReadFile(filepath.Join(b.root, "packaging/runtime.json"), 128*1024)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e == nil {
		manifest, e := safeio.Object(raw, 128*1024)
		if e != nil {
			return e
		}
		version, e := b.ctl(ctx, true, "version")
		if e != nil {
			return e
		}
		schema, ok := integer(manifest["schema_version"])
		if !ok || schema != 1 || runtime.GOARCH != "amd64" || manifest["architecture"] != "x86_64" || !validHex(text(manifest["source_commit"]), 20) || !validHex(text(manifest["hyprland_commit"]), 20) || obj(version)["commit"] != manifest["hyprland_commit"] {
			return errors.New("packaged runtime incompatible")
		}
		allowed := map[string]bool{"a-weather-app": true, "native/qt/a-weather-app-qt": true, "ui/shaders/atmosphere.frag.qsb": true, pluginRelative: true, hostRelative: true}
		for name, record := range obj(manifest["artifacts"]) {
			r := obj(record)
			size, ok := integer(r["bytes"])
			if !allowed[name] || !ok || size <= 0 || size > 16*1024*1024 || !validHex(text(r["sha256"]), 32) {
				return errors.New("invalid runtime artifact record")
			}
		}
		for name, data := range assets {
			record := obj(obj(manifest["artifacts"])[name])
			sum := sha256.Sum256(data)
			if num(record["bytes"]) != float64(len(data)) || record["sha256"] != hex.EncodeToString(sum[:]) {
				return errors.New("packaged runtime changed")
			}
		}
	}
	host, e := filepath.EvalSymlinks("/usr/bin/Hyprland")
	if e != nil {
		return e
	}
	binary, e := safeio.ReadFile(host, 128*1024*1024)
	if e != nil {
		return e
	}
	return symbolCheck(assets[pluginRelative], binary)
}

func validHex(value string, size int) bool {
	raw, e := hex.DecodeString(value)
	return e == nil && len(raw) == size && hex.EncodeToString(raw) == value
}
