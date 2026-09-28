package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
)

func (a *audit) crashGeneration(ctx context.Context, generation float64) error {
	if generation <= 0 || generation > 9007199254740991 || math.Trunc(generation) != generation {
		return errors.New("invalid acknowledged crash generation")
	}
	reply, e := a.request(ctx, 3*time.Second, "snapshot", nil)
	if e != nil {
		return e
	}
	state := status(reply)
	if state["state"] != "running" || state["session_generation"] != generation {
		return errors.New("app no longer acknowledges the crash generation")
	}
	value, e := a.ctl(ctx, "a-weather-app:rain", "status")
	if e != nil {
		return e
	}
	native := objectOf(value)
	if native["enabled"] != true || native["session_generation"] != generation || native["cleanup_failed"] == true {
		return errors.New("native crash generation is not enabled and owned")
	}
	return nil
}

func (a *audit) crashTarget() (processIdentity, processIdentity, error) {
	service, e := inspectProcess(a.servicePID)
	if e != nil {
		return service, processIdentity{}, e
	}
	var candidates []processIdentity
	for _, row := range a.processes {
		if row.Parent != service.PID || row.Executable != a.executable || len(row.Arguments) < 2 || row.Arguments[1] != "--effects-worker" {
			continue
		}
		current, e := inspectProcess(row.PID)
		if e != nil {
			return service, processIdentity{}, e
		}
		if !sameProcess(row, current) {
			return service, processIdentity{}, errors.New("captured worker identity changed")
		}
		if e = a.authorizeWorker(service, current); e != nil {
			return service, processIdentity{}, e
		}
		candidates = append(candidates, current)
	}
	if len(candidates) != 1 {
		return service, processIdentity{}, errors.New("exactly one proven-owned direct-child worker required")
	}
	return service, candidates[0], nil
}

func (a *audit) crashRecovered(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 70*time.Second)
	defer cancel()
	for {
		// The absolute phase context is the bound. A snapshot implementation
		// may wait for the manager's independent 50s recovery/reap reserve.
		reply, e := a.request(ctx, 70*time.Second, "snapshot", nil)
		if e != nil {
			return e
		}
		state := status(reply)
		if state["state"] == "cleanup_failed" {
			if stringOf(state["error"]) == "" || state["persistent"] != false {
				return errors.New("crash failure is not explicitly visible")
			}
			break
		}
		if state["state"] != "running" && state["state"] != "stopping" {
			return fmt.Errorf("unexpected crash recovery contract: %v", state)
		}
		if e = wait(ctx, 500*time.Millisecond); e != nil {
			return e
		}
	}
	if e := a.inventoryRestored(ctx); e != nil {
		return e
	}
	if len(a.runtimeDirs) == 0 {
		return errors.New("no owned diagnostic runtime was captured before crash")
	}
	for path := range a.runtimeDirs {
		diagnostic, e := retainedSnapshot(path)
		if e != nil {
			return e
		}
		a.retainedDirs[path] = diagnostic
	}
	if e := a.verifyGoneExcept(a.servicePID); e != nil {
		return e
	}
	reply, e := a.requestOutcome(ctx, 10*time.Second, "start_effects", object{"duration": float64(2)}, false)
	if e != nil {
		return e
	}
	if reply["error"] != "effects_start_failed" || status(reply)["state"] != "cleanup_failed" {
		return errors.New("same-service restart did not fail closed")
	}
	if e = a.inventoryRestored(ctx); e != nil {
		return e
	}
	if e = a.captureTree(); e != nil {
		return e
	}
	if e = a.verifyGoneExcept(a.servicePID); e != nil {
		return e
	}
	a.crashRecoveryVerified = true
	return a.record("assertion", object{"phase": "fail_closed_recovery", "cleanup_failed_visible": true, "same_service_restart_refused": true, "plugin_inventory_restored": true, "owned_descendants_exited": true, "retained_diagnostics": a.retainedDirs, "full_clean_stopped": false})
}

