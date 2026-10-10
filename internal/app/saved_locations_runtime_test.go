package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

var savedRuntimeNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func runtimeLocations(t *testing.T, options Options, viewed int) (*App, *safeio.Directory) {
	t.Helper()
	state := testState(t)
	first := savedFixture(0)
	forecast := object(first["forecast"])
	forecast["hourly"] = []any{M{"time": savedRuntimeNow.Add(time.Hour).Format(time.RFC3339), "condition": "rain", "is_day": true, "precipitation_probability": 1.0}}
	store, err := createSavedLocations(state, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 3; i++ {
		p := savedFixture(i)
		p["country_code"] = "DE"
		object(object(p["forecast"])["current"])["temperature_c"] = float64(20 + i)
		if err := store.put(p, viewed == i, false); err != nil {
			t.Fatal(err)
		}
	}
	if options.Now == nil {
		options.Now = func() time.Time { return savedRuntimeNow }
	}
	a, err := New(state, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
	// Tests of browsing start within a session. A new application now starts
	// at Home; set up the selected point without starting network work.
	if viewed > 0 {
		id := stringOf(object(a.saved.doc["places"].([]any)[viewed])["id"])
		if err := a.saved.view(id); err != nil {
			t.Fatal(err)
		}
		oldID, oldLocation := a.id, a.location
		a.forecastPoint = a.loadPoint(id)
		a.viewPointChanged(oldID, oldLocation)
	}
	return a, state
}

func locationAction(t *testing.T, a *App, action M) M {
	t.Helper()
	reply, _ := a.Handle(context.Background(), request("saved_location", M{"location": action}))
	if reply["ok"] != true {
		t.Fatal("saved location action", action, reply)
	}
	return reply
}

func settleForecasts(t *testing.T, a *App) {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for {
		a.Snapshot()
		if len(a.forecastJobs) == 0 && a.pending == nil && a.primary.pending == nil {
			return
		}
		if time.Now().After(until) {
			t.Fatal("forecast scheduler did not settle")
		}
		select {
		case <-a.Changed:
		case <-time.After(time.Millisecond):
		}
	}
}

func TestSavedRuntimePrimaryConsumersAndRestart(t *testing.T) {
	messages := make(chan string, 2)
	a, state := runtimeLocations(t, Options{Offline: true, Effects: &coordinatedEffects{}, Sender: func(_ context.Context, _ string, body string) error { messages <- body; return nil }}, 1)
	if a.id != "place-101" || a.primary.id != "place-100" || a.controls["units"] != "C" {
		t.Fatal("browsing changed Home or used the wrong units")
	}
	bar := Bar(state, savedRuntimeNow)
	if !strings.HasPrefix(stringOf(bar["label"]), "59°") || !strings.Contains(stringOf(bar["tooltip"]), "City 0") {
		t.Fatal("bar used viewed city or units", bar)
	}
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.fx.mu.RLock()
	temperature, units := object(a.fx.weather["current"])["temperature_c"], a.fx.controls["units"]
	a.fx.mu.RUnlock()
	if temperature != 21.0 || units != "C" {
		t.Fatal("effects did not use viewed weather", temperature, units)
	}
	reply, _ = a.Handle(context.Background(), request("set_notifications", M{"notifications": M{"enabled": true, "quiet_enabled": false}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	select {
	case body := <-messages:
		if !strings.Contains(body, "City 0") {
			t.Fatal("notification used viewed city", body)
		}
	case <-time.After(time.Second):
		t.Fatal("primary precipitation notification missing")
	}
	locationAction(t, a, M{"action": "view", "id": "place-102"})
	if a.primary.id != "place-100" || a.id != "place-102" {
		t.Fatal("browsing redirected primary")
	}
	locationAction(t, a, M{"action": "primary", "id": "place-101"})
	if a.id != "place-102" || a.primary.id != "place-101" {
		t.Fatal("primary selection redirected view")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := New(state, Options{Offline: true, Now: func() time.Time { return savedRuntimeNow }})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	if restored.id != "place-101" || restored.primary.id != "place-101" || restored.saved.doc["viewed"] != "place-101" {
		t.Fatal("restart did not open the chosen Home")
	}
	if restored.forecastPoint != restored.primary {
		t.Fatal("startup decoded Home more than once")
	}
	store, err := loadSavedLocations(state)
	if err != nil || store.doc["primary"] != "place-101" || store.doc["viewed"] != "place-101" || len(store.doc["places"].([]any)) != 3 {
		t.Fatal("restart lost locations or did not save Home selection", err)
	}
}

func TestSavedRuntimeOnlyDemandedPointsFetch(t *testing.T) {
	calls := make(chan string, 10)
	a, _ := runtimeLocations(t, Options{Fetch: func(_ context.Context, location M, now time.Time) (M, error) {
		calls <- stringOf(location["name"])
		f := appFixture(now)
		f["location"] = location
		return f, nil
	}}, 1)
	a.nextFetch, a.primary.nextFetch = savedRuntimeNow, savedRuntimeNow
	a.Tick(context.Background())
	settleForecasts(t, a)
	if len(calls) != 1 || <-calls != "City 1" {
		t.Fatal("idle favorites or unused primary fetched")
	}
	a.setPresented(false)
	a.nextFetch, a.primary.nextFetch = savedRuntimeNow, savedRuntimeNow
	for range 5 {
		a.Tick(context.Background())
	}
	settleForecasts(t, a)
	if len(calls) != 0 {
		t.Fatal("hidden idle frontend fetched")
	}
	r, _ := a.Handle(context.Background(), request("refresh_primary", nil))
	if r["ok"] != true {
		t.Fatal(r)
	}
	settleForecasts(t, a)
	if len(calls) != 1 || <-calls != "City 0" {
		t.Fatal("bar demand did not target primary")
	}
	for range 5 {
		a.Handle(context.Background(), request("refresh_primary", nil))
	}
	settleForecasts(t, a)
	if len(calls) != 0 {
		t.Fatal("bar request bypassed refresh interval")
	}
	a.barRefreshUntil = time.Time{}
	if err := a.notifications.Configure(M{"enabled": true, "quiet_enabled": false}); err != nil {
		t.Fatal(err)
	}
	a.primary.nextFetch = savedRuntimeNow
	a.Tick(context.Background())
	settleForecasts(t, a)
	if len(calls) != 1 || <-calls != "City 0" {
		t.Fatal("hidden watcher did not refresh primary only")
	}
	a.setPresented(true)
	settleForecasts(t, a)
	if len(calls) != 1 || <-calls != "City 1" {
		t.Fatal("restoring presentation did not refresh viewed city")
	}
}

func TestSavedRuntimeRapidSwitchBoundedAndNoResurrection(t *testing.T) {
	type blocked struct {
		name    string
		release chan struct{}
	}
	started := make(chan blocked, 8)
	var requests []blocked
	defer func() {
		for _, r := range requests {
			select {
			case <-r.release:
			default:
				close(r.release)
			}
		}
	}()
	a, state := runtimeLocations(t, Options{Fetch: func(_ context.Context, location M, now time.Time) (M, error) {
		name := stringOf(location["name"])
		r := blocked{name: name, release: make(chan struct{})}
		started <- r
		<-r.release
		f := appFixture(now)
		f["location"] = location
		return f, nil
	}}, 0)
	a.beginFetch(nil)
	first := <-started
	requests = append(requests, first)
	if first.name != "City 0" {
		t.Fatal("first request")
	}
	locationAction(t, a, M{"action": "view", "id": "place-101"})
	a.beginFetch(nil)
	second := <-started
	requests = append(requests, second)
	if second.name != "City 1" {
		t.Fatal("second request")
	}
	// A -> B -> A while both canceled transports still run. Even the same
	// stable identity with a new point owner cannot overlap its earlier call.
	for range 20 {
		locationAction(t, a, M{"action": "view", "id": "place-102"})
		a.beginFetch(nil)
		locationAction(t, a, M{"action": "view", "id": "place-100"})
		a.beginFetch(nil)
	}
	if len(a.forecastJobs) != 2 || len(started) != 0 {
		t.Fatal("rapid switching exceeded two workers")
	}
	locationAction(t, a, M{"action": "remove", "id": "place-101", "replacement": ""})
	before := a.saved.document()
	close(second.release)
	until := time.Now().Add(time.Second)
	for len(a.forecastJobs) == 2 && time.Now().Before(until) {
		a.Snapshot()
		time.Sleep(time.Millisecond)
	}
	if len(started) != 0 {
		t.Fatal("new owner overlapped old request for same city")
	}
	if savedEntry(a.saved.doc, "place-101") != nil || !reflect.DeepEqual(before, a.saved.document()) {
		t.Fatal("removed city completion changed manifest")
	}
	close(first.release)
	until = time.Now().Add(time.Second)
	for len(started) == 0 && time.Now().Before(until) {
		a.Snapshot()
		time.Sleep(time.Millisecond)
	}
	if len(started) != 1 {
		t.Fatal("latest queued identity did not run")
	}
	latest := <-started
	requests = append(requests, latest)
	if latest.name != "City 0" {
		t.Fatal("obsolete queued request ran", latest.name)
	}
	close(latest.release)
	settleForecasts(t, a)
	store, err := loadSavedLocations(state)
	if err != nil || store.doc["viewed"] != "place-100" || savedEntry(store.doc, "place-101") != nil {
		t.Fatal("obsolete publication survived restart", err)
	}
}

func TestSavedRuntimeAddPreservesPrimaryAndLegacySelection(t *testing.T) {
	a, _ := runtimeLocations(t, Options{ResolveSelection: func(_ context.Context, s M) (M, error) {
		l := weather.DefaultLocation()
		l["name"] = s["zip_code"]
		return M{"location": l, "country_code": "US"}, nil
	}, Fetch: func(_ context.Context, l M, now time.Time) (M, error) {
		f := appFixture(now)
		f["location"] = l
		return f, nil
	}}, 0)
	a.Handle(context.Background(), request("add_location", M{"location": M{"mode": "zip", "zip_code": "10001"}}))
	settleForecasts(t, a)
	if a.id != "zip-10001" || a.primary.id != "place-100" {
		t.Fatal("add redirected primary")
	}
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "10002"}}))
	settleForecasts(t, a)
	if a.id != "zip-10002" || a.forecastPoint != a.primary {
		t.Fatal("legacy selection no longer changes primary")
	}
}

func TestSavedRuntimeCacheDamageAndMetadataAuthority(t *testing.T) {
	a, state := runtimeLocations(t, Options{Offline: true}, 1)
	entry := savedEntry(a.saved.doc, "place-101")
	if err := os.Remove(filepath.Join(state.Path, savedSlotName(int(entry["cache_slot"].(float64))))); err != nil {
		t.Fatal(err)
	}
	// A conflicting old-format file must never override the committed manifest.
	if err := state.Write("location-profile.json", savedFixture(9), weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	b, err := New(state, Options{Offline: true, Now: func() time.Time { return savedRuntimeNow }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if b.forecastPoint != b.primary || b.id != "place-100" {
		t.Fatal("damaged browsing cache prevented startup at Home")
	}
	locationAction(t, b, M{"action": "view", "id": "place-101"})
	if b.id != "place-101" || b.forecast != nil || b.primary.id != "place-100" || b.primary.forecast == nil {
		t.Fatal("cache damage changed identity or hid primary")
	}
	if strings.Contains(stringOf(Bar(state, savedRuntimeNow)["tooltip"]), "City 9") {
		t.Fatal("bar rolled back manifest")
	}
	bad := a.saved.document()
	bad["primary"] = "missing"
	if err := state.Write(savedLocationsFile, bad, savedMetadataBytes); err != nil {
		t.Fatal(err)
	}
	if invalid, err := New(state, Options{Offline: true}); err == nil {
		invalid.Close(context.Background())
		t.Fatal("invalid metadata fell back to legacy")
	}
	if Bar(state, savedRuntimeNow)["freshness"] != "unavailable" {
		t.Fatal("bar displayed conflicting legacy weather")
	}
}

func TestSavedRuntimePublicationFailureAndUncertainty(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			a, _ := runtimeLocations(t, Options{Offline: true}, 0)
			a.saved.files = &savedFaultFiles{Directory: a.state, fail: savedLocationsFile, after: after}
			r, _ := a.Handle(context.Background(), request("saved_location", M{"location": M{"action": "view", "id": "place-101"}}))
			if r["ok"] != false {
				t.Fatal("write failure reported success")
			}
			want := "place-100"
			if after {
				want = "place-101"
			}
			if a.id != want || a.primary.id != "place-100" {
				t.Fatal("in-memory publication disagrees with rename", a.id)
			}
			if after && r["error"] != "save_unconfirmed" {
				t.Fatal("durability uncertainty lost")
			}
		})
	}
}

func TestSavedRuntimeActionsValidateBeforeMutation(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 1)
	before := a.saved.document()
	for _, action := range []M{{"action": "view", "id": "missing"}, {"action": "view", "id": "place-100", "extra": true}, {"action": "remove", "id": "place-100", "replacement": ""}, {"action": "remove", "id": "place-101", "replacement": "place-100"}, {"action": "rename", "id": "place-100", "label": "bad\nname"}, {"action": "move", "id": "place-100", "index": 0.5}} {
		r, _ := a.Handle(context.Background(), request("saved_location", M{"location": action}))
		if r["ok"] != false || r["error"] != "invalid_request" || !reflect.DeepEqual(before, a.saved.document()) {
			t.Fatal("invalid action mutated registry", action, r)
		}
	}
	locationAction(t, a, M{"action": "rename", "id": "place-101", "label": "Work"})
	locationAction(t, a, M{"action": "move", "id": "place-101", "index": 0.0})
	if object(a.saved.doc["places"].([]any)[0])["label"] != "Work" || a.id != "place-101" || a.primary.id != "place-100" {
		t.Fatal("list changes altered selection")
	}
	locationAction(t, a, M{"action": "remove", "id": "place-100", "replacement": "place-102"})
	if a.id != "place-101" || a.primary.id != "place-102" {
		t.Fatal("primary removal changed view")
	}
	locationAction(t, a, M{"action": "remove", "id": "place-101", "replacement": ""})
	if a.forecastPoint != a.primary {
		t.Fatal("removing view did not return to primary")
	}
}

func TestSavedRuntimeHeadlessRefreshKeepsViewedCity(t *testing.T) {
	a, state := runtimeLocations(t, Options{Offline: true}, 1)
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	err := RefreshBarSaved(context.Background(), state, Options{Now: func() time.Time { return savedRuntimeNow.Add(time.Hour) }, Fetch: func(_ context.Context, l M, now time.Time) (M, error) {
		calls.Add(1)
		if l["name"] != "City 0" {
			return nil, errors.New("wrong city")
		}
		f := appFixture(now)
		f["location"] = l
		return f, nil
	}})
	if err != nil || calls.Load() != 1 {
		t.Fatal("headless primary refresh", err, calls.Load())
	}
	store, err := loadSavedLocations(state)
	if err != nil || store.doc["viewed"] != "place-101" || store.doc["primary"] != "place-100" {
		t.Fatal("bar changed viewed city", err)
	}
	if !strings.Contains(stringOf(Bar(state, savedRuntimeNow.Add(time.Hour))["tooltip"]), "City 0") {
		t.Fatal("bar did not read updated primary")
	}
}

func TestSavedRuntimeCurrentLocationResolvesOnlyWithDemand(t *testing.T) {
	state := testState(t)
	primary := savedFixture(0)
	primary["mode"], primary["place"] = "auto", nil
	store, err := createSavedLocations(state, primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.put(savedFixture(1), true, false); err != nil {
		t.Fatal(err)
	}
	var resolves atomic.Int32
	a, err := New(state, Options{Offline: true, Now: func() time.Time { return savedRuntimeNow }, ResolveSelection: func(_ context.Context, s M) (M, error) {
		resolves.Add(1)
		l := weather.DefaultLocation()
		l["name"] = "Updated current location"
		return M{"location": l, "country_code": "US"}, nil
	}, Fetch: func(_ context.Context, l M, now time.Time) (M, error) {
		f := appFixture(now)
		f["location"] = l
		return f, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	locationAction(t, a, M{"action": "view", "id": "place-101"})
	a.options.Offline = false
	if resolves.Load() != 0 || !a.primary.needsResolve {
		t.Fatal("unused current-location favorite resolved")
	}
	if err := a.notifications.Configure(M{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	a.Tick(context.Background())
	settleForecasts(t, a)
	if resolves.Load() != 1 || a.primary.location["name"] != "Updated current location" || a.id != "place-101" {
		t.Fatal("primary current fix changed viewed city or did not resolve")
	}
}

func TestSavedRuntimeLateAlertCannotCrossViewIdentity(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 1)
	old := a.forecastPoint
	old.generation, old.forecastGeneration = 88, 88
	locationAction(t, a, M{"action": "view", "id": "place-102"})
	locationAction(t, a, M{"action": "view", "id": "place-101"})
	// Matching coordinates and even a matching generation are insufficient;
	// the reloaded city's request owner is distinct from the retired point.
	a.generation, a.forecastGeneration = 88, 88
	before := a.saved.document()
	a.results <- completion{point: old, generation: 88, location: a.location, alerts: weather.UnavailableAlerts()}
	a.Snapshot()
	if !reflect.DeepEqual(before, a.saved.document()) {
		t.Fatal("late alert from earlier visit published")
	}
}

func TestSavedRuntimeHeadlessWaitsForIndependentAlerts(t *testing.T) {
	a, state := runtimeLocations(t, Options{Offline: true}, 1)
	a.Close(context.Background())
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- RefreshBarSaved(context.Background(), state, Options{Now: func() time.Time { return savedRuntimeNow.Add(time.Hour) }, Fetch: func(_ context.Context, l M, now time.Time) (M, error) {
			f := appFixture(now)
			f["location"] = l
			return f, nil
		}, FetchAlerts: func(ctx context.Context, _ M, now time.Time) (M, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return M{"status": "available", "items": []any{}, "fetched_at": now.Format(time.RFC3339), "freshness": "current"}, nil
		}})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("alert request missing")
	}
	select {
	case err := <-done:
		t.Fatal("headless refresh exited before alert publication", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("headless refresh did not finish")
	}
	profile, err := ReadPrimaryProfile(state)
	if err != nil || object(object(profile["forecast"])["alerts"])["freshness"] != "current" {
		t.Fatal("independent alerts were not saved", err)
	}
}

func TestSavedRuntimeLimitRejectsBeforeProviderWork(t *testing.T) {
	var calls atomic.Int32
	a, _ := runtimeLocations(t, Options{Resolve: func(context.Context, M) (M, error) { calls.Add(1); return nil, errors.New("unexpected lookup") }}, 0)
	for i := 3; i < savedLocationLimit; i++ {
		if err := a.saved.put(savedFixture(i), false, false); err != nil {
			t.Fatal(err)
		}
	}
	before := a.saved.document()
	r, _ := a.Handle(context.Background(), request("add_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	if r["ok"] != false || r["error"] != "location_limit" || calls.Load() != 0 || a.locationBusy || !reflect.DeepEqual(before, a.saved.document()) {
		t.Fatal("collection limit admitted provider work", r)
	}
}

func TestSavedRuntimeAlertCallsRemainBoundedAfterCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	a := newTestApp(t, Options{Resolve: func(_ context.Context, s M) (M, error) {
		l := weather.DefaultLocation()
		l["name"] = s["zip_code"]
		return l, nil
	}, Fetch: func(_ context.Context, l M, now time.Time) (M, error) {
		f := appFixture(now)
		f["location"] = l
		return f, nil
	}, FetchAlerts: func(context.Context, M, time.Time) (M, error) {
		calls.Add(1)
		<-release
		return weather.UnavailableAlerts(), nil
	}})
	for _, zip := range []string{"10001", "10002", "10003", "10004", "10005", "10006"} {
		a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": zip}}))
		until := time.Now().Add(time.Second)
		for a.zip != zip && time.Now().Before(until) {
			a.Snapshot()
			time.Sleep(time.Millisecond)
		}
		if a.zip != zip {
			t.Fatal("slow canceled alerts blocked new forecast", zip)
		}
	}
	settleForecasts(t, a)
	if calls.Load() != 2 || len(a.alertSlots) != 2 {
		t.Fatal("canceled alert calls escaped hard cap", calls.Load(), len(a.alertSlots))
	}
	if object(a.Snapshot()["alerts"])["freshness"] == "pending" {
		t.Fatal("unadmitted alert call left indefinite loading state")
	}
	started := time.Now()
	if err := a.Close(context.Background()); err != nil || time.Since(started) > time.Second {
		t.Fatal("alert callback blocked shutdown", err)
	}
}

func TestSavedRuntimeFailedBrowsingKeepsPrimaryNotifications(t *testing.T) {
	messages := make(chan string, 1)
	a, _ := runtimeLocations(t, Options{Resolve: func(context.Context, M) (M, error) { return nil, errors.New("lookup failed") }, Sender: func(_ context.Context, _ string, body string) error { messages <- body; return nil }}, 0)
	a.Handle(context.Background(), request("add_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	settleForecasts(t, a)
	if a.locationError != "lookup_failed" || a.forecastPoint != a.primary {
		t.Fatal("failed browse changed selected primary")
	}
	r, _ := a.Handle(context.Background(), request("set_notifications", M{"notifications": M{"enabled": true, "quiet_enabled": false}}))
	if r["ok"] != true {
		t.Fatal(r)
	}
	select {
	case body := <-messages:
		if !strings.Contains(body, "City 0") {
			t.Fatal("wrong primary", body)
		}
	case <-time.After(time.Second):
		t.Fatal("failed browsing suspended primary notifications")
	}
}
