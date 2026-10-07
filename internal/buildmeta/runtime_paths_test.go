package buildmeta

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

type runtimePathFixture struct {
	root, source, data, app, downloads, marker string
}

func writeRuntimeFixture(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	// File creation applies the process umask. Set the requested fixture mode
	// explicitly so permission tests also work in the release container (077).
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeInstallerAllowsDirectoryEntryChurn(t *testing.T) {
	f := newRuntimePathFixture(t)
	// Report the data directory's old stat, then add an ordinary owned child
	// before the validator opens it. This deterministically changes only the
	// directory link count, as parallel temporary-file activity can do in /tmp.
	statStub := `#!/usr/bin/bash
set -euo pipefail
/usr/bin/stat "$@"
path=${!#}
if [[ $path == */data && -d $path && ! -e $path/ordinary-new-child ]]; then
  mkdir -m 700 -- "$path/ordinary-new-child"
fi
`
	writeRuntimeFixture(t, filepath.Join(f.root, "bin/stat"), []byte(statStub), 0700)
	output, err := f.run(t, "scripts/install_release_runtime.sh")
	if err != nil {
		t.Fatalf("ordinary directory activity rejected: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(f.data, "ordinary-new-child")); err != nil {
		t.Fatalf("churn fixture did not run: %v", err)
	}
}

func TestRuntimePrepareOnlyPreservesRunningSelection(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "bad_checksum"}[corrupt], func(t *testing.T) {
			f := newRuntimePathFixture(t)
			old := filepath.Join(f.app, "releases/v0.50.0")
			writeRuntimeFixture(t, filepath.Join(old, "saved-marker"), []byte("retain old installation"), 0600)
			if e := os.Symlink("releases/v0.50.0", filepath.Join(f.app, "current")); e != nil {
				t.Fatal(e)
			}
			if corrupt {
				writeRuntimeFixture(t, filepath.Join(f.downloads, "a-weather-app-v0.1.0-linux-x86_64.tar"), []byte("corrupt download"), 0600)
			}
			output, e := f.run(t, "scripts/install_release_runtime.sh", "--prepare-only")
			if (e != nil) != corrupt {
				t.Fatal("unexpected preparation result", e, string(output))
			}
			selection, e := os.Readlink(filepath.Join(f.app, "current"))
			if e != nil || selection != "releases/v0.50.0" {
				t.Fatal("preparation changed current runtime", selection, e)
			}
			raw, e := os.ReadFile(filepath.Join(old, "saved-marker"))
			if e != nil || string(raw) != "retain old installation" {
				t.Fatal("old installation changed", e)
			}
			if !corrupt {
				if !strings.Contains(string(output), "Prepared v0.1.0") {
					t.Fatal(string(output))
				}
				if _, e := os.Stat(filepath.Join(f.app, "releases/v0.1.0/a-weather-app")); e != nil {
					t.Fatal("verified helper was not staged", e)
				}
			}
		})
	}
}

