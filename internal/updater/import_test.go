package updater

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/joega/a-weather-app/internal/release"
)

func TestStandaloneImportUpdatesAndRollsBackWithoutChangingOriginal(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "rollback"}[fail], func(t *testing.T) {
			l, _ := managedFixture(t)
			original := filepath.Join(privateState(t), "downloaded app")
			bundleFixture(t, original, "0.51.5", nil)
			before, e := os.ReadFile(filepath.Join(original, "packaging/runtime.json"))
			if e != nil {
				t.Fatal(e)
			}
			if e = os.RemoveAll(l.DataRoot); e != nil {
				t.Fatal(e)
			}
			l.RuntimeRoot = original
			f := &fileInstallation{LinuxInstallation: l, failStartup: fail}
			e = (Engine{Config: l.Config, Installation: f}).Run(context.Background())
			if (e != nil) != fail {
				t.Fatal("unexpected update result", e)
			}
			want := "releases/v0.51.9"
			if fail {
				want = "releases/v0.51.5"
			}
			if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != want {
				t.Fatal("wrong selected release", got, e)
			}
			if e = release.Verify(original); e != nil {
				t.Fatal("original download changed", e)
			}
			after, e := os.ReadFile(filepath.Join(original, "packaging/runtime.json"))
			if e != nil || !bytes.Equal(before, after) {
				t.Fatal("original metadata changed", e)
			}
			if e = release.Verify(filepath.Join(l.DataRoot, "releases/v0.51.5")); e != nil {
				t.Fatal("previous version was not retained", e)
			}
		})
	}
}

func TestStandaloneImportRefusesExistingOtherVersion(t *testing.T) {
	l, _ := managedFixture(t)
	original := filepath.Join(privateState(t), "downloaded")
	bundleFixture(t, original, "0.51.4", nil)
	l.RuntimeRoot = original
	l.Config.Installed = "0.51.4"
	unlock, e := l.LockInstallation()
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	if _, _, _, e = l.importRuntime(); e == nil {
		t.Fatal("another installed version was replaced")
	}
	if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != "releases/v0.51.5" {
		t.Fatal("existing selection changed", got, e)
	}
}
