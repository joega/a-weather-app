package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
)

type audit struct {
	root, state, executable, socket, instance, output string
	evidence                                          *safeio.Directory
	sequence, bytes                                   int
	compositor                                        processIdentity
	baseline                                          []any
	servicePID                                        int
	serviceStart                                      string
	serviceArgs                                       []string
	guardianPID                                       int
	guardianStart                                     string
	guardianExe                                       string
	guardianArgs                                      []string
	states                                            []string
	processes                                         map[string]processIdentity
	runtimeDirs                                       map[string]bool
	retainedDirs                                      map[string]directoryEvidence
	crashWorker, crashInjected                        bool
	previewOnly                                       bool
	previewRounds                                     int
	crashRecoveryVerified, crashRestartVerified       bool
	expectedGuardianFailure                           bool
	guardCancel                                       context.CancelFunc
	guardDone                                         chan error
	guardErr                                          error
	guardFinished                                     bool
}

func (a *audit) record(kind string, value object) error {
	a.sequence++
	if a.sequence > 512 {
		return errors.New("audit evidence record budget exceeded")
	}
	envelope := object{"schema_version": 1, "sequence": a.sequence, "at": time.Now().UTC().Format(time.RFC3339Nano), "kind": kind, "data": value}
	raw, e := json.Marshal(envelope)
	if e != nil {
		return e
	}
	if len(raw) > recordLimit || a.bytes+len(raw) > evidenceLimit {
		return errors.New("audit evidence byte budget exceeded; effects cleanup will still run")
	}
	a.bytes += len(raw)
	return a.evidence.Write(fmt.Sprintf("%04d-%s.json", a.sequence, kind), envelope, recordLimit)
}

func (a *audit) request(ctx context.Context, budget time.Duration, op string, extra object) (object, error) {
	return a.requestOutcome(ctx, budget, op, extra, true)
}

func (a *audit) requestOutcome(ctx context.Context, budget time.Duration, op string, extra object, expectedOK bool) (object, error) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	request := object{"op": op, "version": float64(1), "request_id": float64(1)}
	for key, value := range extra {
		request[key] = value
	}
	reply, e := ipc.CallVerified(ctx, a.socket, request, a.verifyServicePeer)
	entry := object{"request": request, "reply": reply}
	if e != nil {
		entry["error"] = e.Error()
	}
	logErr := a.record("ipc", entry)
	if e != nil {
		return nil, errors.Join(e, logErr)
	}
	if reply["ok"] != expectedOK {
		return reply, errors.Join(fmt.Errorf("%s unexpected acknowledgement ok=%v: %v", op, reply["ok"], reply["error"]), logErr)
	}
	return reply, logErr
}

func (a *audit) start(ctx context.Context) error {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(a.state)))
	a.socket = filepath.Join(runtime, fmt.Sprintf("a-weather-app-%d-%s", os.Geteuid(), hash[:16]), "service.sock")
	if _, e := os.Lstat(filepath.Dir(a.socket)); !os.IsNotExist(e) {
		return errors.New("fresh audit runtime path unexpectedly exists")
	}
	command := []string{a.executable, "--service", "--headless", "--offline", "--state-dir", a.state, "--root", a.root, "--instance", a.instance, "--output", a.output, "--duration", "300"}
	a.serviceArgs = append([]string(nil), command...)
	if e := a.record("launch", object{"command": command, "socket": a.socket, "guardian": "independent EOF cleanup owner", "maximum_service_seconds": 300}); e != nil {
		return e
	}
	guard, cancel := context.WithCancel(context.Background())
	a.guardCancel = cancel
	a.guardDone = make(chan error, 1)
	guardianStarted := make(chan struct {
		pid  int
		args []string
	}, 1)
	go func() {
		a.guardDone <- supervision.GuardWithStarted(guard, command, environment(), a.evidence.Path, 300*time.Second, func(pid int, args []string) {
			guardianStarted <- struct {
				pid  int
				args []string
			}{pid, args}
		})
	}()
	startup, done := context.WithTimeout(ctx, 15*time.Second)
	defer done()
	// Bind the exact guardian identity before probing the service socket. The
	// service may become ready before the launch callback is scheduled; dialing
	// first would then spuriously fail closed with an unbound guardian PID.
	select {
	case child := <-guardianStarted:
		if e := a.bindGuardian(child.pid, child.args); e != nil {
			return e
		}
		guardianStarted = nil
	case e := <-a.guardDone:
		a.guardFinished = true
		a.guardErr = e
		return errors.Join(errors.New("owned service exited before readiness"), e)
	case <-startup.Done():
		return fmt.Errorf("owned guardian startup: %w", startup.Err())
	}
	for {
		select {
		case child := <-guardianStarted:
			if e := a.bindGuardian(child.pid, child.args); e != nil {
				return e
			}
			guardianStarted = nil
		case e := <-a.guardDone:
			a.guardFinished = true
			a.guardErr = e
			return errors.Join(errors.New("owned service exited before readiness"), e)
		default:
		}
		dial, stop := context.WithTimeout(startup, 250*time.Millisecond)
		connection, e := (&net.Dialer{}).DialContext(dial, "unix", a.socket)
		stop()
		if e == nil {
			conn := connection.(*net.UnixConn)
			if a.guardianPID == 0 {
				conn.Close()
				return errors.New("owned guardian identity unavailable")
			}
			a.servicePID, e = ipc.PeerPID(conn)
			conn.Close()
			if e != nil {
				return e
			}
			if e = a.captureTree(); e != nil {
				return e
			}
			return nil
		}
		if e = wait(startup, 100*time.Millisecond); e != nil {
			return fmt.Errorf("owned service startup: %w", e)
		}
	}
}

