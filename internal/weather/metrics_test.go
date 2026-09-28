package weather

import (
	"math"
	"net/url"
	"strings"
	"testing"
	"time"
)

func metricFixture(zone string, start float64) Object {
	p := fixture(zone, start)
	for _, section := range []string{"current", "hourly"} {
		u := obj(p[section+"_units"])
		u["uv_index"] = ""
		u["pressure_msl"] = "hPa"
		u["dew_point_2m"] = "°C"
	}
	current := obj(p["current"])
	current["uv_index"] = 0.0
	current["pressure_msl"] = 1013.2
	current["dew_point_2m"] = -4.5
	hourly := obj(p["hourly"])
	count := len(hourly["time"].([]any))
	uv, pressure, dew := make([]any, count), make([]any, count), make([]any, count)
	for i := range uv {
		uv[i], pressure[i], dew[i] = float64(i)/2, 1010.0+float64(i), -3.0+float64(i)/4
	}
	hourly["uv_index"], hourly["pressure_msl"], hourly["dew_point_2m"] = uv, pressure, dew
	return p
}

func metricForecast(t *testing.T, p Object, location Object, now time.Time) Object {
	t.Helper()
	s, e := ParseForecast(p, location, now)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

func TestMetricsUseExistingForecastRequest(t *testing.T) {
	raw, e := ForecastURL(DefaultLocation())
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host != "api.open-meteo.com" || u.Path != "/v1/forecast" {
		t.Fatalf("invalid forecast endpoint: %s %v", raw, e)
	}
	for _, section := range []string{"current", "hourly"} {
		fields := strings.Split(u.Query().Get(section), ",")
		for _, metric := range optionalMetrics {
			count := 0
			for _, field := range fields {
				if field == metric.provider {
					count++
				}
			}
			if count != 1 || strings.Contains(u.Query().Get("daily"), metric.provider) {
				t.Fatalf("%s requested %s %d times or in daily", section, metric.provider, count)
			}
		}
	}
}

func TestOptionalMetricsKeepTheirOwnTimesAndZero(t *testing.T) {
	zone, _ := time.LoadLocation("Europe/Berlin")
	start, _ := time.ParseInLocation("2006-01-02", "2026-10-25", zone)
	location := Object{"name": "Berlin", "latitude": 52.52, "longitude": 13.41, "timezone": "Europe/Berlin"}
	p := metricFixture("Europe/Berlin", float64(start.Unix()))
	s := metricForecast(t, p, location, start)
	current := obj(s["current"])
	if current["uv_index"] != 0.0 || current["pressure_msl_hpa"] != 1013.2 || current["dew_point_c"] != -4.5 || current["time"] != stamp(start.Add(15*time.Minute)) {
		t.Fatalf("current metric time/value changed: %#v", current)
	}
	rows := s["hourly"].([]any)
	for i := 0; i < 3; i++ {
		row := obj(rows[i])
		if row["time"] != stamp(start.Add(time.Duration(i)*time.Hour)) || row["uv_index"] != float64(i)/2 || row["pressure_msl_hpa"] != 1010.0+float64(i) {
			t.Fatalf("hourly metric shifted across DST: %#v", row)
		}
	}
	if e := ValidateSnapshot(withUnavailableAlerts(s), location); e != nil {
		t.Fatalf("normalized metrics failed cache validation: %v", e)
	}
}

func withUnavailableAlerts(s Object) Object {
	s["alerts"] = Object{"status": "unavailable", "items": []any{}, "fetched_at": nil}
	return s
}

func TestOptionalMetricFailuresIsolateFieldAndSection(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	start := float64(now.Unix())
	p := metricFixture("America/New_York", start)
	obj(p["current_units"])["uv_index"] = "%"
	delete(obj(p["current_units"]), "pressure_msl")
	obj(p["current"])["dew_point_2m"] = "bad"
	obj(p["hourly_units"])["pressure_msl"] = "Pa"
	obj(p["hourly"])["dew_point_2m"] = []any{2.0} // wrong series length
	uv := obj(p["hourly"])["uv_index"].([]any)
	uv[0], uv[1], uv[2], uv[3], uv[4] = 0.0, nil, true, 51.0, math.NaN()
	s := metricForecast(t, p, DefaultLocation(), now)
	current := obj(s["current"])
	for _, key := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
		if current[key] != nil {
			t.Fatalf("bad current %s retained: %#v", key, current[key])
		}
	}
	rows := s["hourly"].([]any)
	for i := 0; i < 5; i++ {
		row := obj(rows[i])
		if row["pressure_msl_hpa"] != nil || row["dew_point_c"] != nil {
			t.Fatalf("bad series retained at %d: %#v", i, row)
		}
		if (i == 0 && row["uv_index"] != 0.0) || (i > 0 && row["uv_index"] != nil) {
			t.Fatalf("UV value isolation failed at %d: %#v", i, row)
		}
		if row["temperature_c"] != 15.0 {
			t.Fatal("optional failure blanked core weather")
		}
	}
	if obj(rows[5])["uv_index"] != 2.5 {
		t.Fatal("valid later UV value lost")
	}
	if e := ValidateSnapshot(withUnavailableAlerts(s), DefaultLocation()); e != nil {
		t.Fatalf("optional failure blanked forecast cache: %v", e)
	}
}

