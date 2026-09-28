package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

// Explicit provider validation for the full production ten-day request. Uses
// public city centers only; deterministic fixtures cover nulls and failures.
func TestLivePointMetrics(t *testing.T) {
	if os.Getenv("A_WEATHER_APP_LIVE_METRICS") != "1" {
		t.Skip("set A_WEATHER_APP_LIVE_METRICS=1 for public-provider validation")
	}
	for _, city := range []struct {
		name, code, zone string
		lat, lon         float64
	}{
		{"Berlin", "DE", "Europe/Berlin", 52.52, 13.405},
		{"São Paulo", "BR", "America/Sao_Paulo", -23.5505, -46.6333},
		{"Tokyo", "JP", "Asia/Tokyo", 35.6762, 139.6503},
		{"Nairobi", "KE", "Africa/Nairobi", -1.2921, 36.8219},
		{"Sydney", "AU", "Australia/Sydney", -33.8688, 151.2093},
		{"Toronto", "CA", "America/Toronto", 43.6532, -79.3832},
	} {
		t.Run(city.code, func(t *testing.T) {
			location := M{"name": city.name, "latitude": city.lat, "longitude": city.lon, "timezone": city.zone}
			now := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			forecast, err := weather.FetchForCountry(ctx, location, now, city.code)
			if err != nil {
				t.Fatal(err)
			}
			if err := weather.ValidateSnapshot(forecast, location); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
				if object(forecast["current"])[field] == nil {
					t.Fatalf("%s current %s unavailable in live validation", city.name, field)
				}
				count := 0
				for _, row := range forecast["hourly"].([]any) {
					if object(row)[field] != nil {
						count++
					}
				}
				if count < 24 {
					t.Fatalf("%s %s has only %d populated hours", city.name, field, count)
				}
				t.Logf("%s: %s current=%v, valid hourly values=%d", city.name, field, object(forecast["current"])[field], count)
			}
			state := testState(t)
			profile := M{"schema_version": 2.0, "mode": "place", "zip_code": nil, "location": location, "forecast": forecast, "country_code": city.code, "place": M{"provider": "open-meteo", "id": 1.0}}
			if err := state.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			offline, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer offline.Close(context.Background())
			current := object(offline.Snapshot()["current"])
			for _, field := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
				if current[field] != object(forecast["current"])[field] {
					t.Fatalf("offline %s changed", field)
				}
			}
		})
	}
}
