package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/radar"
	"github.com/joega/a-weather-app/internal/weathermap"
)

type nativeRadarFixture struct {
	times  []time.Time
	frames [][]byte
	legend []byte
	calls  atomic.Int64
	live   radar.Provider
}

func (p *nativeRadarFixture) FetchTimeline(ctx context.Context, now time.Time) (radar.Timeline, error) {
	p.calls.Add(1)
	if p.live != nil {
		return p.live.FetchTimeline(ctx, now)
	}
	times := make([]string, len(p.times))
	for i, at := range p.times {
		times[i] = at.Format(time.RFC3339Nano)
	}
	xml := fmt.Sprintf(`<WMS_Capabilities xmlns="http://www.opengis.net/wms" version="1.3.0"><Capability><Request><GetMap><Format>image/png</Format></GetMap></Request><Layer><CRS>EPSG:3857</CRS><Layer><Name>conus_bref_qcd</Name><EX_GeographicBoundingBox><westBoundLongitude>-130</westBoundLongitude><eastBoundLongitude>-60</eastBoundLongitude><southBoundLatitude>20</southBoundLatitude><northBoundLatitude>55</northBoundLatitude></EX_GeographicBoundingBox><Style><Name>radar_reflectivity</Name></Style><Dimension name="time" units="ISO8601">%s</Dimension></Layer></Layer></Capability></WMS_Capabilities>`, strings.Join(times, ","))
	return radar.ParseTimeline([]byte(xml), now)
}
func (p *nativeRadarFixture) FetchFrame(ctx context.Context, timeline radar.Timeline, at time.Time, view radar.View, now time.Time) ([]byte, error) {
	p.calls.Add(1)
	if p.live != nil {
		return p.live.FetchFrame(ctx, timeline, at, view, now)
	}
	for i, stamp := range p.times {
		if stamp.Equal(at) {
			return bytes.Clone(p.frames[i]), nil
		}
	}
	return nil, radar.ErrUnavailable
}
func (p *nativeRadarFixture) FetchLegend(ctx context.Context) ([]byte, error) {
	p.calls.Add(1)
	if p.live != nil {
		return p.live.FetchLegend(ctx)
	}
	return bytes.Clone(p.legend), nil
}

// Only the native test target launches this private helper. Production IPC and
// the CLI gain no fixture controls; real App/Serve/image chunking are exercised.
func TestRadarNativeServiceFixture(t *testing.T) {
	if os.Getenv("WEATHER_NATIVE_RADAR_FIXTURE") != "1" {
		t.Skip("private native radar helper")
	}
	provider := &nativeRadarFixture{}
	var offset atomic.Int64
	now := func() time.Time { return time.Now().UTC().Add(time.Duration(offset.Load()) * time.Second) }
	if os.Getenv("WEATHER_RADAR_LIVE") == "1" {
		client := radar.New()
		defer client.CloseIdleConnections()
		provider.live = client
	} else {
		count := 3
		if os.Getenv("WEATHER_RADAR_PERFORMANCE") == "1" {
			count = 24
		}
		for i := 0; i < count; i++ {
			provider.times = append(provider.times, now().Add(-time.Duration(count-1-i)*5*time.Minute))
			pixels := image.NewNRGBA(image.Rect(0, 0, 512, 512))
			random := uint32(17 + i)
			for y := 0; y < 512; y++ {
				for x := 0; x < 512; x++ {
					random ^= random << 13
					random ^= random >> 17
					random ^= random << 5
					pixels.SetNRGBA(x, y, color.NRGBA{uint8(random), uint8(random >> 8), uint8(random >> 16), 255})
				}
			}
			var b bytes.Buffer
			if err := png.Encode(&b, pixels); err != nil {
				t.Fatal(err)
			}
			provider.frames = append(provider.frames, b.Bytes())
		}
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 500, 30))); err != nil {
			t.Fatal(err)
		}
		provider.legend = b.Bytes()
	}
	state := testState(t)
	profile := savedFixture(0)
	location := M{"name": "Boston, MA", "latitude": 42.3601, "longitude": -71.0589, "timezone": "America/New_York"}
	forecast := appFixture(now())
	forecast["location"] = location
	profile["location"], profile["forecast"] = location, forecast
	if _, err := createSavedLocations(state, profile); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Radar: provider, Now: now,
		Fetch:       func(context.Context, M, time.Time) (M, error) { return nil, errors.New("private forecast fixture") },
		FetchAlerts: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("private alerts fixture") },
		FetchMap: func(context.Context, float64, float64, string, time.Time) (weathermap.Data, error) {
			return weathermap.Data{}, errors.New("private forecast-map fixture")
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close(context.Background()) })
	runtime, err := os.MkdirTemp("/tmp", "radar-native-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	socket := filepath.Join(runtime, "app.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() { done <- Serve(ctx, socket, a, func() { close(ready) }) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("fixture did not stop")
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("socket did not start")
	}
	respond := func() {
		b, _ := json.Marshal(M{"socket": socket, "calls": provider.calls.Load(), "pid": os.Getpid()})
		fmt.Printf("RADAR_FIXTURE %s\n", b)
	}
	respond()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		var c struct {
			Op      string
			Seconds int64
		}
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		switch c.Op {
		case "stop":
			return
		case "status":
		case "advance":
			if c.Seconds < 0 || c.Seconds > 86400 {
				t.Fatal("invalid time")
			}
			offset.Add(c.Seconds)
			a.Tick(ctx)
			a.signalRadar()
		default:
			t.Fatal("unknown fixture operation")
		}
		respond()
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
