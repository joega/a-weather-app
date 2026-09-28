package release

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

type M = map[string]any

func fixture(t *testing.T) (string, M) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	artifacts, assets := M{}, M{}
	for _, names := range [][]string{artifactNames, runtimeNames} {
		for _, name := range names {
			body := []byte("independent compiled fixture: " + name)
			p := filepath.Join(root, name)
			if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
				t.Fatal(e)
			}
			mode := os.FileMode(0644)
			if name == "a-weather-app" || name == "native/qt/a-weather-app-qt" || name == "native/atmosphere/a-weather-app-atmosphere" {
				mode = 0755
			}
			if e := os.WriteFile(p, body, mode); e != nil {
				t.Fatal(e)
			}
			sum := sha256.Sum256(body)
			r := M{"bytes": float64(len(body)), "sha256": hex.EncodeToString(sum[:])}
			if nameSet(artifactNames)[name] {
				artifacts[name] = r
			} else {
				assets[name] = r
			}
		}
	}
	v := M{"schema_version": 1.0, "runtime": "go-qt", "architecture": "x86_64", "source_commit": strings.Repeat("a", 40), "source_date_epoch": 1234567890.0, "source_dirty": false, "source_status": []any{}, "build_image": "archlinux@sha256:" + strings.Repeat("c", 64), "arch_snapshot": "2026/09/26", "hyprland_commit": strings.Repeat("b", 40), "packages": []any{"go 1.27.1", "qt6-base 6.11.2"}, "artifacts": artifacts, "runtime_files": assets, "sources": M{"main.go": strings.Repeat("d", 64)}}
	writeManifest(t, root, v)
	return root, v
}
func writeManifest(t *testing.T, root string, v M) {
	t.Helper()
	raw, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, manifestPath), raw, 0644); e != nil {
		t.Fatal(e)
	}
}

func TestValidBundleAndMissingManifest(t *testing.T) {
	root, _ := fixture(t)
	if e := Verify(root); e != nil {
		t.Fatal(e)
	}
	if e := os.Remove(filepath.Join(root, manifestPath)); e != nil {
		t.Fatal(e)
	}
	if e := Verify(root); !errors.Is(e, ErrMissingManifest) {
		t.Fatal("missing manifest indistinguishable", e)
	}
	if e := os.WriteFile(filepath.Join(root, manifestPath), []byte("{}"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := Verify(root); e == nil || errors.Is(e, ErrMissingManifest) {
		t.Fatal("broken manifest silently treated as development", e)
	}
	empty := t.TempDir()
	os.Chmod(empty, 0700)
	if e := Verify(empty); !errors.Is(e, ErrMissingManifest) {
		t.Fatal(e)
	}
}
func TestManifestStrictSchema(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(M)
	}{{"boolean_schema", func(v M) { v["schema_version"] = true }}, {"boolean_bytes", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["bytes"] = true }}, {"fractional_bytes", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["bytes"] = 1.5 }}, {"unbounded_bytes", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["bytes"] = float64(MaxArtifact + 1) }}, {"zero_bytes", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["bytes"] = 0.0 }}, {"uppercase_hash", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["sha256"] = strings.Repeat("A", 64) }}, {"extra_record", func(v M) { object(object(v["artifacts"])[artifactNames[0]])["extra"] = true }}, {"missing_artifact", func(v M) { delete(object(v["artifacts"]), artifactNames[0]) }}, {"extra_artifact", func(v M) { object(v["artifacts"])["outside"] = M{"bytes": 1.0, "sha256": strings.Repeat("a", 64)} }}, {"traversal", func(v M) {
		delete(object(v["artifacts"]), artifactNames[0])
		object(v["artifacts"])["../outside"] = M{"bytes": 1.0, "sha256": strings.Repeat("a", 64)}
	}}, {"runtime_traversal", func(v M) {
		delete(object(v["runtime_files"]), runtimeNames[0])
		object(v["runtime_files"])["../outside"] = M{"bytes": 1.0, "sha256": strings.Repeat("a", 64)}
	}}, {"runtime_extra_field", func(v M) { object(object(v["runtime_files"])[runtimeNames[0]])["extra"] = true }}, {"runtime_missing", func(v M) { delete(object(v["runtime_files"]), runtimeNames[0]) }}, {"source_traversal", func(v M) { object(v["sources"])["/etc/passwd"] = strings.Repeat("a", 64) }}, {"dirty_number", func(v M) { v["source_dirty"] = 1.0 }}, {"epoch_boolean", func(v M) { v["source_date_epoch"] = true }}, {"legacy_runtime", func(v M) { delete(v, "runtime") }}, {"extra_top_field", func(v M) { v["extra"] = true }}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, v := fixture(t)
			c.mutate(v)
			writeManifest(t, root, v)
			if e := Verify(root); e == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}
