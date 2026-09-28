package buildmeta

import (
	"context"
	"encoding/json"
	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func fixture(t *testing.T) (string, string, Metadata) {
	t.Helper()
	root, source := t.TempDir(), t.TempDir()
	os.Chmod(root, 0700)
	os.Chmod(source, 0700)
	names := append(append([]string{}, artifacts...), "LICENSE", "THIRD_PARTY_NOTICES.md", "manifest.json", "README.md", "packaging/a-weather-app.desktop", "packaging/icons/a-weather-app.svg", "packaging/go-README.md", "quickshell/a-weather-app.weather/WeatherWidget.qml", "licenses/hyprland-dependencies.txt", "licenses/go.txt")
	for _, name := range names {
		p := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte("compiled fixture"), 0755); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"main.go", "a-weather-app", "build.pro", "resources.qrc", "config.toml", "Makefile", ".github/workflows/build.yml"} {
		p := filepath.Join(source, name)
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("source fixture"), 0644)
	}
	for _, dir := range []string{".agents", ".codex", "native/qt/.frontend-test-build"} {
		os.MkdirAll(filepath.Join(source, dir), 0755)
		os.WriteFile(filepath.Join(source, dir, "personal.md"), []byte("private"), 0644)
	}
	metadata := Metadata{strings.Repeat("a", 40), strings.Repeat("b", 40), 1234567890, []string{" M main.go"}, []string{"go 1.27.1", "qt6-base 6.11.2"}}
	if e := CreateWithMetadata(root, source, metadata); e != nil {
		t.Fatal(e)
	}
	return root, source, metadata
}
func TestCreateInventoryAndDeterminism(t *testing.T) {
	root, source, metadata := fixture(t)
	if e := Verify(root, source); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(root, manifestPath))
	if e != nil {
		t.Fatal(e)
	}
	value, e := safeio.Object(raw, release.MaxManifest)
	if e != nil || value["source_dirty"] != true {
		t.Fatal(value, e)
	}
	sources := value["sources"].(map[string]any)
	if len(sources) != 7 || sources["a-weather-app"] == nil {
		t.Fatal("private/generated sources leaked", sources)
	}
	if len(value["artifacts"].(map[string]any)) != 5 || len(value["runtime_files"].(map[string]any)) != 10 {
		t.Fatal("runtime inventory does not match verifier")
	}
	if e := CreateWithMetadata(root, source, metadata); e != nil {
		t.Fatal(e)
	}
	repeat, _ := os.ReadFile(filepath.Join(root, manifestPath))
	if string(raw) != string(repeat) {
		t.Fatal("same inputs produced different metadata")
	}
}

