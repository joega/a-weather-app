package effects

import (
	"encoding/binary"
	"errors"
	"testing"

	"github.com/joega/a-weather-app/internal/elfsafe"
	"github.com/joega/a-weather-app/internal/elfsafe/elftest"
)

func TestRuntimeSymbolsUseBoundedELFParser(t *testing.T) {
	plugin := elftest.Fixture(binary.LittleEndian, requiredSymbols, false)
	host := elftest.Fixture(binary.LittleEndian, requiredSymbols, true)
	if e := symbolCheck(plugin, host); e != nil {
		t.Fatal("valid symbol fixture rejected", e)
	}
	for name, raw := range elftest.Malformed() {
		t.Run(name, func(t *testing.T) {
			if e := symbolCheck(raw, host); !errors.Is(e, elfsafe.ErrUnsafe) {
				t.Fatal("runtime plugin bypassed ELF preflight", e)
			}
			if e := symbolCheck(plugin, raw); !errors.Is(e, elfsafe.ErrUnsafe) {
				t.Fatal("runtime compositor bypassed ELF preflight", e)
			}
		})
	}
}
