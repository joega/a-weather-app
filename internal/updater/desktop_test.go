package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
)

// This fixture uses the real packaged Go service and Qt frontend, with private
// state and release trees. Release HTTP responses are injected; no host install,
// weather location, desktop effects, shell restart or external network is used.
func TestDesktopUpdateRestartsRealQtFrontend(t *testing.T) {
	testDesktopUpdate(t, false)
}

func TestStandaloneUpdateRestartsRealQtFrontend(t *testing.T) {
	testDesktopUpdate(t, true)
}

func testDesktopUpdate(t *testing.T, standalone bool) {
	qt, err := os.ReadFile(filepath.Join("..", "..", "native", "qt", "a-weather-app-qt"))
	if err != nil {
		t.Skip("build the native Qt frontend with make qt for the isolated restart test")
	}
	root, err := os.MkdirTemp("/tmp", "awu-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
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
	source, pin := fixtureSource(t, "0.51.9", map[string][]byte{"a-weather-app": newBinary, "native/qt/a-weather-app-qt": qt})
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	engine := Engine{Config: c, Installation: l}
	if err = engine.Run(ctx); err != nil {
		raw, _ := os.ReadFile(log.Name())
		t.Fatalf("update and restart: %v\n%s", err, raw)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("old app did not exit")
	}
	current, err := os.Readlink(filepath.Join(data, "current"))
	if err != nil || current != "releases/v0.51.9" {
		t.Fatal("new runtime not selected", current, err)
	}
	installed := c
	installed.Installed = "0.51.9"
	if status := installed.Status(); status.State != "updated" || status.Installed != "0.51.9" {
		t.Fatal("new running version not reported", status)
	}
	if err = l.Ready(ctx, tx, false); err != nil {
		t.Fatal("new Qt frontend did not remain connected", err)
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
	controls, _ := snapshot["controls"].(map[string]any)
	if controls["units"] != "C" || controls["wind_units"] != "km/h" || controls["reduced_motion"] != true {
		t.Fatal("new runtime did not adopt saved controls", controls)
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
