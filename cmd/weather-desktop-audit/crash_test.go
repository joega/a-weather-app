package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type fakeWorkerPin struct {
	alive           bool
	signals, closes int
	signal          syscall.Signal
}

func (p *fakeWorkerPin) Alive() (bool, error) { return p.alive, nil }
func (p *fakeWorkerPin) Signal(signal syscall.Signal) error {
	p.signals++
	p.signal = signal
	return nil
}
func (p *fakeWorkerPin) Close() error { p.closes++; return nil }

func workerFixture() (*audit, processIdentity, processIdentity) {
	pid := os.Getpid() + 100000
	guardianPID := pid - 1
	guardianExe := "/owned/root/build/weather-desktop-audit"
	a := &audit{root: "/owned/root", state: "/private/audit/state", executable: "/owned/root/build/a-weather-app", instance: "abc_1_2", output: "DP-1", servicePID: pid, serviceStart: "101", guardianPID: guardianPID, guardianStart: "77", guardianExe: guardianExe, crashWorker: true, guardDone: make(chan error, 1), compositor: processIdentity{PID: pid + 2}, processes: map[string]processIdentity{}}
	a.serviceArgs = []string{a.executable, "--service", "--headless", "--offline", "--state-dir", a.state, "--root", a.root, "--instance", a.instance, "--output", a.output, "--duration", "300"}
	a.guardianArgs = []string{guardianExe, "--guardian", "--owner-fd", "3", "--duration", "300.000", "--diagnostic-dir-fd", "4", "--diagnostic-name", ".guardian-0123456789abcdef0123456789abcdef.log", "--"}
	a.guardianArgs = append(a.guardianArgs, a.serviceArgs...)
	guardian := processIdentity{PID: guardianPID, Parent: os.Getpid(), StartTime: a.guardianStart, State: "S", Executable: guardianExe, Arguments: append([]string(nil), a.guardianArgs...), UID: uint32(os.Geteuid())}
	service := processIdentity{PID: pid, Parent: guardianPID, StartTime: "101", State: "S", Executable: a.executable, Arguments: append([]string(nil), a.serviceArgs...), UID: uint32(os.Geteuid())}
	worker := processIdentity{PID: pid + 1, Parent: pid, StartTime: "202", State: "S", Executable: a.executable, Arguments: []string{a.executable, "--effects-worker", "--root", a.root, "--instance", a.instance, "--output", a.output}, UID: uint32(os.Geteuid())}
	a.processes[fmt.Sprintf("%d:%s", guardian.PID, guardian.StartTime)] = guardian
	a.processes[fmt.Sprintf("%d:%s", service.PID, service.StartTime)] = service
	a.processes[fmt.Sprintf("%d:%s", worker.PID, worker.StartTime)] = worker
	return a, service, worker
}

func TestWorkerCrashRequiresCompleteOwnedIdentityBeforePinning(t *testing.T) {
	cases := map[string]func(*audit, *processIdentity, *processIdentity){
		"normal mode":                func(a *audit, s, w *processIdentity) { a.crashWorker = false },
		"already injected":           func(a *audit, s, w *processIdentity) { a.crashInjected = true },
		"guardian finished":          func(a *audit, s, w *processIdentity) { a.guardFinished = true },
		"no guardian":                func(a *audit, s, w *processIdentity) { a.guardDone = nil },
		"missing guardian PID":       func(a *audit, s, w *processIdentity) { a.guardianPID = 0 },
		"guardian launch changed":    func(a *audit, s, w *processIdentity) { a.guardianArgs[1] = "--other" },
		"compositor":                 func(a *audit, s, w *processIdentity) { w.PID = a.compositor.PID },
		"own service":                func(a *audit, s, w *processIdentity) { w.PID = s.PID },
		"audit process":              func(a *audit, s, w *processIdentity) { w.PID = os.Getpid() },
		"init":                       func(a *audit, s, w *processIdentity) { w.PID = 1 },
		"foreign parent":             func(a *audit, s, w *processIdentity) { w.Parent++ },
		"service not guardian child": func(a *audit, s, w *processIdentity) { s.Parent++ },
		"foreign uid":                func(a *audit, s, w *processIdentity) { w.UID++ },
		"renderer executable": func(a *audit, s, w *processIdentity) {
			w.Executable = "/owned/root/native/atmosphere/a-weather-app-atmosphere"
		},
		"different compositor instance": func(a *audit, s, w *processIdentity) { w.Arguments[5] = "fff_9_9" },
		"different root":                func(a *audit, s, w *processIdentity) { w.Arguments[3] = "/foreign/root" },
		"different output":              func(a *audit, s, w *processIdentity) { w.Arguments[7] = "DP-2" },
		"extra arguments":               func(a *audit, s, w *processIdentity) { w.Arguments = append(w.Arguments, "--root", "/foreign") },
		"missing starttime":             func(a *audit, s, w *processIdentity) { w.StartTime = "" },
		"exited worker":                 func(a *audit, s, w *processIdentity) { w.State = "Z" },
		"uncaptured worker":             func(a *audit, s, w *processIdentity) { delete(a.processes, fmt.Sprintf("%d:%s", w.PID, w.StartTime)) },
		"service identity":              func(a *audit, s, w *processIdentity) { s.StartTime = "changed" },
		"service state":                 func(a *audit, s, w *processIdentity) { s.Arguments[5] = "/foreign/state" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a, service, worker := workerFixture()
			change(a, &service, &worker)
			injectedBefore := a.crashInjected
			pins := 0
			e := a.signalOwnedWorker(context.Background(), service, worker, func(int) (processIdentity, error) {
				t.Fatal("inspection after rejected ownership")
				return processIdentity{}, nil
			}, func(int) (workerPin, error) { pins++; return &fakeWorkerPin{alive: true}, nil }, func() error { return nil })
			if e == nil || pins != 0 || a.crashInjected != injectedBefore {
				t.Fatal("unowned signal admitted", e, pins)
			}
		})
	}
}

