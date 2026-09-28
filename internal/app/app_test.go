package app

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func request(op string, fields M) M {
	r := M{"version": 1.0, "request_id": 7.0, "op": op}
	for k, v := range fields {
		r[k] = v
	}
	return r
}
func newTestApp(t *testing.T, o Options) *App {
	t.Helper()
	if o.Now == nil {
		o.Now = func() time.Time { return time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC) }
	}
	a, e := New(testState(t), o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
	return a
}
func awaitCompletion(t *testing.T, a *App) {
	t.Helper()
	select {
	case c := <-a.results:
		a.results <- c
		a.Snapshot()
	case <-time.After(3 * time.Second):
		t.Fatal("weather worker did not finish")
	}
}

func TestRunningEffectsUseOneSecondInputPoll(t *testing.T) {
	a := newTestApp(t, Options{Offline: true, Effects: &fakeEffects{}, Now: time.Now})
	reply, _ := a.Handle(context.Background(), request("start_effects", M{"duration": float64(30)}))
	if reply["ok"] != true {
		t.Fatal("start preview", reply)
	}
	if interval := a.Interval(); interval != time.Second {
		t.Fatalf("running effects input poll interval = %s, want 1s", interval)
	}
}

func TestAppControlPersistenceAndValidation(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	before := safeio.Clone(a.controls)
	reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"mode": "manual", "manual": M{"condition": "snow"}, "strength": "immersive", "units": "C", "reduced_motion": true}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	saved, e := a.state.Read("controls.json", 8192)
	if e != nil || !reflect.DeepEqual(saved, a.controls) {
		t.Fatal("controls not persisted", e)
	}
	if before["mode"] != "live" {
		t.Fatal("patch changed previous map")
	}
	for _, patch := range []M{{"fps": true}, {"fps": 31.0}, {"mode": "bad"}, {"units": "K"}, {"strength": "intense"}, {"manual": M{"condition": "rain", "extra": true}}, {"manual": M{"condition": "not_weather"}}, {"unknown": true}, {"window_physics": 1.0}} {
		reply, _ = a.Handle(context.Background(), request("set_controls", M{"controls": patch}))
		if reply["ok"] != false {
			t.Fatal("accepted invalid controls", patch)
		}
		if !reflect.DeepEqual(saved, a.controls) {
			t.Fatal("failed control patch changed state")
		}
	}
	reopened, e := New(a.state, Options{Offline: true, Now: a.options.Now})
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close(context.Background())
	if !reflect.DeepEqual(reopened.controls, saved) {
		t.Fatal("saved controls not restored")
	}
}

func TestAppRejectsMalformedRequests(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	for _, r := range []M{{}, {"version": 1.0, "request_id": true, "op": "snapshot"}, {"version": 1.0, "request_id": -1.0, "op": "snapshot"}, {"version": 1.0, "request_id": 2147483648.0, "op": "snapshot"}, {"version": 1.0, "request_id": 1.5, "op": "snapshot"}, {"version": 2.0, "request_id": 1.0, "op": "snapshot"}, request("unknown", nil), request("snapshot", M{"extra": true}), request("set_controls", nil), request("start_effects", M{"duration": true})} {
		reply, quit := a.Handle(context.Background(), r)
		if reply["ok"] != false || quit {
			t.Fatal("invalid request accepted", r, reply)
		}
	}
	reply, quit := a.Handle(context.Background(), request("quit", nil))
	if !quit || reply["ok"] != true {
		t.Fatal("quit not acknowledged")
	}
	if e := a.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	reply, _ = a.Handle(context.Background(), request("snapshot", nil))
	if reply["error"] != "service_closed" {
		t.Fatal(reply)
	}
}

