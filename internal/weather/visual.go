package weather

import (
	"errors"
	"math"
	"time"
)

func clamp(x, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, x)) }
func VisualTargets(current Object, strength string) (Object, error) {
	if current == nil {
		return nil, errors.New("missing current")
	}
	scales, ok := map[string][3]float64{"subtle": {6, .65, 30}, "normal": {4, .85, 22}, "immersive": {2.5, 1, 15}}[strength]
	if !ok {
		return nil, errors.New("unknown strength")
	}
	c := "unknown"
	if v, ok := current["condition"]; ok {
		c, ok = v.(string)
		if !ok || !conditions[c] {
			return nil, errors.New("unknown condition")
		}
	}
	day := true
	if current["is_day"] != nil {
		var ok bool
		day, ok = current["is_day"].(bool)
		if !ok {
			return nil, errors.New("invalid daylight")
		}
	}
	n := map[string]float64{}
	for _, k := range []string{"precipitation_rate_mm_hr", "precipitation_probability", "probability", "temperature_c", "wind_gust_m_s", "wind_speed_m_s", "wind_direction_deg", "cloud_cover", "visibility_m"} {
		if current[k] != nil {
			v, ok := num(current[k])
			if !ok {
				return nil, errors.New("invalid visual observation")
			}
			n[k] = v
		}
	}
	p := scales[1] * (-math.Expm1(-math.Min(math.Max(0, n["precipitation_rate_mm_hr"]), 1000) / scales[0]))
	rain, snow := 0.0, 0.0
	switch c {
	case "rain", "drizzle", "thunderstorm":
		rain = p
	case "snow":
		snow = p
	case "sleet":
		rain = p * .5
		snow = p * .5
	}
	horizontal := 0.0
	if current["wind_direction_deg"] != nil {
		horizontal = -math.Sin(n["wind_direction_deg"]*math.Pi/180) * clamp(n["wind_speed_m_s"], 0, 100)
	}
	wind := 500 * math.Tanh(horizontal/scales[2])
	if math.Abs(wind) < 1e-10 {
		wind = 0
	}
	fog := 0.0
	if current["visibility_m"] != nil {
		fog = clamp((2000-math.Max(0, n["visibility_m"]))/2000, 0, 1)
	}
	return Object{"rain_intensity": clamp(rain, 0, 1), "snow_intensity": clamp(snow, 0, 1), "wind_x": clamp(wind, -500, 500), "cloud_cover": clamp(n["cloud_cover"], 0, 1), "fog_density": fog * scales[1], "is_day": day, "thunderstorm": c == "thunderstorm"}, nil
}
func Manual(condition string) Object {
	rate := map[string]float64{"drizzle": .7, "rain": 4, "snow": 2, "sleet": 3, "thunderstorm": 8}[condition]
	clouds, ok := map[string]float64{"clear": .05, "partly_cloudy": .45, "cloudy": .95, "fog": .7}[condition]
	if !ok {
		clouds = 1
	}
	visibility := 10000.0
	if condition == "fog" {
		visibility = 500
	}
	return Object{"condition": condition, "precipitation_rate_mm_hr": rate, "cloud_cover": clouds, "visibility_m": visibility, "wind_speed_m_s": 6.0, "wind_direction_deg": 270.0, "is_day": true}
}
func SolarPosition(t time.Time, lat, lon float64) Object {
	u := t.UTC()
	hour := float64(u.Hour()) + float64(u.Minute())/60 + (float64(u.Second())+float64(u.Nanosecond())/1e9)/3600
	days := 365.0
	if u.Year()%4 == 0 && (u.Year()%100 != 0 || u.Year()%400 == 0) {
		days = 366
	}
	g := 2 * math.Pi / days * (float64(u.YearDay()-1) + (hour-12)/24)
	eq := 229.18 * (.000075 + .001868*math.Cos(g) - .032077*math.Sin(g) - .014615*math.Cos(2*g) - .040849*math.Sin(2*g))
	dec := .006918 - .399912*math.Cos(g) + .070257*math.Sin(g) - .006758*math.Cos(2*g) + .000907*math.Sin(2*g) - .002697*math.Cos(3*g) + .00148*math.Sin(3*g)
	minutes := math.Mod(hour*60+eq+4*lon, 1440)
	if minutes < 0 {
		minutes += 1440
	}
	ha := (minutes/4 - 180) * math.Pi / 180
	lat *= math.Pi / 180
	up := math.Sin(lat)*math.Sin(dec) + math.Cos(lat)*math.Cos(dec)*math.Cos(ha)
	elevation := math.Asin(clamp(up, -1, 1)) * 180 / math.Pi
	east := -math.Cos(dec) * math.Sin(ha)
	north := math.Cos(lat)*math.Sin(dec) - math.Sin(lat)*math.Cos(dec)*math.Cos(ha)
	az := 0.0
	if math.Hypot(east, north) > 1e-12 {
		az = math.Mod(math.Atan2(east, north)*180/math.Pi+360, 360)
	}
	twilight := "night"
	switch {
	case elevation >= 0:
		twilight = "day"
	case elevation >= -6:
		twilight = "civil"
	case elevation >= -12:
		twilight = "nautical"
	case elevation >= -18:
		twilight = "astronomical"
	}
	ramp := clamp((elevation+6)/12, 0, 1)
	return Object{"elevation_deg": elevation, "azimuth_deg": az, "is_day": elevation >= 0, "twilight": twilight, "daylight": ramp * ramp * (3 - 2*ramp)}
}
func LightningFlash(seconds float64, storm, enabled, reduced bool, seed int64) float64 {
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || !storm || !enabled || reduced {
		return 0
	}
	cycle := int64(math.Floor(seconds / 24))
	mixed := uint32((cycle+seed)*1664525 + 1013904223)
	onset := 4 + float64(mixed%14000)/1000
	local := math.Mod(seconds, 24)
	pulse := func(start, duration, peak float64) float64 {
		phase := (local - start) / duration
		if phase <= 0 || phase >= 1 {
			return 0
		}
		s := math.Sin(math.Pi * phase)
		return peak * s * s
	}
	v := pulse(onset, .18, .28)
	if mixed&1 != 0 {
		v += pulse(onset+.8, .12, .09)
	}
	return v
}

