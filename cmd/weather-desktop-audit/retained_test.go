package main

import (
	"os"
	"path/filepath"
	"testing"
)

func retainedFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(path, "policy.json"), []byte("original"), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestCrashDiagnosticsRemainUnchangedAndAreNeverDeleted(t *testing.T) {
	path := retainedFixture(t)
	evidence, e := retainedSnapshot(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyRetained(path, evidence); e != nil {
		t.Fatal("unchanged diagnostics rejected", e)
	}
	if e = os.WriteFile(filepath.Join(path, "policy.json"), []byte("modified"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = verifyRetained(path, evidence); e == nil {
		t.Fatal("changed diagnostic accepted")
	}
	if _, e = os.Stat(filepath.Join(path, "policy.json")); e != nil {
		t.Fatal("diagnostic was removed", e)
	}
}

func TestRetainedDiagnosticsRejectUnknownSymlinkAndOversizedEntries(t *testing.T) {
	for _, kind := range []string{"unknown", "symlink", "oversized", "hardlink", "directory replacement"} {
		t.Run(kind, func(t *testing.T) {
			path := retainedFixture(t)
			expected, e := retainedSnapshot(path)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "unknown":
				e = os.WriteFile(filepath.Join(path, "foreign.txt"), []byte("foreign"), 0600)
			case "symlink":
				e = os.Symlink(filepath.Join(path, "policy.json"), filepath.Join(path, "selected.json"))
			case "oversized":
				e = os.WriteFile(filepath.Join(path, "selected.json"), make([]byte, 1048577), 0600)
			case "hardlink":
				e = os.Link(filepath.Join(path, "policy.json"), filepath.Join(path, "selected.json"))
			case "directory replacement":
				e = os.Rename(path, path+"-retained-original")
				if e == nil {
					e = os.Mkdir(path, 0700)
				}
				if e == nil {
					e = os.WriteFile(filepath.Join(path, "policy.json"), []byte("original"), 0600)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			if e = verifyRetained(path, expected); e == nil {
				t.Fatal("unsafe retained diagnostic accepted", kind)
			}
		})
	}
}
