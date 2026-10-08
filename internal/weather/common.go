// Package weather normalizes provider data and computes bounded visual targets.
package weather

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
)

const MaxBytes = 2 * 1024 * 1024
const RefreshSeconds = 900
const StaleSeconds = 2700
const ExpireSeconds = 7200

type Object = map[string]any

func DefaultLocation() Object {
	return Object{"name": "New York, NY", "latitude": 40.7128, "longitude": -74.006, "timezone": "America/New_York"}
}
func Clone(v any) any {
	switch x := v.(type) {
	case Object:
		r := Object{}
		for k, v := range x {
			r[k] = Clone(v)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = Clone(v)
		}
		return r
	}
	return v
}
func obj(v any) Object { x, _ := v.(Object); return x }
func num(v any) (float64, bool) {
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
func bounded(v any, lo, hi float64, optional bool) (any, error) {
	if v == nil && optional {
		return nil, nil
	}
	n, ok := num(v)
	if !ok || n < lo || n > hi {
		return nil, errors.New("invalid weather number")
	}
	return n, nil
}
func Instant(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, errors.New("timestamp must be ISO8601")
	}
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return t, e
	}
	return t.UTC(), nil
}
func stamp(t time.Time) string { return t.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano) }
func epoch(v any) (string, error) {
	n, ok := num(v)
	if !ok || n < -62135596800 || n >= 253402300800 {
		return "", errors.New("invalid epoch")
	}
	s, f := math.Modf(n)
	return stamp(time.Unix(int64(s), int64(f*1e9))), nil
}
func text(v any, limit int) (string, error) {
	s, ok := v.(string)
	if !ok || s == "" || len([]rune(s)) > limit || s != strings.TrimSpace(s) {
		return "", errors.New("invalid location text")
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", errors.New("invalid location text")
		}
	}
	return s, nil
}
func ValidateLocation(v Object) (Object, error) {
	if len(v) != 4 {
		return nil, errors.New("invalid location fields")
	}
	name, e := text(v["name"], 244)
	if e != nil {
		return nil, e
	}
	zone, e := text(v["timezone"], 100)
	if e != nil {
		return nil, e
	}
	// "Local" is Go's host-dependent pseudo-zone, not an IANA timezone.
	if zone == "Local" {
		return nil, errors.New("invalid location timezone")
	}
	if _, e = time.LoadLocation(zone); e != nil {
		return nil, e
	}
	lat, e := bounded(v["latitude"], -90, 90, false)
	if e != nil {
		return nil, e
	}
	lon, e := bounded(v["longitude"], -180, 180, false)
	if e != nil {
		return nil, e
	}
	return Object{"name": name, "latitude": lat, "longitude": lon, "timezone": zone}, nil
}

var conditions = map[string]bool{"clear": true, "partly_cloudy": true, "cloudy": true, "fog": true, "drizzle": true, "rain": true, "snow": true, "sleet": true, "thunderstorm": true, "unknown": true}

func ValidCondition(c string) bool { return conditions[c] }
func Condition(v any) (string, error) {
	n, ok := num(v)
	if !ok || n < 0 || math.Trunc(n) != n {
		return "", errors.New("invalid weather code")
	}
	switch n {
	case 0:
		return "clear", nil
	case 1, 2:
		return "partly_cloudy", nil
	case 3:
		return "cloudy", nil
	case 45, 48:
		return "fog", nil
	case 51, 53, 55:
		return "drizzle", nil
	case 56, 57, 66, 67:
		return "sleet", nil
	case 61, 63, 65, 80, 81, 82:
		return "rain", nil
	case 71, 73, 75, 77, 85, 86:
		return "snow", nil
	case 95, 96, 99:
		return "thunderstorm", nil
	}
	return "unknown", nil
}

var WeatherBounds = map[string][2]float64{"temperature_c": {-150, 100}, "apparent_temperature_c": {-200, 150}, "humidity": {0, 1}, "cloud_cover": {0, 1}, "precipitation_rate_mm_hr": {0, 10000}, "precipitation_probability": {0, 1}, "wind_speed_m_s": {0, 1000}, "wind_direction_deg": {0, 360}, "wind_gust_m_s": {0, 1000}, "visibility_m": {0, 1e7}}
var OptionalWeatherBounds = map[string][2]float64{"uv_index": {0, 50}, "pressure_msl_hpa": {800, 1100}, "dew_point_c": {-100, 60}}

