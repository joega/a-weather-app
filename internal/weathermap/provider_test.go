package weathermap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(hours int, missingAt int) []rawCell {
	cells := make([]rawCell, Samples*Samples)
	lats, lons, _ := Coordinates(40.7128, -74.006)
	start := time.Now().UTC().Truncate(time.Hour).Unix()
	for i := range cells {
		c := &cells[i]
		c.Latitude = lats[i]
		c.Longitude = lons[i]
		c.HourlyUnits = map[string]string{"time": "unixtime", "temperature_2m": "°C", "wind_speed_10m": "m/s", "wind_direction_10m": "°", "precipitation": "mm"}
		for h := 0; h < hours; h++ {
			v := float64(i+h) / 10
			c.Hourly.Time = append(c.Hourly.Time, start+int64(h*3600))
			c.Hourly.Temperature = append(c.Hourly.Temperature, &v)
			c.Hourly.WindSpeed = append(c.Hourly.WindSpeed, &v)
			c.Hourly.WindDirection = append(c.Hourly.WindDirection, &v)
			if h == missingAt {
				c.Hourly.Precipitation = append(c.Hourly.Precipitation, nil)
			} else {
				c.Hourly.Precipitation = append(c.Hourly.Precipitation, &v)
			}
		}
	}
	return cells
}
func TestGridAndShortAvailability(t *testing.T) {
	lats, lons, e := Coordinates(40.7128, -74.006)
	if e != nil || len(lats) != 25 || len(lons) != 25 || lats[0] <= lats[24] || lons[0] >= lons[4] {
		t.Fatal("invalid geographic lattice")
	}
	for _, want := range []int{24, 11} {
		rows := fixture(24, want)
		wire, _ := json.Marshal(rows)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(wire) }))
		client := server.Client()
		model := nbm
		model.Endpoint = server.URL
		data, e := fetchModel(context.Background(), client, model, 40.7128, -74.006, time.Now())
		server.Close()
		if e != nil {
			t.Fatal(e)
		}
		expected := want
		if want == 24 {
			expected = 24
		}
		if len(data.Hours) != expected || len(data.Cells) != 25 || len(data.Cells[0].Precipitation) != expected {
			t.Fatalf("hours: %d", len(data.Hours))
		}
	}
}
func TestResponseBoundAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(make([]byte, MaxResponseBytes+1)) }))
	defer server.Close()
	model := nbm
	model.Endpoint = server.URL
	if _, e := fetchModel(context.Background(), server.Client(), model, 40, -74, time.Now()); e == nil {
		t.Fatal("accepted oversized map response")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := fetchModel(ctx, server.Client(), model, 40, -74, time.Now()); e == nil {
		t.Fatal("canceled request completed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestOverloadDoesNotDoubleProviderRequests(t *testing.T) {
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("overloaded")), Header: make(http.Header)}, nil
	})}
	_, e := Fetch(context.Background(), client, 40.7128, -74.006, "US", time.Now())
	if e == nil || calls.Load() != 1 {
		t.Fatalf("overload made %d requests: %v", calls.Load(), e)
	}
}
func TestRegionalCoverageFallsBackAsOneWholeTimeline(t *testing.T) {
	var calls atomic.Int32
	missing, _ := json.Marshal(fixture(24, 0))
	global, _ := json.Marshal(fixture(24, 24))
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		body := global
		if r.URL.Query().Get("models") == nbm.ID {
			body = missing
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})}
	data, e := Fetch(context.Background(), client, 40.7128, -74.006, "US", time.Now())
	if e != nil || calls.Load() != 2 || data.ModelID != gfs.ID || len(data.Hours) != 24 {
		t.Fatalf("fallback mixed or missing: %s, calls %d, error %v", data.ModelID, calls.Load(), e)
	}
}
func TestLiveRegionalAndGlobal(t *testing.T) {
	if os.Getenv("WEATHER_MAP_LIVE") != "1" {
		t.Skip("set WEATHER_MAP_LIVE=1 for bounded public provider check")
	}
	for _, city := range []struct {
		name, country string
		lat, lon      float64
		model         string
	}{{"New York", "US", 40.7128, -74.006, "ncep_nbm_conus"}, {"Berlin", "DE", 52.52, 13.41, "dwd_icon_d2"}, {"Toronto", "CA", 43.65, -79.38, "cmc_gem_hrdps"}, {"Tokyo", "JP", 35.68, 139.69, "ncep_gfs_global"}} {
		t.Run(city.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
			defer cancel()
			var requests atomic.Int32
			client := &http.Client{Timeout: 12 * time.Second, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				requests.Add(1)
				return http.DefaultTransport.RoundTrip(r)
			})}
			data, e := Fetch(ctx, client, city.lat, city.lon, city.country, time.Now())
			if e != nil {
				t.Fatal(e)
			}
			if data.ModelID != city.model || len(data.Cells) != 25 || len(data.Hours) < 1 {
				t.Fatalf("model %s, cells %d, hours %d", data.ModelID, len(data.Cells), len(data.Hours))
			}
			raw, _ := json.Marshal(data)
			if dir := os.Getenv("WEATHER_MAP_CAPTURE_DIR"); dir != "" {
				if e := os.WriteFile(dir+"/"+city.name+".json", raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			t.Log("model=" + data.ModelID + " hours=" + strconv.Itoa(len(data.Hours)) + " bytes=" + strconv.Itoa(len(raw)) + " http=" + strconv.Itoa(int(requests.Load())) + " quota_equivalents_est=" + strconv.Itoa(int(requests.Load())*Samples*Samples))
		})
	}
}
