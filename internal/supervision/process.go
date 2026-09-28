// Package supervision owns child process groups until after their final signal.
// Linux pidfds observe death without reaping the group leader (the PID anchor).
package supervision

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type Process struct {
	Cmd      *exec.Cmd
	pidfd    int
	mu       sync.Mutex
	reaped   bool
	group    bool
	exitOnce sync.Once
	exited   chan struct{}
}

func Start(command, env []string, dir string, stdin, stdout, stderr *os.File) (*Process, error) {
	return start(command, env, dir, stdin, stdout, stderr, true)
}

// StartChild retains the parent's owned session, so its independent guardian
// can remove the child even if this immediate parent crashes.
func StartChild(command, env []string, dir string, stdin, stdout, stderr *os.File) (*Process, error) {
	return start(command, env, dir, stdin, stdout, stderr, false)
}
func start(command, env []string, dir string, stdin, stdout, stderr *os.File, group bool) (*Process, error) {
	if len(command) == 0 || !strings.HasPrefix(command[0], "/") {
		return nil, errors.New("absolute child executable required")
	}
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	// A typed nil *os.File stored in an io.Reader/Writer interface is not nil:
	// os/exec would pass an invalid descriptor instead of opening /dev/null.
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	fd := -1
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: group, PidFD: &fd}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Process{Cmd: cmd, pidfd: fd, group: group}, nil
}
func (p *Process) PID() int { return p.Cmd.Process.Pid }

// Done observes pidfd readiness without waitpid: the exited leader stays an
// unreaped PID anchor until Cleanup finishes signaling its owned descendants.
// A duplicate descriptor is registered with Go's runtime poller, so an idle
// child requires neither a polling ticker nor a dedicated blocked OS thread.
func (p *Process) Done() <-chan struct{} {
	p.exitOnce.Do(func() {
		p.exited = make(chan struct{})
		p.mu.Lock()
		if p.reaped {
			p.mu.Unlock()
			close(p.exited)
			return
		}
		fd, e := syscall.Dup(p.pidfd)
		p.mu.Unlock()
		if e == nil {
			syscall.CloseOnExec(fd)
		}
		go func() {
			defer close(p.exited)
			if e == nil {
				if syscall.SetNonblock(fd, true) == nil {
					file := os.NewFile(uintptr(fd), "child-exit-pidfd")
					defer file.Close()
					if raw, err := file.SyscallConn(); err == nil {
						err = raw.Read(func(value uintptr) bool {
							poll := struct {
								FD              int32
								Events, Revents int16
							}{int32(value), 1, 0}
							_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 0)
							return errno == 0 && poll.Revents != 0
						})
						if err == nil {
							return
						}
					}
				} else {
					syscall.Close(fd)
				}
			}
			// Old kernels or descriptor exhaustion may lack a usable pidfd.
			// Keep correctness, but use polling only for this exceptional path.
			for p.Alive() {
				time.Sleep(100 * time.Millisecond)
			}
		}()
	})
	return p.exited
}

func (p *Process) WaitExit(ctx context.Context) error {
	select {
	case <-p.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *Process) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return false
	}
	if p.pidfd >= 0 {
		f := struct {
			FD              int32
			Events, Revents int16
		}{int32(p.pidfd), 1, 0}
		_, _, e := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&f)), 1, 0)
		return e != 0 || f.Revents == 0
	}
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.PID()))
	if e != nil {
		return false
	}
	fields := strings.Fields(string(raw[strings.LastIndex(string(raw), ")")+1:]))
	return len(fields) > 0 && fields[0] != "Z"
}
func (p *Process) Signal(signal syscall.Signal) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return os.ErrProcessDone
	}
	if p.pidfd < 0 {
		return errors.New("pidfd unavailable")
	}
	_, _, e := syscall.Syscall6(424, uintptr(p.pidfd), uintptr(signal), 0, 0, 0, 0)
	if e != 0 {
		return e
	}
	return nil
}

