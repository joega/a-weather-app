package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestPrivateFixtureFreshnessBothClocksAndNoMutation(t *testing.T) {
	source := map[string]any{"fetched_at": "old fetched", "current": map[string]any{"time": "old current", "temperature_c": 15.0}, "hourly": []any{map[string]any{"time": "original hour", "condition": "rain"}}}
	original := safeio.Clone(source)
	now := time.Date(2026, 9, 27, 17, 5, 6, 123456789, time.FixedZone("test", -4*3600))
	prepared, err := freshBenchmarkForecast(source, now)
	if err != nil {
		t.Fatal(err)
	}
	stamp := "2026-09-27T21:05:06.123456Z"
	if prepared["fetched_at"] != stamp || prepared["current"].(map[string]any)["time"] != stamp {
		t.Fatal("clocks not refreshed together", prepared)
	}
	if !reflect.DeepEqual(source, original) {
		t.Fatal("mutated original saved forecast")
	}
	if !reflect.DeepEqual(prepared["hourly"], source["hourly"]) || prepared["current"].(map[string]any)["temperature_c"] != 15.0 {
		t.Fatal("changed weather values")
	}
	prepared["hourly"].([]any)[0].(map[string]any)["condition"] = "snow"
	if !reflect.DeepEqual(source, original) {
		t.Fatal("private fixture aliases source")
	}
	if _, err = freshBenchmarkForecast(map[string]any{}, now); err == nil {
		t.Fatal("accepted missing current record")
	}
}

func TestBenchmarkPreparationLeavesSavedDocumentsUntouched(t *testing.T) {
	for _, effects := range []bool{false, true} {
		name := "live"
		if effects {
			name = "finite-effects"
		}
		t.Run(name, func(t *testing.T) {
			saved := t.TempDir()
			if err := os.Chmod(saved, 0700); err != nil {
				t.Fatal(err)
			}
			stamp := "2026-09-27T12:00:00Z"
			location := weather.DefaultLocation()
			profile := map[string]any{
				"schema_version": 1.0, "mode": "custom", "zip_code": nil, "location": location,
				"forecast": map[string]any{"schema_version": 1.0, "location": location, "fetched_at": stamp, "current": map[string]any{"time": stamp, "temperature_c": 15.0, "condition": "clear", "is_day": true}, "hourly": []any{}, "daily": []any{}, "alerts": weather.UnavailableAlerts()},
			}
			controls := map[string]any{"mode": "manual", "reduced_motion": false, "manual": map[string]any{"condition": "snow"}}
			originals := map[string][]byte{}
			for file, document := range map[string]any{"location-profile.json": profile, "controls.json": controls} {
				path := filepath.Join(saved, file)
				if err := write(path, document); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				originals[file] = raw
			}
			location, forecast, preparedControls, err := benchmarkDocuments(saved, true, effects)
			if err != nil {
				t.Fatal(err)
			}
			private := filepath.Join(t.TempDir(), "private-state")
			if err := prepareBenchmarkState(private, location, forecast, preparedControls, true); err != nil {
				t.Fatal(err)
			}
			state, err := safeio.OpenDir(private, false)
			if err != nil {
				t.Fatal(err)
			}
			defer state.Close()
			actual, err := state.Read("controls.json", 8192)
			if err != nil {
				t.Fatal(err)
			}
			mode := "live"
			if effects {
				mode = "manual"
				if actual["manual"].(map[string]any)["condition"] != "rain" {
					t.Fatalf("finite fixture controls = %v; want manual rain", actual)
				}
			}
			if actual["mode"] != mode || actual["reduced_motion"] != true {
				t.Fatalf("fixture controls = %v; want mode %s and reduced motion", actual, mode)
			}
			notifications, err := state.Read("notifications.json", 16384)
			if err != nil {
				t.Fatal(err)
			}
			if notifications["settings"].(map[string]any)["enabled"] != true || notifications["snoozed_until"].(float64) <= float64(time.Now().Unix()) {
				t.Fatalf("hidden fixture notifications are not enabled and snoozed: %v", notifications)
			}
			if forecast["fetched_at"] != stamp || forecast["current"].(map[string]any)["time"] != stamp {
				t.Fatal("preparation mutated the source forecast")
			}
			for file, original := range originals {
				current, err := os.ReadFile(filepath.Join(saved, file))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(current, original) {
					t.Fatalf("benchmark modified saved %s", file)
				}
			}
		})
	}
}
