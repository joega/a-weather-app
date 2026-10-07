package effects

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/supervision"
)

type backend interface {
	preflight(context.Context, string, string) (int, error)
	load(context.Context) error
	native(context.Context, string) (object, error)
	unload(context.Context) error
	ctl(context.Context, bool, ...string) (any, error)
}

type session struct {
	b                                 backend
	root, instance, output, state     string
	lastError                         error
	generation                        int64
	loaded, persistent, cleanupFailed bool
	directory                         *runtimeDir
	deadline                          time.Time
	lastUpdates                       map[string]string
	fps, restarts                     int
	host                              *supervision.Process
	log                               *os.File
	sequence                          int64
	lastPublishedEffects              object
	lastWeatherPublished              time.Time
	now                               func() time.Time
	spawn                             func(int, int) error
	alive                             func() bool
	stopHost                          func(context.Context) error
	renewHost                         func() error
}

func newSession(root, instance, output string) *session {
	s := &session{b: newBackend(root), root: root, instance: instance, output: output, state: "stopped", now: time.Now}
	s.spawn = s.spawnHost
	s.alive = func() bool { return s.host != nil && s.host.Alive() }
	s.stopHost = s.stopOwnedHost
	s.renewHost = func() error {
		if s.host == nil {
			return errors.New("effects lease child missing")
		}
		return s.host.Signal(syscall.SIGUSR1)
	}
	return s
}
func (s *session) status() object {
	var generation any
	var failure any
	if s.generation > 0 {
		generation = s.generation
	}
	if s.lastError != nil {
		failure = bounded(s.lastError.Error(), 1024)
	}
	remaining := 0.
	if s.state == "running" {
		remaining = math.Max(0, s.deadline.Sub(s.now()).Seconds())
	}
	return object{"state": s.state, "error": failure, "session_generation": generation, "persistent": s.persistent && s.state == "running", "remaining_seconds": remaining}
}
func bounded(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
func (s *session) guard(command string) string {
	return fmt.Sprintf("guard %d %s", s.generation, command)
}
func (s *session) verify(status, flags object) error {
	g, ok := integer(status["session_generation"])
	if status["enabled"] != true || !ok || g != s.generation {
		return errors.New("native session generation verification failed")
	}
	fps, ok := integer(status["fps"])
	if !ok || fps != int64(flags["fps"].(int)) {
		return errors.New("native fps verification failed")
	}
	for _, key := range []string{"reduced_motion", "window_physics", "accumulation"} {
		if status[key] != flags[key] {
			return errors.New("native control verification failed")
		}
	}
	return nil
}
func (s *session) start(ctx context.Context, duration int, persistent bool, value object) (err error) {
	if s.state != "stopped" {
		return errors.New("effects already active or cleanup unresolved")
	}
	if duration < 1 || duration > 300 || !instancePattern.MatchString(s.instance) || !outputPattern.MatchString(s.output) {
		return errors.New("invalid effects selectors or duration")
	}
	flags, e := controls(value)
	if e != nil {
		return e
	}
	if persistent {
		duration = 30
	}
	s.persistent = persistent
	s.state = "starting"
	s.lastError = nil
	s.lastUpdates = map[string]string{}
	s.lastPublishedEffects = nil
	s.lastWeatherPublished = time.Time{}
	defer func() {
		if err != nil {
			s.lastError = err
			cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			if e := s.stop(cleanup); e != nil {
				err = errors.Join(err, e)
			}
		}
	}()
	monitor, e := s.b.preflight(ctx, s.instance, s.output)
	if e != nil {
		return e
	}
	path, e := os.MkdirTemp("", "a-weather-app-effects-")
	if e != nil {
		return e
	}
	s.directory, e = captureDirectory(path)
	if e != nil {
		return e
	}
	if e = s.directory.publish("policy.json", policyEnvelope(s.instance, s.output, 1, "producer_stopped", 0)); e != nil {
		return e
	}
	s.loaded = true
	if e = s.b.load(ctx); e != nil {
		return e
	}
	initial, e := s.b.native(ctx, "status")
	if e != nil {
		return e
	}
	if initial["enabled"] != false {
		return errors.New("new plugin must start disabled")
	}
	for _, key := range []string{"reduced_motion", "window_physics", "accumulation", "fps"} {
		if _, e = s.b.native(ctx, key+" "+formatted(flags[key])); e != nil {
			return e
		}
	}
	for _, command := range []string{"set rain_intensity 0", "snow intensity 0"} {
		if _, e = s.b.native(ctx, command); e != nil {
			return e
		}
	}
	s.deadline = s.now().Add(time.Duration(duration) * time.Second)
	status, e := s.b.native(ctx, fmt.Sprintf("on %d %d", duration, monitor))
	if e != nil {
		return e
	}
	generation, ok := integer(status["session_generation"])
	if !ok || generation <= 0 || status["enabled"] != true {
		return errors.New("native session identity missing")
	}
	s.generation = generation
	if e = s.verify(status, flags); e != nil {
		return e
	}
	s.fps = flags["fps"].(int)
	if e = s.spawn(duration, s.fps); e != nil {
		return e
	}
	s.state = "running"
	return nil
}
func (s *session) tick(ctx context.Context, selected, value object) (err error) {
	if s.state != "running" {
		return nil
	}
	if !s.now().Before(s.deadline) {
		cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		return s.stop(cleanup)
	}
	defer func() {
		if err != nil {
			s.lastError = err
			cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			err = errors.Join(err, s.stop(cleanup))
		}
	}()
	flags, e := controls(value)
	if e != nil {
		return e
	}
	statusObservedAt := s.now()
	status, e := s.b.native(ctx, "status")
	if e != nil {
		return e
	}
	g, ok := integer(status["session_generation"])
	if !ok || status["enabled"] != true || g != s.generation {
		return errors.New("native session replaced or expired")
	}
	if !s.alive() {
		return errors.New("effects child stopped")
	}
	if e = s.boundLog(); e != nil {
		return e
	}
	if s.persistent && s.deadline.Sub(s.now()) <= 20*time.Second {
		renewed, e := s.b.native(ctx, s.guard("renew 30"))
		if e != nil {
			return e
		}
		generation, ok := integer(renewed["session_generation"])
		if !ok || renewed["enabled"] != true || generation != s.generation {
			return errors.New("native lease renewal identity mismatch")
		}
		status = renewed
		if e = s.renewHost(); e != nil {
			return e
		}
		s.deadline = s.now().Add(30 * time.Second)
	}
	targetChanged := false
	if selected != nil {
		effects := obj(selected["effects"])
		if effects == nil {
			return errors.New("selected effects missing")
		}
		effects = clone(effects)
		updates := map[string]string{}
		ordered := []string{"reduced_motion", "window_physics", "accumulation", "fps", "set rain_intensity", "snow intensity", "set wind_x"}
		for _, key := range ordered[:4] {
			updates[key] = formatted(flags[key])
		}
		for _, row := range []struct {
			key, command string
			low, high    float64
		}{{"rain_intensity", "set rain_intensity", 0, 1}, {"snow_intensity", "snow intensity", 0, 1}, {"wind_x", "set wind_x", -500, 500}} {
			v, ok := number(effects[row.key])
			if !ok || v < row.low || v > row.high {
				return errors.New("invalid selected " + row.key)
			}
			updates[row.command] = formatted(v)
		}
		for _, command := range ordered {
			if s.lastUpdates[command] != updates[command] {
				if _, e = s.b.native(ctx, s.guard(command+" "+updates[command])); e != nil {
					return e
				}
				targetChanged = true
			}
		}
		temperature, until, known := temperatureLease(selected, s.now())
		thermal := "unknown"
		if known {
			thermal = formatted(temperature) + " " + strconv.FormatInt(until, 10)
		}
		updates["snow temperature"] = thermal
		if s.lastUpdates["snow temperature"] != thermal {
			if _, e = s.b.native(ctx, s.guard("snow temperature "+thermal)); e != nil {
				return e
			}
			targetChanged = true
		}
		after := status
		if targetChanged {
			statusObservedAt = s.now()
			after, e = s.b.native(ctx, "status")
			if e != nil {
				return e
			}
		}
		if e = s.verify(after, flags); e != nil {
			return e
		}
		// This is fresher than the initial heartbeat status. Keep its lock and
		// cleanup evidence for the policy publication later in this tick.
		status = after
		snow := obj(after["snow_target_parameters"])
		targets := obj(after["target_parameters"])
		for _, key := range []string{"rain_intensity", "snow_intensity", "wind_x"} {
			actual := targets[key]
			if key == "snow_intensity" {
				actual = snow["intensity"]
			}
			v, ok := number(actual)
			expected := num(effects[key])
			if !ok || math.Abs(v-expected) > math.Max(1e-6, 1e-5*math.Max(math.Abs(v), math.Abs(expected))) {
				return errors.New("native target verification failed")
			}
		}
		actualKnown, kok := snow["temperature_known"].(bool)
		if !kok || (actualKnown != known && !(known && s.now().UnixMilli() >= until && !actualKnown)) {
			return errors.New("native temperature verification failed")
		}
		if actualKnown {
			v, ok := number(snow["temperature_c"])
			if !ok || math.Abs(v-temperature) > math.Max(1e-6, 1e-5*math.Abs(temperature)) {
				return errors.New("native temperature verification failed")
			}
		}
		s.lastUpdates = updates
		effects["reduced_motion"] = flags["reduced_motion"]
		effects["lightning_enabled"] = flags["lightning_enabled"] == true && effects["lightning_enabled"] == true && flags["reduced_motion"] != true
		// The native host polls this file once per second. Avoid replacing and
		// syncing it on every 500 ms policy heartbeat: solar angles are the only
		// continuously varying values, and one-second sky updates preserve the
		// consumer's sampling resolution. Meaningful weather/control changes
		// still publish immediately.
		stableEffects := clone(effects)
		delete(stableEffects, "sun_elevation")
		delete(stableEffects, "sun_azimuth")
		if !reflect.DeepEqual(stableEffects, s.lastPublishedEffects) || s.now().Sub(s.lastWeatherPublished) >= time.Second {
			if e = s.directory.publish("selected.json", object{"schema_version": selected["schema_version"], "selected_at": selected["selected_at"], "effects": effects}); e != nil {
				return e
			}
			s.lastPublishedEffects = stableEffects
			s.lastWeatherPublished = s.now()
		}
	}
	if flags["fps"].(int) != s.fps {
		if s.restarts >= 8 {
			return errors.New("finite atmosphere restart budget exceeded")
		}
		if e = s.stopHost(ctx); e != nil {
			return e
		}
		s.restarts++
		remaining := int(s.deadline.Sub(s.now()).Seconds())
		if s.persistent {
			remaining = 30
		}
		if remaining < 1 {
			return errors.New("effects expiry during host restart")
		}
		if e = s.spawn(remaining, flags["fps"].(int)); e != nil {
			return e
		}
		s.fps = flags["fps"].(int)
	}
	// Observe presentation metadata on every 500 ms heartbeat. Direct socket
	// queries avoid child processes and leave headroom for the native host's
	// 500 ms consumer poll and 1500 ms freshness cutoff. Timestamp before the
	// queries so delayed replies cannot make old evidence appear fresh.
	metadataObservedAt := s.now()
	reason := "ipc_error"
	monitors, e := s.b.ctl(ctx, true, "monitors", "all")
	if e == nil {
		clients, e := s.b.ctl(ctx, true, "clients")
		if e == nil {
			reason = policyMetadataDecision(monitors, clients, s.output)
		}
	}
	// Never carry an allow across new lock or cleanup denial evidence.
	reason = applyLockDecision(reason, status)
	s.sequence++
	policyObservedAt := statusObservedAt
	if metadataObservedAt.Before(policyObservedAt) {
		policyObservedAt = metadataObservedAt
	}
	if e = s.directory.publish("policy.json", policyEnvelope(s.instance, s.output, s.sequence, reason, policyObservedAt.UnixMilli())); e != nil {
		return e
	}
	// A disconnected or disabled selected output cannot be rebound safely to a
	// newly created monitor object. Stop the owned generation; a later explicit
	// compatibility check can start a fresh session after reconnection.
	if reason == "output_missing" || reason == "output_disabled" || reason == "output_ambiguous" {
		cleanup, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		return s.stop(cleanup)
	}
	return nil
}
func (s *session) stop(ctx context.Context) error {
	var errs []error
	if s.directory != nil {
		s.sequence++
		if e := s.directory.publish("policy.json", policyEnvelope(s.instance, s.output, s.sequence, "producer_stopped", s.now().UnixMilli())); e != nil {
			errs = append(errs, e)
		}
	}
	if e := s.stopHost(ctx); e != nil {
		errs = append(errs, e)
	}
	if s.loaded {
		e := func() error {
			status, e := s.b.native(ctx, "status")
			if e != nil {
				return e
			}
			g, ok := integer(status["session_generation"])
			if s.generation > 0 && (!ok || g != s.generation) {
				return errors.New("native ownership changed; refusing stop/unload")
			}
			enabled, valid := status["enabled"].(bool)
			if !valid {
				return errors.New("invalid native enabled acknowledgement")
			}
			s.cleanupFailed = s.cleanupFailed || status["cleanup_failed"] == true
			if enabled {
				if s.generation == 0 {
					return errors.New("native ownership unknown; refusing stop/unload")
				}
				stopped, e := s.b.native(ctx, s.guard("off"))
				if e != nil {
					// The finite native lease may expire after the status read but
					// before the guarded off reaches Hyprland. Re-observe instead
					// of treating that already-stopped owned generation as a
					// failed cleanup. A replacement generation still fails closed.
					stopped, e = s.b.native(ctx, "status")
					if e != nil {
						return e
					}
				}
				sg, ok := integer(stopped["session_generation"])
				if !ok || sg != s.generation || stopped["enabled"] != false {
					return errors.New("native stop acknowledgement invalid; refusing unload")
				}
				s.cleanupFailed = s.cleanupFailed || stopped["cleanup_failed"] == true
			}
			if e = s.b.unload(ctx); e != nil {
				return e
			}
			s.loaded = false
			return nil
		}()
		if e != nil {
			errs = append(errs, e)
		}
	}
	if s.cleanupFailed {
		errs = append(errs, errors.New("native resource cleanup failed; runtime evidence retained"))
	}
	if len(errs) == 0 && s.directory != nil {
		if e := s.directory.cleanup(); e != nil {
			errs = append(errs, e)
		} else {
			s.directory = nil
		}
	}
	if len(errs) > 0 {
		s.state = "cleanup_failed"
		s.lastError = errors.Join(append([]error{s.lastError}, errs...)...)
	} else {
		s.state = "stopped"
	}
	if !s.loaded {
		s.generation = 0
	}
	return errors.Join(errs...)
}
func (s *session) spawnHost(duration, fps int) error {
	if e := s.directory.check(); e != nil {
		return e
	}
	name := "child-1.jsonl"
	if s.restarts > 0 {
		name = fmt.Sprintf("host-restart-%d.jsonl", s.restarts)
	}
	log, e := s.directory.log(name)
	if e != nil {
		return e
	}
	s.log = log
	env := childEnvironment()
	if b, ok := s.b.(*nativeBackend); ok {
		env = b.env
	}
	command := []string{"/usr/bin/prlimit", "--fsize=1048576", "--core=0", "--", filepath.Join(s.root, hostRelative), "--duration", strconv.Itoa(duration), "--fps", strconv.Itoa(fps), "--weather", filepath.Join(s.directory.path, "selected.json"), "--policy", filepath.Join(s.directory.path, "policy.json"), "--policy-session", s.instance, "--output", s.output}
	if s.persistent {
		command = append(command, "--renewable-lease")
	}
	s.host, e = supervision.StartChild(command, env, s.root, nil, log, log)
	if e != nil {
		log.Close()
		s.log = nil
	}
	return e
}
func (s *session) stopOwnedHost(ctx context.Context) error {
	var err error
	if s.host != nil {
		err = s.host.Cleanup(ctx, 3*time.Second)
		s.host = nil
	}
	if s.log != nil {
		s.log.Close()
		s.log = nil
	}
	return err
}
func (s *session) boundLog() error {
	if s.log == nil {
		return nil
	}
	info, e := s.log.Stat()
	if e != nil {
		return e
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || info.Mode().Perm()&077 != 0 {
		return errors.New("effects log identity changed")
	}
	if info.Size() >= 262144 {
		return s.log.Truncate(0)
	}
	return nil
}
func clone(m object) object {
	r := object{}
	for k, v := range m {
		r[k] = v
	}
	return r
}
