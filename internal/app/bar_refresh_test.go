package app

import (
	"context"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

func TestBarRefreshRequiresSavedLocationAndThrottles(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	calls := 0
	options := Options{Now: func() time.Time { return now }, Fetch: func(_ context.Context, location M, at time.Time) (M, error) {
		calls++
		forecast := appFixture(at)
		forecast["location"] = location
		return forecast, nil
	}}
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || calls != 0 {
		t.Fatalf("fresh install fetched weather: %v, calls=%d", err, calls)
	}
	if err := state.Write("location.json", weather.DefaultLocation(), 8192); err != nil {
		t.Fatal(err)
	}
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || calls != 1 {
		t.Fatalf("saved location did not refresh: %v, calls=%d", err, calls)
	}
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || calls != 1 {
		t.Fatalf("fresh saved weather fetched again: %v, calls=%d", err, calls)
	}
	if got := Bar(state, now)["freshness"]; got != "fresh" {
		t.Fatalf("bar did not see refreshed weather: %v", got)
	}
	now = now.Add(16 * time.Minute)
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || calls != 2 {
		t.Fatalf("stale saved location did not refresh: %v, calls=%d", err, calls)
	}
}

func TestBarRefreshOnlyResolvesOptedInAutoLocation(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	state := testState(t)
	profile := M{"schema_version": 1.0, "mode": "auto", "zip_code": nil, "location": weather.DefaultLocation(), "forecast": appFixture(now.Add(-time.Hour))}
	if err := state.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	resolves := 0
	options := Options{Now: func() time.Time { return now }, Resolve: func(_ context.Context, selection M) (M, error) {
		resolves++
		if len(selection) != 1 || selection["mode"] != "auto" {
			t.Fatalf("unexpected location request: %v", selection)
		}
		return weather.DefaultLocation(), nil
	}, Fetch: func(_ context.Context, location M, at time.Time) (M, error) {
		forecast := appFixture(at)
		forecast["location"] = location
		return forecast, nil
	}}
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || resolves != 1 {
		t.Fatalf("opted-in location did not resolve: %v, calls=%d", err, resolves)
	}
	if err := RefreshBarSaved(context.Background(), state, options); err != nil || resolves != 1 {
		t.Fatalf("fresh auto location resolved again: %v, calls=%d", err, resolves)
	}
}
