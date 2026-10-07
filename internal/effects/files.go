package effects

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unsafe"

	"github.com/joega/a-weather-app/internal/safeio"
)

func openDirectory(path string) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, errors.New("absolute nontraversing directory required")
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return -1, e
	}
	var root syscall.Stat_t
	syscall.Fstat(fd, &root)
	if path == "/" {
		return fd, nil
	}
	for _, part := range strings.Split(path[1:], "/") {
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return -1, e
		}
		fd = next
		var st syscall.Stat_t
		e = syscall.Fstat(fd, &st)
		if e != nil || (st.Uid != uint32(os.Getuid()) && st.Uid != root.Uid) || (st.Mode&022 != 0 && st.Mode&syscall.S_ISVTX == 0) {
			syscall.Close(fd)
			return -1, errors.New("untrusted directory ancestor")
		}
	}
	return fd, nil
}
func readRegular(path string, limit int) ([]byte, error) {
	parent, e := openDirectory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer syscall.Close(parent)
	fd, e := syscall.Openat(parent, filepath.Base(path), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || st.Size > int64(limit) || st.Mode&022 != 0 {
		return nil, errors.New("untrusted identity file")
	}
	raw, e := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(raw) > limit {
		return nil, errors.New("identity file byte limit")
	}
	return raw, e
}

type runtimeDir struct {
	path     string
	fd       int
	dev, ino uint64
}

func captureDirectory(path string) (*runtimeDir, error) {
	fd, e := openDirectory(path)
	if e != nil {
		return nil, e
	}
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil || st.Mode&077 != 0 || st.Uid != uint32(os.Getuid()) {
		syscall.Close(fd)
		return nil, errors.New("effects runtime must be private")
	}
	return &runtimeDir{path, fd, uint64(st.Dev), st.Ino}, nil
}
func (d *runtimeDir) check() error {
	fd, e := openDirectory(d.path)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if e = syscall.Fstat(fd, &st); e != nil {
		return e
	}
	if uint64(st.Dev) != d.dev || st.Ino != d.ino {
		return errors.New("effects runtime directory identity changed")
	}
	return nil
}
func (d *runtimeDir) publish(name string, value any) error {
	if e := d.check(); e != nil {
		return e
	}
	return (&safeio.Directory{FD: d.fd, Path: d.path}).Write(name, value, 262144)
}
func (d *runtimeDir) log(name string) (*os.File, error) {
	if e := d.check(); e != nil {
		return nil, e
	}
	fd, e := syscall.Openat(d.fd, name, syscall.O_WRONLY|syscall.O_APPEND|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if e != nil {
		return nil, e
	}
	return os.NewFile(uintptr(fd), name), nil
}

var managedEntry = regexp.MustCompile(`^(selected\.json|policy\.json|child-[01]\.jsonl|host-restart-[0-9]+\.jsonl)$`)

func (d *runtimeDir) cleanup() error {
	if e := d.check(); e != nil {
		return e
	}
	dup, e := syscall.Openat(d.fd, ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(dup), d.path)
	entries, e := f.ReadDir(65)
	f.Close()
	if e != nil && e != io.EOF {
		return e
	}
	if len(entries) > 64 {
		return errors.New("effects runtime entry bound")
	}
	for _, entry := range entries {
		if !managedEntry.MatchString(entry.Name()) {
			return errors.New("unmanaged effects runtime entry")
		}
		fd, e := syscall.Openat(d.fd, entry.Name(), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if e != nil {
			return e
		}
		var st syscall.Stat_t
		e = syscall.Fstat(fd, &st)
		syscall.Close(fd)
		if e != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
			return errors.New("unmanaged effects runtime object")
		}
	}
	if e = d.check(); e != nil {
		return e
	}
	for _, entry := range entries {
		if e = syscall.Unlinkat(d.fd, entry.Name()); e != nil {
			return e
		}
	}
	parent, e := openDirectory(filepath.Dir(d.path))
	if e != nil {
		return e
	}
	defer syscall.Close(parent)
	if e = d.check(); e != nil {
		return e
	}
	name, e := syscall.BytePtrFromString(filepath.Base(d.path))
	if e != nil {
		return e
	}
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(parent), uintptr(unsafe.Pointer(name)), 0x200)
	if errno != 0 {
		return fmt.Errorf("runtime remove: %w", errno)
	}
	syscall.Close(d.fd)
	d.fd = -1
	return nil
}