func TestOptionalMetricMissingArrayAndPerValueBounds(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	p := metricFixture("America/New_York", float64(now.Unix()))
	delete(obj(p["hourly"]), "pressure_msl")
	obj(p["current"])["pressure_msl"] = 799.9
	obj(p["current"])["dew_point_2m"] = 60.1
	dew := obj(p["hourly"])["dew_point_2m"].([]any)
	dew[0], dew[1] = -100.1, -100.0
	s := metricForecast(t, p, DefaultLocation(), now)
	if obj(s["current"])["uv_index"] != 0.0 || obj(s["current"])["pressure_msl_hpa"] != nil || obj(s["current"])["dew_point_c"] != nil {
		t.Fatal("invalid current values affected independent UV")
	}
	rows := s["hourly"].([]any)
	for _, raw := range rows {
		if obj(raw)["pressure_msl_hpa"] != nil {
			t.Fatal("missing pressure array became a number")
		}
	}
	if obj(rows[0])["dew_point_c"] != nil || obj(rows[1])["dew_point_c"] != -100.0 || obj(rows[2])["dew_point_c"] != -2.5 {
		t.Fatal("dew point bounds affected adjacent timestamps")
	}
}

func TestOptionalMetricMissingAndCachedBounds(t *testing.T) {
	s := sample(t) // old provider payload and cache have no point metrics
	if e := ValidateSnapshot(s, DefaultLocation()); e != nil {
		t.Fatalf("old cache rejected: %v", e)
	}
	for _, key := range []string{"uv_index", "pressure_msl_hpa", "dew_point_c"} {
		if obj(s["current"])[key] != nil || obj(s["hourly"].([]any)[0])[key] != nil {
			t.Fatalf("missing %s became a number", key)
		}
	}
	bad := map[string]any{"uv_index": -0.1, "pressure_msl_hpa": 1100.1, "dew_point_c": math.Inf(1)}
	for key, value := range bad {
		candidate := Clone(s).(Object)
		obj(candidate["current"])[key] = value
		if e := ValidateSnapshot(candidate, DefaultLocation()); e == nil {
			t.Fatalf("invalid cached current %s accepted", key)
		}
		candidate = Clone(s).(Object)
		obj(candidate["hourly"].([]any)[0])[key] = value
		if e := ValidateSnapshot(candidate, DefaultLocation()); e == nil {
			t.Fatalf("invalid cached hourly %s accepted", key)
		}
	}
}
