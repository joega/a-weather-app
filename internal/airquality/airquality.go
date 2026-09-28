// Package airquality fetches and validates a separate current CAMS global forecast.
package airquality

import (
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

type Object = map[string]any

const MaxBytes = 8192
const Attribution = "CAMS global model data via Open-Meteo (CC BY 4.0)"
const domain = "cams_global"

var source = Object{"provider": "Open-Meteo", "model": "CAMS global model data", "kind": "model_forecast", "attribution": Attribution}
var units = Object{"us_aqi": "USAQI", "european_aqi": "EAQI", "pm2_5_ug_m3": "μg/m³"}

var fields = []struct {
	provider, record, unit string
	max                    float64
}{
	{"us_aqi", "us_aqi", "USAQI", 1000},
	{"european_aqi", "european_aqi", "EAQI", 1000},
	{"pm2_5", "pm2_5_ug_m3", "μg/m³", 5000},
}

func object(v any) Object { x, _ := v.(Object); return x }
func number(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func bounded(v any, max float64) (float64, bool) {
	n, ok := number(v)
	return n, ok && n >= 0 && n <= max
}
func epoch(v any) (time.Time, error) {
	n, ok := number(v)
	if !ok || math.Trunc(n) != n || n < -62135596800 || n >= 253402300800 {
		return time.Time{}, errors.New("invalid air quality time")
	}
	return time.Unix(int64(n), 0).UTC(), nil
}
func stamp(t time.Time) string { return t.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano) }
func instant(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.New("invalid air quality timestamp")
	}
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil || stamp(t) != s {
		return time.Time{}, errors.New("noncanonical air quality timestamp")
	}
	return t.UTC(), nil
}
func validTimes(fetched, valid time.Time) bool {
	return !valid.Before(fetched.Add(-24*time.Hour)) && !valid.After(fetched.Add(5*time.Minute))
}

// ValidateRecord is independent of wall-clock age. The app classifies an old
// but intact cache as expired, rather than treating it as corrupt.
func ValidateRecord(record, location Object) error {
	wantLocation, e := weather.ValidateLocation(location)
	if e != nil {
		return e
	}
	if len(record) < 7 || len(record) > 10 || record["schema_version"] != float64(1) || record["domain"] != domain {
		return errors.New("invalid air quality schema or domain")
	}
	for _, key := range []string{"schema_version", "location", "fetched_at", "valid_at", "domain", "source", "units"} {
		if _, present := record[key]; !present {
			return errors.New("missing air quality field")
		}
	}
	for key := range record {
		switch key {
		case "schema_version", "location", "fetched_at", "valid_at", "domain", "source", "units", "us_aqi", "european_aqi", "pm2_5_ug_m3":
		default:
			return errors.New("unknown air quality field")
		}
	}
	storedLocation, e := weather.ValidateLocation(object(record["location"]))
	if e != nil || !reflect.DeepEqual(storedLocation, wantLocation) {
		return errors.New("air quality location mismatch")
	}
	if !reflect.DeepEqual(record["source"], source) || !reflect.DeepEqual(record["units"], units) {
		return errors.New("invalid air quality provenance or units")
	}
	fetched, e := instant(record["fetched_at"])
	if e != nil {
		return e
	}
	valid, e := instant(record["valid_at"])
	if e != nil || !validTimes(fetched, valid) {
		return errors.New("invalid air quality time relation")
	}
	available := false
	for _, field := range fields {
		if record[field.record] == nil {
			continue
		}
		if _, ok := bounded(record[field.record], field.max); !ok {
			return errors.New("invalid air quality value")
		}
		available = true
	}
	if !available {
		return errors.New("air quality has no valid values")
	}
	return nil
}

