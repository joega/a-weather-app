package app

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/outdoor"
	"github.com/joega/a-weather-app/internal/safeio"
)

func outdoorFixture(t *testing.T) *App {
	t.Helper()
	a := briefingFixture(t, time.Date(2026, 10, 9, 14, 30, 0, 0, time.UTC), "America/New_York")
	a.state = testState(t)
	for _, h := range a.forecast["hourly"].([]any) {
		r := object(h)
		r["wind_speed_m_s"], r["precipitation_rate_mm_hr"] = 2.0, 0.0
	}
	a.forecast["daily"] = []any{M{"date": "2026-10-09", "sunrise": "2026-10-09T11:00:00Z", "sunset": "2026-10-09T22:00:00Z"}, M{"date": "2026-10-10", "sunrise": "2026-10-10T11:00:00Z", "sunset": "2026-10-10T22:00:00Z"}, M{"date": "2026-10-11", "sunrise": "2026-10-11T11:00:00Z", "sunset": "2026-10-11T22:00:00Z"}}
	return a
}
func outdoorQuery(a *App, p any) M {
	return M{"latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": a.location["timezone"], "forecast_at": a.forecast["fetched_at"], "preferences": p}
}
func TestOutdoorNarrowReplyPersistenceAndNoDefaultWork(t *testing.T) {
	a := outdoorFixture(t)
	before := safeio.Clone(a.forecast)
	reply, _ := a.Handle(context.Background(), request("outdoor_plan", M{"plan": outdoorQuery(a, nil)}))
	if reply["ok"] != true || reply["snapshot"] != nil || len(reply) != 4 {
		t.Fatal(reply)
	}
	plan := object(reply["outdoor"])
	if plan["save_status"] != "defaults" || plan["freshness"] != "fresh" || len(plan["windows"].([]any)) != 3 {
		t.Fatal(plan)
	}
	w := object(plan["windows"].([]any)[0])
	if w["start"] != "2026-10-09T15:00:00Z" || w["range_label"] != "Fri Oct 9, 11:00 AM EDT – Fri Oct 9, 12:00 PM EDT" || w["fits"] != true {
		t.Fatal(w)
	}
	if doc, err := a.state.Read(outdoorFile, outdoorPreferenceLimit); err != nil || doc != nil {
		t.Fatal("opening saved defaults without an explicit submission", doc, err)
	}
	p := outdoor.DefaultPreferences()
	p.Hours, p.MaxProbability = 3, .4
	reply = a.outdoorPlan(7, outdoorQuery(a, outdoorPreferenceView(p)))
	if object(reply["outdoor"])["save_status"] != "saved" {
		t.Fatal(reply)
	}
	reopened := outdoorFixture(t)
	reopened.state = a.state
	r := object(reopened.outdoorPlan(8, outdoorQuery(reopened, nil))["outdoor"])
	if !reflect.DeepEqual(r["preferences"], outdoorPreferenceView(p)) {
		t.Fatal("preferences not restored", r)
	}
	raw, err := ipc.Encode(reply, ipc.ResponseLimit)
	if err != nil || len(raw) > 6000 {
		t.Fatal("planner reply exceeded local 6 KiB budget", len(raw), err)
	}
	if !reflect.DeepEqual(a.forecast, before) || a.fetchBusy || a.radar != nil || a.displayRows.source != nil {
		t.Fatal("planning mutated forecast, started work or built ordinary snapshot")
	}
}
func TestOutdoorRejectsStaleContextAndMalformedPreferences(t *testing.T) {
	a := outdoorFixture(t)
	for _, key := range []string{"latitude", "longitude", "timezone", "forecast_at"} {
		q := outdoorQuery(a, outdoorPreferenceView(outdoor.DefaultPreferences()))
		q[key] = "changed"
		if r := a.outdoorPlan(1, q); r["error"] != "outdoor_context_changed" || r["ok"] != false {
			t.Fatal(key, r)
		}
	}
	for _, patch := range []M{{"hours": 1.5}, {"hours": 5.0}, {"min_temperature_c": 90.0}, {"max_probability": nil}, {"daylight_only": nil}, {"max_wind_m_s": 40.0}, {"extra": true}, {"schema_version": 2.0}} {
		p := outdoorPreferenceView(outdoor.DefaultPreferences())
		for k, v := range patch {
			p[k] = v
		}
		if r := a.outdoorPlan(1, outdoorQuery(a, p)); r["error"] != "invalid_request" || r["ok"] != false {
			t.Fatal(p, r)
		}
	}
	if doc, err := a.state.Read(outdoorFile, outdoorPreferenceLimit); err != nil || doc != nil {
		t.Fatal("invalid request wrote preferences", doc, err)
	}
	a.locationBusy = true
	if a.outdoorPlan(1, outdoorQuery(a, nil))["ok"] != false {
		t.Fatal("planned during place change")
	}
}
func TestOutdoorFreshnessMissingValuesAndSavingFailure(t *testing.T) {
	a := outdoorFixture(t)
	now := a.options.Now()
	for _, tc := range []struct {
		offset    time.Duration
		freshness string
		windows   bool
	}{{50 * time.Minute, "stale", true}, {3 * time.Hour, "expired", false}, {-time.Hour, "invalid_future", false}} {
		a.options.Now = func() time.Time { return now.Add(tc.offset) }
		r := object(a.outdoorPlan(1, outdoorQuery(a, nil))["outdoor"])
		if r["freshness"] != tc.freshness || (len(r["windows"].([]any)) > 0) != tc.windows {
			t.Fatal(tc, r)
		}
	}
	a.options.Now = func() time.Time { return now }
	for _, h := range a.forecast["hourly"].([]any) {
		object(h)["wind_speed_m_s"] = nil
	}
	r := object(a.outdoorPlan(1, outdoorQuery(a, nil))["outdoor"])
	for _, raw := range r["windows"].([]any) {
		w := object(raw)
		if w["fits"] != false || w["wind_m_s"] != nil || len(w["missing"].([]string)) != 1 {
			t.Fatal(w)
		}
	}
	// A malformed saved file must be reported, not silently called saved.
	if err := a.state.Write(outdoorFile, M{"schema_version": 99.0}, outdoorPreferenceLimit); err != nil {
		t.Fatal(err)
	}
	r = object(a.outdoorPlan(1, outdoorQuery(a, nil))["outdoor"])
	if r["save_status"] != "unavailable" {
		t.Fatal(r)
	}
	// A directory at the destination makes atomic replacement fail reliably.
	path := filepath.Join(a.state.Path, outdoorFile)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	r = object(a.outdoorPlan(1, outdoorQuery(a, outdoorPreferenceView(outdoor.DefaultPreferences())))["outdoor"])
	if r["save_status"] != "unconfirmed" || len(r["windows"].([]any)) != 3 {
		t.Fatal(r)
	}
}
