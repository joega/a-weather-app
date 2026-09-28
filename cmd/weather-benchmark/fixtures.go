package main

import (
	"errors"
	"github.com/joega/a-weather-app/internal/safeio"
	"time"
)

// Freshness uses both timestamps. Refresh only this deep private fixture so
// sequential measurements retain the same live weather workload, not an
// accidental stale-to-expired transition. No real saved data is changed.
func freshBenchmarkForecast(source map[string]any, now time.Time) (map[string]any, error) {
	copy := safeio.Clone(source)
	current, ok := copy["current"].(map[string]any)
	if !ok {
		return nil, errors.New("benchmark current record missing")
	}
	stamp := now.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
	copy["fetched_at"] = stamp
	current["time"] = stamp
	return copy, nil
}
