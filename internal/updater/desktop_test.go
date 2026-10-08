package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/app"
	"github.com/joega/a-weather-app/internal/ipc"
)

// This fixture uses the real packaged Go service and Qt frontend, with private
// state and release trees. Release HTTP responses are injected; no host install,
// weather location, desktop effects, shell restart or external network is used.
func TestDesktopUpdateRestartsRealQtFrontend(t *testing.T) {
	testDesktopUpdate(t, false, "")
}

func TestStandaloneUpdateRestartsRealQtFrontend(t *testing.T) {
	testDesktopUpdate(t, true, "")
}

func TestDetachedUpdateRejectsCorruptDownload(t *testing.T) {
	testDesktopUpdate(t, false, "verification")
}

func TestDetachedUpdateRollsBackFailedQtStartup(t *testing.T) {
	testDesktopUpdate(t, false, "startup")
}

func TestStagedBootstrapWorkerUpdatesOlderRuntime(t *testing.T) {
	testDesktopUpdate(t, false, "bootstrap")
}

func TestNormalLaunchRecoversInterruptedUpdate(t *testing.T) {
	testDesktopUpdate(t, false, "interrupted")
}

func testDesktopUpdate(t *testing.T, standalone bool, failure string) {
	qt, err := os.ReadFile(filepath.Join("..", "..", "native", "qt", "a-weather-app-qt"))
	if err != nil {
		t.Skip("build the native Qt frontend with make qt for the isolated restart test")
	}
	// Honor TMPDIR so the copied runtime bundles need not consume a quota-limited
	// tmpfs. Keep the prefix short for the fixture's nested Unix socket path.
	root, err := os.MkdirTemp("", "awu-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	t.Setenv("HOME", root)
	for _, key := range []string{"XDG_DATA_HOME", "XDG_CONFIG_HOME", "XDG_RUNTIME_DIR"} {
		dir := filepath.Join(root, strings.ToLower(key))
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, dir)
	}
	t.Setenv("QT_QPA_PLATFORM", "offscreen")
	t.Setenv("QT_QPA_PLATFORMTHEME", "generic")
	t.Setenv("QT_QUICK_CONTROLS_STYLE", "Basic")
	t.Setenv("QT_QUICK_BACKEND", "software")
	t.Setenv("QT_IM_MODULE", "none")
	t.Setenv("HYPRLAND_INSTANCE_SIGNATURE", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", "")
	state := filepath.Join(root, "state")
	if err = os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	data := DefaultDataRoot()
	oldRoot := filepath.Join(data, "releases/v0.51.5")
	if standalone {
		oldRoot = filepath.Join(root, "downloaded")
	}
	module, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	build := func(version string) []byte {
		t.Helper()
		file := filepath.Join(root, "app-"+version)
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", "-s -w -X main.buildMode=package -X main.appVersion="+version, "-o", file, "./cmd/a-weather-app")
		cmd.Dir = module
		if output, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("build fixture: %v\n%s", e, output)
		}
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	oldBinary := build("0.51.5")
	newBinary := build("0.51.9")
	bundleFixture(t, oldRoot, "0.51.5", map[string][]byte{"a-weather-app": oldBinary, "native/qt/a-weather-app-qt": qt})
	if !standalone {
		if err = os.Symlink("releases/v0.51.5", filepath.Join(data, "current")); err != nil {
			t.Fatal(err)
		}
	}
	newQt := qt
	if failure == "startup" {
		newQt = []byte("#!/usr/bin/bash\nexit 1\n")
	}
	source, pin := fixtureSource(t, "0.51.9", map[string][]byte{"a-weather-app": newBinary, "native/qt/a-weather-app-qt": newQt})
	if failure == "verification" {
		originalTransport := source.Client.Transport
		source.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			response, e := originalTransport.RoundTrip(r)
			if e == nil && strings.HasSuffix(r.URL.Path, ".tar") {
				response.Body.Close()
				response.Body = io.NopCloser(strings.NewReader("corrupt download"))
				response.ContentLength = int64(len("corrupt download"))
			}
			return response, e
		})
	}
	fixtureReleaseProxy(t, root, source, pin)
	locationBefore := []byte("{\"name\":\"Saved test location\",\"latitude\":42.0,\"longitude\":-71.0,\"timezone\":\"America/New_York\"}")
	if err = os.WriteFile(filepath.Join(state, "location.json"), locationBefore, 0600); err != nil {
		t.Fatal(err)
	}
	var savedLocation map[string]any
	if err = json.Unmarshal(locationBefore, &savedLocation); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	forecast := map[string]any{"schema_version": 1.0, "location": savedLocation, "fetched_at": now, "current": map[string]any{"time": now, "condition": "clear", "temperature_c": 15.0, "is_day": true}, "hourly": []any{}, "daily": []any{}, "alerts": map[string]any{"status": "not_supported_here", "coverage": "unsupported", "items": []any{}}}
	profile := map[string]any{"schema_version": 2.0, "mode": "place", "zip_code": nil, "country_code": "GB", "place": map[string]any{"provider": "open-meteo", "id": 12345.0}, "location": savedLocation, "forecast": forecast}
	if err = app.ValidateProfile(profile); err != nil {
		t.Fatal("invalid saved place fixture", err)
	}
	profileBefore, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(state, "location-profile.json"), profileBefore, 0600); err != nil {
		t.Fatal(err)
	}
	barConfig := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "omarchy/shell.json")
	if err = os.MkdirAll(filepath.Dir(barConfig), 0700); err != nil {
		t.Fatal(err)
	}
	barBefore := []byte("{\"fixture_bar_placement\":\"left\"}")
	if err = os.WriteFile(barConfig, barBefore, 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{Installed: "0.51.5", StatePath: state}
	if err = c.Save(Status{State: "available", Installed: c.Installed, Available: "0.51.9", Pin: &pin}); err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(state)))
	socket := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), fmt.Sprintf("a-weather-app-%d-%s", os.Geteuid(), hash[:16]), "service.sock")
	l := &LinuxInstallation{Config: c, RuntimeRoot: oldRoot, DataRoot: data, Socket: socket, Source: source}
	log, err := os.Create(filepath.Join(root, "old-app.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	old := exec.Command(filepath.Join(oldRoot, "a-weather-app"), "--state-dir", state, "--offline")
	old.Stdout = log
	old.Stderr = log
	if err = old.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- old.Wait() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx := Transaction{OldRuntime: filepath.Join(data, "releases/v0.51.5"), OldVersion: "0.51.5", OldCurrent: "releases/v0.51.5", NewRuntime: filepath.Join(data, "releases/v0.51.9"), Pin: pin}
		if standalone {
			tx.RunningRoot = oldRoot
		}
		_ = l.Stop(ctx, tx)
		_ = old.Process.Kill()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	tx := Transaction{OldRuntime: oldRoot, OldVersion: "0.51.5", OldCurrent: "releases/v0.51.5", NewRuntime: filepath.Join(data, "releases/v0.51.9"), Pin: pin}
	if err = l.Ready(ctx, tx, true); err != nil {
		raw, _ := os.ReadFile(log.Name())
		t.Fatalf("old frontend: %v\n%s", err, raw)
	}
	reply, err := ipc.Call(ctx, socket, ipc.M{"op": "set_controls", "controls": ipc.M{"units": "C", "wind_units": "km/h", "reduced_motion": true}})
	if err != nil || reply["ok"] != true {
		t.Fatal("could not save user controls before update", reply, err)
	}
	controlsBefore, err := os.ReadFile(filepath.Join(state, "controls.json"))
	if err != nil {
		t.Fatal(err)
	}
	if failure == "interrupted" {
		unlock, e := l.LockInstallation()
		if e != nil {
			t.Fatal(e)
		}
		transaction, e := l.Prepare(ctx, pin, nil)
		if e != nil {
			unlock()
			t.Fatal(e)
		}
		if e = l.Stop(ctx, transaction); e != nil {
			unlock()
			t.Fatal(e)
		}
		transaction.Phase = "switching"
		engine := Engine{Config: c, Installation: l}
		if e = engine.saveJournal(transaction); e != nil {
			unlock()
			t.Fatal(e)
		}
		if e = l.Activate(ctx, transaction); e != nil {
			unlock()
			t.Fatal(e)
		}
		transaction.Phase = "activated"
		if e = engine.saveJournal(transaction); e != nil {
			unlock()
			t.Fatal(e)
		}
		unlock()
		// No worker is alive. Reopening the selected release must launch its
		// detached recovery helper and restore the prior running application.
		if e = StartDetached(filepath.Join(transaction.NewRuntime, "a-weather-app"), []string{"--state-dir", state}, os.Environ()); e != nil {
			t.Fatal(e)
		}
	} else if failure == "bootstrap" {
		staged, e := source.Prepare(ctx, pin, data, nil)
		if e != nil {
			t.Fatal("could not stage bootstrap helper", e)
		}
		defer os.RemoveAll(filepath.Dir(staged))
		if e = StartDetached(filepath.Join(staged, "a-weather-app"), []string{"--update-worker", "--bootstrap-update", "--state-dir", state}, os.Environ()); e != nil {
			t.Fatal("bootstrap worker failed", e)
		}
	} else {
		reply, err = ipc.Call(ctx, socket, ipc.M{"op": "install_update"})
		if err != nil || reply["ok"] != true {
			t.Fatal("update action failed", reply, err)
		}
	}
	installed := c
	wantState, wantVersion := "updated", "0.51.9"
	if failure == "verification" {
		wantState, wantVersion = "failed", "0.51.5"
	}
	if failure == "startup" || failure == "interrupted" {
		wantState, wantVersion = "rolled_back", "0.51.5"
	}
	installed.Installed = wantVersion
	for installed.Status().State != wantState {
		if ctx.Err() != nil {
			raw, _ := os.ReadFile(log.Name())
			t.Fatal("detached update did not finish", c.Status(), ctx.Err(), string(raw))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if failure != "verification" {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("old app did not exit")
		}
	}
	current, err := os.Readlink(filepath.Join(data, "current"))
	if err != nil || current != "releases/v"+wantVersion {
		t.Fatal("wrong runtime selected", current, err)
	}
	if status := installed.Status(); status.State != wantState || status.Installed != wantVersion || status.Message == "" {
		t.Fatal("update result not reported", status)
	}
	if err = l.Ready(ctx, tx, failure == "startup" || failure == "verification" || failure == "interrupted"); err != nil {
		t.Fatal("expected runtime did not remain connected", err)
	}
	controlsAfter, err := os.ReadFile(filepath.Join(state, "controls.json"))
	if err != nil || !bytes.Equal(controlsBefore, controlsAfter) {
		t.Fatal("saved controls changed during update", err)
	}
	reply, err = ipc.Call(ctx, socket, ipc.M{"op": "snapshot"})
	if err != nil || reply["ok"] != true {
		t.Fatal("new app snapshot failed", err)
	}
	snapshot, _ := reply["snapshot"].(map[string]any)
	update, _ := snapshot["update"].(map[string]any)
	if reply["frontend_ready"] != true || update["installed"] != wantVersion {
		t.Fatal("expected Qt frontend or running version missing", reply)
	}
	controls, _ := snapshot["controls"].(map[string]any)
	if controls["units"] != "C" || controls["wind_units"] != "km/h" || controls["reduced_motion"] != true {
		t.Fatal("new runtime did not adopt saved controls", controls)
	}
	location, _ := snapshot["location"].(map[string]any)
	if location["name"] != "Saved test location" || location["latitude"] != 42.0 || location["longitude"] != -71.0 {
		t.Fatal("saved location was not adopted", location)
	}
	for file, want := range map[string][]byte{filepath.Join(state, "location.json"): locationBefore, filepath.Join(state, "location-profile.json"): profileBefore, barConfig: barBefore} {
		got, e := os.ReadFile(file)
		if e != nil || !bytes.Equal(got, want) {
			t.Fatal("saved location or bar placement changed", file, e)
		}
	}
}

func TestDetachedHelperChild(t *testing.T) {
	if os.Getenv("WEATHER_TEST_DETACH") != "1" {
		return
	}
	err := StartDetached("/usr/bin/sh", []string{"-c", `while [ ! -e "$1" ]; do sleep 0.05; done; printf survived > "$2"`, "sh", os.Getenv("WEATHER_TEST_GATE"), os.Getenv("WEATHER_TEST_MARKER")}, os.Environ())
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
func TestDetachedHelperSurvivesParentExit(t *testing.T) {
	dir := privateState(t)
	gate := filepath.Join(dir, "gate")
	marker := filepath.Join(dir, "marker")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDetachedHelperChild$")
	cmd.Env = append(os.Environ(), "WEATHER_TEST_DETACH=1", "WEATHER_TEST_GATE="+gate, "WEATHER_TEST_MARKER="+marker)
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("parent failed %v %s", e, output)
	}
	if e := os.WriteFile(gate, []byte("parent exited"), 0600); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		raw, e := os.ReadFile(marker)
		if e == nil && string(raw) == "survived" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("detached worker did not survive its parent")
}