func TestSourceOwnershipAnchoredToExplicitRoot(t *testing.T) {
	// Model root inside a container consuming UID 1000's read-only bind mount.
	for _, uid := range []uint32{0, 1000} {
		if !inputOwnerAllowed(uid, 1000, 0) {
			t.Fatal("anchored source owner rejected", uid)
		}
	}
	if inputOwnerAllowed(1001, 1000, 0) || inputOwnerAllowed(1000, 0, 0) {
		t.Fatal("unrelated or runtime owner accepted")
	}
	root, source, metadata := fixture(t)
	if os.Geteuid() == 0 {
		if e := filepath.Walk(source, func(path string, _ os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			return os.Chown(path, 1000, 1000)
		}); e != nil {
			t.Fatal(e)
		}
		if e := CreateWithMetadata(root, source, metadata); e != nil {
			t.Fatal("host-owned source rejected", e)
		}
		if e := Verify(root, source); e != nil {
			t.Fatal(e)
		}
		// A third owner's regular file is not covered by the root's trust anchor.
		if e := os.Chown(filepath.Join(source, "main.go"), 1001, 1001); e != nil {
			t.Fatal(e)
		}
		if e := Verify(root, source); e == nil {
			t.Fatal("foreign source owner accepted")
		}
	}
	if e := os.Chmod(source, 0777); e != nil {
		t.Fatal(e)
	}
	if _, e := sourceOwner(source); e == nil {
		t.Fatal("writable source root accepted")
	}
	if e := os.Chmod(source, 0700); e != nil {
		t.Fatal(e)
	}
}
func TestChangedRuntimeAndSources(t *testing.T) {
	for _, kind := range []string{"artifact", "runtime", "source", "launcher_source", "added_runtime", "added_source"} {
		t.Run(kind, func(t *testing.T) {
			root, source, _ := fixture(t)
			p := filepath.Join(root, "a-weather-app")
			switch kind {
			case "runtime":
				p = filepath.Join(root, "LICENSE")
			case "source":
				p = filepath.Join(source, "main.go")
			case "launcher_source":
				p = filepath.Join(source, "a-weather-app")
			case "added_runtime":
				p = filepath.Join(root, "unexpected.sh")
			case "added_source":
				p = filepath.Join(source, "new.go")
			}
			os.WriteFile(p, []byte("tampered fixture"), 0755)
			if e := Verify(root, source); e == nil {
				t.Fatal("tamper accepted")
			}
		})
	}
}
func TestStrictManifestAndLinks(t *testing.T) {
	root, source, _ := fixture(t)
	p := filepath.Join(root, manifestPath)
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, append([]byte(`{"schema_version":1,`), raw[1:]...), 0644)
	if e := Verify(root, source); e == nil {
		t.Fatal("duplicate metadata accepted")
	}
	root, source, _ = fixture(t)
	p = filepath.Join(root, "a-weather-app")
	os.Remove(p)
	os.Symlink(filepath.Join(root, "LICENSE"), p)
	if e := Verify(root, source); e == nil {
		t.Fatal("symlink artifact accepted")
	}
	root, source, _ = fixture(t)
	os.Link(filepath.Join(source, "main.go"), filepath.Join(source, "copy.go"))
	if e := Verify(root, source); e == nil {
		t.Fatal("hardlinked source accepted")
	}
}
func TestRejectPythonAndSourceSymlinks(t *testing.T) {
	for _, name := range []string{"helper.py", "helper.pyc", "helper.pyo", "helper.pyi", "helper.pyw", "helper.PYI", "__pycache__/cache"} {
		t.Run(name, func(t *testing.T) {
			root, source, metadata := fixture(t)
			p := filepath.Join(source, name)
			os.MkdirAll(filepath.Dir(p), 0755)
			os.WriteFile(p, []byte("forbidden"), 0644)
			if e := CreateWithMetadata(root, source, metadata); e == nil {
				t.Fatal("Python input accepted")
			}
		})
	}
	root, source, metadata := fixture(t)
	os.Symlink(filepath.Join(source, "main.go"), filepath.Join(source, "alias.go"))
	if e := CreateWithMetadata(root, source, metadata); e == nil {
		t.Fatal("source symlink accepted")
	}
}

