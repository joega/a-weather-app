package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const launcherTemplate = "[Desktop Entry]\nType=Application\nName=Weather\nExec=a-weather-app\nTryExec=a-weather-app\nIcon=a-weather-app\n"

func launcherFixture(t *testing.T) (root, data, running string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "weather app")
	data = filepath.Join(base, "user data")
	for _, name := range []string{filepath.Join(root, "packaging/icons"), data} {
		if e := os.MkdirAll(name, 0755); e != nil {
			t.Fatal(e)
		}
	}
	launcherWrite(t, filepath.Join(root, "packaging/a-weather-app.desktop"), []byte(launcherTemplate), 0644)
	launcherWrite(t, filepath.Join(root, "packaging/icons/a-weather-app.svg"), []byte("<svg>fixture</svg>"), 0644)
	running = filepath.Join(root, "a-weather-app")
	launcherWrite(t, running, []byte("compiled fixture"), 0755)
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("PATH", root)
	return
}
func launcherWrite(t *testing.T, path string, raw []byte, mode os.FileMode) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, raw, mode); e != nil {
		t.Fatal(e)
	}
}
func launcherBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}

func TestLauncherRepeatableQuotedExecutable(t *testing.T) {
	root, data, running := launcherFixture(t)
	for i := 0; i < 2; i++ {
		if got := installLauncher(root, running); got != "installed" {
			t.Fatal(got)
		}
	}
	raw := launcherBytes(t, filepath.Join(data, "applications/a-weather-app.desktop"))
	if !bytes.Contains(raw, []byte("Exec=\""+running+"\"\n")) || bytes.Contains(raw, []byte("TryExec=")) {
		t.Fatal(string(raw))
	}
	if !bytes.Contains(raw, []byte("Icon="+filepath.Join(data, "icons/hicolor/scalable/apps/a-weather-app.svg")+"\n")) {
		t.Fatal(string(raw))
	}
	info, e := os.Stat(filepath.Join(data, "applications/a-weather-app.desktop"))
	if e != nil || info.Mode().Perm() != 0644 {
		t.Fatal(info, e)
	}
}

func TestLauncherDevelopmentTargetsCompiledGo(t *testing.T) {
	root, data, _ := launcherFixture(t)
	running := filepath.Join(root, "build/a-weather-app")
	launcherWrite(t, running, []byte("compiled fixture"), 0755)
	if got := installLauncher(root, running); got != "installed" {
		t.Fatal(got)
	}
	if !strings.Contains(string(launcherBytes(t, filepath.Join(data, "applications/a-weather-app.desktop"))), "Exec=\""+running+"\"") {
		t.Fatal("development entry did not use Go binary")
	}
}

func TestLauncherConflictPreserved(t *testing.T) {
	root, data, running := launcherFixture(t)
	desktop := filepath.Join(data, "applications/a-weather-app.desktop")
	original := []byte("unrelated user launcher")
	launcherWrite(t, desktop, original, 0644)
	if got := installLauncher(root, running); got != "conflict" {
		t.Fatal(got)
	}
	if !bytes.Equal(launcherBytes(t, desktop), original) {
		t.Fatal("conflict overwritten")
	}
}

func TestLauncherSymlinkIconUnchanged(t *testing.T) {
	root, data, running := launcherFixture(t)
	target := filepath.Join(data, "unrelated")
	launcherWrite(t, target, []byte("preserve me"), 0644)
	icon := filepath.Join(data, "icons/hicolor/scalable/apps/a-weather-app.svg")
	if e := os.MkdirAll(filepath.Dir(icon), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(target, icon); e != nil {
		t.Fatal(e)
	}
	if got := installLauncher(root, running); got != "failed" {
		t.Fatal(got)
	}
	if info, e := os.Lstat(icon); e != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal(info, e)
	}
	if string(launcherBytes(t, target)) != "preserve me" {
		t.Fatal("symlink target modified")
	}
	if _, e := os.Lstat(filepath.Join(data, "applications/a-weather-app.desktop")); !os.IsNotExist(e) {
		t.Fatal("published after unsafe icon", e)
	}
}

func TestLauncherRefusesUnsupportedDataPaths(t *testing.T) {
	root, _, running := launcherFixture(t)
	for _, data := range []string{"relative/weather", filepath.Join(t.TempDir(), "weather%f"), filepath.Join(t.TempDir(), "weather\nname"), filepath.Join(t.TempDir(), "weather=bad")} {
		t.Setenv("XDG_DATA_HOME", data)
		got := installLauncher(root, running)
		if got != "unsupported_path" && got != "failed" {
			t.Fatal(data, got)
		}
		if filepath.IsAbs(data) {
			if _, e := os.Lstat(data); !os.IsNotExist(e) {
				t.Fatal("unsafe data path created", e)
			}
		}
	}
}

