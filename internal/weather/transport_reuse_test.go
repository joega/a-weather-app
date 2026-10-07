package weather

import (
	"context"
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

func TestProviderPoolReuseAndRecovery(t *testing.T) {
	var connects atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") == "large" {
			_, _ = w.Write([]byte(strings.Repeat(" ", MaxBytes+1)))
			return
		}
		if r.URL.Query().Get("name") == "cancel" {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connects.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	transport := providerhttp.New(server.Client().Transport.(*http.Transport), false)
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer transport.CloseIdleConnections()
	provider := jsonProvider{transport: transport}
	fetch := func(ctx context.Context, name string) error {
		_, e := provider.fetchJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?name="+name, false)
		return e
	}
	for i := 0; i < 3; i++ {
		if e := fetch(context.Background(), "ok"); e != nil {
			t.Fatal(e)
		}
	}
	if connects.Load() != 1 {
		t.Fatalf("three sequential requests used %d connections", connects.Load())
	}
	if e := fetch(context.Background(), "large"); e == nil {
		t.Fatal("accepted oversized body")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := fetch(ctx, "cancel"); e == nil {
		t.Fatal("ignored cancellation")
	}
	if e := fetch(context.Background(), "ok"); e != nil {
		t.Fatalf("pool failed after invalid replies: %v", e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := fetch(context.Background(), "ok"); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	// IP lookup must bypass even an injected proxy. Its JSON can fail validation;
	// the security assertion is that the proxy is never consulted.
	var proxies atomic.Int32
	proxied := providerhttp.New(transport, false)
	defer proxied.CloseIdleConnections()
	proxied.Proxy = func(*http.Request) (*url.URL, error) { proxies.Add(1); return url.Parse("http://127.0.0.1:1") }
	// Clone proxy-disabled configuration before either pool is used.
	direct := providerhttp.New(proxied, true)
	defer direct.CloseIdleConnections()
	provider = jsonProvider{transport: proxied, localTransport: direct}
	_, _ = provider.fetchJSON(context.Background(), LocalURL, true)
	if proxies.Load() != 0 {
		t.Fatal("IP lookup inherited proxy")
	}
}

// Three incremental searches with 15ms connection setup and 5ms response
// latency. Each iteration is a separate typing session with a fresh idle pool.
func BenchmarkTypingSearch(b *testing.B) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	base := server.Client().Transport.(*http.Transport).Clone()
	base.Proxy = nil
	base.TLSClientConfig.ServerName = "example.com"
	base.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		timer := time.NewTimer(15 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer base.CloseIdleConnections()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		transport := base.Clone()
		provider := jsonProvider{transport: transport}
		for _, name := range []string{"Bo", "Bos", "Boston"} {
			if _, e := provider.fetchJSON(context.Background(), "https://geocoding-api.open-meteo.com/v1/search?name="+name, false); e != nil {
				b.Fatal(e)
			}
		}
		transport.CloseIdleConnections()
	}
}