func newRuntimePathFixture(t *testing.T) runtimePathFixture {
	t.Helper()
	// All fixtures and download stubs are private, disposable, and offline.
	root, err := os.MkdirTemp("/tmp", "weather-runtime-paths-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	f := runtimePathFixture{root: root, source: filepath.Join(root, "source"), data: filepath.Join(root, "data"), downloads: filepath.Join(root, "downloads"), marker: filepath.Join(root, "download-called")}
	f.app = filepath.Join(f.data, "a-weather-app")
	for _, name := range []string{"a-weather-app", "scripts/install_release_runtime.sh", "scripts/runtime_paths.sh", "scripts/bootstrap_update.sh", "scripts/bar_release_status.sh"} {
		raw, err := os.ReadFile(filepath.Join("../..", name))
		if err != nil {
			t.Fatal(err)
		}
		writeRuntimeFixture(t, filepath.Join(f.source, name), raw, 0700)
	}
	if err := os.MkdirAll(f.data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.downloads, 0700); err != nil {
		t.Fatal(err)
	}
	stub := `#!/usr/bin/bash
set -euo pipefail
printf 'download\n' >> "$WEATHER_TEST_DOWNLOAD_MARKER"
output=
while (( $# )); do
  if [[ $1 == --output ]]; then output=$2; shift 2; else url=$1; shift; fi
done
cp -- "$WEATHER_TEST_DOWNLOADS/${url##*/}" "$output"
`
	writeRuntimeFixture(t, filepath.Join(root, "bin/curl"), []byte(stub), 0700)
	// A valid tiny release lets installation/update tests run all existing
	// checksum, archive, metadata, and publication steps without network access.
	runtime := filepath.Join(root, "runtime")
	binary := []byte("#!/usr/bin/bash\nprintf 'fixture runtime: %s\\n' \"$*\"\n")
	manifest := []byte(`{"version":"0.1.0"}`)
	writeRuntimeFixture(t, filepath.Join(runtime, "a-weather-app"), binary, 0700)
	writeRuntimeFixture(t, filepath.Join(runtime, "manifest.json"), manifest, 0600)
	sourceCommit := strings.Repeat("a", 40)
	metadata := map[string]any{"source_commit": sourceCommit, "source_dirty": false, "source_status": []string{}, "runtime": "go-qt", "architecture": "x86_64", "artifacts": map[string]any{"a-weather-app": map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(binary))}}, "runtime_files": map[string]any{"manifest.json": map[string]string{"sha256": fmt.Sprintf("%x", sha256.Sum256(manifest))}}}
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, filepath.Join(runtime, "packaging/runtime.json"), raw, 0600)
	writeRuntimeFixture(t, filepath.Join(f.downloads, "go-runtime.json"), raw, 0600)
	archiveName := "a-weather-app-v0.1.0-linux-x86_64.tar"
	archive := filepath.Join(f.downloads, archiveName)
	if output, err := exec.Command("tar", "-C", runtime, "-cf", archive, ".").CombinedOutput(); err != nil {
		t.Fatalf("tar fixture: %v: %s", err, output)
	}
	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(archiveBytes))
	writeRuntimeFixture(t, filepath.Join(f.downloads, "SHA256SUMS"), []byte(hash+"  "+archiveName+"\n"), 0600)
	pin, err := json.Marshal(map[string]any{"schemaVersion": 1, "tag": "v0.1.0", "source_commit": sourceCommit, "archive_sha256": hash})
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, filepath.Join(f.source, "packaging/release-lock.json"), pin, 0600)
	return f
}

