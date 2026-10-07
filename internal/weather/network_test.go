package weather

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestInjectedTransportCannotBypassEndpointPolicy(t *testing.T) {
	var dials atomic.Int32
	provider := jsonProvider{transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		},
	}}
	for _, endpoint := range []string{
		"http://api.weather.gov/alerts/active",
		"https://evil.example/",
		"https://user@api.weather.gov/alerts/active",
		"https://api.weather.gov:444/alerts/active",
		"https://api.weather.gov/alerts/active#fragment",
		"https://geocoding-api.open-meteo.com/evil",
	} {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := provider.fetchJSON(context.Background(), endpoint, false); err == nil {
				t.Fatalf("accepted unsupported endpoint %q", endpoint)
			}
		})
	}
	if n := dials.Load(); n != 0 {
		t.Fatalf("endpoint policy reached injected transport %d times", n)
	}
}

// Route the allowlisted origin to a private TLS fixture while retaining the
// actual HTTPS, HTTP status, redirect, size and decoding boundary.
func TestHTTPSBoundary(t *testing.T) {
	t.Parallel()
	mode := "valid"
	hits := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("User-Agent") != userAgent {
			t.Error("missing user agent")
		}
		switch mode {
		case "redirect":
			w.Header().Set("Location", "https://evil.example/")
			w.WriteHeader(302)
		case "duplicate":
			_, _ = w.Write([]byte(`{"value":1,"value":2}`))
		case "large":
			_, _ = w.Write([]byte(strings.Repeat(" ", MaxBytes+1)))
		case "nonfinite":
			_, _ = w.Write([]byte(`{"value":1e1000}`))
		case "status":
			w.WriteHeader(503)
		default:
			_, _ = w.Write([]byte(`{"value":1}`))
		}
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	provider := jsonProvider{transport: transport}
	defer transport.CloseIdleConnections()
	p, e := provider.fetchJSON(context.Background(), "https://api.weather.gov/fixture", false)
	if e != nil || p["value"] != 1.0 {
		t.Fatalf("fixture fetch: %v %v", p, e)
	}
	for _, m := range []string{"redirect", "duplicate", "large", "nonfinite", "status"} {
		mode = m
		before := hits
		if _, e := provider.fetchJSON(context.Background(), "https://api.weather.gov/fixture", false); e == nil {
			t.Errorf("accepted %s", m)
		}
		if hits != before+1 {
			t.Fatal("redirect caused an extra request")
		}
	}
}