// Select assumes validated controls and snapshots. Invalid caller inputs safely disable effects.
func Select(snapshot Object, now time.Time, mode string, manual Object, strength string, reduced, lightning bool) Object {
	return selectWeather(snapshot, now, mode, manual, strength, reduced, lightning, true)
}

// SelectView borrows immutable forecast rows for a lock-protected display read.
// Its forecast must not escape to a mutable caller. The app normalizes the rows
// and structurally copies its final display snapshot before publishing it.
func SelectView(snapshot Object, now time.Time, mode string, manual Object, strength string, reduced, lightning bool) Object {
	return selectWeather(snapshot, now, mode, manual, strength, reduced, lightning, false)
}

func selectWeather(snapshot Object, now time.Time, mode string, manual Object, strength string, reduced, lightning, copyForecast bool) Object {
	if now.IsZero() {
		now = time.Now()
	}
	location := DefaultLocation()
	if snapshot != nil && obj(snapshot["location"]) != nil {
		location = obj(snapshot["location"])
	}
	lat, _ := num(location["latitude"])
	lon, _ := num(location["longitude"])
	solar := SolarPosition(now, lat, lon)
	var forecastValue any
	if snapshot != nil {
		if copyForecast {
			forecastValue = Clone(snapshot)
		} else {
			view := Object{}
			for k, v := range snapshot {
				view[k] = v
			}
			view["alerts"] = Clone(obj(snapshot["alerts"]))
			forecastValue = view
		}
	}
	r := Object{"schema_version": float64(1), "mode": mode, "selected_at": stamp(now), "forecast": forecastValue, "error": nil, "solar": solar}
	if snapshot == nil {
		r["forecast"] = nil
	}
	if forecast := obj(r["forecast"]); forecast != nil {
		alerts := obj(forecast["alerts"])
		if alerts == nil {
			alerts = Object{}
		}
		f, e := Instant(alerts["fetched_at"])
		age := now.Sub(f).Seconds()
		if alerts["status"] == "not_supported_here" {
			alerts["items"] = []any{}
		} else if e != nil || age < 0 || age > StaleSeconds {
			alerts["status"] = "unavailable"
			alerts["items"] = []any{}
		} else {
			items := []any{}
			rows, _ := alerts["items"].([]any)
			for _, v := range rows {
				a := obj(v)
				exp, e := Instant(a["expires"])
				if e == nil && exp.After(now) {
					items = append(items, a)
				}
			}
			alerts["items"] = items
		}
		forecast["alerts"] = alerts
	}
	current := Object{"condition": "unknown"}
	freshness := "unavailable"
	var age any
	usable := false
	if mode == "manual" {
		current = Clone(manual).(Object)
		freshness = "manual"
		usable = true
	} else if snapshot != nil {
		current = Clone(obj(snapshot["current"])).(Object)
		f, e1 := Instant(snapshot["fetched_at"])
		c, e2 := Instant(current["time"])
		a, b := now.Sub(f).Seconds(), now.Sub(c).Seconds()
		if e1 != nil || e2 != nil {
			freshness = "unavailable"
		} else if math.Min(a, b) < -300 {
			freshness = "invalid_future"
		} else {
			n := math.Max(0, math.Max(a, b))
			age = n
			switch {
			case n <= StaleSeconds:
				freshness = "fresh"
			case n <= ExpireSeconds:
				freshness = "stale"
			default:
				freshness = "expired"
			}
			usable = freshness == "fresh" || freshness == "stale"
		}
	}
	effective := current
	if !usable {
		effective = Object{"condition": "unknown"}
	}
	effects, e := VisualTargets(effective, strength)
	if e != nil {
		effects, _ = VisualTargets(Object{"condition": "unknown"}, "subtle")
		r["error"] = "invalid weather controls"
	}
	elevation := solar["elevation_deg"].(float64)
	az := solar["azimuth_deg"]
	if mode == "manual" {
		elevation = 35
		if current["is_day"] == false {
			elevation = -25
		}
		if current["sun_elevation_deg"] != nil {
			if n, ok := num(current["sun_elevation_deg"]); ok && n >= -90 && n <= 90 {
				elevation = n
			} else {
				r["error"] = "invalid manual solar elevation"
			}
		}
		az = 180.0
	}
	effects["sun_elevation"] = elevation
	effects["sun_azimuth"] = az
	effects["is_day"] = elevation >= 0
	effects["reduced_motion"] = reduced
	effects["lightning_enabled"] = lightning && !reduced
	r["current"] = current
	r["freshness"] = freshness
	r["age_seconds"] = age
	r["effects"] = effects
	return r
}
