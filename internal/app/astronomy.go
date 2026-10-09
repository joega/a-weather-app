package app

import (
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/astronomy"
)

// One explicitly requested date, calculated from the selected place. Astronomy
// works offline and is independent of forecast freshness; no provider, cache,
// persistence or normal snapshot work is involved.
func (a *App) astronomyDay(id float64, query M) M {
	reply := M{"version": 1.0, "request_id": id, "ok": false, "error": "invalid_request"}
	if !savedFields(query, "location_id", "latitude", "longitude", "timezone", "date") {
		return reply
	}
	date, ok := query["date"].(string)
	if !ok || len(date) > 10 {
		return reply
	}
	for _, key := range []string{"latitude", "longitude"} {
		if n, ok := query[key].(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return reply
		}
	}
	reply["error"] = "astronomy_context_changed"
	if a.primaryOnly || a.forecastPoint == nil || a.mode == "default" || a.id == "" || a.location == nil || a.locationBusy || query["location_id"] != a.id {
		return reply
	}
	for _, key := range []string{"latitude", "longitude", "timezone"} {
		if query[key] != a.location[key] {
			return reply
		}
	}
	zone, err := time.LoadLocation(stringOf(a.location["timezone"]))
	if err != nil {
		return reply
	}
	now := a.options.Now()
	today := now.In(zone)
	minDate, maxDate := today.AddDate(0, 0, -366).Format(time.DateOnly), today.AddDate(0, 0, 366).Format(time.DateOnly)
	if date == "" {
		date = today.Format(time.DateOnly)
	}
	if date < minDate || date > maxDate {
		reply["error"] = "astronomy_date_unavailable"
		return reply
	}
	d, err := astronomy.Calculate(date, zone, a.location["latitude"].(float64), a.location["longitude"].(float64))
	if err != nil {
		reply["error"] = "astronomy_date_unavailable"
		return reply
	}
	stamp := func(t time.Time) string { return t.UTC().Truncate(time.Second).Format(time.RFC3339) }
	label := func(t time.Time) string { return t.In(zone).Format("3:04 PM MST") }
	events := func(rows []astronomy.Event) []any {
		result := []any{}
		for _, e := range rows {
			result = append(result, M{"time": stamp(e.Time), "label": label(e.Time), "kind": e.Kind})
		}
		return result
	}
	spans := func(rows []astronomy.Span) []any {
		result := []any{}
		for _, s := range rows {
			result = append(result, M{"start": stamp(s.Start), "end": stamp(s.End), "label": label(s.Start) + " – " + label(s.End)})
		}
		return result
	}
	view := M{"location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": zone.String(), "date": d.Date, "date_label": d.PhaseAt.In(zone).Format("Mon Jan 2, 2006"), "today": today.Format(time.DateOnly), "min_date": minDate, "max_date": maxDate, "previous_date": d.Start.Add(-time.Second).In(zone).Format(time.DateOnly), "next_date": d.End.In(zone).Format(time.DateOnly), "day_start": stamp(d.Start), "day_end": stamp(d.End), "calculated_at": stamp(now), "phase_at": stamp(d.PhaseAt), "phase_label": label(d.PhaseAt), "phase": d.Phase, "illumination": d.Illumination, "daylight_seconds": d.Daylight.Seconds(), "change_seconds": d.Change.Seconds(), "sun_state": d.Sun.State, "moon_state": d.Moon.State, "sun_events": events(d.Sun.Events), "moon_events": events(d.Moon.Events), "civil_events": events(d.Civil), "nautical_events": events(d.Nautical), "astronomical_events": events(d.Astronomical), "daylight": spans(d.Sun.Above), "golden": spans(d.Golden)}
	return M{"version": 1.0, "request_id": id, "ok": true, "astronomy": view}
}
