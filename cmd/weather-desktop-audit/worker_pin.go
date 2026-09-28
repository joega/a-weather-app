package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"github.com/joega/a-weather-app/internal/safeio"
)

type workerPin interface {
	Alive() (bool, error)
	Signal(syscall.Signal) error
	Close() error
}

type pidfdWorker struct{ fd, pid int }

func openWorkerPin(pid int) (workerPin, error) {
	// Linux pidfd_open, shared by the supported Linux x86-64 native ABI.
	fd, _, errno := syscall.Syscall(434, uintptr(pid), 0, 0)
	if errno != 0 {
		return nil, fmt.Errorf("pidfd_open required; no PID-only fallback: %w", errno)
	}
	p := &pidfdWorker{int(fd), pid}
	syscall.CloseOnExec(p.fd)
	if live, e := p.Alive(); e != nil || !live {
		p.Close()
		return nil, errors.Join(errors.New("worker pin is not live"), e)
	}
	return p, nil
}

func (p *pidfdWorker) Alive() (bool, error) {
	raw, e := safeio.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", p.fd), 4096)
	if e != nil {
		return false, e
	}
	matched := false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Pid:") {
			pid, e := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Pid:")))
			if e != nil || pid != p.pid {
				return false, errors.New("pidfd process identity changed or exited")
			}
			matched = true
		}
	}
	if !matched {
		return false, errors.New("pidfd identity unavailable")
	}
	poll := struct {
		FD              int32
		Events, Revents int16
	}{int32(p.fd), 1, 0}
	_, _, errno := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 0)
	if errno != 0 {
		return false, errno
	}
	return poll.Revents == 0, nil
}

func (p *pidfdWorker) Signal(signal syscall.Signal) error {
	if signal != syscall.SIGKILL {
		return errors.New("audit worker pin permits only its explicit crash signal")
	}
	_, _, errno := syscall.Syscall6(424, uintptr(p.fd), uintptr(signal), 0, 0, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
func (p *pidfdWorker) Close() error { return syscall.Close(p.fd) }

func liveProcess(p processIdentity) bool {
	return p.State == "R" || p.State == "S" || p.State == "D" || p.State == "I"
}

func sameProcess(a, b processIdentity) bool {
	return a.PID == b.PID && a.Parent == b.Parent && a.StartTime == b.StartTime && a.UID == b.UID && a.Executable == b.Executable && reflect.DeepEqual(a.Arguments, b.Arguments)
}

func (a *audit) authorizeWorker(service, worker processIdentity) error {
	if !a.crashWorker || a.crashInjected || a.guardDone == nil || a.guardFinished || a.serviceStart == "" || len(a.serviceArgs) == 0 || a.guardianPID <= 1 || a.guardianStart == "" {
		return errors.New("explicit crash mode and live owned guardian/service required")
	}
	guardian, capturedGuardian := a.processes[fmt.Sprintf("%d:%s", a.guardianPID, a.guardianStart)]
	if !capturedGuardian || guardian.Parent != os.Getpid() || guardian.Executable != a.guardianExe || guardian.UID != uint32(os.Geteuid()) || !liveProcess(guardian) || !reflect.DeepEqual(guardian.Arguments, a.guardianArgs) {
		return errors.New("launched guardian ownership proof missing")
	}
	if service.PID != a.servicePID || service.PID <= 1 || service.PID == a.compositor.PID || service.StartTime != a.serviceStart || service.Parent != a.guardianPID || service.Executable != a.executable || service.UID != uint32(os.Geteuid()) || !liveProcess(service) || !reflect.DeepEqual(service.Arguments, a.serviceArgs) || !hasPair(service.Arguments, "--state-dir", a.state) {
		return errors.New("service ownership proof failed")
	}
	wanted := []string{a.executable, "--effects-worker", "--root", a.root, "--instance", a.instance, "--output", a.output}
	if worker.PID <= 1 || worker.PID == os.Getpid() || worker.PID == service.PID || worker.PID == a.compositor.PID || worker.Parent != service.PID || worker.StartTime == "" || worker.UID != uint32(os.Geteuid()) || worker.Executable != a.executable || !liveProcess(worker) || !reflect.DeepEqual(worker.Arguments, wanted) {
		return errors.New("exact direct-child effects worker ownership proof failed")
	}
	for _, identity := range []processIdentity{service, worker} {
		observed, ok := a.processes[fmt.Sprintf("%d:%s", identity.PID, identity.StartTime)]
		if !ok || !sameProcess(observed, identity) {
			return errors.New("process was not captured in this owned service tree")
		}
	}
	return nil
}

// signalOwnedWorker is injectable for refusal/drift tests. Production only uses
// pidfd handles: no syscall.Kill, Process.Kill or process-group signal occurs.
func (a *audit) signalOwnedWorker(ctx context.Context, service, worker processIdentity, inspect func(int) (processIdentity, error), open func(int) (workerPin, error), beforeSignal func() error) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := a.authorizeWorker(service, worker); e != nil {
		return e
	}
	pin, e := open(worker.PID)
	if e != nil {
		return e
	}
	defer pin.Close()
	revalidate := func() error {
		currentService, e := inspect(service.PID)
		if e != nil {
			return e
		}
		currentWorker, e := inspect(worker.PID)
		if e != nil {
			return e
		}
		if !sameProcess(service, currentService) || !sameProcess(worker, currentWorker) {
			return errors.New("worker or owning service identity drifted after pidfd pinning")
		}
		if e = a.authorizeWorker(currentService, currentWorker); e != nil {
			return e
		}
		currentGuardian, e := inspect(a.guardianPID)
		if e != nil || !sameProcess(a.processes[fmt.Sprintf("%d:%s", a.guardianPID, a.guardianStart)], currentGuardian) || !liveProcess(currentGuardian) {
			return errors.Join(errors.New("launched guardian identity drifted"), e)
		}
		if alive, e := pin.Alive(); e != nil || !alive {
			return errors.Join(errors.New("pinned worker already exited"), e)
		}
		return ctx.Err()
	}
	if e = revalidate(); e != nil {
		return e
	}
	if e = beforeSignal(); e != nil {
		return e
	}
	if e = revalidate(); e != nil {
		return e
	}
	if e = pin.Signal(syscall.SIGKILL); e != nil {
		return e
	}
	a.crashInjected = true
	return nil
}
