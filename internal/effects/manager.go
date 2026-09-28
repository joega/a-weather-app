// Package effects controls opt-in, generation-owned finite native sessions.
package effects

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

const requestLimit = 8192
const replyLimit = 32768

// RecoveryBudget is independent of a canceled request. The worker gets time to
// finish its own 25s cleanup before the parent attempts acknowledged recovery.
// Subprocess reaping may add up to two 3s tails after this deadline.
const RecoveryBudget = 50 * time.Second
const WorkerEOFGrace = 35 * time.Second

type Manager struct {
	mu                               sync.Mutex
	root, stateDir, instance, output string
	setup, current, ownership        object
	process                          *supervision.Process
	input, reply                     *os.File
	reader                           *bufio.Reader
	id                               int
	backendFactory                   func() *nativeBackend
	recover                          func(context.Context, object) error
}

func New(root, state, instance, output string) *Manager {
	m := &Manager{root: root, stateDir: state, instance: instance, output: output, setup: object{"status": "unchecked", "reason": "not_checked", "outputs": []any{}, "selected_output": nil}, current: object{"state": "stopped", "error": nil, "session_generation": nil, "remaining_seconds": 0, "persistent": false}}
	m.backendFactory = func() *nativeBackend { return newBackend(root) }
	m.recover = m.fallback
	return m
}
func (m *Manager) SetupSnapshot() object {
	m.mu.Lock()
	defer m.mu.Unlock()
	return safeio.Clone(m.setup)
}
func (m *Manager) Check(ctx context.Context) object {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil {
		return safeio.Clone(m.setup)
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	m.setup = object{"status": "unavailable", "reason": "session_unavailable", "outputs": []any{}, "selected_output": nil}
	instance, pid, _, e := discover(m.instance)
	if e != nil {
		if e.Error() == "wayland_required" {
			m.setup["reason"] = "wayland_required"
		}
		return safeio.Clone(m.setup)
	}
	b := m.backendFactory()
	b.instance = instance
	b.pid = pid
	b.starttime, e = metadata(pid)
	if e != nil {
		return safeio.Clone(m.setup)
	}
	v, e := b.ctl(ctx, true, "monitors", "all")
	if e != nil {
		return safeio.Clone(m.setup)
	}
	outputs, e := monitorRecords(v)
	if e != nil {
		m.setup["reason"] = "output_unavailable"
		return safeio.Clone(m.setup)
	}
	m.setup["outputs"] = outputs
	enabled := []string{}
	for _, row := range outputs {
		if obj(row)["enabled"] == true {
			enabled = append(enabled, text(obj(row)["name"]))
		}
	}
	selected := m.output
	if selected == "" && len(enabled) == 1 {
		selected = enabled[0]
	}
	monitor, selectedError := selectedOutput(v, selected)
	if selectedError == nil {
		m.setup["selected_output"] = selected
	}
	reason := "ready"
	switch {
	case selected == "" && len(enabled) > 1:
		reason = "output_selection_required"
	case selectedError != nil:
		reason = "output_unavailable"
	case validateOutput(monitor) != nil:
		reason = "unsupported_output"
	default:
		inventory, e := b.inventory(ctx)
		if e != nil {
			reason = "session_unavailable"
		} else if len(inventory) > 0 {
			reason = "plugin_conflict"
		} else if _, e = os.Stat(filepath.Join(m.root, pluginRelative)); e != nil {
			reason = "native_missing"
		} else if _, e = os.Stat(filepath.Join(m.root, hostRelative)); e != nil {
			reason = "native_missing"
		} else if _, e = b.preflight(ctx, instance, selected); e != nil {
			reason = "native_incompatible"
		}
	}
	m.setup["reason"] = reason
	if reason == "ready" {
		m.setup["status"] = "ready"
		m.instance = instance
		m.output = selected
	}
	return safeio.Clone(m.setup)
}
func (m *Manager) SelectOutput(ctx context.Context, output string) (object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !outputPattern.MatchString(output) {
		return nil, errors.New("output")
	}
	rows, _ := m.setup["outputs"].([]any)
	for _, row := range rows {
		if obj(row)["name"] == output && obj(row)["enabled"] == true {
			m.output = output
			m.setup["selected_output"] = output
			return safeio.Clone(m.setup), nil
		}
	}
	return nil, errors.New("output unavailable")
}
func (m *Manager) Status() object {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process != nil && !m.process.Alive() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		m.failure(ctx, errors.New("effects owner stopped"))
		cancel()
	}
	return safeio.Clone(m.current)
}
func (m *Manager) rpc(ctx context.Context, op string, args object) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.process == nil {
		return errors.New("effects owner unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 28*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	m.id++
	request := clone(args)
	request["id"] = m.id
	request["op"] = op
	raw, e := json.Marshal(request)
	if e != nil || len(raw)+1 > requestLimit {
		return errors.New("effects request limit")
	}
	m.input.SetWriteDeadline(deadline)
	m.reply.SetReadDeadline(deadline)
	input, reply := m.input, m.reply
	interrupted := make(chan struct{})
	interrupt := context.AfterFunc(ctx, func() {
		input.SetWriteDeadline(time.Now())
		reply.SetReadDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !interrupt() {
			<-interrupted
		}
	}()
	if _, e = m.input.Write(append(raw, '\n')); e != nil {
		return e
	}
	line, e := m.reader.ReadSlice('\n')
	if e != nil {
		return e
	}
	response, e := safeio.Object(line, replyLimit)
	if e != nil {
		return e
	}
	id, ok := integer(response["id"])
	success, valid := response["ok"].(bool)
	if !ok || id != int64(m.id) || !valid || obj(response["status"]) == nil {
		return errors.New("effects owner reply identity")
	}
	m.current = obj(response["status"])
	if ownership := obj(response["ownership"]); ownership != nil {
		m.ownership = ownership
	}
	if !success {
		return errors.New(bounded(text(response["error"]), 512))
	}
	return nil
}
func (m *Manager) Start(ctx context.Context, duration int, persistent bool, flags object) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if m.process != nil || m.current["state"] != "stopped" {
		return errors.New("effects already active or cleanup unresolved")
	}
	validatedFlags, e := controls(flags)
	if e != nil {
		return e
	}
	flags = validatedFlags
	if duration < 1 || duration > 300 {
		return errors.New("effects duration must be 1..300")
	}
	if !instancePattern.MatchString(m.instance) || !outputPattern.MatchString(m.output) {
		return errors.New("check effects compatibility and select output first")
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return e
	}
	inputR, inputW, e := os.Pipe()
	if e != nil {
		return e
	}
	replyR, replyW, e := os.Pipe()
	if e != nil {
		inputR.Close()
		inputW.Close()
		return e
	}
	env := append(childEnvironment(), "WAYLAND_DISPLAY="+os.Getenv("WAYLAND_DISPLAY"))
	p, e := supervision.Start([]string{exe, "--effects-worker", "--root", m.root, "--instance", m.instance, "--output", m.output}, env, m.root, inputR, replyW, nil)
	inputR.Close()
	replyW.Close()
	if e != nil {
		inputW.Close()
		replyR.Close()
		return e
	}
	m.process = p
	m.input = inputW
	m.reply = replyR
	m.reader = bufio.NewReaderSize(replyR, replyLimit)
	m.ownership = nil
	m.current["state"] = "starting"
	if e = m.rpc(ctx, "start", object{"duration": duration, "persistent": persistent, "controls": flags}); e != nil {
		m.setup["status"] = "unavailable"
		m.setup["reason"] = "activation_failed"
		return m.failure(ctx, e)
	}
	return nil
}
func (m *Manager) Tick(ctx context.Context, selected, flags object) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.process == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var weather object
	if selected != nil {
		weather = object{}
		for _, key := range []string{"schema_version", "selected_at", "effects", "mode", "freshness"} {
			weather[key] = selected[key]
		}
		for key, fields := range map[string][]string{"current": {"time", "temperature_c"}, "forecast": {"fetched_at"}} {
			source := obj(selected[key])
			value := object{}
			for _, field := range fields {
				value[field] = source[field]
			}
			weather[key] = value
		}
	}
	if e := m.rpc(ctx, "tick", object{"weather": weather, "controls": flags}); e != nil {
		return m.failure(ctx, e)
	}
	if m.current["state"] == "stopped" {
		return m.retire(ctx)
	}
	return nil
}
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if m.process == nil {
		if m.current["state"] == "cleanup_failed" {
			return errors.New(text(m.current["error"]))
		}
		return nil
	}
	if e := m.rpc(ctx, "stop", nil); e != nil {
		return m.failure(ctx, e)
	}
	return m.retire(ctx)
}
func (m *Manager) retire(ctx context.Context) error {
	m.input.Close()
	deadline := time.Now().Add(5 * time.Second)
	for m.process.Alive() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	if m.process.Alive() {
		return m.failure(ctx, errors.New("stopped effects owner did not exit"))
	}
	e := m.process.Cleanup(ctx, 0)
	m.closePipes()
	if e != nil {
		m.current = object{"state": "cleanup_failed", "error": bounded(e.Error(), 1024), "session_generation": nil, "remaining_seconds": 0, "persistent": false}
		return e
	}
	m.ownership = nil
	return nil
}
func (m *Manager) closePipes() {
	if m.input != nil {
		m.input.Close()
	}
	if m.reply != nil {
		m.reply.Close()
	}
	m.process = nil
	m.input = nil
	m.reply = nil
	m.reader = nil
}
func (m *Manager) failure(_ context.Context, cause error) error {
	// A caller timeout revokes the command, not our obligation to clean up the
	// already-acknowledged native generation. Never reuse that canceled context.
	ctx, cancel := context.WithTimeout(context.Background(), RecoveryBudget)
	defer cancel()
	errs := []error{cause}
	confirmedStopped := m.current["state"] == "stopped"
	if m.process != nil {
		m.input.Close()
		deadline := time.Now().Add(WorkerEOFGrace)
		for m.process.Alive() && time.Now().Before(deadline) && ctx.Err() == nil {
			time.Sleep(10 * time.Millisecond)
		}
		// Retire the owned process group before fallback so the worker cannot
		// race our guarded native commands. Its acknowledged identity is kept.
		if e := m.process.Cleanup(ctx, 0); e != nil {
			errs = append(errs, e)
		}
		if m.current["state"] != "stopped" {
			if e := m.recover(ctx, m.ownership); e != nil {
				errs = append(errs, e)
			}
		}
		m.closePipes()
	}
	e := errors.Join(errs...)
	state := "cleanup_failed"
	if confirmedStopped && len(errs) == 1 {
		state = "stopped"
		m.ownership = nil
	}
	m.current = object{"state": state, "error": bounded(e.Error(), 1024), "session_generation": nil, "remaining_seconds": 0, "persistent": false}
	return e
}
func (m *Manager) fallback(ctx context.Context, ownership object) error {
	if ownership == nil {
		return errors.New("effects owner died before ownership acknowledgement; native state uncertain")
	}
	g, gok := integer(ownership["generation"])
	pid, pok := integer(ownership["pid"])
	inventory, iok := ownership["plugin_inventory"].([]any)
	instance := text(ownership["instance"])
	start := text(ownership["starttime"])
	if !gok || g <= 0 || !pok || pid <= 0 || pid > 2147483647 || !iok || len(inventory) != 1 || !instancePattern.MatchString(instance) || start == "" {
		return errors.New("effects ownership acknowledgement invalid")
	}
	b := m.backendFactory()
	b.instance = instance
	b.pid = int(pid)
	b.starttime = start
	b.inventoryValue = inventory
	if e := b.identity(); e != nil {
		return e
	}
	current, e := b.inventory(ctx)
	if e != nil {
		return e
	}
	if len(current) == 0 {
		return nil
	}
	if !reflect.DeepEqual(current, inventory) {
		return errors.New("effects plugin ownership changed; refusing recovery")
	}
	return recoverGeneration(ctx, b, g)
}

// Inventory and compositor identity must already be checked by the caller.
func recoverGeneration(ctx context.Context, b backend, g int64) error {
	status, e := b.native(ctx, "status")
	if e != nil {
		return e
	}
	generation, ok := integer(status["session_generation"])
	enabled, valid := status["enabled"].(bool)
	if !ok || generation != g || !valid {
		return errors.New("effects generation changed; refusing recovery")
	}
	cleanupFailed := status["cleanup_failed"] == true
	if enabled {
		stopped, e := b.native(ctx, fmt.Sprintf("guard %d off", g))
		if e != nil {
			return e
		}
		generation, ok := integer(stopped["session_generation"])
		if !ok || generation != g || stopped["enabled"] != false {
			return errors.New("native stop acknowledgement invalid; refusing unload")
		}
		cleanupFailed = cleanupFailed || stopped["cleanup_failed"] == true
	}
	if e = b.unload(ctx); e != nil {
		return e
	}
	if cleanupFailed {
		return errors.New("native resource cleanup failed; runtime evidence retained")
	}
	return nil
}
