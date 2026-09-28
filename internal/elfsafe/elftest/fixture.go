// Package elftest supplies small synthetic ELF fixtures, never executable code.
package elftest

import (
	"debug/elf"
	"encoding/binary"
)

const SectionTable = 64
const HeaderSize = 64

func Fixture(order binary.ByteOrder, names []string, defined bool) []byte {
	sectionNames := []byte("\x00.shstrtab\x00.dynstr\x00.dynsym\x00.dynamic\x00.zdebug_bad\x00")
	strings := []byte{0}
	indices := make([]uint32, len(names))
	for i, name := range names {
		indices[i] = uint32(len(strings))
		strings = append(strings, []byte(name)...)
		strings = append(strings, 0)
	}
	shstr := 64 + 5*64
	dynstr := shstr + len(sectionNames)
	dynsym := dynstr + len(strings)
	dynamic := dynsym + (len(names)+1)*24
	raw := make([]byte, dynamic+16)
	copy(raw, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	if order == binary.BigEndian {
		raw[5] = 2
	}
	order.PutUint16(raw[16:18], uint16(elf.ET_DYN))
	order.PutUint16(raw[18:20], uint16(elf.EM_X86_64))
	order.PutUint32(raw[20:24], 1)
	order.PutUint64(raw[40:48], SectionTable)
	order.PutUint16(raw[52:54], 64)
	order.PutUint16(raw[58:60], 64)
	order.PutUint16(raw[60:62], 5)
	order.PutUint16(raw[62:64], 1)
	for i, record := range []struct {
		name         uint32
		kind         elf.SectionType
		offset, size int
		link         uint32
		entry        uint64
	}{
		{1, elf.SHT_STRTAB, shstr, len(sectionNames), 0, 0},
		{11, elf.SHT_STRTAB, dynstr, len(strings), 0, 0},
		{19, elf.SHT_DYNSYM, dynsym, (len(names) + 1) * 24, 2, 24},
		{27, elf.SHT_DYNAMIC, dynamic, 16, 2, 16},
	} {
		s := raw[SectionTable+(i+1)*HeaderSize:]
		order.PutUint32(s[:4], record.name)
		order.PutUint32(s[4:8], uint32(record.kind))
		order.PutUint64(s[24:32], uint64(record.offset))
		order.PutUint64(s[32:40], uint64(record.size))
		order.PutUint32(s[40:44], record.link)
		order.PutUint64(s[56:64], record.entry)
	}
	copy(raw[shstr:], sectionNames)
	copy(raw[dynstr:], strings)
	for i, index := range indices {
		s := raw[dynsym+(i+1)*24:]
		order.PutUint32(s[:4], index)
		s[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT)
		if defined {
			order.PutUint16(s[6:8], 1)
		}
		order.PutUint64(s[16:24], 8)
	}
	return raw
}

func Malformed() map[string][]byte {
	result := map[string][]byte{}
	add := func(name string, edit func([]byte)) {
		raw := Fixture(binary.LittleEndian, []string{"required"}, true)
		edit(raw)
		result[name] = raw
	}
	o := binary.LittleEndian
	for _, i := range []int{1, 2, 3} {
		index := i
		add(map[int]string{1: "compressed shstrtab", 2: "compressed dynstr", 3: "compressed dynsym"}[i], func(raw []byte) {
			s := raw[SectionTable+index*HeaderSize:]
			o.PutUint64(s[8:16], uint64(elf.SHF_COMPRESSED))
			offset := o.Uint64(s[24:32])
			o.PutUint64(s[32:40], 24)
			compression := raw[offset : offset+24]
			o.PutUint32(compression[:4], uint32(elf.COMPRESS_ZLIB))
			o.PutUint64(compression[8:16], 1<<62)
			o.PutUint64(compression[16:24], 1)
		})
	}
	for _, i := range []int{1, 2, 3, 4} {
		index := i
		add(map[int]string{1: "legacy zdebug shstrtab", 2: "legacy zdebug dynstr", 3: "legacy zdebug dynsym", 4: "legacy zdebug dynamic"}[i], func(raw []byte) { o.PutUint32(raw[SectionTable+index*HeaderSize:], 36) })
	}
	add("extended section count", func(raw []byte) { o.PutUint16(raw[60:62], 0); o.PutUint64(raw[SectionTable+32:], 1<<62) })
	add("excess section count", func(raw []byte) { o.PutUint16(raw[60:62], 65535) })
	add("extended string index", func(raw []byte) { o.PutUint16(raw[62:64], 65535) })
	add("excess program count", func(raw []byte) { o.PutUint16(raw[56:58], 65535) })
	add("section table overflow", func(raw []byte) { o.PutUint64(raw[40:48], ^uint64(0)-16) })
	add("section offset overflow", func(raw []byte) { o.PutUint64(raw[SectionTable+2*HeaderSize+24:], ^uint64(0)) })
	add("section extent overflow", func(raw []byte) { o.PutUint64(raw[SectionTable+2*HeaderSize+32:], ^uint64(0)) })
	add("program table overflow", func(raw []byte) {
		o.PutUint16(raw[54:56], 56)
		o.PutUint16(raw[56:58], 1)
		o.PutUint64(raw[32:40], ^uint64(0)-16)
	})
	add("bad section name", func(raw []byte) { o.PutUint32(raw[SectionTable+HeaderSize:], ^uint32(0)) })
	add("symbol link out of range", func(raw []byte) { o.PutUint32(raw[SectionTable+3*HeaderSize+40:], 50) })
	add("symbol link wrong kind", func(raw []byte) { o.PutUint32(raw[SectionTable+3*HeaderSize+40:], 4) })
	add("symbol entry size", func(raw []byte) { o.PutUint64(raw[SectionTable+3*HeaderSize+56:], 1) })
	add("symbol entry count overflow", func(raw []byte) { o.PutUint64(raw[SectionTable+3*HeaderSize+32:], ^uint64(0)-23) })
	add("dynamic tag entry size", func(raw []byte) { o.PutUint64(raw[SectionTable+4*HeaderSize+56:], 1) })
	return result
}
