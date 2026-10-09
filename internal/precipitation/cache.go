package precipitation

import (
	"errors"
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

const CacheBytes = 64 * 1024
const cacheSource = "open-meteo/hourly-precipitation/v1"

// CacheRecord projects one dataset into compact rows: epoch, liquid mm, rain
// mm, showers mm, new snow cm, ground depth m, freezing height m ASL, probability
// fraction. Null preserves unknown; zero is a known value. The owner stores one
// optional file with safeio.Directory.Write and the CacheBytes limit.
func CacheRecord(d Data) (weather.Object, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	rows := make([]any, len(d.Hours))
	for i, h := range d.Hours {
		row := make([]any, 8)
		row[0] = float64(h.End.Unix())
		for j, v := range []Value{h.TotalMM, h.RainMM, h.ShowersMM, h.SnowCM, h.DepthM, h.FreezingM, h.Probability} {
			if v.Known {
				row[j+1] = v.Number
			}
		}
		rows[i] = row
	}
	return weather.Object{"version": 1.0, "source": cacheSource, "latitude": d.Latitude, "longitude": d.Longitude, "grid_latitude": d.GridLatitude, "grid_longitude": d.GridLongitude, "timezone": d.Timezone, "fetched_at": d.FetchedAt.UTC().Format(time.RFC3339Nano), "hours": rows}, nil
}

// ReadCache accepts a record already decoded by bounded safeio.Directory.Read.
// Every field is revalidated; changed formats, damaged cells and mismatched
// source semantics reject the optional cache without changing other forecasts.
func ReadCache(record weather.Object) (Data, error) {
	bad := errors.New("invalid precipitation cache")
	keys := []string{"version", "source", "latitude", "longitude", "grid_latitude", "grid_longitude", "timezone", "fetched_at", "hours"}
	if len(record) != len(keys) || record["version"] != 1.0 || record["source"] != cacheSource {
		return Data{}, bad
	}
	for _, key := range keys {
		if _, ok := record[key]; !ok {
			return Data{}, bad
		}
	}
	var d Data
	for key, dest := range map[string]*float64{"latitude": &d.Latitude, "longitude": &d.Longitude, "grid_latitude": &d.GridLatitude, "grid_longitude": &d.GridLongitude} {
		n, ok := record[key].(float64)
		if !ok {
			return Data{}, bad
		}
		*dest = n
	}
	var ok bool
	if d.Timezone, ok = record["timezone"].(string); !ok {
		return Data{}, bad
	}
	stamp, ok := record["fetched_at"].(string)
	if !ok || len(stamp) > 35 {
		return Data{}, bad
	}
	var err error
	d.FetchedAt, err = time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return Data{}, bad
	}
	rows, ok := record["hours"].([]any)
	if !ok || len(rows) == 0 || len(rows) > MaxHours {
		return Data{}, bad
	}
	d.Hours = make([]Hour, len(rows))
	for i, raw := range rows {
		row, ok := raw.([]any)
		if !ok || len(row) != 8 {
			return Data{}, bad
		}
		n, ok := row[0].(float64)
		if !ok || !finite(n) || n != math.Trunc(n) || n < float64(d.FetchedAt.Add(-28*time.Hour).Unix()) || n > float64(d.FetchedAt.Add(266*time.Hour).Unix()) {
			return Data{}, bad
		}
		h := &d.Hours[i]
		h.End = time.Unix(int64(n), 0).UTC()
		for j, dest := range []*Value{&h.TotalMM, &h.RainMM, &h.ShowersMM, &h.SnowCM, &h.DepthM, &h.FreezingM, &h.Probability} {
			if row[j+1] == nil {
				continue
			}
			value, ok := row[j+1].(float64)
			if !ok {
				return Data{}, bad
			}
			*dest = Number(value)
		}
	}
	if err := d.Validate(); err != nil {
		return Data{}, err
	}
	return d, nil
}
