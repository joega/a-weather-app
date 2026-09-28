package app

import (
	"context"
	"errors"
	"github.com/joega/a-weather-app/internal/safeio"
	"reflect"
	"sync"
	"time"
)

// CloseBudget includes notification cancellation (4s), effects Stop admission
// (8s), and the native manager's independent recovery/reap reserve (<=58s).
// The guardian allows 80s for the service, including Qt's final event/exit.
const CloseBudget = 70 * time.Second
const effectsShutdownRequestBudget = 8 * time.Second

const effectsHeartbeatInterval = 500 * time.Millisecond

// contextMutex supports cancellation before mutation admission. Its zero value
// remains usable by existing App literals and read-only snapshot fixtures.
type contextMutex struct {
	once sync.Once
	gate chan struct{}
}

func (m *contextMutex) init() {
	m.once.Do(func() { m.gate = make(chan struct{}, 1); m.gate <- struct{}{} })
}
func (m *contextMutex) Lock()   { m.init(); <-m.gate }
func (m *contextMutex) Unlock() { m.gate <- struct{}{} }
func (m *contextMutex) TryLock() bool {
	m.init()
	select {
	case <-m.gate:
		return true
	default:
		return false
	}
}
func (m *contextMutex) LockContext(ctx context.Context) error {
	m.init()
	if e := ctx.Err(); e != nil {
		return e
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.gate:
		if e := ctx.Err(); e != nil {
			m.Unlock()
			return e
		}
		return nil
	}
}

type effectResult struct {
	err  error
	code string
}
type effectAction struct {
	ctx               context.Context
	op, output        string
	duration          int
	weather, controls M
	done              chan effectResult
}

// effectsCoordinator is the sole caller of the native manager. Its heartbeat is
// independent of UI requests, socket writes and the main forecast event loop.
type effectsCoordinator struct {
	backend                          Effects
	jobs                             chan effectAction
	closing, done                    chan struct{}
	closeOnce                        sync.Once
	mu                               sync.RWMutex
	status, setup, weather, controls M
	active                           context.CancelFunc
	closeErr                         error
	changed                          func()
}