func TestOldRuntimeBarReportsPinnedMismatchAndHelperProgress(t *testing.T) {
	for _, cached := range []string{"", "{bad json", `{"state":"verifying","installed":"0.51.5","available":"0.51.9","message":"Verifying the downloaded release…","checked_at":1791300000}`, `{"state":"downloading","installed":"0.51.5","available":"0.51.9","message":"Downloading","checked_at":1791300000}`} {
		t.Run(cached, func(t *testing.T) {
			f := newRuntimePathFixture(t)
			binary := []byte("#!/usr/bin/bash\nprintf '%s\\n' '{\"schema_version\":1,\"label\":\"20°C\",\"tooltip\":\"Saved weather\",\"freshness\":\"fresh\"}'\n")
			writeRuntimeFixture(t, filepath.Join(f.app, "releases/v0.51.5/a-weather-app"), binary, 0700)
			if e := os.Symlink("releases/v0.51.5", filepath.Join(f.app, "current")); e != nil {
				t.Fatal(e)
			}
			pin, _ := json.Marshal(map[string]any{"schemaVersion": 1, "tag": "v0.51.9", "source_commit": strings.Repeat("a", 40), "archive_sha256": strings.Repeat("b", 64)})
			writeRuntimeFixture(t, filepath.Join(f.source, "packaging/release-lock.json"), pin, 0600)
			state := filepath.Join(f.root, "state")
			if cached != "" {
				writeRuntimeFixture(t, filepath.Join(state, "update-status.json"), []byte(cached), 0600)
			}
			if strings.Contains(cached, "verifying") {
				d, e := safeio.OpenDir(state, false)
				if e != nil {
					t.Fatal(e)
				}
				defer d.Close()
				f, e := d.Lock("update-install.lock")
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
			}
			output, e := f.run(t, "a-weather-app", "--state-dir", state, "--bar")
			if e != nil {
				t.Fatal(e, string(output))
			}
			var value map[string]any
			if e = json.Unmarshal(output, &value); e != nil {
				t.Fatal(e, string(output))
			}
			u, _ := value["update"].(map[string]any)
			want := "available"
			if strings.Contains(cached, "verifying") {
				want = "verifying"
			}
			if strings.Contains(cached, "downloading") {
				want = "failed"
			}
			if u["state"] != want || u["installed"] != "0.51.5" || u["available"] != "0.51.9" {
				t.Fatal("wrong update notice", u)
			}
			if value["label"] != "20°C" || value["freshness"] != "fresh" {
				t.Fatal("weather output changed", value)
			}
			if _, e := os.Stat(f.marker); !os.IsNotExist(e) {
				t.Fatal("cached bar read downloaded a release", e)
			}
		})
	}
}

