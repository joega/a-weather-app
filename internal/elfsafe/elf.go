// Package elfsafe bounds ELF metadata before debug/elf can allocate or decode
// attacker-selected sections. Native validation needs headers and a few named
// exports, never compressed debug information or GNU symbol-version metadata.
package elfsafe

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MaxInput       = 128 * 1024 * 1024
	MaxSections    = 4096
	MaxPrograms    = 1024
	MaxSymbols     = 262144
	MaxStrings     = 16 * 1024 * 1024
	MaxDynamic     = 1024 * 1024
	maxSectionName = 256
)

var ErrUnsafe = errors.New("unsafe or unsupported ELF metadata")

type section struct {
	name, kind, link           uint32
	flags, offset, size, entry uint64
}

func extent(offset, size uint64, length int) bool {
	return offset <= uint64(length) && size <= uint64(length)-offset
}

// NewFile accepts the native package's ELF64 ABI, with ordinary bounded header
// tables. Extended counts and compressed sections are intentionally unsupported.
// Crucially, every check precedes elf.NewFile, which reads shstrtab itself.
func NewFile(raw []byte, limit int) (*elf.File, error) {
	if limit <= 0 || limit > MaxInput || len(raw) > limit || len(raw) < 64 || !bytes.Equal(raw[:4], []byte{0x7f, 'E', 'L', 'F'}) || raw[4] != byte(elf.ELFCLASS64) || raw[6] != 1 {
		return nil, ErrUnsafe
	}
	var order binary.ByteOrder
	switch raw[5] {
	case 1:
		order = binary.LittleEndian
	case 2:
		order = binary.BigEndian
	default:
		return nil, ErrUnsafe
	}
	if order.Uint32(raw[20:24]) != 1 || order.Uint16(raw[52:54]) != 64 {
		return nil, ErrUnsafe
	}
	phoff, shoff := order.Uint64(raw[32:40]), order.Uint64(raw[40:48])
	phsize, phnum := uint64(order.Uint16(raw[54:56])), uint64(order.Uint16(raw[56:58]))
	shsize, shnum := uint64(order.Uint16(raw[58:60])), uint64(order.Uint16(raw[60:62]))
	stringsIndex := uint64(order.Uint16(raw[62:64]))
	if shnum == 0 || shnum > MaxSections || shsize != 64 || !extent(shoff, shnum*shsize, len(raw)) || stringsIndex >= shnum || phnum > MaxPrograms || (phnum > 0 && (phsize != 56 || !extent(phoff, phnum*phsize, len(raw)))) {
		return nil, fmt.Errorf("%w: header counts or extents", ErrUnsafe)
	}
	for i := uint64(0); i < phnum; i++ {
		p := raw[phoff+i*phsize : phoff+(i+1)*phsize]
		if !extent(order.Uint64(p[8:16]), order.Uint64(p[32:40]), len(raw)) {
			return nil, fmt.Errorf("%w: program extent", ErrUnsafe)
		}
	}
	sections := make([]section, int(shnum))
	for i := range sections {
		s := raw[shoff+uint64(i)*shsize : shoff+uint64(i+1)*shsize]
		v := section{name: order.Uint32(s[:4]), kind: order.Uint32(s[4:8]), flags: order.Uint64(s[8:16]), offset: order.Uint64(s[24:32]), size: order.Uint64(s[32:40]), link: order.Uint32(s[40:44]), entry: order.Uint64(s[56:64])}
		if v.flags&uint64(elf.SHF_COMPRESSED) != 0 || (v.kind != uint32(elf.SHT_NOBITS) && !extent(v.offset, v.size, len(raw))) {
			return nil, fmt.Errorf("%w: compressed section or extent", ErrUnsafe)
		}
		if v.kind == uint32(elf.SHT_STRTAB) && v.size > MaxStrings {
			return nil, fmt.Errorf("%w: string-table budget", ErrUnsafe)
		}
		if v.kind == uint32(elf.SHT_DYNSYM) && (v.entry != 24 || v.size%24 != 0 || v.size/24 > MaxSymbols || uint64(v.link) >= shnum) {
			return nil, fmt.Errorf("%w: dynamic-symbol budget or layout", ErrUnsafe)
		}
		if v.kind == uint32(elf.SHT_DYNAMIC) && (v.entry != 16 || v.size%16 != 0 || v.size > MaxDynamic) {
			return nil, fmt.Errorf("%w: dynamic-tag budget or layout", ErrUnsafe)
		}
		sections[i] = v
	}
	if sections[0].kind != uint32(elf.SHT_NULL) {
		return nil, ErrUnsafe
	}
	if stringsIndex != 0 {
		s := sections[stringsIndex]
		if s.kind != uint32(elf.SHT_STRTAB) {
			return nil, ErrUnsafe
		}
		names := raw[s.offset : s.offset+s.size]
		for _, v := range sections {
			if uint64(v.name) >= uint64(len(names)) {
				return nil, fmt.Errorf("%w: section name index", ErrUnsafe)
			}
			name := names[v.name:min(uint64(len(names)), uint64(v.name)+maxSectionName+1)]
			end := bytes.IndexByte(name, 0)
			if end < 0 || end > maxSectionName || bytes.HasPrefix(name[:end], []byte(".zdebug")) {
				return nil, fmt.Errorf("%w: section name or legacy compression", ErrUnsafe)
			}
		}
	}
	for _, v := range sections {
		if v.kind == uint32(elf.SHT_DYNSYM) && sections[v.link].kind != uint32(elf.SHT_STRTAB) {
			return nil, fmt.Errorf("%w: dynamic string table", ErrUnsafe)
		}
	}
	return elf.NewFile(bytes.NewReader(raw))
}

