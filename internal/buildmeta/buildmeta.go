// Package buildmeta creates and audits reproducible Go/Qt package metadata.
// It is a development tool; the installed application uses internal/release.
package buildmeta

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
)

const Image = "archlinux@sha256:917e543c9d0f1f495d70907bdf05bf53607e791b351e1b01ccd3aec2442303ed"
const Snapshot = "2026/09/26"
const manifestPath = "packaging/runtime.json"

var artifacts = []string{"a-weather-app", "native/qt/a-weather-app-qt", "ui/shaders/atmosphere.frag.qsb", "native/frame-alignment/a-weather-app-frame-alignment.so", "native/atmosphere/a-weather-app-atmosphere"}
var omit = words(".git .agents .codex .idea .vscode .build .test-build .frontend-test-build .service-test-build build dist artifacts node_modules")
var suffixes = words(".go .mod .sum .pro .qrc .qml .js .c .cpp .h .hpp .gdshader .frag .sh .md .txt .json .toml .yml .yaml .svg .desktop .png")
var sourceNames = words("Makefile qmldir a-weather-app LICENSE .gitignore")
var pythonSuffixes = words(".py .pyc .pyo .pyi .pyw")

func words(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

type M = map[string]any
type Metadata struct {
	SourceCommit, HyprlandCommit string
	SourceDateEpoch              int64
	SourceStatus, Packages       []string
}
type Record struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func regular(name string, maximum int64) ([]byte, error) {
	return regularOwned(name, maximum, uint32(os.Geteuid()))
}

// The explicitly supplied source tree can be a read-only host bind mount owned
// by a different UID. Only source reads inherit its anchored root owner; runtime
// reads retain the installed-package ownership policy.
func inputOwnerAllowed(uid, sourceOwner, currentOwner uint32) bool {
	return uid == 0 || uid == currentOwner || uid == sourceOwner
}

func regularOwned(name string, maximum int64, sourceOwner uint32) ([]byte, error) {
	fd, e := syscall.Open(name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var before, after syscall.Stat_t
	if e = syscall.Fstat(fd, &before); e != nil {
		return nil, e
	}
	if before.Mode&syscall.S_IFMT != syscall.S_IFREG || before.Nlink != 1 || before.Size < 0 || before.Size > maximum || before.Mode&0022 != 0 || !inputOwnerAllowed(before.Uid, sourceOwner, uint32(os.Geteuid())) {
		return nil, errors.New("unsafe or oversized build input")
	}
	raw, e := io.ReadAll(io.LimitReader(f, maximum+1))
	if e != nil {
		return nil, e
	}
	if e = syscall.Fstat(fd, &after); e != nil || before.Ino != after.Ino || before.Dev != after.Dev || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim || len(raw) > int(maximum) {
		return nil, errors.New("build input changed while reading")
	}
	return raw, nil
}
func digest(name string, maximum int64) (Record, error) {
	return digestOwned(name, maximum, uint32(os.Geteuid()))
}
func digestOwned(name string, maximum int64, sourceOwner uint32) (Record, error) {
	raw, e := regularOwned(name, maximum, sourceOwner)
	if e != nil {
		return Record{}, e
	}
	h := sha256.Sum256(raw)
	return Record{int64(len(raw)), hex.EncodeToString(h[:])}, nil
}
func SourceFiles(root string) ([]string, error) {
	if e := canonicalRoot(root); e != nil {
		return nil, e
	}
	result := []string{}
	artifactSet := map[string]bool{}
	for _, name := range artifacts {
		artifactSet[name] = true
	}
	// The source checkout's root command is a small development launcher, not
	// the assembled Go executable. Its bytes belong in source provenance too.
	delete(artifactSet, "a-weather-app")
	e := filepath.WalkDir(root, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if name == root {
			return nil
		}
		relative, e := filepath.Rel(root, name)
		if e != nil {
			return e
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() && omit[entry.Name()] {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("source symlink refused")
		}
		if entry.Name() == "__pycache__" || pythonSuffixes[strings.ToLower(filepath.Ext(name))] {
			return errors.New("source tree contains Python files")
		}
		if entry.IsDir() {
			return nil
		}
		if artifactSet[relative] || relative == manifestPath {
			return nil
		}
		if suffixes[filepath.Ext(name)] || sourceNames[entry.Name()] {
			result = append(result, relative)
		}
		return nil
	})
	sort.Strings(result)
	return result, e
}
func runtimeFiles(root string) ([]string, error) {
	if e := canonicalRoot(root); e != nil {
		return nil, e
	}
	result := []string{}
	set := map[string]bool{}
	for _, s := range artifacts {
		set[s] = true
	}
	e := filepath.WalkDir(root, func(name string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if name == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("runtime symlink refused")
		}
		if entry.IsDir() {
			return nil
		}
		rel, e := filepath.Rel(root, name)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if !set[rel] && rel != manifestPath {
			result = append(result, rel)
		}
		return nil
	})
	sort.Strings(result)
	return result, e
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, errors.New("metadata command output exceeds bound")
	}
	return b.Buffer.Write(p)
}
func command(ctx context.Context, root, executable string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if executable == "/usr/bin/git" {
		args = append([]string{"-c", "safe.directory=" + root}, args...)
	}
	c := exec.CommandContext(ctx, executable, args...)
	c.Dir = root
	output := &boundedBuffer{limit: 1024 * 1024}
	c.Stdout = output
	c.Stderr = io.Discard
	if e := c.Run(); e != nil {
		return "", fmt.Errorf("metadata command failed: %s", filepath.Base(executable))
	}
	return strings.TrimSpace(output.String()), nil
}
func lines(s string) []string {
	if s == "" {
		return []string{}
	}
	return strings.Split(s, "\n")
}
func Gather(ctx context.Context, source string) (Metadata, error) {
	header, e := regular("/usr/include/hyprland/src/version.h", 1024*1024)
	if e != nil {
		return Metadata{}, e
	}
	match := regexp.MustCompile(`#define GIT_COMMIT_HASH\s+"([0-9a-f]{40})"`).FindSubmatch(header)
	if len(match) != 2 {
		return Metadata{}, errors.New("cannot identify compiled Hyprland ABI")
	}
	commit, e := command(ctx, source, "/usr/bin/git", "rev-parse", "HEAD")
	if e != nil {
		return Metadata{}, e
	}
	epoch, e := command(ctx, source, "/usr/bin/git", "show", "-s", "--format=%ct", "HEAD")
	if e != nil {
		return Metadata{}, e
	}
	seconds, e := strconv.ParseInt(epoch, 10, 64)
	if e != nil {
		return Metadata{}, e
	}
	status, e := command(ctx, source, "/usr/bin/git", "status", "--porcelain", "--untracked-files=all")
	if e != nil {
		return Metadata{}, e
	}
	packages, e := command(ctx, source, "/usr/bin/pacman", "-Q")
	if e != nil {
		return Metadata{}, e
	}
	return Metadata{commit, string(match[1]), seconds, lines(status), lines(packages)}, nil
}
func Create(ctx context.Context, root, source string) error {
	if e := requireContainerSource(source); e != nil {
		return e
	}
	metadata, e := Gather(ctx, source)
	if e != nil {
		return e
	}
	return CreateWithMetadata(root, source, metadata)
}