func TestWorkerIdentityRevalidatedAfterPinAndBeforeSignal(t *testing.T) {
	for _, afterGenerationCheck := range []bool{false, true} {
		t.Run(fmt.Sprint(afterGenerationCheck), func(t *testing.T) {
			a, service, worker := workerFixture()
			pin := &fakeWorkerPin{alive: true}
			reads := 0
			inspect := func(pid int) (processIdentity, error) {
				reads++
				if pid == a.guardianPID {
					return a.processes[fmt.Sprintf("%d:%s", a.guardianPID, a.guardianStart)], nil
				}
				if pid == service.PID {
					return service, nil
				}
				current := worker
				if !afterGenerationCheck || reads > 2 {
					current.StartTime = "reused"
				}
				return current, nil
			}
			e := a.signalOwnedWorker(context.Background(), service, worker, inspect, func(int) (workerPin, error) { return pin, nil }, func() error { return nil })
			if e == nil || pin.signals != 0 || pin.closes != 1 || a.crashInjected {
				t.Fatal("identity drift admitted", e, pin)
			}
		})
	}
}

func TestPinnedCrashSignalIsExactAndOptIn(t *testing.T) {
	a, service, worker := workerFixture()
	pin := &fakeWorkerPin{alive: true}
	checks := 0
	inspect := func(pid int) (processIdentity, error) {
		if pid == a.guardianPID {
			return a.processes[fmt.Sprintf("%d:%s", a.guardianPID, a.guardianStart)], nil
		}
		if pid == service.PID {
			return service, nil
		}
		if pid == worker.PID {
			return worker, nil
		}
		t.Fatal("foreign PID inspected")
		return processIdentity{}, nil
	}
	e := a.signalOwnedWorker(context.Background(), service, worker, inspect, func(pid int) (workerPin, error) {
		if pid != worker.PID {
			t.Fatal("foreign PID pinned")
		}
		return pin, nil
	}, func() error { checks++; return nil })
	if e != nil || pin.signals != 1 || pin.signal != syscall.SIGKILL || pin.closes != 1 || checks != 1 || !a.crashInjected {
		t.Fatal(e, pin, checks)
	}
}

func TestPinAndGenerationFailuresNeverFallBackToPIDSignal(t *testing.T) {
	for _, failure := range []string{"pin unavailable", "generation changed", "pinned process exited", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			a, service, worker := workerFixture()
			pin := &fakeWorkerPin{alive: failure != "pinned process exited"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if failure == "canceled" {
				cancel()
			}
			inspect := func(pid int) (processIdentity, error) {
				if pid == a.guardianPID {
					return a.processes[fmt.Sprintf("%d:%s", a.guardianPID, a.guardianStart)], nil
				}
				if pid == service.PID {
					return service, nil
				}
				return worker, nil
			}
			e := a.signalOwnedWorker(ctx, service, worker, inspect, func(int) (workerPin, error) {
				if failure == "pin unavailable" {
					return nil, syscall.ENOSYS
				}
				return pin, nil
			}, func() error {
				if failure == "generation changed" {
					return errors.New("generation changed")
				}
				return nil
			})
			if e == nil || pin.signals != 0 || a.crashInjected {
				t.Fatal("unsafe fallback", e, pin)
			}
		})
	}
}

func TestReadOnlyPidfdPinCanObserveSelfWithoutSignaling(t *testing.T) {
	pin, e := openWorkerPin(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	defer pin.Close()
	if live, e := pin.Alive(); e != nil || !live {
		t.Fatal(live, e)
	}
}

func TestCrashFlagStillRequiresActivation(t *testing.T) {
	for _, args := range [][]string{{"--crash-worker"}, {"--crash-worker", "--root", "/missing"}, {"--crash-worker=false", "--root", "/missing"}} {
		if e := run(args); e == nil {
			t.Fatal("crash flag bypassed activation", args)
		}
	}
	missing := filepath.Join(t.TempDir(), "missing-root")
	if e := run([]string{"--root", missing, "--activate-native", "--crash-worker"}); !os.IsNotExist(e) {
		t.Fatal("valid crash flags were not recognized before harmless missing-root refusal", e)
	}
}
