package updater

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLauncherMigrationFollowsUpdateAndRollback(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "generated", true: "custom"}[custom], func(t *testing.T) {
			base := privateState(t)
			data := filepath.Join(base, "a-weather-app")
			old := filepath.Join(data, "releases/v0.51.5")
			next := filepath.Join(data, "releases/v0.51.9")
			template := []byte("[Desktop Entry]\nExec=a-weather-app\nTryExec=a-weather-app\nIcon=a-weather-app\n")
			bundleFixture(t, old, "0.51.5", map[string][]byte{"packaging/a-weather-app.desktop": template})
			bundleFixture(t, next, "0.51.9", nil)
			if e := os.Symlink("releases/v0.51.5", filepath.Join(data, "current")); e != nil {
				t.Fatal(e)
			}
			apps := filepath.Join(base, "applications")
			if e := os.Mkdir(apps, 0700); e != nil {
				t.Fatal(e)
			}
			file := filepath.Join(apps, "a-weather-app.desktop")
			raw := launcherContents(template, filepath.Join(old, "a-weather-app"), filepath.Join(base, "icons/hicolor/scalable/apps/a-weather-app.svg"))
			if custom {
				raw = append(raw, []byte("# my launcher customization\n")...)
			}
			if e := os.WriteFile(file, raw, 0644); e != nil {
				t.Fatal(e)
			}
			l := &LinuxInstallation{DataRoot: data}
			unlock, e := l.LockInstallation()
			if e != nil {
				t.Fatal(e)
			}
			defer unlock()
			if e = l.switchRuntime("releases/v0.51.9"); e != nil {
				t.Fatal(e)
			}
			if e = l.migrateLauncher(Transaction{OldRuntime: old}); e != nil {
				t.Fatal(e)
			}
			got, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			if custom {
				if !bytes.Equal(got, raw) {
					t.Fatal("custom launcher changed")
				}
				return
			}
			stable := filepath.Join(data, "current/a-weather-app")
			if !strings.Contains(string(got), "Exec=\""+stable+"\"") {
				t.Fatal("launcher does not follow current release", string(got))
			}
			resolved, e := filepath.EvalSymlinks(stable)
			if e != nil || resolved != filepath.Join(next, "a-weather-app") {
				t.Fatal("launcher missed update", resolved, e)
			}
			if e = l.switchRuntime("releases/v0.51.5"); e != nil {
				t.Fatal(e)
			}
			resolved, e = filepath.EvalSymlinks(stable)
			if e != nil || resolved != filepath.Join(old, "a-weather-app") {
				t.Fatal("launcher missed rollback", resolved, e)
			}
		})
	}
}

func TestStandaloneGeneratedLauncherMovesToManagedSelector(t *testing.T) {
	base := privateState(t)
	data := filepath.Join(base, "a-weather-app")
	old := filepath.Join(data, "releases/v0.51.5")
	original := filepath.Join(privateState(t), "downloaded app")
	template := []byte("[Desktop Entry]\nExec=a-weather-app\nTryExec=a-weather-app\nIcon=a-weather-app\n")
	bundleFixture(t, old, "0.51.5", map[string][]byte{"packaging/a-weather-app.desktop": template})
	bundleFixture(t, original, "0.51.5", map[string][]byte{"packaging/a-weather-app.desktop": template})
	apps := filepath.Join(base, "applications")
	if e := os.Mkdir(apps, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(apps, "a-weather-app.desktop")
	raw := launcherContents(template, filepath.Join(original, "a-weather-app"), filepath.Join(base, "icons/hicolor/scalable/apps/a-weather-app.svg"))
	if e := os.WriteFile(file, raw, 0644); e != nil {
		t.Fatal(e)
	}
	l := &LinuxInstallation{DataRoot: data}
	if e := l.migrateLauncher(Transaction{OldRuntime: old, RunningRoot: original}); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(file)
	if e != nil || !strings.Contains(string(got), "Exec=\""+filepath.Join(data, "current/a-weather-app")+"\"") {
		t.Fatal("standalone menu still points at download", string(got), e)
	}
}
