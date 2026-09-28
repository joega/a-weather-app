package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateOutsideContainerDoesNotWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	source := t.TempDir()
	e := run([]string{"create", "--root", root, "--source", source})
	if e == nil || !strings.Contains(e.Error(), "scripts/run_go_migration_build.sh") {
		t.Fatal("missing production guard", e)
	}
	if _, e := os.Lstat(root); !os.IsNotExist(e) {
		t.Fatal("refused command created package files", e)
	}
}
