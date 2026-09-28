package nativebuild

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShaderTranslationsMatchReviewedArtifacts(t *testing.T) {
	root := filepath.Join("..", "..")
	raw, err := os.ReadFile(filepath.Join(root, "godot/shaders/atmosphere.gdshader"))
	if err != nil {
		t.Fatal(err)
	}
	qt, err := TranslateQt(raw)
	if err != nil {
		t.Fatal(err)
	}
	gold, err := os.ReadFile(filepath.Join(root, "ui/shaders/atmosphere.frag"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(qt, gold) {
		t.Fatal("Qt translation changed reviewed shader")
	}
	native, err := TranslateNative(raw)
	if err != nil {
		t.Fatal(err)
	}
	gold, err = os.ReadFile(filepath.Join(root, "native/atmosphere/sky_shader.h"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(native, gold) {
		t.Fatal("native translation changed reviewed header")
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte("scene_time"), []byte("changed"), 1), append(raw, []byte("\nvoid fragment() {}")...), bytes.Replace(raw, []byte("COLOR ="), []byte("OTHER ="), 1), bytes.Repeat([]byte(" "), 65537), {0xff}} {
		if _, err = TranslateQt(bad); err == nil {
			t.Fatal("accepted changed or unbounded shader interface")
		}
	}
}
func TestHardeningMemoryProtections(t *testing.T) {
	valid := protections{stacks: 1, relro: true, now: true, pie: true}
	if err := validateProtections(valid, false); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*protections){func(p *protections) { p.stacks = 0 }, func(p *protections) { p.stacks = 2 }, func(p *protections) { p.executableStack = true }, func(p *protections) { p.relro = false }, func(p *protections) { p.now = false }, func(p *protections) { p.pie = false }, func(p *protections) { p.writableExecutable = true }} {
		p := valid
		change(&p)
		if validateProtections(p, false) == nil {
			t.Fatal("accepted missing ELF protection")
		}
	}
	p := valid
	p.pie = false
	if err := validateProtections(p, true); err != nil {
		t.Fatal("shared libraries should not require PIE", err)
	}
}
func TestHardeningReadsELFTagsNotSonameStrings(t *testing.T) {
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("development compiler unavailable")
	}
	directory := t.TempDir()
	source := filepath.Join(directory, "main.c")
	if err = os.WriteFile(source, []byte("int main(void){return 0;}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, now := range []bool{false, true} {
		output := filepath.Join(directory, map[bool]string{false: "unsafe", true: "safe"}[now])
		binding := "lazy"
		if now {
			binding = "now"
		}
		cmd := exec.Command(cc, "-fPIE", "-pie", "-Wl,-z,relro,-z,"+binding+",-z,noexecstack,-soname,BIND_NOW_NOW_PIE", source, "-o", output)
		if data, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("compiler: %s %v", data, e)
		}
		if (Hardening(output) == nil) != now {
			t.Fatalf("ELF hardening tag acceptance now=%v", now)
		}
	}
}
func TestCompositorGlobalsArePreemptible(t *testing.T) {
	plugin, host := map[string]elf.Symbol{}, map[string]elf.Symbol{}
	for _, name := range required {
		plugin[name] = elf.Symbol{Name: name, Info: byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT), Other: byte(elf.STV_DEFAULT), Section: elf.SHN_UNDEF}
		host[name] = elf.Symbol{Name: name, Info: byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_OBJECT), Other: byte(elf.STV_DEFAULT), Size: 8, Section: 1}
	}
	if err := checkSymbols(plugin, host, false); err != nil {
		t.Fatal(err)
	}
	if checkSymbols(plugin, host, true) == nil {
		t.Fatal("accepted SYMBOLIC")
	}
	for _, change := range []func(map[string]elf.Symbol, map[string]elf.Symbol){func(p, h map[string]elf.Symbol) { delete(p, required[1]) }, func(p, h map[string]elf.Symbol) {
		s := p[required[0]]
		s.Other = byte(elf.STV_HIDDEN)
		p[required[0]] = s
	}, func(p, h map[string]elf.Symbol) {
		s := p[required[0]]
		s.Info = byte(elf.STB_LOCAL)<<4 | byte(elf.STT_OBJECT)
		p[required[0]] = s
	}, func(p, h map[string]elf.Symbol) { s := h[required[0]]; s.Section = elf.SHN_UNDEF; h[required[0]] = s }, func(p, h map[string]elf.Symbol) { s := p[required[0]]; s.Section = 1; s.Size = 16; p[required[0]] = s }} {
		p, h := map[string]elf.Symbol{}, map[string]elf.Symbol{}
		for k, v := range plugin {
			p[k] = v
		}
		for k, v := range host {
			h[k] = v
		}
		change(p, h)
		if checkSymbols(p, h, false) == nil {
			t.Fatal("accepted incompatible compositor globals")
		}
	}
}
func TestGeneratedWritesRejectAncestorSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "real")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if atomicWrite(filepath.Join(alias, "generated"), []byte("data"), 10) == nil {
		t.Fatal("followed output directory symlink")
	}
	if err := atomicWrite(filepath.Join(target, "generated"), []byte("data"), 10); err != nil {
		t.Fatal(err)
	}
	if atomicWrite(filepath.Join(target, "generated"), []byte(strings.Repeat("x", 11)), 10) == nil {
		t.Fatal("accepted unbounded output")
	}
}

func TestNativeCommandsHaveFixedBudgets(t *testing.T) {
	started := time.Now()
	if _, err := run(50*time.Millisecond, 64, "/usr/bin/sleep", "5"); err == nil {
		t.Fatal("unbounded command lifetime")
	}
	if time.Since(started) > time.Second {
		t.Fatal("deadline exceeded")
	}
	started = time.Now()
	data, err := run(time.Second, 64, "/usr/bin/yes")
	if err == nil || len(data) > 64 {
		t.Fatal("unbounded command output")
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("output cancellation did not stop helper")
	}
}
