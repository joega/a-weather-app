package weather

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"
)

func fixture(zone string, start float64) Object {
	row := Object{"temperature_2m": 15.0, "apparent_temperature": 14.0, "relative_humidity_2m": 50.0, "cloud_cover": 20.0, "precipitation": 1.0, "weather_code": 61.0, "wind_speed_10m": 1.0, "wind_direction_10m": 180.0, "wind_gusts_10m": 2.0, "is_day": 1.0}
	p := Object{"timezone": zone}
	for sec, u := range map[string]Object{"current": currentUnits, "hourly": hourlyUnits, "daily": dailyUnits} {
		units := Clone(u).(Object)
		units["time"] = "unixtime"
		if sec == "current" {
			units["interval"] = "seconds"
		}
		p[sec+"_units"] = units
	}
	cur := Clone(row).(Object)
	cur["time"] = start + 900
	cur["interval"] = 900.0
	p["current"] = cur
	hr := Object{}
	times := []any{}
	for i := 0; i < 25; i++ {
		times = append(times, start+float64(i)*3600)
	}
	hr["time"] = times
	for k, v := range row {
		arr := []any{}
		for range times {
			arr = append(arr, v)
		}
		hr[k] = arr
	}
	probs, vis := []any{}, []any{}
	for i := range times {
		probs = append(probs, float64(i))
		vis = append(vis, float64(10000+i))
	}
	hr["precipitation_probability"] = probs
	hr["visibility"] = vis
	p["hourly"] = hr
	p["daily"] = Object{"time": []any{start}, "temperature_2m_max": []any{20.0}, "temperature_2m_min": []any{10.0}, "sunrise": []any{nil}, "sunset": []any{nil}, "precipitation_probability_max": []any{nil}, "weather_code": []any{nil}}
	return p
}
func sample(t *testing.T) Object {
	t.Helper()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	s, e := ParseForecast(fixture("America/New_York", float64(now.Unix())), DefaultLocation(), now)
	if e != nil {
		t.Fatal(e)
	}
	s["alerts"] = Object{"status": "unavailable", "items": []any{}, "fetched_at": nil}
	return s
}
func TestProviderCalendarAndDST(t *testing.T) {
	for _, c := range [][2]string{{"America/New_York", "2026-11-01"}, {"America/New_York", "2026-03-08"}, {"Asia/Kathmandu", "2026-09-27"}, {"Pacific/Auckland", "2026-09-27"}} {
		t.Run(c[0]+c[1], func(t *testing.T) {
			zone, _ := time.LoadLocation(c[0])
			start, _ := time.ParseInLocation("2006-01-02", c[1], zone)
			l := Object{"name": "Test", "latitude": 0.0, "longitude": 0.0, "timezone": c[0]}
			s, e := ParseForecast(fixture(c[0], float64(start.Unix())), l, start)
			if e != nil {
				t.Fatal(e)
			}
			if obj(s["daily"].([]any)[0])["date"] != c[1] {
				t.Fatal("UTC date used for local daily record")
			}
			rows := s["hourly"].([]any)
			for i := 1; i < len(rows); i++ {
				a, _ := Instant(obj(rows[i-1])["time"])
				b, _ := Instant(obj(rows[i])["time"])
				if b.Sub(a) != time.Hour {
					t.Fatal("DST altered absolute hourly ordering")
				}
			}
		})
	}
}
func TestPrecipitationIntervalsAndUnknowns(t *testing.T) {
	s := sample(t)
	c := obj(s["current"])
	if c["precipitation_rate_mm_hr"] != 4.0 || c["precipitation_probability"] != .01 || c["visibility_m"] != 10000.0 {
		t.Fatalf("wrong accumulation or interval: %#v", c)
	}
	d := obj(s["daily"].([]any)[0])
	if d["condition"] != "unknown" || d["sunrise"] != nil || d["precipitation_probability"] != nil {
		t.Fatal("unknown values fabricated")
	}
	p := fixture("America/New_York", 0)
	obj(p["current"])["time"] = 0.0
	s, e := ParseForecast(p, DefaultLocation(), time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if obj(s["current"])["precipitation_probability"] != 0.0 {
		t.Fatal("hour boundary probability must use ending hour")
	}
}
func TestProviderRejectsMalformed(t *testing.T) {
	mutations := []func(Object){func(p Object) { obj(p["current_units"])["precipitation"] = "inch" }, func(p Object) { obj(p["current"])["interval"] = true }, func(p Object) { obj(p["current"])["is_day"] = .5 }, func(p Object) { obj(p["current"])["weather_code"] = 61.5 }, func(p Object) { obj(p["current"])["temperature_2m"] = math.Inf(1) }, func(p Object) { obj(p["hourly"])["time"].([]any)[1] = 0.0 }, func(p Object) { obj(p["hourly"])["visibility"] = []any{} }, func(p Object) { obj(p["daily"])["temperature_2m_min"] = []any{21.0} }, func(p Object) { p["timezone"] = "UTC" }}
	for i, m := range mutations {
		p := fixture("America/New_York", 0)
		m(p)
		if _, e := ParseForecast(p, DefaultLocation(), time.Now()); e == nil {
			t.Fatalf("malformed fixture %d accepted", i)
		}
	}
}
func TestCacheBoundary(t *testing.T) {
	s := sample(t)
	if e := ValidateSnapshot(s, DefaultLocation()); e != nil {
		t.Fatal(e)
	}
	mutations := []func(Object){func(s Object) { s["schema_version"] = true }, func(s Object) { obj(s["current"])["humidity"] = 2.0 }, func(s Object) { s["hourly"] = []any{nil} }, func(s Object) { s["daily"] = []any{Object{"date": "2026-02-30", "condition": "clear"}} }, func(s Object) { s["alerts"] = Object{"status": "available", "items": []any{Object{"expires": "bad"}}} }, func(s Object) { obj(s["location"])["latitude"] = 0.0 }}
	for i, m := range mutations {
		candidate := Clone(s).(Object)
		m(candidate)
		if e := ValidateSnapshot(candidate, DefaultLocation()); e == nil {
			t.Fatalf("bad cache %d accepted", i)
		}
	}
}
func TestSelectFreshnessAndAlertIsolation(t *testing.T) {
	s := sample(t)
	now, _ := Instant(s["fetched_at"])
	obj(s["current"])["time"] = stamp(now)
	s["alerts"] = Object{"status": "available", "fetched_at": stamp(now), "items": []any{Object{"expires": stamp(now.Add(time.Second))}, Object{"expires": stamp(now.Add(-time.Second))}}}
	before := Clone(s)
	for _, c := range []struct {
		delta time.Duration
		fresh string
		rain  bool
	}{{0, "fresh", true}, {2701 * time.Second, "stale", true}, {7201 * time.Second, "expired", false}, {-301 * time.Second, "invalid_future", false}} {
		r := Select(s, now.Add(c.delta), "live", nil, "subtle", false, false)
		if r["freshness"] != c.fresh {
			t.Fatalf("%v -> %v", c.delta, r["freshness"])
		}
		rain := obj(r["effects"])["rain_intensity"].(float64) > 0
		if rain != c.rain {
			t.Fatal("expired effects not disabled")
		}
	}
	r := Select(s, now, "manual", Manual("snow"), "normal", true, true)
	if r["freshness"] != "manual" || obj(r["effects"])["lightning_enabled"] != false {
		t.Fatal("manual/reduced motion")
	}
	if len(obj(obj(r["forecast"])["alerts"])["items"].([]any)) != 1 {
		t.Fatal("expired alert remained")
	}
	if !reflect.DeepEqual(s, before) {
		t.Fatal("Select mutated snapshot")
	}
	r = Select(nil, now, "live", nil, "subtle", false, false)
	if r["forecast"] != nil || r["freshness"] != "unavailable" {
		t.Fatal("no-cache fallback")
	}
}
func TestVisualAndSolar(t *testing.T) {
	e, err := VisualTargets(Manual("sleet"), "normal")
	if err != nil {
		t.Fatal(err)
	}
	if e["rain_intensity"] != e["snow_intensity"] || e["wind_x"].(float64) <= 0 {
		t.Fatal("sleet split or meteorological wind reversed")
	}
	if _, e := VisualTargets(Object{"condition": "rain", "wind_speed_m_s": true}, "subtle"); e == nil {
		t.Fatal("bool accepted as number")
	}
	e, _ = VisualTargets(Object{"condition": "unknown", "precipitation_rate_mm_hr": 100.0}, "subtle")
	if e["rain_intensity"] != 0.0 {
		t.Fatal("unknown rendered precipitation")
	}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	a, b := SolarPosition(now, 40.7128, -74.006), SolarPosition(now.In(time.FixedZone("Test", 19800)), 40.7128, -74.006)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("sun depended on input zone")
	}
	if LightningFlash(8, true, true, true, 0) != 0 {
		t.Fatal("reduced motion lightning")
	}
	max := 0.0
	for i := 0; i < 24000; i++ {
		v := LightningFlash(float64(i)/1000, true, true, false, 0)
		if v > max {
			max = v
		}
		if v < 0 || v > .28 {
			t.Fatal("unbounded lightning")
		}
	}
	if max < .27 {
		t.Fatal("no lightning pulse")
	}
}
func TestZIPAndAuto(t *testing.T) {
	candidate := Object{"name": "Boston", "admin1": "Massachusetts", "country_code": "US", "latitude": 42.36, "longitude": -71.05, "timezone": "America/New_York", "postcodes": []any{"02108"}}
	p := Object{"results": []any{candidate}}
	l, e := ParseZIP(p, "02108")
	if e != nil || l["name"] != "Boston, MA" {
		t.Fatalf("ZIP: %v %v", l, e)
	}
	if _, e := ParseZIP(p, "02109"); e == nil {
		t.Fatal("prefix ZIP accepted")
	}
	p["results"] = []any{candidate, candidate}
	if _, e := ParseZIP(p, "02108"); e == nil {
		t.Fatal("ambiguous ZIP accepted")
	}
	if _, e := ValidateSelection(Object{"mode": "auto", "zip_code": nil}); e == nil {
		t.Fatal("extra selection field accepted")
	}
	local := Object{"success": true, "city": "Boston", "region": "Massachusetts", "region_code": "MA", "country_code": "US", "latitude": 42.36, "longitude": -71.05, "timezone": Object{"id": "America/New_York"}}
	if _, e := ParseLocalLocation(local); e != nil {
		t.Fatal(e)
	}
	local["region_code"] = "NY"
	if _, e := ParseLocalLocation(local); e == nil {
		t.Fatal("mismatched state accepted")
	}
	local["region_code"] = "MA"
	local["extra"] = []any{}
	if _, e := ParseLocalLocation(local); e == nil {
		t.Fatal("unsolicited local fields accepted")
	}
}
func TestEndpointAllowlist(t *testing.T) {
	for _, u := range []string{"http://api.weather.gov/alerts", "https://evil.example/", "https://user@api.weather.gov/", "https://api.weather.gov:444/", "https://ipwho.is/"} {
		if _, e := FetchJSON(context.Background(), u); e == nil {
			t.Fatal("unsafe endpoint accepted", u)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := FetchJSON(ctx, "https://api.weather.gov/alerts"); e == nil {
		t.Fatal("canceled context ignored")
	}
}
func TestAlerts(t *testing.T) {
	now := time.Now().UTC()
	p := Object{"features": []any{Object{"properties": Object{"status": "Actual", "effective": stamp(now.Add(-time.Hour)), "expires": stamp(now.Add(time.Hour)), "event": "Rain", "description": "<b>Rain</b>"}}, Object{"properties": Object{"status": "Test"}}}}
	items, e := ParseAlerts(p, now)
	if e != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, e)
	}
	r, e := AlertRecord(obj(items[0]))
	if e != nil || r["description"] != "bRain/b" {
		t.Fatal("alert plain text validation", r, e)
	}
	p["features"] = []any{nil}
	if _, e := ParseAlerts(p, now); e == nil {
		t.Fatal("malformed alert accepted")
	}
}
