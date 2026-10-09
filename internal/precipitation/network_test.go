package precipitation

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/providerhttp"
)

func fixtureTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
	transport := providerhttp.New(server.Client().Transport.(*http.Transport), true)
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "api.open-meteo.com:443" {
			return nil, fmt.Errorf("unexpected destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	t.Cleanup(transport.CloseIdleConnections)
	return transport
}

func TestFetchBoundariesAndConnectionReuse(t *testing.T) {
	var hits, connections atomic.Int32
	var mode atomic.Value
	mode.Store("valid")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Host != "api.open-meteo.com" || r.URL.Path != "/v1/forecast" || r.URL.Query().Get("forecast_hours") != "264" || r.Header.Get("Accept") != "application/json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "a-weather-app/") {
			t.Errorf("unexpected request: %s %s", r.Host, r.URL)
		}
		switch mode.Load().(string) {
		case "redirect":
			w.Header().Set("Location", "https://evil.example/")
			w.WriteHeader(http.StatusFound)
		case "status":
			w.WriteHeader(http.StatusTooManyRequests)
		case "duplicate":
			fmt.Fprint(w, `{"hourly":{},"hourly":{}}`)
		case "large", "large-gzip":
			var out io.Writer = w
			if mode.Load().(string) == "large-gzip" {
				w.Header().Set("Content-Encoding", "gzip")
				z := gzip.NewWriter(w)
				defer z.Close()
				out = z
			}
			fmt.Fprint(out, strings.Repeat(" ", MaxResponseBytes+1))
		case "array":
			fmt.Fprint(w, `[]`)
		case "trailing":
			_ = json.NewEncoder(w).Encode(payload(3))
			fmt.Fprint(w, `{}`)
		default:
			_ = json.NewEncoder(w).Encode(payload(MaxHours))
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	transport := fixtureTransport(t, server)
	for range 3 {
		d, err := fetch(context.Background(), testLocation(), testTime(), transport)
		if err != nil || len(d.Hours) != MaxHours {
			t.Fatalf("valid fetch: hours=%d err=%v", len(d.Hours), err)
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("three sequential requests opened %d connections", connections.Load())
	}
	for _, scenario := range []string{"redirect", "status", "duplicate", "large", "large-gzip", "array", "trailing"} {
		mode.Store(scenario)
		before := hits.Load()
		if _, err := fetch(context.Background(), testLocation(), testTime(), transport); err == nil {
			t.Errorf("accepted %s response", scenario)
		}
		if hits.Load() != before+1 {
			t.Fatal("unexpected automatic retry or redirect")
		}
	}
}

func TestFetchCancellationAndDeadline(t *testing.T) {
	started := make(chan struct{}, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	transport := fixtureTransport(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := fetch(ctx, testLocation(), testTime(), transport)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel did not propagate: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled fetch blocked")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := fetch(ctx, testLocation(), testTime(), transport); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline did not propagate: %v", err)
	}
}

type eofCancelConn struct {
	net.Conn
	response *strings.Reader
	cancel   context.CancelFunc
	written  chan struct{}
	once     sync.Once
}

func (c *eofCancelConn) Read(p []byte) (int, error) {
	<-c.written
	n, err := c.response.Read(p)
	if err == io.EOF && c.cancel != nil {
		c.cancel()
	}
	return n, err
}

func (c *eofCancelConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.written) })
	return len(p), nil
}

func TestFetchCancellationRacingWithBodyEOF(t *testing.T) {
	for _, valid := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			t.Run(fmt.Sprintf("valid=%t/canceled=%t", valid, canceled), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				client, peer := net.Pipe()
				t.Cleanup(func() { client.Close(); peer.Close() })
				body := ""
				if valid {
					raw, _ := json.Marshal(payload(3))
					body = string(raw)
				}
				conn := &eofCancelConn{Conn: client, response: strings.NewReader("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n" + body), written: make(chan struct{})}
				if canceled {
					conn.cancel = cancel
				}
				transport := providerhttp.New(http.DefaultTransport.(*http.Transport), true)
				transport.DialTLSContext = func(context.Context, string, string) (net.Conn, error) { return conn, nil }
				t.Cleanup(transport.CloseIdleConnections)
				d, err := fetch(ctx, testLocation(), testTime(), transport)
				switch {
				case canceled:
					if !errors.Is(err, context.Canceled) || len(d.Hours) != 0 {
						t.Fatalf("canceled result escaped: %+v %v", d, err)
					}
				case valid:
					if err != nil || len(d.Hours) != 3 {
						t.Fatalf("valid body rejected: %+v %v", d, err)
					}
				default:
					if !errors.Is(err, io.EOF) {
						t.Fatalf("expected malformed-body error: %v", err)
					}
				}
			})
		}
	}
}