func TestLauncherManualTemplateRequiresSameRunningExecutable(t *testing.T) {
	for _, kind := range []string{"same", "symlink_same", "unrelated", "not_running", "development_old_script"} {
		t.Run(kind, func(t *testing.T) {
			root, data, running := launcherFixture(t)
			desktop := filepath.Join(data, "applications/a-weather-app.desktop")
			launcherWrite(t, desktop, []byte(launcherTemplate), 0644)
			switch kind {
			case "symlink_same":
				bin := filepath.Join(t.TempDir(), "bin")
				os.MkdirAll(bin, 0755)
				if e := os.Symlink(running, filepath.Join(bin, "a-weather-app")); e != nil {
					t.Fatal(e)
				}
				t.Setenv("PATH", bin)
			case "unrelated":
				bin := t.TempDir()
				launcherWrite(t, filepath.Join(bin, "a-weather-app"), []byte("foreign"), 0755)
				t.Setenv("PATH", bin)
			case "not_running":
				running = filepath.Join(root, "different")
			case "development_old_script":
				running = filepath.Join(root, "build/a-weather-app")
				launcherWrite(t, running, []byte("compiled fixture"), 0755)
			}
			want := "conflict"
			if kind == "same" || kind == "symlink_same" {
				want = "installed"
			}
			if got := installLauncher(root, running); got != want {
				t.Fatal(got, want)
			}
			if string(launcherBytes(t, desktop)) != launcherTemplate {
				t.Fatal("manual template replaced")
			}
		})
	}
}

func TestLauncherHardlinkFIFOAndWritableFilesRefused(t *testing.T) {
	for _, kind := range []string{"hardlink", "fifo", "writable"} {
		t.Run(kind, func(t *testing.T) {
			root, data, running := launcherFixture(t)
			desktop := filepath.Join(data, "applications/a-weather-app.desktop")
			os.MkdirAll(filepath.Dir(desktop), 0755)
			switch kind {
			case "hardlink":
				target := filepath.Join(data, "original")
				launcherWrite(t, target, []byte(launcherTemplate), 0644)
				if e := os.Link(target, desktop); e != nil {
					t.Fatal(e)
				}
			case "fifo":
				if e := syscall.Mkfifo(desktop, 0600); e != nil {
					t.Fatal(e)
				}
			case "writable":
				launcherWrite(t, desktop, []byte(launcherTemplate), 0644)
				if e := os.Chmod(desktop, 0666); e != nil {
					t.Fatal(e)
				}
			}
			before, e := os.Lstat(desktop)
			if e != nil {
				t.Fatal(e)
			}
			if got := installLauncher(root, running); got != "conflict" {
				t.Fatal(got)
			}
			after, e := os.Lstat(desktop)
			if e != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("unsafe file replaced", e)
			}
		})
	}
}

func TestLauncherAncestorPolicyAndSymlinks(t *testing.T) {
	for _, tc := range []struct {
		uid, mode uint32
		want      bool
	}{{0, 01777, true}, {1000, 0755, true}, {0, 0755, true}, {1000, 01777, false}, {1001, 01777, false}, {1001, 0755, false}, {0, 0777, false}, {1000, 0775, false}} {
		if got := publicAncestorSafe(syscall.Stat_t{Uid: tc.uid, Mode: tc.mode}, 0, 1000); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	for _, kind := range []string{"writable", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, data, running := launcherFixture(t)
			if kind == "writable" {
				if e := os.Chmod(data, 0777); e != nil {
					t.Fatal(e)
				}
				defer os.Chmod(data, 0755)
			} else {
				target := filepath.Join(t.TempDir(), "preserved")
				os.MkdirAll(target, 0755)
				if e := os.Symlink(target, filepath.Join(data, "icons")); e != nil {
					t.Fatal(e)
				}
			}
			if got := installLauncher(root, running); got != "failed" {
				t.Fatal(got)
			}
			if _, e := os.Lstat(filepath.Join(data, "applications/a-weather-app.desktop")); !os.IsNotExist(e) {
				t.Fatal("published through unsafe ancestor", e)
			}
		})
	}
}