func TestOldRuntimeLauncherBootstrapsDetachedVerifiedHelper(t *testing.T) {
	f := newRuntimePathFixture(t)
	config := filepath.Join(f.root, "config")
	plugin := filepath.Join(config, "omarchy/plugins/a-weather-app.weather")
	if e := os.MkdirAll(filepath.Dir(plugin), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(f.source, plugin); e != nil {
		t.Fatal(e)
	}
	f.source = plugin
	t.Setenv("XDG_CONFIG_HOME", config)
	oldBinary := []byte("#!/usr/bin/bash\nprintf 'old runtime help\\n'\n")
	writeRuntimeFixture(t, filepath.Join(f.app, "releases/v0.50.0/a-weather-app"), oldBinary, 0700)
	if e := os.Symlink("releases/v0.50.0", filepath.Join(f.app, "current")); e != nil {
		t.Fatal(e)
	}
	// The downloaded helper waits until the launcher has exited, proving it
	// survives that boundary. Every modified fixture byte is pinned anew.
	binary := []byte(`#!/usr/bin/bash
set -euo pipefail
if [[ ${1:-} == --help ]]; then printf '%s\n' '-bootstrap-update'; exit 0; fi
while [[ ! -e "$WEATHER_TEST_DOWNLOAD_MARKER.gate" ]]; do sleep 0.02; done
printf '%s\n' "$@" > "$WEATHER_TEST_DOWNLOAD_MARKER.bootstrap"
`)
	runtime := filepath.Join(f.root, "runtime")
	writeRuntimeFixture(t, filepath.Join(runtime, "a-weather-app"), binary, 0700)
	raw, e := os.ReadFile(filepath.Join(runtime, "packaging/runtime.json"))
	if e != nil {
		t.Fatal(e)
	}
	var metadata map[string]any
	if e = json.Unmarshal(raw, &metadata); e != nil {
		t.Fatal(e)
	}
	metadata["artifacts"].(map[string]any)["a-weather-app"].(map[string]any)["sha256"] = fmt.Sprintf("%x", sha256.Sum256(binary))
	raw, e = json.Marshal(metadata)
	if e != nil {
		t.Fatal(e)
	}
	writeRuntimeFixture(t, filepath.Join(runtime, "packaging/runtime.json"), raw, 0600)
	writeRuntimeFixture(t, filepath.Join(f.downloads, "go-runtime.json"), raw, 0600)
	archiveName := "a-weather-app-v0.1.0-linux-x86_64.tar"
	archive := filepath.Join(f.downloads, archiveName)
	if out, e := exec.Command("tar", "-C", runtime, "-cf", archive, ".").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	raw, e = os.ReadFile(archive)
	if e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	writeRuntimeFixture(t, filepath.Join(f.downloads, "SHA256SUMS"), []byte(hash+"  "+archiveName+"\n"), 0600)
	pin, _ := json.Marshal(map[string]any{"schemaVersion": 1, "tag": "v0.1.0", "source_commit": strings.Repeat("a", 40), "archive_sha256": hash})
	writeRuntimeFixture(t, filepath.Join(f.source, "packaging/release-lock.json"), pin, 0600)
	state := filepath.Join(f.root, "state")
	output, e := f.run(t, "a-weather-app", "--state-dir", state, "--install-update")
	if e != nil || !strings.Contains(string(output), "Update helper started") {
		t.Fatal(e, string(output))
	}
	if got, e := os.Readlink(filepath.Join(f.app, "current")); e != nil || got != "releases/v0.50.0" {
		t.Fatal("bootstrap closed or replaced old runtime prematurely", got, e)
	}
	writeRuntimeFixture(t, f.marker+".gate", []byte("launcher exited"), 0600)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, e := os.ReadFile(f.marker + ".bootstrap")
		if e == nil {
			if string(raw) != "--update-worker\n--bootstrap-update\n--state-dir\n"+state+"\n" {
				t.Fatal("wrong detached helper arguments", string(raw))
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("staged helper did not survive launcher shutdown")
}

func (f runtimePathFixture) run(t *testing.T, script string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", append([]string{filepath.Join(f.source, script)}, args...)...)
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+f.data, "PATH="+filepath.Join(f.root, "bin")+":"+os.Getenv("PATH"), "WEATHER_TEST_DOWNLOADS="+f.downloads, "WEATHER_TEST_DOWNLOAD_MARKER="+f.marker)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("script blocked: %s", output)
	}
	return output, err
}

func TestRuntimeInstallerRejectsUnsafePathsBeforeWrites(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, runtimePathFixture)
	}{
		{"writable data", func(t *testing.T, f runtimePathFixture) {
			if err := os.Chmod(f.data, 0777); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink data", func(t *testing.T, f runtimePathFixture) {
			os.Rename(f.data, f.data+"-real")
			if err := os.Symlink(f.data+"-real", f.data); err != nil {
				t.Fatal(err)
			}
		}},
		{"writable app", func(t *testing.T, f runtimePathFixture) { os.Mkdir(f.app, 0700); os.Chmod(f.app, 0777) }},
		{"symlink app", func(t *testing.T, f runtimePathFixture) {
			if err := os.Symlink(f.root, f.app); err != nil {
				t.Fatal(err)
			}
		}},
		{"writable releases", func(t *testing.T, f runtimePathFixture) {
			os.MkdirAll(filepath.Join(f.app, "releases"), 0700)
			os.Chmod(filepath.Join(f.app, "releases"), 0777)
		}},
		{"symlink releases", func(t *testing.T, f runtimePathFixture) {
			os.Mkdir(f.app, 0700)
			if err := os.Symlink(f.root, filepath.Join(f.app, "releases")); err != nil {
				t.Fatal(err)
			}
		}},
		{"writable old release", func(t *testing.T, f runtimePathFixture) {
			path := filepath.Join(f.app, "releases/v0.1.0")
			os.MkdirAll(path, 0700)
			os.Chmod(path, 0777)
		}},
		{"symlink lock", func(t *testing.T, f runtimePathFixture) {
			os.Mkdir(f.app, 0700)
			if err := os.Symlink(filepath.Join(f.root, "sentinel"), filepath.Join(f.app, ".install.lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{"hardlinked lock", func(t *testing.T, f runtimePathFixture) {
			os.Mkdir(f.app, 0700)
			if err := os.Link(filepath.Join(f.root, "sentinel"), filepath.Join(f.app, ".install.lock")); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory lock", func(t *testing.T, f runtimePathFixture) {
			if err := os.MkdirAll(filepath.Join(f.app, ".install.lock"), 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"fifo lock", func(t *testing.T, f runtimePathFixture) {
			os.Mkdir(f.app, 0700)
			if output, err := exec.Command("mkfifo", filepath.Join(f.app, ".install.lock")).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, output)
			}
		}},
		{"public lock", func(t *testing.T, f runtimePathFixture) {
			writeRuntimeFixture(t, filepath.Join(f.app, ".install.lock"), []byte("old-lock"), 0644)
		}},
		{"unrelated current", func(t *testing.T, f runtimePathFixture) {
			os.Mkdir(f.app, 0700)
			if err := os.Symlink(f.root, filepath.Join(f.app, "current")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRuntimePathFixture(t)
			sentinel := []byte("must not be truncated\n")
			writeRuntimeFixture(t, filepath.Join(f.root, "sentinel"), sentinel, 0600)
			tc.setup(t, f)
			before := runtimeFixtureEntries(t, f.root)
			output, err := f.run(t, "scripts/install_release_runtime.sh")
			if err == nil {
				t.Fatalf("unsafe installation accepted: %s", output)
			}
			if _, err := os.Stat(f.marker); !os.IsNotExist(err) {
				t.Fatal("download ran before path rejection")
			}
			after := runtimeFixtureEntries(t, f.root)
			if before != after {
				t.Fatalf("fixture entries changed before rejection:\nbefore %s\nafter %s\n%s", before, after, output)
			}
			got, err := os.ReadFile(filepath.Join(f.root, "sentinel"))
			if err != nil || string(got) != string(sentinel) {
				t.Fatalf("sentinel changed: %q %v", got, err)
			}
		})
	}
}

func runtimeFixtureEntries(t *testing.T, root string) string {
	t.Helper()
	var entries []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		size := info.Size()
		if info.IsDir() {
			size = 0
		}
		entries = append(entries, fmt.Sprintf("%s:%v:%d", strings.TrimPrefix(path, root), info.Mode(), size))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Directory byte sizes can change when temporary entries are removed, so
	// compare names/types and file sizes, not directory implementation details.
	return strings.Join(entries, "\n")
}

func TestRuntimeInstallerAndWrapperOwnedRelease(t *testing.T) {
	f := newRuntimePathFixture(t)
	for n := 0; n < 2; n++ {
		output, err := f.run(t, "scripts/install_release_runtime.sh")
		if err != nil {
			t.Fatalf("install %d: %v: %s", n, err, output)
		}
		if n == 0 {
			writeRuntimeFixture(t, filepath.Join(f.app, ".install.lock"), []byte("keep existing lock contents"), 0600)
		}
	}
	lock, err := os.ReadFile(filepath.Join(f.app, ".install.lock"))
	if err != nil || string(lock) != "keep existing lock contents" {
		t.Fatalf("old lock was changed: %q %v", lock, err)
	}
	selection, err := os.Readlink(filepath.Join(f.app, "current"))
	if err != nil || selection != "releases/v0.1.0" {
		t.Fatalf("current: %q %v", selection, err)
	}
	output, err := f.run(t, "a-weather-app", "--bar", "--offline")
	if err != nil || string(output) != "fixture runtime: --bar --offline\n" {
		t.Fatalf("launcher: %v: %s", err, output)
	}
}

func TestRuntimeWrapperRejectsUnsafeExecutableSelection(t *testing.T) {
	for _, kind := range []string{"writable ancestor", "symlink ancestor", "writable release", "symlink executable", "hardlinked executable", "writable executable", "unrelated current"} {
		t.Run(kind, func(t *testing.T) {
			f := newRuntimePathFixture(t)
			if output, err := f.run(t, "scripts/install_release_runtime.sh"); err != nil {
				t.Fatalf("fixture install: %v: %s", err, output)
			}
			binary := filepath.Join(f.app, "releases/v0.1.0/a-weather-app")
			switch kind {
			case "writable ancestor":
				os.Chmod(f.data, 0777)
			case "symlink ancestor":
				os.Rename(f.data, f.data+"-real")
				os.Symlink(f.data+"-real", f.data)
			case "writable release":
				os.Chmod(filepath.Dir(binary), 0777)
			case "symlink executable":
				os.Rename(binary, filepath.Join(f.root, "binary"))
				os.Symlink(filepath.Join(f.root, "binary"), binary)
			case "hardlinked executable":
				os.Link(binary, filepath.Join(f.root, "binary"))
			case "writable executable":
				os.Chmod(binary, 0777)
			case "unrelated current":
				os.Remove(filepath.Join(f.app, "current"))
				os.Symlink(filepath.Join(f.root, "runtime"), filepath.Join(f.app, "current"))
			}
			output, err := f.run(t, "a-weather-app", "--bar")
			if err == nil || strings.Contains(string(output), "fixture runtime:") {
				t.Fatalf("unsafe wrapper executed: %v %s", err, output)
			}
		})
	}
}

func TestRuntimeDirectoryDescriptorSurvivesPathReplacement(t *testing.T) {
	f := newRuntimePathFixture(t)
	if err := os.Mkdir(f.app, 0700); err != nil {
		t.Fatal(err)
	}
	script := `set -euo pipefail
source "$1/scripts/runtime_paths.sh"
weather_open_directory "$2" 0
weather_open_child "$weather_dir_fd" a-weather-app 0 0 1
fd=$weather_child_fd
mv "$2/a-weather-app" "$2/held-app"
ln -s "$3" "$2/a-weather-app"
printf 'anchored' > "/proc/self/fd/$fd/result"
`
	cmd := exec.Command("bash", "-c", script, "descriptor-test", f.source, f.data, f.root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("anchored write: %v: %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(f.root, "result")); !os.IsNotExist(err) {
		t.Fatal("write followed replacement symlink")
	}
	got, err := os.ReadFile(filepath.Join(f.data, "held-app/result"))
	if err != nil || string(got) != "anchored" {
		t.Fatalf("held directory not used: %q %v", got, err)
	}
}

func TestRuntimeOpenRejectsReplacementBetweenStatAndOpen(t *testing.T) {
	for _, kind := range []string{"directory", "executable"} {
		t.Run(kind, func(t *testing.T) {
			f := newRuntimePathFixture(t)
			victim := filepath.Join(f.data, "victim")
			replacement := filepath.Join(f.root, "replacement")
			if kind == "directory" {
				if err := os.Mkdir(victim, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(replacement, 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeRuntimeFixture(t, victim, []byte("#!/usr/bin/bash\nprintf 'original'\n"), 0700)
				writeRuntimeFixture(t, replacement, []byte("#!/usr/bin/bash\nprintf 'substituted'\n"), 0700)
			}
			// This offline stat interposer deterministically replaces the selected
			// entry AFTER lstat and BEFORE Bash opens it. The held object's stat
			// must disagree, so no write or executable invocation is permitted.
			statPath, err := exec.LookPath("stat")
			if err != nil {
				t.Fatal(err)
			}
			stub := `#!/usr/bin/bash
set -euo pipefail
result=$("$WEATHER_TEST_REAL_STAT" "$@")
if [[ ${!#} == */victim && ! -e "$WEATHER_TEST_RACE_DONE" ]]; then
  mv -- "$WEATHER_TEST_VICTIM" "$WEATHER_TEST_VICTIM.saved"
  ln -s -- "$WEATHER_TEST_REPLACEMENT" "$WEATHER_TEST_VICTIM"
  : > "$WEATHER_TEST_RACE_DONE"
fi
printf '%s' "$result"
`
			writeRuntimeFixture(t, filepath.Join(f.root, "bin/stat"), []byte(stub), 0700)
			action := "weather_open_child \"$weather_dir_fd\" victim 0 0 1\nprintf 'wrote' > \"/proc/self/fd/$weather_child_fd/result\""
			if kind == "executable" {
				action = "weather_open_file \"$weather_dir_fd\" victim\nexec \"/proc/self/fd/$weather_file_fd\""
			}
			script := "set -euo pipefail\nsource \"$1/scripts/runtime_paths.sh\"\nweather_open_directory \"$2\" 0\n" + action
			cmd := exec.Command("bash", "-c", script, "open-race-test", f.source, f.data)
			cmd.Env = append(os.Environ(), "PATH="+filepath.Join(f.root, "bin")+":"+os.Getenv("PATH"), "WEATHER_TEST_REAL_STAT="+statPath, "WEATHER_TEST_VICTIM="+victim, "WEATHER_TEST_REPLACEMENT="+replacement, "WEATHER_TEST_RACE_DONE="+filepath.Join(f.root, "race-done"))
			output, err := cmd.CombinedOutput()
			if err == nil || strings.Contains(string(output), "substituted") {
				t.Fatalf("replacement accepted: %v: %s", err, output)
			}
			if _, err := os.Stat(filepath.Join(f.root, "race-done")); err != nil {
				t.Fatalf("replacement race was not exercised: %v: %s", err, output)
			}
			if kind == "directory" {
				if _, err := os.Stat(filepath.Join(replacement, "result")); !os.IsNotExist(err) {
					t.Fatal("write reached replacement directory")
				}
			}
		})
	}
}

func TestRuntimeShellTrustRulesRejectForeignOwners(t *testing.T) {
	f := newRuntimePathFixture(t)
	script := `set -euo pipefail
source "$1/scripts/runtime_paths.sh"
weather_system_uid=0
foreign=$((EUID + 1))
if weather_check_directory "41c0 $foreign 1 0 1" 0; then exit 11; fi
if weather_check_file "81c0 $foreign 1 0 1"; then exit 12; fi
# A foreign sticky ancestor does not get the system-directory exception.
if weather_check_directory "43ff $foreign 1 0 1" 1; then exit 13; fi
`
	if output, err := exec.Command("bash", "-c", script, "owner-rule-test", f.source).CombinedOutput(); err != nil {
		t.Fatalf("owner trust checks: %v: %s", err, output)
	}
}

func TestRuntimeWrapperMissingRuntimeAndOwnedDevelopmentBuild(t *testing.T) {
	f := newRuntimePathFixture(t)
	output, err := f.run(t, "a-weather-app", "--bar")
	if err != nil || !strings.Contains(string(output), `"label":"Install weather app"`) {
		t.Fatalf("missing runtime bar fallback: %v: %s", err, output)
	}
	output, err = f.run(t, "a-weather-app", "--refresh-bar")
	if err != nil || len(output) != 0 {
		t.Fatalf("missing runtime refresh fallback: %v: %s", err, output)
	}
	binary := filepath.Join(f.source, "build/a-weather-app")
	writeRuntimeFixture(t, binary, []byte("#!/usr/bin/bash\nprintf 'owned development build'\n"), 0700)
	output, err = f.run(t, "a-weather-app", "--bar")
	if err != nil || string(output) != "owned development build" {
		t.Fatalf("owned build execution: %v: %s", err, output)
	}
	if err := os.Chmod(filepath.Dir(binary), 0777); err != nil {
		t.Fatal(err)
	}
	output, err = f.run(t, "a-weather-app", "--bar")
	if err == nil || strings.Contains(string(output), "owned development build") {
		t.Fatalf("writable build directory accepted: %v: %s", err, output)
	}
}
