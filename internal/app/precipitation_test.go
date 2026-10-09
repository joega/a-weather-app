package app

import (
	"bufio"
	"context"
	"errors"
	"math"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/precipitation"
	"github.com/joega/a-weather-app/internal/safeio"
)

func precipitationFixture(location M, at time.Time) precipitation.Data {
	d := precipitation.Data{Latitude: location["latitude"].(float64), Longitude: location["longitude"].(float64), GridLatitude: location["latitude"].(float64), GridLongitude: location["longitude"].(float64), Timezone: stringOf(location["timezone"]), FetchedAt: at}
	for i := 0; i < precipitation.MaxHours; i++ {
		d.Hours = append(d.Hours, precipitation.Hour{End: at.Truncate(time.Hour).Add(time.Duration(i-26) * time.Hour), TotalMM: precipitation.Number(1), RainMM: precipitation.Number(.3), ShowersMM: precipitation.Number(.2), SnowCM: precipitation.Number(.4), DepthM: precipitation.Number(.1), FreezingM: precipitation.Number(1500), Probability: precipitation.Number(.45)})
	}
	return d
}
func precipitationQuery(a *App, date string, token float64) M {
	q := astronomyQuery(a, date)
	q["client_token"] = token
	return q
}
func precipitationApp(t *testing.T, fetch func(context.Context, M, time.Time) (precipitation.Data, error), now func() time.Time) (*App, *safeio.Directory) {
	t.Helper()
	return runtimeLocations(t, Options{Now: now, FetchPrecipitation: fetch, Fetch: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("fixture forecast unavailable") }, FetchAlerts: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("fixture alerts unavailable") }}, 0)
}
func awaitPrecipitation(t *testing.T, a *App, predicate func(M) bool) M {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		_, p, _ := a.precipitationSince(nil)
		if p != nil && predicate(p) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("precipitation state did not settle")
	return nil
}

