package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestStoppedEffectsDoNotPrepareUnusedWeather(t *testing.T) {
	fx := &coordinatedEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	a.forecast = appFixture(a.options.Now())
	for i := 0; i < 10; i++ {
		a.Tick(context.Background())
	}
	reply, _ := a.Handle(context.Background(), request("set_controls", M{
		"controls": M{"mode": "manual", "manual": M{"condition": "snow"}},
	}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.fx.mu.RLock()
	prepared := a.fx.weather != nil
	a.fx.mu.RUnlock()
	if prepared {
		t.Fatal("stopped effects prepared unused weather")
	}
	reply, _ = a.Handle(context.Background(), request("start_effects", M{"duration": 2.0}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.fx.mu.RLock()
	mode := a.fx.controls["mode"]
	condition := object(a.fx.controls["manual"])["condition"]
	prepared = a.fx.weather != nil
	a.fx.mu.RUnlock()
	if !prepared || mode != "manual" || condition != "snow" {
		t.Fatal("explicit start did not receive current controls", prepared, mode, condition)
	}
}

func TestCachedSnapshotReadersCannotMutateEachOther(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	a.forecast = appFixture(a.options.Now())
	a.forecast["hourly"] = []any{M{
		"time":      a.options.Now().Add(time.Hour).Format(time.RFC3339),
		"condition": "rain", "is_day": true,
	}}
	first := a.Snapshot()
	object(first["controls"])["units"] = "C"
	object(first["hourly"].([]any)[0])["condition"] = "snow"
	a.mu.Lock()
	defer a.mu.Unlock()
	cached := a.Snapshot()
	if object(cached["controls"])["units"] != "F" || object(cached["hourly"].([]any)[0])["condition"] != "rain" {
		t.Fatal("foreground caller changed cached snapshot")
	}
	object(cached["controls"])["units"] = "C"
	object(cached["hourly"].([]any)[0])["condition"] = "snow"
	next := a.Snapshot()
	if object(next["controls"])["units"] != "F" || object(next["hourly"].([]any)[0])["condition"] != "rain" {
		t.Fatal("cached readers share mutable state")
	}
}

// Synthetic full-sized forecasts keep idle regressions measurable without
// copying a user's location or cache into the test suite.
func benchmarkIdleApp(b *testing.B) *App {
	b.Helper()
	root := b.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		b.Fatal(err)
	}
	state, err := safeio.OpenDir(root, false)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { state.Close() })
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	forecast := appFixture(now)
	hours := []any{}
	for i := 0; i < 240; i++ {
		row := M{"time": now.Add(time.Duration(i+1) * time.Hour).Format(time.RFC3339), "condition": "clear", "is_day": true}
		for key := range weather.WeatherBounds {
			row[key] = 0.5
		}
		for key, bounds := range weather.OptionalWeatherBounds {
			row[key] = bounds[0] + 0.5
		}
		hours = append(hours, row)
	}
	forecast["hourly"] = hours
	for key, bounds := range weather.OptionalWeatherBounds {
		object(forecast["current"])[key] = bounds[0] + 0.5
	}
	watching := notifications.DefaultDocument()
	object(watching["settings"])["enabled"] = true
	watching["snoozed_until"] = float64(now.Add(24 * time.Hour).Unix())
	for name, value := range map[string]M{"location.json": weather.DefaultLocation(), "forecast.json": forecast, "notifications.json": watching} {
		if err = state.Write(name, value, weather.MaxBytes); err != nil {
			b.Fatal(err)
		}
	}
	a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, Effects: &coordinatedEffects{}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { a.Close(context.Background()) })
	return a
}

func BenchmarkIdleNotificationTick(b *testing.B) {
	a := benchmarkIdleApp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Tick(context.Background())
	}
}

func BenchmarkFullForecastSnapshot(b *testing.B) {
	a := benchmarkIdleApp(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(a.Snapshot()); err != nil {
			b.Fatal(err)
		}
	}
}
