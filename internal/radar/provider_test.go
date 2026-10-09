package radar

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 9, 16, 20, 0, 0, time.UTC)
var testView = View{-8453323.83211421, 5009377.08569731, -7827151.69640205, 5635549.22140948}

func capabilities(times string) []byte {
	return []byte(`<WMS_Capabilities xmlns="http://www.opengis.net/wms" version="1.3.0"><Capability><Request><GetMap><Format>image/png</Format></GetMap></Request><Layer><CRS>EPSG:3857</CRS><Layer><Name>conus_bref_qcd</Name><EX_GeographicBoundingBox><westBoundLongitude>-130</westBoundLongitude><eastBoundLongitude>-60</eastBoundLongitude><southBoundLatitude>20</southBoundLatitude><northBoundLatitude>55</northBoundLatitude></EX_GeographicBoundingBox><Style><Name>radar_reflectivity</Name></Style><Dimension name="time" units="ISO8601">` + times + `</Dimension></Layer></Layer></Capability></WMS_Capabilities>`)
}
func timeline(t *testing.T) Timeline {
	t.Helper()
	result, err := ParseTimeline(capabilities("2026-10-09T16:10:00.123Z,2026-10-09T16:18:00Z"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func testPNG(t testing.TB, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), 50, 128})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(body []byte) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}
}

