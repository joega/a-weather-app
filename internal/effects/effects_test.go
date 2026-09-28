package effects

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type fakeNative struct {
	status   object
	commands []string
	ctlCalls int
	unloaded bool
	reject   string
	monitors any
	clients  any
	advance  func(time.Duration)
	ctlDelay time.Duration
}

func newFake() *fakeNative {
	return &fakeNative{status: object{"enabled": false, "session_generation": float64(7), "fps": float64(30), "reduced_motion": false, "window_physics": true, "accumulation": true, "target_parameters": object{"rain_intensity": float64(0), "wind_x": float64(0)}, "snow_target_parameters": object{"intensity": float64(0), "temperature_known": false}}}
}
func (f *fakeNative) preflight(context.Context, string, string) (int, error) { return 0, nil }
func (f *fakeNative) load(context.Context) error                             { return nil }
func (f *fakeNative) unload(context.Context) error                           { f.unloaded = true; return nil }
func (f *fakeNative) ctl(_ context.Context, _ bool, args ...string) (any, error) {
	f.ctlCalls++
	if f.advance != nil && f.ctlDelay > 0 {
		f.advance(f.ctlDelay)
	}
	if len(args) > 0 && args[0] == "monitors" && f.monitors != nil {
		return f.monitors, nil
	}
	if len(args) > 0 && args[0] == "clients" && f.clients != nil {
		return f.clients, nil
	}
	return []any{}, nil
}
func (f *fakeNative) native(ctx context.Context, command string) (object, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	f.commands = append(f.commands, command)
	if command == f.reject {
		return nil, errors.New("injected failure")
	}
	parts := strings.Fields(command)
	if len(parts) > 0 && parts[0] == "guard" {
		g, e := strconv.Atoi(parts[1])
		if e != nil || int(num(f.status["session_generation"])) != g {
			return nil, errors.New("foreign generation")
		}
		parts = parts[2:]
		command = strings.Join(parts, " ")
	}
	switch {
	case command == "status":
	case command == "off":
		f.status["enabled"] = false
	case strings.HasPrefix(command, "on "):
		f.status["enabled"] = true
	case strings.HasPrefix(command, "renew "):
	case strings.HasPrefix(command, "set "):
		n, _ := strconv.ParseFloat(parts[2], 64)
		obj(f.status["target_parameters"])[parts[1]] = n
	case strings.HasPrefix(command, "snow intensity "):
		n, _ := strconv.ParseFloat(parts[2], 64)
		obj(f.status["snow_target_parameters"])["intensity"] = n
	case strings.HasPrefix(command, "snow temperature "):
		snow := obj(f.status["snow_target_parameters"])
		snow["temperature_known"] = parts[2] != "unknown"
		if parts[2] != "unknown" {
			n, _ := strconv.ParseFloat(parts[2], 64)
			snow["temperature_c"] = n
		}
	case len(parts) == 2:
		if parts[0] == "fps" {
			n, _ := strconv.ParseFloat(parts[1], 64)
			f.status["fps"] = n
		} else {
			f.status[parts[0]] = parts[1] == "true"
		}
	default:
		return nil, errors.New("unknown fake native command")
	}
	return clone(f.status), nil
}
func testSession(t *testing.T) (*session, *fakeNative) {
	t.Helper()
	s := newSession(t.TempDir(), "abc_1_2", "DP-1")
	f := newFake()
	s.b = f
	s.spawn = func(int, int) error { return nil }
	s.alive = func() bool { return true }
	s.stopHost = func(context.Context) error { return nil }
	s.renewHost = func() error { return nil }
	t.Cleanup(func() {
		if s.directory != nil {
			os.RemoveAll(s.directory.path)
		}
	})
	return s, f
}
func TestForeignGenerationRefusesStopAndRecovery(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		s, f := testSession(t)
		if e := s.start(context.Background(), 30, false, object{}); e != nil {
			t.Fatal(e)
		}
		f.status["session_generation"] = float64(8)
		before := len(f.commands)
		var e error
		if recovery {
			e = recoverGeneration(context.Background(), f, 7)
		} else {
			e = s.stop(context.Background())
		}
		if e == nil || f.unloaded {
			t.Fatal("foreign generation was adopted", e)
		}
		for _, c := range f.commands[before:] {
			if strings.Contains(c, "off") {
				t.Fatal("foreign generation stopped", c)
			}
		}
	}
}
func TestCleanupFailureRetainsEvidence(t *testing.T) {
	s, f := testSession(t)
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	path := s.directory.path
	f.status["cleanup_failed"] = true
	if e := s.stop(context.Background()); e == nil {
		t.Fatal("cleanup failure hidden")
	}
	if s.state != "cleanup_failed" || !f.unloaded {
		t.Fatal(s.status())
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("evidence removed", e)
	}
	if e := s.start(context.Background(), 30, false, object{}); e == nil {
		t.Fatal("restarted unresolved cleanup")
	}
}
func TestReplacementRuntimeDirectoryNotTouched(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "session")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := captureDirectory(path)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if d.fd >= 0 {
			os.NewFile(uintptr(d.fd), path).Close()
		}
	}()
	if e = d.publish("selected.json", object{"a": 1}); e != nil {
		t.Fatal(e)
	}
	os.Rename(path, path+"-old")
	os.Mkdir(path, 0700)
	marker := filepath.Join(path, "selected.json")
	os.WriteFile(marker, []byte("foreign"), 0600)
	if e = d.publish("selected.json", object{"a": 2}); e == nil {
		t.Fatal("published into replaced runtime")
	}
	if e = d.cleanup(); e == nil {
		t.Fatal("removed replaced runtime")
	}
	raw, _ := os.ReadFile(marker)
	if string(raw) != "foreign" {
		t.Fatal("replacement modified")
	}
}
func TestRuntimeRefusesUnknownEntryAndSymlink(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		path := t.TempDir()
		os.Chmod(path, 0700)
		d, e := captureDirectory(path)
		if e != nil {
			t.Fatal(e)
		}
		if symlink {
			os.Symlink("/dev/null", filepath.Join(path, "selected.json"))
		} else {
			os.WriteFile(filepath.Join(path, "settings.json"), []byte("keep"), 0600)
		}
		if e = d.cleanup(); e == nil {
			t.Fatal("unmanaged entry removed")
		}
		os.NewFile(uintptr(d.fd), path).Close()
	}
}
func TestPreviewLeaseAndThermalProvenance(t *testing.T) {
	s, f := testSession(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	original := s.deadline
	selected := object{"schema_version": float64(1), "selected_at": now.Format(time.RFC3339Nano), "mode": "live", "freshness": "fresh", "current": object{"temperature_c": float64(2), "time": now.Format(time.RFC3339Nano)}, "forecast": object{"fetched_at": now.Format(time.RFC3339Nano)}, "effects": object{"rain_intensity": float64(.2), "snow_intensity": float64(.3), "wind_x": float64(2), "lightning_enabled": true}}
	if e := s.tick(context.Background(), selected, object{"fps": float64(15), "reduced_motion": true, "lightning_enabled": true}); e != nil {
		t.Fatal(e)
	}
	if s.deadline != original {
		t.Fatal("preview expiry extended")
	}
	if obj(f.status["snow_target_parameters"])["temperature_known"] != true {
		t.Fatal("fresh thermal observation missing")
	}
	now = now.Add(9 * time.Second)
	if e := s.tick(context.Background(), selected, object{"fps": 15}); e != nil {
		t.Fatal(e)
	}
	if obj(f.status["snow_target_parameters"])["temperature_known"] != false {
		t.Fatal("stale thermal observation retained")
	}
	if e := s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestSkyPublicationMatchesConsumerCadenceAndChangeEvents(t *testing.T) {
	s, _ := testSession(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	selected := object{
		"schema_version": float64(1), "selected_at": now.Format(time.RFC3339Nano),
		"mode": "live", "freshness": "fresh",
		"current":  object{"temperature_c": float64(2), "time": now.Format(time.RFC3339Nano)},
		"forecast": object{"fetched_at": now.Format(time.RFC3339Nano)},
		"effects":  object{"rain_intensity": float64(.2), "snow_intensity": float64(.3), "wind_x": float64(2), "sun_elevation": float64(30), "sun_azimuth": float64(180), "lightning_enabled": true},
	}
	if e := s.tick(context.Background(), selected, object{}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(s.directory.path, "selected.json")
	original, e := os.Stat(path)
	if e != nil {
		t.Fatal(e)
	}
	// Solar-angle changes are sampled once per second by the C host, so an
	// intermediate 500 ms policy tick should not rewrite/fsync its weather file.
	now = now.Add(100 * time.Millisecond)
	selected["selected_at"] = now.Format(time.RFC3339Nano)
	obj(selected["effects"])["sun_elevation"] = float64(30.001)
	if e = s.tick(context.Background(), selected, object{}); e != nil {
		t.Fatal(e)
	}
	unchanged, e := os.Stat(path)
	if e != nil || !os.SameFile(original, unchanged) {
		t.Fatal("solar-only subsecond tick replaced the sky file", e)
	}
	// Target/control changes still publish immediately, without waiting for the
	// one-second freshness refresh.
	now = now.Add(100 * time.Millisecond)
	selected["selected_at"] = now.Format(time.RFC3339Nano)
	obj(selected["effects"])["rain_intensity"] = float64(.4)
	if e = s.tick(context.Background(), selected, object{}); e != nil {
		t.Fatal(e)
	}
	changed, e := os.Stat(path)
	if e != nil || os.SameFile(original, changed) {
		t.Fatal("meaningful weather change was not published immediately", e)
	}
	raw, e := os.ReadFile(path)
	var sky object
	if e != nil || json.Unmarshal(raw, &sky) != nil || num(obj(sky["effects"])["rain_intensity"]) != .4 {
		t.Fatal("published sky missed the changed rain target", e)
	}
	now = now.Add(time.Second)
	selected["selected_at"] = now.Format(time.RFC3339Nano)
	obj(selected["effects"])["sun_elevation"] = float64(30.01)
	if e = s.tick(context.Background(), selected, object{}); e != nil {
		t.Fatal(e)
	}
	refreshed, e := os.Stat(path)
	if e != nil || os.SameFile(changed, refreshed) {
		t.Fatal("unchanged sky did not refresh its selected_at lease", e)
	}
	if e = s.stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestPolicyMetadataRefreshedOnEveryHeartbeat(t *testing.T) {
	s, f := testSession(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	defer s.stop(context.Background())
	if e := s.tick(context.Background(), nil, object{}); e != nil {
		t.Fatal(e)
	}
	if f.ctlCalls != 2 {
		t.Fatal("initial policy monitor/client check missing", f.ctlCalls)
	}
	now = now.Add(500 * time.Millisecond)
	if e := s.tick(context.Background(), nil, object{}); e != nil {
		t.Fatal(e)
	}
	if f.ctlCalls != 4 {
		t.Fatal("policy heartbeat reused prior metadata", f.ctlCalls)
	}
	now = now.Add(500 * time.Millisecond)
	if e := s.tick(context.Background(), nil, object{}); e != nil {
		t.Fatal(e)
	}
	if f.ctlCalls != 6 {
		t.Fatal("policy metadata not refreshed on third heartbeat", f.ctlCalls)
	}
}

func TestSlowMetadataCannotRefreshPolicyEvidenceTimestamp(t *testing.T) {
	s, f := testSession(t)
	now := time.Now().UTC()
	s.now = func() time.Time { return now }
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	defer s.stop(context.Background())
	f.status["lock_state_known"] = true
	f.status["session_locked"] = false
	f.monitors = []any{object{"id": float64(1), "name": "DP-1", "disabled": false, "dpmsStatus": true, "activeWorkspace": object{"id": float64(1)}}}
	f.clients = []any{}
	f.advance = func(d time.Duration) { now = now.Add(d) }
	f.ctlDelay = time.Second
	observedAt := now.UnixMilli()
	if e := s.tick(context.Background(), nil, object{}); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(s.directory.path, "policy.json"))
	if e != nil {
		t.Fatal(e)
	}
	policy, e := safeio.Object(raw, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if policy["render_allowed"] != true {
		t.Fatal("expected valid metadata to allow rendering", policy)
	}
	if got := int64(num(policy["generated_at_unix_ms"])); got != observedAt {
		t.Fatalf("slow query refreshed old evidence: generated_at=%d want observation start %d", got, observedAt)
	}
	if now.Sub(time.UnixMilli(observedAt)) < 2*time.Second {
		t.Fatal("test did not simulate slow metadata queries", now.Sub(time.UnixMilli(observedAt)))
	}
}

func TestFreshLockDenialsOverrideAllowedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		update       func(*fakeNative)
	}{
		{"locked", "session_lock", func(f *fakeNative) { f.status["session_locked"] = true }},
		{"unknown", "lock_unknown", func(f *fakeNative) { f.status["lock_state_known"] = false }},
		{"cleanup failed", "lock_unknown", func(f *fakeNative) {
			f.status["cleanup_failed"] = true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, f := testSession(t)
			now := time.Now().UTC()
			s.now = func() time.Time { return now }
			if e := s.start(context.Background(), 30, false, object{}); e != nil {
				t.Fatal(e)
			}
			defer s.stop(context.Background())
			f.status["lock_state_known"] = true
			f.status["session_locked"] = false
			f.monitors = []any{object{"id": float64(1), "name": "DP-1", "disabled": false, "dpmsStatus": true, "activeWorkspace": object{"id": float64(1)}}}
			f.clients = []any{}
			readPolicy := func() object {
				t.Helper()
				b, e := os.ReadFile(filepath.Join(s.directory.path, "policy.json"))
				if e != nil {
					t.Fatal(e)
				}
				v, e := safeio.Object(b, 4096)
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
			if e := s.tick(context.Background(), nil, object{}); e != nil {
				t.Fatal(e)
			}
			if got := readPolicy(); got["render_allowed"] != true {
				t.Fatal("expected initial allow", got)
			}
			calls := f.ctlCalls
			tc.update(f)
			now = now.Add(500 * time.Millisecond)
			if e := s.tick(context.Background(), nil, object{}); e != nil {
				t.Fatal(e)
			}
			if f.ctlCalls != calls+2 {
				t.Fatal("metadata not refreshed with lock observation")
			}
			if got := readPolicy(); got["render_allowed"] != false || got["reason"] != tc.reason {
				t.Fatal("fresh lock denial ignored", got)
			}
		})
	}
}

type transitionStatusBackend struct {
	backend
	calls int
}

func (b *transitionStatusBackend) native(ctx context.Context, command string) (object, error) {
	status, err := b.backend.native(ctx, command)
	if err == nil && command == "status" {
		b.calls++
		if b.calls == 1 {
			status["lock_state_known"], status["session_locked"] = true, false
		}
		if b.calls == 2 {
			status["lock_state_known"], status["session_locked"] = true, true
		}
	}
	return status, err
}

func TestPostUpdateNativeStatusDenialIsPublishedImmediately(t *testing.T) {
	s, f := testSession(t)
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	defer s.stop(context.Background())
	f.monitors = []any{object{"id": float64(1), "name": "DP-1", "disabled": false, "dpmsStatus": true, "activeWorkspace": object{"id": float64(1)}}}
	f.clients = []any{}
	b := &transitionStatusBackend{backend: f}
	s.b = b
	selected := object{"schema_version": float64(1), "selected_at": time.Now().UTC().Format(time.RFC3339Nano), "effects": object{"rain_intensity": float64(.1), "snow_intensity": float64(.1), "wind_x": float64(1)}}
	if e := s.tick(context.Background(), selected, object{}); e != nil {
		t.Fatal(e)
	}
	policyBytes, e := os.ReadFile(filepath.Join(s.directory.path, "policy.json"))
	if e != nil {
		t.Fatal(e)
	}
	policy, e := safeio.Object(policyBytes, 4096)
	if e != nil {
		t.Fatal(e)
	}
	if policy["render_allowed"] != false || policy["reason"] != "session_lock" {
		t.Fatal("new post-update denial was discarded", policy, b.calls)
	}
}
func TestControlAndOutputParsing(t *testing.T) {
	for _, bad := range []object{{"fps": true}, {"fps": 30.5}, {"fps": "30"}, {"window_physics": 1}, {"lightning_enabled": "false"}} {
		if _, e := controls(bad); e == nil {
			t.Fatal("accepted invalid controls", bad)
		}
	}
	monitor := object{"name": "DP-1", "disabled": false, "width": 1920., "height": 1080., "scale": 1., "x": 0., "y": 0., "transform": 0., "mirrorOf": "none", "colorManagementPreset": "srgb"}
	if e := validateOutput(monitor); e != nil {
		t.Fatal(e)
	}
	monitor["name"] = "HEADLESS-2"
	if e := validateOutput(monitor); e == nil {
		t.Fatal("accepted virtual headless output")
	}
	monitor["name"] = "DP-1"
	monitor["transform"] = false
	if e := validateOutput(monitor); e == nil {
		t.Fatal("accepted boolean transform")
	}
	if _, e := monitorRecords([]any{monitor, monitor}); e == nil {
		t.Fatal("duplicate output accepted")
	}
	monitor["transform"] = float64(0)
	laptop := object{"name": "eDP-1", "disabled": false, "width": 1920., "height": 1200., "scale": 1.25}
	rows := []any{laptop, monitor}
	if selected, e := selectedOutput(rows, "DP-1"); e != nil || selected["name"] != "DP-1" {
		t.Fatal("external output not selected from two enabled monitors", e)
	}
	if selected, e := selectedOutput([]any{monitor, laptop}, "DP-1"); e != nil || selected["name"] != "DP-1" {
		t.Fatal("monitor reorder changed the selected output", e)
	}
	if _, e := selectedOutput([]any{laptop}, "DP-1"); e == nil {
		t.Fatal("disconnected output accepted")
	}
	if _, e := selectedOutput([]any{laptop, monitor, monitor}, "DP-1"); e == nil {
		t.Fatal("duplicate selected output accepted")
	}
	monitor["disabled"] = true
	if _, e := selectedOutput(rows, "DP-1"); e == nil {
		t.Fatal("disabled selected output accepted")
	}
}
func TestPolicyFailsClosed(t *testing.T) {
	monitor := object{"id": float64(0), "name": "DP-1", "disabled": false, "dpmsStatus": true, "activeWorkspace": object{"id": float64(1)}}
	laptop := object{"id": float64(1), "name": "eDP-1", "disabled": false, "dpmsStatus": true, "activeWorkspace": object{"id": float64(2)}}
	lock := object{"enabled": true, "lock_state_known": true, "session_locked": false}
	if reason := policyDecision([]any{laptop, monitor}, []any{}, "DP-1", lock); reason != "none" {
		t.Fatal("unselected connected monitor blocked effects", reason)
	}
	if reason := policyDecision([]any{monitor, laptop}, []any{}, "DP-1", lock); reason != "none" {
		t.Fatal("monitor reorder blocked effects", reason)
	}
	if reason := policyDecision([]any{laptop}, []any{}, "DP-1", lock); reason != "output_missing" {
		t.Fatal("selected monitor disconnect was not suppressed", reason)
	}
	monitor["disabled"] = true
	if reason := policyDecision([]any{laptop, monitor}, []any{}, "DP-1", lock); reason != "output_disabled" {
		t.Fatal("selected monitor disable was not suppressed", reason)
	}
	monitor["disabled"] = false
	if reason := policyDecision([]any{monitor}, []any{}, "DP-1", lock); reason != "none" {
		t.Fatal(reason)
	}
	lock["session_locked"] = true
	if reason := policyDecision([]any{monitor}, []any{}, "DP-1", lock); reason != "session_lock" {
		t.Fatal(reason)
	}
	delete(lock, "session_locked")
	if reason := policyDecision([]any{monitor}, []any{}, "DP-1", lock); reason != "lock_unknown" {
		t.Fatal(reason)
	}
}
func TestWorkerEOFStopsOwnedSession(t *testing.T) {
	s, f := testSession(t)
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan int, 1)
	go func() { done <- serveWorker(s, inR, outW) }()
	raw, _ := json.Marshal(object{"id": 1, "op": "start", "duration": 30, "persistent": false, "controls": object{}})
	inW.Write(append(raw, '\n'))
	outR.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, e := bufio.NewReader(outR).ReadBytes('\n'); e != nil {
		t.Fatal(e)
	}
	inW.Close()
	select {
	case code := <-done:
		if code != 0 || !f.unloaded || s.state != "stopped" {
			t.Fatal(code, s.status())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop on EOF")
	}
}
func TestWorkerCrashTriggersAcknowledgedRecovery(t *testing.T) {
	m := New(t.TempDir(), "", "", "")
	r, w, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer r.Close()
	defer outW.Close()
	p, e := supervision.Start([]string{"/bin/sh", "-c", "exit 9"}, []string{"PATH=/usr/bin:/bin"}, "", r, outW, nil)
	if e != nil {
		t.Fatal(e)
	}
	m.process = p
	m.input = w
	m.reply = outR
	m.current["state"] = "running"
	m.ownership = object{"generation": float64(7)}
	recovered := false
	m.recover = func(_ context.Context, ownership object) error {
		recovered = reflect.DeepEqual(ownership, m.ownership)
		return nil
	}
	deadline := time.Now().Add(time.Second)
	for p.Alive() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	status := m.Status()
	if !recovered || m.process != nil || status["state"] != "cleanup_failed" {
		t.Fatal("crash not recovered visibly", status, recovered)
	}
}

func TestWorkerRejectsMalformedRequestsBeforeActivation(t *testing.T) {
	for _, raw := range []string{`{"id":1,"id":2,"op":"start","duration":30,"persistent":false,"controls":{}}`, strings.Repeat(" ", requestLimit) + "{}"} {
		s, f := testSession(t)
		inR, inW, _ := os.Pipe()
		outR, outW, _ := os.Pipe()
		done := make(chan int, 1)
		go func() { done <- serveWorker(s, inR, outW) }()
		go func() { inW.Write([]byte(raw + "\n")); inW.Close() }()
		select {
		case code := <-done:
			if code != 1 || len(f.commands) > 0 {
				t.Fatal("malformed request activated native backend", code, f.commands)
			}
		case <-time.After(time.Second):
			t.Fatal("unbounded malformed request")
		}
		inR.Close()
		outR.Close()
		outW.Close()
	}
}
func TestNativeArtifactAndAcknowledgementValidation(t *testing.T) {
	if e := symbolCheck([]byte("not ELF"), nil); e == nil {
		t.Fatal("accepted malformed plugin")
	}
	for _, value := range []string{strings.Repeat("g", 40), strings.Repeat("A", 40), strings.Repeat("a", 39)} {
		if validHex(value, 20) {
			t.Fatal("accepted malformed source hash")
		}
	}
	m := New(t.TempDir(), "", "", "")
	if e := m.fallback(context.Background(), nil); e == nil {
		t.Fatal("adopted unacknowledged native ownership")
	}
}
func TestHeldLogIdentityAndRotation(t *testing.T) {
	s, _ := testSession(t)
	path := t.TempDir()
	os.Chmod(path, 0700)
	directory, e := captureDirectory(path)
	if e != nil {
		t.Fatal(e)
	}
	defer os.NewFile(uintptr(directory.fd), path).Close()
	log, e := directory.log("child-1.jsonl")
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	s.log = log
	if e = log.Truncate(262144); e != nil {
		t.Fatal(e)
	}
	if e = s.boundLog(); e != nil {
		t.Fatal(e)
	}
	info, _ := log.Stat()
	if info.Size() != 0 {
		t.Fatal("log was not rotated")
	}
	if e = os.Link(filepath.Join(path, "child-1.jsonl"), filepath.Join(path, "other")); e != nil {
		t.Fatal(e)
	}
	if e = s.boundLog(); e == nil {
		t.Fatal("hardlinked log accepted")
	}
}

func TestNativeWorkerHelper(t *testing.T) {
	if os.Getenv("GO_WEATHER_EFFECT_WORKER_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(RunWorker(os.Args[i+1:]))
		}
	}
	os.Exit(99)
}
func TestNativeWorkerDispatchWithoutDesktopIO(t *testing.T) {
	inputR, inputW, _ := os.Pipe()
	replyR, replyW, _ := os.Pipe()
	defer inputW.Close()
	defer replyR.Close()
	p, e := supervision.Start([]string{os.Args[0], "-test.run=^TestNativeWorkerHelper$", "--", "--root", t.TempDir(), "--instance", "abc_1_2", "--output", "DP-1"}, append(os.Environ(), "GO_WEATHER_EFFECT_WORKER_HELPER=1"), "", inputR, replyW, nil)
	inputR.Close()
	replyW.Close()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	defer p.Cleanup(ctx, time.Second)
	inputW.Write([]byte("{\"id\":1,\"op\":\"status\"}\n"))
	replyR.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(replyR)
	line, e := reader.ReadBytes('\n')
	if e != nil || !strings.Contains(string(line), `"state":"stopped"`) {
		t.Fatal("internal dispatch reply", string(line), e)
	}
	inputW.Write([]byte("{\"id\":2,\"op\":\"stop\"}\n"))
	if _, e = reader.ReadBytes('\n'); e != nil {
		t.Fatal(e)
	}
	inputW.Close()
	deadline := time.Now().Add(2 * time.Second)
	for p.Alive() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.Alive() {
		t.Fatal("worker did not exit")
	}
}

func TestWorkerDoesNotEraseEvidenceByRetryingFailedCleanup(t *testing.T) {
	s, _ := testSession(t)
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	path := s.directory.path
	calls := 0
	s.stopHost = func(context.Context) error {
		calls++
		if calls == 1 {
			return errors.New("child exited abnormally")
		}
		return nil
	}
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan int, 1)
	go func() { done <- serveWorker(s, inR, outW) }()
	inW.Write([]byte("{\"id\":1,\"op\":\"stop\"}\n"))
	outR.SetReadDeadline(time.Now().Add(time.Second))
	if _, e := bufio.NewReader(outR).ReadBytes('\n'); e != nil {
		t.Fatal(e)
	}
	if code := <-done; code != 1 || calls != 1 {
		t.Fatal("failed cleanup retried", code, calls)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal("cleanup evidence erased", e)
	}
}

func TestFiniteWorkerHelper(t *testing.T) {
	if os.Getenv("GO_WEATHER_FINITE_WORKER_HELPER") != "1" {
		return
	}
	s := newSession(os.Getenv("GO_WEATHER_TEST_ROOT"), "abc_1_2", "DP-1")
	s.b = newFake()
	s.spawn = func(int, int) error { return nil }
	s.alive = func() bool { return true }
	s.stopHost = func(context.Context) error { return nil }
	s.renewHost = func() error { return nil }
	fd, e := syscall.Dup(1)
	if e != nil || syscall.SetNonblock(fd, true) != nil {
		os.Exit(99)
	}
	os.Exit(serveWorker(s, os.Stdin, os.NewFile(uintptr(fd), "test-worker-replies")))
}

func TestAutonomousFiniteExpiryAcknowledgedAndRestartable(t *testing.T) {
	m := New(t.TempDir(), "", "abc_1_2", "DP-1")
	start := func(duration int) {
		t.Helper()
		if m.process != nil || m.current["state"] != "stopped" {
			t.Fatal("clean expiry did not release restart admission", m.current)
		}
		inR, inW, _ := os.Pipe()
		outR, outW, _ := os.Pipe()
		p, e := supervision.Start([]string{os.Args[0], "-test.run=^TestFiniteWorkerHelper$"}, append(os.Environ(), "GO_WEATHER_FINITE_WORKER_HELPER=1", "GO_WEATHER_TEST_ROOT="+m.root), "", inR, outW, nil)
		inR.Close()
		outW.Close()
		if e != nil {
			t.Fatal(e)
		}
		m.process, m.input, m.reply = p, inW, outR
		m.reader = bufio.NewReaderSize(outR, replyLimit)
		m.current["state"] = "starting"
		if e = m.rpc(context.Background(), "start", object{"duration": duration, "persistent": false, "controls": object{}}); e != nil {
			t.Fatal(e)
		}
	}
	defer m.Stop(context.Background())
	start(1)
	// This is deliberately longer than the finite lease with no parent RPC.
	time.Sleep(1600 * time.Millisecond)
	if !m.process.Alive() {
		t.Fatal("clean expiry exited before its stopped acknowledgement")
	}
	m.mu.Lock()
	if e := m.rpc(context.Background(), "status", nil); e != nil || m.current["state"] != "stopped" {
		m.mu.Unlock()
		t.Fatal("worker did not autonomously stop before parent tick", m.current, e)
	}
	if e := m.retire(context.Background()); e != nil {
		m.mu.Unlock()
		t.Fatal("retire after autonomous expiry", e)
	}
	m.mu.Unlock()
	if e := m.Tick(context.Background(), nil, object{}); e != nil {
		t.Fatal(e)
	}
	if m.Status()["state"] != "stopped" || m.process != nil {
		t.Fatal("expiry was mistaken for worker crash", m.Status())
	}
	start(30)
	if m.Status()["state"] != "running" {
		t.Fatal("restart failed", m.Status())
	}
	if e := m.Stop(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestStatusRPCDoesNotRenewStaleOwnerDeadline(t *testing.T) {
	s, f := testSession(t)
	s.state, s.loaded, s.generation = "running", true, 7
	s.deadline = time.Now().Add(time.Minute)
	f.status["enabled"] = true
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan int, 1)
	const ownerTimeout = 250 * time.Millisecond
	go func() { done <- serveWorkerObservedWithTimeout(s, inR, outW, nil, ownerTimeout) }()
	reader := bufio.NewReader(outR)
	time.Sleep(120 * time.Millisecond)
	if _, e := inW.Write([]byte("{\"id\":1,\"op\":\"status\"}\n")); e != nil {
		t.Fatal(e)
	}
	outR.SetReadDeadline(time.Now().Add(time.Second))
	if line, e := reader.ReadBytes('\n'); e != nil || !strings.Contains(string(line), `"state":"running"`) {
		t.Fatal("status RPC", string(line), e)
	}
	// Status is observational and must not extend the last start/tick deadline.
	// Ask again after the original timeout but before a renewed timeout would elapse.
	time.Sleep(150 * time.Millisecond)
	if _, e := inW.Write([]byte("{\"id\":2,\"op\":\"status\"}\n")); e != nil {
		t.Fatal(e)
	}
	if line, e := reader.ReadBytes('\n'); e != nil || !strings.Contains(string(line), `"state":"stopped"`) {
		t.Fatal("stale owner was not stopped autonomously", string(line), e)
	}
	inW.Close()
	if code := <-done; code != 0 || !f.unloaded {
		t.Fatal("stale owner cleanup", code, f.unloaded)
	}
}

func TestCanceledRPCInterruptsReadAndPreservesIndependentRecovery(t *testing.T) {
	m := New(t.TempDir(), "", "", "")
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	// This owner intentionally consumes input but sends no reply, and exits
	// cleanly on EOF. It must not receive a cancellation-triggered SIGKILL.
	p, e := supervision.Start([]string{"/bin/cat"}, []string{"PATH=/usr/bin:/bin"}, "", inR, nil, nil)
	inR.Close()
	if e != nil {
		t.Fatal(e)
	}
	defer outW.Close()
	m.process, m.input, m.reply = p, inW, outR
	m.reader = bufio.NewReaderSize(outR, replyLimit)
	m.current["state"] = "running"
	ack := object{"generation": float64(7), "instance": "abc_1_2"}
	m.ownership = ack
	calls := 0
	m.recover = func(ctx context.Context, got object) error {
		calls++
		if ctx.Err() != nil || !reflect.DeepEqual(got, ack) {
			t.Errorf("canceled recovery or lost generation: %v %v", ctx.Err(), got)
		}
		if p.Alive() {
			t.Error("recovery raced the old owner")
		}
		return errors.New("uncertain native stop")
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	if e = m.Tick(ctx, nil, object{}); e == nil {
		t.Fatal("canceled request succeeded")
	}
	if time.Since(start) > time.Second || calls != 1 || !p.Cmd.ProcessState.Success() {
		t.Fatal("cancellation did not interrupt promptly/cleanly", time.Since(start), calls, p.Cmd.ProcessState)
	}
	if m.current["state"] != "cleanup_failed" || !reflect.DeepEqual(m.ownership, ack) {
		t.Fatal("cleanup evidence or ownership discarded", m.current, m.ownership)
	}
	if m.Stop(context.Background()) == nil || calls != 1 {
		t.Fatal("uncertain cleanup retried")
	}
	if m.Start(context.Background(), 30, false, object{}) == nil || calls != 1 {
		t.Fatal("new activation admitted after uncertainty")
	}
}

type cancelNative struct {
	*fakeNative
	entered chan struct{}
	blocked bool
}

func (b *cancelNative) native(ctx context.Context, command string) (object, error) {
	if command == "status" && !b.blocked {
		b.blocked = true
		close(b.entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return b.fakeNative.native(ctx, command)
}

func TestOwnerEOFInterruptsInFlightNativeCommandButNotCleanup(t *testing.T) {
	s, f := testSession(t)
	if e := s.start(context.Background(), 30, false, object{}); e != nil {
		t.Fatal(e)
	}
	b := &cancelNative{fakeNative: f, entered: make(chan struct{})}
	s.b = b
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan int, 1)
	go func() { done <- serveWorker(s, inR, outW) }()
	inW.Write([]byte("{\"id\":1,\"op\":\"tick\",\"weather\":null,\"controls\":{}}\n"))
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("native command did not start")
	}
	inW.Close()
	select {
	case code := <-done:
		if code != 0 || !f.unloaded || s.state != "stopped" {
			t.Fatal("EOF canceled the cleanup obligation", code, s.status())
		}
	case <-time.After(time.Second):
		t.Fatal("EOF failed to interrupt native request")
	}
}

type cleanupWaitNative struct {
	*fakeNative
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *cleanupWaitNative) native(ctx context.Context, command string) (object, error) {
	if command == "status" {
		b.once.Do(func() {
			close(b.entered)
			select {
			case <-b.release:
			case <-ctx.Done():
			}
		})
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return b.fakeNative.native(ctx, command)
}

func TestOwnerEOFDuringAutonomousExpiryDoesNotCancelCleanup(t *testing.T) {
	s, f := testSession(t)
	s.state, s.loaded, s.generation = "running", true, 7
	s.deadline = time.Now().Add(-time.Second)
	f.status["enabled"] = true
	b := &cleanupWaitNative{fakeNative: f, entered: make(chan struct{}), release: make(chan struct{})}
	s.b = b
	inR, inW, _ := os.Pipe()
	outR, outW, _ := os.Pipe()
	defer inR.Close()
	defer outR.Close()
	defer outW.Close()
	done := make(chan int, 1)
	eofObserved := make(chan struct{})
	go func() { done <- serveWorkerObserved(s, inR, outW, func() { close(eofObserved) }) }()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("autonomous expiry cleanup did not start")
	}
	if e := inW.Close(); e != nil {
		t.Fatal(e)
	}
	select {
	case <-eofObserved:
	case <-time.After(time.Second):
		t.Fatal("worker reader did not observe EOF")
	}
	// EOF cancels the owner immediately; the independent bounded cleanup must
	// remain live until native stop/unload has completed.
	close(b.release)
	select {
	case code := <-done:
		if code != 0 || s.state != "stopped" || !f.unloaded {
			t.Fatal("autonomous cleanup lost on EOF", code, s.status(), f.unloaded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("autonomous cleanup did not complete")
	}
}
