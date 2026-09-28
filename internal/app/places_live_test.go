package app

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in provider smoke test. Only public named cities are queried; no user
// settings or cache are read. Ordinary CI uses deterministic offline fixtures.
func TestLiveGlobalPlaces(t *testing.T) {
	if os.Getenv("A_WEATHER_APP_LIVE_PLACES") != "1" {
		t.Skip("set A_WEATHER_APP_LIVE_PLACES=1 for public-provider smoke test")
	}
	for _, city := range []struct{ name, country string }{{"Berlin", "DE"}, {"São Paulo", "BR"}, {"Tokyo", "JP"}, {"Nairobi", "KE"}, {"Sydney", "AU"}, {"Toronto", "CA"}} {
		t.Run(city.country, func(t *testing.T) {
			a := newTestApp(t, Options{Now: time.Now})
			reply, _ := a.Handle(context.Background(), request("search_places", M{"search": M{"query": city.name, "country_code": city.country}}))
			if reply["ok"] != true {
				t.Fatal("search not accepted")
			}
			deadline := time.Now().Add(10 * time.Second)
			var s M
			for time.Now().Before(deadline) {
				s = object(a.Snapshot()["place_search"])
				if s["status"] != "loading" {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			rows, _ := s["results"].([]any)
			if s["status"] != "ready" || len(rows) == 0 {
				t.Fatalf("%s search status %v error %v", city.name, s["status"], s["error"])
			}
			chosen := object(rows[0])
			if chosen["country_code"] != city.country {
				t.Fatal("country filter not honored")
			}
			reply, _ = a.Handle(context.Background(), pickRequest(s, chosen["id"].(float64)))
			if reply["ok"] != true {
				t.Fatal("selection not accepted")
			}
			select {
			case c := <-a.results:
				a.results <- c
				a.Snapshot()
			case <-time.After(27 * time.Second):
				t.Fatal("location did not settle")
			}
			if a.mode != "place" || a.country != city.country || a.forecast == nil || a.locationError != nil {
				t.Fatalf("%s selection failed: %v", city.name, a.locationError)
			}
			if object(a.Snapshot()["alerts"])["status"] != "not_supported_here" {
				t.Fatal("incorrect non-US alert state")
			}
			offline, err := New(a.state, Options{Now: time.Now, Offline: true})
			if err != nil {
				t.Fatal(err)
			}
			defer offline.Close(context.Background())
			if offline.mode != "place" || offline.forecast == nil {
				t.Fatal("saved place not available offline")
			}
			t.Logf("%s (%s): search, authoritative selection, forecast, unsupported alerts and offline reopen passed", city.name, city.country)
		})
	}
}
