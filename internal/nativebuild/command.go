// Package nativebuild provides bounded, offline development checks and shader generation.
package nativebuild

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type boundedOutput struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) > b.limit-b.buffer.Len() {
		b.cancel()
		return 0, errors.New("native helper output exceeded its limit")
	}
	return b.buffer.Write(p)
}
func run(timeout time.Duration, limit int, command string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C", "TZ=UTC", "ASAN_OPTIONS=detect_leaks=1:halt_on_error=1", "UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1"}
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	out := &boundedOutput{limit: limit, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.buffer.Bytes(), ctx.Err()
	}
	return out.buffer.Bytes(), err
}

func executable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		return errors.New("native helper is not executable")
	}
	return nil
}
