package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func metricFixture(now time.Time) M {
	f := appFixture(now)
	current := object(f["current"])
	current["uv_index"], current["pressure_msl_hpa"], current["dew_point_c"] = 0.0, 1013.25, -2.5
	hour := safeio.Clone(current)
	hour["time"], hour["uv_index"] = now.Add(3*time.Hour).Format(time.RFC3339), 6.5
	f["hourly"] = []any{hour}
	return f
}

func TestPointMetricsSavedUpgradeOfflineAndFailedRefresh(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	legacy := M{"schema_version": 1.0, "mode": "zip", "zip_code": "10001", "location": weather.DefaultLocation(), "forecast": appFixture(now)}
	controls := DefaultControls()
	controls["units"], controls["reduced_motion"] = "C", true
	for name, value := range map[string]M{"location-profile.json": legacy, "controls.json": controls} {
		if err := state.Write(name, value, weather.MaxBytes); err != nil {
			t.Fatal(err)
		}
	}
	fail := false
	a, err := New(state, Options{Now: func() time.Time { return now }, FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		if fail {
			return nil, errors.New("provider unavailable")
		}
		return metricFixture(now), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	old := object(a.Snapshot()["current"])
	for _, field := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
		if value, present := old[field]; !present || value != nil {
			t.Fatalf("legacy %s must normalize to null", field)
		}
	}
	a.Handle(context.Background(), request("refresh", nil))
	awaitCompletion(t, a)
	saved, err := state.Read("location-profile.json", weather.MaxBytes)
	if err != nil || ValidateProfile(saved) != nil || saved["schema_version"] != 2.0 {
		t.Fatal("metric refresh did not save a valid upgraded profile", err)
	}
	backup, err := state.Read("location-profile-v1.json", weather.MaxBytes)
	if err != nil || !reflect.DeepEqual(backup, legacy) {
		t.Fatal("legacy rollback changed", err)
	}
	for _, field := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
		if object(object(saved["forecast"])["current"])[field] != object(metricFixture(now)["current"])[field] {
			t.Fatalf("saved metric lost: %s", field)
		}
	}
	offline, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		t.Error("offline reopen fetched provider data")
		return nil, errors.New("offline")
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer offline.Close(context.Background())
	s := offline.Snapshot()
	if object(s["current"])["uv_index"] != 0.0 || object(s["current"])["pressure_msl_hpa"] != 1013.25 || object(s["current"])["dew_point_c"] != -2.5 || object(s["hourly"].([]any)[0])["uv_index"] != 6.5 {
		t.Fatal("offline metrics or valid times lost")
	}
	if !reflect.DeepEqual(object(s["controls"]), controls) {
		t.Fatal("metrics changed saved preferences")
	}
	fail = true
	a.Handle(context.Background(), request("refresh", nil))
	awaitCompletion(t, a)
	after, err := state.Read("location-profile.json", weather.MaxBytes)
	if err != nil || !reflect.DeepEqual(saved, after) || object(a.Snapshot()["current"])["pressure_msl_hpa"] != 1013.25 {
		t.Fatal("failed refresh erased last-good metrics", err)
	}
}

func TestPointMetricsFollowForecastFreshnessAndTime(t *testing.T) {
	fetched := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	f := metricFixture(fetched)
	for _, tc := range []struct {
		age   time.Duration
		state string
	}{{0, "fresh"}, {46 * time.Minute, "stale"}, {121 * time.Minute, "expired"}} {
		t.Run(tc.state, func(t *testing.T) {
			now := fetched.Add(tc.age)
			a := &App{options: Options{Now: func() time.Time { return now }}, location: weather.DefaultLocation(), forecast: f, controls: DefaultControls(), mode: "default", country: "US", launcherStatus: "ready"}
			s := a.snapshot()
			if object(s["source"])["freshness"] != tc.state || object(s["current"])["time"] != fetched.Format(time.RFC3339) || object(s["current"])["uv_index"] != 0.0 {
				t.Fatal("current metric timestamp/freshness changed or future UV borrowed")
			}
			hour := object(s["hourly"].([]any)[0])
			if hour["time"] != fetched.Add(3*time.Hour).Format(time.RFC3339) || hour["uv_index"] != 6.5 {
				t.Fatal("hourly metric timestamp changed")
			}
		})
	}
}
