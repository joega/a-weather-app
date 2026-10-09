package app

import (
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

// forecastBriefing derives at most three periods from already normalized hours.
// It belongs to the display-row cache, not the rendering or polling loop. Each
// precipitation value describes the hour ending at its timestamp; an hourly
// peak is never interpreted as the onset or end of rain.
func forecastBriefing(hours []any, zone *time.Location, now time.Time) []any {
	result := []any{}
	if len(hours) == 0 {
		return result
	}
	local := now.In(zone)
	evening := time.Date(local.Year(), local.Month(), local.Day(), 18, 0, 0, 0, zone)
	tomorrow := local.AddDate(0, 0, 1)
	morning := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 6, 0, 0, 0, zone)
	nextEvening := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 18, 0, 0, 0, zone)
	for _, period := range []struct {
		id         string
		start, end time.Time
	}{
		{"today", now, evening},
		{"tonight", evening, morning},
		{"tomorrow", morning, nextEvening},
	} {
		start := period.start
		if start.Before(now) {
			start = now
		}
		if !start.Before(period.end) {
			continue
		}
		row := M{"period": period.id, "start": start.UTC().Format(time.RFC3339), "end": period.end.UTC().Format(time.RFC3339), "range_label": clockLabel(start.In(zone)) + " " + start.In(zone).Format("MST") + " – " + clockLabel(period.end.In(zone)) + " " + period.end.In(zone).Format("MST"), "low_c": nil, "high_c": nil, "peak_probability": nil, "peak_label": nil, "gust_m_s": nil}
		var temperatureCoverage, rainCoverage, windCoverage time.Duration
		coveredUntil := start
		for _, value := range hours {
			hour := object(value)
			stamp, err := weather.Instant(hour["time"])
			if err != nil || !stamp.After(start) || stamp.After(period.end) {
				continue
			}
			from := stamp.Add(-time.Hour)
			if from.Before(coveredUntil) {
				from = coveredUntil
			}
			if !from.Before(stamp) {
				continue
			}
			duration := stamp.Sub(from)
			coveredUntil = stamp
			if v, ok := briefingNumber(hour["temperature_c"]); ok {
				temperatureCoverage += duration
				if row["low_c"] == nil || v < row["low_c"].(float64) {
					row["low_c"] = v
				}
				if row["high_c"] == nil || v > row["high_c"].(float64) {
					row["high_c"] = v
				}
			}
			if v, ok := briefingNumber(hour["precipitation_probability"]); ok {
				rainCoverage += duration
				if row["peak_probability"] == nil || v > row["peak_probability"].(float64) {
					row["peak_probability"], row["peak_label"] = v, hour["period_label"]
				}
			}
			if v, ok := briefingNumber(hour["wind_gust_m_s"]); ok {
				windCoverage += duration
				if row["gust_m_s"] == nil || v > row["gust_m_s"].(float64) {
					row["gust_m_s"] = v
				}
			}
		}
		duration := period.end.Sub(start)
		row["temperature_complete"] = temperatureCoverage == duration
		row["precipitation_complete"] = rainCoverage == duration
		row["wind_complete"] = windCoverage == duration
		result = append(result, row)
	}
	return result
}

func briefingNumber(value any) (float64, bool) {
	n, ok := value.(float64)
	return n, ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}
