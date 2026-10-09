package airquality

import (
	"errors"
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

const OutlookCacheBytes = 24 * 1024
const outlookCacheSource = "open-meteo/cams-global-outlook/v1"

// OutlookCacheRecord stores UTC epoch plus eight values in OutlookMetrics order.
// Source version fixes the domain, units and interpretation of every column.
// Null preserves unknown; zero is known. The owner stores one optional file
// with safeio.Directory.Write and the OutlookCacheBytes limit.
func OutlookCacheRecord(d Outlook) (weather.Object, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	rows := make([]any, len(d.Hours))
	for i, h := range d.Hours {
		row := make([]any, 9)
		row[0] = float64(h.Time.Unix())
		for j, v := range h.Values {
			if v.Known {
				row[j+1] = v.Number
			}
		}
		rows[i] = row
	}
	return weather.Object{"version": 1.0, "source": outlookCacheSource, "latitude": d.Latitude, "longitude": d.Longitude, "grid_latitude": d.GridLatitude, "grid_longitude": d.GridLongitude, "timezone": d.Timezone, "fetched_at": d.FetchedAt.UTC().Format(time.RFC3339Nano), "hours": rows}, nil
}

// ReadOutlookCache accepts a record already decoded by bounded safeio.Directory.Read.
// Every field is revalidated; changed formats, damaged cells and mismatched
// source semantics reject the optional cache without changing other forecasts.
func ReadOutlookCache(record weather.Object) (Outlook, error) {
	bad := errors.New("invalid AQ outlook cache")
	keys := []string{"version", "source", "latitude", "longitude", "grid_latitude", "grid_longitude", "timezone", "fetched_at", "hours"}
	if len(record) != len(keys) || record["version"] != 1.0 || record["source"] != outlookCacheSource {
		return Outlook{}, bad
	}
	for _, key := range keys {
		if _, ok := record[key]; !ok {
			return Outlook{}, bad
		}
	}
	var d Outlook
	for key, dest := range map[string]*float64{"latitude": &d.Latitude, "longitude": &d.Longitude, "grid_latitude": &d.GridLatitude, "grid_longitude": &d.GridLongitude} {
		n, ok := record[key].(float64)
		if !ok {
			return Outlook{}, bad
		}
		*dest = n
	}
	var ok bool
	if d.Timezone, ok = record["timezone"].(string); !ok {
		return Outlook{}, bad
	}
	stamp, ok := record["fetched_at"].(string)
	if !ok || len(stamp) > 35 {
		return Outlook{}, bad
	}
	var err error
	d.FetchedAt, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return Outlook{}, bad
	}
	rows, ok := record["hours"].([]any)
	if !ok || len(rows) == 0 || len(rows) > OutlookHours {
		return Outlook{}, bad
	}
	d.Hours = make([]OutlookHour, len(rows))
	for i, raw := range rows {
		row, ok := raw.([]any)
		if !ok || len(row) != 9 {
			return Outlook{}, bad
		}
		n, ok := row[0].(float64)
		if !ok || (math.IsNaN(n) || math.IsInf(n, 0)) || n != math.Trunc(n) || n < float64(d.FetchedAt.Add(-2*time.Hour).Unix()) || n > float64(d.FetchedAt.Add(50*time.Hour).Unix()) {
			return Outlook{}, bad
		}
		h := &d.Hours[i]
		h.Time = time.Unix(int64(n), 0).UTC()
		for j := range h.Values {
			if row[j+1] == nil {
				continue
			}
			value, ok := row[j+1].(float64)
			if !ok {
				return Outlook{}, bad
			}
			h.Values[j] = OutlookValue{Number: value, Known: true}
		}
	}
	if err := d.Validate(); err != nil {
		return Outlook{}, err
	}
	return d, nil
}