func TestTimelineActualTimesCoverageAndFreshness(t *testing.T) {
	wire := capabilities("2026-10-09T16:18:00Z,2026-10-09T13:00:00Z,2026-10-09T16:10:00.123Z,2026-10-09T16:18:00Z")
	got, err := ParseTimeline(wire, testNow)
	if err != nil {
		t.Fatal(err)
	}
	times := got.Times()
	if len(times) != 2 || times[0].Nanosecond() != 123000000 || !got.Latest().Equal(times[1]) {
		t.Fatal(times)
	}
	times[0] = time.Time{}
	if got.Times()[0].IsZero() {
		t.Fatal("timeline ownership escaped")
	}
	if !got.Coverage().Contains(42.36, -71.05) || got.Coverage().Contains(51.5, 0) || got.Coverage().Contains(math.NaN(), -71) {
		t.Fatal("coverage")
	}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{{testNow, "current"}, {testNow.Add(6 * time.Minute), "stale"}, {testNow.Add(29 * time.Minute), "unavailable"}, {testNow.Add(-5 * time.Minute), "unavailable"}} {
		if got.Freshness(tc.at) != tc.want {
			t.Fatalf("freshness %v: %s", tc.at, got.Freshness(tc.at))
		}
	}
}
func TestTimelineRejectsMalformedOrUnsupportedCapabilities(t *testing.T) {
	good := string(capabilities("2026-10-09T16:18:00Z"))
	cases := map[string]string{
		"interval instead of real frames": string(capabilities("2026-10-09T16:00:00Z/2026-10-09T16:18:00Z/PT2M")),
		"future":                          string(capabilities("2026-10-09T16:25:00Z")),
		"old":                             string(capabilities("2026-10-09T12:00:00Z")),
		"empty":                           string(capabilities("")),
		"too many":                        string(capabilities(strings.Repeat("2026-10-09T16:18:00Z,", MaxFrames) + "2026-10-09T16:18:00Z")),
		"unbounded":                       strings.Repeat(" ", MaxMetadataBytes+1),
		"foreign namespace":               strings.Replace(good, namespace, "https://other.invalid", 1),
		"missing projection":              strings.Replace(good, "EPSG:3857", "EPSG:4326", 1),
		"wrong product":                   strings.Replace(good, layerName, "other", 1),
		"wrong style":                     strings.Replace(good, styleName, "other", 1),
		"no PNG":                          strings.Replace(good, "image/png", "image/jpeg", 1),
		"bad bounds":                      strings.Replace(good, "-130", "NaN", 1),
		"DTD":                             "<!DOCTYPE x SYSTEM 'file:///etc/passwd'>" + good,
		"second document":                 good + good,
		"deep":                            strings.Replace(good, "</Capability>", strings.Repeat("<Layer>", 17)+strings.Repeat("</Layer>", 17)+"</Capability>", 1),
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTimeline([]byte(wire), testNow); err == nil {
				t.Fatal("accepted invalid capabilities")
			}
		})
	}
}
func TestFrameUsesExactTimeAndFixedEndpoint(t *testing.T) {
	raw := testPNG(t, 512, 512)
	tl := timeline(t)
	at := tl.Times()[0]
	calls := 0
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "opengeo.ncep.noaa.gov" || r.URL.Path != "/geoserver/conus/conus_bref_qcd/ows" {
			t.Fatal(r.URL)
		}
		q := r.URL.Query()
		if q.Get("time") != "2026-10-09T16:10:00.123Z" || q.Get("crs") != "EPSG:3857" || q.Get("layers") != layerName || q.Get("styles") != styleName || q.Get("bbox") != "-8453323.83211421,5009377.08569731,-7827151.69640205,5635549.22140948" || q.Get("width") != "512" || q.Get("height") != "512" {
			t.Fatal(q)
		}
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept") != "image/png" {
			t.Fatal("missing identity/type")
		}
		return response(raw), nil
	}))
	got, err := c.FetchFrame(context.Background(), tl, at, testView, testNow)
	if err != nil || !bytes.Equal(got, raw) || calls != 1 {
		t.Fatal(err, calls)
	}
	for _, tc := range []struct {
		at  time.Time
		v   View
		now time.Time
	}{{at.Add(time.Second), testView, testNow}, {at, testView, testNow.Add(6 * time.Minute)}, {at, View{0, 0, 1, 1}, testNow}, {at, View{math.NaN(), 0, 1, 1}, testNow}, {at, testView, testNow.Add(-time.Hour)}} {
		if _, err = c.FetchFrame(context.Background(), tl, tc.at, tc.v, tc.now); err == nil {
			t.Fatal("invalid request sent")
		}
	}
	if calls != 1 {
		t.Fatal("invalid inputs consumed requests", calls)
	}
}
func TestProviderRejectsRedirectFallbackAndOversize(t *testing.T) {
	raw := testPNG(t, 512, 512)
	for _, mode := range []string{"redirect", "nearest", "stale", "wrong type", "declared oversized", "stream oversized", "HTTP failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := newClient(roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				r := response(raw)
				switch mode {
				case "redirect":
					r.StatusCode = 302
					r.Header.Set("Location", "https://other.invalid/image")
				case "nearest":
					r.Header.Set("Warning", "99 Nearest value used: time=2026-10-09T16:18:00Z ISO8601")
				case "stale":
					r.Header.Set("Warning", `110 proxy "Response is stale"`)
				case "wrong type":
					r.Header.Set("Content-Type", "application/xml")
				case "declared oversized":
					r.ContentLength = MaxImageBytes + 1
				case "stream oversized":
					r.ContentLength = -1
					r.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", MaxImageBytes+1)))
				case "HTTP failure":
					r.StatusCode = 503
				}
				return r, nil
			}))
			tl := timeline(t)
			if _, err := c.FetchFrame(context.Background(), tl, tl.Latest(), testView, testNow); err == nil {
				t.Fatal("invalid image response accepted")
			}
			if calls != 1 {
				t.Fatal("redirected or retried", calls)
			}
		})
	}
}
func TestProviderConcurrencyAndCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	var calls atomic.Int32
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
		return nil, r.Context().Err()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := c.FetchTimeline(ctx, testNow); done <- err }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("request did not start")
		}
	}
	if _, err := c.FetchTimeline(context.Background(), testNow); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation stalled")
		}
	}
	if len(c.slots) != 0 || calls.Load() != 2 {
		t.Fatal("slots not released or excess network work")
	}
	if _, err := c.FetchLegend(ctx); !errors.Is(err, context.Canceled) || calls.Load() != 2 {
		t.Fatal("pre-canceled request sent")
	}
}
func TestMetadataAndLegendRequests(t *testing.T) {
	legend := testPNG(t, 500, 30)
	c := newClient(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Query().Get("request") {
		case "GetCapabilities":
			return response(capabilities("2026-10-09T16:18:00Z")), nil
		case "GetLegendGraphic":
			if r.URL.Query().Get("layer") != layerName {
				t.Fatal(r.URL)
			}
			return response(legend), nil
		default:
			t.Fatal(r.URL)
			return nil, errors.New("unexpected request")
		}
	}))
	if _, err := c.FetchTimeline(context.Background(), testNow); err != nil {
		t.Fatal(err)
	}
	if _, err := c.FetchLegend(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func pngChunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(kind)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	return b.Bytes()
}
func TestPNGRejectsExpandingMetadataCorruptionAndWrongGeometry(t *testing.T) {
	raw := testPNG(t, 512, 512)
	if err := validatePNG(raw, 512, 512); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"zTXt", "iTXt", "iCCP", "eXIf", "acTL", "tEXt"} {
		t.Run(kind, func(t *testing.T) {
			wire := append(bytes.Clone(raw[:33]), pngChunk(kind, []byte("not sent to decoder"))...)
			wire = append(wire, raw[33:]...)
			if validatePNG(wire, 512, 512) == nil {
				t.Fatal("unsupported metadata accepted")
			}
		})
	}
	badCRC := bytes.Clone(raw)
	badCRC[len(badCRC)-1] ^= 1
	ihdr := bytes.Clone(raw[16:29])
	binary.BigEndian.PutUint32(ihdr[:4], 0x7fffffff)
	huge := append(bytes.Clone(raw[:8]), pngChunk("IHDR", ihdr)...)
	huge = append(huge, raw[33:]...)
	corruptPixels := append(bytes.Clone(raw[:33]), pngChunk("IDAT", []byte("not zlib"))...)
	corruptPixels = append(corruptPixels, pngChunk("IEND", nil)...)
	for name, wire := range map[string][]byte{"wrong size": testPNG(t, 256, 256), "wrong CRC": badCRC, "huge dimensions": huge, "truncated": raw[:len(raw)-4], "trailing": append(bytes.Clone(raw), 1), "corrupt pixels": corruptPixels, "oversized": make([]byte, MaxImageBytes+1), "short": []byte("x")} {
		t.Run(name, func(t *testing.T) {
			if validatePNG(wire, 512, 512) == nil {
				t.Fatal("malformed PNG accepted")
			}
		})
	}
}
func BenchmarkValidatePNG(b *testing.B) {
	raw := testPNG(b, 512, 512)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := validatePNG(raw, 512, 512); err != nil {
			b.Fatal(err)
		}
	}
}