func (a *audit) closeFailedService(ctx context.Context) error {
	reply, e := a.requestOutcome(ctx, 110*time.Second, "stop_effects", nil, false)
	if e != nil {
		return e
	}
	if reply["error"] != "effects_stop_failed" || status(reply)["state"] != "cleanup_failed" {
		return errors.New("failed owner did not preserve its stop failure")
	}
	reply, e = a.requestOutcome(ctx, 80*time.Second, "quit", nil, false)
	if e != nil {
		return e
	}
	if reply["error"] != "cleanup_failed" {
		return errors.New("quit did not acknowledge controlled cleanup failure")
	}
	a.guardCancel()
	select {
	case a.guardErr = <-a.guardDone:
		a.guardFinished = true
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(supervision.GuardianCleanupGrace + 10*time.Second):
		return errors.New("failed service guardian did not complete")
	}
	var exit *exec.ExitError
	if !errors.As(a.guardErr, &exit) || exit.ExitCode() != 1 {
		return fmt.Errorf("expected controlled guardian exit 1, got %v", a.guardErr)
	}
	a.expectedGuardianFailure = true
	if e = a.verifyGone(); e != nil {
		return e
	}
	if e = a.inventoryRestored(ctx); e != nil {
		return e
	}
	return a.record("assertion", object{"phase": "failed-service-quit", "controlled_quit_error": "cleanup_failed", "guardian_exit": 1, "owned_processes_exited": true, "diagnostics_unchanged": true})
}

func (a *audit) exerciseWorkerCrash(ctx context.Context) error {
	controls := object{"mode": "manual", "manual": object{"condition": "rain"}, "strength": "subtle", "lightning_enabled": false, "reduced_motion": true, "fps": float64(15), "window_physics": false, "accumulation": false, "pause_fullscreen": true}
	if e := a.controls(ctx, controls); e != nil {
		return e
	}
	if e := a.check(ctx); e != nil {
		return e
	}
	reply, e := a.request(ctx, 45*time.Second, "start_effects", object{"duration": float64(30)})
	if e != nil {
		return e
	}
	generation := numberOf(status(reply)["session_generation"])
	if e = a.captureTree(); e != nil {
		return e
	}
	if len(a.runtimeDirs) != 1 {
		return errors.New("one captured native runtime required before crash")
	}
	if e = a.crashGeneration(ctx, generation); e != nil {
		return e
	}
	service, worker, e := a.crashTarget()
	if e != nil {
		return e
	}
	e = a.signalOwnedWorker(ctx, service, worker, inspectProcess, openWorkerPin, func() error {
		if e := a.crashGeneration(ctx, generation); e != nil {
			return e
		}
		return a.record("owned-worker-crash", object{"generation": generation, "service": service, "worker": worker, "signal": "SIGKILL", "transport": "identity-revalidated pidfd only", "foreign_pid_signal": false})
	})
	if e != nil {
		return e
	}
	if e = a.crashRecovered(ctx); e != nil {
		return e
	}
	if e = a.closeFailedService(ctx); e != nil {
		return e
	}
	// A recovered plugin does not authorize reusing the failed manager. Start
	// a completely fresh app state/service after all prior descendants exited.
	fresh := filepath.Join(a.evidence.Path, "restart-state")
	if fresh == a.state {
		return errors.New("restart state was not fresh")
	}
	directory, e := safeio.OpenDir(fresh, true)
	if e != nil {
		return e
	}
	directory.Close()
	a.state = fresh
	a.states = append(a.states, fresh)
	a.servicePID = 0
	a.serviceStart = ""
	a.serviceArgs = nil
	a.guardianPID = 0
	a.guardianStart = ""
	a.guardianExe = ""
	a.guardianArgs = nil
	a.guardDone = nil
	a.guardErr = nil
	a.guardFinished = false
	a.expectedGuardianFailure = false
	if e = a.start(ctx); e != nil {
		return e
	}
	if e = a.controls(ctx, controls); e != nil {
		return e
	}
	if e = a.check(ctx); e != nil {
		return e
	}
	reply, e = a.request(ctx, 45*time.Second, "start_effects", object{"duration": float64(2)})
	if e != nil {
		return e
	}
	if status(reply)["state"] != "running" || numberOf(status(reply)["session_generation"]) <= 0 {
		return errors.New("fresh-service finite preview failed to activate")
	}
	if e = a.captureTree(); e != nil {
		return e
	}
	if e = a.stopped(ctx, "fresh-service-after-worker-crash"); e != nil {
		return e
	}
	if e = a.verifyGoneExcept(a.servicePID); e != nil {
		return e
	}
	a.crashRestartVerified = true
	return a.record("assertion", object{"phase": "fresh-service-restart", "finite_preview_passed": true, "state": fresh, "prior_diagnostics_unchanged": true, "prior_failed_service_not_reused": true})
}
