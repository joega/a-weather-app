package nativebuild

import (
	"debug/elf"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/joega/a-weather-app/internal/elfsafe"
	"github.com/joega/a-weather-app/internal/safeio"
)

type protections struct {
	stacks                                               int
	executableStack, relro, now, pie, writableExecutable bool
}

func validateProtections(p protections, shared bool) error {
	if p.stacks != 1 || p.executableStack {
		return errors.New("missing or executable GNU_STACK")
	}
	if !p.relro || !p.now {
		return errors.New("full RELRO required")
	}
	if p.writableExecutable {
		return errors.New("writable executable load segment")
	}
	if !shared && !p.pie {
		return errors.New("position-independent executable required")
	}
	return nil
}
func openELF(path string, limit int) (*elf.File, error) {
	raw, err := safeio.ReadFile(path, limit)
	if err != nil {
		return nil, err
	}
	return elfsafe.NewFile(raw, limit)
}
func dyn(file *elf.File, tag elf.DynTag) []uint64 { v, _ := file.DynValue(tag); return v }
func flags(file *elf.File, tag elf.DynTag) uint64 {
	var result uint64
	for _, v := range dyn(file, tag) {
		result |= v
	}
	return result
}
func Hardening(path string) error {
	file, err := openELF(path, 128*1024*1024)
	if err != nil {
		return err
	}
	defer file.Close()
	p := protections{}
	for _, program := range file.Progs {
		switch program.Type {
		case elf.PT_GNU_STACK:
			p.stacks++
			p.executableStack = program.Flags&elf.PF_X != 0
		case elf.PT_GNU_RELRO:
			p.relro = true
		case elf.PT_LOAD:
			if program.Flags&(elf.PF_W|elf.PF_X) == elf.PF_W|elf.PF_X {
				p.writableExecutable = true
			}
		}
	}
	p.now = len(dyn(file, elf.DT_BIND_NOW)) > 0 || flags(file, elf.DT_FLAGS)&8 != 0 || flags(file, elf.DT_FLAGS_1)&1 != 0
	p.pie = flags(file, elf.DT_FLAGS_1)&0x08000000 != 0
	return validateProtections(p, filepath.Ext(path) == ".so")
}

var required = []string{"g_pHyprRenderer", "_ZN10NProtocols11sessionLockE", "_ZGV15g_pHyprRenderer", "_ZGVN10NProtocols11sessionLockE", "g_pEventLoopManager", "_ZGV19g_pEventLoopManager"}

func symbols(file *elf.File) (map[string]elf.Symbol, error) {
	return elfsafe.Symbols(file, required)
}
func checkSymbols(plugin, host map[string]elf.Symbol, symbolic bool) error {
	if symbolic {
		return errors.New("plugin SYMBOLIC binding prevents compositor preemption")
	}
	for _, name := range required {
		p, present := plugin[name]
		h, exported := host[name]
		if !present || elf.ST_TYPE(p.Info) != elf.STT_OBJECT || (elf.ST_BIND(p.Info) != elf.STB_GLOBAL && elf.ST_BIND(p.Info) != elf.STB_WEAK) || elf.ST_VISIBILITY(p.Other) != elf.STV_DEFAULT {
			return fmt.Errorf("plugin %s lacks DEFAULT GLOBAL/WEAK OBJECT", name)
		}
		if !exported || elf.ST_TYPE(h.Info) != elf.STT_OBJECT || (elf.ST_BIND(h.Info) != elf.STB_GLOBAL && elf.ST_BIND(h.Info) != elf.STB_WEAK && elf.ST_BIND(h.Info) != elf.SymBind(10)) || elf.ST_VISIBILITY(h.Other) != elf.STV_DEFAULT || h.Section == elf.SHN_UNDEF {
			return fmt.Errorf("host %s lacks compatible defined dynamic export", name)
		}
		if p.Section != elf.SHN_UNDEF && p.Size != h.Size {
			return fmt.Errorf("plugin %s size %d differs from host %d", name, p.Size, h.Size)
		}
	}
	return nil
}
func Symbols(plugin, host string) error {
	p, err := openELF(plugin, 16*1024*1024)
	if err != nil {
		return err
	}
	defer p.Close()
	host, err = filepath.EvalSymlinks(host)
	if err != nil {
		return err
	}
	h, err := openELF(host, 128*1024*1024)
	if err != nil {
		return err
	}
	defer h.Close()
	ps, err := symbols(p)
	if err != nil {
		return err
	}
	hs, err := symbols(h)
	if err != nil {
		return err
	}
	return checkSymbols(ps, hs, len(dyn(p, elf.DT_SYMBOLIC)) > 0 || flags(p, elf.DT_FLAGS)&2 != 0)
}