// Symbols returns only requested exports, without DynamicSymbols' full-symbol
// allocation, arbitrary-length name copies, or GNU version-table decoding.
// The caller must pass a File returned by NewFile.
func Symbols(file *elf.File, names []string) (map[string]elf.Symbol, error) {
	if len(names) > 64 {
		return nil, ErrUnsafe
	}
	longest := 0
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		if len(name) > 256 {
			return nil, ErrUnsafe
		}
		longest = max(longest, len(name))
		wanted[name] = true
	}
	s := file.SectionByType(elf.SHT_DYNSYM)
	if s == nil || s.ReaderAt == nil || s.Entsize != 24 || s.Size%24 != 0 || s.Size/24 > MaxSymbols || uint64(s.Link) >= uint64(len(file.Sections)) {
		return nil, elf.ErrNoSymbols
	}
	str := file.Sections[s.Link]
	if str.Type != elf.SHT_STRTAB || str.ReaderAt == nil || str.Size > MaxStrings {
		return nil, ErrUnsafe
	}
	found := map[string]elf.Symbol{}
	var entry [24]byte
	nameBytes := make([]byte, longest+1)
	for offset := uint64(24); offset < s.Size; offset += 24 {
		if _, e := s.ReadAt(entry[:], int64(offset)); e != nil {
			return nil, e
		}
		index := uint64(file.ByteOrder.Uint32(entry[:4]))
		if index >= str.Size {
			return nil, fmt.Errorf("%w: symbol name index", ErrUnsafe)
		}
		size := min(uint64(len(nameBytes)), str.Size-index)
		if _, e := str.ReadAt(nameBytes[:size], int64(index)); e != nil {
			return nil, e
		}
		end := bytes.IndexByte(nameBytes[:size], 0)
		if end < 0 {
			continue
		} // Longer names cannot match the requested exports.
		name := string(nameBytes[:end])
		if wanted[name] {
			found[name] = elf.Symbol{Name: name, Info: entry[4], Other: entry[5], Section: elf.SectionIndex(file.ByteOrder.Uint16(entry[6:8])), Value: file.ByteOrder.Uint64(entry[8:16]), Size: file.ByteOrder.Uint64(entry[16:24])}
		}
	}
	return found, nil
}