func newEffectsCoordinator(backend Effects, changed func()) *effectsCoordinator {
	f := &effectsCoordinator{backend: backend, jobs: make(chan effectAction, 8), closing: make(chan struct{}), done: make(chan struct{}), status: M{"state": "stopped", "persistent": false, "remaining_seconds": 0.0, "session_generation": nil, "error": nil}, setup: M{"status": "unchecked", "reason": "not_checked", "outputs": []any{}, "selected_output": nil}, controls: DefaultControls(), changed: changed}
	go f.run()
	return f
}
func (f *effectsCoordinator) Status() M {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return safeio.Clone(f.status)
}
func (f *effectsCoordinator) SetupSnapshot() M {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return safeio.Clone(f.setup)
}
func effectWeather(selected M) M {
	if selected == nil {
		return nil
	}
	v := M{}
	for _, key := range []string{"schema_version", "selected_at", "effects", "mode", "freshness"} {
		v[key] = selected[key]
	}
	for key, fields := range map[string][]string{"current": {"time", "temperature_c"}, "forecast": {"fetched_at"}} {
		source := object(selected[key])
		row := M{}
		for _, field := range fields {
			row[field] = source[field]
		}
		v[key] = row
	}
	return safeio.Clone(v)
}
func (f *effectsCoordinator) update(weather, controls M) {
	w, c := effectWeather(weather), safeio.Clone(controls)
	f.mu.Lock()
	f.weather = w
	f.controls = c
	f.mu.Unlock()
}
func (f *effectsCoordinator) refresh() {
	status, setup := f.backend.Status(), f.backend.SetupSnapshot()
	f.mu.Lock()
	previous, next := safeio.Clone(f.status), safeio.Clone(status)
	delete(previous, "remaining_seconds")
	delete(next, "remaining_seconds")
	changed := !reflect.DeepEqual(previous, next) || !reflect.DeepEqual(f.setup, setup)
	f.status = safeio.Clone(status)
	f.setup = safeio.Clone(setup)
	f.mu.Unlock()
	if changed {
		f.changed()
	}
}
func (f *effectsCoordinator) operation(ctx context.Context, fn func(context.Context) effectResult) effectResult {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	f.mu.Lock()
	f.active = cancel
	f.mu.Unlock()
	select {
	case <-f.closing:
		cancel()
	default:
	}
	result := fn(ctx)
	cancel()
	f.mu.Lock()
	f.active = nil
	f.mu.Unlock()
	f.refresh()
	return result
}
func (f *effectsCoordinator) heartbeat() {
	if f.Status()["state"] != "running" {
		return
	}
	f.mu.RLock()
	w, c := f.weather, f.controls
	f.mu.RUnlock()
	f.operation(context.Background(), func(ctx context.Context) effectResult { return effectResult{err: f.backend.Tick(ctx, w, c)} })
}
func (f *effectsCoordinator) run() {
	defer close(f.done)
	f.refresh()
	var next time.Time
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	for {
		select {
		case <-f.closing:
			ctx, cancel := context.WithTimeout(context.Background(), effectsShutdownRequestBudget)
			e := f.backend.Stop(ctx)
			cancel()
			f.refresh()
			f.mu.Lock()
			f.closeErr = e
			f.mu.Unlock()
			return
		default:
		}
		running := f.Status()["state"] == "running"
		if running && next.IsZero() {
			next = time.Now().Add(effectsHeartbeatInterval)
		}
		if running && !time.Now().Before(next) {
			f.heartbeat()
			next = time.Now().Add(effectsHeartbeatInterval)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		var tick <-chan time.Time
		if f.Status()["state"] == "running" {
			timer.Reset(time.Until(next))
			tick = timer.C
		} else {
			// No native session means no lease to renew or status to poll.
			next = time.Time{}
		}
		select {
		case <-f.closing:
			continue
		case <-tick:
			continue
		case action := <-f.jobs:
			if e := action.ctx.Err(); e != nil {
				action.done <- effectResult{err: e, code: "request_timeout"}
				continue
			}
			result := f.operation(action.ctx, func(ctx context.Context) effectResult { return f.perform(ctx, action) })
			action.done <- result
		}
	}
}
func (f *effectsCoordinator) perform(ctx context.Context, a effectAction) effectResult {
	if e := ctx.Err(); e != nil {
		return effectResult{e, "request_timeout"}
	}
	status := f.backend.Status()
	var e error
	code := "effects_failed"
	switch a.op {
	case "check_effects", "select_output":
		if status["state"] != "stopped" {
			return effectResult{errors.New("effects active"), "effects_active"}
		}
		if a.op == "check_effects" {
			f.backend.Check(ctx)
			e = ctx.Err()
		} else {
			_, e = f.backend.SelectOutput(ctx, a.output)
			if e == nil {
				f.backend.Check(ctx)
				e = ctx.Err()
			}
		}
	case "start_effects", "start_live_effects":
		code = "effects_start_failed"
		live := a.op == "start_live_effects"
		already := live && status["state"] == "running" && status["persistent"] == true
		if live && !already {
			if status["state"] != "stopped" {
				e = f.backend.Stop(ctx)
			}
			if e == nil {
				f.backend.Check(ctx)
				e = ctx.Err()
			}
		}
		if e == nil && !already {
			e = f.backend.Start(ctx, a.duration, live, a.controls)
		}
		if e == nil {
			e = f.backend.Tick(ctx, a.weather, a.controls)
		}
	case "stop_effects":
		code = "effects_stop_failed"
		e = f.backend.Stop(ctx)
	default:
		e = errors.New("invalid effects action")
	}
	return effectResult{e, code}
}
func (f *effectsCoordinator) submit(ctx context.Context, a effectAction) effectResult {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return effectResult{e, "request_timeout"}
	}
	a.ctx = ctx
	a.done = make(chan effectResult, 1)
	select {
	case <-f.closing:
		return effectResult{errors.New("service closing"), "service_closed"}
	case <-ctx.Done():
		return effectResult{ctx.Err(), "request_timeout"}
	case f.jobs <- a:
	}
	select {
	case result := <-a.done:
		return result
	case <-ctx.Done():
		return effectResult{ctx.Err(), "request_timeout"}
	case <-f.closing:
		return effectResult{errors.New("service closing"), "service_closed"}
	}
}
func (f *effectsCoordinator) close(ctx context.Context) error {
	f.closeOnce.Do(func() {
		close(f.closing)
		f.mu.Lock()
		if f.active != nil {
			f.active()
		}
		f.mu.Unlock()
	})
	select {
	case <-f.done:
		f.mu.RLock()
		defer f.mu.RUnlock()
		return f.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
