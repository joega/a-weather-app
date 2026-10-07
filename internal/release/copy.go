package release

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// CopyVerified imports a pristine extracted bundle into a fresh private
// directory. Reads and writes are anchored to descriptors; originals are never
// changed. Every copied byte is verified before the result can be activated.
func CopyVerified(source, destination string) (err error) {
	if err = Verify(source); err != nil {
		return err
	}
	s, err := openRoot(source)
	if err != nil {
		return err
	}
	defer s.close()
	parent, err := openRoot(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer parent.close()
	name := filepath.Base(destination)
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || !safeRelative(name) {
		return errors.New("invalid import destination")
	}
	pfd := parent.dirs[""]
	if err = syscall.Mkdirat(pfd, name, 0700); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(filepath.Join("/proc/self/fd", strconv.Itoa(pfd), name))
		}
	}()
	fd, err := syscall.Openat(pfd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	d := &tree{dirs: map[string]int{"": fd}, systemUID: s.systemUID}
	defer d.close()
	f, before, err := s.openFile(manifestPath)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxManifest+1))
	f.Close()
	if err != nil {
		return err
	}
	m, err := parseManifest(raw)
	if err != nil {
		return err
	}
	if err = s.inventory(before); err != nil {
		return err
	}
	checksum := sha256.Sum256(raw)
	m.records[manifestPath] = record{int64(len(raw)), hex.EncodeToString(checksum[:])}
	names := make([]string, 0, len(m.records))
	for name := range m.records {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		parentName := path.Dir(name)
		current := ""
		if parentName != "." {
			for _, part := range strings.Split(parentName, "/") {
				base := d.dirs[current]
				next := part
				if current != "" {
					next = current + "/" + part
				}
				if _, ok := d.dirs[next]; !ok {
					if err = syscall.Mkdirat(base, part, 0700); err != nil {
						return err
					}
					if _, err = d.directory(next); err != nil {
						return err
					}
				}
				current = next
			}
		}
		in, before, e := s.openFile(name)
		if e != nil {
			return e
		}
		r := m.records[name]
		outFD, e := syscall.Openat(d.dirs[current], path.Base(name), syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if e != nil {
			in.Close()
			return e
		}
		out := os.NewFile(uintptr(outFD), name)
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(out, h), io.LimitReader(in, r.bytes+1))
		var after syscall.Stat_t
		statErr := syscall.Fstat(int(in.Fd()), &after)
		in.Close()
		if e == nil && (statErr != nil || changed(before, after) || n != r.bytes || hex.EncodeToString(h.Sum(nil)) != r.digest) {
			e = errors.New("runtime changed during import")
		}
		if e == nil {
			e = out.Chmod(0600 | os.FileMode(before.Mode&0100))
		}
		if e == nil {
			e = out.Sync()
		}
		out.Close()
		if e != nil {
			return e
		}
	}
	for _, fd := range d.dirs {
		if err = syscall.Fsync(fd); err != nil {
			return err
		}
	}
	if err = syscall.Fsync(pfd); err != nil {
		return err
	}
	return Verify(destination)
}
