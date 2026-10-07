package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultCommandFollowsUpdateAndRollback(t *testing.T) {
	for _, custom := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "custom"}[custom], func(t *testing.T) {
			l, _ := managedFixture(t)
			l.BinRoot = privateState(t)
			original := l.RuntimeRoot
			if custom {
				original = privateState(t)
			}
			file := filepath.Join(l.BinRoot, "a-weather-app")
			if e := os.Symlink(filepath.Join(original, "a-weather-app"), file); e != nil {
				t.Fatal(e)
			}
			unlock, e := l.LockInstallation()
			if e != nil {
				t.Fatal(e)
			}
			defer unlock()
			bundleFixture(t, filepath.Join(l.DataRoot, "releases/v0.51.9"), "0.51.9", nil)
			if e = l.switchRuntime("releases/v0.51.9"); e != nil {
				t.Fatal(e)
			}
			if e = l.migrateBinLink(Transaction{OldRuntime: l.RuntimeRoot}); e != nil {
				t.Fatal(e)
			}
			if custom {
				if got, e := os.Readlink(file); e != nil || got != filepath.Join(original, "a-weather-app") {
					t.Fatal("custom command changed", got, e)
				}
				return
			}
			if got, e := filepath.EvalSymlinks(file); e != nil || got != filepath.Join(l.DataRoot, "releases/v0.51.9/a-weather-app") {
				t.Fatal("command missed update", got, e)
			}
			if e = l.switchRuntime("releases/v0.51.5"); e != nil {
				t.Fatal(e)
			}
			if got, e := filepath.EvalSymlinks(file); e != nil || got != filepath.Join(l.RuntimeRoot, "a-weather-app") {
				t.Fatal("command missed rollback", got, e)
			}
		})
	}
}