func TestPrecipitationLazyNarrowReplyCacheAndDaySelection(t *testing.T) {
	now := savedRuntimeNow
	var calls atomic.Int32
	a, state := precipitationApp(t, func(_ context.Context, l M, at time.Time) (precipitation.Data, error) {
		calls.Add(1)
		return precipitationFixture(l, at), nil
	}, func() time.Time { return now })
	for range 5 {
		a.Snapshot()
		a.Tick(context.Background())
	}
	if a.precipitation != nil || calls.Load() != 0 {
		t.Fatal("unused detail initialized or fetched")
	}
	if data, err := state.Read(precipitationFile, precipitation.CacheBytes); err != nil || data != nil {
		t.Fatal("unused detail persisted data", err)
	}
	open := func(date string, token float64) M {
		t.Helper()
		r, stop := a.Handle(context.Background(), request("precipitation_open", M{"detail": precipitationQuery(a, date, token)}))
		if stop || r["ok"] != true || len(r) != 4 || r["snapshot"] != nil {
			t.Fatal("detail reply not isolated", r)
		}
		return object(r["precipitation"])
	}
	open("", 1)
	p := awaitPrecipitation(t, a, func(p M) bool { return p["status"] == "fresh" })
	if len(p["days"].([]any)) != 10 || len(p["hours"].([]any)) != 24 || p["source"] != precipitation.Source || p["client_token"] != 1.0 {
		t.Fatal("missing bounded daily/hourly data", p)
	}
	first := object(p["days"].([]any)[0])
	if first["total_mm"] != 24.0 || first["rain_mm"] != 12.0 || first["total_mm_coverage"] != 24.0 || first["depth_max_m"] != .1 || first["freezing_min_m"] != 1500.0 || first["chance_max"] != .45 || first["kind"] != "rain_and_snow" {
		t.Fatal("daily semantics changed", first)
	}
	for _, day := range p["days"].([]any) {
		view := open(stringOf(object(day)["date"]), 2)
		raw, err := ipc.Encode(M{"version": 1.0, "request_id": 1.0, "ok": true, "precipitation": view}, ipc.ResponseLimit)
		if err != nil || len(raw) > 32*1024 {
			t.Fatal("narrow reply exceeded 32 KiB target", len(raw), err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("date selection fetched again")
	}
	record, err := state.Read(precipitationFile, precipitation.CacheBytes)
	if err != nil || record == nil {
		t.Fatal("optional data was not saved", err)
	}
	d, err := precipitation.ReadCache(record)
	if err != nil || len(d.Hours) != precipitation.MaxHours {
		t.Fatal("invalid saved detail", err)
	}
	// An old close must not cancel the newer selection on this same peer.
	a.Handle(context.Background(), request("precipitation_close", M{"detail": M{"client_token": 1.0}}))
	if !a.precipitation.open {
		t.Fatal("late close canceled newer selection")
	}
	a.Handle(context.Background(), request("precipitation_close", M{"detail": M{"client_token": 2.0}}))
	if a.precipitation.open {
		t.Fatal("close retained demand")
	}
	for range 40 {
		open("", 3)
		a.Handle(context.Background(), request("precipitation_close", M{"detail": M{"client_token": 3.0}}))
	}
	after, _ := state.Read(precipitationFile, precipitation.CacheBytes)
	if calls.Load() != 1 || !reflect.DeepEqual(record, after) {
		t.Fatal("fresh reopen fetched or rewrote cache")
	}
	// The regular snapshot never contains the optional detail dataset.
	snapshot := a.Snapshot()
	if snapshot["precipitation"] != nil || snapshot["precipitation_detail"] != nil {
		t.Fatal("detail leaked into ordinary snapshot")
	}
}

func TestPrecipitationValidationVisibilityAndLateCompletion(t *testing.T) {
	started, released := make(chan context.Context, 1), make(chan struct{})
	a, state := precipitationApp(t, func(ctx context.Context, l M, at time.Time) (precipitation.Data, error) {
		started <- ctx
		<-released
		return precipitationFixture(l, at), nil
	}, nil)
	t.Cleanup(func() {
		select {
		case <-released:
		default:
			close(released)
		}
	})
	for _, patch := range []M{{"date": "2026-02-30"}, {"date": "2026-10-18"}, {"date": nil}, {"latitude": math.NaN()}, {"longitude": 100.0}, {"timezone": "UTC"}, {"location_id": "other"}, {"client_token": -1.0}, {"client_token": 1.5}, {"extra": true}} {
		q := precipitationQuery(a, "", 1)
		for k, v := range patch {
			q[k] = v
		}
		r, _ := a.Handle(context.Background(), request("precipitation_open", M{"detail": q}))
		if r["ok"] != false {
			t.Fatal("bad query admitted", patch, r)
		}
	}
	if a.precipitation != nil {
		t.Fatal("invalid query initialized detail")
	}
	a.setPresented(false)
	r, _ := a.Handle(context.Background(), request("precipitation_open", M{"detail": precipitationQuery(a, "", 1)}))
	if r["ok"] != false {
		t.Fatal("hidden direct demand admitted")
	}
	a.setPresented(true)
	q := precipitationQuery(a, "", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ = a.Handle(ctx, request("precipitation_open", M{"detail": q}))
	if r["ok"] != false || a.precipitation != nil {
		t.Fatal("canceled demand admitted")
	}
	a.Handle(context.Background(), request("precipitation_open", M{"detail": q}))
	var work context.Context
	select {
	case work = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no fetch")
	}
	locationAction(t, a, M{"action": "view", "id": stringOf(savedEntryAt(a, 1)["id"])})
	if work.Err() != context.Canceled || a.precipitation.open {
		t.Fatal("place change did not cancel detail")
	}
	for len(a.PrecipitationChanged) > 0 {
		<-a.PrecipitationChanged
	}
	close(released)
	select {
	case <-a.PrecipitationChanged:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled callback did not finish")
	}
	a.Snapshot()
	if record, _ := state.Read(precipitationFile, precipitation.CacheBytes); record != nil {
		t.Fatal("late data saved")
	}
	if _, p, _ := a.precipitationSince(nil); p != nil {
		t.Fatal("late data shown after place change")
	}
}

func TestPrecipitationOfflineRestartRolloverAndFailedStorage(t *testing.T) {
	now := savedRuntimeNow
	a, state := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	d := precipitationFixture(a.location, now)
	record, _ := precipitation.CacheRecord(d)
	if err := state.Write(precipitationFile, record, precipitation.CacheBytes); err != nil {
		t.Fatal(err)
	}
	open := func(a *App) M {
		r, _ := a.Handle(context.Background(), request("precipitation_open", M{"detail": precipitationQuery(a, "", 1)}))
		if r["ok"] != true {
			t.Fatal(r)
		}
		return object(r["precipitation"])
	}
	p := open(a)
	if p["status"] != "fresh" || p["error"] != "offline" || len(p["days"].([]any)) != 10 {
		t.Fatal("offline cache not available", p)
	}
	restarted, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close(context.Background()) })
	if restarted.precipitation != nil || !reflect.DeepEqual(p, open(restarted)) {
		t.Fatal("lazy offline restart changed data")
	}
	// Calendar rollover must publish even when controller freshness is unchanged.
	zone, _ := time.LoadLocation(stringOf(a.location["timezone"]))
	now = time.Date(2026, 10, 8, 23, 59, 0, 0, zone)
	revision, before, _ := a.precipitationSince(nil)
	now = now.Add(2 * time.Minute)
	_, after, _ := a.precipitationSince(&revision)
	if after == nil || after["today"] == before["today"] || after["date"] != after["today"] {
		t.Fatal("local day rollover not published")
	}
	if after["status"] != "unavailable" || len(after["days"].([]any)) != 0 {
		t.Fatal("expired offline data retained")
	}
	// Missing storage cannot stop an otherwise valid online retrieval.
	online, _ := precipitationApp(t, func(_ context.Context, l M, at time.Time) (precipitation.Data, error) {
		return precipitationFixture(l, at), nil
	}, nil)
	online.state = nil
	open(online)
	p = awaitPrecipitation(t, online, func(p M) bool { return p["status"] == "fresh" })
	if p["save_failed"] != true || len(p["days"].([]any)) != 10 {
		t.Fatal("failed optional save erased data")
	}
}

func TestPrecipitationSocketOwnerHideAndDisconnect(t *testing.T) {
	f := serveFixture(t)
	started := make(chan context.Context, 4)
	f.a.mu.Lock()
	f.a.mode, f.a.id = "place", "place-100"
	f.a.options.Offline = false
	originalNow := f.a.options.Now()
	f.a.options.Fetch = func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchCountry = func(context.Context, M, time.Time, string) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchAlerts = func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchPrecipitation = func(ctx context.Context, _ M, _ time.Time) (precipitation.Data, error) {
		started <- ctx
		<-ctx.Done()
		return precipitation.Data{}, ctx.Err()
	}
	q := precipitationQuery(f.a, "", 1)
	f.a.mu.Unlock()
	c, other := connect(t, f.path), connect(t, f.path)
	r, otherReader := bufio.NewReader(c), bufio.NewReader(other)
	send := func(primary bool, op string, patch M) M {
		t.Helper()
		target, reader := c, r
		if !primary {
			target, reader = other, otherReader
		}
		if err := ipc.Send(target, request(op, patch), ipc.RequestLimit); err != nil {
			t.Fatal(err)
		}
		return readReply(t, reader)
	}
	if got := send(true, "precipitation_open", M{"detail": q}); got["error"] != "precipitation_not_presented" {
		t.Fatal("unsubscribed demand accepted", got)
	}
	send(true, "subscribe", nil)
	send(false, "subscribe", nil)
	if got := send(true, "precipitation_open", M{"detail": q}); got["ok"] != true {
		t.Fatal(got)
	}
	var work context.Context
	select {
	case work = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no demand")
	}
	if got := send(false, "precipitation_open", M{"detail": q}); got["error"] != "precipitation_in_use" {
		t.Fatal("second peer replaced owner", got)
	}
	send(false, "precipitation_close", M{"detail": M{"client_token": 1.0}})
	if work.Err() != nil {
		t.Fatal("other peer closed owner")
	}
	f.a.setPresented(false)
	if work.Err() != nil {
		t.Fatal("delayed aggregate visibility closed a presented peer's detail")
	}
	send(true, "set_presentation", M{"active": false})
	if work.Err() != context.Canceled {
		t.Fatal("hidden owner kept working despite another visible peer")
	}
	if got := send(true, "precipitation_open", M{"detail": q}); got["error"] != "precipitation_not_presented" {
		t.Fatal("hidden demand accepted", got)
	}
	// Opening from the remaining visible peer obtains its own lease. Disconnect
	// then releases it without relying on aggregate presentation becoming false.
	send(true, "set_presentation", M{"active": true})
	f.a.mu.Lock()
	f.a.options.Now = func() time.Time { return originalNow.Add(2 * time.Minute) }
	f.a.mu.Unlock()
	if got := send(false, "precipitation_open", M{"detail": q}); got["ok"] != true {
		t.Fatal(got)
	}
	select {
	case work = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no replacement demand")
	}
	other.Close()
	select {
	case <-work.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("disconnected owner kept working")
	}
	// The remaining subscriber keeps the service alive.
	if send(true, "snapshot", nil)["ok"] != true {
		t.Fatal("owner disconnect stopped another subscriber")
	}
}
