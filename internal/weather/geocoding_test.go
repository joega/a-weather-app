package weather

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func providerFixture(t *testing.T, handler http.HandlerFunc) {
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

func TestPlaceSearchValidation(t *testing.T) {
	for _, query := range []string{"São Paulo", "東京", "الرياض", "Київ", "עברית"} {
		v, e := ValidatePlaceSearch(Object{"query": "  " + query + "  ", "country_code": ""})
		if e != nil || v["query"] != query || v["client_token"] != float64(0) || len(v) != 3 {
			t.Fatalf("unicode query %q: %v", query, e)
		}
	}
	for _, tc := range []struct {
		raw  any
		want float64
	}{{0, 0}, {19, 19}, {float64(2147483647), 2147483647}} {
		v, e := ValidatePlaceSearch(Object{"query": "Berlin", "country_code": "DE", "client_token": tc.raw})
		if e != nil || v["client_token"] != tc.want || len(v) != 3 {
			t.Fatalf("client token %v: %#v %v", tc.raw, v, e)
		}
	}
	for _, v := range []Object{
		{"query": "A", "country_code": ""},
		{"query": strings.Repeat("x", 121), "country_code": ""},
		{"query": "Berlin\u202e", "country_code": ""},
		{"query": "Berlin\n", "country_code": ""},
		{"query": "Berlin", "country_code": "us"},
		{"query": "Berlin", "country_code": "US", "url": "https://evil.example"},
		{"query": "Berlin", "country_code": "DE", "client_token": -1},
		{"query": "Berlin", "country_code": "DE", "client_token": 1.5},
		{"query": "Berlin", "country_code": "DE", "client_token": 2147483648.0},
		{"query": "Berlin", "country_code": "DE", "client_token": "1"},
		{"query": "Berlin", "country_code": "DE", "client_token": true},
	} {
		if _, e := ValidatePlaceSearch(v); e == nil {
			t.Fatalf("accepted query %#v", v)
		}
	}
	for _, id := range []any{0, -1, 1.5, 2147483648.0, true, "42"} {
		if _, e := ValidateSelection(Object{"mode": "place", "place_id": id, "search_generation": 1}); e == nil {
			t.Fatalf("accepted place id %v", id)
		}
	}
	if _, e := ValidateSelection(Object{"mode": "place", "place_id": 42, "search_generation": 0}); e != nil {
		t.Fatal(e)
	}
}

func TestPlaceSearchHTTPAndBounds(t *testing.T) {
	mode := "valid"
	hits := 0
	providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Host != "geocoding-api.open-meteo.com" || r.URL.Path != "/v1/search" || r.URL.Query().Get("count") != "10" || r.URL.Query().Get("countryCode") != "DE" || r.URL.Query().Get("name") != "Berlin & id=7" || r.URL.Query().Has("client_token") {
			t.Errorf("unsafe or malformed request: %s %s", r.Host, r.URL)
		}
		row := `{"id":2950159,"name":"Berlin","admin1":"Berlin","country":"Germany","country_code":"DE","postcodes":[` + strings.Repeat(`"10001",`, 144) + `"10001"]}`
		switch mode {
		case "same-name":
			fmt.Fprintf(w, `{"results":[%s,{"id":1000000,"name":"Berlin","admin1":"Bavaria","country":"Germany","country_code":"DE"}]}`, row)
		case "indistinguishable":
			fmt.Fprintf(w, `{"results":[%s,{"id":1000000,"name":" BERLIN ","admin1":" berlin ","country":"GERMANY","country_code":"DE"}]}`, row)
		case "ambiguous-with-main":
			fmt.Fprintf(w, `{"results":[%s,{"id":1000000,"name":"Island","admin1":"Bavaria","country":"Germany","country_code":"DE"},{"id":1000001,"name":"ISLAND","admin1":" bavaria ","country":"GERMANY","country_code":"DE"}]}`, row)
		case "formatted-collision":
			fmt.Fprintf(w, `{"results":[%s,{"id":1000000,"name":"Berlin, Bavaria","country":"Germany","country_code":"DE"},{"id":1000001,"name":"Berlin","admin1":"Bavaria","country":"Germany","country_code":"DE"}]}`, row)
		case "admin2-enrichment":
			fmt.Fprint(w, `{"results":[{"id":21,"name":"Berlin","admin1":"Brandenburg","admin2":"Potsdam","country":"Germany","country_code":"DE"},{"id":22,"name":"Berlin","admin1":"Brandenburg","admin2":"Cottbus","country":"Germany","country_code":"DE"}]}`)
		case "malformed-admin2":
			fmt.Fprint(w, `{"results":[{"id":1,"name":"Berlin","admin1":"Berlin","admin2":7,"country":"Germany","country_code":"DE"}]}`)
		case "wrong-country":
			fmt.Fprint(w, `{"results":[{"id":1,"name":"Berlin","admin1":"Berlin","country":"France","country_code":"FR"}]}`)
		case "redirect":
			w.Header().Set("Location", "https://evil.example/")
			w.WriteHeader(302)
		case "duplicate-field":
			fmt.Fprint(w, `{"results":[{"id":1,"id":2}]}`)
		case "duplicate-id":
			fmt.Fprintf(w, `{"results":[%s,%s]}`, row, row)
		case "huge-id":
			fmt.Fprint(w, `{"results":[{"id":2147483648,"name":"X","country":"Germany","country_code":"DE"}]}`)
		case "huge-array":
			fmt.Fprint(w, `{"results":[`+strings.Repeat(`null,`, 30)+`null]}`)
		case "huge-body":
			fmt.Fprint(w, strings.Repeat(" ", geocodingMaxBytes+1))
		case "bidi":
			fmt.Fprint(w, `{"results":[{"id":1,"name":"A\u202eB","country":"Germany","country_code":"DE"}]}`)
		default:
			fmt.Fprintf(w, `{"results":[%s]}`, row)
		}
	})
	request := Object{"query": "Berlin & id=7", "country_code": "DE", "client_token": float64(17)}
	rows, e := SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 1 || obj(rows[0])["id"] != float64(2950159) {
		t.Fatalf("valid search: %#v %v", rows, e)
	}
	mode = "same-name"
	rows, e = SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 2 || obj(rows[0])["id"] == obj(rows[1])["id"] {
		t.Fatalf("same-name places lost identity: %#v %v", rows, e)
	}
	mode = "indistinguishable"
	rows, e = SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 0 {
		t.Fatalf("all ambiguous rows must be omitted: %#v %v", rows, e)
	}
	mode = "ambiguous-with-main"
	rows, e = SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 1 || obj(rows[0])["id"] != float64(2950159) {
		t.Fatalf("valid main city lost with unrelated ambiguity: %#v %v", rows, e)
	}
	mode = "formatted-collision"
	rows, e = SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 1 || obj(rows[0])["id"] != float64(2950159) {
		t.Fatalf("formatted label collision remained selectable: %#v %v", rows, e)
	}
	mode = "admin2-enrichment"
	rows, e = SearchPlaces(context.Background(), request)
	if e != nil || len(rows) != 2 || obj(rows[0])["admin1"] != "Brandenburg, Potsdam" || obj(rows[1])["admin1"] != "Brandenburg, Cottbus" {
		t.Fatalf("admin2 did not disambiguate regions: %#v %v", rows, e)
	}
	for _, m := range []string{"malformed-admin2", "wrong-country", "redirect", "duplicate-field", "duplicate-id", "huge-id", "huge-array", "huge-body", "bidi"} {
		mode = m
		before := hits
		if _, e := SearchPlaces(context.Background(), request); e == nil {
			t.Errorf("accepted %s", m)
		}
		if hits != before+1 {
			t.Fatal("unexpected follow-up request")
		}
	}
	if _, e := FetchJSON(context.Background(), geocodingBase+"/evil"); e == nil {
		t.Fatal("unlisted geocoding path accepted")
	}
}

