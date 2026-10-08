package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestForecastPublicationIndependentOfAlerts(t *testing.T) {
	for _, alertsFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "forecast_first", true: "alerts_first"}[alertsFirst], func(t *testing.T) {
			forecastGate, alertGate := make(chan struct{}), make(chan struct{})
			a := newTestApp(t, Options{
				ResolveSelection: func(context.Context, M) (M, error) {
					return M{"location": weather.DefaultLocation(), "country_code": "US"}, nil
				},
				FetchCountry: func(ctx context.Context, l M, now time.Time, _ string) (M, error) {
					select {
					case <-forecastGate:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					f := appFixture(now)
					f["location"] = l
					f["alerts"] = weather.UnavailableAlerts()
					object(f["alerts"])["freshness"], object(f["alerts"])["refreshing"] = "pending", true
					return f, nil
				},
				FetchAlerts: func(ctx context.Context, _ M, now time.Time) (M, error) {
					select {
					case <-alertGate:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return M{"status": "available", "items": []any{}, "fetched_at": now.Format(time.RFC3339), "freshness": "current"}, nil
				},
			})
			a.beginFetch(M{"mode": "auto"})
			if alertsFirst {
				close(alertGate)
			}
			close(forecastGate)
			deadlineForecast := time.Now().Add(time.Second)
			for a.Snapshot()["current"] == nil {
				if time.Now().After(deadlineForecast) {
					t.Fatal("forecast not published")
				}
				time.Sleep(time.Millisecond)
			}
			if a.Snapshot()["current"] == nil {
				t.Fatal("forecast not published")
			}
			if !alertsFirst {
				alerts := object(a.Snapshot()["alerts"])
				if alerts["freshness"] != "pending" || alerts["status"] != "unavailable" {
					t.Fatal("pending feed misrepresented", alerts)
				}
				close(alertGate)
			}
			deadline := time.Now().Add(time.Second)
			for object(a.Snapshot()["alerts"])["freshness"] != "current" {
				if time.Now().After(deadline) {
					t.Fatal("alerts not published")
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
func TestAlertFailureRetainsHonestLocationBoundCache(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	cache := M{"status": "available", "items": []any{}, "fetched_at": now.Add(-time.Minute).Format(time.RFC3339)}
	for _, pending := range []bool{false, true} {
		got := mergeAlerts(weather.UnavailableAlerts(), cache, now, pending)
		if got["freshness"] != "stale" || got["refreshing"] != pending || got["fetched_at"] != cache["fetched_at"] {
			t.Fatal(got)
		}
	}
	if _, ok := cache["freshness"]; ok {
		t.Fatal("mutated cache")
	}
	for _, age := range []time.Duration{-time.Minute, (weather.StaleSeconds + 1) * time.Second} {
		old := safeio.Clone(cache)
		old["fetched_at"] = now.Add(-age).Format(time.RFC3339)
		if mergeAlerts(weather.UnavailableAlerts(), old, now, false)["status"] != "unavailable" {
			t.Fatal("invalid cache retained")
		}
	}
	f := appFixture(now)
	other := weather.DefaultLocation()
	other["latitude"] = 1.0
	if cachedAlerts(f, other) != nil {
		t.Fatal("cross-location alerts retained")
	}
}
func TestLateAlertGenerationAndShutdown(t *testing.T) {
	entered, canceled := make(chan struct{}), make(chan struct{})
	a := newTestApp(t, Options{
		ResolveSelection: func(context.Context, M) (M, error) {
			return M{"location": weather.DefaultLocation(), "country_code": "US"}, nil
		},
		FetchCountry: func(_ context.Context, l M, now time.Time, _ string) (M, error) {
			f := appFixture(now)
			f["location"] = l
			return f, nil
		},
		FetchAlerts: func(ctx context.Context, _ M, _ time.Time) (M, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return nil, errors.New("canceled")
		},
	})
	a.beginFetch(M{"mode": "auto"})
	<-entered
	awaitCompletion(t, a)
	generation := a.generation
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("alert worker not canceled")
	}
	// A second app provides a live newer generation; never reopen a closed App.
	newer := newTestApp(t, Options{Offline: true})
	newer.forecast = appFixture(newer.options.Now())
	newer.generation = generation + 1
	newer.forecastGeneration = generation + 1
	before := safeio.Clone(newer.forecast)
	newer.results <- completion{generation: generation, location: newer.location, alerts: weather.UnavailableAlerts()}
	newer.poll()
	if object(newer.forecast["alerts"])["status"] != object(before["alerts"])["status"] {
		t.Fatal("old alerts adopted")
	}

}
func TestVisualQualityPersistence(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	for _, quality := range []string{"auto", "full", "economical", "static"} {
		r, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"visual_quality": quality}}))
		if r["ok"] != true {
			t.Fatal(r)
		}
		saved, err := readControls(a.state, a.country)
		if err != nil || saved["visual_quality"] != quality {
			t.Fatal(saved, err)
		}
	}
	if _, err := PatchControls(a.controls, M{"visual_quality": "ultra"}); err == nil {
		t.Fatal("invalid quality accepted")
	}
}

func TestPendingCacheReopensAsUnavailable(t *testing.T) {
	state := testState(t)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	f := appFixture(now)
	f["alerts"] = weather.UnavailableAlerts()
	object(f["alerts"])["freshness"], object(f["alerts"])["refreshing"] = "pending", true
	if err := state.Write("forecast.json", f, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	if object(a.forecast["alerts"])["freshness"] != "unavailable" || object(a.forecast["alerts"])["refreshing"] != false {
		t.Fatal("persisted pending request survived restart")
	}
}

func TestRapidLocationChangeRejectsLateAlerts(t *testing.T) {
	started := make(chan M, 2)
	released := make(chan struct{})
	a := newTestApp(t, Options{
		ResolveSelection: func(_ context.Context, s M) (M, error) {
			l := weather.DefaultLocation()
			if s["mode"] == "zip" {
				l["latitude"], l["name"] = 40.0, "Other"
			}
			return M{"location": l, "country_code": "US"}, nil
		},
		FetchCountry: func(_ context.Context, l M, now time.Time, _ string) (M, error) {
			f := appFixture(now)
			f["location"] = l
			f["alerts"] = weather.UnavailableAlerts()
			object(f["alerts"])["freshness"], object(f["alerts"])["refreshing"] = "pending", true
			return f, nil
		},
		FetchAlerts: func(ctx context.Context, l M, now time.Time) (M, error) {
			started <- l
			if l["name"] == "Other" {
				select {
				case <-released:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return M{"status": "available", "items": []any{}, "fetched_at": now.Format(time.RFC3339), "freshness": "current"}, nil
		},
	})
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "zip", "zip_code": "02108"}}))
	<-started
	awaitCompletion(t, a)
	oldGeneration := a.generation
	a.Handle(context.Background(), request("set_location", M{"location": M{"mode": "auto"}}))
	<-started
	deadline := time.Now().Add(time.Second)
	for a.Snapshot()["location_settings"].(M)["busy"] == true || object(a.Snapshot()["alerts"])["freshness"] != "current" {
		if time.Now().After(deadline) {
			t.Fatal("new location did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	close(released)
	a.results <- completion{generation: oldGeneration, location: a.location, alerts: weather.UnavailableAlerts()}
	if object(a.Snapshot()["alerts"])["freshness"] != "current" || a.location["name"] == "Other" {
		t.Fatal("old location alerts overwritten current feed")
	}
}
