package updater

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joega/a-weather-app/internal/release"
)

var fixtureArtifacts = []string{"a-weather-app", "native/qt/a-weather-app-qt", "ui/shaders/atmosphere.frag.qsb", "native/frame-alignment/a-weather-app-frame-alignment.so", "native/atmosphere/a-weather-app-atmosphere"}
var fixtureAssets = []string{"LICENSE", "THIRD_PARTY_NOTICES.md", "manifest.json", "README.md", "packaging/a-weather-app.desktop", "packaging/icons/a-weather-app.svg", "packaging/go-README.md", "quickshell/a-weather-app.weather/WeatherWidget.qml", "licenses/hyprland-dependencies.txt", "licenses/go.txt"}

func bundleFixture(t *testing.T, root, version string, overrides map[string][]byte) []byte {
	t.Helper()
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	artifacts, assets := map[string]any{}, map[string]any{}
	for i, names := range [][]string{fixtureArtifacts, fixtureAssets} {
		for _, name := range names {
			body := []byte("verified fixture: " + name)
			if name == "manifest.json" {
				body = []byte(`{"id":"a-weather-app.weather","version":"` + version + `"}`)
			}
			if override, ok := overrides[name]; ok {
				body = override
			}
			file := filepath.Join(root, name)
			if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
				t.Fatal(e)
			}
			mode := os.FileMode(0600)
			if i == 0 {
				mode = 0700
			}
			if e := os.WriteFile(file, body, mode); e != nil {
				t.Fatal(e)
			}
			row := map[string]any{"bytes": len(body), "sha256": fmt.Sprintf("%x", sha256.Sum256(body))}
			if i == 0 {
				artifacts[name] = row
			} else {
				assets[name] = row
			}
		}
	}
	metadata := map[string]any{"schema_version": 1, "runtime": "go-qt", "architecture": "x86_64", "source_commit": strings.Repeat("b", 40), "source_date_epoch": 1791300000, "source_dirty": false, "source_status": []any{}, "build_image": "archlinux@sha256:" + strings.Repeat("c", 64), "arch_snapshot": "2026/09/26", "hyprland_commit": strings.Repeat("d", 40), "packages": []any{"fixture 1.0"}, "artifacts": artifacts, "runtime_files": assets, "sources": map[string]any{}}
	raw, e := json.Marshal(metadata)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, "packaging/runtime.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	if e = release.Verify(root); e != nil {
		t.Fatal("fixture invalid", e)
	}
	return raw
}