func TestResolveGlobalPlaces(t *testing.T) {
	cities := []struct {
		name, admin, country, code, zone string
		id                               int
		lat, lon                         float64
	}{
		{"Berlin", "Berlin", "Germany", "DE", "Europe/Berlin", 2950159, 52.52, 13.41},
		{"São Paulo", "São Paulo", "Brazil", "BR", "America/Sao_Paulo", 3448439, -23.55, -46.63},
		{"Tokyo", "Tokyo", "Japan", "JP", "Asia/Tokyo", 1850147, 35.68, 139.69},
		{"Nairobi", "Nairobi", "Kenya", "KE", "Africa/Nairobi", 184745, -1.28, 36.82},
		{"Sydney", "New South Wales", "Australia", "AU", "Australia/Sydney", 2147714, -33.87, 151.21},
		{"Toronto", "Ontario", "Canada", "CA", "America/Toronto", 6167865, 43.65, -79.38},
	}
	byID := map[string]any{}
	for _, city := range cities {
		byID[fmt.Sprint(city.id)] = Object{"id": city.id, "name": city.name, "admin1": city.admin, "country": city.country, "country_code": city.code, "timezone": city.zone, "latitude": city.lat, "longitude": city.lon}
	}
	providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "geocoding-api.open-meteo.com" || r.URL.Path != "/v1/get" || len(r.URL.Query()) != 1 {
			t.Errorf("unexpected get URL: %s", r.URL)
		}
		_ = json.NewEncoder(w).Encode(byID[r.URL.Query().Get("id")])
	})
	for _, city := range cities {
		r, e := ResolveSelection(context.Background(), Object{"mode": "place", "place_id": city.id, "search_generation": 7})
		if e != nil || r["country_code"] != city.code || obj(r["place"])["id"] != float64(city.id) || obj(r["location"])["timezone"] != city.zone {
			t.Fatalf("%s: %#v %v", city.name, r, e)
		}
	}
	if _, e := ResolveSelection(context.Background(), Object{"mode": "place", "place_id": 1234, "search_generation": 7}); e == nil {
		t.Fatal("missing place accepted")
	}
	zone, _ := time.LoadLocation("Europe/Berlin")
	winter, _ := time.Parse(time.RFC3339, "2026-01-15T12:00:00Z")
	summer, _ := time.Parse(time.RFC3339, "2026-07-15T12:00:00Z")
	_, a := winter.In(zone).Zone()
	_, b := summer.In(zone).Zone()
	if a != 3600 || b != 7200 {
		t.Fatal("Berlin timezone does not track DST")
	}
}

