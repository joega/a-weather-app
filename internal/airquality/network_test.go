package airquality

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func tlsFixture(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = old; transport.CloseIdleConnections() })
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
	mode := "valid"
	hits := 0
	tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
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
	r, e := Fetch(context.Background(), testLocation(), testTime())
	if e != nil || r["us_aqi"] != float64(551) {
		t.Fatalf("valid TLS fetch: %#v %v", r, e)
	}
	mode = "optional-array"
	r, e = Fetch(context.Background(), testLocation(), testTime())
	if e != nil || r["us_aqi"] != nil || r["european_aqi"] != float64(120) {
		t.Fatalf("decoded optional array was not field-local: %#v %v", r, e)
	}
	for _, m := range []string{"redirect", "status", "duplicate", "large-gzip", "array"} {
		mode = m
		before := hits
		if _, e := Fetch(context.Background(), testLocation(), testTime()); e == nil {
			t.Errorf("accepted %s", m)
		}
		if hits != before+1 {
			t.Fatal("redirect or retry caused an extra request")
		}
	}
}

func TestFetchCancellationAndDeadline(t *testing.T) {
	started := make(chan struct{}, 2)
	tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := Fetch(ctx, testLocation(), testTime())
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
	if _, e := Fetch(ctx, testLocation(), testTime()); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline not propagated: %v", e)
	}
}
