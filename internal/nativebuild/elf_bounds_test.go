package nativebuild

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/joega/a-weather-app/internal/elfsafe"
	"github.com/joega/a-weather-app/internal/elfsafe/elftest"
)

func TestBuildAndRuntimeShareELFPreflight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	raw := elftest.Fixture(binary.LittleEndian, required, true)
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	file, e := openELF(path, elfsafe.MaxInput)
	if e != nil {
		t.Fatal(e)
	}
	if found, e := symbols(file); e != nil || len(found) != len(required) {
		t.Fatal(found, e)
	}
	file.Close()
	for name, raw := range elftest.Malformed() {
		t.Run(name, func(t *testing.T) {
			if e := os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e := openELF(path, elfsafe.MaxInput); !errors.Is(e, elfsafe.ErrUnsafe) {
				t.Fatal("release checker bypassed shared preflight", e)
			}
			if e := Hardening(path); !errors.Is(e, elfsafe.ErrUnsafe) {
				t.Fatal("hardening checker bypassed shared preflight", e)
			}
		})
	}
}
