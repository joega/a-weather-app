package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
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