// StopLeaderThenGroup lets an owned service orchestrate its children's graceful
// shutdown. If the service dies, group cleanup still has its unreaped PID anchor.
func (p *Process) StopLeaderThenGroup(ctx context.Context, grace time.Duration) error {
	if !p.group {
		return p.Cleanup(ctx, grace)
	}
	if p.Alive() {
		if e := p.Signal(syscall.SIGTERM); e != nil && e != syscall.ESRCH {
			return errors.Join(e, p.Cleanup(ctx, 0))
		}
	}
	wait, cancel := context.WithTimeout(ctx, grace)
	p.WaitExit(wait)
	cancel()
	if p.Alive() {
		return p.Cleanup(ctx, 0)
	}
	return p.Cleanup(ctx, 3*time.Second)
}
func members(group int) ([]int, error) {
	dir, e := os.Open("/proc")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	deadline := time.Now().Add(time.Second)
	var result []int
	count := 0
	for {
		names, e := dir.Readdirnames(128)
		if e != nil && e != io.EOF {
			return nil, e
		}
		for _, name := range names {
			count++
			if count > 65536 || time.Now().After(deadline) {
				return nil, errors.New("process inventory bound exceeded")
			}
			pid, e := strconv.Atoi(name)
			if e != nil {
				continue
			}
			f, e := os.OpenFile("/proc/"+name+"/stat", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				if os.IsNotExist(e) {
					continue
				}
				return nil, e
			}
			info, e := f.Stat()
			if e != nil {
				f.Close()
				continue
			}
			if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
				f.Close()
				continue
			}
			raw, e := io.ReadAll(io.LimitReader(f, 4097))
			f.Close()
			if e != nil {
				return nil, e
			}
			if len(raw) > 4096 {
				return nil, errors.New("process metadata bound exceeded")
			}
			i := strings.LastIndex(string(raw), ")")
			if i < 0 {
				return nil, errors.New("invalid process metadata")
			}
			fields := strings.Fields(string(raw[i+1:]))
			if len(fields) < 4 {
				return nil, errors.New("invalid process metadata")
			}
			g, _ := strconv.Atoi(fields[2])
			s, _ := strconv.Atoi(fields[3])
			if g == group && s == group && fields[0] != "Z" {
				result = append(result, pid)
			}
		}
		if e == io.EOF {
			break
		}
	}
	return result, nil
}

// Cleanup never reaps a leader until its owned group has received its final
// signal. Observation failures force teardown and remain visible to the caller.
func (p *Process) Cleanup(ctx context.Context, grace time.Duration) error {
	p.mu.Lock()
	if p.reaped {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()
	var errs []error
	signalGroup := func(sig syscall.Signal) {
		var e error
		if p.group {
			e = syscall.Kill(-p.PID(), sig)
		} else {
			e = p.Signal(sig)
		}
		if e != nil && e != syscall.ESRCH {
			errs = append(errs, e)
		}
	}
	signalGroup(syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	forced := false
	// kill(-pgid, 0) also succeeds for an unreaped zombie-only group. Inspect
	// membership while retaining the leader as the group identity anchor.
	for {
		live := []int{}
		var e error
		if p.group {
			live, e = members(p.PID())
		} else if p.Alive() {
			live = append(live, p.PID())
		}
		if e != nil {
			errs = append(errs, e)
			forced = true
			break
		}
		if len(live) == 0 {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			forced = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if forced {
		signalGroup(syscall.SIGKILL)
		errs = append(errs, errors.New("owned child required forced termination"))
	}
	// Also kill abandoned descendants after a clean leader exit; still anchored.
	signalGroup(syscall.SIGKILL)
	done := make(chan error, 1)
	go func() { done <- p.Cmd.Wait() }()
	select {
	case e := <-done:
		p.mu.Lock()
		p.reaped = true
		if p.pidfd >= 0 {
			syscall.Close(p.pidfd)
			p.pidfd = -1
		}
		p.mu.Unlock()
		if e != nil {
			var x *exec.ExitError
			if !errors.As(e, &x) || x.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
				errs = append(errs, e)
			}
		}
	case <-time.After(3 * time.Second):
		errs = append(errs, errors.New("owned child reap deadline"))
	}
	return errors.Join(errs...)
}

type limitWriter struct {
	data   []byte
	limit  int
	cancel context.CancelFunc
	err    error
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > w.limit {
		w.err = errors.New("command output byte limit")
		w.cancel()
		return 0, w.err
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

// Run bounds both streams while reading them concurrently and kills the owned
// group before reaping, including descendants that inherited a pipe.
func Run(ctx context.Context, command, env []string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	outR, outW, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer outR.Close()
	defer outW.Close()
	errR, errW, e := os.Pipe()
	if e != nil {
		return nil, e
	}
	defer errR.Close()
	defer errW.Close()
	p, e := Start(command, env, "", nil, outW, errW)
	if e != nil {
		return nil, e
	}
	outW.Close()
	errW.Close()
	out := &limitWriter{limit: limit, cancel: cancel}
	errout := &limitWriter{limit: 8192, cancel: cancel}
	done := make(chan struct{}, 2)
	go func() { io.Copy(out, outR); done <- struct{}{} }()
	go func() { io.Copy(errout, errR); done <- struct{}{} }()
	exit := p.Done()
	exited := false
	reads := 0
	for !exited || reads < 2 {
		select {
		case <-ctx.Done():
			goto finished
		case <-done:
			reads++
		case <-exit:
			exited = true
			exit = nil
		}
	}
finished:
	// Close streams on timeout so no reader goroutine or pipe can outlive Run.
	cleanupCtx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	cleanup := p.Cleanup(cleanupCtx, 0)
	outR.Close()
	errR.Close()
	for reads < 2 {
		<-done
		reads++
	}
	if out.err != nil {
		return nil, out.err
	}
	if errout.err != nil {
		return nil, errout.err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if cleanup != nil {
		return nil, fmt.Errorf("command failed: %w: %.512s", cleanup, errout.data)
	}
	return out.data, nil
}
