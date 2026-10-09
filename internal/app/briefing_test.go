package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func briefingFixture(t *testing.T, now time.Time, zoneName string) *App {
	t.Helper()
	zone, err := time.LoadLocation(zoneName)
	if err != nil {
		t.Fatal(err)
	}
	location := weather.DefaultLocation()
	location["timezone"] = zoneName
	forecast := appFixture(now)
	forecast["location"] = location
	hours := []any{}
	local := now.In(zone)
	start := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, zone)
	for i := 0; i < 60; i++ {
		hours = append(hours, M{"time": start.Add(time.Duration(i) * time.Hour).UTC().Format(time.RFC3339), "condition": "clear", "is_day": true, "temperature_c": 20.0, "precipitation_probability": 0.1, "wind_gust_m_s": 10.0})
	}
	forecast["hourly"] = hours
	return &App{options: Options{Now: func() time.Time { return now }}, forecastPoint: &forecastPoint{location: location, forecast: forecast, mode: "default", country: "US"}, controls: DefaultControls(), launcherStatus: "ready"}
}

func TestBriefingPeriodsAndHourlyPrecipitation(t *testing.T) {
	now := time.Date(2026, 10, 8, 16, 30, 0, 123456789, time.UTC)
	a := briefingFixture(t, now, "America/New_York")
	hours := a.forecast["hourly"].([]any)
	// The noon period is already over and must not influence the remaining day.
	object(hours[0])["precipitation_probability"] = 1.0
	object(hours[0])["temperature_c"] = 99.0
	object(hours[3])["precipitation_probability"] = 0.8
	before := safeio.Clone(a.forecast)
	rows := a.Snapshot()["briefing"].([]any)
	if len(rows) != 3 {
		t.Fatal(rows)
	}
	day := object(rows[0])
	if day["start"] != "2026-10-08T16:30:00Z" || day["period"] != "today" || day["range_label"] != "12:30 PM EDT – 6:00 PM EDT" || day["high_c"] != 20.0 || day["peak_probability"] != 0.8 || day["peak_label"] != "2:00 PM EDT – 3:00 PM EDT" {
		t.Fatal(day)
	}
	for _, value := range rows {
		row := object(value)
		if row["temperature_complete"] != true || row["precipitation_complete"] != true || row["wind_complete"] != true {
			t.Fatal("complete hourly series marked partial", row)
		}
	}
	if !reflect.DeepEqual(a.forecast, before) {
		t.Fatal("briefing mutated forecast")
	}
	day["high_c"] = 80.0
	if object(a.Snapshot()["briefing"].([]any)[0])["high_c"] != 20.0 {
		t.Fatal("public snapshot exposed cached briefing")
	}
}

func TestBriefingIncompleteCoverage(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 30, 0, 0, time.UTC)
	for _, missing := range []string{"temperature_c", "precipitation_probability", "wind_gust_m_s", "hour"} {
		t.Run(missing, func(t *testing.T) {
			a := briefingFixture(t, now, "UTC")
			hours := a.forecast["hourly"].([]any)
			if missing == "hour" {
				a.forecast["hourly"] = append(hours[:2], hours[3:]...)
			} else {
				object(hours[2])[missing] = nil
			}
			row := object(a.Snapshot()["briefing"].([]any)[0])
			for field, complete := range map[string]string{"temperature_c": "temperature_complete", "precipitation_probability": "precipitation_complete", "wind_gust_m_s": "wind_complete"} {
				if row[complete] != (missing != field && missing != "hour") {
					t.Fatal("missing field/interval coverage misrepresented", missing, row)
				}
			}
		})
	}
}

func TestBriefingLocalPeriodsAcrossDST(t *testing.T) {
	for _, tc := range []struct {
		name, now string
		hours     time.Duration
	}{
		{"spring", "2026-03-07T23:30:00Z", 10*time.Hour + 30*time.Minute},
		{"fall", "2026-10-31T22:30:00Z", 12*time.Hour + 30*time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.now)
			a := briefingFixture(t, now, "America/New_York")
			rows := a.Snapshot()["briefing"].([]any)
			if len(rows) != 2 || object(rows[0])["period"] != "tonight" || object(rows[1])["period"] != "tomorrow" {
				t.Fatal(rows)
			}
			row := object(rows[0])
			start, _ := weather.Instant(row["start"])
			end, _ := weather.Instant(row["end"])
			if end.Sub(start) != tc.hours || row["precipitation_complete"] != true {
				t.Fatal("local night treated as fixed duration", row, end.Sub(start))
			}
		})
	}
}

func TestBriefingCacheFreshnessAndBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 8, 17, 30, 0, 0, time.UTC)
	a := briefingFixture(t, now, "UTC")
	a.options.Now = func() time.Time { return now }
	first := a.Snapshot()
	built := a.displayRows.builtAt
	now = now.Add(time.Minute)
	if !reflect.DeepEqual(first["briefing"], a.Snapshot()["briefing"]) || !a.displayRows.builtAt.Equal(built) {
		t.Fatal("briefing rebuilt without changed forecast or period")
	}
	now = now.Add(29 * time.Minute)
	if rows := a.Snapshot()["briefing"].([]any); len(rows) != 2 || object(rows[0])["period"] != "tonight" {
		t.Fatal("evening boundary did not invalidate briefing", rows)
	}
	now = now.Add(20 * time.Minute)
	stale := a.Snapshot()
	if object(stale["source"])["freshness"] != "stale" || len(stale["briefing"].([]any)) != 2 {
		t.Fatal("stale summary unavailable without expiry", stale["source"])
	}
	now = now.Add(2 * time.Hour)
	if len(a.Snapshot()["briefing"].([]any)) != 0 {
		t.Fatal("expired weather still offers planning summary")
	}
	now = now.Add(-4 * time.Hour)
	if len(a.Snapshot()["briefing"].([]any)) != 0 {
		t.Fatal("future-dated forecast offers planning summary")
	}
}

func TestBriefingEmptyForecastAndFractionalTimezone(t *testing.T) {
	now := time.Date(2026, 10, 8, 7, 0, 0, 0, time.UTC)
	a := briefingFixture(t, now, "Asia/Kathmandu")
	row := object(a.Snapshot()["briefing"].([]any)[0])
	if row["precipitation_complete"] != true {
		t.Fatal("fractional timezone lost hourly coverage", row)
	}
	a.forecast = safeio.Clone(a.forecast)
	a.forecast["hourly"] = []any{}
	if len(a.Snapshot()["briefing"].([]any)) != 0 {
		t.Fatal("missing hours created a summary")
	}
}
