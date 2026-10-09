package app

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
)

func astronomyFixture(t *testing.T) *App {
	t.Helper()
	a := briefingFixture(t, time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC), "America/New_York")
	a.mode, a.id = "custom", "new-york"
	a.location["latitude"], a.location["longitude"] = 40.7128, -74.006
	a.options.Offline = true
	return a
}

func astronomyQuery(a *App, date string) M {
	return M{"location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": a.location["timezone"], "date": date}
}

func TestAstronomyNarrowOfflineReplyWithoutForecastOrState(t *testing.T) {
	a := astronomyFixture(t)
	before := safeio.Clone(a.forecast)
	requestDay := func(date string) M {
		t.Helper()
		reply, stop := a.Handle(context.Background(), request("astronomy_day", M{"day": astronomyQuery(a, date)}))
		if stop || reply["ok"] != true || len(reply) != 4 || reply["snapshot"] != nil {
			t.Fatal(reply, stop)
		}
		encoded, err := ipc.Encode(reply, ipc.ResponseLimit)
		if err != nil || len(encoded) > 6000 {
			t.Fatal("astronomy reply exceeds local 6 KiB target", len(encoded), err)
		}
		return object(reply["astronomy"])
	}
	d := requestDay("")
	if d["date"] != "2026-10-09" || d["date_label"] != "Fri Oct 9, 2026" || d["phase_label"] != "12:00 PM EDT" || d["timezone"] != "America/New_York" || d["previous_date"] != "2026-10-08" || d["next_date"] != "2026-10-10" {
		t.Fatal(d)
	}
	if !reflect.DeepEqual(before, a.forecast) || a.fetchBusy || a.radar != nil || a.displayRows.source != nil || a.forecastHistory != nil {
		t.Fatal("astronomy mutated weather or started unrelated work")
	}
	a.forecast = nil
	if without := requestDay("2026-10-09"); !reflect.DeepEqual(d, without) {
		t.Fatal("astronomy depends on weather availability", without)
	}
	d = requestDay("2026-11-01")
	if d["day_start"] != "2026-11-01T04:00:00Z" || d["day_end"] != "2026-11-02T05:00:00Z" {
		t.Fatal("DST day", d)
	}
	for _, date := range []string{"2025-10-08", "2027-10-10"} {
		requestDay(date)
	}
}

func TestAstronomyValidationAndContext(t *testing.T) {
	a := astronomyFixture(t)
	for _, patch := range []M{{"date": nil}, {"date": "2026-02-30"}, {"date": "2026-2-10"}, {"date": "2025-10-07"}, {"date": "2027-10-11"}, {"date": "2026-10-09T00:00:00Z"}, {"latitude": math.NaN()}, {"longitude": math.Inf(1)}, {"latitude": nil}, {"extra": true}, {"location_id": "other"}, {"latitude": 41.0}, {"longitude": -75.0}, {"timezone": "America/Chicago"}} {
		q := astronomyQuery(a, "")
		for k, v := range patch {
			q[k] = v
		}
		if r := a.astronomyDay(1, q); r["ok"] != false {
			t.Fatal(patch, r)
		}
	}
	for _, change := range []func(*App){func(a *App) { a.primaryOnly = true }, func(a *App) { a.mode = "default" }, func(a *App) { a.id = "" }, func(a *App) { a.locationBusy = true }, func(a *App) { a.location = nil }, func(a *App) { a.location["timezone"] = "Invalid/Zone" }} {
		a := astronomyFixture(t)
		q := astronomyQuery(a, "")
		change(a)
		if a.astronomyDay(1, q)["ok"] != false {
			t.Fatal("accepted unavailable or changed location")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r, _ := a.Handle(ctx, request("astronomy_day", M{"day": astronomyQuery(a, "")})); r["ok"] != false {
		t.Fatal("canceled request admitted", r)
	}
}

func TestAstronomyPolarMissingEventsAndRollover(t *testing.T) {
	a := astronomyFixture(t)
	a.location["latitude"], a.location["longitude"], a.location["timezone"] = 69.6492, 18.9553, "Europe/Oslo"
	for _, row := range []struct {
		date, state string
		seconds     float64
	}{{"2026-06-21", "always_up", 86400}, {"2026-12-21", "always_down", 0}} {
		d := object(a.astronomyDay(1, astronomyQuery(a, row.date))["astronomy"])
		if d["sun_state"] != row.state || len(d["sun_events"].([]any)) != 0 || d["daylight_seconds"] != row.seconds {
			t.Fatal(d)
		}
	}
	a.location["timezone"] = "Pacific/Kiritimati"
	a.options.Now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	d := object(a.astronomyDay(1, astronomyQuery(a, ""))["astronomy"])
	if d["date"] != "2026-10-10" || d["day_start"] != "2026-10-09T10:00:00Z" {
		t.Fatal(d)
	}
}
