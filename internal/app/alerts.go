package app

import (
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func cachedAlerts(forecast, location M) M {
	if forecast == nil || !reflect.DeepEqual(object(forecast["location"]), location) {
		return nil
	}
	return object(forecast["alerts"])
}

// Cached alerts are location-bound, bounded by the existing stale window, and
// retain their original timestamp. A failure cannot create a fresh empty feed.
func mergeAlerts(incoming, cached M, now time.Time, pending bool) M {
	if incoming["status"] == "available" {
		return incoming
	}
	fetched, err := weather.Instant(cached["fetched_at"])
	age := now.Sub(fetched).Seconds()
	if cached["status"] == "available" && err == nil && age >= 0 && age <= weather.StaleSeconds {
		result := safeio.Clone(cached)
		result["freshness"], result["refreshing"] = "stale", pending
		return result
	}
	return incoming
}
