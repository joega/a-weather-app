package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/joega/a-weather-app/internal/app"
	"github.com/joega/a-weather-app/internal/safeio"
)

func TestPreparedCLIZIPRetainsCountryWithoutSecondLookup(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/search" || r.URL.Query().Get("countryCode") != "US" || r.URL.Query().Get("name") != "02108" {
			t.Error("unexpected ZIP request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"name":"Boston","admin1":"Massachusetts","country_code":"US","latitude":42.36,"longitude":-71.05,"timezone":"America/New_York","postcodes":["02108"]}]}`))
	}))
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig.ServerName = "example.com"
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	old := http.DefaultTransport
	http.DefaultTransport = transport
	defer func() { http.DefaultTransport = old; transport.CloseIdleConnections() }()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	state, err := safeio.OpenDir(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err = prepareLocation(state, "02108", ""); err != nil {
		t.Fatal(err)
	}
	a, err := app.New(state, app.Options{Offline: true, ResolveSelection: func(context.Context, M) (M, error) {
		t.Fatal("repeated geocoding after guardian restart")
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	snap := a.Snapshot()
	settings := snap["location_settings"].(M)
	alerts := snap["alerts"].(M)
	if settings["mode"] != "zip" || settings["zip_code"] != "02108" || settings["country_code"] != "US" || alerts["coverage"] != "US" {
		t.Fatal("CLI discarded verified country", settings, alerts)
	}
}