func TestAppRefreshPersistenceAndCancellation(t *testing.T) {
	started := make(chan context.Context, 2)
	release := make(chan struct{})
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Fetch: func(ctx context.Context, l M, n time.Time) (M, error) {
		started <- ctx
		select {
		case <-release:
			f := appFixture(n)
			f["location"] = l
			return f, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	a.Handle(context.Background(), request("refresh", nil))
	ctx := <-started
	if !a.fetchBusy {
		t.Fatal("refresh not busy")
	}
	a.Handle(context.Background(), request("stop_effects", nil))
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel forecast refresh")
	}
	awaitCompletion(t, a)
	if a.forecast != nil || a.fetchBusy {
		t.Fatal("canceled refresh changed cache")
	}
	a.Handle(context.Background(), request("refresh", nil))
	<-started
	close(release)
	awaitCompletion(t, a)
	if a.forecast == nil {
		t.Fatal("refresh result not adopted")
	}
	saved, e := a.state.Read("forecast.json", weather.MaxBytes)
	if e != nil || !reflect.DeepEqual(saved, a.forecast) {
		t.Fatal("refresh not persisted", e)
	}
}

func TestAppStaleCompletionAndAtomicLocation(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	resolveStarted := make(chan M, 2)
	gate := make(chan struct{})
	boston := M{"name": "Boston, MA", "latitude": 42.36, "longitude": -71.05, "timezone": "America/New_York"}
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Resolve: func(ctx context.Context, s M) (M, error) {
		resolveStarted <- s
		select {
		case <-gate:
			return safeio.Clone(boston), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, Fetch: func(ctx context.Context, l M, n time.Time) (M, error) {
		f := appFixture(n)
		f["location"] = l
		return f, nil
	}})
	oldLocation := safeio.Clone(a.location)
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	<-resolveStarted
	if !a.locationBusy || !reflect.DeepEqual(a.location, oldLocation) {
		t.Fatal("unfetched location published")
	}
	oldGeneration := a.generation
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "auto"}}))
	selection := <-resolveStarted
	if len(selection) != 1 || selection["mode"] != "auto" {
		t.Fatal("resolver did not receive exact auto selection", selection)
	}
	stale := appFixture(now)
	a.results <- completion{generation: oldGeneration, forecast: stale, location: oldLocation}
	a.Snapshot()
	if !a.locationBusy || a.forecast != nil {
		t.Fatal("stale completion adopted or cleared current worker")
	}
	close(gate)
	for a.locationBusy {
		awaitCompletion(t, a)
	}
	if a.mode != "auto" || a.zip != nil || !reflect.DeepEqual(a.location, boston) || a.forecast == nil {
		t.Fatal("location and weather not adopted together")
	}
	profile, e := a.state.Read("location-profile.json", weather.MaxBytes)
	if e != nil || ValidateProfile(profile) != nil || !reflect.DeepEqual(profile, a.profile) {
		t.Fatal("profile document not atomically persisted", e)
	}
	if legacy, _ := a.state.Read("location.json", 8192); legacy != nil {
		t.Fatal("split location file written")
	}
}

func TestAppLocationFailureLeavesProfile(t *testing.T) {
	a := newTestApp(t, Options{Resolve: func(context.Context, M) (M, error) {
		return nil, &weather.LocationError{Code: "zip_not_found", Message: "No ZIP"}
	}})
	before := safeio.Clone(a.location)
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	awaitCompletion(t, a)
	if !reflect.DeepEqual(before, a.location) || a.profile != nil {
		t.Fatal("failed lookup changed location")
	}
	if a.locationError != "zip_not_found" {
		t.Fatal("specific lookup failure lost", a.locationError)
	}
}

func TestAppSaveFailureKeepsPreviousState(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	boston := M{"name": "Boston, MA", "latitude": 42.36, "longitude": -71.05, "timezone": "America/New_York"}
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Resolve: func(context.Context, M) (M, error) { return boston, nil }, Fetch: func(_ context.Context, l M, n time.Time) (M, error) {
		f := appFixture(n)
		f["location"] = l
		return f, nil
	}})
	before := safeio.Clone(a.location)
	// An existing directory at the atomic document target refuses replacement.
	if e := os.Mkdir(filepath.Join(a.state.Path, "location-profile.json"), 0700); e != nil {
		t.Fatal(e)
	}
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	awaitCompletion(t, a)
	if a.locationError != "state_io_failed" || a.profile != nil || a.forecast != nil || !reflect.DeepEqual(a.location, before) {
		t.Fatal("failed profile save published unsaved location", a.Snapshot())
	}
	if e := os.Mkdir(filepath.Join(a.state.Path, "controls.json"), 0700); e != nil {
		t.Fatal(e)
	}
	controls := safeio.Clone(a.controls)
	reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"units": "C"}}))
	if reply["error"] != "state_io_failed" || !reflect.DeepEqual(a.controls, controls) {
		t.Fatal("failed controls save changed active controls", reply)
	}
}

