package app

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
)

func dashboardHide(v M, id string) {
	for _, raw := range v["sections"].([]any) {
		if object(raw)["id"] == id {
			object(raw)["enabled"] = false
		}
	}
}
func dashboardRequest(a *App, v M) M {
	return request("set_dashboard", M{"dashboard": M{"revision": a.dashboard.revision, "preferences": v}})
}
func TestDashboardDefaultsValidationAndBoundedContract(t *testing.T) {
	p := defaultDashboard()
	if validateDashboard(p) != nil {
		t.Fatal("invalid defaults")
	}
	q := defaultDashboard()
	dashboardHide(q, "maps")
	if reflect.DeepEqual(q, p) {
		t.Fatal("default documents alias")
	}
	for _, mutate := range []func(M){
		func(v M) { v["schema_version"] = 2.0 },
		func(v M) { v["schema_version"] = true },
		func(v M) { v["extra"] = true },
		func(v M) { v["density"] = "tiny" },
		func(v M) { v["sections"] = []any{} },
		func(v M) { object(v["sections"].([]any)[0])["id"] = "alerts" },
		func(v M) { object(v["sections"].([]any)[0])["enabled"] = nil },
		func(v M) { object(v["sections"].([]any)[0])["extra"] = false },
		func(v M) { v["metrics"].([]any)[0] = v["metrics"].([]any)[1] },
		func(v M) {
			for _, raw := range v["metrics"].([]any) {
				object(raw)["enabled"] = false
			}
		},
		func(v M) { v["hourly"] = []any{} },
		func(v M) { v["hourly"] = []any{"temperature_c", "temperature_c"} },
		func(v M) { v["hourly"] = []any{"precipitation_probability", "rainfall_probability"} },
		func(v M) { v["hourly"] = []any{"temperature_c", "humidity", "uv_index", "wind_speed_m_s"} },
	} {
		v := defaultDashboard()
		mutate(v)
		if validateDashboard(v) == nil {
			t.Fatal("accepted malformed dashboard", v)
		}
	}
	for _, raw := range q["sections"].([]any) {
		object(raw)["enabled"] = false
	}
	if validateDashboard(q) != nil {
		t.Fatal("hiding every optional section rejected")
	}
	a := newTestApp(t, Options{Offline: true})
	doc, err := a.state.Read(dashboardFile, dashboardBytes)
	if err != nil || doc != nil {
		t.Fatal("opening wrote defaults", doc, err)
	}
	view := a.dashboardSnapshot()
	raw, err := ipc.Encode(view, dashboardBytes)
	if err != nil || len(raw) > 1024 {
		t.Fatal("dashboard snapshot exceeds local 1 KiB target", len(raw), err)
	}
	dashboardHide(object(view["preferences"]), "maps")
	if !a.dashboardVisible("maps") {
		t.Fatal("snapshot exposed mutable settings")
	}
}
func TestDashboardPersistenceStaleEditsAndRestore(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	p := defaultDashboard()
	dashboardHide(p, "maps")
	p["density"], p["hourly"] = "compact", []any{"uv_index", "wind_speed_m_s", "apparent_temperature_c"}
	rows := p["sections"].([]any)
	rows[0], rows[4] = rows[4], rows[0]
	metrics := p["metrics"].([]any)
	metrics[0], metrics[5] = metrics[5], metrics[0]
	object(metrics[2])["enabled"] = false
	query := dashboardRequest(a, p)
	reply, _ := a.Handle(context.Background(), query)
	if reply["ok"] != true || a.dashboard.revision != 2 || a.dashboardVisible("maps") {
		t.Fatal(reply)
	}
	saved, err := a.state.Read(dashboardFile, dashboardBytes)
	if err != nil || !reflect.DeepEqual(saved, p) {
		t.Fatal(saved, err)
	}
	reply, _ = a.Handle(context.Background(), query)
	if reply["error"] != "dashboard_changed" || !reflect.DeepEqual(a.dashboard.preferences, p) {
		t.Fatal("stale full edit overwritten", reply)
	}
	// Both the request and snapshot are caller owned, never live app settings.
	p["density"] = "spacious"
	if a.dashboard.preferences["density"] != "compact" {
		t.Fatal("request alias")
	}
	reopened, err := New(a.state, Options{Offline: true, Now: a.options.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if !reflect.DeepEqual(reopened.dashboard.preferences, saved) {
		t.Fatal("layout not restored")
	}
	for _, revision := range []any{nil, true, 1.5, math.NaN(), math.Inf(1), 0.0, 9007199254740992.0} {
		if a.setDashboard(M{"revision": revision, "preferences": defaultDashboard()}) != "invalid_request" {
			t.Fatal("invalid revision", revision)
		}
	}
	reply, _ = a.Handle(context.Background(), dashboardRequest(a, defaultDashboard()))
	if reply["ok"] != true || !reflect.DeepEqual(a.dashboard.preferences, defaultDashboard()) {
		t.Fatal("reset failed", reply)
	}
}
func TestDashboardDamagedStateAndWriteConfirmation(t *testing.T) {
	state := testState(t)
	if err := state.Write(dashboardFile, M{"schema_version": 99.0}, dashboardBytes); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Offline: true})
	if err != nil {
		t.Fatal("optional preferences prevented opening", err)
	}
	defer a.Close(context.Background())
	if a.dashboard.errorCode != "state_unavailable" || !reflect.DeepEqual(a.dashboard.preferences, defaultDashboard()) {
		t.Fatal(a.dashboardSnapshot())
	}
	faults := &savedFaultFiles{Directory: state, fail: dashboardFile}
	a.dashboard.files = faults
	next := defaultDashboard()
	next["density"] = "compact"
	if code := a.setDashboard(M{"revision": 1.0, "preferences": next}); code != "state_io_failed" || a.dashboard.revision != 1 || a.dashboard.preferences["density"] != "spacious" {
		t.Fatal("failed rename changed layout", code)
	}
	faults.after = true
	if code := a.setDashboard(M{"revision": 1.0, "preferences": next}); code != "save_unconfirmed" || a.dashboard.revision != 2 || a.dashboard.preferences["density"] != "compact" || a.dashboard.errorCode != "save_unconfirmed" {
		t.Fatal("visible rename falsely confirmed or lost", code)
	}
	faults.fail = ""
	if code := a.setDashboard(M{"revision": 2.0, "preferences": next}); code != "" || a.dashboard.errorCode != "" || a.dashboard.revision != 3 {
		t.Fatal("retry did not confirm", code)
	}
	writes := len(faults.writes)
	if code := a.setDashboard(M{"revision": 3.0, "preferences": next}); code != "" || len(faults.writes) != writes || a.dashboard.revision != 3 {
		t.Fatal("unchanged preferences wrote again", code)
	}
}
func TestDashboardHiddenDataDoesNotStartAndCancelsLateAQ(t *testing.T) {
	state := testState(t)
	p := defaultDashboard()
	dashboardHide(p, "maps")
	dashboardHide(p, "air_quality")
	if err := state.Write(dashboardFile, p, dashboardBytes); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 9, 18, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	started, canceled, release := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{})
	a, err := New(state, Options{Now: func() time.Time { return now }, Fetch: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture forecast") }, FetchAirQuality: func(ctx context.Context, location M, at time.Time) (M, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		<-release
		return airQualityFixture(location, at, at), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	for i := 0; i < 5; i++ {
		a.Tick(context.Background())
	}
	if calls.Load() != 0 || a.aq.active {
		t.Fatal("hidden AQ started")
	}
	a.openMap()
	if a.wmap.open || a.wmap.active {
		t.Fatal("hidden map opened")
	}
	if a.openRadar(nil) == nil || a.radar != nil {
		t.Fatal("hidden radar opened")
	}
	on := safeio.Clone(p)
	for _, raw := range on["sections"].([]any) {
		if object(raw)["id"] == "air_quality" {
			object(raw)["enabled"] = true
		}
	}
	a.Handle(context.Background(), dashboardRequest(a, on))
	a.Tick(context.Background())
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("enabled AQ did not start")
	}
	a.Handle(context.Background(), dashboardRequest(a, p))
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("hidden AQ did not cancel")
	}
	// The worker deliberately waits after cancellation; re-enabling must still
	// preserve the one-worker and hourly-attempt budgets.
	a.Handle(context.Background(), dashboardRequest(a, on))
	a.Tick(context.Background())
	if calls.Load() != 1 || !a.aq.active {
		t.Fatal("visibility bypassed outstanding worker or rate cap")
	}
	// Release the canceled provider and deliver its real late completion.
	unblock()
	select {
	case completion := <-a.aq.results:
		a.aq.results <- completion
	case <-time.After(time.Second):
		t.Fatal("canceled worker did not return")
	}
	a.pollAirQuality()
	if a.aq.record != nil {
		t.Fatal("obsolete AQ published")
	}
	if cache, err := state.Read("air-quality.json", 1024*1024); err != nil || cache != nil {
		t.Fatal("obsolete AQ saved", cache, err)
	}
}

func TestDashboardHidingMapsClosesDemandAndRestoreStaysIdle(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	a.mode = "custom"
	a.openMap()
	canceled := false
	a.wmap.cancel = func() { canceled = true }
	if err := a.openRadar(nil); err != nil {
		t.Fatal(err)
	}
	next := defaultDashboard()
	dashboardHide(next, "maps")
	r, _ := a.Handle(context.Background(), dashboardRequest(a, next))
	_, radar := a.radarSince(nil)
	if r["ok"] != true || a.wmap.open || !canceled || radar.Status != "closed" {
		t.Fatal("hidden map demand continued", r["error"], radar.Status)
	}
	r, _ = a.Handle(context.Background(), dashboardRequest(a, defaultDashboard()))
	_, radar = a.radarSince(nil)
	if r["ok"] != true || a.wmap.open || radar.Status != "closed" {
		t.Fatal("enabling maps opened them without viewing demand")
	}
}