func TestReadOnlySourceMount(t *testing.T) {
	valid := "36 25 0:32 / /source ro,relatime - ext4 /dev/sda rw,errors=remount-ro\n"
	for _, tc := range []struct {
		name, raw string
		want      bool
	}{
		{"readonly_bind_writable_backing", valid, true},
		{"optional_fields", "36 25 0:32 / /source ro,relatime shared:1 - ext4 /dev/sda rw\n", true},
		{"absent", "36 25 0:32 / /elsewhere ro - ext4 /dev/sda rw\n", false},
		{"writable_bind", strings.Replace(valid, "ro,relatime", "rw,relatime", 1), false},
		{"ro_substring", strings.Replace(valid, "ro,relatime", "errors=ro,relatime", 1), false},
		{"conflicting_options", strings.Replace(valid, "ro,relatime", "ro,rw", 1), false},
		{"missing_separator", strings.Replace(valid, " - ", " ", 1), false},
		{"missing_backing_fields", "36 25 0:32 / /source ro - ext4\n", false},
		{"stacked_mount", valid + valid, false},
		{"writable_nested_mount", valid + "40 36 0:40 / /source/nested rw - tmpfs tmpfs rw\n", false},
		{"readonly_nested_mount", valid + "40 36 0:40 / /source/nested ro - tmpfs tmpfs rw\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readOnlySourceMount([]byte(tc.raw)); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProductionCreateRefusesHostBeforeWrites(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	if e := Create(context.Background(), root, source); e == nil || !strings.Contains(e.Error(), "scripts/run_go_migration_build.sh") {
		t.Fatal("missing container refusal", e)
	}
	files, e := os.ReadDir(root)
	if e != nil || len(files) != 0 {
		t.Fatal("refused creation wrote package files", files, e)
	}
}
func TestInvalidMetadataAndNoSourceRuntimeReads(t *testing.T) {
	root, source, metadata := fixture(t)
	metadata.SourceCommit = "bad"
	if e := CreateWithMetadata(root, source, metadata); e == nil {
		t.Fatal("invalid provenance accepted")
	}
	root, source, _ = fixture(t)
	p := filepath.Join(root, manifestPath)
	raw, _ := os.ReadFile(p)
	value, _ := safeio.Object(raw, release.MaxManifest)
	value["sources"] = M{"missing-source.go": strings.Repeat("d", 64)}
	raw, _ = json.Marshal(value)
	os.WriteFile(p, raw, 0644)
	if e := Verify(root, ""); e != nil {
		t.Fatal("runtime audit read unshipped sources", e)
	}
	if e := Verify(root, source); e == nil {
		t.Fatal("source inventory mismatch ignored")
	}
}

func TestDockerHelperFreezesSourceAndPreservesExitStatus(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip("Git is unavailable")
	}
	repository := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", repository}, args...)...)
		if raw, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git fixture: %v %s", e, raw)
		}
	}
	git("init", "--quiet")
	git("config", "user.email", "fixture@example.invalid")
	git("config", "user.name", "Fixture")
	helper, e := os.ReadFile("../../scripts/run_go_migration_build.sh")
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(repository, "scripts"), 0755)
	os.WriteFile(filepath.Join(repository, "scripts/run_go_migration_build.sh"), helper, 0755)
	os.WriteFile(filepath.Join(repository, "main.go"), []byte("committed input"), 0644)
	os.WriteFile(filepath.Join(repository, "deleted.go"), []byte("deleted input"), 0644)
	git("add", ".")
	git("commit", "--quiet", "-m", "fixture baseline")
	os.Remove(filepath.Join(repository, "deleted.go"))
	os.WriteFile(filepath.Join(repository, "main.go"), []byte("frozen input"), 0644)
	bin := t.TempDir()
	mock := `#!/usr/bin/bash
set -euo pipefail
if [[ "$1" == info ]]; then exit 0; fi
snapshot=''
for argument in "$@"; do
  case "$argument" in type=bind,src=*,dst=/source,readonly)
    snapshot=${argument#type=bind,src=}; snapshot=${snapshot%,dst=/source,readonly};;
  esac
done
[[ -n "$snapshot" && "$snapshot" != "$WEATHER_TEST_SOURCE" && -d "$snapshot/.git" ]]
[[ ! -e "$snapshot/deleted.go" ]]
printf 'later edit' > "$WEATHER_TEST_SOURCE/main.go"
[[ $(< "$snapshot/main.go") == 'frozen input' ]]
git -C "$snapshot" rev-parse HEAD >/dev/null
printf 'PASS: fixture Docker used frozen source\n'
exit 23
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(mock), 0755)
	c := exec.Command("bash", filepath.Join(repository, "scripts/run_go_migration_build.sh"))
	c.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin", "WEATHER_TEST_SOURCE="+repository)
	raw, e := c.CombinedOutput()
	x, ok := e.(*exec.ExitError)
	if !ok || x.ExitCode() != 23 || !strings.Contains(string(raw), "Build exit status: 23") {
		t.Fatalf("container status lost: %v %s", e, raw)
	}
	match := regexp.MustCompile(`Private full log: (/tmp/weather-go-migration\.[A-Za-z0-9]+\.log)`).FindSubmatch(raw)
	if len(match) != 2 {
		t.Fatalf("private log not reported: %s", raw)
	}
	logName := string(match[1])
	defer os.Remove(logName)
	info, e := os.Stat(logName)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("full log is not private", e)
	}
	log, _ := os.ReadFile(logName)
	if !strings.Contains(string(log), "PASS: fixture Docker used frozen source") {
		t.Fatalf("full build log lost: %s", log)
	}
	if later, _ := os.ReadFile(filepath.Join(repository, "main.go")); string(later) != "later edit" {
		t.Fatal("fixture did not edit source during the build")
	}
}
