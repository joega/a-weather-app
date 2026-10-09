package radar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/joega/a-weather-app/internal/providerhttp"
)

const (
	endpoint       = "https://opengeo.ncep.noaa.gov/geoserver/conus/conus_bref_qcd/ows"
	ImageSize      = 512
	MaxImageBytes  = 2 * 1024 * 1024
	MaxLegendBytes = 128 * 1024
	mercatorExtent = 20037508.342789244
)

var (
	ErrBusy        = errors.New("radar request slots occupied")
	ErrUnavailable = errors.New("radar data unavailable")
	ErrUnsupported = errors.New("outside radar service footprint")
)

// View is a Web Mercator viewport in metres, aligned with the displayed
// basemap. The provider always returns one 512-square image, not world tiles.
type View struct {
	West  float64 `json:"west"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	North float64 `json:"north"`
}

func (v View) valid() bool {
	return finite(v.West) && finite(v.South) && finite(v.East) && finite(v.North) && v.West >= -mercatorExtent && v.East <= mercatorExtent && v.South >= -mercatorExtent && v.North <= mercatorExtent && v.East-v.West >= 1 && v.North-v.South >= 1
}
func (v View) intersects(c Coverage) bool {
	projectX := func(lon float64) float64 { return lon / 180 * mercatorExtent }
	projectY := func(lat float64) float64 { return math.Asinh(math.Tan(lat*math.Pi/180)) / math.Pi * mercatorExtent }
	return c.valid() && v.West < projectX(c.East) && v.East > projectX(c.West) && v.South < projectY(c.North) && v.North > projectY(c.South)
}

type Client struct {
	http  *http.Client
	slots chan struct{}
}

// New owns a reusable connection pool. Construction performs no networking.
// Use one client for the service and close its idle pool at shutdown.
func New() *Client {
	transport := providerhttp.New(http.DefaultTransport.(*http.Transport), false)
	transport.MaxResponseHeaderBytes = 32 * 1024
	transport.MaxConnsPerHost = 2
	return newClient(transport)
}
func newClient(transport http.RoundTripper) *Client {
	return &Client{http: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("radar redirect refused") }}, slots: make(chan struct{}, 2)}
}
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
func (c *Client) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case c.slots <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}
func (c *Client) release() { <-c.slots }

func (c *Client) FetchTimeline(ctx context.Context, now time.Time) (Timeline, error) {
	if err := c.acquire(ctx); err != nil {
		return Timeline{}, err
	}
	defer c.release()
	body, err := c.get(ctx, url.Values{"service": {"WMS"}, "version": {"1.3.0"}, "request": {"GetCapabilities"}}, MaxMetadataBytes, false)
	if err != nil {
		return Timeline{}, err
	}
	t, err := ParseTimeline(body, now)
	if ctx.Err() != nil {
		return Timeline{}, ctx.Err()
	}
	return t, err
}

// FetchFrame accepts only a time from a recently fetched timeline. Requests use
// the precise supplied composite time, never a rounded or interpolated time.
// Weather service warnings about fallback/nearest/stale images are rejected.
func (c *Client) FetchFrame(ctx context.Context, timeline Timeline, at time.Time, view View, now time.Time) ([]byte, error) {
	if !view.valid() {
		return nil, errors.New("invalid radar viewport")
	}
	if !view.intersects(timeline.coverage) {
		return nil, ErrUnsupported
	}
	known := false
	for _, t := range timeline.times {
		if t.Equal(at) {
			known = true
			break
		}
	}
	if !known || now.IsZero() || at.After(now.Add(time.Minute)) || now.Sub(at) > History || now.Sub(timeline.fetchedAt) > 5*time.Minute || now.Before(timeline.fetchedAt.Add(-time.Minute)) || timeline.Freshness(now) == "unavailable" {
		return nil, ErrUnavailable
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	q := url.Values{"service": {"WMS"}, "version": {"1.3.0"}, "request": {"GetMap"}, "layers": {layerName}, "styles": {styleName}, "crs": {"EPSG:3857"}, "bbox": {fmt.Sprintf("%.8f,%.8f,%.8f,%.8f", view.West, view.South, view.East, view.North)}, "width": {"512"}, "height": {"512"}, "format": {"image/png"}, "transparent": {"true"}, "time": {at.UTC().Format(time.RFC3339Nano)}}
	return c.image(ctx, q, ImageSize, ImageSize)
}
func (c *Client) FetchLegend(ctx context.Context) ([]byte, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()
	q := url.Values{"service": {"WMS"}, "version": {"1.3.0"}, "request": {"GetLegendGraphic"}, "layer": {layerName}, "style": {styleName}, "format": {"image/png"}, "width": {"500"}, "height": {"30"}}
	return c.image(ctx, q, 500, 30)
}
func (c *Client) image(ctx context.Context, q url.Values, width, height int) ([]byte, error) {
	limit := MaxImageBytes
	if width == 500 && height == 30 {
		limit = MaxLegendBytes
	}
	body, err := c.get(ctx, q, limit, true)
	if err != nil {
		return nil, err
	}
	if err = validatePNG(body, width, height); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return body, nil
}
func (c *Client) get(ctx context.Context, q url.Values, limit int, image bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "a-weather-app (native desktop radar; https://github.com/joega/a-weather-app)")
	req.Header.Set("Accept", "application/xml, text/xml")
	if image {
		req.Header.Set("Accept", "image/png")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("radar HTTP status %d", res.StatusCode)
	}
	if res.ContentLength > int64(limit) {
		return nil, errors.New("radar response too large")
	}
	if len(res.Header.Values("Warning")) > 0 {
		return nil, fmt.Errorf("%w: provider warned of substituted or stale data", ErrUnavailable)
	}
	if image {
		media, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
		if err != nil || media != "image/png" {
			return nil, errors.New("unexpected radar image type")
		}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(body) > limit {
		return nil, errors.New("radar response too large")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return body, nil
}
