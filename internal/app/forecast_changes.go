package app

import (
	"errors"
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/forecastchange"
	"github.com/joega/a-weather-app/internal/weather"
)

const (
	forecastHistoryFile   = "forecast-history.json"
	forecastHistoryBytes  = 96 * 1024
	forecastHistoryPlaces = 4
	// Change this identity if the hourly provider or its interval contract changes.
	forecastChangeSource = "open-meteo-hourly-v1"
)

type forecastHistoryEntry struct {
	id       string
	current  forecastchange.Forecast
	previous *forecastchange.Forecast
}

type forecastHistory struct {
	files     savedLocationFiles
	entries   []forecastHistoryEntry // Most recently presented first; maximum four.
	dirty     bool
	recovered bool
}

func changeStamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// Store only the comparison fields. Null is missing, not zero. The compact row
// representation keeps eight maximum-size projections below the local 96 KiB cap.
func forecastProjectionView(f forecastchange.Forecast) M {
	rows := make([]any, 0, len(f.Hours))
	value := func(v forecastchange.Value) any {
		if !v.Known {
			return nil
		}
		return v.Number
	}
	for _, h := range f.Hours {
		rows = append(rows, []any{changeStamp(h.Time), value(h.TemperatureC), value(h.Probability), value(h.PrecipitationMM), value(h.GustMS)})
	}
	return M{"latitude": f.Place.Latitude, "longitude": f.Place.Longitude, "timezone": f.Place.Timezone, "source": f.Source, "retrieved": changeStamp(f.Retrieved), "hours": rows}
}

func readForecastProjection(v M) (forecastchange.Forecast, error) {
	f := forecastchange.Forecast{}
	bad := errors.New("invalid forecast history projection")
	if !savedFields(v, "latitude", "longitude", "timezone", "source", "retrieved", "hours") {
		return f, bad
	}
	lat, latOK := v["latitude"].(float64)
	lon, lonOK := v["longitude"].(float64)
	f.Place = forecastchange.Place{Latitude: lat, Longitude: lon, Timezone: stringOf(v["timezone"])}
	f.Source = stringOf(v["source"])
	var err error
	f.Retrieved, err = weather.Instant(v["retrieved"])
	rows, ok := v["hours"].([]any)
	if !latOK || !lonOK || err != nil || !ok || len(rows) == 0 || len(rows) > forecastchange.MaxHours {
		return f, bad
	}
	for _, raw := range rows {
		row, ok := raw.([]any)
		if !ok || len(row) != 5 {
			return f, bad
		}
		t, err := weather.Instant(row[0])
		if err != nil {
			return f, bad
		}
		h := forecastchange.Hour{Time: t}
		for i, field := range []*forecastchange.Value{&h.TemperatureC, &h.Probability, &h.PrecipitationMM, &h.GustMS} {
			if row[i+1] == nil {
				continue
			}
			n, ok := row[i+1].(float64)
			if !ok {
				return f, bad
			}
			*field = forecastchange.Number(n)
		}
		f.Hours = append(f.Hours, h)
	}
	return f, f.Validate()
}

