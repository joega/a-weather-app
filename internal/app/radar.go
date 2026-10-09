package app

import (
	"encoding/base64"
	"errors"
	"math"

	"github.com/joega/a-weather-app/internal/radar"
)

func (a *App) openRadar(view M) error {
	if !a.presented || a.locationBusy || a.mode == "default" {
		return errors.New("radar location unavailable")
	}
	lat, latOK := a.location["latitude"].(float64)
	lon, lonOK := a.location["longitude"].(float64)
	zoom := 7
	if view != nil {
		if len(view) != 3 {
			return errors.New("invalid radar view")
		}
		lat, latOK = view["latitude"].(float64)
		lon, lonOK = view["longitude"].(float64)
		z, ok := view["zoom"].(float64)
		if !ok || math.IsNaN(z) || math.IsInf(z, 0) || z != math.Trunc(z) || z < 4 || z > 10 {
			return errors.New("invalid radar zoom")
		}
		zoom = int(z)
	}
	if !latOK || !lonOK {
		return errors.New("radar coordinates unavailable")
	}
	bounds, err := radar.CenteredView(lat, lon, zoom)
	if err != nil {
		return err
	}
	if a.radar == nil {
		a.radar = radar.NewController(a.options.Radar, a.options.Offline, a.signalRadar)
	}
	return a.radar.Open(bounds, a.options.Now())
}
func (a *App) closeRadar() {
	if a.radar != nil {
		a.radar.Close(a.options.Now())
	}
}
func (a *App) pollRadar() {
	if a.radar != nil {
		a.radar.Poll(a.options.Now())
	}
}
func (a *App) radarSince(last *uint64) (uint64, *radar.Presentation) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.radar == nil {
		return 0, nil
	}
	return a.radar.Since(last, a.options.Now())
}

// radarImage never triggers remote image downloads. A separate unsubscribed
// native image connection pulls small pieces of already validated cached PNGs.
// Replies omit the ordinary forecast snapshot and fit its unchanged IPC limit.
func (a *App) radarImage(requestID float64, image M) M {
	reply := M{"version": 1.0, "request_id": requestID, "ok": false, "error": "invalid_request"}
	if len(image) != 2 {
		return reply
	}
	id := stringOf(image["id"])
	if len(id) != 64 {
		return reply
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return reply
		}
	}
	n, ok := image["offset"].(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n >= radar.MaxImageBytes || n != math.Trunc(n) || int(n)%radar.ImageChunkBytes != 0 {
		return reply
	}
	reply["error"] = "radar_image_unavailable"
	if a.radar == nil {
		return reply
	}
	body, total, err := a.radar.Chunk(id, int(n), a.options.Now())
	if err != nil {
		return reply
	}
	return M{"version": 1.0, "request_id": requestID, "ok": true, "image": M{"id": id, "offset": int(n), "total": total, "data": base64.StdEncoding.EncodeToString(body)}}
}

// Image completions wake the metadata path without rebuilding the forecast.
func (a *App) signalRadar() {
	select {
	case a.RadarChanged <- struct{}{}:
	default:
	}
}
