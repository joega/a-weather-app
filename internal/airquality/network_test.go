package airquality

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/providerhttp"
)

func tlsFixture(t *testing.T, handler http.HandlerFunc) *http.Transport {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func TestRequestURLIsFixedAndCoordinatesOnly(t *testing.T) {
	raw, e := RequestURL(testLocation())
	if e != nil {
		t.Fatal(e)
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != endpointHost || u.Path != endpointPath || len(u.Query()) != 7 || u.Query().Get("domains") != "cams_global" || u.Query().Get("current") != "us_aqi,european_aqi,pm2_5" || u.Query().Get("forecast_days") != "1" || u.Query().Get("timeformat") != "unixtime" || u.Query().Get("timezone") != "GMT" {
		t.Fatalf("unexpected air quality URL: %s %v", raw, e)
	}
	if u.Query().Has("hourly") || u.Query().Has("daily") || u.Query().Has("apikey") {
		t.Fatal("extra provider data requested")
	}
	if _, e := RequestURL(Object{"name": "Evil", "latitude": "0&domains=cams_europe", "longitude": 0.0, "timezone": "UTC"}); e == nil {
		t.Fatal("coordinate injection accepted")
	}
	bad := testLocation()
	bad["url"] = "https://evil.example"
	if _, e := RequestURL(bad); e == nil {
		t.Fatal("UI URL accepted")
	}
}

func TestFetchHTTPSBoundary(t *testing.T) {
	t.Parallel()
	mode := "valid"
	hits := 0
	transport := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host != endpointHost || r.URL.Path != endpointPath || r.URL.Query().Get("domains") != "cams_global" || r.URL.Query().Get("forecast_days") != "1" || r.Header.Get("User-Agent") != userAgent {
			t.Errorf("unexpected HTTP request: %s %s", r.Host, r.URL)
		}
		switch mode {
		case "optional-array":
			p := payload(testTime())
			object(p["current"])["us_aqi"] = []any{1.0}
			_ = json.NewEncoder(w).Encode(p)
		case "redirect":
			w.Header().Set("Location", "https://evil.example/")
			w.WriteHeader(http.StatusFound)
		case "status":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "duplicate":
			fmt.Fprint(w, `{"latitude":40.7,"latitude":40.7}`)
		case "large-gzip":
			w.Header().Set("Content-Encoding", "gzip")
			z := gzip.NewWriter(w)
			_, _ = z.Write([]byte(strings.Repeat(" ", responseMaxBytes+1)))
			_ = z.Close()
		case "array":
			fmt.Fprint(w, `{"current":[]}`)
		default:
			_ = json.NewEncoder(w).Encode(payload(testTime()))
		}
	})
	r, e := fetchWithTransport(context.Background(), testLocation(), testTime(), transport)
	if e != nil || r["us_aqi"] != float64(551) {
		t.Fatalf("valid TLS fetch: %#v %v", r, e)
	}
	mode = "optional-array"
	r, e = fetchWithTransport(context.Background(), testLocation(), testTime(), transport)
	if e != nil || r["us_aqi"] != nil || r["european_aqi"] != float64(120) {
		t.Fatalf("decoded optional array was not field-local: %#v %v", r, e)
	}
	for _, m := range []string{"redirect", "status", "duplicate", "large-gzip", "array"} {
		mode = m
		before := hits
		if _, e := fetchWithTransport(context.Background(), testLocation(), testTime(), transport); e == nil {
			t.Errorf("accepted %s", m)
		}
		if hits != before+1 {
			t.Fatal("redirect or retry caused an extra request")
		}
	}
}

func TestFetchCancellationAndDeadline(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 2)
	transport := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := fetchWithTransport(ctx, testLocation(), testTime(), transport)
		done <- e
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not start")
	}
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancellation not propagated: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled fetch blocked")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, e := fetchWithTransport(ctx, testLocation(), testTime(), transport); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline not propagated: %v", e)
	}
}

// Return an empty successful response, then cancel exactly when its body reaches
// EOF. This reproduces cancellation racing with response decoding without
// depending on the timing of a TLS server or the scheduler.
type cancelAtEOFConn struct {
	net.Conn
	response       *strings.Reader
	cancel         context.CancelFunc
	requestWritten chan struct{}
	wrote          sync.Once
}

func (c *cancelAtEOFConn) Read(p []byte) (int, error) {
	<-c.requestWritten
	n, err := c.response.Read(p)
	if err == io.EOF && c.cancel != nil {
		c.cancel()
	}
	return n, err
}

func (c *cancelAtEOFConn) Write(p []byte) (int, error) {
	c.wrote.Do(func() { close(c.requestWritten) })
	return len(p), nil
}

func TestFetchCancellationDuringBodyEOF(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, peer := net.Pipe()
			t.Cleanup(func() { client.Close(); peer.Close() })
			conn := &cancelAtEOFConn{
				Conn:           client,
				response:       strings.NewReader("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n"),
				requestWritten: make(chan struct{}),
			}
			if canceled {
				conn.cancel = cancel
			}
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) {
				return conn, nil
			}
			t.Cleanup(transport.CloseIdleConnections)
			_, err := fetchWithTransport(ctx, testLocation(), testTime(), transport)
			want := io.EOF
			if canceled {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}

func TestSequentialAQRequestsReuseConnection(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewEncoder(w).Encode(payload(testTime())); err != nil {
			t.Error(err)
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	transport := providerhttp.New(server.Client().Transport.(*http.Transport), true)
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer transport.CloseIdleConnections()
	for i := 0; i < 3; i++ {
		if _, err := fetchWithTransport(context.Background(), testLocation(), testTime(), transport); err != nil {
			t.Fatal(err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("three requests used %d connections", connections.Load())
	}
}
