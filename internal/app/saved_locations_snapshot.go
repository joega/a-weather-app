package app

import (
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

// Reuse immutable list presentation until metadata or a displayed freshness
// boundary changes. No forecasts are opened and no saved city is refreshed to
// build this list. Its twenty compact rows also support primary labels while
// the lazy frontend picker is closed.
type savedListPresentation struct {
	generation       string
	builtAt, expires time.Time
	items            []any
}

func (a *App) savedLocationsSnapshot() any {
	if a.saved == nil {
		return nil
	}
	now := a.options.Now()
	cache := &a.savedList
	generation := stringOf(a.saved.doc["generation"])
	if cache.items == nil || cache.generation != generation || now.Before(cache.builtAt) || !now.Before(cache.expires) {
		cache.generation, cache.builtAt = generation, now
		cache.expires = now.Truncate(time.Minute).Add(time.Minute)
		cache.items = make([]any, 0, len(a.saved.doc["places"].([]any)))
		for _, value := range a.saved.doc["places"].([]any) {
			entry := object(value)
			profile := object(entry["profile"])
			location := object(profile["location"])
			row := M{"id": entry["id"], "name": location["name"], "label": entry["label"], "mode": profile["mode"], "country_code": profile["country_code"], "timezone": location["timezone"], "summary": nil}
			if summary := object(entry["summary"]); summary != nil {
				row["summary"] = savedSummaryPresentation(summary, now, &cache.expires)
			}
			cache.items = append(cache.items, row)
		}
	}
	return M{"schema_version": 1.0, "primary": a.saved.doc["primary"], "viewed": a.saved.doc["viewed"], "items": cache.items, "primary_forecast_available": a.primary != nil && a.primary.forecast != nil}
}

func savedSummaryPresentation(summary M, now time.Time, expires *time.Time) M {
	boundary := func(at time.Time) {
		if at.After(now) && at.Before(*expires) {
			*expires = at
		}
	}
	fetched, _ := weather.Instant(summary["fetched_at"])
	valid, _ := weather.Instant(summary["valid_at"])
	age := math.Max(0, math.Max(now.Sub(fetched).Seconds(), now.Sub(valid).Seconds()))
	freshness := "fresh"
	if math.Min(now.Sub(fetched).Seconds(), now.Sub(valid).Seconds()) < -300 {
		freshness = "invalid_future"
	} else if age > weather.ExpireSeconds {
		freshness = "expired"
	} else if age > weather.StaleSeconds {
		freshness = "stale"
	}
	for _, stamp := range []time.Time{fetched, valid} {
		boundary(stamp.Add(-5 * time.Minute))
		boundary(stamp.Add(weather.StaleSeconds*time.Second + time.Nanosecond))
		boundary(stamp.Add(weather.ExpireSeconds*time.Second + time.Nanosecond))
	}
	status := "unavailable"
	if summary["alert_status"] == "not_supported_here" {
		status = "not_supported_here"
	}
	alertFetched, err := weather.Instant(summary["alert_fetched_at"])
	alertExpiry, _ := weather.Instant(summary["alert_expires"])
	alertAge := now.Sub(alertFetched).Seconds()
	if err == nil && alertAge >= 0 && alertAge <= weather.StaleSeconds {
		current := summary["alert_status"] == "current" && alertAge <= weather.RefreshSeconds
		if alertExpiry.After(now) {
			status = "cached"
			if current {
				status = "active"
			}
		} else if current {
			status = "none"
		}
		boundary(alertExpiry)
	}
	if err == nil {
		boundary(alertFetched)
		boundary(alertFetched.Add(weather.RefreshSeconds*time.Second + time.Nanosecond))
		boundary(alertFetched.Add(weather.StaleSeconds*time.Second + time.Nanosecond))
	}
	return M{"temperature_c": summary["temperature_c"], "condition": summary["condition"], "is_day": summary["is_day"], "fetched_at": summary["fetched_at"], "valid_at": summary["valid_at"], "freshness": freshness, "alert_status": status}
}
