package precipitation

import (
	"encoding/json"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

func testTime() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
func testLocation() weather.Object {
	return weather.Object{"name": "Kathmandu", "latitude": 27.7172, "longitude": 85.324, "timezone": "Asia/Kathmandu"}
}

func payload(count int) weather.Object {
	// Real provider requests for this zone use UTC minute 15, corresponding
	// to whole local hours. The epoch, not utc_offset_seconds, is authoritative.
	start := testTime().Add(-26*time.Hour - 45*time.Minute)
	times := make([]any, count)
	for i := range times {
		times[i] = float64(start.Add(time.Duration(i) * time.Hour).Unix())
	}
	hourly := weather.Object{"time": times}
	units := weather.Object{"time": "unixtime"}
	for k, f := range fields {
		values := make([]any, count)
		for i := range values {
			values[i] = float64(k + 1)
		}
		hourly[f.name], units[f.name] = values, f.unit
	}
	return weather.Object{"latitude": 27.75, "longitude": 85.3, "timezone": "Asia/Kathmandu", "utc_offset_seconds": 20700.0, "hourly": hourly, "hourly_units": units}
}

func series(p weather.Object, name string) []any {
	return p["hourly"].(weather.Object)[name].([]any)
}

func TestRequestURLFixedScope(t *testing.T) {
	raw, err := RequestURL(testLocation())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"latitude": {"27.7172"}, "longitude": {"85.324"}, "timezone": {"Asia/Kathmandu"},
		"timeformat": {"unixtime"}, "precipitation_unit": {"mm"}, "past_hours": {"26"}, "forecast_hours": {"264"},
		"hourly": {"precipitation,rain,showers,snowfall,snow_depth,freezing_level_height,precipitation_probability"},
	}
	if u.Scheme != "https" || u.Host != "api.open-meteo.com" || u.Path != "/v1/forecast" || u.User != nil || u.Fragment != "" || !reflect.DeepEqual(u.Query(), want) {
		t.Fatalf("unexpected request: %s", raw)
	}
	for _, mutate := range []func(weather.Object){
		func(l weather.Object) { l["url"] = "https://evil.example" },
		func(l weather.Object) { l["latitude"] = "0&hourly=temperature_2m" },
		func(l weather.Object) { l["longitude"] = math.NaN() },
		func(l weather.Object) { l["timezone"] = "Local" },
	} {
		l := testLocation()
		mutate(l)
		if _, err := RequestURL(l); err == nil {
			t.Fatalf("accepted bad location: %#v", l)
		}
	}
}

func TestParseMaximumHoursAndUnits(t *testing.T) {
	p := payload(MaxHours)
	// A conflicting offset must not shift the returned epochs a second time.
	p["utc_offset_seconds"] = -18000.0
	before, _ := json.Marshal(p)
	d, err := Parse(p, testLocation(), testTime())
	if err != nil {
		t.Fatal(err)
	}
	want := Hour{End: testTime().Add(-26*time.Hour - 45*time.Minute), TotalMM: Number(1), RainMM: Number(2), ShowersMM: Number(3), SnowCM: Number(4), DepthM: Number(5), FreezingM: Number(6), Probability: Number(.07)}
	if len(d.Hours) != MaxHours || d.Hours[0] != want || d.FetchedAt != testTime() || d.Latitude != 27.7172 || d.GridLatitude != 27.75 || d.Timezone != "Asia/Kathmandu" {
		t.Fatalf("bad normalized data: %+v first=%+v", d, d.Hours[0])
	}
	if d.Hours[0].RainAmount() != Number(5) || d.Hours[0].SnowCM != Number(4) || d.Hours[0].DepthM != Number(5) {
		t.Fatal("rain, snowfall and on-ground depth confused")
	}
	series(p, "precipitation")[0] = 90.0
	if d.Hours[0].TotalMM != Number(1) {
		t.Fatal("parsed data aliases provider input")
	}
	series(p, "precipitation")[0] = 1.0
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("parse mutated provider data")
	}
}

func TestOptionalFieldsRemainUnknown(t *testing.T) {
	p := payload(3)
	series(p, "precipitation")[0] = 0.0
	series(p, "precipitation")[1] = nil
	series(p, "precipitation")[2] = -1.0
	p["hourly_units"].(weather.Object)["snowfall"] = "mm"
	p["hourly"].(weather.Object)["rain"] = []any{1.0}
	delete(p["hourly"].(weather.Object), "showers")
	series(p, "snow_depth")[0] = math.NaN()
	series(p, "snow_depth")[1] = 1001.0
	series(p, "freezing_level_height")[0] = []any{1.0}
	series(p, "freezing_level_height")[1] = "100"
	series(p, "precipitation_probability")[0] = 0.0
	series(p, "precipitation_probability")[1] = 100.0
	series(p, "precipitation_probability")[2] = 101.0
	d, err := Parse(p, testLocation(), testTime())
	if err != nil {
		t.Fatal(err)
	}
	if d.Hours[0].TotalMM != Number(0) || d.Hours[1].TotalMM.Known || d.Hours[2].TotalMM.Known {
		t.Fatal("unknown precipitation became zero")
	}
	for _, h := range d.Hours {
		if h.SnowCM.Known || h.RainMM.Known || h.ShowersMM.Known || h.RainAmount().Known {
			t.Fatal("bad units, shape or missing column accepted")
		}
	}
	if d.Hours[0].DepthM.Known || d.Hours[1].DepthM.Known || d.Hours[2].DepthM != Number(5) || d.Hours[0].FreezingM.Known || d.Hours[1].FreezingM.Known || d.Hours[2].FreezingM != Number(6) {
		t.Fatal("invalid optional value poisoned neighboring cells or became zero")
	}
	if d.Hours[0].Probability != Number(0) || d.Hours[1].Probability != Number(1) || d.Hours[2].Probability.Known {
		t.Fatal("probabilities not normalized")
	}
}

