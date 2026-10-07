package updater

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
)

const maxArchive = 64 * 1024 * 1024

// Prepare downloads into a fresh owned staging directory. Nothing running or
// installed is changed until the complete archive and runtime inventory verify.
func (g GitHubSource) Prepare(ctx context.Context, pin Pin, parent string, progress func(string)) (string, error) {
	if e := pin.Validate(); e != nil {
		return "", e
	}
	stage, e := os.MkdirTemp(parent, ".update-download-")
	if e != nil {
		return "", e
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()
	base := "https://github.com/joega/a-weather-app/releases/download/" + pin.Tag + "/"
	archive := "a-weather-app-" + pin.Tag + "-linux-x86_64.tar"
	if progress != nil {
		progress("downloading")
	}
	checks, e := g.get(ctx, base+"SHA256SUMS", 4096)
	if e != nil {
		return "", e
	}
	fields := strings.Fields(string(checks))
	if len(fields) != 2 || fields[0] != pin.ArchiveSHA256 || fields[1] != archive {
		return "", errors.New("release checksum differs from published pin")
	}
	metadata, e := g.get(ctx, base+"go-runtime.json", release.MaxManifest)
	if e != nil {
		return "", e
	}
	m, e := safeio.Object(metadata, release.MaxManifest)
	if e != nil || m["source_commit"] != pin.SourceCommit || m["source_dirty"] != false || m["runtime"] != "go-qt" || m["architecture"] != "x86_64" {
		return "", errors.New("release metadata differs from published pin")
	}
	rows, okRows := m["source_status"].([]any)
	if !okRows || len(rows) != 0 {
		return "", errors.New("release was built from modified sources")
	}
	archivePath := filepath.Join(stage, "download.tar")
	if e = g.download(ctx, base+archive, archivePath, pin.ArchiveSHA256); e != nil {
		return "", e
	}
	if progress != nil {
		progress("verifying")
	}
	bundle := filepath.Join(stage, "runtime")
	if e = os.Mkdir(bundle, 0700); e != nil {
		return "", e
	}
	if e = extract(archivePath, bundle); e != nil {
		return "", e
	}
	installedMetadata, e := safeio.ReadFile(filepath.Join(bundle, "packaging/runtime.json"), release.MaxManifest)
	if e != nil || !bytes.Equal(metadata, installedMetadata) {
		return "", errors.New("archive metadata differs from release metadata")
	}
	manifest, e := safeio.ReadFile(filepath.Join(bundle, "manifest.json"), release.MaxManifest)
	if e != nil {
		return "", e
	}
	v, e := safeio.Object(manifest, release.MaxManifest)
	if e != nil || v["version"] != strings.TrimPrefix(pin.Tag, "v") {
		return "", errors.New("archive version differs from selected release")
	}
	if e = release.Verify(bundle); e != nil {
		return "", fmt.Errorf("runtime verification: %w", e)
	}
	if e = os.Remove(archivePath); e != nil {
		return "", e
	}
	ok = true
	return bundle, nil
}

func (g GitHubSource) download(ctx context.Context, url, destination, digest string) error {
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if r.URL.Scheme != "https" || len(via) >= 5 {
				return errors.New("unsafe download redirect")
			}
			return nil
		}}
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "a-weather-app-updater")
	resp, e := client.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArchive {
		return errors.New("archive exceeds download bound")
	}
	f, e := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if e != nil {
		return e
	}
	if n > maxArchive {
		return errors.New("archive exceeds download bound")
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return errors.New("download checksum verification failed")
	}
	return f.Sync()
}

func extract(archive, destination string) error {
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	r := tar.NewReader(f)
	seen := map[string]bool{}
	var expanded int64
	for entries := 0; ; entries++ {
		h, e := r.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		if entries > 128 {
			return errors.New("archive entry limit")
		}
		name := strings.TrimSuffix(strings.TrimPrefix(h.Name, "./"), "/")
		if name == "." || name == "" {
			if h.Typeflag != tar.TypeDir {
				return errors.New("invalid archive root")
			}
			continue
		}
		if path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "../") || name == ".." || seen[name] {
			return errors.New("unsafe or duplicate archive path")
		}
		seen[name] = true
		file := filepath.Join(destination, filepath.FromSlash(name))
		switch h.Typeflag {
		case tar.TypeDir:
			if e = os.MkdirAll(file, 0700); e != nil {
				return e
			}
		case tar.TypeReg, 0: // NUL is the legacy regular-file type; preserve archive compatibility.
			if h.Size < 0 || h.Size > release.MaxArtifact {
				return errors.New("archive file exceeds bound")
			}
			expanded += h.Size
			if expanded > 128*1024*1024 {
				return errors.New("expanded archive exceeds bound")
			}
			if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
				return e
			}
			mode := os.FileMode(0600)
			if h.Mode&0111 != 0 {
				mode = 0700
			}
			out, e := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if e != nil {
				return e
			}
			_, copyErr := io.CopyN(out, r, h.Size)
			syncErr := out.Sync()
			closeErr := out.Close()
			if e = errors.Join(copyErr, syncErr, closeErr); e != nil {
				return e
			}
		default:
			return errors.New("archive links and special files are unsupported")
		}
	}
}