func TestDuplicateAndBoundedManifest(t *testing.T) {
	root, v := fixture(t)
	raw, _ := json.Marshal(v)
	raw = append([]byte(`{"schema_version":1,`), raw[1:]...)
	if e := os.WriteFile(filepath.Join(root, manifestPath), raw, 0644); e != nil {
		t.Fatal(e)
	}
	if e := Verify(root); e == nil {
		t.Fatal("duplicate field accepted")
	}
	os.WriteFile(filepath.Join(root, manifestPath), []byte(strings.Repeat(" ", MaxManifest+1)), 0644)
	if e := Verify(root); e == nil {
		t.Fatal("unbounded manifest accepted")
	}
}
func TestAllRecordsValidateBeforeArtifacts(t *testing.T) {
	root, v := fixture(t)
	for _, name := range artifactNames {
		if e := os.Remove(filepath.Join(root, name)); e != nil {
			t.Fatal(e)
		}
	}
	object(object(v["runtime_files"])[runtimeNames[len(runtimeNames)-1]])["sha256"] = "invalid"
	writeManifest(t, root, v)
	e := Verify(root)
	if e == nil || !strings.Contains(e.Error(), "invalid runtime file record") {
		t.Fatal("artifact filesystem accessed before all manifest records validated", e)
	}
}
func TestSameLengthTampering(t *testing.T) {
	for _, name := range []string{"native/qt/a-weather-app-qt", "a-weather-app", "LICENSE", "quickshell/a-weather-app.weather/WeatherWidget.qml"} {
		t.Run(name, func(t *testing.T) {
			root, _ := fixture(t)
			p := filepath.Join(root, name)
			raw, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			raw[0] ^= 1
			if e = os.WriteFile(p, raw, 0755); e != nil {
				t.Fatal(e)
			}
			if e := Verify(root); e == nil || !strings.Contains(e.Error(), "checksum") {
				t.Fatal("same-length tampering accepted", e)
			}
		})
	}
}
func TestLinksAndUnsafeModes(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) error
	}{{"file_symlink", func(root string) error {
		p := filepath.Join(root, artifactNames[0])
		if e := os.Remove(p); e != nil {
			return e
		}
		return os.Symlink(filepath.Join(root, "LICENSE"), p)
	}}, {"manifest_symlink", func(root string) error {
		p := filepath.Join(root, manifestPath)
		if e := os.Remove(p); e != nil {
			return e
		}
		return os.Symlink(filepath.Join(root, "LICENSE"), p)
	}}, {"hardlink", func(root string) error {
		return os.Link(filepath.Join(root, artifactNames[0]), filepath.Join(root, "hardlink"))
	}}, {"directory_symlink", func(root string) error {
		p := filepath.Join(root, "native/qt")
		if e := os.Rename(p, p+"-old"); e != nil {
			return e
		}
		return os.Symlink(p+"-old", p)
	}}, {"writable_file", func(root string) error { return os.Chmod(filepath.Join(root, "LICENSE"), 0666) }}, {"writable_directory", func(root string) error { return os.Chmod(filepath.Join(root, "native"), 0777) }}, {"unexecutable_qt", func(root string) error { return os.Chmod(filepath.Join(root, "native/qt/a-weather-app-qt"), 0644) }}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, _ := fixture(t)
			if e := c.mutate(root); e != nil {
				t.Fatal(e)
			}
			if e := Verify(root); e == nil {
				t.Fatal("unsafe bundle accepted")
			}
		})
	}
}
func TestExtraInventoryAndRootBoundary(t *testing.T) {
	root, _ := fixture(t)
	os.WriteFile(filepath.Join(root, "unexpected.py"), []byte("not runtime"), 0644)
	if e := Verify(root); e == nil {
		t.Fatal("extra file accepted")
	}
	root, _ = fixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(root, alias)
	if e := Verify(alias); e == nil {
		t.Fatal("symlink root accepted")
	}
	for _, p := range []string{"/", "relative", root + "/.."} {
		if e := Verify(p); e == nil {
			t.Fatal("unsafe root accepted", p)
		}
	}
	parent := t.TempDir()
	os.Chmod(parent, 0777)
	nested := filepath.Join(parent, "package")
	os.Mkdir(nested, 0700)
	if e := Verify(nested); e == nil || errors.Is(e, ErrMissingManifest) {
		t.Fatal("writable ancestor accepted", e)
	}
}
func TestSourceInventoryIsNeverRead(t *testing.T) {
	root, v := fixture(t)
	v["sources"] = M{"not-present/main.go": strings.Repeat("d", 64), ".github/workflows/build.yml": strings.Repeat("e", 64)}
	writeManifest(t, root, v)
	if e := Verify(root); e != nil {
		t.Fatal("source hashes incorrectly treated as runtime paths", e)
	}
}

func TestOwnershipAndRegularFileChecks(t *testing.T) {
	verifier := &tree{systemUID: 0}
	foreign := uint32(os.Geteuid() + 100)
	if foreign == 0 {
		foreign++
	}
	if e := verifier.checkFile(syscall.Stat_t{Mode: syscall.S_IFREG | 0644, Uid: foreign, Nlink: 1}); e == nil {
		t.Fatal("foreign file ownership accepted")
	}
	if e := verifier.checkFile(syscall.Stat_t{Mode: syscall.S_IFREG | 0644, Uid: uint32(os.Geteuid()), Nlink: 2}); e == nil {
		t.Fatal("hardlinked inode accepted")
	}
	root, _ := fixture(t)
	artifact := filepath.Join(root, artifactNames[0])
	if e := os.Remove(artifact); e != nil {
		t.Fatal(e)
	}
	if e := syscall.Mkfifo(artifact, 0600); e != nil {
		t.Fatal(e)
	}
	if e := Verify(root); e == nil {
		t.Fatal("FIFO runtime file accepted")
	}
	root, _ = fixture(t)
	if e := os.Truncate(filepath.Join(root, artifactNames[0]), MaxArtifact+1); e != nil {
		t.Fatal(e)
	}
	if e := Verify(root); e == nil {
		t.Fatal("unbounded runtime artifact accepted")
	}
}