func TestResolveRejectsTamperedPlace(t *testing.T) {
	row := Object{"id": 7, "name": "Paris", "admin1": "Île-de-France", "admin2": "Seine", "country": "France", "country_code": "FR", "latitude": 48.86, "longitude": 2.35, "timezone": "Europe/Paris"}
	providerFixture(t, func(w http.ResponseWriter, r *http.Request) { _ = json.NewEncoder(w).Encode(row) })
	selection := Object{"mode": "place", "place_id": 7, "search_generation": 1}
	resolved, e := ResolveSelection(context.Background(), selection)
	if e != nil || obj(resolved["location"])["name"] != "Paris, Île-de-France, Seine, France" {
		t.Fatalf("saved place omitted composed region: %#v %v", resolved, e)
	}
	for _, mutate := range []func(){
		func() { row["id"] = 8 },
		func() { row["name"] = "Pa\u202eris" },
		func() { row["name"] = "<script>" },
		func() { row["admin2"] = 42 },
		func() { row["timezone"] = "../etc/passwd" },
		func() { row["latitude"] = 999.0 },
	} {
		row = Object{"id": 7, "name": "Paris", "admin1": "Île-de-France", "admin2": "Seine", "country": "France", "country_code": "FR", "latitude": 48.86, "longitude": 2.35, "timezone": "Europe/Paris"}
		mutate()
		if _, e := ResolveSelection(context.Background(), selection); e == nil {
			t.Fatalf("accepted tampered place %#v", row)
		}
	}
	selection["latitude"] = 0
	if _, e := ResolveSelection(context.Background(), selection); e == nil {
		t.Fatal("UI coordinates accepted")
	}
}

func TestPlaceSearchCancellationAndDeadline(t *testing.T) {
	started := make(chan struct{})
	providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := SearchPlaces(ctx, Object{"query": "Berlin", "country_code": ""})
		done <- e
	}()
	<-started
	cancel()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancellation: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled search remained blocked")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, e := SearchPlaces(ctx, Object{"query": "Berlin", "country_code": ""}); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", e)
	}
}

func TestCountryAlertCoverage(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal(fixture("America/New_York", float64(now.Unix())))
	alertHits := 0
	providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "api.open-meteo.com":
			_, _ = w.Write(payload)
		case "api.weather.gov":
			alertHits++
			fmt.Fprint(w, `{"features":[]}`)
		default:
			t.Errorf("unexpected host %s", r.Host)
		}
	})
	for _, tc := range []struct{ country, status, coverage string }{{"DE", "not_supported_here", "unsupported"}, {"", "unavailable", "unknown"}, {"US", "available", "US"}} {
		s, e := FetchForCountry(context.Background(), DefaultLocation(), now, tc.country)
		if e != nil {
			t.Fatal(e)
		}
		a := obj(s["alerts"])
		if a["status"] != tc.status || a["coverage"] != tc.coverage {
			t.Fatalf("%s: %#v", tc.country, a)
		}
		selected := Select(s, now.Add(time.Hour), "live", nil, "subtle", false, false)
		if obj(obj(selected["forecast"])["alerts"])["status"] != tc.status && tc.country != "US" {
			t.Fatalf("%s lost unsupported/unknown coverage", tc.country)
		}
	}
	if alertHits != 1 {
		t.Fatalf("NWS called %d times", alertHits)
	}
	if _, e := FetchForCountry(context.Background(), DefaultLocation(), now, "us"); e == nil {
		t.Fatal("invalid country accepted")
	}
}
