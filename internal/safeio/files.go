package safeio

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type Directory struct {
	FD   int
	Path string
}

func OpenDir(path string, create bool) (*Directory, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, errors.New("absolute normalized directory required")
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	var rootInfo syscall.Stat_t
	if e = syscall.Fstat(fd, &rootInfo); e != nil {
		syscall.Close(fd)
		return nil, e
	}
	for _, part := range strings.Split(path[1:], "/") {
		if create {
			e = syscall.Mkdirat(fd, part, 0700)
			if e != nil && e != syscall.EEXIST {
				syscall.Close(fd)
				return nil, e
			}
		}
		child, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = child
		var ancestor syscall.Stat_t
		if e = syscall.Fstat(fd, &ancestor); e != nil {
			syscall.Close(fd)
			return nil, e
		}
		if ancestor.Uid != rootInfo.Uid && ancestor.Uid != uint32(os.Geteuid()) {
			syscall.Close(fd)
			return nil, errors.New("untrusted state ancestor")
		}
		if ancestor.Mode&0022 != 0 && !(ancestor.Uid == rootInfo.Uid && ancestor.Mode&syscall.S_ISVTX != 0) {
			syscall.Close(fd)
			return nil, errors.New("writable state ancestor")
		}
	}
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0700 {
		syscall.Close(fd)
		return nil, errors.New("directory must be owned and mode 0700")
	}
	return &Directory{fd, path}, nil
}
func (d *Directory) Close() error { return syscall.Close(d.FD) }
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\x00")
}
func (d *Directory) Read(name string, limit int) (map[string]any, error) {
	if !validName(name) {
		return nil, errors.New("state name")
	}
	fd, e := syscall.Openat(d.FD, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e == syscall.ENOENT {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Mode&0777 != 0600 || st.Size > int64(limit) {
		return nil, errors.New("unsafe state file")
	}
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if e != nil {
		return nil, e
	}
	return Object(raw, limit)
}
func (d *Directory) Write(name string, v any, limit int) error {
	if !validName(name) {
		return errors.New("state name")
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if len(raw) > limit {
		return errors.New("state size")
	}
	entropy := make([]byte, 16)
	if _, e = rand.Read(entropy); e != nil {
		return e
	}
	tmp := ".go-state-" + hex.EncodeToString(entropy)
	fd, e := syscall.Openat(d.FD, tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(fd), tmp)
	defer f.Close()
	defer syscall.Unlinkat(d.FD, tmp)
	if e = f.Chmod(0600); e != nil {
		return e
	}
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = syscall.Renameat(d.FD, tmp, d.FD, name); e != nil {
		return e
	}
	return syscall.Fsync(d.FD)
}
func (d *Directory) Lock(name string) (*os.File, error) {
	return d.lock(name, true)
}

// LockExisting tests an existing lock without creating or changing a file.
func (d *Directory) LockExisting(name string) (*os.File, error) {
	return d.lock(name, false)
}

func (d *Directory) lock(name string, create bool) (*os.File, error) {
	if !validName(name) {
		return nil, errors.New("lock name")
	}
	flags := syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}
	fd, e := syscall.Openat(d.FD, name, flags, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || st.Size != 0 {
		f.Close()
		return nil, errors.New("unsafe lock")
	}
	if e = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
func Read(path string, limit int) (map[string]any, error) {
	d, e := OpenDir(filepath.Dir(path), false)
	if e != nil {
		return nil, e
	}
	defer d.Close()
	return d.Read(filepath.Base(path), limit)
}
func Write(path string, v any, limit int) error {
	d, e := OpenDir(filepath.Dir(path), false)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Write(filepath.Base(path), v, limit)
}

// ReadFile is for immutable installed assets; state files use stricter mode 0600.
func ReadFile(path string, limit int) ([]byte, error) {
	fd, e := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil {
		return nil, e
	}
	if st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Nlink != 1 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || st.Mode&0022 != 0 || st.Size > int64(limit) {
		return nil, errors.New("unsafe asset")
	}
	b, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(b) > limit {
		return nil, errors.New("asset size")
	}
	return b, e
}
