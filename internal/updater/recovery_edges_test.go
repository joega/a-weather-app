package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRollbackReopensAppWhenShellRestartFails(t *testing.T) {
	l, pin := managedFixture(t)
	marker := filepath.Join(privateState(t), "reopened")
	t.Setenv("WEATHER_TEST_REOPEN", marker)
	bundleFixture(t, l.RuntimeRoot, "0.51.5", map[string][]byte{"a-weather-app": []byte("#!/usr/bin/bash\nprintf reopened > \"$WEATHER_TEST_REOPEN\"\n")})
	l.PluginRoot = filepath.Join(privateState(t), "missing-plugin")
	l.command = func(context.Context, string, ...string) (string, error) {
		return "", errors.New("shell restart failed")
	}
	tx := Transaction{OldRuntime: l.RuntimeRoot, OldVersion: "0.51.5", OldCurrent: "releases/v0.51.5", NewRuntime: filepath.Join(l.DataRoot, "releases/v0.51.9"), PluginRoot: l.PluginRoot, OldCommit: strings.Repeat("b", 40), NewCommit: strings.Repeat("c", 40), Pin: pin}
	if e := l.Start(context.Background(), tx, true); e != nil {
		t.Fatal("bar failure prevented app recovery", e)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if raw, e := os.ReadFile(marker); e == nil && string(raw) == "reopened" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("previous app did not restart")
}

func TestRecoveryPreservesExternalRuntimeSelection(t *testing.T) {
	l, pin := managedFixture(t)
	unlock, e := l.LockInstallation()
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	tx, e := l.Prepare(context.Background(), pin, nil)
	if e != nil {
		t.Fatal(e)
	}
	bundleFixture(t, filepath.Join(l.DataRoot, "releases/v0.51.8"), "0.51.8", nil)
	if e = l.switchRuntime("releases/v0.51.8"); e != nil {
		t.Fatal(e)
	}
	if e = l.Restore(context.Background(), tx); e == nil {
		t.Fatal("recovery overwrote user-selected runtime")
	}
	if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != "releases/v0.51.8" {
		t.Fatal("external selection changed", got, e)
	}
}
