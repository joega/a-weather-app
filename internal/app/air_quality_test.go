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

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func airQualityFixture(location M, fetched, valid time.Time) M {
	return M{"schema_version": 1.0, "location": safeio.Clone(location), "fetched_at": fetched.UTC().Format(time.RFC3339), "valid_at": valid.UTC().Format(time.RFC3339),
		"domain": "cams_global", "source": M{"provider": "Open-Meteo", "model": "CAMS global model data", "kind": "model_forecast", "attribution": airQualityAttribution},
		"units": M{"us_aqi": "USAQI", "european_aqi": "EAQI", "pm2_5_ug_m3": "μg/m³"}, "us_aqi": 42.0, "european_aqi": 37.0, "pm2_5_ug_m3": 3.5}
}

func awaitAirQuality(t *testing.T, a *App) M {
	t.Helper()
	until := time.Now().Add(3 * time.Second)
	for time.Now().Before(until) {
		s := a.Snapshot()
		if !object(s["air_quality"])["refreshing"].(bool) {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("air quality worker did not complete")
	return nil
}

func TestAirQualityIndependentCacheThrottleAndFailure(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	forecast := metricFixture(now)
	if err := state.Write("forecast.json", forecast, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	fail := false
	a, err := New(state, Options{Now: func() time.Time { return now }, Fetch: func(context.Context, M, time.Time) (M, error) {
		return nil, errors.New("weather unavailable")
	}, FetchAirQuality: func(_ context.Context, location M, at time.Time) (M, error) {
		calls.Add(1)
		if fail {
			return nil, errors.New("AQ unavailable")
		}
		return airQualityFixture(location, at, at), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	s := awaitAirQuality(t, a)
	if calls.Load() != 1 || object(s["air_quality"])["us_aqi"] != 42.0 || object(s["current"])["pressure_msl_hpa"] != 1013.25 {
		t.Fatal("independent AQ did not publish alongside core weather")
	}
	saved, err := state.Read("air-quality.json", airquality.MaxBytes)
	if err != nil || airquality.ValidateRecord(saved, weather.DefaultLocation()) != nil {
		t.Fatal("AQ cache not saved", err)
	}
	for i := 0; i < 20; i++ {
		a.Handle(context.Background(), request("refresh", nil))
		a.Tick(context.Background())
	}
	if a.fetchBusy {
		awaitCompletion(t, a)
	}
	if calls.Load() != 1 || object(a.Snapshot()["air_quality"])["us_aqi"] != 42.0 || object(a.Snapshot()["source"])["error"] != "refresh_failed" {
		t.Fatal("weather refresh bypassed AQ cap or weather failure erased AQ")
	}
	fail = true
	now = now.Add(time.Hour)
	s = awaitAirQuality(t, a)
	if calls.Load() != 2 || object(s["air_quality"])["error"] != "fetch_failed" || object(s["air_quality"])["us_aqi"] != 42.0 || object(s["current"])["temperature_c"] != 15.0 {
		t.Fatal("AQ failure affected last-good AQ/weather")
	}
	after, err := state.Read("air-quality.json", airquality.MaxBytes)
	if err != nil || !reflect.DeepEqual(saved, after) {
		t.Fatal("failed AQ refresh changed cache", err)
	}
	for i := 0; i < 100; i++ {
		a.Tick(context.Background())
	}
	if calls.Load() != 2 {
		t.Fatal("AQ failures retry before hourly budget")
	}
	core, err := state.Read("forecast.json", weather.MaxBytes)
	if err != nil || !reflect.DeepEqual(core, forecast) {
		t.Fatal("AQ changed core forecast bytes", err)
	}
	offline, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, FetchAirQuality: func(context.Context, M, time.Time) (M, error) {
		t.Error("offline AQ contacted provider")
		return nil, errors.New("offline")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close(context.Background())
	if aq := object(offline.Snapshot()["air_quality"]); aq["us_aqi"] != 42.0 || aq["offline"] != true {
		t.Fatal("offline AQ cache did not reopen")
	}
}

func TestAirQualityOwnFreshnessAndExpiredValues(t *testing.T) {
	now := time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, freshness      string
		fetchedAge, validAge time.Duration
		values               bool
	}{
		{"fresh", "fresh", time.Minute, time.Hour, true},
		{"valid-time-stale", "stale", time.Minute, 3 * time.Hour, true},
		{"fetch-time-stale", "stale", 3 * time.Hour, 3 * time.Hour, true},
		{"expired", "expired", time.Minute, 7 * time.Hour, false},
		{"clock-rollback", "invalid_future", -6 * time.Minute, -6 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &App{options: Options{Offline: true, Now: func() time.Time { return now }}, forecastPoint: &forecastPoint{location: weather.DefaultLocation()}}
			a.aq.record = airQualityFixture(a.location, now.Add(-tc.fetchedAge), now.Add(-tc.validAge))
			aq := a.airQualitySnapshot()
			if aq["freshness"] != tc.freshness || aq["offline"] != true || aq["valid_at"] != a.aq.record["valid_at"] {
				t.Fatal("AQ freshness borrowed weather or lost original valid time", aq)
			}
			for _, key := range []string{"us_aqi", "european_aqi", "pm2_5_ug_m3"} {
				if (aq[key] != nil) != tc.values {
					t.Fatal("AQ expiration failed to hide values", aq)
				}
			}
		})
	}
}

func TestAirQualityDamagedOrDifferentLocationCacheDoesNotBlockWeather(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"invalid", "different", "oversize", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			state := testState(t)
			if err := state.Write("forecast.json", metricFixture(now), weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			record := airQualityFixture(weather.DefaultLocation(), now, now)
			switch kind {
			case "invalid":
				record["us_aqi"] = -1.0
			case "different":
				object(record["location"])["name"] = "Another location"
			case "oversize":
				record["extra"] = strings.Repeat("x", airquality.MaxBytes)
			case "symlink":
				outside := filepath.Join(t.TempDir(), "external.json")
				if err := os.WriteFile(outside, []byte(`{"do_not_touch":true}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(state.Path, "air-quality.json")); err != nil {
					t.Fatal(err)
				}
			}
			if kind != "symlink" {
				if err := state.Write("air-quality.json", record, 2*airquality.MaxBytes); err != nil {
					t.Fatal(err)
				}
			}
			a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal("optional AQ cache prevented weather startup", err)
			}
			defer a.Close(context.Background())
			s := a.Snapshot()
			if object(s["current"])["temperature_c"] != 15.0 || object(s["air_quality"])["us_aqi"] != nil {
				t.Fatal("bad AQ affected weather or exposed data")
			}
			if kind != "different" && object(s["air_quality"])["error"] != "cache_invalid" {
				t.Fatal("missing AQ cache error")
			}
		})
	}
}

func TestAirQualityCanceledLocationWorkerCannotPublish(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls, running, maxRunning atomic.Int32
	a, err := New(state, Options{Now: func() time.Time { return now }, FetchAirQuality: func(ctx context.Context, location M, at time.Time) (M, error) {
		call := calls.Add(1)
		active := running.Add(1)
		defer running.Add(-1)
		for old := maxRunning.Load(); active > old && !maxRunning.CompareAndSwap(old, active); old = maxRunning.Load() {
		}
		if call == 1 {
			close(started)
			<-ctx.Done()
			close(canceled)
			<-release
		}
		return airQualityFixture(location, at, at), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	a.Snapshot()
	<-started
	for i := 0; i < 40; i++ {
		a.cancelAirQuality()
		a.location = M{"name": "Latest location", "latitude": float64(i), "longitude": 13.4, "timezone": "UTC"}
		a.aq.record = nil
		a.aq.nextFetch = now
		a.Snapshot()
	}
	<-canceled
	if calls.Load() != 1 {
		t.Fatal("cancellation released worker slot before completion")
	}
	close(release)
	s := awaitAirQuality(t, a)
	if calls.Load() != 2 || maxRunning.Load() != 1 || object(s["air_quality"])["us_aqi"] != 42.0 {
		t.Fatal("latest AQ was not coalesced into one worker")
	}
	record, err := state.Read("air-quality.json", airquality.MaxBytes)
	if err != nil || !reflect.DeepEqual(record["location"], a.location) {
		t.Fatal("obsolete AQ location was published")
	}
}

func TestAirQualityCloseAndSaveFailureRetainState(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	old := airQualityFixture(weather.DefaultLocation(), now.Add(-time.Hour), now.Add(-time.Hour))
	if err := state.Write("air-quality.json", old, airquality.MaxBytes); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	a, err := New(state, Options{Now: func() time.Time { return now }, FetchAirQuality: func(_ context.Context, location M, at time.Time) (M, error) {
		close(started)
		<-release
		v := airQualityFixture(location, at, at)
		v["us_aqi"] = 99.0
		return v, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	a.Snapshot()
	<-started
	if err = a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case completion := <-a.aq.results:
		a.aq.results <- completion
	case <-time.After(time.Second):
		t.Fatal("closed worker did not exit")
	}
	a.pollAirQuality()
	after, err := state.Read("air-quality.json", airquality.MaxBytes)
	if err != nil || !reflect.DeepEqual(old, after) {
		t.Fatal("closed AQ worker changed cache")
	}
	// A safeio publication refusal affects only AQ, retaining its last good record.
	b, err := New(state, Options{Now: func() time.Time { return now }, FetchAirQuality: func(_ context.Context, location M, at time.Time) (M, error) {
		v := airQualityFixture(location, at, at)
		v["us_aqi"] = 99.0
		return v, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	path := filepath.Join(state.Path, "air-quality.json")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	s := awaitAirQuality(t, b)
	if object(s["air_quality"])["us_aqi"] != 42.0 || object(s["air_quality"])["error"] != "save_failed" {
		t.Fatal("failed AQ publication lost last good data")
	}
}

func TestAirQualityTentativeLocationDoesNotBypassHourlyBudget(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-place", true: "failed-place"}[fail], func(t *testing.T) {
			now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			state := testState(t)
			var calls atomic.Int32
			a, err := New(state, Options{Now: func() time.Time { return now },
				Fetch: func(_ context.Context, location M, at time.Time) (M, error) {
					v := appFixture(at)
					v["location"] = location
					return v, nil
				},
				Resolve: func(context.Context, M) (M, error) {
					if fail {
						return nil, errors.New("lookup failed")
					}
					return weather.DefaultLocation(), nil
				},
				FetchAirQuality: func(_ context.Context, location M, at time.Time) (M, error) {
					calls.Add(1)
					return airQualityFixture(location, at, at), nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			awaitAirQuality(t, a)
			for i := 0; i < 3; i++ {
				r, _ := a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "10001"}}))
				if r["ok"] != true {
					t.Fatal(r)
				}
				awaitCompletion(t, a)
				awaitAirQuality(t, a)
			}
			if calls.Load() != 1 {
				t.Fatal("tentative or unchanged location reset hourly AQ budget", calls.Load())
			}
		})
	}
}

func TestAirQualityRefreshValidTimeAndFutureCacheRecovery(t *testing.T) {
	for _, kind := range []string{"regression", "same-old-time", "future-cache"} {
		t.Run(kind, func(t *testing.T) {
			now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			state := testState(t)
			oldFetched, oldValid := now.Add(-time.Hour), now.Add(-time.Hour)
			if kind == "same-old-time" {
				oldFetched, oldValid = now.Add(-4*time.Hour), now.Add(-4*time.Hour)
			}
			if kind == "future-cache" {
				oldFetched, oldValid = now.Add(24*time.Hour), now.Add(24*time.Hour)
			}
			old := airQualityFixture(weather.DefaultLocation(), oldFetched, oldValid)
			if err := state.Write("air-quality.json", old, airquality.MaxBytes); err != nil {
				t.Fatal(err)
			}
			a, err := New(state, Options{Now: func() time.Time { return now }, FetchAirQuality: func(_ context.Context, location M, at time.Time) (M, error) {
				valid := at
				if kind == "regression" {
					valid = oldValid.Add(-time.Hour)
				}
				if kind == "same-old-time" {
					valid = oldValid
				}
				v := airQualityFixture(location, at, valid)
				v["us_aqi"] = 99.0
				return v, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			aq := object(awaitAirQuality(t, a)["air_quality"])
			saved, err := state.Read("air-quality.json", airquality.MaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "regression":
				if aq["us_aqi"] != 42.0 || aq["error"] != "fetch_failed" || !reflect.DeepEqual(saved, old) {
					t.Fatal("older valid time replaced last good AQ", aq)
				}
			case "same-old-time":
				if aq["us_aqi"] != 99.0 || aq["freshness"] != "stale" || aq["age_seconds"] != 14400.0 {
					t.Fatal("fetch time falsely refreshed old model value", aq)
				}
			case "future-cache":
				if aq["us_aqi"] != 99.0 || aq["freshness"] != "fresh" || aq["error"] != nil {
					t.Fatal("valid fetch could not replace future-invalid cache", aq)
				}
			}
		})
	}
}

func TestAirQualityDisplayTimesUseSelectedZoneAcrossDST(t *testing.T) {
	valid := time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC)
	fetched := valid.Add(time.Hour)
	for _, tc := range []struct{ zone, valid, fetched string }{
		{"America/New_York", "2026-11-01 01:30 EDT (-04:00)", "2026-11-01 01:30 EST (-05:00)"},
		{"Asia/Tokyo", "2026-11-01 14:30 JST (+09:00)", "2026-11-01 15:30 JST (+09:00)"},
		{"UTC", "2026-11-01 05:30 UTC (+00:00)", "2026-11-01 06:30 UTC (+00:00)"},
	} {
		t.Run(tc.zone, func(t *testing.T) {
			location := weather.DefaultLocation()
			location["timezone"] = tc.zone
			a := &App{forecastPoint: &forecastPoint{location: location}, options: Options{Now: func() time.Time { return fetched }}}
			a.aq.record = airQualityFixture(location, fetched, valid)
			aq := a.airQualitySnapshot()
			if aq["valid_label"] != tc.valid || aq["fetched_label"] != tc.fetched {
				t.Fatal("AQ display used host timezone or lost repeated-hour offset", aq)
			}
		})
	}
}