func TestAppRestoresProfileAndOnlySavedAutoResolves(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d := testState(t)
	f := appFixture(now)
	profile := M{"schema_version": 1.0, "mode": "auto", "zip_code": nil, "location": weather.DefaultLocation(), "forecast": f}
	if e := d.Write("location-profile.json", profile, weather.MaxBytes); e != nil {
		t.Fatal(e)
	}
	called := make(chan M, 1)
	o := Options{Now: func() time.Time { return now }, Resolve: func(_ context.Context, selection M) (M, error) {
		called <- selection
		return weather.DefaultLocation(), nil
	}, Fetch: func(_ context.Context, l M, n time.Time) (M, error) {
		f := appFixture(n)
		f["location"] = l
		return f, nil
	}}
	a, e := New(d, o)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close(context.Background())
	select {
	case s := <-called:
		if len(s) != 1 || s["mode"] != "auto" {
			t.Fatal("saved permission resolver selection", s)
		}
	case <-time.After(time.Second):
		t.Fatal("saved auto selection not refreshed")
	}
	if a.mode != "auto" || !reflect.DeepEqual(a.forecast, f) {
		t.Fatal("saved matching forecast hidden before auto success")
	}
	awaitCompletion(t, a)
	o.Offline = true
	reopened, e := New(d, o)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close(context.Background())
	if reopened.locationBusy || reopened.mode != "auto" {
		t.Fatal("offline profile initiated location lookup")
	}
	select {
	case <-called:
		t.Fatal("offline profile contacted location resolver")
	default:
	}
	fresh, e := New(testState(t), o)
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close(context.Background())
	if fresh.mode != "default" || fresh.locationBusy {
		t.Fatal("fresh install initiated auto location")
	}
}

type fakeEffects struct {
	state                        string
	persistent                   bool
	starts, stops, ticks, checks int
	stopErr                      error
}

func (f *fakeEffects) SetupSnapshot() M {
	return M{"status": "unchecked", "reason": "not_checked", "outputs": []any{}, "selected_output": nil}
}
func (f *fakeEffects) Check(context.Context) M                         { f.checks++; return f.SetupSnapshot() }
func (f *fakeEffects) SelectOutput(context.Context, string) (M, error) { return f.SetupSnapshot(), nil }
func (f *fakeEffects) Status() M {
	s := f.state
	if s == "" {
		s = "stopped"
	}
	return M{"state": s, "persistent": f.persistent}
}
func (f *fakeEffects) Start(_ context.Context, d int, p bool, c M) error {
	f.starts++
	f.state = "running"
	f.persistent = p
	return nil
}
func (f *fakeEffects) Tick(context.Context, M, M) error { f.ticks++; return nil }
func (f *fakeEffects) Stop(context.Context) error {
	f.stops++
	if f.stopErr != nil {
		f.state = "cleanup_failed"
		return f.stopErr
	}
	f.state = "stopped"
	f.persistent = false
	return nil
}
func TestAppExplicitEffectsAndPersistentIdempotence(t *testing.T) {
	fx := &fakeEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	a.Snapshot()
	if fx.starts != 0 || fx.ticks != 0 {
		t.Fatal("snapshot activated effects")
	}
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true || fx.starts != 1 || fx.checks != 1 {
		t.Fatal(reply)
	}
	a.Handle(context.Background(), request("start_live_effects", nil))
	if fx.starts != 1 {
		t.Fatal("duplicate persistent session")
	}
	reply, _ = a.Handle(context.Background(), request("check_effects", nil))
	if reply["error"] != "effects_active" {
		t.Fatal("active session compatibility check allowed")
	}
	a.Handle(context.Background(), request("stop_effects", nil))
	if fx.state != "stopped" {
		t.Fatal("effects did not stop")
	}
}
func TestSelectingEffectsOutputRechecksCompatibility(t *testing.T) {
	fx := &fakeEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	reply, _ := a.Handle(context.Background(), request("select_output", M{"output": "DP-1"}))
	if reply["ok"] != true || fx.checks != 1 {
		t.Fatal("selection did not refresh compatibility", reply, fx.checks)
	}
}
func TestAppCloseFailureStaysFailure(t *testing.T) {
	fx := &fakeEffects{state: "running", stopErr: errors.New("cleanup uncertain")}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	if e := a.Close(context.Background()); e == nil {
		t.Fatal("cleanup failure not returned")
	}
	if e := a.Close(context.Background()); e == nil {
		t.Fatal("repeat close forgot cleanup failure")
	}
	if fx.stops != 1 {
		t.Fatal("repeat close retried cleanup")
	}
}

type socketFixture struct {
	path   string
	a      *App
	done   chan error
	cancel context.CancelFunc
}