func TestRejectMalformedProviderStructure(t *testing.T) {
	cases := map[string]func(weather.Object){
		"empty":              func(p weather.Object) { p["hourly"] = weather.Object{"time": []any{}} },
		"too many":           func(p weather.Object) { p["hourly"] = payload(MaxHours + 1)["hourly"] },
		"time units":         func(p weather.Object) { p["hourly_units"].(weather.Object)["time"] = "iso8601" },
		"duplicate":          func(p weather.Object) { series(p, "time")[1] = series(p, "time")[0] },
		"reversed":           func(p weather.Object) { series(p, "time")[1] = series(p, "time")[0].(float64) - 3600 },
		"overlap":            func(p weather.Object) { series(p, "time")[1] = series(p, "time")[0].(float64) + 1800 },
		"fractional seconds": func(p weather.Object) { series(p, "time")[0] = series(p, "time")[0].(float64) + .5 },
		"non-finite":         func(p weather.Object) { series(p, "time")[0] = math.Inf(1) },
		"too old":            func(p weather.Object) { series(p, "time")[0] = float64(testTime().Add(-29 * time.Hour).Unix()) },
		"too far":            func(p weather.Object) { series(p, "time")[2] = float64(testTime().Add(267 * time.Hour).Unix()) },
		"string time":        func(p weather.Object) { series(p, "time")[0] = "2026-10-09T12:00" },
		"zone":               func(p weather.Object) { p["timezone"] = "America/New_York" },
		"missing grid":       func(p weather.Object) { delete(p, "latitude") },
		"far grid":           func(p weather.Object) { p["latitude"] = 35.0 },
		"bad grid":           func(p weather.Object) { p["longitude"] = math.NaN() },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := payload(3)
			mutate(p)
			if _, err := Parse(p, testLocation(), testTime()); err == nil {
				t.Fatal("accepted invalid provider data")
			}
		})
	}
	if _, err := Parse(payload(3), testLocation(), time.Time{}); err == nil {
		t.Fatal("accepted unknown retrieval time")
	}
	p := payload(3)
	series(p, "time")[2] = series(p, "time")[2].(float64) + 3600
	if _, err := Parse(p, testLocation(), testTime()); err != nil {
		t.Fatalf("missing whole hour must stay a gap: %v", err)
	}
}

func TestUTCAndDateLineIdentity(t *testing.T) {
	for _, alias := range []string{"UTC", "GMT", "Etc/UTC", "Etc/GMT"} {
		l, p := testLocation(), payload(3)
		l["timezone"], p["timezone"] = "UTC", alias
		l["longitude"], p["longitude"] = 179.9, -179.9
		if _, err := Parse(p, l, testTime()); err != nil {
			t.Fatalf("valid UTC alias and date-line grid: %v", err)
		}
	}
}

func TestValidateCachedData(t *testing.T) {
	d, err := Parse(payload(3), testLocation(), testTime())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Data){
		"nil hours":          func(d *Data) { d.Hours = nil },
		"bad zone":           func(d *Data) { d.Timezone = "not/a/zone" },
		"local zone":         func(d *Data) { d.Timezone = "Local" },
		"long zone":          func(d *Data) { d.Timezone = strings.Repeat("X", 101) },
		"zero retrieval":     func(d *Data) { d.FetchedAt = time.Time{} },
		"requested latitude": func(d *Data) { d.Latitude = 91 },
		"grid longitude":     func(d *Data) { d.GridLongitude = -181 },
		"unknown number":     func(d *Data) { d.Hours[0].TotalMM = Value{Number: 1} },
		"negative":           func(d *Data) { d.Hours[0].SnowCM = Number(-1) },
		"non-finite":         func(d *Data) { d.Hours[0].DepthM = Number(math.Inf(1)) },
		"probability":        func(d *Data) { d.Hours[0].Probability = Number(1.01) },
		"nanos":              func(d *Data) { d.Hours[0].End = d.Hours[0].End.Add(time.Nanosecond) },
	} {
		t.Run(name, func(t *testing.T) {
			copy := d
			copy.Hours = append([]Hour(nil), d.Hours...)
			mutate(&copy)
			if copy.Validate() == nil {
				t.Fatal("accepted corrupt cached data")
			}
		})
	}
}

func BenchmarkParseMaximum(b *testing.B) {
	p := payload(MaxHours)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(p, testLocation(), testTime()); err != nil {
			b.Fatal(err)
		}
	}
}
