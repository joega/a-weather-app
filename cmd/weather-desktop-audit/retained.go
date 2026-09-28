package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"syscall"

	"github.com/joega/a-weather-app/internal/safeio"
)

var diagnosticEntry = regexp.MustCompile(`^(selected\.json|policy\.json|child-[01]\.jsonl|host-restart-[0-9]+\.jsonl)$`)

type fileEvidence struct {
	Device   uint64           `json:"device"`
	Inode    uint64           `json:"inode"`
	Bytes    int              `json:"bytes"`
	SHA256   string           `json:"sha256"`
	Modified syscall.Timespec `json:"modified"`
	Changed  syscall.Timespec `json:"changed"`
}
type directoryEvidence struct {
	Device uint64                  `json:"device"`
	Inode  uint64                  `json:"inode"`
	Files  map[string]fileEvidence `json:"files"`
}

// retainedSnapshot is read-only and descriptor-relative. It intentionally never
// deletes a crash diagnostic, even after successful guarded native recovery.
func retainedSnapshot(path string) (directoryEvidence, error) {
	result := directoryEvidence{Files: map[string]fileEvidence{}}
	directory, e := safeio.OpenDir(path, false)
	if e != nil {
		return result, e
	}
	defer directory.Close()
	var stat syscall.Stat_t
	if e = syscall.Fstat(directory.FD, &stat); e != nil {
		return result, e
	}
	result.Device, result.Inode = uint64(stat.Dev), stat.Ino
	fd, e := syscall.Openat(directory.FD, ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return result, e
	}
	listing := os.NewFile(uintptr(fd), "retained-effects-diagnostic")
	names, e := listing.Readdirnames(17)
	listing.Close()
	if e != nil && e != io.EOF {
		return result, e
	}
	if len(names) == 0 || len(names) > 16 {
		return result, errors.Join(errors.New("retained diagnostic entry budget"), e)
	}
	total := 0
	for _, name := range names {
		if !diagnosticEntry.MatchString(name) {
			return result, errors.New("unexpected retained effects diagnostic entry")
		}
		fd, e := syscall.Openat(directory.FD, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if e != nil {
			return result, e
		}
		var info syscall.Stat_t
		e = syscall.Fstat(fd, &info)
		if e != nil || info.Mode&syscall.S_IFMT != syscall.S_IFREG || info.Mode&0777 != 0600 || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 || info.Size < 0 || info.Size > 1048576 {
			syscall.Close(fd)
			return result, errors.New("unsafe retained effects diagnostic file")
		}
		file := os.NewFile(uintptr(fd), name)
		raw, e := io.ReadAll(io.LimitReader(file, 1048577))
		var after syscall.Stat_t
		statErr := syscall.Fstat(fd, &after)
		file.Close()
		if e != nil || statErr != nil || info.Dev != after.Dev || info.Ino != after.Ino || info.Mode != after.Mode || info.Uid != after.Uid || info.Nlink != after.Nlink || info.Size != after.Size || info.Mtim != after.Mtim || info.Ctim != after.Ctim || int64(len(raw)) != info.Size {
			return result, errors.Join(errors.New("retained diagnostic changed while reading"), e, statErr)
		}
		total += len(raw)
		if total > 4*1024*1024 {
			return result, errors.New("retained diagnostic total byte budget")
		}
		result.Files[name] = fileEvidence{uint64(info.Dev), info.Ino, len(raw), fmt.Sprintf("%x", sha256.Sum256(raw)), info.Mtim, info.Ctim}
	}
	return result, nil
}

func verifyRetained(path string, expected directoryEvidence) error {
	actual, e := retainedSnapshot(path)
	if e != nil {
		return e
	}
	if !reflect.DeepEqual(actual, expected) {
		return errors.New("retained crash diagnostic was changed or replaced")
	}
	return nil
}
