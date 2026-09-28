package weather

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

var currentUnits = Object{"temperature_2m": "°C", "apparent_temperature": "°C", "relative_humidity_2m": "%", "cloud_cover": "%", "precipitation": "mm", "weather_code": "wmo code", "wind_speed_10m": "m/s", "wind_direction_10m": "°", "wind_gusts_10m": "m/s", "is_day": ""}
var hourlyUnits = func() Object {
	r := Clone(currentUnits).(Object)
	r["precipitation_probability"] = "%"
	r["visibility"] = "m"
	return r
}()
var dailyUnits = Object{"temperature_2m_max": "°C", "temperature_2m_min": "°C", "sunrise": "unixtime", "sunset": "unixtime", "precipitation_probability_max": "%", "weather_code": "wmo code"}

func ForecastURL(location Object) (string, error) {
	l, e := ValidateLocation(location)
	if e != nil {
		return "", e
	}
	q := url.Values{"latitude": {fmt.Sprint(l["latitude"])}, "longitude": {fmt.Sprint(l["longitude"])}, "temperature_unit": {"celsius"}, "wind_speed_unit": {"ms"}, "precipitation_unit": {"mm"}, "timezone": {l["timezone"].(string)}, "timeformat": {"unixtime"}, "forecast_days": {"10"}}
	for section, fields := range map[string]Object{"current": currentUnits, "hourly": hourlyUnits, "daily": dailyUnits} {
		keys := make([]string, 0, len(fields))
		for k := range fields {
			keys = append(keys, k)
		}
		q.Set(section, strings.Join(keys, ","))
	}
	return "https://api.open-meteo.com/v1/forecast?" + q.Encode(), nil
}
func units(p Object, section string, expected Object) error {
	u := obj(p[section+"_units"])
	if u == nil || u["time"] != "unixtime" {
		return errors.New("missing units")
	}
	for k, v := range expected {
		if u[k] != v {
			return fmt.Errorf("%s.%s: unexpected unit", section, k)
		}
	}
	return nil
}
func records(p Object, section string, fields Object) ([]Object, error) {
	data := obj(p[section])
	times, ok := data["time"].([]any)
	limit := 240
	if section == "daily" {
		limit = 10
	}
	if !ok || len(times) < 1 || len(times) > limit {
		return nil, errors.New("invalid record count")
	}
	for k := range fields {
		a, ok := data[k].([]any)
		if !ok || len(a) != len(times) {
			return nil, errors.New("inconsistent forecast arrays")
		}
	}
	r := make([]Object, len(times))
	prev := math.Inf(-1)
	for i, v := range times {
		if _, e := epoch(v); e != nil {
			return nil, e
		}
		n, _ := num(v)
		if n <= prev {
			return nil, errors.New("unordered forecast")
		}
		prev = n
		r[i] = Object{"time": v}
		for k := range fields {
			r[i][k] = data[k].([]any)[i]
		}
	}
	return r, nil
}
func normalize(row Object, optional bool) (Object, error) {
	t, e := epoch(row["time"])
	if e != nil {
		return nil, e
	}
	r := Object{"time": t, "sun_elevation_deg": nil}
	spec := map[string]struct {
		out      string
		lo, hi   float64
		fraction bool
	}{"temperature_2m": {"temperature_c", -150, 100, false}, "apparent_temperature": {"apparent_temperature_c", -200, 150, false}, "relative_humidity_2m": {"humidity", 0, 100, true}, "cloud_cover": {"cloud_cover", 0, 100, true}, "precipitation": {"precipitation_rate_mm_hr", 0, math.Inf(1), false}, "wind_speed_10m": {"wind_speed_m_s", 0, math.Inf(1), false}, "wind_direction_10m": {"wind_direction_deg", 0, 360, false}, "wind_gusts_10m": {"wind_gust_m_s", 0, math.Inf(1), false}}
	for k, b := range spec {
		v, e := bounded(row[k], b.lo, b.hi, optional)
		if e != nil {
			return nil, e
		}
		if v != nil && b.fraction {
			v = v.(float64) / 100
		}
		r[b.out] = v
	}
	code, e := bounded(row["weather_code"], 0, math.Inf(1), optional)
	if e != nil {
		return nil, e
	}
	r["weather_code"] = code
	r["condition"] = "unknown"
	if code != nil {
		c, e := Condition(code)
		if e != nil {
			return nil, e
		}
		r["condition"] = c
	}
	day, e := bounded(row["is_day"], 0, 1, optional)
	if e != nil {
		return nil, e
	}
	r["is_day"] = nil
	if day != nil {
		n := day.(float64)
		if n != 0 && n != 1 {
			return nil, errors.New("invalid is_day")
		}
		r["is_day"] = n == 1
	}
	return r, nil
}
func ParseForecast(p, location Object, now time.Time) (Object, error) {
	l, e := ValidateLocation(location)
	if e != nil {
		return nil, e
	}
	if z, ok := p["timezone"]; ok && z != l["timezone"] {
		return nil, errors.New("unexpected timezone")
	}
	for sec, u := range map[string]Object{"current": currentUnits, "hourly": hourlyUnits, "daily": dailyUnits} {
		if e = units(p, sec, u); e != nil {
			return nil, e
		}
	}
	if obj(p["current_units"])["interval"] != "seconds" {
		return nil, errors.New("unexpected interval unit")
	}
	row := obj(p["current"])
	current, e := normalize(row, false)
	if e != nil {
		return nil, e
	}
	iv, e := bounded(row["interval"], 1, math.Inf(1), false)
	if e != nil {
		return nil, e
	}
	current["precipitation_rate_mm_hr"] = current["precipitation_rate_mm_hr"].(float64) * 3600 / iv.(float64)
	current["precipitation_probability"] = nil
	current["visibility_m"] = nil
	hourly := []any{}
	rows, e := records(p, "hourly", hourlyUnits)
	if e != nil {
		return nil, e
	}
	ct, _ := num(row["time"])
	for _, item := range rows {
		r, e := normalize(item, true)
		if e != nil {
			return nil, e
		}
		prob, e := bounded(item["precipitation_probability"], 0, 100, true)
		if e != nil {
			return nil, e
		}
		if prob != nil {
			prob = prob.(float64) / 100
		}
		r["precipitation_probability"] = prob
		vis, e := bounded(item["visibility"], 0, math.Inf(1), true)
		if e != nil {
			return nil, e
		}
		r["visibility_m"] = vis
		ht, _ := num(item["time"])
		if ht-3600 < ct && ct <= ht {
			current["precipitation_probability"] = prob
		}
		if ht <= ct && ct < ht+3600 {
			current["visibility_m"] = vis
		}
		hourly = append(hourly, r)
	}
	daily := []any{}
	rows, e = records(p, "daily", dailyUnits)
	if e != nil {
		return nil, e
	}
	zone, _ := time.LoadLocation(l["timezone"].(string))
	for _, item := range rows {
		ts, _ := epoch(item["time"])
		tm, _ := Instant(ts)
		r := Object{"date": tm.In(zone).Format("2006-01-02"), "condition": "unknown"}
		for k, out := range map[string]string{"temperature_2m_max": "high_c", "temperature_2m_min": "low_c", "precipitation_probability_max": "precipitation_probability"} {
			lo, hi := -150.0, 100.0
			if k == "precipitation_probability_max" {
				lo = 0
			}
			v, e := bounded(item[k], lo, hi, true)
			if e != nil {
				return nil, e
			}
			if v != nil && k == "precipitation_probability_max" {
				v = v.(float64) / 100
			}
			r[out] = v
		}
		if r["high_c"] != nil && r["low_c"] != nil && r["low_c"].(float64) > r["high_c"].(float64) {
			return nil, errors.New("low exceeds high")
		}
		if item["weather_code"] != nil {
			c, e := Condition(item["weather_code"])
			if e != nil {
				return nil, e
			}
			r["condition"] = c
		}
		for _, k := range []string{"sunrise", "sunset"} {
			r[k] = nil
			if item[k] != nil {
				t, e := epoch(item[k])
				if e != nil {
					return nil, e
				}
				r[k] = t
			}
		}
		daily = append(daily, r)
	}
	return Object{"schema_version": float64(1), "location": l, "source": Object{"name": "Open-Meteo", "url": "https://open-meteo.com/", "attribution": "Weather data by Open-Meteo.com (CC BY 4.0)"}, "fetched_at": stamp(now), "current": current, "hourly": hourly, "daily": daily}, nil
}
