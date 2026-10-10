package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func currentSummaryFixture(now time.Time) Object {
	return Object{
		"timezone": "America/New_York",
		"current_units": Object{
			"time": "unixtime", "temperature_2m": "°C", "weather_code": "wmo code", "is_day": "",
		},
		"current": Object{"time": float64(now.Add(-15 * time.Minute).Unix()), "temperature_2m": 13.5, "weather_code": 71, "is_day": 0},
	}
}

func TestCurrentSummaryURL(t *testing.T) {
	raw, err := CurrentSummaryURL(DefaultLocation())
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"latitude": {"40.7128"}, "longitude": {"-74.006"},
		"current":          {"temperature_2m,weather_code,is_day"},
		"temperature_unit": {"celsius"}, "timeformat": {"unixtime"}, "timezone": {"America/New_York"},
	}
	if u.Scheme != "https" || u.Host != "api.open-meteo.com" || u.Path != "/v1/forecast" || !reflect.DeepEqual(u.Query(), want) {
		t.Fatalf("unexpected current-only URL: %s", raw)
	}
	bad := DefaultLocation()
	bad["timezone"] = "Local"
	if _, err := CurrentSummaryURL(bad); err == nil {
		t.Fatal("accepted host-dependent location")
	}
	bad = DefaultLocation()
	bad["latitude"] = 91
	if _, err := CurrentSummaryURL(bad); err == nil {
		t.Fatal("accepted invalid coordinates")
	}
}

func TestCurrentSummaryParse(t *testing.T) {
	now := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	got, err := ParseCurrentSummary(currentSummaryFixture(now), DefaultLocation(), now)
	want := Object{"fetched_at": "2026-10-10T16:00:00Z", "valid_at": "2026-10-10T15:45:00Z", "temperature_c": 13.5, "condition": "snow", "is_day": false}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("summary = %#v, %v; want %#v", got, err, want)
	}
	for _, offset := range []time.Duration{-45 * time.Minute, 5 * time.Minute} {
		p := currentSummaryFixture(now)
		obj(p["current"])["time"] = float64(now.Add(offset).Unix())
		obj(p["current"])["is_day"] = 1
		if got, err := ParseCurrentSummary(p, DefaultLocation(), now); err != nil || got["is_day"] != true {
			t.Fatalf("freshness boundary %v: %#v, %v", offset, got, err)
		}
	}
	cases := map[string]func(Object){
		"missing current":       func(p Object) { delete(p, "current") },
		"missing units":         func(p Object) { delete(p, "current_units") },
		"fahrenheit":            func(p Object) { obj(p["current_units"])["temperature_2m"] = "°F" },
		"wrong time units":      func(p Object) { obj(p["current_units"])["time"] = "iso8601" },
		"wrong code units":      func(p Object) { obj(p["current_units"])["weather_code"] = "code" },
		"wrong day units":       func(p Object) { obj(p["current_units"])["is_day"] = "bool" },
		"wrong timezone":        func(p Object) { p["timezone"] = "UTC" },
		"missing time":          func(p Object) { delete(obj(p["current"]), "time") },
		"future":                func(p Object) { obj(p["current"])["time"] = float64(now.Add(5*time.Minute + time.Second).Unix()) },
		"stale":                 func(p Object) { obj(p["current"])["time"] = float64(now.Add(-45*time.Minute - time.Second).Unix()) },
		"null temperature":      func(p Object) { obj(p["current"])["temperature_2m"] = nil },
		"string temperature":    func(p Object) { obj(p["current"])["temperature_2m"] = "13" },
		"temperature bounds":    func(p Object) { obj(p["current"])["temperature_2m"] = 101 },
		"nonfinite temperature": func(p Object) { obj(p["current"])["temperature_2m"] = math.NaN() },
		"null code":             func(p Object) { obj(p["current"])["weather_code"] = nil },
		"fractional code":       func(p Object) { obj(p["current"])["weather_code"] = 71.5 },
		"null day":              func(p Object) { obj(p["current"])["is_day"] = nil },
		"boolean day":           func(p Object) { obj(p["current"])["is_day"] = true },
		"fractional day":        func(p Object) { obj(p["current"])["is_day"] = .5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := currentSummaryFixture(now)
			mutate(p)
			if _, err := ParseCurrentSummary(p, DefaultLocation(), now); err == nil {
				t.Fatal("accepted malformed summary")
			}
		})
	}
}

func TestCurrentSummaryTransport(t *testing.T) {
	now := time.Date(2026, 10, 10, 16, 0, 0, 0, time.UTC)
	var mode atomic.Int32
	var hits atomic.Int32
	provider := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "api.open-meteo.com" || r.URL.Path != "/v1/forecast" || r.URL.Query().Get("current") != "temperature_2m,weather_code,is_day" || r.URL.Query().Has("hourly") || r.URL.Query().Has("daily") {
			t.Errorf("unexpected request: %s %s", r.Host, r.URL)
		}
		switch mode.Load() {
		case 1:
			fmt.Fprint(w, strings.Repeat(" ", currentSummaryMaxBytes+1))
		case 2:
			w.Header().Set("Location", "https://api.weather.gov/alerts/active")
			w.WriteHeader(http.StatusFound)
		case 3:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 4:
			<-r.Context().Done()
		case 5:
			fmt.Fprint(w, `{"current":{},"current":{}}`)
		default:
			_ = json.NewEncoder(w).Encode(currentSummaryFixture(now))
		}
	})
	got, err := provider.fetchCurrentSummary(context.Background(), DefaultLocation(), now)
	if err != nil || len(got) != 5 || got["condition"] != "snow" {
		t.Fatalf("valid fetch: %#v, %v", got, err)
	}
	for _, tc := range []struct {
		name string
		mode int32
	}{{"body limit", 1}, {"redirect", 2}, {"status", 3}, {"duplicate JSON", 5}} {
		t.Run(tc.name, func(t *testing.T) {
			mode.Store(tc.mode)
			before := hits.Load()
			if _, err := provider.fetchCurrentSummary(context.Background(), DefaultLocation(), now); err == nil {
				t.Fatal("accepted unsafe response")
			}
			if hits.Load() != before+1 {
				t.Fatal("unexpected extra request or redirect")
			}
		})
	}
	mode.Store(4)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := provider.fetchCurrentSummary(ctx, DefaultLocation(), now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation = %v", err)
	}
	mode.Store(0)
	if _, err := provider.fetchCurrentSummary(context.Background(), DefaultLocation(), now); err != nil {
		t.Fatalf("transport did not recover after cancellation: %v", err)
	}
	bad := DefaultLocation()
	bad["longitude"] = "invalid"
	before := hits.Load()
	if _, err := provider.fetchCurrentSummary(context.Background(), bad, now); err == nil || hits.Load() != before {
		t.Fatal("invalid location reached network")
	}
}
