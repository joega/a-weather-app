package main

import (
	"context"
	"os"
	"testing"

	"github.com/joega/a-weather-app/internal/app"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestPreparedCLIZIPRetainsCountryWithoutSecondLookup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := safeio.OpenDir(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	resolved := false
	resolve := func(ctx context.Context, selection M) (M, error) {
		if selection["mode"] != "zip" || selection["zip_code"] != "02108" {
			t.Fatalf("unexpected CLI selection: %v", selection)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("CLI resolution lacks its request deadline")
		}
		resolved = true
		return weather.ParseZIP(M{"results": []any{M{"name": "Boston", "admin1": "Massachusetts", "country_code": "US", "latitude": 42.36, "longitude": -71.05, "timezone": "America/New_York", "postcodes": []any{"02108"}}}}, "02108")
	}
	if err = prepareLocationWithResolver(state, "02108", "", resolve); err != nil {
		t.Fatal(err)
	}
	if !resolved {
		t.Fatal("CLI skipped ZIP resolution")
	}
	a, err := app.New(state, app.Options{Offline: true, ResolveSelection: func(context.Context, M) (M, error) {
		t.Fatal("repeated geocoding after guardian restart")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	snap := a.Snapshot()
	settings := snap["location_settings"].(M)
	alerts := snap["alerts"].(M)
	if settings["mode"] != "zip" || settings["zip_code"] != "02108" || settings["country_code"] != "US" || alerts["coverage"] != "US" {
		t.Fatal("CLI discarded verified country", settings, alerts)
	}
}
