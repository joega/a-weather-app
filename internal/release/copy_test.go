package release

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyVerifiedPreservesOriginal(t *testing.T) {
	root, _ := fixture(t)
	destination := filepath.Join(t.TempDir(), "imported")
	before, e := os.ReadFile(filepath.Join(root, "a-weather-app"))
	if e != nil {
		t.Fatal(e)
	}
	if e = CopyVerified(root, destination); e != nil {
		t.Fatal(e)
	}
	if e = Verify(destination); e != nil {
		t.Fatal(e)
	}
	if e = CopyVerified(root, destination); e == nil {
		t.Fatal("existing import was overwritten")
	}
	after, e := os.ReadFile(filepath.Join(root, "a-weather-app"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("original changed", e)
	}
	if e = os.WriteFile(filepath.Join(destination, "a-weather-app"), []byte("changed copy"), 0700); e != nil {
		t.Fatal(e)
	}
	after, e = os.ReadFile(filepath.Join(root, "a-weather-app"))
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("copy shares mutable original bytes", e)
	}
}

func TestCopyVerifiedRejectsChangedSourceAndUnsafeDestination(t *testing.T) {
	root, _ := fixture(t)
	if e := os.WriteFile(filepath.Join(root, "user-notes"), []byte("keep my changes"), 0600); e != nil {
		t.Fatal(e)
	}
	destination := filepath.Join(t.TempDir(), "imported")
	if e := CopyVerified(root, destination); e == nil {
		t.Fatal("modified source accepted")
	}
	if _, e := os.Stat(destination); !os.IsNotExist(e) {
		t.Fatal("invalid source produced an import", e)
	}
	root, _ = fixture(t)
	base := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked")
	if e := os.Symlink(base, link); e != nil {
		t.Fatal(e)
	}
	if e := CopyVerified(root, filepath.Join(link, "imported")); e == nil {
		t.Fatal("symlink destination accepted")
	}
}