func afterDelimiter(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func (a *audit) bindGuardian(pid int, args []string) error {
	if pid <= 1 || pid == os.Getpid() || len(args) < 3 || args[1] != "--guardian" || !reflect.DeepEqual(afterDelimiter(args), a.serviceArgs) {
		return errors.New("guardian launch identity invalid")
	}
	identity, e := inspectProcess(pid)
	if e != nil {
		return e
	}
	exe, e := filepath.EvalSymlinks(args[0])
	// Start can report the child before exec has published its final cmdline in
	// /proc. Retry only that transient mismatch, and only while the same owned
	// process still has its expected executable, parent, UID and start time.
	for attempt := 0; e == nil && attempt < 10 && identity.Executable == exe && identity.Parent == os.Getpid() && identity.UID == uint32(os.Geteuid()) && liveProcess(identity) && !reflect.DeepEqual(identity.Arguments, args); attempt++ {
		start := identity.StartTime
		time.Sleep(5 * time.Millisecond)
		identity, e = inspectProcess(pid)
		if e != nil || identity.StartTime != start {
			return errors.Join(errors.New("guardian identity changed during startup"), e)
		}
	}
	if e != nil || identity.Executable != exe || identity.Parent != os.Getpid() || identity.UID != uint32(os.Geteuid()) || !reflect.DeepEqual(identity.Arguments, args) || !liveProcess(identity) {
		return errors.Join(fmt.Errorf("launched guardian identity mismatch: executable=%t parent=%t uid=%d/%d arguments=%t state=%q", identity.Executable == exe, identity.Parent == os.Getpid(), identity.UID, os.Geteuid(), reflect.DeepEqual(identity.Arguments, args), identity.State), e)
	}
	a.guardianPID, a.guardianStart, a.guardianExe, a.guardianArgs = pid, identity.StartTime, exe, append([]string(nil), args...)
	return nil
}

func (a *audit) verifyServicePeer(pid int) error {
	if pid != a.servicePID || a.servicePID <= 1 || a.guardianPID <= 1 || a.serviceStart == "" {
		return errors.New("service socket peer is not the captured service")
	}
	service, e := inspectProcess(pid)
	if e != nil {
		return e
	}
	guardian, ge := inspectProcess(a.guardianPID)
	if ge != nil {
		return ge
	}
	if service.StartTime != a.serviceStart || service.Parent != a.guardianPID || service.Executable != a.executable || service.UID != uint32(os.Geteuid()) || !liveProcess(service) || !reflect.DeepEqual(service.Arguments, a.serviceArgs) {
		return errors.New("service socket peer identity/origin changed")
	}
	if guardian.StartTime != a.guardianStart || guardian.Parent != os.Getpid() || guardian.Executable != a.guardianExe || guardian.UID != uint32(os.Geteuid()) || !liveProcess(guardian) || !reflect.DeepEqual(guardian.Arguments, a.guardianArgs) {
		return errors.New("service guardian identity changed")
	}
	if captured, ok := a.processes[fmt.Sprintf("%d:%s", service.PID, service.StartTime)]; !ok || !sameProcess(captured, service) {
		return errors.New("service peer was not captured in the launched guardian tree")
	}
	if captured, ok := a.processes[fmt.Sprintf("%d:%s", guardian.PID, guardian.StartTime)]; !ok || !sameProcess(captured, guardian) {
		return errors.New("guardian peer was not captured at launch")
	}
	return nil
}

func status(reply object) object { return objectOf(objectOf(reply["snapshot"])["effect_status"]) }

func (a *audit) check(ctx context.Context) error {
	if e := a.inventoryRestored(ctx); e != nil {
		return e
	}
	reply, e := a.request(ctx, 45*time.Second, "check_effects", nil)
	if e != nil {
		return e
	}
	setup := objectOf(objectOf(reply["snapshot"])["effects_setup"])
	if setup["status"] != "ready" && setup["reason"] != "output_selection_required" {
		return fmt.Errorf("native compatibility unavailable: %v", setup)
	}
	// Exercise selection through the public protocol even when the headless
	// audit service booted with the intended output already set. The selection
	// acknowledgement must itself contain the refreshed ready state.
	reply, e = a.request(ctx, 30*time.Second, "select_output", object{"output": a.output})
	if e != nil {
		return e
	}
	setup = objectOf(objectOf(reply["snapshot"])["effects_setup"])
	if setup["status"] != "ready" || setup["reason"] != "ready" || setup["selected_output"] != a.output {
		return fmt.Errorf("native compatibility unavailable: %v", setup)
	}
	return nil
}

func (a *audit) controls(ctx context.Context, patch object) error {
	reply, e := a.request(ctx, 5*time.Second, "set_controls", object{"controls": patch})
	if e != nil {
		return e
	}
	got := objectOf(objectOf(reply["snapshot"])["controls"])
	for key, value := range patch {
		if !reflect.DeepEqual(got[key], value) {
			return fmt.Errorf("control snapshot did not acknowledge %s", key)
		}
	}
	return nil
}

func (a *audit) stopped(ctx context.Context, phase string) error {
	deadline, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		reply, e := a.request(deadline, 2*time.Second, "snapshot", nil)
		if e != nil {
			return e
		}
		state := status(reply)
		if state["state"] == "cleanup_failed" {
			return fmt.Errorf("%s cleanup unresolved: %v", phase, state)
		}
		if state["state"] == "stopped" {
			if e = a.inventoryRestored(deadline); e != nil {
				return e
			}
			return a.record("assertion", object{"phase": phase, "stopped_acknowledged": true, "plugin_inventory_restored": true})
		}
		if e = wait(deadline, 250*time.Millisecond); e != nil {
			return fmt.Errorf("%s did not autonomously stop: %w", phase, e)
		}
	}
}