func airTree(v any) error {
	nodes := 0
	var visit func(any, int, string, bool) error
	visit = func(v any, depth int, parent string, optionalMetric bool) error {
		nodes++
		if nodes > 64 || depth > 3 {
			return errors.New("air quality structure limit")
		}
		switch x := v.(type) {
		case Object:
			if len(x) > 16 {
				return errors.New("air quality object limit")
			}
			for key, item := range x {
				if len(key) > 64 {
					return errors.New("air quality key limit")
				}
				childParent := parent
				if depth == 0 {
					childParent = key
				}
				childOptional := optionalMetric
				if depth == 1 && (parent == "current" || parent == "current_units") {
					for _, field := range fields {
						if key == field.provider {
							childOptional = true
							break
						}
					}
				}
				if e := visit(item, depth+1, childParent, childOptional); e != nil {
					return e
				}
			}
		case []any:
			if !optionalMetric || len(x) > 16 {
				return errors.New("air quality arrays refused")
			}
			for _, item := range x {
				if e := visit(item, depth+1, parent, true); e != nil {
					return e
				}
			}
		case string:
			if len(x) > 256 {
				return errors.New("air quality string limit")
			}
		case nil, bool, float64, int, int64:
		default:
			return errors.New("invalid air quality tree type")
		}
		return nil
	}
	return visit(v, 0, "", false)
}

func gridClose(payload, selected Object) bool {
	lat, latOK := number(payload["latitude"])
	lon, lonOK := number(payload["longitude"])
	wantLat, _ := number(selected["latitude"])
	wantLon, _ := number(selected["longitude"])
	if !latOK || !lonOK || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return false
	}
	toRadians := math.Pi / 180
	dLat := (lat - wantLat) * toRadians
	dLon := math.Remainder(lon-wantLon, 360) * toRadians
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(lat*toRadians)*math.Cos(wantLat*toRadians)*math.Pow(math.Sin(dLon/2), 2)
	if a > 1 {
		a = 1
	}
	return 6371*2*math.Asin(math.Sqrt(a)) <= 250
}

// Parse accepts only current values from the explicitly requested global
// domain. The provider response does not attest its domain; Fetch supplies
// that provenance by using the fixed URL and query parameters.
func Parse(payload, location Object, fetchedAt time.Time) (Object, error) {
	l, e := weather.ValidateLocation(location)
	if e != nil {
		return nil, e
	}
	if fetchedAt.IsZero() || fetchedAt.Year() < 1 || fetchedAt.Year() > 9999 {
		return nil, errors.New("invalid air quality fetch time")
	}
	if e = airTree(payload); e != nil {
		return nil, e
	}
	_, claimedDomain := payload["domain"]
	_, claimedDomains := payload["domains"]
	if payload["error"] != nil && payload["error"] != false || claimedDomain || claimedDomains {
		return nil, errors.New("invalid air quality provider response")
	}
	if !gridClose(payload, l) {
		return nil, errors.New("air quality grid mismatch")
	}
	current, providerUnits := object(payload["current"]), object(payload["current_units"])
	if current == nil || providerUnits == nil || providerUnits["time"] != "unixtime" || providerUnits["interval"] != "seconds" {
		return nil, errors.New("invalid air quality current units")
	}
	interval, ok := number(current["interval"])
	if !ok || interval != 3600 {
		return nil, errors.New("invalid air quality interval")
	}
	validAt, e := epoch(current["time"])
	if e != nil || !validTimes(fetchedAt, validAt) {
		return nil, errors.New("invalid air quality provider time")
	}
	record := Object{"schema_version": float64(1), "location": l, "fetched_at": stamp(fetchedAt), "valid_at": stamp(validAt), "domain": domain, "source": Object{"provider": source["provider"], "model": source["model"], "kind": source["kind"], "attribution": source["attribution"]}, "units": Object{"us_aqi": units["us_aqi"], "european_aqi": units["european_aqi"], "pm2_5_ug_m3": units["pm2_5_ug_m3"]}}
	for _, field := range fields {
		record[field.record] = nil
		if providerUnits[field.provider] == field.unit {
			if value, ok := bounded(current[field.provider], field.max); ok {
				record[field.record] = value
			}
		}
	}
	if e = ValidateRecord(record, l); e != nil {
		return nil, e
	}
	return record, nil
}
