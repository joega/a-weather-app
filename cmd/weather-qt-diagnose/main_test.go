package main

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestPrivateEnvironment(t *testing.T) {
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE", "DBUS_SESSION_BUS_ADDRESS", "LD_PRELOAD", "QT_PLUGIN_PATH", "WEATHER_QT_MAP_CAPTURE"} {
		t.Setenv(key, "real-session")
	}
	env, err := privateEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"DISPLAY", "WAYLAND_DISPLAY", "HYPRLAND_INSTANCE_SIGNATURE", "DBUS_SESSION_BUS_ADDRESS", "LD_PRELOAD", "QT_PLUGIN_PATH", "WEATHER_QT_MAP_CAPTURE"} {
		if _, ok := env[key]; ok {
			t.Fatalf("inherited session setting %s", key)
		}
	}
	for _, key := range []string{"HOME", "XDG_RUNTIME_DIR", "XDG_STATE_HOME", "TMPDIR"} {
		info, err := os.Stat(env[key])
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("nonprivate directory %s: %v", key, err)
		}
	}
	if env["QT_QPA_PLATFORM"] != "offscreen" || env["QT_QUICK_BACKEND"] != "software" {
		t.Fatal("desktop backend inherited")
	}
}

func TestExecuteRetainsFailureAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		command []string
		timeout bool
	}{{"exit", []string{"/bin/sh", "-c", "printf diagnostic; exit 7"}, false}, {"deadline", []string{"/usr/bin/sleep", "30"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			folder := t.TempDir()
			env, err := privateEnv(folder)
			if err != nil {
				t.Fatal(err)
			}
			r, err := execute(tc.command, folder, env, filepath.Join(folder, "raw.log"), 1)
			if err != nil {
				t.Fatal(err)
			}
			if r.TimedOut != tc.timeout || r.ReturnCode == 0 || r.Seconds > 5 {
				t.Fatalf("lost failure/deadline: %+v", r)
			}
			for _, name := range []string{"raw.log", "raw.command.json", "raw.result.json"} {
				if _, err = os.Stat(filepath.Join(folder, name)); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestExecuteCleansAdoptedChildren(t *testing.T) {
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	folder := t.TempDir()
	env, err := privateEnv(folder)
	if err != nil {
		t.Fatal(err)
	}
	r, err := execute([]string{"/bin/sh", "-c", "sleep 30 &"}, folder, env, filepath.Join(folder, "raw.log"), 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.ReturnCode != 0 || len(r.Cleaned) == 0 {
		t.Fatalf("orphan cleanup missing: %+v", r)
	}
	pids, err := childPIDs()
	if err != nil || len(pids) != 0 {
		t.Fatalf("owned processes remain: %v %v", pids, err)
	}
}

func TestInterruptedExperimentCleansUp(t *testing.T) {
	previous := experimentContext
	ctx, cancel := context.WithCancel(context.Background())
	experimentContext = ctx
	defer func() { experimentContext = previous }()
	defer cancel()
	folder := t.TempDir()
	env, err := privateEnv(folder)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(100*time.Millisecond, cancel)
	r, err := execute([]string{"/usr/bin/sleep", "30"}, folder, env, filepath.Join(folder, "raw.log"), 3)
	if err != context.Canceled || !r.Interrupted || r.Seconds > 5 {
		t.Fatalf("interrupt did not terminate owned command: %+v %v", r, err)
	}
}
