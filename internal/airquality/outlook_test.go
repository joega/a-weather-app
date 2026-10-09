package airquality

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

func outlookPayload(at time.Time) Object {
	l := testLocation()
	h, units := Object{}, Object{"time": "unixtime"}
	times := make([]any, OutlookHours)
	for i := range times {
		times[i] = float64(at.Truncate(time.Hour).Add(time.Duration(i) * time.Hour).Unix())
	}
	h["time"] = times
	for k, f := range OutlookMetrics() {
		rows := make([]any, len(times))
		for i := range rows {
			rows[i] = float64(k*10 + i)
		}
		h[f.Key], units[f.Key] = rows, f.Unit
	}
	return Object{"latitude": l["latitude"], "longitude": l["longitude"], "timezone": "GMT", "utc_offset_seconds": float64(0), "hourly": h, "hourly_units": units}
}
func validOutlook(t *testing.T) Outlook {
	t.Helper()
	d, err := ParseOutlook(outlookPayload(testTime()), testLocation(), testTime())
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestOutlookFixedRequestAndTypedValues(t *testing.T) {
	raw, err := OutlookRequestURL(testLocation())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	want := url.Values{"latitude": {"40.7128"}, "longitude": {"-74.006"}, "domains": {"cams_global"}, "timezone": {"GMT"}, "timeformat": {"unixtime"}, "forecast_hours": {"48"}, "hourly": {"us_aqi,european_aqi,pm2_5,pm10,nitrogen_dioxide,ozone,sulphur_dioxide,carbon_monoxide"}}
	if u.Scheme != "https" || u.Host != endpointHost || u.Path != endpointPath || !reflect.DeepEqual(u.Query(), want) {
		t.Fatal("wrong fixed outlook query", raw)
	}
	l := testLocation()
	l["latitude"] = "0&domains=auto"
	if _, err := OutlookRequestURL(l); err == nil {
		t.Fatal("coordinate injection")
	}
	d := validOutlook(t)
	if len(d.Hours) != 48 || d.Timezone != testLocation()["timezone"] || d.Hours[0].Values[0] != (OutlookValue{Known: true}) || d.Hours[47].Values[7].Number != 117 {
		t.Fatal("lost known zero or values", d)
	}
	for k, f := range OutlookMetrics() {
		if !d.Hours[0].Values[k].Known || f.Unit == "" {
			t.Fatal("missing field", k)
		}
	}
	metrics := OutlookMetrics()
	metrics[0].Key = "mutated"
	if OutlookMetrics()[0].Key != "us_aqi" {
		t.Fatal("mutable shared schema")
	}
}

func TestOutlookUnknownFieldsAndGaps(t *testing.T) {
	for name, mutate := range map[string]func(Object){
		"missing column":  func(p Object) { delete(object(p["hourly"]), "pm2_5") },
		"wrong unit":      func(p Object) { object(p["hourly_units"])["pm2_5"] = "mg/m³" },
		"short column":    func(p Object) { object(p["hourly"])["pm2_5"] = []any{1.0} },
		"null cell":       func(p Object) { object(p["hourly"])["pm2_5"].([]any)[0] = nil },
		"structured cell": func(p Object) { object(p["hourly"])["pm2_5"].([]any)[0] = Object{"value": 0.0} },
		"negative":        func(p Object) { object(p["hourly"])["pm2_5"].([]any)[0] = -1.0 },
		"oversized":       func(p Object) { object(p["hourly"])["pm2_5"].([]any)[0] = 5001.0 },
		"nan":             func(p Object) { object(p["hourly"])["pm2_5"].([]any)[0] = math.NaN() },
	} {
		t.Run(name, func(t *testing.T) {
			p := outlookPayload(testTime())
			mutate(p)
			d, err := ParseOutlook(p, testLocation(), testTime())
			if err != nil || d.Hours[0].Values[2].Known || !d.Hours[0].Values[0].Known {
				t.Fatal("optional field contaminated others", err)
			}
		})
	}
	p := outlookPayload(testTime())
	h := object(p["hourly"])
	for k, raw := range h {
		rows := raw.([]any)
		h[k] = append(rows[:8:8], rows[9:]...)
	}
	d, err := ParseOutlook(p, testLocation(), testTime())
	if err != nil || len(d.Hours) != 47 || d.Hours[8].Time.Sub(d.Hours[7].Time) != 2*time.Hour {
		t.Fatal("gap was filled or discarded", err)
	}
	p = outlookPayload(testTime())
	for _, f := range OutlookMetrics() {
		delete(object(p["hourly"]), f.Key)
	}
	if _, err := ParseOutlook(p, testLocation(), testTime()); err == nil {
		t.Fatal("all-unknown product accepted")
	}
}

func TestOutlookProviderAndCacheBoundaries(t *testing.T) {
	for name, mutate := range map[string]func(Object){
		"far grid":        func(p Object) { p["latitude"] = 0.0 },
		"bad grid":        func(p Object) { p["longitude"] = math.NaN() },
		"wrong zone":      func(p Object) { p["timezone"] = "Asia/Tokyo" },
		"offset":          func(p Object) { p["utc_offset_seconds"] = 3600.0 },
		"missing offset":  func(p Object) { delete(p, "utc_offset_seconds") },
		"error":           func(p Object) { p["error"] = true },
		"claimed domain":  func(p Object) { p["domain"] = "cams_europe" },
		"wrong time unit": func(p Object) { object(p["hourly_units"])["time"] = "iso8601" },
		"empty":           func(p Object) { object(p["hourly"])["time"] = []any{} },
		"many rows": func(p Object) {
			h := object(p["hourly"])
			h["time"] = append(h["time"].([]any), float64(testTime().Add(48*time.Hour).Unix()))
		},
		"fraction":   func(p Object) { object(p["hourly"])["time"].([]any)[0] = float64(testTime().Unix()) + .1 },
		"not hourly": func(p Object) { object(p["hourly"])["time"].([]any)[0] = float64(testTime().Unix() + 1) },
		"repeat":     func(p Object) { h := object(p["hourly"])["time"].([]any); h[1] = h[0] },
		"reverse":    func(p Object) { h := object(p["hourly"])["time"].([]any); h[0], h[1] = h[1], h[0] },
		"past": func(p Object) {
			object(p["hourly"])["time"].([]any)[0] = float64(testTime().Add(-3 * time.Hour).Unix())
		},
		"future": func(p Object) {
			object(p["hourly"])["time"].([]any)[47] = float64(testTime().Add(51 * time.Hour).Unix())
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := outlookPayload(testTime())
			mutate(p)
			if _, err := ParseOutlook(p, testLocation(), testTime()); err == nil {
				t.Fatal("invalid provider accepted")
			}
		})
	}
	d := validOutlook(t)
	d.Hours[0].Values[2] = OutlookValue{}
	record, err := OutlookCacheRecord(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > OutlookCacheBytes {
		t.Fatal("cache exceeded limit", len(raw), err)
	}
	record, err = safeio.Object(raw, OutlookCacheBytes)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ReadOutlookCache(record)
	if err != nil || !reflect.DeepEqual(back, d) {
		t.Fatal("cache changed data", err)
	}
	for name, mutate := range map[string]func(Object){
		"version":           func(p Object) { p["version"] = 2.0 },
		"source":            func(p Object) { p["source"] = "cams_europe" },
		"extra":             func(p Object) { p["extra"] = nil },
		"missing":           func(p Object) { delete(p, "timezone") },
		"string coordinate": func(p Object) { p["latitude"] = "40" },
		"bad time":          func(p Object) { p["fetched_at"] = "tomorrow" },
		"null rows":         func(p Object) { p["hours"] = nil },
		"short row":         func(p Object) { p["hours"].([]any)[0] = []any{1.0} },
		"wrong cell":        func(p Object) { p["hours"].([]any)[0].([]any)[1] = "0" },
		"bad number":        func(p Object) { p["hours"].([]any)[0].([]any)[1] = -1.0 },
		"epoch overflow":    func(p Object) { p["hours"].([]any)[0].([]any)[0] = 1e30 },
	} {
		t.Run("cache "+name, func(t *testing.T) {
			p, _ := safeio.Object(raw, OutlookCacheBytes)
			mutate(p)
			if _, err := ReadOutlookCache(p); err == nil {
				t.Fatal("bad cache accepted")
			}
		})
	}
	// Upper bound includes maximal-length finite float encodings in all columns.
	for i := range d.Hours {
		for k := range d.Hours[i].Values {
			d.Hours[i].Values[k] = OutlookValue{Number: 123.12345678912345, Known: true}
		}
	}
	record, err = OutlookCacheRecord(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(record)
	if len(raw) > OutlookCacheBytes {
		t.Fatal("worst-row cache exceeds bound", len(raw))
	}
	t.Log("48-row long-float cache bytes", len(raw))
}

func TestOutlookHTTPBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "redirect", "status", "duplicate", "trailing", "gzip-limit", "limit", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			hits := 0
			transport := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Host != endpointHost || r.URL.Path != endpointPath || r.URL.Query().Get("domains") != "cams_global" || r.URL.Query().Get("forecast_hours") != "48" || r.URL.Query().Has("current") {
					t.Error("wrong outlook request", r.URL)
				}
				switch mode {
				case "redirect":
					w.Header().Set("Location", "https://example.com/other")
					w.WriteHeader(302)
				case "status":
					w.WriteHeader(503)
				case "duplicate":
					_, _ = w.Write([]byte(`{"latitude":1,"latitude":2}`))
				case "trailing":
					_ = json.NewEncoder(w).Encode(outlookPayload(testTime()))
					_, _ = w.Write([]byte(`{}`))
				case "limit":
					_, _ = w.Write([]byte(strings.Repeat(" ", OutlookResponseBytes+1)))
				case "gzip-limit":
					w.Header().Set("Content-Encoding", "gzip")
					z := gzip.NewWriter(w)
					_, _ = z.Write([]byte(strings.Repeat(" ", OutlookResponseBytes+1)))
					_ = z.Close()
				default:
					_ = json.NewEncoder(w).Encode(outlookPayload(testTime()))
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			d, err := fetchOutlook(ctx, testLocation(), testTime(), transport)
			if mode == "valid" {
				if err != nil || len(d.Hours) != 48 {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("bad HTTP accepted")
			}
			if mode == "canceled" {
				if !errors.Is(err, context.Canceled) || hits != 0 {
					t.Fatal("cancellation lost", err, hits)
				}
			} else if hits != 1 {
				t.Fatal("redirect or retry", hits)
			}
		})
	}
}

func BenchmarkOutlookParse(b *testing.B) {
	p, location, at := outlookPayload(testTime()), testLocation(), testTime()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ParseOutlook(p, location, at); err != nil {
			b.Fatal(err)
		}
	}
}
