package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

func TestBarFollowsSavedTemperatureUnits(t *testing.T) {
	for _, tc := range []struct {
		name       string
		celsius    float64
		fahrenheit string
		metric     string
		age        time.Duration
	}{
		{"reported_60F_16C", 15.5, "60", "16", 0},
		{"fahrenheit_half_degree", 2.5, "37", "3", 0},
		{"below_zero", -1.5, "29", "-1", 0},
		{"negative_half_degree", -0.5, "31", "0", 0},
		{"stale_forecast", 15.5, "60", "16", time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testState(t)
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			forecast := appFixture(now.Add(-tc.age))
			object(forecast["current"])["temperature_c"] = tc.celsius
			if err := d.Write("forecast.json", forecast, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			a, err := New(d, Options{Offline: true, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			for _, units := range []string{"F", "C", "F", "C"} {
				reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"units": units}}))
				if reply["ok"] != true || object(object(reply["snapshot"])["controls"])["units"] != units {
					t.Fatal("app did not save selected units", reply)
				}
				degrees := tc.fahrenheit
				if units == "C" {
					degrees = tc.metric
				}
				if bar := Bar(d, now); !strings.HasPrefix(stringOf(bar["label"]), degrees+"° · Clear") {
					t.Fatalf("app selected %s, bar = %v; want %s°", units, bar, degrees)
				}
			}
			reopened, err := New(d, Options{Offline: true, Now: a.options.Now})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close(context.Background())
			if reopened.controls["units"] != "C" || !strings.HasPrefix(stringOf(Bar(d, now)["label"]), tc.metric+"°") {
				t.Fatal("saved units were not retained")
			}
		})
	}
}

func TestBarSavedControlsValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		controls M
		label    string
	}{
		{"missing_defaults_to_F", nil, "59° · Clear"},
		{"empty_defaults_to_F", M{}, "59° · Clear"},
		{"partial_C", M{"units": "C"}, "15° · Clear"},
		{"invalid_units", M{"units": "K"}, "--° · Unavailable"},
		{"invalid_controls", M{"units": "C", "fps": true}, "--° · Unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testState(t)
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			if err := d.Write("forecast.json", appFixture(now), weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			if tc.controls != nil {
				if err := d.Write("controls.json", tc.controls, 8192); err != nil {
					t.Fatal(err)
				}
			}
			if bar := Bar(d, now); bar["label"] != tc.label {
				t.Fatalf("bar = %v; want %s", bar, tc.label)
			}
			if saved, err := d.Read("controls.json", 8192); err != nil || (tc.controls == nil && saved != nil) {
				t.Fatal("bar created missing controls", err)
			}
		})
	}
}

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

func TestBarCoverageDoesNotTrustLegacyAllClear(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, country := range []any{nil, "DE", "US"} {
		t.Run(stringOf(country), func(t *testing.T) {
			d := testState(t)
			f := appFixture(now)
			profile := M{"schema_version": 2.0, "mode": "custom", "zip_code": nil, "country_code": country, "place": nil, "location": weather.DefaultLocation(), "forecast": f}
			if err := d.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			expected := "Alerts unavailable"
			if country == "DE" {
				expected = "Alerts not supported here"
			}
			if country == "US" {
				expected = "No active alerts in cached feed"
			}
			if Bar(d, now)["tooltip"] != "New York, NY · Clear · "+expected {
				t.Fatal("bar misrepresented country coverage", Bar(d, now))
			}
		})
	}
}
