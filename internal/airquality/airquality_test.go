package airquality

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func testTime() time.Time  { return time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC) }
func testLocation() Object { return weather.DefaultLocation() }
func payload(now time.Time) Object {
	return Object{
		"latitude": 40.7, "longitude": -74.0,
		"current_units": Object{"time": "unixtime", "interval": "seconds", "us_aqi": "USAQI", "european_aqi": "EAQI", "pm2_5": "μg/m³"},
		"current":       Object{"time": float64(now.Unix()), "interval": float64(3600), "us_aqi": float64(551), "european_aqi": float64(120), "pm2_5": float64(0)},
	}
}
func validRecord(t *testing.T) Object {
	t.Helper()
	r, e := Parse(payload(testTime()), testLocation(), testTime())
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestParseCurrentUnitsAndZero(t *testing.T) {
	r := validRecord(t)
	if r["schema_version"] != float64(1) || r["domain"] != "cams_global" || r["fetched_at"] != stamp(testTime()) || r["valid_at"] != stamp(testTime()) {
		t.Fatal("incorrect record identity/time", r)
	}
	if r["us_aqi"] != float64(551) || r["european_aqi"] != float64(120) || r["pm2_5_ug_m3"] != float64(0) {
		t.Fatal("AQ scale or zero lost", r)
	}
	if !reflect.DeepEqual(r["source"], source) || !reflect.DeepEqual(r["units"], units) {
		t.Fatal("source/units differ from fixed contract")
	}
	if e := ValidateRecord(r, testLocation()); e != nil {
		t.Fatal(e)
	}
}

func TestParseFieldIsolationAndAllNull(t *testing.T) {
	p := payload(testTime())
	object(p["current_units"])["us_aqi"] = "US AQI"
	object(p["current"])["european_aqi"] = true
	object(p["current"])["pm2_5"] = 0.0
	r, e := Parse(p, testLocation(), testTime())
	if e != nil || r["us_aqi"] != nil || r["european_aqi"] != nil || r["pm2_5_ug_m3"] != float64(0) {
		t.Fatalf("bad fields contaminated valid zero: %#v %v", r, e)
	}
	delete(object(p["current_units"]), "pm2_5")
	if _, e := Parse(p, testLocation(), testTime()); e == nil {
		t.Fatal("all-null provider result accepted")
	}
	for _, tc := range []struct {
		key string
		bad any
	}{{"us_aqi", -1.0}, {"us_aqi", 1000.1}, {"european_aqi", math.NaN()}, {"pm2_5", 5000.1}, {"pm2_5", "1"}, {"pm2_5", nil}} {
		p := payload(testTime())
		object(p["current"])[tc.key] = tc.bad
		r, e := Parse(p, testLocation(), testTime())
		if e != nil {
			t.Fatalf("individual %s failure blanked record: %v", tc.key, e)
		}
		out := tc.key
		if out == "pm2_5" {
			out = "pm2_5_ug_m3"
		}
		if r[out] != nil {
			t.Fatalf("bad %s value retained: %v", tc.key, r[out])
		}
	}
}

func TestOptionalStructuredValuesStayFieldLocalWithinBounds(t *testing.T) {
	for _, tc := range []struct {
		name string
		bad  any
	}{
		{"array", []any{1.0}},
		{"object", Object{"unexpected": 1.0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := payload(testTime())
			object(p["current"])["us_aqi"] = tc.bad
			object(p["current_units"])["european_aqi"] = tc.bad
			r, e := Parse(p, testLocation(), testTime())
			if e != nil || r["us_aqi"] != nil || r["european_aqi"] != nil || r["pm2_5_ug_m3"] != 0.0 {
				t.Fatalf("optional structured value blanked valid peer: %#v %v", r, e)
			}
		})
	}
	for _, mutate := range []func(Object){
		func(p Object) { object(p["current"])["us_aqi"] = make([]any, 17) },
		func(p Object) { object(p["current_units"])["pm2_5"] = make([]any, 17) },
		func(p Object) { object(p["current"])["us_aqi"] = Object{"nested": Object{"too_deep": 1.0}} },
		func(p Object) { object(p["current"])["other"] = []any{1.0} },
	} {
		p := payload(testTime())
		mutate(p)
		if _, e := Parse(p, testLocation(), testTime()); e == nil {
			t.Fatalf("oversize/deep/structural array accepted: %#v", p)
		}
	}
}

func TestProviderTimeAndGridBoundary(t *testing.T) {
	now := testTime()
	for _, offset := range []time.Duration{-24 * time.Hour, 5 * time.Minute} {
		p := payload(now)
		object(p["current"])["time"] = float64(now.Add(offset).Unix())
		if _, e := Parse(p, testLocation(), now); e != nil {
			t.Fatalf("allowed time boundary %v rejected: %v", offset, e)
		}
	}
	for _, offset := range []time.Duration{-24*time.Hour - time.Second, 5*time.Minute + time.Second} {
		p := payload(now)
		object(p["current"])["time"] = float64(now.Add(offset).Unix())
		if _, e := Parse(p, testLocation(), now); e == nil {
			t.Fatalf("invalid provider time %v accepted", offset)
		}
	}
	location := Object{"name": "Dateline", "latitude": -16.5, "longitude": 179.9, "timezone": "Pacific/Fiji"}
	p := payload(now)
	p["latitude"], p["longitude"] = -16.5, -179.9
	if _, e := Parse(p, location, now); e != nil {
		t.Fatalf("nearby grid across dateline rejected: %v", e)
	}
	p["longitude"] = -150.0
	if _, e := Parse(p, location, now); e == nil {
		t.Fatal("distant grid accepted")
	}
	p["longitude"] = -179.9
	delete(p, "latitude")
	if _, e := Parse(p, location, now); e == nil {
		t.Fatal("missing grid coordinate accepted")
	}
}

func TestProviderShapeAndProvenance(t *testing.T) {
	for _, mutate := range []func(Object){
		func(p Object) { p["domain"] = "cams_europe" },
		func(p Object) { p["current"] = []any{} },
		func(p Object) { object(p["current_units"])["time"] = "iso8601" },
		func(p Object) { object(p["current_units"])["interval"] = "hours" },
		func(p Object) { object(p["current"])["interval"] = 900.0 },
		func(p Object) { object(p["current"])["time"] = "2026-09-28T12:00" },
		func(p Object) { p["surprise"] = []any{1.0} },
		func(p Object) { p["current"] = Object{"nested": Object{"nested": Object{"nested": Object{}}}} },
		func(p Object) { p["extra"] = string(make([]byte, 257)) },
	} {
		p := payload(testTime())
		mutate(p)
		if _, e := Parse(p, testLocation(), testTime()); e == nil {
			t.Fatal("malformed provider object accepted", p)
		}
	}
}

func TestCacheStrictnessAndOldRecordAge(t *testing.T) {
	r := validRecord(t)
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > MaxBytes {
		t.Fatalf("AQ cache exceeds bound: %d %v", len(raw), e)
	}
	decoded, e := safeio.Object(raw, MaxBytes)
	if e == nil {
		e = ValidateRecord(decoded, testLocation())
	}
	if e != nil {
		t.Fatalf("AQ cache roundtrip invalid: %v", e)
	}
	// Cache integrity is independent of the current wall clock. The app marks
	// old records expired without treating an intact cache as corrupt.
	old := Object{}
	for k, v := range r {
		old[k] = v
	}
	old["fetched_at"] = "2020-01-01T12:00:00Z"
	old["valid_at"] = "2020-01-01T12:00:00Z"
	if e := ValidateRecord(old, testLocation()); e != nil {
		t.Fatalf("old intact cache rejected: %v", e)
	}
	mutations := []func(Object){
		func(r Object) { r["domain"] = "cams_europe" },
		func(r Object) { r["source"] = Object{"provider": "station"} },
		func(r Object) { r["units"] = Object{"us_aqi": "EAQI"} },
		func(r Object) {
			r["location"] = Object{"name": "Other", "latitude": 0.0, "longitude": 0.0, "timezone": "UTC"}
		},
		func(r Object) { r["us_aqi"] = 1000.1 },
		func(r Object) { r["european_aqi"] = true },
		func(r Object) { r["pm2_5_ug_m3"] = math.Inf(1) },
		func(r Object) { r["valid_at"] = "2026-09-29T12:00:00Z" },
		func(r Object) { r["fetched_at"] = "bad" },
		func(r Object) { delete(r, "fetched_at") },
		func(r Object) { r["extra"] = true },
		func(r Object) { r["us_aqi"], r["european_aqi"], r["pm2_5_ug_m3"] = nil, nil, nil },
	}
	for i, mutate := range mutations {
		candidate := Object{}
		for k, v := range r {
			candidate[k] = v
		}
		mutate(candidate)
		if e := ValidateRecord(candidate, testLocation()); e == nil {
			t.Fatalf("bad cache %d accepted", i)
		}
	}
	partial := Object{}
	for k, v := range r {
		partial[k] = v
	}
	partial["us_aqi"], partial["european_aqi"] = nil, nil
	if e := ValidateRecord(partial, testLocation()); e != nil {
		t.Fatalf("nullable cached fields rejected: %v", e)
	}
	delete(partial, "us_aqi")
	delete(partial, "european_aqi")
	if e := ValidateRecord(partial, testLocation()); e != nil {
		t.Fatalf("missing optional cached fields rejected: %v", e)
	}
}