// readOnlySourceMount checks mount options, not the filesystem's superblock
// options: a read-only bind can correctly report a writable backing filesystem.
func readOnlySourceMount(raw []byte) bool {
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		separator := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || len(fields) != separator+4 {
			continue
		}
		mount := fields[4]
		if mount != "/source" && !strings.HasPrefix(mount, "/source/") {
			continue
		}
		readOnly, readWrite := false, false
		for _, option := range strings.Split(fields[5], ",") {
			readOnly = readOnly || option == "ro"
			readWrite = readWrite || option == "rw"
		}
		if !readOnly || readWrite {
			return false
		}
		if mount == "/source" {
			if found {
				return false
			} // Refuse ambiguous stacked mounts.
			found = true
		}
	}
	return found
}

func requireContainerSource(source string) error {
	refusal := errors.New("manifest creation requires Docker with canonical read-only /source; build via scripts/run_go_migration_build.sh")
	if source != "/source" || canonicalRoot(source) != nil {
		return refusal
	}
	marker, e := os.Lstat("/.dockerenv")
	if e != nil || !marker.Mode().IsRegular() {
		return refusal
	}
	f, e := os.Open("/proc/self/mountinfo")
	if e != nil {
		return refusal
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if e != nil || len(raw) > 1024*1024 || !readOnlySourceMount(raw) {
		return refusal
	}
	return nil
}
func CreateWithMetadata(root, source string, metadata Metadata) error {
	if e := canonicalRoot(root); e != nil {
		return e
	}
	names, e := SourceFiles(source)
	if e != nil {
		return e
	}
	owner, e := sourceOwner(source)
	if e != nil {
		return e
	}
	sources := M{}
	for _, name := range names {
		r, e := digestOwned(filepath.Join(source, filepath.FromSlash(name)), release.MaxArtifact, owner)
		if e != nil {
			return e
		}
		sources[name] = r.SHA256
	}
	artifactRecords := M{}
	for _, name := range artifacts {
		r, e := digest(filepath.Join(root, filepath.FromSlash(name)), release.MaxArtifact)
		if e != nil {
			return e
		}
		if r.Bytes == 0 {
			return errors.New("empty runtime artifact")
		}
		artifactRecords[name] = r
	}
	names, e = runtimeFiles(root)
	if e != nil {
		return e
	}
	runtimeRecords := M{}
	for _, name := range names {
		r, e := digest(filepath.Join(root, filepath.FromSlash(name)), release.MaxArtifact)
		if e != nil {
			return e
		}
		runtimeRecords[name] = r
	}
	status := metadata.SourceStatus
	if status == nil {
		status = []string{}
	}
	packages := metadata.Packages
	if packages == nil {
		packages = []string{}
	}
	value := M{"schema_version": 1, "runtime": "go-qt", "architecture": "x86_64", "source_commit": metadata.SourceCommit, "source_date_epoch": metadata.SourceDateEpoch, "source_dirty": len(status) > 0, "source_status": status, "build_image": Image, "arch_snapshot": Snapshot, "hyprland_commit": metadata.HyprlandCommit, "packages": packages, "artifacts": artifactRecords, "runtime_files": runtimeRecords, "sources": sources}
	raw, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	raw = append(raw, '\n')
	if len(raw) > release.MaxManifest {
		return errors.New("runtime manifest exceeds bound")
	}
	destination := filepath.Join(root, manifestPath)
	if e = os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
		return e
	}
	if _, e := os.Lstat(destination); e == nil {
		if _, e = regular(destination, release.MaxManifest); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(destination), ".runtime-*.json")
	if e != nil {
		return e
	}
	defer f.Close()
	defer os.Remove(f.Name())
	if e = f.Chmod(0644); e != nil {
		return e
	}
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), destination); e != nil {
		return e
	}
	return Verify(root, source)
}

