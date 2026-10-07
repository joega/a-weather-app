package updater

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapUsesInstalledVersionInsteadOfStagedHelper(t *testing.T) {
	l, _ := managedFixture(t)
	l.Config.Installed = "0.51.9"
	l.RuntimeRoot = filepath.Join(l.DataRoot, "releases/v0.51.9")
	c, e := l.Bootstrap()
	if e != nil {
		t.Fatal(e)
	}
	if c.Installed != "0.51.5" || l.RuntimeRoot != filepath.Join(l.DataRoot, "releases/v0.51.5") {
		t.Fatal("bootstrap treated helper as installed runtime", c, l.RuntimeRoot)
	}
	if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != "releases/v0.51.5" {
		t.Fatal("bootstrap switched runtime before transaction", got, e)
	}
	f := &fileInstallation{LinuxInstallation: l}
	if e = (Engine{Config: c, Installation: f}).Run(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != "releases/v0.51.9" {
		t.Fatal("bootstrap did not update runtime", got, e)
	}
}

func TestBootstrapRejectsDevelopmentAndUnrelatedSelections(t *testing.T) {
	for _, kind := range []string{"development", "unrelated", "damaged"} {
		t.Run(kind, func(t *testing.T) {
			l, _ := managedFixture(t)
			switch kind {
			case "development":
				l.Config.Development = true
			case "unrelated":
				os.Remove(filepath.Join(l.DataRoot, "current"))
				os.Symlink("../unrelated", filepath.Join(l.DataRoot, "current"))
			case "damaged":
				os.Remove(filepath.Join(l.RuntimeRoot, "a-weather-app"))
			}
			if _, e := l.Bootstrap(); e == nil {
				t.Fatal("unsafe bootstrap accepted")
			}
		})
	}
}
