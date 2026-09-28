package app

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/weather"
)

// Public-center provider checks are opt-in; normal CI uses deterministic fixtures.
func TestLiveAirQuality(t *testing.T) {
	if os.Getenv("A_WEATHER_APP_LIVE_AIR_QUALITY") != "1" {
		t.Skip("set A_WEATHER_APP_LIVE_AIR_QUALITY=1 for public-provider validation")
	}
	for _, city := range []struct {
		name, zone string
		lat, lon   float64
	}{
		{"Berlin", "Europe/Berlin", 52.52, 13.405},
		{"São Paulo", "America/Sao_Paulo", -23.5505, -46.6333},
		{"Tokyo", "Asia/Tokyo", 35.6762, 139.6503},
		{"Nairobi", "Africa/Nairobi", -1.2921, 36.8219},
		{"Sydney", "Australia/Sydney", -33.8688, 151.2093},
		{"Toronto", "America/Toronto", 43.6532, -79.3832},
	} {
		t.Run(city.name, func(t *testing.T) {
			location := M{"name": city.name, "latitude": city.lat, "longitude": city.lon, "timezone": city.zone}
			now := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			record, err := airquality.Fetch(ctx, location, now)
			if err != nil {
				t.Fatal(err)
			}
			if err = airquality.ValidateRecord(record, location); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"us_aqi", "european_aqi", "pm2_5_ug_m3"} {
				if record[key] == nil {
					t.Fatalf("live %s missing %s", city.name, key)
				}
			}
			raw, _ := json.Marshal(record)
			t.Logf("CAMS global: US AQI=%v, European AQI=%v, PM2.5=%v µg/m³, valid=%v, normalized cache=%d bytes", record["us_aqi"], record["european_aqi"], record["pm2_5_ug_m3"], record["valid_at"], len(raw))
			state := testState(t)
			forecast := metricFixture(now)
			forecast["location"] = location
			for name, value := range map[string]M{"location.json": location, "forecast.json": forecast, "air-quality.json": record} {
				if err = state.Write(name, value, weather.MaxBytes); err != nil {
					t.Fatal(err)
				}
			}
			offline, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer offline.Close(context.Background())
			s := offline.Snapshot()
			aq := object(s["air_quality"])
			if aq["freshness"] != "fresh" || aq["offline"] != true || object(s["current"])["temperature_c"] != 15.0 {
				t.Fatal("offline AQ/core fixture failed")
			}
			for _, key := range []string{"us_aqi", "european_aqi", "pm2_5_ug_m3"} {
				if aq[key] != record[key] {
					t.Fatal("offline AQ changed", key)
				}
			}
		})
	}
}
