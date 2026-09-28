package supervision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

// The service may spend 70s closing effects, 2s flushing terminal events, and
// 5s retiring Qt. Keep the leader anchored for that whole graceful interval.
const GuardianLeaderGrace = 80 * time.Second
const GuardianCleanupGrace = 85 * time.Second
const GuardianDiagnosticLimit = 262144

type diagnosticDrain struct {
	writer    io.Writer
	remaining int
}

func (w *diagnosticDrain) Write(raw []byte) (int, error) {
	n := len(raw)
	if w.remaining > 0 {
		keep := min(n, w.remaining)
		written, e := w.writer.Write(raw[:keep])
		w.remaining -= written
		if e != nil {
			return written, e
		}
		if written != keep {
			return written, io.ErrShortWrite
		}
	}
	// Keep draining after the diagnostic prefix is full. A verbose child must
	// neither block on stderr nor receive SIGPIPE because its log was capped.
	return n, nil
}

// Guard launches an independent cleanup owner. Only this process holds the
// private pipe writer; SIGKILL therefore still starts the guardian's cleanup.
func Guard(ctx context.Context, command, env []string, state string, duration time.Duration) error {
	return GuardWithStarted(ctx, command, env, state, duration, nil)
}

// GuardWithStarted reports the exact private guardian child immediately after
// Start succeeds. The callback is observational and must not control cleanup.
func GuardWithStarted(ctx context.Context, command, env []string, state string, duration time.Duration, started func(int, []string)) error {
	if len(command) == 0 || !filepath.IsAbs(command[0]) || duration < 0 || duration > time.Hour {
		return errors.New("invalid guarded command")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return e
	}
	reader, writer, e := os.Pipe()
	if e != nil {
		return e
	}
	defer reader.Close()
	defer writer.Close()
	var dir *safeio.Directory
	var log *os.File
	name := ""
	args := []string{"--guardian", "--owner-fd", "3", "--duration", strconv.FormatFloat(duration.Seconds(), 'f', 3, 64)}
	extras := []*os.File{reader}
	if state != "" {
		dir, e = safeio.OpenDir(state, false)
		if e != nil {
			return e
		}
		defer dir.Close()
		entropy := make([]byte, 16)
		if _, e = rand.Read(entropy); e != nil {
			return e
		}
		name = ".guardian-" + hex.EncodeToString(entropy) + ".log"
		fd, e := syscall.Openat(dir.FD, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if e != nil {
			return e
		}
		log = os.NewFile(uintptr(fd), name)
		defer log.Close()
		dfd, e := syscall.Dup(dir.FD)
		if e != nil {
			return e
		}
		df := os.NewFile(uintptr(dfd), state)
		defer df.Close()
		extras = append(extras, df)
		args = append(args, "--diagnostic-dir-fd", "4", "--diagnostic-name", name)
	} else {
		log, e = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if e != nil {
			return e
		}
		defer log.Close()
	}
	args = append(args, "--")
	args = append(args, command...)
	cmd := exec.Command(exe, args...)
	cmd.Env = env
	cmd.ExtraFiles = extras
	cmd.Stdout = nil
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if e = cmd.Start(); e != nil {
		if dir != nil {
			syscall.Unlinkat(dir.FD, name)
		}
		return e
	}
	if started != nil {
		started(cmd.Process.Pid, append([]string(nil), cmd.Args...))
	}
	reader.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		if e != nil {
			return fmt.Errorf("weather guardian failed: %w", e)
		}
		return nil
	case <-ctx.Done():
		writer.Close()
	}
	select {
	case e = <-done:
		return e
	case <-time.After(GuardianCleanupGrace + 10*time.Second):
		return errors.New("weather guardian exceeded cleanup deadline; cleanup owner remains running")
	}
}

var diagnosticPattern = regexp.MustCompile(`^\.guardian-[a-f0-9]{32}\.log$`)

