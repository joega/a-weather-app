package app

import (
	"github.com/joega/a-weather-app/internal/weather"
	"testing"
	"time"
)

func TestBarFreshnessAndAlerts(t *testing.T) {
	d := testState(t)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	f := appFixture(now)
	object(f["alerts"])["items"] = []any{M{"event": "<Rain>", "effective": "2026-09-27T11:00:00Z", "expires": "2026-09-27T14:00:00Z"}}
	if e := d.Write("forecast.json", f, weather.MaxBytes); e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		delta        time.Duration
		fresh, label string
	}{{0, "fresh", "59° · Clear · 1 alert"}, {2701 * time.Second, "stale", "59° · Clear · Stale"}, {7201 * time.Second, "expired", "--° · Unavailable"}, {-301 * time.Second, "invalid_future", "--° · Unavailable"}} {
		s := Bar(d, now.Add(c.delta))
		if s["freshness"] != c.fresh || s["label"] != c.label {
			t.Fatalf("%v: %v", c.delta, s)
		}
	}
	s := Bar(d, now)
	if s["tooltip"] != "New York, NY · Clear · Rain" {
		t.Fatal(s)
	}
	if _, e := d.Read("controls.json", 8192); e != nil {
		t.Fatal(e)
	}
}
func TestBarProfileAndLegacyBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	d := testState(t)
	if Bar(d, now)["freshness"] != "unavailable" {
		t.Fatal("empty state")
	}
	f := appFixture(now)
	location := M{"name": "Boston, MA", "latitude": 42.36, "longitude": -71.05, "timezone": "America/New_York"}
	f["location"] = location
	if e := d.Write("forecast.json", f, weather.MaxBytes); e != nil {
		t.Fatal(e)
	}
	if Bar(d, now)["label"] != "--° · Unavailable" {
		t.Fatal("developer legacy location cache exposed")
	}
	profile := M{"schema_version": 1.0, "mode": "zip", "zip_code": "02108", "location": location, "forecast": f}
	if e := d.Write("location-profile.json", profile, weather.MaxBytes); e != nil {
		t.Fatal(e)
	}
	if Bar(d, now)["tooltip"] != "Boston, MA · Clear · No active alerts in cached feed" {
		t.Fatal(Bar(d, now))
	}
	object(f["current"])["temperature_c"] = true
	if e := d.Write("location-profile.json", profile, weather.MaxBytes); e != nil {
		t.Fatal(e)
	}
	if Bar(d, now)["freshness"] != "unavailable" {
		t.Fatal("invalid profile trusted")
	}
	if Bar(nil, now)["label"] != "--° · Unavailable" {
		t.Fatal("nil state")
	}
}
