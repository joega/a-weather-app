package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
	"github.com/joega/a-weather-app/internal/weathermap"
)

func mapFixture(now time.Time, lat, lon float64) weathermap.Data {
	d := weathermap.Data{Latitude: lat, Longitude: lon, RadiusMiles: 10, ModelID: "ncep_nbm_conus", ModelName: "NOAA NBM CONUS", ResolutionKM: 2.5, FetchedAt: now, Attribution: "Model forecast via Open-Meteo (CC BY 4.0)"}
	for h := 0; h < 24; h++ {
		d.Hours = append(d.Hours, now.Truncate(time.Hour).Add(time.Duration(h)*time.Hour).Unix())
	}
	for i := 0; i < 25; i++ {
		c := weathermap.Cell{Latitude: lat + float64(2-i/5)*0.07, Longitude: lon + float64(i%5-2)*0.09}
		for h := 0; h < 24; h++ {
			c.Temperature = append(c.Temperature, 12)
			c.WindSpeed = append(c.WindSpeed, 3)
			c.WindDirection = append(c.WindDirection, 0)
			c.Precipitation = append(c.Precipitation, 0)
		}
		d.Cells = append(d.Cells, c)
	}
	return d
}

func waitMap(t *testing.T, a *App, status string) M {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, v := a.Map()
		if v != nil && v["status"] == status {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("map did not reach %s", status)
	return nil
}

func TestMapOnDemandCacheOfflineAndCancellation(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Fetch: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline fixture") }, FetchMap: func(ctx context.Context, lat, lon float64, country string, at time.Time) (weathermap.Data, error) {
		calls.Add(1)
		return mapFixture(at, lat, lon), nil
	}})
	for i := 0; i < 100; i++ {
		a.Snapshot()
		a.Tick(context.Background())
	}
	if calls.Load() != 0 {
		t.Fatal("closed map made requests")
	}
	reply, _ := a.Handle(context.Background(), request("map_open", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	v := waitMap(t, a, "fresh")
	if v["error"] != "" {
		t.Fatal("map cache write failed", v["error"])
	}
	if calls.Load() != 1 || v["data"] == nil {
		t.Fatal("map result missing")
	}
	a.Handle(context.Background(), request("map_close", nil))
	a.Handle(context.Background(), request("map_open", nil))
	if v = waitMap(t, a, "fresh"); v["data"] == nil || calls.Load() != 1 {
		t.Fatal("cached reopen refetched")
	}
	a.Handle(context.Background(), request("map_close", nil))
	offline, e := New(a.state, Options{Now: func() time.Time { return now.Add(time.Hour) }, Offline: true, FetchMap: func(context.Context, float64, float64, string, time.Time) (weathermap.Data, error) {
		t.Error("offline map requested provider")
		return weathermap.Data{}, errors.New("offline")
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer offline.Close(context.Background())
	offline.Handle(context.Background(), request("map_open", nil))
	if v = waitMap(t, offline, "stale"); v["data"] == nil {
		t.Fatal("offline map cache unavailable")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	b := newTestApp(t, Options{Now: func() time.Time { return now }, FetchMap: func(ctx context.Context, lat, lon float64, country string, at time.Time) (weathermap.Data, error) {
		close(started)
		<-release
		return mapFixture(at, lat, lon), nil
	}})
	b.Handle(context.Background(), request("map_open", nil))
	<-started
	b.Handle(context.Background(), request("map_close", nil))
	close(release)
	if v = waitMap(t, b, "closed"); v["data"] != nil {
		t.Fatal("late map result published after close")
	}
	deadline := time.Now().Add(time.Second)
	for b.wmap.active && time.Now().Before(deadline) {
		b.Map()
		time.Sleep(time.Millisecond)
	}
	if b.wmap.active || b.wmap.data != nil {
		t.Fatal("canceled result retained")
	}
}
func TestMapFailedOpenIsRateLimited(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	a := newTestApp(t, Options{Now: func() time.Time { return now }, FetchMap: func(context.Context, float64, float64, string, time.Time) (weathermap.Data, error) {
		calls.Add(1)
		return weathermap.Data{}, errors.New("provider overloaded")
	}})
	a.Handle(context.Background(), request("map_open", nil))
	waitMap(t, a, "unavailable")
	for i := 0; i < 50; i++ {
		a.Handle(context.Background(), request("map_close", nil))
		a.Handle(context.Background(), request("map_open", nil))
		a.Map()
	}
	if calls.Load() != 1 {
		t.Fatalf("rapid reopen made %d provider requests", calls.Load())
	}
	now = now.Add(21 * time.Minute)
	a.Handle(context.Background(), request("map_open", nil))
	deadline := time.Now().Add(time.Second)
	for calls.Load() < 2 && time.Now().Before(deadline) {
		a.Map()
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 2 {
		t.Fatal("did not retry after rate limit")
	}
}
func TestMapHourLabelsDisambiguateDST(t *testing.T) {
	now := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Offline: true})
	d := mapFixture(now, 40.7128, -74.006)
	a.wmap.data = &d
	a.wmap.open = true
	a.wmap.revision++
	_, m := a.Map()
	labels := m["hour_labels"].([]string)
	if len(labels) != 24 || !strings.Contains(labels[0], "1:00 AM EDT") || !strings.Contains(labels[1], "1:00 AM EST") {
		t.Fatalf("ambiguous map time labels: %v", labels[:2])
	}
}
func TestQuitCancelsMapFetch(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	a := newTestApp(t, Options{FetchMap: func(ctx context.Context, lat, lon float64, country string, now time.Time) (weathermap.Data, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return weathermap.Data{}, ctx.Err()
	}})
	a.Handle(context.Background(), request("map_open", nil))
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := a.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("map worker survived quit")
	}
	if saved, e := a.state.Read("weather-map.json", mapCacheBytes); e != nil || saved != nil {
		t.Fatal("quit saved obsolete map", e)
	}
}
func TestCanceledOpeningGetsOnePromptRetry(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	var calls atomic.Int32
	a := newTestApp(t, Options{Now: func() time.Time { return now }, FetchMap: func(ctx context.Context, lat, lon float64, country string, at time.Time) (weathermap.Data, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return weathermap.Data{}, ctx.Err()
		}
		return mapFixture(at, lat, lon), nil
	}})
	a.Handle(context.Background(), request("map_open", nil))
	<-started
	a.Handle(context.Background(), request("map_close", nil))
	a.Handle(context.Background(), request("map_open", nil))
	if v := waitMap(t, a, "fresh"); v["data"] == nil || calls.Load() != 2 {
		t.Fatal("canceled first open did not retry once")
	}
}
func TestSelectedLocationReplacesMapIdentity(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	boston := weather.DefaultLocation()
	boston["name"] = "Boston, MA"
	boston["latitude"] = 42.3601
	boston["longitude"] = -71.0589
	var calls atomic.Int32
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Resolve: func(context.Context, M) (M, error) { return boston, nil }, Fetch: func(_ context.Context, location M, _ time.Time) (M, error) {
		f := appFixture(now)
		f["location"] = location
		return f, nil
	}, FetchMap: func(_ context.Context, lat, lon float64, _ string, at time.Time) (weathermap.Data, error) {
		calls.Add(1)
		return mapFixture(at, lat, lon), nil
	}})
	a.Handle(context.Background(), request("map_open", nil))
	waitMap(t, a, "fresh")
	reply, _ := a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	if _, v := a.Map(); v["status"] != "closed" {
		t.Fatal("map remained open during location change")
	}
	awaitCompletion(t, a)
	if a.location["latitude"] != 42.3601 || a.wmap.data != nil {
		t.Fatal("old map survived selected location")
	}
	a.Handle(context.Background(), request("map_open", nil))
	v := waitMap(t, a, "fresh")
	if v["data"].(*weathermap.Data).Latitude != 42.3601 || calls.Load() != 2 {
		t.Fatal("new map did not follow selected location")
	}
}
