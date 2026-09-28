package safeio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictJSON(t *testing.T) {
	for _, raw := range []string{`{"a":1,"\u0061":2}`, `{"a":{"x":1,"x":2}}`, `{"v":1e999}`, `{} {}`, "{\"x\":\"\xff\"}", strings.Repeat("[", 18) + strings.Repeat("]", 18)} {
		if _, e := Decode([]byte(raw), 4096); e == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, e := Object([]byte(`{"a":{"x":1},"b":{"x":2}}`), 100); e != nil {
		t.Fatal(e)
	}
}
func TestStateBoundaries(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	d, e := OpenDir(root, false)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	target := filepath.Join(root, "target")
	os.WriteFile(target, []byte("untouched"), 0600)
	os.Symlink(target, filepath.Join(root, "file"))
	if _, e = d.Read("file", 100); e == nil {
		t.Fatal("followed symlink")
	}
	if e = d.Write("file", map[string]any{"n": 1}, 100); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(target)
	if string(raw) != "untouched" {
		t.Fatal("changed target")
	}
	os.Link(target, filepath.Join(root, "hard"))
	if _, e = d.Read("hard", 100); e == nil {
		t.Fatal("read hard link")
	}
	if e = d.Write("file", map[string]any{"x": strings.Repeat("x", 100)}, 10); e == nil {
		t.Fatal("oversized write")
	}
	v, e := d.Read("file", 100)
	if e != nil || v["n"] != float64(1) {
		t.Fatal(v, e)
	}
}
func TestDirectoryTraversal(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	os.Symlink(root, filepath.Join(root, "alias"))
	if d, e := OpenDir(filepath.Join(root, "alias"), false); e == nil {
		d.Close()
		t.Fatal("followed directory symlink")
	}
}

func TestWritableAncestorRefused(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if e := os.Mkdir(child, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(root, 0777); e != nil {
		t.Fatal(e)
	}
	if d, e := OpenDir(child, false); e == nil {
		d.Close()
		t.Fatal("accepted writable ancestor")
	}
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	if d, e := OpenDir(child, false); e != nil {
		t.Fatal(e)
	} else {
		d.Close()
	}
}
