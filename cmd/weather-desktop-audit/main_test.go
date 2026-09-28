package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

func TestRequiresExplicitNativeActivation(t *testing.T) {
	for _, args := range [][]string{nil, {"--root", "/nonexistent"}, {"--activate-native"}, {"--root", "relative", "--activate-native"}} {
		if e := run(args); e == nil || !strings.Contains(e.Error(), "require explicit") {
			t.Fatal(args, e)
		}
	}
}

func TestOwnedArgumentPairRequiresExactAdjacentValues(t *testing.T) {
	args := []string{"app", "--service", "--state-dir", "/private/audit-state"}
	if !hasPair(args, "--state-dir", "/private/audit-state") || hasPair(args, "--state-dir", "/private/audit") || hasPair(args, "--root", "/private/audit-state") {
		t.Fatal("ambiguous process ownership match")
	}
}

func TestCanceledWaitReturnsImmediately(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := wait(ctx, time.Hour); e != context.Canceled {
		t.Fatal(e)
	}
}

func TestEvidenceIsPrivateAndBoundedWithoutActivation(t *testing.T) {
	path := t.TempDir()
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	directory, e := safeio.OpenDir(path, false)
	if e != nil {
		t.Fatal(e)
	}
	defer directory.Close()
	a := audit{evidence: directory}
	if e = a.record("assertion", object{"safe": true}); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(path, "0001-assertion.json"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("evidence mode", info, e)
	}
	a.bytes = evidenceLimit
	if e = a.record("assertion", object{"safe": true}); e == nil {
		t.Fatal("accepted excess evidence bytes")
	}
	a.bytes = 0
	a.sequence = 512
	if e = a.record("assertion", object{"safe": true}); e == nil {
		t.Fatal("accepted excess evidence records")
	}
	entries, e := os.ReadDir(path)
	if e != nil || len(entries) != 1 {
		t.Fatal("failed admission wrote evidence", len(entries), e)
	}
}
