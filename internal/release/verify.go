// Package release verifies an installed Go/Qt bundle without invoking build
// tools or reading the development source inventory.
package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"github.com/joega/a-weather-app/internal/safeio"
)

const MaxManifest = 128 * 1024
const MaxArtifact = 16 * 1024 * 1024
const manifestPath = "packaging/runtime.json"

// ErrMissingManifest distinguishes an absent manifest from an invalid bundle.
// Only a caller that independently knows it is a development build may bypass
// this error; Verify itself never falls back to weaker checks.
var ErrMissingManifest = errors.New("Go/Qt runtime manifest missing")

var artifactNames = []string{
	"a-weather-app",
	"native/qt/a-weather-app-qt",
	"ui/shaders/atmosphere.frag.qsb",
	"native/frame-alignment/a-weather-app-frame-alignment.so",
	"native/atmosphere/a-weather-app-atmosphere",
}

// This inventory follows packaging/build-go-release.sh. Manifest keys cannot
// select arbitrary files outside the installed runtime contract.
var runtimeNames = []string{
	"LICENSE", "THIRD_PARTY_NOTICES.md", "manifest.json", "README.md",
	"packaging/a-weather-app.desktop", "packaging/icons/a-weather-app.svg", "packaging/go-README.md",
	"quickshell/a-weather-app.weather/WeatherWidget.qml", "licenses/hyprland-dependencies.txt", "licenses/go.txt",
}

type record struct {
	bytes  int64
	digest string
}
type manifest struct{ records map[string]record }
type tree struct {
	dirs      map[string]int
	systemUID uint32
}