func (a *audit) liveStatus(ctx context.Context, generation float64, controls object) error {
	reply, e := a.request(ctx, 3*time.Second, "snapshot", nil)
	if e != nil {
		return e
	}
	state := status(reply)
	if state["state"] != "running" || state["persistent"] != true || state["session_generation"] != generation || numberOf(state["remaining_seconds"]) <= 0 {
		return fmt.Errorf("live lease or generation not maintained: %v", state)
	}
	value, e := a.ctl(ctx, "a-weather-app:rain", "status")
	if e != nil {
		return e
	}
	native := objectOf(value)
	if native["enabled"] != true || native["session_generation"] != generation {
		return errors.New("native generation changed; refusing further lifecycle actions except app-owned cleanup")
	}
	for _, key := range []string{"fps", "reduced_motion", "window_physics", "accumulation"} {
		if native[key] != controls[key] {
			return fmt.Errorf("%w: %s", errControlsPending, key)
		}
	}
	return a.captureTree()
}

var errControlsPending = errors.New("native control acknowledgement pending")

func (a *audit) awaitControls(ctx context.Context, generation float64, controls object) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		e := a.liveStatus(ctx, generation, controls)
		if e == nil {
			return nil
		}
		if !errors.Is(e, errControlsPending) {
			return e
		}
		if e = wait(ctx, 250*time.Millisecond); e != nil {
			return e
		}
	}
}

