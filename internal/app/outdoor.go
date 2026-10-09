package app

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/joega/a-weather-app/internal/outdoor"
	"github.com/joega/a-weather-app/internal/weather"
)

const outdoorFile = "outdoor-preferences.json"
const outdoorPreferenceLimit = 1024

func outdoorPreferences(v M) (outdoor.Preferences, error) {
	p := outdoor.Preferences{}
	if len(v) != 9 {
		return p, errors.New("outdoor preferences")
	}
	for _, key := range []string{"hours", "min_temperature_c", "max_temperature_c", "max_probability", "max_hourly_precipitation_mm", "max_wind_m_s", "max_gust_m_s", "daylight_only", "schema_version"} {
		if _, ok := v[key]; !ok {
			return p, errors.New("outdoor preference fields")
		}
	}
	if v["schema_version"] != 1.0 {
		return p, errors.New("outdoor preference version")
	}
	// JSON types, including booleans versus null, are checked before decoding.
	for key, value := range v {
		if key == "daylight_only" {
			if _, ok := value.(bool); !ok {
				return p, errors.New("outdoor daylight preference")
			}
		} else if n, ok := value.(float64); !ok || math.IsNaN(n) || math.IsInf(n, 0) {
			return p, errors.New("outdoor numeric preference")
		}
	}
	raw, err := json.Marshal(v)
	if err == nil {
		err = json.Unmarshal(raw, &p)
	}
	if err != nil || !p.Valid() {
		return p, errors.New("outdoor preference range")
	}
	return p, nil
}
func outdoorPreferenceView(p outdoor.Preferences) M {
	return M{"schema_version": 1.0, "hours": float64(p.Hours), "min_temperature_c": p.MinTemperatureC, "max_temperature_c": p.MaxTemperatureC, "max_probability": p.MaxProbability, "max_hourly_precipitation_mm": p.MaxHourlyPrecipitationMM, "max_wind_m_s": p.MaxWindMS, "max_gust_m_s": p.MaxGustMS, "daylight_only": p.DaylightOnly}
}

// A narrow, synchronous query over existing immutable forecast rows. No planner
// state, worker, timer or result cache exists while the feature is closed.
// Preferences are read only on demand and written only on explicit submission.
func (a *App) outdoorPlan(id float64, query M) M {
	reply := M{"version": 1.0, "request_id": id, "ok": false, "error": "invalid_request"}
	if len(query) != 5 {
		return reply
	}
	for _, key := range []string{"latitude", "longitude", "timezone", "forecast_at", "preferences"} {
		if _, ok := query[key]; !ok {
			return reply
		}
	}
	p := outdoor.DefaultPreferences()
	var err error
	if query["preferences"] != nil {
		p, err = outdoorPreferences(object(query["preferences"]))
		if err != nil {
			return reply
		}
	}
	// Reject a queued request for a city/forecast that has since changed before
	// persisting its preferences or calculating any suggestions.
	reply["error"] = "outdoor_context_changed"
	if a.location == nil || a.forecast == nil || a.locationBusy || query["forecast_at"] != a.forecast["fetched_at"] {
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
	saved, saveStatus := outdoor.DefaultPreferences(), "defaults"
	if a.state != nil {
		doc, readErr := a.state.Read(outdoorFile, outdoorPreferenceLimit)
		if readErr != nil {
			saveStatus = "unavailable"
		} else if doc != nil {
			if saved, err = outdoorPreferences(doc); err != nil {
				saved, saveStatus = outdoor.DefaultPreferences(), "unavailable"
			} else {
				saveStatus = "saved"
			}
		}
	} else {
		saveStatus = "unavailable"
	}
	if query["preferences"] == nil {
		p = saved
	} else if saveStatus != "saved" || p != saved {
		saveStatus = "unconfirmed"
		if a.state != nil && a.state.Write(outdoorFile, outdoorPreferenceView(p), outdoorPreferenceLimit) == nil {
			saveStatus = "saved"
		}
	}
	now := a.options.Now()
	live := weather.SelectView(a.forecast, now, "live", nil, "subtle", false, false)
	plan := M{"preferences": outdoorPreferenceView(p), "save_status": saveStatus, "freshness": live["freshness"], "forecast_at": a.forecast["fetched_at"], "generated_at": now.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano), "place": a.location["name"], "timezone": zone.String(), "windows": []any{}, "evaluated": 0.0, "gaps": 0.0}
	if live["freshness"] == "fresh" || live["freshness"] == "stale" {
		rows, _ := a.forecast["hourly"].([]any)
		daily, _ := a.forecast["daily"].([]any)
		if len(rows) > outdoor.MaxHours || len(daily) > outdoor.MaxDays {
			reply["error"] = "outdoor_unavailable"
			return reply
		}
		hours := make([]outdoor.Hour, 0, len(rows))
		value := func(v any) outdoor.Value {
			if n, ok := v.(float64); ok {
				return outdoor.Number(n)
			}
			return outdoor.Value{}
		}
		for _, raw := range rows {
			r := object(raw)
			t, _ := weather.Instant(r["time"])
			hours = append(hours, outdoor.Hour{Time: t, TemperatureC: value(r["temperature_c"]), WindMS: value(r["wind_speed_m_s"]), GustMS: value(r["wind_gust_m_s"]), Probability: value(r["precipitation_probability"]), PrecipitationMM: value(r["precipitation_rate_mm_hr"])})
		}
		days := make([]outdoor.Daylight, 0, len(daily))
		for _, raw := range daily {
			r := object(raw)
			rise, _ := weather.Instant(r["sunrise"])
			set, _ := weather.Instant(r["sunset"])
			days = append(days, outdoor.Daylight{Date: stringOf(r["date"]), Sunrise: rise, Sunset: set})
		}
		result, err := outdoor.Rank(hours, days, zone, now, p)
		if err != nil {
			reply["error"] = "outdoor_unavailable"
			return reply
		}
		windows := make([]any, 0, len(result.Windows))
		for _, w := range result.Windows {
			metric := func(v outdoor.Value, key string) any {
				if !v.Known || slices.Contains(w.Missing, key) {
					return nil
				}
				return v.Number
			}
			windows = append(windows, M{"start": w.Start.UTC().Format(time.RFC3339), "end": w.End.UTC().Format(time.RFC3339), "range_label": w.Start.In(zone).Format("Mon Jan 2, 3:04 PM MST") + " – " + w.End.In(zone).Format("Mon Jan 2, 3:04 PM MST"), "fits": w.Fits, "missing": w.Missing, "exceeds": w.Exceeds, "low_c": metric(outdoor.Value{Number: w.TemperatureC.Low, Known: w.TemperatureC.Known}, "temperature"), "high_c": metric(outdoor.Value{Number: w.TemperatureC.High, Known: w.TemperatureC.Known}, "temperature"), "peak_probability": metric(w.PeakProbability, "rain_chance"), "peak_hourly_mm": metric(w.PeakHourlyPrecipitationMM, "rain_amount"), "total_mm": metric(w.TotalPrecipitationMM, "rain_amount"), "wind_m_s": metric(w.PeakWindMS, "wind"), "gust_m_s": metric(w.PeakGustMS, "gusts"), "daylight": w.Daylight})
		}
		plan["windows"], plan["evaluated"], plan["gaps"] = windows, float64(result.Evaluated), float64(result.Gaps)
	}
	return M{"version": 1.0, "request_id": id, "ok": true, "outdoor": plan}
}