func lowerHex(v any, n int) bool {
	s, ok := v.(string)
	if !ok || len(s) != n*2 {
		return false
	}
	b, e := hex.DecodeString(s)
	return e == nil && hex.EncodeToString(b) == s
}
func integer(v any, lo, hi float64) (int64, bool) {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < lo || n > hi {
		return 0, false
	}
	return int64(n), true
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func safeRelative(s string) bool {
	if s == "" || len(s) > 512 || path.IsAbs(s) || path.Clean(s) != s || strings.ContainsAny(s, "\\\x00") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	for _, c := range s {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
func nameSet(names []string) map[string]bool {
	r := map[string]bool{}
	for _, n := range names {
		r[n] = true
	}
	return r
}
func parseRecords(value any, names []string) (map[string]record, error) {
	rows := object(value)
	if len(rows) != len(names) {
		return nil, errors.New("runtime inventory mismatch")
	}
	allowed := nameSet(names)
	records := map[string]record{}
	for name, value := range rows {
		if !safeRelative(name) || !allowed[name] {
			return nil, errors.New("unsupported runtime file")
		}
		r := object(value)
		size, ok := integer(r["bytes"], 1, MaxArtifact)
		if len(r) != 2 || !ok || !lowerHex(r["sha256"], 32) {
			return nil, errors.New("invalid runtime file record")
		}
		records[name] = record{size, r["sha256"].(string)}
	}
	return records, nil
}

func parseManifest(raw []byte) (manifest, error) {
	v, e := safeio.Object(raw, MaxManifest)
	if e != nil {
		return manifest{}, e
	}
	fields := []string{"schema_version", "runtime", "architecture", "source_commit", "source_date_epoch", "source_dirty", "source_status", "build_image", "arch_snapshot", "hyprland_commit", "packages", "artifacts", "runtime_files", "sources"}
	if len(v) != len(fields) {
		return manifest{}, errors.New("invalid runtime manifest fields")
	}
	for _, k := range fields {
		if _, ok := v[k]; !ok {
			return manifest{}, errors.New("missing runtime manifest field")
		}
	}
	schema, ok := integer(v["schema_version"], 1, 1)
	if !ok || schema != 1 || v["runtime"] != "go-qt" || v["architecture"] != "x86_64" || runtime.GOARCH != "amd64" || !lowerHex(v["source_commit"], 20) || !lowerHex(v["hyprland_commit"], 20) {
		return manifest{}, errors.New("incompatible Go/Qt manifest")
	}
	if _, ok := integer(v["source_date_epoch"], 0, 253402300799); !ok {
		return manifest{}, errors.New("invalid source epoch")
	}
	dirty, ok := v["source_dirty"].(bool)
	if !ok {
		return manifest{}, errors.New("invalid source dirty flag")
	}
	status, e := textList(v["source_status"], 2048, 1024)
	if e != nil || (dirty != (len(status) > 0)) {
		return manifest{}, errors.New("invalid source status")
	}
	if _, e = textList(v["packages"], 4096, 256); e != nil {
		return manifest{}, e
	}
	image, ok := v["build_image"].(string)
	if !ok || !regexp.MustCompile(`^archlinux@sha256:[0-9a-f]{64}$`).MatchString(image) {
		return manifest{}, errors.New("invalid build image")
	}
	snapshot, ok := v["arch_snapshot"].(string)
	if !ok || !regexp.MustCompile(`^[0-9]{4}/[0-9]{2}/[0-9]{2}$`).MatchString(snapshot) {
		return manifest{}, errors.New("invalid build snapshot")
	}
	sources := object(v["sources"])
	if sources == nil || len(sources) > 2048 {
		return manifest{}, errors.New("invalid source inventory")
	}
	for name, hash := range sources {
		if !safeRelative(name) || !lowerHex(hash, 32) {
			return manifest{}, errors.New("invalid source record")
		}
	}
	artifacts, e := parseRecords(v["artifacts"], artifactNames)
	if e != nil {
		return manifest{}, e
	}
	assets, e := parseRecords(v["runtime_files"], runtimeNames)
	if e != nil {
		return manifest{}, e
	}
	for name, r := range assets {
		artifacts[name] = r
	}
	return manifest{artifacts}, nil
}
func textList(value any, maxItems, maxRunes int) ([]any, error) {
	items, ok := value.([]any)
	if !ok || len(items) > maxItems {
		return nil, errors.New("invalid manifest text list")
	}
	for _, v := range items {
		s, ok := v.(string)
		if !ok || s == "" || len([]rune(s)) > maxRunes {
			return nil, errors.New("invalid manifest text")
		}
		for _, r := range s {
			if r < 32 || r == 127 {
				return nil, errors.New("invalid manifest text")
			}
		}
	}
	return items, nil
}

func (t *tree) close() {
	for _, fd := range t.dirs {
		syscall.Close(fd)
	}
}
func (t *tree) checkDirectory(fd int, stickyParent bool) error {
	var st syscall.Stat_t
	if e := syscall.Fstat(fd, &st); e != nil {
		return e
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR || (st.Uid != uint32(os.Geteuid()) && st.Uid != t.systemUID) || st.Mode&06000 != 0 {
		return errors.New("unsafe runtime directory")
	}
	if st.Mode&0022 != 0 && !(stickyParent && st.Uid == t.systemUID && st.Mode&syscall.S_ISVTX != 0) {
		return errors.New("writable runtime directory")
	}
	return nil
}
func openRoot(root string) (*tree, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, errors.New("absolute normalized runtime root required")
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil {
		syscall.Close(fd)
		return nil, e
	}
	t := &tree{dirs: map[string]int{}, systemUID: st.Uid}
	parts := strings.Split(root[1:], "/")
	for i, part := range parts {
		child, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = child
		if e = t.checkDirectory(fd, i < len(parts)-1); e != nil {
			syscall.Close(fd)
			return nil, e
		}
	}
	t.dirs[""] = fd
	return t, nil
}
func (t *tree) directory(name string) (int, error) {
	if name == "." {
		name = ""
	}
	if fd, ok := t.dirs[name]; ok {
		return fd, nil
	}
	parent, e := t.directory(path.Dir(name))
	if e != nil {
		return -1, e
	}
	fd, e := syscall.Openat(parent, path.Base(name), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return -1, e
	}
	if e = t.checkDirectory(fd, false); e != nil {
		syscall.Close(fd)
		return -1, e
	}
	t.dirs[name] = fd
	return fd, nil
}
func (t *tree) openFile(name string) (*os.File, syscall.Stat_t, error) {
	var st syscall.Stat_t
	parent := path.Dir(name)
	if parent == "." {
		parent = ""
	}
	fd, e := t.directory(parent)
	if e != nil {
		return nil, st, e
	}
	fileFD, e := syscall.Openat(fd, path.Base(name), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, st, e
	}
	f := os.NewFile(uintptr(fileFD), name)
	if e = syscall.Fstat(fileFD, &st); e != nil {
		f.Close()
		return nil, st, e
	}
	if e = t.checkFile(st); e != nil {
		f.Close()
		return nil, st, e
	}
	return f, st, nil
}
func (t *tree) checkFile(st syscall.Stat_t) error {
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 || (st.Uid != uint32(os.Geteuid()) && st.Uid != t.systemUID) || st.Mode&07022 != 0 {
		return errors.New("unsafe runtime file")
	}
	return nil
}
func changed(a, b syscall.Stat_t) bool {
	return a.Dev != b.Dev || a.Ino != b.Ino || a.Size != b.Size || a.Mode != b.Mode || a.Uid != b.Uid || a.Nlink != b.Nlink || a.Mtim != b.Mtim || a.Ctim != b.Ctim
}

// inventory rejects extra files and non-allowlisted directories before opening
// any artifact, and pins the accepted directory descriptors for later reads.
func (t *tree) inventory(manifestState syscall.Stat_t) error {
	allowed := nameSet(append(append([]string{}, artifactNames...), runtimeNames...))
	allowed[manifestPath] = true
	directories := map[string]bool{"": true}
	for name := range allowed {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			directories[dir] = true
		}
	}
	found := map[string]bool{}
	var walk func(string) error
	walk = func(dir string) error {
		fd, e := t.directory(dir)
		if e != nil {
			return e
		}
		duplicate, e := syscall.Dup(fd)
		if e != nil {
			return e
		}
		file := os.NewFile(uintptr(duplicate), dir)
		names, e := file.Readdirnames(len(allowed) + len(directories) + 1)
		file.Close()
		if e != nil && e != io.EOF {
			return e
		}
		if len(names) > len(allowed)+len(directories) {
			return errors.New("runtime inventory bound")
		}
		sort.Strings(names)
		for _, entry := range names {
			relative := entry
			if dir != "" {
				relative = dir + "/" + entry
			}
			if directories[relative] {
				if e := walk(relative); e != nil {
					return e
				}
				continue
			}
			if !allowed[relative] {
				return errors.New("unexpected runtime entry")
			}
			f, st, e := t.openFile(relative)
			if e != nil {
				return e
			}
			f.Close()
			if relative == manifestPath && st.Size > MaxManifest {
				return errors.New("manifest exceeds bound")
			}
			if relative == manifestPath && changed(manifestState, st) {
				return errors.New("manifest replaced while verifying")
			}
			found[relative] = true
		}
		return nil
	}
	if e := walk(""); e != nil {
		return e
	}
	if len(found) != len(allowed) {
		return errors.New("runtime file inventory changed")
	}
	return nil
}

// Verify checks the installed manifest and every shipped runtime file. No file
// selected by the source inventory is read, and executable bytes are not run.
func Verify(root string) error {
	t, e := openRoot(root)
	if e != nil {
		return e
	}
	defer t.close()
	f, before, e := t.openFile(manifestPath)
	if e != nil {
		if errors.Is(e, syscall.ENOENT) {
			return ErrMissingManifest
		}
		return e
	}
	if before.Size <= 0 || before.Size > MaxManifest {
		f.Close()
		return errors.New("manifest exceeds bound")
	}
	raw, e := io.ReadAll(io.LimitReader(f, MaxManifest+1))
	var after syscall.Stat_t
	statErr := syscall.Fstat(int(f.Fd()), &after)
	f.Close()
	if e != nil {
		return e
	}
	if statErr != nil || changed(before, after) {
		return errors.New("manifest changed while reading")
	}
	m, e := parseManifest(raw)
	if e != nil {
		return e
	}
	if e = t.inventory(before); e != nil {
		return e
	}
	names := make([]string, 0, len(m.records))
	for name := range m.records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r := m.records[name]
		f, before, e := t.openFile(name)
		if e != nil {
			return e
		}
		if before.Size != r.bytes {
			f.Close()
			return fmt.Errorf("runtime file size changed: %s", name)
		}
		if name == "a-weather-app" || name == "native/qt/a-weather-app-qt" || name == "native/atmosphere/a-weather-app-atmosphere" {
			if before.Mode&0111 == 0 {
				f.Close()
				return errors.New("runtime executable is not executable")
			}
		}
		h := sha256.New()
		count, e := io.Copy(h, io.LimitReader(f, r.bytes+1))
		statErr = syscall.Fstat(int(f.Fd()), &after)
		f.Close()
		if e != nil {
			return e
		}
		if statErr != nil || changed(before, after) || count != r.bytes || hex.EncodeToString(h.Sum(nil)) != r.digest {
			return fmt.Errorf("runtime file checksum changed: %s", name)
		}
	}
	return nil
}
