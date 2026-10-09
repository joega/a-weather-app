package app

import (
	"bufio"
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
)

func airOutlookFixture(location M, at time.Time) airquality.Outlook {
	d := airquality.Outlook{Latitude: location["latitude"].(float64), Longitude: location["longitude"].(float64), GridLatitude: location["latitude"].(float64), GridLongitude: location["longitude"].(float64), Timezone: stringOf(location["timezone"]), FetchedAt: at}
	for i := 0; i < airquality.OutlookHours; i++ {
		h := airquality.OutlookHour{Time: at.UTC().Truncate(time.Hour).Add(time.Duration(i) * time.Hour)}
		for k := range h.Values {
			h.Values[k] = airquality.OutlookValue{Number: float64(k*10 + i), Known: true}
		}
		d.Hours = append(d.Hours, h)
	}
	return d
}
func airOutlookQuery(a *App, token float64) M {
	return M{"location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": a.location["timezone"], "client_token": token}
}
func airOutlookApp(t *testing.T, fetch func(context.Context, M, time.Time) (airquality.Outlook, error), now func() time.Time) (*App, *safeio.Directory) {
	t.Helper()
	return runtimeLocations(t, Options{Now: now, FetchAirOutlook: fetch, Fetch: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("fixture forecast unavailable") }, FetchAlerts: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("fixture alerts unavailable") }}, 0)
}
func awaitAirOutlook(t *testing.T, a *App, predicate func(M) bool) M {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		_, p, _ := a.airOutlookSince(nil)
		if p != nil && predicate(p) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("AQ outlook did not settle")
	return nil
}
func openAirOutlook(t *testing.T, a *App, token float64) M {
	t.Helper()
	r, stop := a.Handle(context.Background(), request("air_outlook_open", M{"detail": airOutlookQuery(a, token)}))
	if stop || r["ok"] != true || len(r) != 4 || r["snapshot"] != nil {
		t.Fatal("outlook not isolated", r)
	}
	return object(r["air_outlook"])
}
func TestAirOutlookLazyReplyGapsCacheAndReopen(t *testing.T) {
	now := savedRuntimeNow
	var calls atomic.Int32
	a, state := airOutlookApp(t, func(_ context.Context, l M, at time.Time) (airquality.Outlook, error) {
		calls.Add(1)
		d := airOutlookFixture(l, at)
		d.Hours[2].Values[5] = airquality.OutlookValue{}
		d.Hours = append(d.Hours[:4:4], d.Hours[5:]...)
		return d, nil
	}, func() time.Time { return now })
	for range 5 {
		a.Snapshot()
		a.Tick(context.Background())
	}
	if a.airOutlook != nil || calls.Load() != 0 {
		t.Fatal("unused outlook performed work")
	}
	if data, err := state.Read(airOutlookFile, airquality.OutlookCacheBytes); err != nil || data != nil {
		t.Fatal("unused outlook persisted", err)
	}
	openAirOutlook(t, a, 1)
	p := awaitAirOutlook(t, a, func(p M) bool { return p["status"] == "fresh" })
	rows := p["hours"].([]any)
	if len(rows) != 48 || object(rows[0])["us_aqi"] != 0.0 || object(rows[2])["ozone"] != nil || object(rows[2])["pm2_5"] != 22.0 {
		t.Fatal("unknown and zero lost", p)
	}
	for _, field := range airquality.OutlookMetrics() {
		if object(rows[4])[field.Key] != nil {
			t.Fatal("gap interpolated")
		}
	}
	encoded, err := ipc.Encode(M{"version": 1.0, "request_id": 1.0, "ok": true, "air_outlook": p}, ipc.ResponseLimit)
	if err != nil || len(encoded) > 32*1024 {
		t.Fatal("outlook exceeds narrow IPC budget", len(encoded), err)
	}
	record, err := state.Read(airOutlookFile, airquality.OutlookCacheBytes)
	if err != nil || record == nil {
		t.Fatal("cache missing", err)
	}
	openAirOutlook(t, a, 2)
	a.Handle(context.Background(), request("air_outlook_close", M{"detail": M{"client_token": 1.0}}))
	if !a.airOutlook.open {
		t.Fatal("old token canceled replacement")
	}
	a.Handle(context.Background(), request("air_outlook_close", M{"detail": M{"client_token": 2.0}}))
	if a.airOutlook.open {
		t.Fatal("close retained demand")
	}
	for range 40 {
		openAirOutlook(t, a, 3)
		a.Handle(context.Background(), request("air_outlook_close", M{"detail": M{"client_token": 3.0}}))
	}
	after, _ := state.Read(airOutlookFile, airquality.OutlookCacheBytes)
	if calls.Load() != 1 || !reflect.DeepEqual(record, after) {
		t.Fatal("fresh reopening fetched or rewrote cache")
	}
	if a.Snapshot()["air_outlook"] != nil {
		t.Fatal("outlook enlarged ordinary snapshot")
	}
}
func TestAirOutlookOfflineRestartHourlyRolloverAndStorageFailure(t *testing.T) {
	now := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	a, state := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	// New York's repeated 1 AM must have distinct UTC offsets.
	a.location["timezone"] = "America/New_York"
	d := airOutlookFixture(a.location, now)
	record, _ := airquality.OutlookCacheRecord(d)
	if err := state.Write(airOutlookFile, record, airquality.OutlookCacheBytes); err != nil {
		t.Fatal(err)
	}
	p := openAirOutlook(t, a, 1)
	rows := p["hours"].([]any)
	if p["status"] != "fresh" || p["error"] != "offline" || len(rows) != 48 || !strings.Contains(stringOf(object(rows[0])["label"]), "1:00 AM EDT (-04:00)") || !strings.Contains(stringOf(object(rows[1])["label"]), "1:00 AM EST (-05:00)") {
		t.Fatal("DST labels or offline state", p)
	}
	restarted, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close(context.Background()) })
	restarted.location["timezone"] = "America/New_York"
	if restarted.airOutlook != nil || !reflect.DeepEqual(p, openAirOutlook(t, restarted, 1)) {
		t.Fatal("offline restart changed data")
	}
	revision, before, _ := a.airOutlookSince(nil)
	now = now.Add(31 * time.Minute)
	_, after, _ := a.airOutlookSince(&revision)
	if after == nil || after["start"] == before["start"] || after["status"] != "fresh" || !strings.Contains(stringOf(object(after["hours"].([]any)[0])["label"]), "EST") {
		t.Fatal("hour rollover not published without freshness change")
	}
	if object(after["hours"].([]any)[47])["us_aqi"] != nil {
		t.Fatal("trailing missing sample fabricated")
	}
	now = now.Add(6 * time.Hour)
	_, expired, _ := a.airOutlookSince(nil)
	if expired["status"] != "unavailable" || len(expired["hours"].([]any)) != 0 {
		t.Fatal("expired samples shown")
	}
	online, _ := airOutlookApp(t, func(_ context.Context, l M, at time.Time) (airquality.Outlook, error) {
		return airOutlookFixture(l, at), nil
	}, nil)
	online.state = nil
	openAirOutlook(t, online, 1)
	p = awaitAirOutlook(t, online, func(p M) bool { return p["status"] == "fresh" })
	if p["save_failed"] != true || len(p["hours"].([]any)) != 48 {
		t.Fatal("optional save failure erased result")
	}
}
func TestAirOutlookValidationVisibilityAndLateCompletion(t *testing.T) {
	started, released := make(chan context.Context, 1), make(chan struct{})
	a, state := airOutlookApp(t, func(ctx context.Context, l M, at time.Time) (airquality.Outlook, error) {
		started <- ctx
		<-released
		return airOutlookFixture(l, at), nil
	}, nil)
	t.Cleanup(func() {
		select {
		case <-released:
		default:
			close(released)
		}
	})
	for _, patch := range []M{{"date": "2026-02-30"}, {"date": "2026-10-18"}, {"date": nil}, {"latitude": math.NaN()}, {"longitude": 100.0}, {"timezone": "UTC"}, {"location_id": "other"}, {"client_token": -1.0}, {"client_token": 1.5}, {"extra": true}} {
		q := airOutlookQuery(a, 1)
		for k, v := range patch {
			q[k] = v
		}
		r, _ := a.Handle(context.Background(), request("air_outlook_open", M{"detail": q}))
		if r["ok"] != false {
			t.Fatal("bad query admitted", patch, r)
		}
	}
	if a.airOutlook != nil {
		t.Fatal("invalid query initialized detail")
	}
	a.setPresented(false)
	r, _ := a.Handle(context.Background(), request("air_outlook_open", M{"detail": airOutlookQuery(a, 1)}))
	if r["ok"] != false {
		t.Fatal("hidden direct demand admitted")
	}
	a.setPresented(true)
	q := airOutlookQuery(a, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ = a.Handle(ctx, request("air_outlook_open", M{"detail": q}))
	if r["ok"] != false || a.airOutlook != nil {
		t.Fatal("canceled demand admitted")
	}
	a.Handle(context.Background(), request("air_outlook_open", M{"detail": q}))
	var work context.Context
	select {
	case work = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no fetch")
	}
	locationAction(t, a, M{"action": "view", "id": stringOf(savedEntryAt(a, 1)["id"])})
	if work.Err() != context.Canceled || a.airOutlook.open {
		t.Fatal("place change did not cancel detail")
	}
	for len(a.AirOutlookChanged) > 0 {
		<-a.AirOutlookChanged
	}
	close(released)
	select {
	case <-a.AirOutlookChanged:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled callback did not finish")
	}
	a.Snapshot()
	if record, _ := state.Read(airOutlookFile, airquality.OutlookCacheBytes); record != nil {
		t.Fatal("late data saved")
	}
	if _, p, _ := a.airOutlookSince(nil); p != nil {
		t.Fatal("late data shown after place change")
	}
}

func TestAirOutlookSocketOwnerHideAndDisconnect(t *testing.T) {
	f := serveFixture(t)
	started := make(chan context.Context, 4)
	f.a.mu.Lock()
	f.a.mode, f.a.id = "place", "place-100"
	f.a.options.Offline = false
	originalNow := f.a.options.Now()
	f.a.options.Fetch = func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchCountry = func(context.Context, M, time.Time, string) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchAlerts = func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture") }
	f.a.options.FetchAirOutlook = func(ctx context.Context, _ M, _ time.Time) (airquality.Outlook, error) {
		started <- ctx
		<-ctx.Done()
		return airquality.Outlook{}, ctx.Err()
	}
	q := airOutlookQuery(f.a, 1)
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
	if got := send(true, "air_outlook_open", M{"detail": q}); got["error"] != "air_outlook_not_presented" {
		t.Fatal("unsubscribed demand accepted", got)
	}
	send(true, "subscribe", nil)
	send(false, "subscribe", nil)
	if got := send(true, "air_outlook_open", M{"detail": q}); got["ok"] != true {
		t.Fatal(got)
	}
	var work context.Context
	select {
	case work = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no demand")
	}
	if got := send(false, "air_outlook_open", M{"detail": q}); got["error"] != "air_outlook_in_use" {
		t.Fatal("second peer replaced owner", got)
	}
	send(false, "air_outlook_close", M{"detail": M{"client_token": 1.0}})
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
	if got := send(true, "air_outlook_open", M{"detail": q}); got["error"] != "air_outlook_not_presented" {
		t.Fatal("hidden demand accepted", got)
	}
	// Opening from the remaining visible peer obtains its own lease. Disconnect
	// then releases it without relying on aggregate presentation becoming false.
	send(true, "set_presentation", M{"active": true})
	f.a.mu.Lock()
	f.a.options.Now = func() time.Time { return originalNow.Add(2 * time.Minute) }
	f.a.mu.Unlock()
	if got := send(false, "air_outlook_open", M{"detail": q}); got["ok"] != true {
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
