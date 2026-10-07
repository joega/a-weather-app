package updater

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveFixture(t *testing.T, headers ...*tar.Header) string {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, h := range headers {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			if _, e := w.Write(bytes.Repeat([]byte("x"), int(h.Size))); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e := w.Close(); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "fixture.tar")
	if e := os.WriteFile(p, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}

func TestArchiveRefusesTraversalLinksDuplicatesAndBounds(t *testing.T) {
	for _, h := range []*tar.Header{{Name: "../escaped", Typeflag: tar.TypeReg}, {Name: "/escaped", Typeflag: tar.TypeReg}, {Name: "parent/../escaped", Typeflag: tar.TypeReg}, {Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"}, {Name: "link", Typeflag: tar.TypeLink, Linkname: "existing"}, {Name: "fifo", Typeflag: tar.TypeFifo}, {Name: "huge", Typeflag: tar.TypeReg, Size: releaseBound() + 1}} {
		// Bound checking is exercised using a header-only tar to avoid allocating a large payload.
		if h.Size > 0 {
			var b bytes.Buffer
			w := tar.NewWriter(&b)
			_ = w.WriteHeader(h)
			p := filepath.Join(t.TempDir(), "header.tar")
			_ = os.WriteFile(p, b.Bytes(), 0600)
			if extract(p, t.TempDir()) == nil {
				t.Fatal("accepted", h)
			}
			continue
		}
		if extract(archiveFixture(t, h), t.TempDir()) == nil {
			t.Fatal("accepted", h)
		}
	}
	h := &tar.Header{Name: "duplicate", Typeflag: tar.TypeReg}
	if extract(archiveFixture(t, h, h), t.TempDir()) == nil {
		t.Fatal("accepted duplicate")
	}
}
func releaseBound() int64 { return 16 * 1024 * 1024 }

func TestArchivePreservesOnlyExecutableBit(t *testing.T) {
	p := archiveFixture(t, &tar.Header{Name: "./bin/tool", Typeflag: tar.TypeReg, Mode: 04777, Size: 1}, &tar.Header{Name: "./data", Typeflag: tar.TypeReg, Mode: 0666, Size: 1})
	d := t.TempDir()
	if e := extract(p, d); e != nil {
		t.Fatal(e)
	}
	for name, want := range map[string]os.FileMode{"bin/tool": 0700, "data": 0600} {
		info, e := os.Stat(filepath.Join(d, name))
		if e != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: %v %v", name, info, e)
		}
	}
}

func TestDownloadChecksDigestAndHTTPFailure(t *testing.T) {
	for _, v := range []struct {
		code   int
		digest string
		ok     bool
	}{{200, fmt.Sprintf("%x", sha256.Sum256([]byte("archive"))), true}, {200, strings.Repeat("0", 64), false}, {404, strings.Repeat("0", 64), false}} {
		g := GitHubSource{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: v.code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("archive"))}, nil
		})}}
		e := g.download(context.Background(), "https://github.com/fixture", filepath.Join(t.TempDir(), "archive"), v.digest)
		if (e == nil) != v.ok {
			t.Fatalf("%+v: %v", v, e)
		}
	}
}

func TestPrepareRejectsDifferentPinBeforeArchiveDownload(t *testing.T) {
	requests := 0
	parent := t.TempDir()
	g := GitHubSource{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("c", 64) + "  a-weather-app-v0.51.9-linux-x86_64.tar\n"))}, nil
	})}}
	if _, e := g.Prepare(context.Background(), fixturePin("v0.51.9"), parent, nil); e == nil {
		t.Fatal("accepted changed checksum")
	}
	rows, e := os.ReadDir(parent)
	if e != nil || len(rows) != 0 || requests != 1 {
		t.Fatalf("staging cleanup: %v %v requests %d", rows, e, requests)
	}
}