func (a *audit) exercise(ctx context.Context) error {
	initial := object{"mode": "manual", "manual": object{"condition": "rain"}, "strength": "subtle", "lightning_enabled": false, "reduced_motion": false, "fps": float64(30), "window_physics": false, "accumulation": false, "pause_fullscreen": true}
	if e := a.controls(ctx, initial); e != nil {
		return e
	}
	for round := 1; round <= a.previewRounds; round++ {
		if e := a.check(ctx); e != nil {
			return e
		}
		reply, e := a.request(ctx, 45*time.Second, "start_effects", object{"duration": float64(2)})
		if e != nil {
			return e
		}
		state := status(reply)
		if state["state"] != "running" || state["persistent"] != false || numberOf(state["session_generation"]) <= 0 {
			return fmt.Errorf("finite preview did not activate: %v", state)
		}
		if e = a.captureTree(); e != nil {
			return e
		}
		if e = a.stopped(ctx, fmt.Sprintf("finite-preview-%d", round)); e != nil {
			return e
		}
	}
	if a.previewOnly {
		return nil
	}
	if e := a.check(ctx); e != nil {
		return e
	}
	reply, e := a.request(ctx, 45*time.Second, "start_live_effects", nil)
	if e != nil {
		return e
	}
	generation := numberOf(status(reply)["session_generation"])
	if generation <= 0 {
		return errors.New("live session has no generation acknowledgement")
	}
	started := time.Now()
	current := object{"fps": float64(30), "reduced_motion": false, "window_physics": false, "accumulation": false}
	changed, restored := false, false
	for time.Since(started) < 35*time.Second {
		if time.Since(started) >= 5*time.Second && !changed {
			current = object{"fps": float64(15), "reduced_motion": true, "window_physics": true, "accumulation": true, "lightning_enabled": false}
			if e = a.controls(ctx, current); e != nil {
				return e
			}
			changed = true
			if e = a.awaitControls(ctx, generation, current); e != nil {
				return e
			}
		}
		if time.Since(started) >= 20*time.Second && !restored {
			current = object{"fps": float64(30), "reduced_motion": false, "window_physics": false, "accumulation": false, "lightning_enabled": false}
			if e = a.controls(ctx, current); e != nil {
				return e
			}
			restored = true
			if e = a.awaitControls(ctx, generation, current); e != nil {
				return e
			}
		}
		if e = a.liveStatus(ctx, generation, current); e != nil {
			return e
		}
		if e = wait(ctx, time.Second); e != nil {
			return e
		}
	}
	if e = a.liveStatus(ctx, generation, current); e != nil {
		return e
	}
	if e = a.record("assertion", object{"phase": "live-lease", "generation": generation, "elapsed_seconds": time.Since(started).Seconds(), "renewal_beyond_initial_30s": true, "controls_changed_and_restored": changed && restored, "offline_empty_weather": true}); e != nil {
		return e
	}
	if _, e = a.request(ctx, 110*time.Second, "stop_effects", nil); e != nil {
		return e
	}
	return a.stopped(ctx, "explicit-live-stop")
}

func (a *audit) cleanup() error {
	var failures []error
	if a.guardDone != nil {
		// Cleanup uses fresh contexts even when the audit was canceled. Every
		// mutation is sent only to this fresh state's app; no hyprctl unload,
		// process-name kill or signal to a bare/discovered PID exists in this tool.
		if !a.guardFinished {
			if _, e := a.request(context.Background(), 110*time.Second, "stop_effects", nil); e != nil {
				failures = append(failures, e)
			}
			if _, e := a.request(context.Background(), 80*time.Second, "quit", nil); e != nil {
				failures = append(failures, e)
			}
			a.guardCancel()
			select {
			case a.guardErr = <-a.guardDone:
				a.guardFinished = true
			case <-time.After(supervision.GuardianCleanupGrace + 10*time.Second):
				failures = append(failures, errors.New("guardian completion unconfirmed; independent cleanup owner may remain running"))
			}
		}
		a.guardCancel()
		if !a.expectedGuardianFailure {
			failures = append(failures, a.guardErr)
		}
		failures = append(failures, a.verifyGone())
	}
	if a.baseline != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		failures = append(failures, a.inventoryRestored(ctx))
		cancel()
	}
	result := errors.Join(failures...)
	entry := object{"ok": result == nil, "guardian_finished": a.guardFinished, "captured_owned_processes": len(a.processes), "captured_effects_runtime_directories": len(a.runtimeDirs), "manual_unload_attempted": false, "foreign_pid_signals_attempted": false}
	entry["retained_crash_diagnostic_directories"] = len(a.retainedDirs)
	entry["full_clean_stopped"] = !a.crashInjected && result == nil
	if result != nil {
		entry["error"] = result.Error()
	}
	return errors.Join(result, a.record("cleanup", entry))
}
