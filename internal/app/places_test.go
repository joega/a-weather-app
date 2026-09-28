package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func readySearch(t *testing.T, a *App) M {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := object(a.Snapshot()["place_search"])
		if s["status"] != "loading" {
			return s
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("search did not settle")
	return nil
}
func cityRow(id float64) M {
	return M{"id": id, "name": "Berlin", "admin1": "Berlin", "country": "Germany", "country_code": "DE"}
}
func searchRequest(query string) M {
	return request("search_places", M{"search": M{"query": query, "country_code": ""}})
}
func pickRequest(s M, id float64) M {
	return request("set_location", M{"location": M{"mode": "place", "place_id": id, "search_generation": s["generation"]}})
}

func TestGlobalPlaceAtomicSaveOfflineAndCountry(t *testing.T) {
	cities := []struct {
		name, country, zone string
		lat, lon            float64
	}{
		{"Berlin", "DE", "Europe/Berlin", 52.52, 13.41},
		{"São Paulo", "BR", "America/Sao_Paulo", -23.55, -46.63},
		{"Tokyo", "JP", "Asia/Tokyo", 35.68, 139.69},
		{"Nairobi", "KE", "Africa/Nairobi", -1.29, 36.82},
		{"Sydney", "AU", "Australia/Sydney", -33.87, 151.21},
		{"Toronto", "CA", "America/Toronto", 43.65, -79.38},
	}
	for _, city := range cities {
		t.Run(city.country, func(t *testing.T) {
			loc := M{"name": city.name, "latitude": city.lat, "longitude": city.lon, "timezone": city.zone}
			calls := atomic.Int32{}
			a := newTestApp(t, Options{
				SearchPlaces: func(context.Context, M) ([]any, error) { return []any{cityRow(1)}, nil },
				ResolveSelection: func(_ context.Context, s M) (M, error) {
					calls.Add(1)
					if s["place_id"] != 1.0 {
						t.Error("identity not passed")
					}
					return M{"location": loc, "country_code": city.country, "place": M{"provider": "open-meteo", "id": 1.0}}, nil
				},
				FetchCountry: func(_ context.Context, l M, now time.Time, country string) (M, error) {
					if country != city.country {
						t.Error("country lost")
					}
					f := appFixture(now)
					f["location"] = l
					f["alerts"] = M{"status": "not_supported_here", "items": []any{}}
					return f, nil
				},
			})
			controls := safeio.Clone(a.controls)
			a.Handle(context.Background(), searchRequest(city.name))
			s := readySearch(t, a)
			reply, _ := a.Handle(context.Background(), pickRequest(s, 1))
			if reply["ok"] != true {
				t.Fatal(reply)
			}
			awaitCompletion(t, a)
			saved, err := a.state.Read("location-profile.json", weather.MaxBytes)
			if err != nil || ValidateProfile(saved) != nil || saved["schema_version"] != 2.0 {
				t.Fatal("profile", err, saved)
			}
			if !reflect.DeepEqual(a.location, loc) || !reflect.DeepEqual(a.controls, controls) || a.mode != "place" {
				t.Fatal("state not adopted or settings changed")
			}
			if object(a.Snapshot()["alerts"])["status"] != "not_supported_here" || object(a.Snapshot()["alerts"])["source"] != nil {
				t.Fatal("wrong coverage attribution")
			}
			offline, err := New(a.state, Options{Offline: true, Now: a.options.Now, ResolveSelection: func(context.Context, M) (M, error) { t.Error("offline geocoding"); return nil, errors.New("offline") }})
			if err != nil {
				t.Fatal(err)
			}
			defer offline.Close(context.Background())
			if !reflect.DeepEqual(offline.location, loc) || offline.forecast == nil || calls.Load() != 1 {
				t.Fatal("offline place lost")
			}
			reply, _ = a.Handle(context.Background(), pickRequest(s, 1))
			if reply["ok"] != false {
				t.Fatal("replayed selection accepted")
			}
		})
	}
}

func TestSearchCancellationCoalescingAndStop(t *testing.T) {
	started := make(chan context.Context, 3)
	queries := make(chan string, 3)
	gate := make(chan struct{})
	var active, maxActive atomic.Int32
	a := newTestApp(t, Options{SearchPlaces: func(ctx context.Context, q M) ([]any, error) {
		n := active.Add(1)
		defer active.Add(-1)
		if n > maxActive.Load() {
			maxActive.Store(n)
		}
		queries <- stringOf(q["query"])
		started <- ctx
		<-gate
		return []any{cityRow(1)}, nil
	}})
	a.Handle(context.Background(), searchRequest("Berlin"))
	first := <-started
	for _, q := range []string{"Paris", "Tokyo", "Sydney"} {
		a.Handle(context.Background(), searchRequest(q))
	}
	select {
	case <-first.Done():
	case <-time.After(time.Second):
		t.Fatal("search not canceled")
	}
	before := time.Now()
	reply, _ := a.Handle(context.Background(), request("stop_effects", nil))
	if reply["ok"] != true || time.Since(before) > 200*time.Millisecond {
		t.Fatal("search delayed stop")
	}
	reply, quit := a.Handle(context.Background(), request("quit", nil))
	if !quit || reply["ok"] != true {
		t.Fatal("search delayed quit")
	}
	if len(started) != 0 {
		t.Fatal("replacement started before canceled worker exited")
	}
	close(gate)
	s := readySearch(t, a)
	if s["status"] != "ready" || maxActive.Load() != 1 {
		t.Fatal("search failed or overlapped", s, maxActive.Load())
	}
	if <-queries != "Berlin" || <-queries != "Sydney" || len(queries) != 0 {
		t.Fatal("intermediate queries not coalesced")
	}
	a.Handle(context.Background(), request("cancel_place_search", nil))
	if object(a.Snapshot()["place_search"])["status"] != "idle" {
		t.Fatal("cancel kept results")
	}
}

func TestPlaceSelectionIssuanceExpiryAndLookupFailure(t *testing.T) {
	now := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)
	a := newTestApp(t, Options{Now: func() time.Time { return now }, SearchPlaces: func(context.Context, M) ([]any, error) { return []any{cityRow(1), cityRow(2)}, nil }, ResolveSelection: func(context.Context, M) (M, error) {
		return nil, &weather.LocationError{Code: "place_not_found", Message: "not found"}
	}})
	reply, _ := a.Handle(context.Background(), pickRequest(M{"generation": 0.0}, 1))
	if reply["ok"] != false {
		t.Fatal("unissued ID accepted")
	}
	a.Handle(context.Background(), searchRequest("Berlin"))
	s := readySearch(t, a)
	reply, _ = a.Handle(context.Background(), pickRequest(s, 3))
	if reply["ok"] != false {
		t.Fatal("unlisted ID accepted")
	}
	now = now.Add(6 * time.Minute)
	reply, _ = a.Handle(context.Background(), pickRequest(s, 1))
	if reply["ok"] != false {
		t.Fatal("expired ID accepted")
	}
	a.Handle(context.Background(), searchRequest("Berlin"))
	s = readySearch(t, a)
	old := safeio.Clone(a.location)
	a.Handle(context.Background(), pickRequest(s, 2))
	awaitCompletion(t, a)
	if !reflect.DeepEqual(old, a.location) || a.locationError != "place_not_found" {
		t.Fatal("lookup failure erased last good state")
	}
}