func bundleArchive(t *testing.T, root string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	e := filepath.Walk(root, func(file string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if file == root {
			return nil
		}
		rel, e := filepath.Rel(root, file)
		if e != nil {
			return e
		}
		h, e := tar.FileInfoHeader(info, "")
		if e != nil {
			return e
		}
		h.Name = "./" + filepath.ToSlash(rel)
		if e = w.WriteHeader(h); e != nil {
			return e
		}
		if info.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		_, e = w.Write(raw)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func fixtureSource(t *testing.T, version string, overrides map[string][]byte) (GitHubSource, Pin) {
	t.Helper()
	root := filepath.Join(privateState(t), "bundle")
	metadata := bundleFixture(t, root, version, overrides)
	archive := bundleArchive(t, root)
	pin := Pin{1, "v" + version, fmt.Sprintf("%x", sha256.Sum256(archive)), strings.Repeat("b", 40)}
	assets := map[string][]byte{"SHA256SUMS": []byte(pin.ArchiveSHA256 + "  a-weather-app-" + pin.Tag + "-linux-x86_64.tar\n"), "go-runtime.json": metadata, "a-weather-app-" + pin.Tag + "-linux-x86_64.tar": archive}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, ok := assets[filepath.Base(r.URL.Path)]
		if !ok {
			return nil, errors.New("unexpected fixture request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
	})}
	return GitHubSource{Client: client}, pin
}

func managedFixture(t *testing.T) (*LinuxInstallation, Pin) {
	t.Helper()
	data := privateState(t)
	old := filepath.Join(data, "releases/v0.51.5")
	bundleFixture(t, old, "0.51.5", nil)
	if e := os.Symlink("releases/v0.51.5", filepath.Join(data, "current")); e != nil {
		t.Fatal(e)
	}
	source, pin := fixtureSource(t, "0.51.9", nil)
	c := Config{Installed: "0.51.5", StatePath: privateState(t)}
	if e := c.Save(Status{State: "available", Installed: c.Installed, Available: "0.51.9", Pin: &pin}); e != nil {
		t.Fatal(e)
	}
	return &LinuxInstallation{Config: c, RuntimeRoot: old, DataRoot: data, Socket: filepath.Join(privateState(t), "absent.sock"), Source: source}, pin
}

type fileInstallation struct {
	*LinuxInstallation
	failStartup bool
	started     []bool
}

func (f *fileInstallation) Start(_ context.Context, _ Transaction, rollback bool) error {
	f.started = append(f.started, rollback)
	return nil
}
func (f *fileInstallation) Ready(_ context.Context, t Transaction, rollback bool) error {
	selection, e := os.Readlink(filepath.Join(f.DataRoot, "current"))
	if e != nil {
		return e
	}
	want := "releases/" + t.Pin.Tag
	if rollback {
		want = t.OldCurrent
	}
	if selection != want {
		return errors.New("wrong installed runtime")
	}
	if f.failStartup && !rollback {
		return errors.New("new frontend failed")
	}
	return nil
}

func TestVerifiedFileInstallationAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			l, _ := managedFixture(t)
			f := &fileInstallation{LinuxInstallation: l, failStartup: fail}
			engine := Engine{Config: l.Config, Installation: f}
			settings := []byte(`{"units":"C","location":"London"}`)
			file := filepath.Join(l.Config.StatePath, "saved-settings")
			if e := os.WriteFile(file, settings, 0600); e != nil {
				t.Fatal(e)
			}
			err := engine.Run(context.Background())
			if (err != nil) != fail {
				t.Fatal("wrong result", err)
			}
			selection, e := os.Readlink(filepath.Join(l.DataRoot, "current"))
			if e != nil {
				t.Fatal(e)
			}
			want := "releases/v0.51.9"
			if fail {
				want = "releases/v0.51.5"
			}
			if selection != want {
				t.Fatal(selection, want)
			}
			if e = release.Verify(filepath.Join(l.DataRoot, "releases/v0.51.5")); e != nil {
				t.Fatal("old installation not retained", e)
			}
			raw, e := os.ReadFile(file)
			if e != nil || !bytes.Equal(raw, settings) {
				t.Fatal("settings changed", e)
			}
			if fail && l.Config.Status().State != "rolled_back" {
				t.Fatal(l.Config.Status())
			}
		})
	}
}

func TestRecoveryWorksWithDamagedNewBundleAndNewWorkerVersion(t *testing.T) {
	l, pin := managedFixture(t)
	f := &fileInstallation{LinuxInstallation: l}
	engine := Engine{Config: l.Config, Installation: f}
	unlock, e := l.LockInstallation()
	if e != nil {
		t.Fatal(e)
	}
	j, e := l.Prepare(context.Background(), pin, nil)
	if e != nil {
		t.Fatal(e)
	}
	j.Phase = "activated"
	if e = l.Activate(context.Background(), j); e != nil {
		t.Fatal(e)
	}
	unlock()
	if e = engine.saveJournal(j); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(j.NewRuntime, "a-weather-app")); e != nil {
		t.Fatal(e)
	}
	engine.Config.Installed = "0.51.9"
	if e = engine.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if l.Config.Status().State != "rolled_back" {
		t.Fatal("rollback reported wrong installed version", l.Config.Status())
	}
	selection, _ := os.Readlink(filepath.Join(l.DataRoot, "current"))
	if selection != j.OldCurrent {
		t.Fatal(selection)
	}
}

func TestRuntimeSelectionChangeIsPreserved(t *testing.T) {
	l, pin := managedFixture(t)
	unlock, e := l.LockInstallation()
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	j, e := l.Prepare(context.Background(), pin, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.switchRuntime("releases/v0.51.9"); e != nil {
		t.Fatal(e)
	}
	if e = l.Activate(context.Background(), j); e == nil {
		t.Fatal("overwrote concurrently changed selection")
	}
}

func TestLinkedDevelopmentPluginRefusedBeforeDownload(t *testing.T) {
	l, pin := managedFixture(t)
	link := filepath.Join(privateState(t), "plugin")
	if e := os.Symlink(privateState(t), link); e != nil {
		t.Fatal(e)
	}
	l.PluginRoot = link
	unlock, e := l.LockInstallation()
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	if _, e = l.Prepare(context.Background(), pin, nil); e == nil || !strings.Contains(e.Error(), "locally") {
		t.Fatal(e)
	}
}
