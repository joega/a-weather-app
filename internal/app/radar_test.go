package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/radar"
)

type appRadarProvider struct {
	calls atomic.Int32
	body  []byte
}

func (p *appRadarProvider) FetchTimeline(_ context.Context, now time.Time) (radar.Timeline, error) {
	p.calls.Add(1)
	raw := fmt.Sprintf(`<WMS_Capabilities xmlns="http://www.opengis.net/wms" version="1.3.0"><Capability><Request><GetMap><Format>image/png</Format></GetMap></Request><Layer><CRS>EPSG:3857</CRS><Layer><Name>conus_bref_qcd</Name><EX_GeographicBoundingBox><westBoundLongitude>-130</westBoundLongitude><eastBoundLongitude>-60</eastBoundLongitude><southBoundLatitude>20</southBoundLatitude><northBoundLatitude>55</northBoundLatitude></EX_GeographicBoundingBox><Style><Name>radar_reflectivity</Name></Style><Dimension name="time" units="ISO8601">%s</Dimension></Layer></Layer></Capability></WMS_Capabilities>`, now.UTC().Format(time.RFC3339Nano))
	return radar.ParseTimeline([]byte(raw), now)
}
func (p *appRadarProvider) FetchFrame(context.Context, radar.Timeline, time.Time, radar.View, time.Time) ([]byte, error) {
	p.calls.Add(1)
	return bytes.Clone(p.body), nil
}
func (p *appRadarProvider) FetchLegend(context.Context) ([]byte, error) {
	p.calls.Add(1)
	return []byte("legend"), nil
}
func radarApp(t *testing.T, p *appRadarProvider, now func() time.Time) *App {
	t.Helper()
	a, _ := runtimeLocations(t, Options{Radar: p, Now: now, Fetch: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline forecast fixture") }, FetchAlerts: func(context.Context, M, time.Time) (M, error) { return nil, errors.New("offline alert fixture") }}, 0)
	return a
}
func awaitRadar(t *testing.T, a *App, predicate func(*radar.Presentation) bool) *radar.Presentation {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_, p := a.radarSince(nil)
		if p != nil && predicate(p) {
			return p
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("radar did not reach expected state")
	return nil
}
func TestRadarServiceDemandChunksAndLocationOwnership(t *testing.T) {
	now := savedRuntimeNow
	provider := &appRadarProvider{body: bytes.Repeat([]byte{77}, radar.ImageChunkBytes*2+17)}
	a := radarApp(t, provider, func() time.Time { return now })
	for i := 0; i < 10; i++ {
		a.Snapshot()
		a.Tick(context.Background())
	}
	if a.radar != nil || provider.calls.Load() != 0 {
		t.Fatal("unused radar initialized or fetched")
	}
	result, _ := a.Handle(context.Background(), request("radar_open", nil))
	if result["ok"] != true {
		t.Fatal(result)
	}
	awaitRadar(t, a, func(p *radar.Presentation) bool { return len(p.Frames) > 0 })
	if d := a.Interval(); d > 500*time.Millisecond {
		t.Fatal("radar timer depends on notification opt-in", d)
	}
	now = now.Add(time.Second)
	state := awaitRadar(t, a, func(p *radar.Presentation) bool { return len(p.Frames) == 1 && p.Frames[0].State == "ready" })
	id := state.Frames[0].ID
	var combined []byte
	for offset := 0; offset < len(provider.body); offset += radar.ImageChunkBytes {
		reply, _ := a.Handle(context.Background(), request("radar_image", M{"image": M{"id": id, "offset": float64(offset)}}))
		if reply["ok"] != true || reply["snapshot"] != nil {
			t.Fatal("image reply not isolated", reply["error"])
		}
		wire, err := ipc.Encode(reply, ipc.ResponseLimit)
		if err != nil || len(wire) >= ipc.ResponseLimit {
			t.Fatal("image exceeded existing IPC limit", err)
		}
		image := object(reply["image"])
		b, err := base64.StdEncoding.DecodeString(stringOf(image["data"]))
		if err != nil || image["total"] != len(provider.body) || image["id"] != id {
			t.Fatal("image identity", err)
		}
		combined = append(combined, b...)
	}
	if !bytes.Equal(combined, provider.body) {
		t.Fatal("chunked image changed bytes")
	}
	for _, image := range []M{{"id": id, "offset": -1.0}, {"id": id, "offset": 1.0}, {"id": "../file", "offset": 0.0}, {"id": id, "offset": 0.0, "extra": true}} {
		r, _ := a.Handle(context.Background(), request("radar_image", M{"image": image}))
		if r["ok"] != false {
			t.Fatal("bad image request accepted")
		}
	}
	for _, view := range []any{nil, M{}, M{"latitude": 40.0, "longitude": -74.0, "zoom": 99.0}, M{"latitude": 40.0, "longitude": -74.0, "zoom": 7.0, "extra": true}} {
		r, _ := a.Handle(context.Background(), request("radar_view", M{"view": view}))
		if r["ok"] != false {
			t.Fatal("bad viewport accepted")
		}
	}
	changed, _ := a.Handle(context.Background(), request("radar_view", M{"view": M{"latitude": 35.0, "longitude": -90.0, "zoom": 6.0}}))
	if changed["ok"] != true {
		t.Fatal(changed)
	}
	old, _ := a.Handle(context.Background(), request("radar_image", M{"image": M{"id": id, "offset": 0.0}}))
	if old["ok"] != false {
		t.Fatal("old-view image served")
	}
	locationAction(t, a, M{"action": "view", "id": stringOf(savedEntryAt(a, 1)["id"])})
	_, closed := a.radarSince(nil)
	if closed.Status != "closed" {
		t.Fatal("location switch retained radar demand", closed)
	}
}
func savedEntryAt(a *App, i int) M { return object(a.saved.doc["places"].([]any)[i]) }

func TestRadarEventsFreshnessAndHiddenDemand(t *testing.T) {
	now := savedRuntimeNow
	provider := &appRadarProvider{body: []byte("frame")}
	a := radarApp(t, provider, func() time.Time { return now })
	p := &peer{out: make(chan outbound, 4), closed: make(chan struct{}), subscribed: true}
	server := &Server{app: a, peers: map[*peer]bool{p: true}}
	server.radarEvent()
	if len(p.out) != 0 {
		t.Fatal("unused radar emitted metadata")
	}
	a.Handle(context.Background(), request("radar_open", nil))
	awaitRadar(t, a, func(p *radar.Presentation) bool { return len(p.Frames) > 0 })
	now = now.Add(time.Second)
	awaitRadar(t, a, func(p *radar.Presentation) bool { return p.Status == "current" })
	server.radarEvent()
	event := <-p.out
	var value M
	if err := json.Unmarshal(event.raw, &value); err != nil || value["event"] != "radar" || object(value["radar"])["status"] != "current" {
		t.Fatal("missing radar metadata", err)
	}
	if bytes.Contains(event.raw, []byte("data\"")) {
		t.Fatal("image bytes broadcast")
	}
	a.setPresented(true)
	a.setPresented(false)
	server.radarEvent()
	event = <-p.out
	json.Unmarshal(event.raw, &value)
	if object(value["radar"])["status"] != "closed" {
		t.Fatal("hidden demand remained open")
	}
	calls := provider.calls.Load()
	now = now.Add(time.Hour)
	a.Tick(context.Background())
	server.radarEvent()
	if provider.calls.Load() != calls || len(p.out) != 0 {
		t.Fatal("hidden radar polled or broadcast")
	}
}

func TestRadarSocketImageRoundTrip(t *testing.T) {
	provider := &appRadarProvider{body: bytes.Repeat([]byte{19}, radar.ImageChunkBytes+13)}
	a := radarApp(t, provider, time.Now)
	dir, err := os.MkdirTemp("/tmp", "radar-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "app.sock")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, socket, a, func() { close(ready) }) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("radar service did not stop")
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("radar socket not ready")
	}
	connection := connect(t, socket)
	reader := bufio.NewReader(connection)
	if err := ipc.Send(connection, request("subscribe", nil), ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	readReply(t, reader)
	ipc.Send(connection, request("radar_open", nil), ipc.RequestLimit)
	if r := readReply(t, reader); r["ok"] != true {
		t.Fatal(r)
	}
	state := awaitRadar(t, a, func(p *radar.Presentation) bool { return p.Status == "current" })
	imageConn := connect(t, socket)
	imageReader := bufio.NewReader(imageConn)
	var combined []byte
	for offset := 0; offset < len(provider.body); offset += radar.ImageChunkBytes {
		ipc.Send(imageConn, request("radar_image", M{"image": M{"id": state.Frames[0].ID, "offset": float64(offset)}}), ipc.RequestLimit)
		reply := readReply(t, imageReader)
		if reply["ok"] != true || reply["snapshot"] != nil {
			t.Fatal(reply["error"])
		}
		decoded, e := base64.StdEncoding.DecodeString(stringOf(object(reply["image"])["data"]))
		if e != nil {
			t.Fatal(e)
		}
		combined = append(combined, decoded...)
	}
	if !bytes.Equal(combined, provider.body) {
		t.Fatal("image socket corrupted PNG bytes")
	}
	imageConn.Close() // An unsubscribed image connection must not stop the service.
	ipc.Send(connection, request("snapshot", nil), ipc.RequestLimit)
	if r := readReply(t, reader); r["ok"] != true {
		t.Fatal("image connection closed app")
	}
	ipc.Send(connection, request("radar_close", nil), ipc.RequestLimit)
	if r := readReply(t, reader); r["ok"] != true {
		t.Fatal(r)
	}
	ipc.Send(connection, request("radar_image", M{"image": M{"id": strings.Repeat("a", 64), "offset": 0.0}}), ipc.RequestLimit)
	if r := readReply(t, reader); r["error"] != "radar_image_unavailable" {
		t.Fatal(r)
	}
}

func TestRadarViewTokenAndLocalTimeLabels(t *testing.T) {
	now := savedRuntimeNow
	a := radarApp(t, &appRadarProvider{body: []byte("frame")}, func() time.Time { return now })
	a.mu.Lock()
	a.location["timezone"] = "America/New_York"
	a.mu.Unlock()
	view := M{"latitude": 40.0, "longitude": -74.0, "zoom": 7.0, "client_token": 17.0}
	result, _ := a.Handle(context.Background(), request("radar_view", M{"view": view}))
	if result["ok"] != true {
		t.Fatal(result)
	}
	p := awaitRadar(t, a, func(p *radar.Presentation) bool { return len(p.Frames) > 0 })
	if p.ClientToken != 17 || !strings.HasSuffix(p.LatestLabel, "EDT") || p.Frames[0].Label != p.LatestLabel {
		t.Fatal("missing view identity or local observation time", p)
	}
	revision, _ := a.radarSince(nil)
	view["client_token"] = 18.0
	result, _ = a.Handle(context.Background(), request("radar_view", M{"view": view}))
	_, next := a.radarSince(&revision)
	if result["ok"] != true || next == nil || next.ClientToken != 18 {
		t.Fatal("same-view reopen lost new identity", next)
	}
	for _, invalid := range []any{-1.0, 2147483648.0, 1.5, "19", nil} {
		view["client_token"] = invalid
		result, _ = a.Handle(context.Background(), request("radar_view", M{"view": view}))
		if result["ok"] != false {
			t.Fatal("invalid token accepted", invalid)
		}
	}
	_, next = a.radarSince(nil)
	if next.ClientToken != 18 {
		t.Fatal("invalid request changed view identity")
	}
}