func readForecastHistory(v M) ([]forecastHistoryEntry, error) {
	bad := errors.New("invalid forecast history")
	if !savedFields(v, "schema_version", "places") || v["schema_version"] != 1.0 {
		return nil, bad
	}
	rows, ok := v["places"].([]any)
	if !ok || len(rows) > forecastHistoryPlaces {
		return nil, bad
	}
	entries := make([]forecastHistoryEntry, 0, len(rows))
	seen := map[string]bool{}
	for _, raw := range rows {
		row := object(raw)
		id := stringOf(row["id"])
		if !savedFields(row, "id", "current", "previous") || id == "" || len(id) > 96 || seen[id] {
			return nil, bad
		}
		seen[id] = true
		current, err := readForecastProjection(object(row["current"]))
		if err != nil {
			return nil, bad
		}
		entry := forecastHistoryEntry{id: id, current: current}
		if row["previous"] != nil {
			previous, err := readForecastProjection(object(row["previous"]))
			if err != nil || previous.Place != current.Place || previous.Source != current.Source || !previous.Retrieved.Before(current.Retrieved) {
				return nil, bad
			}
			entry.previous = &previous
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Optional history is read only after an explicit visible-forecast acknowledgment
// (or removal of a saved place), never while constructing an App or a snapshot.
func loadForecastHistory(files savedLocationFiles) *forecastHistory {
	h := &forecastHistory{files: files}
	if files == nil {
		h.recovered = true
		return h
	}
	doc, err := files.Read(forecastHistoryFile, forecastHistoryBytes)
	if err == nil && doc != nil {
		h.entries, err = readForecastHistory(doc)
	}
	if err != nil {
		h.entries = nil
		h.recovered = true
	}
	return h
}

func (a *App) loadForecastHistory() {
	if a.forecastHistory != nil {
		return
	}
	var files savedLocationFiles
	if a.state != nil {
		files = a.state
	}
	a.forecastHistory = loadForecastHistory(files)
}

// Removing a place also removes its optional comparison evidence. A failed
// optional write is retained for retry and reported as an unconfirmed save;
// it does not roll back the already committed saved-place removal.
func (a *App) forgetForecastHistory(id string) bool {
	a.loadForecastHistory()
	h := a.forecastHistory
	for i, e := range h.entries {
		if e.id == id {
			h.entries = append(h.entries[:i:i], h.entries[i+1:]...)
			h.dirty = true
			break
		}
	}
	if h.recovered {
		h.dirty = true
	}
	return h.save() == "saved"
}
func (h *forecastHistory) save() string {
	if !h.dirty {
		return "saved"
	}
	if h.files == nil {
		return "unconfirmed"
	}
	rows := make([]any, 0, len(h.entries))
	for _, e := range h.entries {
		var previous any
		if e.previous != nil {
			previous = forecastProjectionView(*e.previous)
		}
		rows = append(rows, M{"id": e.id, "current": forecastProjectionView(e.current), "previous": previous})
	}
	if h.files.Write(forecastHistoryFile, M{"schema_version": 1.0, "places": rows}, forecastHistoryBytes) != nil {
		return "unconfirmed"
	}
	h.dirty = false
	return "saved"
}

// advance adopts what was actually presented even when optional persistence
// fails. Repeating the same retrieval keeps its preceding displayed baseline.
// An older retrieval cannot roll history backward. Same-retrieval corrections
// use the first acknowledged evidence, avoiding revisions from alert-only work.
func (h *forecastHistory) advance(id string, current forecastchange.Forecast, now time.Time) (forecastchange.Result, string, error) {
	index := -1
	entry := forecastHistoryEntry{id: id, current: current}
	for i, e := range h.entries {
		if e.id == id {
			index = i
			break
		}
	}
	if index >= 0 {
		old := h.entries[index]
		if old.current.Place == current.Place && old.current.Source == current.Source {
			if current.Retrieved.Before(old.current.Retrieved) {
				result, err := forecastchange.Compare(&old.current, current, now)
				return result, h.save(), err
			}
			if current.Retrieved.Equal(old.current.Retrieved) {
				entry = old
			} else {
				previous := old.current
				entry.previous = &previous
			}
		}
	}
	result, err := forecastchange.Compare(entry.previous, entry.current, now)
	if err != nil || result.Status == "current_unavailable" {
		return result, "unconfirmed", err
	}
	changed := index != 0 || len(h.entries) == 0 || !h.entries[0].current.Retrieved.Equal(entry.current.Retrieved) || h.entries[0].current.Place != entry.current.Place || h.entries[0].current.Source != entry.current.Source
	if changed {
		next := make([]forecastHistoryEntry, 0, forecastHistoryPlaces)
		next = append(next, entry)
		for i, e := range h.entries {
			if i != index && len(next) < forecastHistoryPlaces {
				next = append(next, e)
			}
		}
		h.entries, h.dirty = next, true
	}
	return result, h.save(), nil
}

func (a *App) forecastProjection() (forecastchange.Forecast, error) {
	f := forecastchange.Forecast{Source: forecastChangeSource}
	bad := errors.New("forecast comparison unavailable")
	lat, latOK := a.location["latitude"].(float64)
	lon, lonOK := a.location["longitude"].(float64)
	f.Place = forecastchange.Place{Latitude: lat, Longitude: lon, Timezone: stringOf(a.location["timezone"])}
	var err error
	f.Retrieved, err = weather.Instant(a.forecast["fetched_at"])
	rows, ok := a.forecast["hourly"].([]any)
	if !latOK || !lonOK || err != nil || !ok || len(rows) > 240 {
		return f, bad
	}
	for _, raw := range rows {
		row := object(raw)
		t, err := weather.Instant(row["time"])
		if err != nil {
			return f, bad
		}
		if t.Before(f.Retrieved) || t.After(f.Retrieved.Add(forecastchange.MaxHours*time.Hour)) {
			continue
		}
		if len(f.Hours) == forecastchange.MaxHours {
			break
		}
		h := forecastchange.Hour{Time: t}
		for _, field := range []struct {
			key string
			out *forecastchange.Value
		}{
			{"temperature_c", &h.TemperatureC}, {"precipitation_probability", &h.Probability}, {"precipitation_rate_mm_hr", &h.PrecipitationMM}, {"wind_gust_m_s", &h.GustMS},
		} {
			if row[field.key] == nil {
				continue
			}
			n, ok := row[field.key].(float64)
			if !ok {
				return f, bad
			}
			*field.out = forecastchange.Number(n)
		}
		f.Hours = append(f.Hours, h)
	}
	if len(f.Hours) == 0 {
		return f, bad
	}
	return f, f.Validate()
}

// Caller holds App.mu. The IPC server additionally requires this specific peer
// to be subscribed and presented. Global App.presented is asynchronous demand,
// so it is not used as proof that any particular forecast was displayed.
func (a *App) forecastPresented(id float64, query M) M {
	reply := M{"version": 1.0, "request_id": id, "ok": false, "error": "invalid_request"}
	if !savedFields(query, "location_id", "latitude", "longitude", "timezone", "forecast_at") {
		return reply
	}
	for _, key := range []string{"latitude", "longitude"} {
		if n, ok := query[key].(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return reply
		}
	}
	if _, err := weather.Instant(query["forecast_at"]); err != nil {
		return reply
	}
	reply["error"] = "forecast_context_changed"
	if a.primaryOnly || a.forecastPoint == nil || a.mode == "default" || a.id == "" || a.location == nil || a.forecast == nil || a.locationBusy || query["location_id"] != a.id || query["forecast_at"] != a.forecast["fetched_at"] {
		return reply
	}
	for _, key := range []string{"latitude", "longitude", "timezone"} {
		if query[key] != a.location[key] {
			return reply
		}
	}
	reply["error"] = "forecast_changes_unavailable"
	now := a.options.Now()
	current, err := a.forecastProjection()
	if err != nil {
		return reply
	}
	age := now.Sub(current.Retrieved)
	if age < 0 || age > forecastchange.MaxCurrentAge {
		return reply
	}
	a.loadForecastHistory()
	result, saveStatus, err := a.forecastHistory.advance(a.id, current, now)
	if err != nil {
		return reply
	}
	return M{"version": 1.0, "request_id": id, "ok": true, "forecast_changes": forecastChangesView(result, a.id, current.Place, saveStatus, a.forecastHistory.recovered)}
}

func forecastChangesView(r forecastchange.Result, id string, place forecastchange.Place, saveStatus string, recovered bool) M {
	zone, _ := time.LoadLocation(place.Timezone)
	label := func(start, end time.Time) string {
		s := start.In(zone).Format("Mon Jan 2, 3:04 PM MST")
		if !start.Equal(end) {
			s += " – " + end.In(zone).Format("Mon Jan 2, 3:04 PM MST")
		}
		return s
	}
	var previous any
	if !r.PreviousRetrieved.IsZero() {
		previous = changeStamp(r.PreviousRetrieved)
	}
	rows := make([]any, 0, len(r.Changes))
	for _, c := range r.Changes {
		row := M{"kind": c.Kind, "start": changeStamp(c.Start), "end": changeStamp(c.End), "range_label": label(c.Start, c.End), "samples": float64(c.Samples), "before": c.Before, "after": c.After, "previous_start": nil, "previous_end": nil, "previous_range_label": nil}
		if c.Kind == "wet_hours" {
			row["before"], row["after"] = nil, nil
			row["previous_start"], row["previous_end"], row["previous_range_label"] = changeStamp(c.BeforeStart), changeStamp(c.BeforeEnd), label(c.BeforeStart, c.BeforeEnd)
		}
		rows = append(rows, row)
	}
	c := r.Coverage
	return M{"status": r.Status, "location_id": id, "latitude": place.Latitude, "longitude": place.Longitude, "timezone": place.Timezone, "source": "Open-Meteo", "current_retrieved": changeStamp(r.CurrentRetrieved), "previous_retrieved": previous, "start": changeStamp(r.Start), "end": changeStamp(r.End), "save_status": saveStatus, "history_recovered": recovered, "coverage": M{"expected_points": float64(c.ExpectedPoints), "expected_intervals": float64(c.ExpectedIntervals), "temperature": float64(c.Temperature), "probability": float64(c.Probability), "precipitation": float64(c.Precipitation), "gusts": float64(c.Gusts)}, "changes": rows}
}
