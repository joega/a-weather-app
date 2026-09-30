package app

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestAutomaticUnitsUseSavedCountryAndPreserveLegacyChoices(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		country any
		saved   M
		units   string
		mode    string
	}{
		{"US_default", "US", nil, "F", "auto"},
		{"UK_default", "GB", nil, "C", "auto"},
		{"Germany_default", "DE", nil, "C", "auto"},
		{"unknown_legacy_country", nil, nil, "F", "auto"},
		{"automatic_saved_units_are_derived", "GB", M{"units": "F", "units_mode": "auto"}, "C", "auto"},
		{"legacy_F_is_preserved", "GB", M{"units": "F"}, "F", "manual"},
		{"legacy_C_is_preserved", "US", M{"units": "C"}, "C", "manual"},
		{"other_controls_keep_automatic_units", "GB", M{"reduced_motion": true}, "C", "auto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := testState(t)
			forecast := appFixture(now)
			profile := M{"schema_version": 2.0, "mode": "custom", "zip_code": nil, "country_code": tc.country, "place": nil, "location": forecast["location"], "forecast": forecast}
			if err := d.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			if tc.saved != nil {
				if err := d.Write("controls.json", tc.saved, 8192); err != nil {
					t.Fatal(err)
				}
			}
			a, err := New(d, Options{Offline: true, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			if a.controls["units"] != tc.units || a.controls["units_mode"] != tc.mode {
				t.Fatal("unexpected app units", a.controls)
			}
			degrees := "59"
			if tc.units == "C" {
				degrees = "15"
			}
			if bar := Bar(d, now); !strings.HasPrefix(stringOf(bar["label"]), degrees+"° · Clear") {
				t.Fatal("bar units differ from app", bar)
			}
			saved, err := d.Read("controls.json", 8192)
			if err != nil || !reflect.DeepEqual(saved, tc.saved) {
				t.Fatal("reading units rewrote saved preferences", err, saved)
			}
		})
	}
}

func TestAutomaticUnitsFollowSuccessfulLocationChanges(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, Options{
		Now: func() time.Time { return now },
		ResolveSelection: func(_ context.Context, selection M) (M, error) {
			country, location := "US", weather.DefaultLocation()
			if selection["mode"] == "auto" {
				country = "GB"
				location = M{"name": "London, England, United Kingdom", "latitude": 51.5085, "longitude": -0.1257, "timezone": "Europe/London"}
			}
			return M{"location": location, "country_code": country}, nil
		},
		FetchCountry: func(_ context.Context, location M, _ time.Time, _ string) (M, error) {
			forecast := appFixture(now)
			forecast["location"] = location
			return forecast, nil
		},
	})
	check := func(units, mode string) {
		t.Helper()
		controls := object(a.Snapshot()["controls"])
		if controls["units"] != units || controls["units_mode"] != mode {
			t.Fatal("unexpected units after location change", controls)
		}
		degrees := "59"
		if units == "C" {
			degrees = "15"
		}
		if bar := Bar(a.state, now); !strings.HasPrefix(stringOf(bar["label"]), degrees+"° · Clear") {
			t.Fatal("bar and app disagree", bar, controls)
		}
	}
	selectLocation := func(location M) {
		t.Helper()
		reply, _ := a.Handle(context.Background(), request("set_location", M{"location": location}))
		if reply["ok"] != true {
			t.Fatal(reply)
		}
		awaitCompletion(t, a)
	}
	patch := func(controls M) {
		t.Helper()
		reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": controls}))
		if reply["ok"] != true {
			t.Fatal(reply)
		}
	}
	selectLocation(M{"mode": "auto"})
	check("C", "auto")
	patch(M{"reduced_motion": true})
	check("C", "auto")
	selectLocation(M{"mode": "zip", "zip_code": "10001"})
	check("F", "auto")
	patch(M{"units": "C"})
	selectLocation(M{"mode": "zip", "zip_code": "10002"})
	check("C", "manual")
	patch(M{"units": "F"})
	selectLocation(M{"mode": "auto"})
	check("F", "manual")
	patch(M{"units_mode": "auto"})
	check("C", "auto")
	reopened, err := New(a.state, Options{Offline: true, Now: a.options.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close(context.Background())
	if reopened.controls["units"] != "C" || reopened.controls["units_mode"] != "auto" {
		t.Fatal("automatic units were not restored", reopened.controls)
	}
	// A failed location operation must retain both the location and its units.
	before := safeio.Clone(a.controls)
	a.generation++
	a.results <- completion{generation: a.generation, selection: M{"mode": "zip", "zip_code": "10001"}, err: context.DeadlineExceeded}
	a.poll()
	if !reflect.DeepEqual(a.controls, before) || a.country != "GB" {
		t.Fatal("failed location change altered units")
	}
}

func TestWindUnitPreferencesPersistIndependently(t *testing.T) {
	for _, unit := range []string{"auto", "mph", "km/h", "m/s", "kn"} {
		t.Run(unit, func(t *testing.T) {
			a := newTestApp(t, Options{})
			reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"wind_units": unit}}))
			if reply["ok"] != true || a.controls["wind_units"] != unit || a.controls["units_mode"] != "auto" {
				t.Fatal("wind preference changed temperature mode", reply, a.controls)
			}
			for _, country := range []string{"US", "GB", "JP"} {
				controls, err := readControls(a.state, country)
				if err != nil || controls["wind_units"] != unit {
					t.Fatal("saved wind preference lost", country, controls, err)
				}
			}
			controls, err := PatchControls(a.controls, M{"units": "C"})
			if err != nil || controls["wind_units"] != unit {
				t.Fatal("temperature change lost wind preference", controls, err)
			}
		})
	}
	for _, invalid := range []any{"kph", "bogus", true, nil, 3.6} {
		if _, err := PatchControls(DefaultControls(), M{"wind_units": invalid}); err == nil {
			t.Fatal("accepted invalid wind units", invalid)
		}
	}
}