func WeatherRecord(row Object) (Object, error) {
	c, _ := row["condition"].(string)
	if !conditions[c] {
		return nil, errors.New("invalid weather condition")
	}
	t, e := Instant(row["time"])
	if e != nil {
		return nil, e
	}
	if row["is_day"] != nil {
		if _, ok := row["is_day"].(bool); !ok {
			return nil, errors.New("invalid daylight")
		}
	}
	r := Object{"time": stamp(t), "condition": c, "is_day": row["is_day"]}
	for k, b := range WeatherBounds {
		v, e := bounded(row[k], b[0], b[1], true)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", k, e)
		}
		r[k] = v
	}
	for k, b := range OptionalWeatherBounds {
		v, e := bounded(row[k], b[0], b[1], true)
		if e != nil {
			return nil, fmt.Errorf("%s: %w", k, e)
		}
		r[k] = v
	}
	return r, nil
}
func DailyRecord(row Object) (Object, error) {
	c, _ := row["condition"].(string)
	if !conditions[c] {
		return nil, errors.New("invalid daily condition")
	}
	date, ok := row["date"].(string)
	if !ok || len(date) != 10 {
		return nil, errors.New("invalid date")
	}
	if _, e := time.Parse("2006-01-02", date); e != nil {
		return nil, e
	}
	r := Object{"date": date, "condition": c}
	for k, b := range map[string][2]float64{"high_c": {-150, 100}, "low_c": {-150, 100}, "precipitation_probability": {0, 1}} {
		v, e := bounded(row[k], b[0], b[1], true)
		if e != nil {
			return nil, e
		}
		r[k] = v
	}
	for _, k := range []string{"sunrise", "sunset"} {
		r[k] = nil
		if row[k] != nil {
			t, e := Instant(row[k])
			if e != nil {
				return nil, e
			}
			r[k] = stamp(t)
		}
	}
	return r, nil
}
func ValidateSnapshot(s, location Object) error {
	n, ok := num(s["schema_version"])
	if !ok || n != 1 || !reflect.DeepEqual(s["location"], location) {
		return errors.New("cache schema or location mismatch")
	}
	if _, e := Instant(s["fetched_at"]); e != nil {
		return e
	}
	if _, e := WeatherRecord(obj(s["current"])); e != nil {
		return e
	}
	if _, e := VisualTargets(obj(s["current"]), "subtle"); e != nil {
		return e
	}
	for key, limit := range map[string]int{"hourly": 240, "daily": 10} {
		rows, ok := s[key].([]any)
		if !ok || len(rows) > limit {
			return errors.New("invalid cached forecast")
		}
		for _, v := range rows {
			r := obj(v)
			if r == nil {
				return errors.New("invalid cached record")
			}
			var e error
			if key == "hourly" {
				_, e = WeatherRecord(r)
			} else {
				_, e = DailyRecord(r)
			}
			if e != nil {
				return e
			}
		}
	}
	return ValidateAlerts(obj(s["alerts"]))
}

// ValidateAlerts checks an independent feed using the same persisted bounds.
func ValidateAlerts(a Object) error {
	if a == nil || (a["status"] != "available" && a["status"] != "unavailable" && a["status"] != "not_supported_here") {
		return errors.New("invalid alerts")
	}
	if coverage, present := a["coverage"]; present {
		if coverage != "US" && coverage != "unknown" && coverage != "unsupported" {
			return errors.New("invalid alert coverage")
		}
		if coverage == "unsupported" && a["status"] != "not_supported_here" || coverage == "unknown" && a["status"] != "unavailable" || coverage == "US" && a["status"] == "not_supported_here" {
			return errors.New("inconsistent alert coverage")
		}
	}
	if source, present := a["source"]; present && source != nil {
		if source != "National Weather Service" || a["coverage"] == "unknown" || a["coverage"] == "unsupported" {
			return errors.New("invalid alert source")
		}
	}
	if a["status"] == "not_supported_here" && a["source"] != nil {
		return errors.New("unsupported alert source")
	}
	if fetched, present := a["fetched_at"]; present && fetched != nil {
		if _, e := Instant(fetched); e != nil {
			return e
		}
	}
	if freshness, present := a["freshness"]; present {
		if freshness != "pending" && freshness != "unavailable" && freshness != "stale" && freshness != "current" && freshness != "not_supported_here" {
			return errors.New("invalid alert freshness")
		}
	}
	switch a["freshness"] {
	case "current", "stale":
		if a["status"] != "available" || a["fetched_at"] == nil {
			return errors.New("inconsistent alert freshness")
		}
	case "pending":
		if a["status"] != "unavailable" || a["refreshing"] != true || a["fetched_at"] != nil {
			return errors.New("inconsistent pending alerts")
		}
	case "unavailable":
		if a["status"] != "unavailable" {
			return errors.New("inconsistent unavailable alerts")
		}
	case "not_supported_here":
		if a["status"] != "not_supported_here" {
			return errors.New("inconsistent unsupported alerts")
		}
	}
	if refreshing, present := a["refreshing"]; present {
		if _, ok := refreshing.(bool); !ok {
			return errors.New("invalid alert refresh")
		}
	}
	rows, ok := a["items"].([]any)
	if !ok || len(rows) > 256 || a["status"] != "available" && len(rows) != 0 {
		return errors.New("invalid alerts")
	}
	for _, v := range rows {
		if _, e := AlertRecord(obj(v)); e != nil {
			return e
		}
	}
	return nil
}