func finalizeDiagnostic(directory int, name string, success bool) error {
	if directory < 0 {
		return nil
	}
	if !diagnosticPattern.MatchString(name) {
		return errors.New("invalid guardian diagnostic name")
	}
	var parent, file, current syscall.Stat_t
	if e := syscall.Fstat(directory, &parent); e != nil {
		return e
	}
	if e := syscall.Fstat(2, &file); e != nil {
		return e
	}
	fd, e := syscall.Openat(directory, name, syscall.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	if e = syscall.Fstat(fd, &current); e != nil {
		return e
	}
	if parent.Mode&syscall.S_IFMT != syscall.S_IFDIR || parent.Uid != uint32(os.Getuid()) || parent.Mode&077 != 0 || file.Mode&syscall.S_IFMT != syscall.S_IFREG || file.Uid != uint32(os.Getuid()) || file.Mode&077 != 0 || file.Nlink != 1 || file.Dev != current.Dev || file.Ino != current.Ino {
		return errors.New("guardian diagnostic ownership changed")
	}
	if success {
		e = syscall.Unlinkat(directory, name)
	} else {
		if e = syscall.Fsync(2); e != nil {
			return e
		}
		e = syscall.Renameat(directory, name, directory, "guardian-last-error.log")
	}
	if e != nil {
		return e
	}
	return syscall.Fsync(directory)
}

// RunGuardian is private executable dispatch, using a passed pipe rather than
// any externally supplied PID to select its owned process tree.
func RunGuardian(args []string) (result int) {
	flags := flag.NewFlagSet("guardian", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	ownerFD := flags.Int("owner-fd", -1, "")
	duration := flags.Float64("duration", 0, "")
	directory := flags.Int("diagnostic-dir-fd", -1, "")
	name := flags.String("diagnostic-name", "", "")
	if flags.Parse(args) != nil || *ownerFD < 3 || math.IsNaN(*duration) || math.IsInf(*duration, 0) || *duration < 0 || *duration > 3600 || len(flags.Args()) == 0 || !filepath.IsAbs(flags.Args()[0]) {
		return 2
	}
	owner := os.NewFile(uintptr(*ownerFD), "guardian-owner")
	if owner == nil {
		return 2
	}
	defer owner.Close()
	syscall.CloseOnExec(*ownerFD)
	info, e := owner.Stat()
	if e != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 2
	}
	if *directory >= 0 {
		syscall.CloseOnExec(*directory)
		defer syscall.Close(*directory)
	}
	defer func() {
		if e := finalizeDiagnostic(*directory, *name, result == 0); e != nil {
			result = 1
		}
	}()
	// Validate EOF before creating the UI: a dead launcher cannot create a late surface.
	if e = syscall.SetNonblock(*ownerFD, true); e != nil {
		return 2
	}
	probe := make([]byte, 1)
	n, e := syscall.Read(*ownerFD, probe)
	if n == 0 && e == nil {
		return 0
	}
	if n > 0 || e != syscall.EAGAIN {
		return 2
	}
	syscall.SetNonblock(*ownerFD, false)
	diagnosticR, diagnosticW, e := os.Pipe()
	if e != nil {
		return 1
	}
	defer diagnosticR.Close()
	defer diagnosticW.Close()
	drained := make(chan struct{})
	go func() {
		if _, e := io.Copy(&diagnosticDrain{os.Stderr, GuardianDiagnosticLimit}, diagnosticR); e != nil {
			// Even a full/unwritable diagnostic filesystem must not stall the
			// service's cleanup while it reports its own failure.
			io.Copy(io.Discard, diagnosticR)
		}
		close(drained)
	}()
	defer func() {
		diagnosticW.Close()
		select {
		case <-drained:
		case <-time.After(time.Second):
			diagnosticR.Close()
			<-drained
		}
	}()
	child, e := Start(flags.Args(), os.Environ(), "", nil, nil, diagnosticW)
	diagnosticW.Close()
	if e != nil {
		fmt.Fprintf(os.Stderr, "guardian start: %.512s\n", e)
		return 1
	}
	ownerDone := make(chan struct{})
	go func() { io.ReadFull(owner, probe); close(ownerDone) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	var timer <-chan time.Time
	if *duration > 0 {
		t := time.NewTimer(time.Duration(*duration * float64(time.Second)))
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ownerDone:
	case <-signals:
	case <-timer:
	case <-child.Done():
	}
	ctx, cancel := context.WithTimeout(context.Background(), GuardianCleanupGrace)
	defer cancel()
	// The service owns Qt, unlike the former Qt-owned bridge. Let the service
	// finish effects cleanup and acknowledge frontend shutdown before sending
	// any signal to its remaining descendants. The unreaped service anchors
	// the group through this entire phase, including an already-crashed leader.
	if e = child.StopLeaderThenGroup(ctx, GuardianLeaderGrace); e != nil {
		fmt.Fprintf(os.Stderr, "guardian cleanup: %.1024s\n", e)
		return 1
	}
	return 0
}
