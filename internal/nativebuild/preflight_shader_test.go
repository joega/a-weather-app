package nativebuild

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// A successful preflight must compile the same fragment variant Qt will use.
// Go-only hosts can omit Shader Tools; Qt/release validation must run this test.
func TestPreflightShadersMatchPack(t *testing.T) {
	if _, err := os.Stat("/usr/lib/qt6/bin/qsb"); err != nil {
		t.Skip("Qt Shader Tools unavailable")
	}
	root := filepath.Join("..", "..")
	pack, err := filepath.Abs(filepath.Join(root, "ui", "shaders", "atmosphere.frag.qsb"))
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct{ key, name string }{{"glsl,330", "atmosphere.gl330.frag"}, {"glsl,300 es", "atmosphere.gles300.frag"}} {
		t.Run(variant.key, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "extracted.frag")
			if err := extractGLShader(pack, target, variant.key); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(root, "ui", "shaders", variant.name))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("preflight shader differs from the baked Qt shader; run make shaders")
			}
		})
	}
}