func canonicalRoot(root string) error {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return errors.New("absolute normalized build root required")
	}
	canonical, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	if canonical != root {
		return errors.New("build root symlink refused")
	}
	info, e := os.Lstat(root)
	if e != nil || !info.IsDir() {
		return errors.New("build root must be a directory")
	}
	return nil
}

func sourceOwner(root string) (uint32, error) {
	if e := canonicalRoot(root); e != nil {
		return 0, e
	}
	var st syscall.Stat_t
	if e := syscall.Lstat(root, &st); e != nil {
		return 0, e
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFDIR || st.Mode&0022 != 0 {
		return 0, errors.New("unsafe source root")
	}
	return st.Uid, nil
}
func Verify(root, source string) error {
	if e := release.Verify(root); e != nil {
		return e
	}
	if source == "" {
		return nil
	}
	raw, e := regular(filepath.Join(root, manifestPath), release.MaxManifest)
	if e != nil {
		return e
	}
	value, e := safeio.Object(raw, release.MaxManifest)
	if e != nil {
		return e
	}
	expected, _ := value["sources"].(map[string]any)
	names, e := SourceFiles(source)
	if e != nil {
		return e
	}
	owner, e := sourceOwner(source)
	if e != nil {
		return e
	}
	actual := M{}
	for _, name := range names {
		r, e := digestOwned(filepath.Join(source, filepath.FromSlash(name)), release.MaxArtifact, owner)
		if e != nil {
			return e
		}
		actual[name] = r.SHA256
	}
	if !reflect.DeepEqual(expected, actual) {
		return errors.New("source inventory or checksum changed")
	}
	return nil
}
