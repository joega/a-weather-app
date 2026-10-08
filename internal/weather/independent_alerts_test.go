package weather

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIndependentForecastAndAlertBoundary(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(fixture("America/New_York", float64(now.Unix())))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"slow", "malformed", "timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			entered := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/forecast" {
					_, _ = w.Write(payload)
					return
				}
				close(entered)
				if mode == "malformed" {
					_, _ = w.Write([]byte(`{"features":[{}]}`))
					return
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(200 * time.Millisecond):
				}
				_, _ = w.Write([]byte(`{"features":[]}`))
			}))
			defer server.Close()
			transport := server.Client().Transport.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.TLSClientConfig.ServerName = "example.com"
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}
			defer transport.CloseIdleConnections()
			provider := jsonProvider{transport: transport}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			if mode == "slow" || mode == "malformed" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				defer cancel()
			}
			result := make(chan error, 1)
			go func() { _, err := provider.fetchAlerts(ctx, DefaultLocation(), now); result <- err }()
			<-entered
			forecast, err := provider.fetchForecastForCountry(context.Background(), DefaultLocation(), now, "US")
			if err != nil || obj(forecast["alerts"])["freshness"] != "pending" {
				t.Fatal("forecast withheld or alert feed misrepresented", forecast, err)
			}
			if mode == "cancel" {
				cancel()
			}
			err = <-result
			if (err == nil) != (mode == "slow") {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
		})
	}
}