func TestLegacyProfileMigrationAndRollbackBackup(t *testing.T) {
	for _, mode := range []string{"zip", "auto", "custom"} {
		t.Run(mode, func(t *testing.T) {
			state := testState(t)
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			profile := M{"schema_version": 1.0, "mode": mode, "zip_code": nil, "location": weather.DefaultLocation(), "forecast": appFixture(now)}
			if mode == "zip" {
				profile["zip_code"] = "10001"
			}
			if err := state.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, Fetch: func(_ context.Context, l M, n time.Time) (M, error) {
				f := appFixture(n)
				f["location"] = l
				return f, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close(context.Background())
			if mode != "zip" {
				alerts := object(a.Snapshot()["alerts"])
				if alerts["status"] != "unavailable" || alerts["source"] != nil {
					t.Fatal("legacy unknown country guessed")
				}
			}
			disk, _ := state.Read("location-profile.json", weather.MaxBytes)
			if !reflect.DeepEqual(disk, profile) {
				t.Fatal("startup rewrote legacy state")
			}
			a.options.Offline = false
			a.Handle(context.Background(), request("refresh", nil))
			awaitCompletion(t, a)
			disk, _ = state.Read("location-profile.json", weather.MaxBytes)
			backup, _ := state.Read("location-profile-v1.json", weather.MaxBytes)
			if disk["schema_version"] != 2.0 || ValidateProfile(disk) != nil || !reflect.DeepEqual(backup, profile) {
				t.Fatal("migration or rollback backup failed", disk, backup)
			}
		})
	}
}

func TestMigrationBackupFailureLeavesLegacyProfile(t *testing.T) {
	state := testState(t)
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	profile := M{"schema_version": 1.0, "mode": "zip", "zip_code": "10001", "location": weather.DefaultLocation(), "forecast": appFixture(now)}
	if err := state.Write("location-profile.json", profile, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(state.Path, "location-profile-v1.json"), 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, Fetch: func(_ context.Context, l M, n time.Time) (M, error) {
		f := appFixture(n)
		f["location"] = l
		return f, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	a.options.Offline = false
	a.Handle(context.Background(), request("refresh", nil))
	awaitCompletion(t, a)
	disk, _ := state.Read("location-profile.json", weather.MaxBytes)
	if !reflect.DeepEqual(disk, profile) || a.errorCode != "refresh_failed" {
		t.Fatal("failed migration altered saved state")
	}
}

func TestOfflineSearchAndProfileIdentityValidation(t *testing.T) {
	a := newTestApp(t, Options{Offline: true, SearchPlaces: func(context.Context, M) ([]any, error) { t.Fatal("offline network"); return nil, nil }})
	a.Handle(context.Background(), searchRequest("Berlin"))
	if object(a.Snapshot()["place_search"])["error"] != "offline" {
		t.Fatal("offline search state")
	}
	p := M{"schema_version": 2.0, "mode": "place", "zip_code": nil, "country_code": "DE", "place": M{"provider": "open-meteo", "id": 1.0}, "location": weather.DefaultLocation(), "forecast": appFixture(a.options.Now())}
	if ValidateProfile(p) != nil {
		t.Fatal("valid profile")
	}
	for _, patch := range []M{{"country_code": "../DE"}, {"place": M{"provider": "https://evil.example", "id": 1.0}}, {"place": M{"provider": "open-meteo", "id": 9007199254740992.0}}, {"zip_code": "10001"}, {"country_code": nil}, {"unexpected": true}} {
		bad := safeio.Clone(p)
		for k, v := range patch {
			bad[k] = v
		}
		if ValidateProfile(bad) == nil {
			t.Fatal("bad identity accepted", patch)
		}
	}
}

func TestSearchSnapshotIsolationAndFailure(t *testing.T) {
	a := newTestApp(t, Options{SearchPlaces: func(context.Context, M) ([]any, error) { return []any{cityRow(1)}, nil }})
	a.Handle(context.Background(), searchRequest("Berlin"))
	s := readySearch(t, a)
	object(s["results"].([]any)[0])["id"] = 9.0
	fresh := object(a.Snapshot()["place_search"])
	if object(fresh["results"].([]any)[0])["id"] != 1.0 {
		t.Fatal("snapshot mutated issued identities")
	}
	a.options.SearchPlaces = func(context.Context, M) ([]any, error) { return nil, context.DeadlineExceeded }
	a.Handle(context.Background(), searchRequest("Berlin"))
	s = readySearch(t, a)
	if s["status"] != "error" || s["error"] != "timeout" || len(s["results"].([]any)) != 0 {
		t.Fatal("timed-out search retained selectable results")
	}
	reply, _ := a.Handle(context.Background(), pickRequest(fresh, 1))
	if reply["ok"] != false {
		t.Fatal("failure left prior identities valid")
	}
}

func TestSearchClientTokenFollowsLatestQuery(t *testing.T) {
	started := make(chan context.Context, 2)
	gate := make(chan struct{})
	a := newTestApp(t, Options{SearchPlaces: func(ctx context.Context, query M) ([]any, error) {
		started <- ctx
		<-gate
		return []any{cityRow(1)}, nil
	}})
	a.Handle(context.Background(), request("search_places", M{"search": M{"query": "Berlin", "country_code": "DE", "client_token": 1.0}}))
	<-started
	a.Handle(context.Background(), request("search_places", M{"search": M{"query": "Tokyo", "country_code": "JP", "client_token": 2.0}}))
	s := object(a.Snapshot()["place_search"])
	if s["client_token"] != 2.0 || s["status"] != "loading" {
		t.Fatal("latest token missing", s)
	}
	close(gate)
	s = readySearch(t, a)
	if s["client_token"] != 2.0 {
		t.Fatal("old worker changed new query token")
	}
	a.Handle(context.Background(), request("cancel_place_search", nil))
	if object(a.Snapshot()["place_search"])["client_token"] != 0.0 {
		t.Fatal("cancel retained query token")
	}
	offline := newTestApp(t, Options{Offline: true})
	offline.Handle(context.Background(), request("search_places", M{"search": M{"query": "Tokyo", "country_code": "JP", "client_token": 9.0}}))
	s = object(offline.Snapshot()["place_search"])
	if s["client_token"] != 9.0 || s["error"] != "offline" {
		t.Fatal("direct error lost token")
	}
}

func TestZIPIdentityMigrationValidationAndProfilePrecedence(t *testing.T) {
	state := testState(t)
	loc := weather.DefaultLocation()
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	if err := state.Write("location.json", loc, 8192); err != nil {
		t.Fatal(err)
	}
	if err := SaveZIPIdentity(state, "10001", loc); err != nil {
		t.Fatal(err)
	}
	a, err := New(state, Options{Offline: true, Now: func() time.Time { return now }, Fetch: func(_ context.Context, l M, n time.Time) (M, error) {
		f := appFixture(n)
		f["location"] = l
		return f, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	if a.country != "US" || a.mode != "zip" {
		t.Fatal("ZIP identity not restored")
	}
	a.options.Offline = false
	a.Handle(context.Background(), request("refresh", nil))
	awaitCompletion(t, a)
	profile, _ := state.Read("location-profile.json", weather.MaxBytes)
	if profile["schema_version"] != 2.0 || profile["mode"] != "zip" || ValidateProfile(profile) != nil {
		t.Fatal("CLI identity not upgraded")
	}
	// A complete profile remains authoritative over an obsolete sidecar.
	identity, _ := state.Read("location-identity.json", 8192)
	identity["country_code"] = "ZZ"
	state.Write("location-identity.json", identity, 8192)
	restored, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	restored.Close(context.Background())
	fresh := testState(t)
	fresh.Write("location.json", loc, 8192)
	legacy, err := New(fresh, Options{Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if legacy.country != nil || legacy.mode != "custom" {
		t.Fatal("legacy custom country guessed")
	}
	legacy.Close(context.Background())
	base := M{"schema_version": 1.0, "mode": "zip", "zip_code": "10001", "country_code": "US", "location": loc}
	for _, patch := range []M{{"country_code": "DE"}, {"zip_code": "../../"}, {"schema_version": 2.0}, {"location": M{"name": "Other", "latitude": 1.0, "longitude": 2.0, "timezone": "UTC"}}, {"extra": true}} {
		bad := safeio.Clone(base)
		for k, v := range patch {
			bad[k] = v
		}
		if err = fresh.Write("location-identity.json", bad, 8192); err != nil {
			t.Fatal(err)
		}
		if invalid, err := New(fresh, Options{Offline: true}); err == nil {
			invalid.Close(context.Background())
			t.Fatal("invalid country identity accepted", patch)
		}
	}
}
