package elfsafe

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/joega/a-weather-app/internal/elfsafe/elftest"
)

func TestMetadataRejectedBeforeDebugELF(t *testing.T) {
	for name, raw := range elftest.Malformed() {
		t.Run(name, func(t *testing.T) {
			if _, e := NewFile(raw, MaxInput); !errors.Is(e, ErrUnsafe) {
				t.Fatalf("preflight did not reject malicious metadata: %v", e)
			}
		})
	}
}

func TestTargetedSymbolsPreserveNativeABIFields(t *testing.T) {
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		file, e := NewFile(elftest.Fixture(order, []string{"required", "irrelevant"}, true), MaxInput)
		if e != nil {
			t.Fatal(e)
		}
		found, e := Symbols(file, []string{"required"})
		file.Close()
		if e != nil || len(found) != 1 {
			t.Fatal(found, e)
		}
		s := found["required"]
		if s.Name != "required" || s.Info != byte(elf.STB_GLOBAL)<<4|byte(elf.STT_OBJECT) || s.Other != byte(elf.STV_DEFAULT) || s.Size != 8 || s.Section != 1 {
			t.Fatal(s)
		}
	}
}

func TestInputAndSymbolAllocationBudgets(t *testing.T) {
	raw := elftest.Fixture(binary.LittleEndian, []string{"required"}, true)
	if _, e := NewFile(raw, len(raw)-1); !errors.Is(e, ErrUnsafe) {
		t.Fatal("input limit bypassed", e)
	}
	if _, e := NewFile(raw, MaxInput+1); !errors.Is(e, ErrUnsafe) {
		t.Fatal("global input limit bypassed", e)
	}
	section := raw[elftest.SectionTable+3*elftest.HeaderSize:]
	size := uint64(MaxSymbols+1) * 24
	binary.LittleEndian.PutUint64(section[24:32], uint64(len(raw)))
	binary.LittleEndian.PutUint64(section[32:40], size)
	raw = append(raw, make([]byte, int(size))...)
	if _, e := NewFile(raw, MaxInput); !errors.Is(e, ErrUnsafe) {
		t.Fatal("symbol count allocation bypassed", e)
	}
	raw = elftest.Fixture(binary.LittleEndian, []string{"required"}, true)
	section = raw[elftest.SectionTable+4*elftest.HeaderSize:]
	size = MaxDynamic + 16
	binary.LittleEndian.PutUint64(section[24:32], uint64(len(raw)))
	binary.LittleEndian.PutUint64(section[32:40], size)
	raw = append(raw, make([]byte, int(size))...)
	if _, e := NewFile(raw, MaxInput); !errors.Is(e, ErrUnsafe) {
		t.Fatal("dynamic-tag allocation budget bypassed", e)
	}
}

func TestTargetedSymbolReadIgnoresHugeUnrelatedNames(t *testing.T) {
	// Repeated symbols may reference one long string. DynamicSymbols copies
	// each complete name; the targeted reader examines only requested prefixes.
	longName := make([]byte, 1<<20)
	for i := range longName {
		longName[i] = 'x'
	}
	raw := elftest.Fixture(binary.LittleEndian, []string{string(longName), "required"}, true)
	file, e := NewFile(raw, MaxInput)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if found, e := Symbols(file, []string{"required"}); e != nil || len(found) != 1 {
		t.Fatal(found, e)
	}
	if n := testing.AllocsPerRun(10, func() { Symbols(file, []string{"required"}) }); n > 20 {
		t.Fatal("unexpected symbol allocation growth", n)
	}
}

func FuzzNewFile(f *testing.F) {
	f.Add(elftest.Fixture(binary.LittleEndian, []string{"required"}, true))
	for _, raw := range elftest.Malformed() {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		file, e := NewFile(raw, MaxInput)
		if e == nil {
			defer file.Close()
			Symbols(file, []string{"required"})
			file.DynValue(elf.DT_FLAGS)
		}
	})
}