func serveFixture(t *testing.T) *socketFixture {
	t.Helper()
	runtime, e := os.MkdirTemp("/tmp", "weather-ipc-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(runtime) })
	a := newTestApp(t, Options{Offline: true})
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	f := &socketFixture{filepath.Join(runtime, "app.sock"), a, make(chan error, 1), cancel}
	go func() { f.done <- Serve(ctx, f.path, a, func() { close(ready) }) }()
	select {
	case <-ready:
	case e := <-f.done:
		cancel()
		t.Fatal(e)
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("socket not ready")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-f.done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not exit")
		}
	})
	info, e := os.Stat(f.path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("socket permissions", e)
	}
	return f
}
func connect(t *testing.T, path string) net.Conn {
	t.Helper()
	c, e := net.Dial("unix", path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(3 * time.Second))
	return c
}
func readReply(t *testing.T, r *bufio.Reader) M {
	t.Helper()
	for {
		line, e := r.ReadBytes('\n')
		if e != nil {
			t.Fatal(e)
		}
		v, e := safeio.Object(line, ipc.ResponseLimit)
		if e != nil {
			t.Fatal(e)
		}
		if v["event"] == nil {
			return v
		}
	}
}
func TestSocketProtocolMalformedAndQuit(t *testing.T) {
	f := serveFixture(t)
	c := connect(t, f.path)
	reader := bufio.NewReader(c)
	for _, raw := range []string{`{`, `[]`, `{"version":1,"request_id":1,"op":"snapshot","op":"quit"}`, `{"version":1,"request_id":1e999,"op":"snapshot"}`, strings.Repeat(" ", ipc.RequestLimit-2) + "{}" + " "} {
		if _, e := c.Write([]byte(raw + "\n")); e != nil {
			t.Fatal(e)
		}
		reply := readReply(t, reader)
		if reply["ok"] != false || reply["error"] != "invalid_request" {
			t.Fatal("bad wire request accepted", reply)
		}
	}
	if e := ipc.Send(c, request("snapshot", nil), ipc.RequestLimit); e != nil {
		t.Fatal(e)
	}
	reply := readReply(t, reader)
	if reply["request_id"] != 7.0 || reply["ok"] != true || object(reply["snapshot"])["schema_version"] != 1.0 {
		t.Fatal(reply)
	}
	if e := ipc.Send(c, request("quit", nil), ipc.RequestLimit); e != nil {
		t.Fatal(e)
	}
	reply = readReply(t, reader)
	if reply["ok"] != true {
		t.Fatal(reply)
	}
}
func TestSocketSubscriptionAndToggle(t *testing.T) {
	f := serveFixture(t)
	subscriber := connect(t, f.path)
	reader := bufio.NewReader(subscriber)
	ipc.Send(subscriber, request("subscribe", nil), ipc.RequestLimit)
	reply := readReply(t, reader)
	if reply["ok"] != true || object(reply["snapshot"])["current"] != nil {
		t.Fatal("missing initial subscription snapshot")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	reply, e := ipc.Call(ctx, f.path, M{"op": "toggle_window"})
	if e != nil || reply["ok"] != true {
		t.Fatal(reply, e)
	}
	for {
		raw, e := reader.ReadBytes('\n')
		if e != nil {
			t.Fatal(e)
		}
		event, e := safeio.Object(raw, ipc.ResponseLimit)
		if e != nil {
			t.Fatal(e)
		}
		if event["event"] == "toggle_window" {
			break
		}
	}
}
func TestSocketPeerLimitAndOversize(t *testing.T) {
	f := serveFixture(t)
	clients := []net.Conn{}
	for i := 0; i < 16; i++ {
		c := connect(t, f.path)
		ipc.Send(c, request("snapshot", nil), ipc.RequestLimit)
		if readReply(t, bufio.NewReader(c))["ok"] != true {
			t.Fatal("peer admission")
		}
		clients = append(clients, c)
	}
	extra := connect(t, f.path)
	extra.Write([]byte("{}\n"))
	if _, e := bufio.NewReader(extra).ReadByte(); e == nil {
		t.Fatal("17th peer was not refused")
	}
	for _, c := range clients {
		c.Close()
	}
	deadline := time.Now().Add(2 * time.Second)
	var oversized net.Conn
	for time.Now().Before(deadline) {
		c, e := net.Dial("unix", f.path)
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(time.Second))
		if e = ipc.Send(c, request("snapshot", nil), ipc.RequestLimit); e == nil {
			scan := ipc.Scanner(c, ipc.ResponseLimit)
			if scan.Scan() {
				oversized = c
				break
			}
		}
		c.Close()
	}
	if oversized == nil {
		t.Fatal("peer slots not released")
	}
	defer oversized.Close()
	oversized.Write([]byte(strings.Repeat("x", ipc.RequestLimit+20) + "\n"))
	if _, e := bufio.NewReader(oversized).ReadByte(); e == nil {
		t.Fatal("oversize connection remained open")
	}
}
