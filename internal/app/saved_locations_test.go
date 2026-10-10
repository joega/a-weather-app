package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func savedFixture(index int) M {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	location := weather.DefaultLocation()
	location["name"] = fmt.Sprintf("City %d", index)
	location["latitude"] = 20.0 + float64(index)
	forecast := appFixture(now)
	forecast["location"] = location
	return M{"schema_version": 2.0, "mode": "place", "zip_code": nil, "country_code": "US", "place": M{"provider": "open-meteo", "id": float64(index + 100)}, "location": location, "forecast": forecast}
}

type savedFaultFiles struct {
	*safeio.Directory
	reads, writes []string
	fail          string
	after         bool
}

func (f *savedFaultFiles) Read(name string, limit int) (M, error) {
	f.reads = append(f.reads, name)
	return f.Directory.Read(name, limit)
}
func (f *savedFaultFiles) Write(name string, value any, limit int) error {
	f.writes = append(f.writes, name)
	if name == f.fail && !f.after {
		return errors.New("injected pre-rename failure")
	}
	if err := f.Directory.Write(name, value, limit); err != nil {
		return err
	}
	if name == f.fail {
		return errors.New("injected directory-sync failure")
	}
	return nil
}

func TestSavedLocationsMigrationAndLazyReads(t *testing.T) {
	for _, mode := range []string{"place", "custom", "auto", "zip", "default"} {
		t.Run(mode, func(t *testing.T) {
			files := &savedFaultFiles{Directory: testState(t)}
			legacy := savedFixture(0)
			legacy["mode"] = mode
			if mode != "place" {
				legacy["schema_version"] = 1.0
				delete(legacy, "country_code")
				delete(legacy, "place")
			}
			if mode == "zip" {
				legacy["zip_code"] = "10001"
			}
			if err := files.Write("location-profile.json", legacy, weather.MaxBytes); err != nil {
				t.Fatal(err)
			}
			store, err := createSavedLocations(files, legacy)
			if err != nil {
				t.Fatal(err)
			}
			before := store.document()
			if before["primary"] != before["viewed"] {
				t.Fatal("migration split primary and view")
			}
			old, _ := files.Read("location-profile.json", weather.MaxBytes)
			if !reflect.DeepEqual(old, legacy) {
				t.Fatal("migration modified rollback profile")
			}
			files.reads = nil
			loaded, err := loadSavedLocations(files)
			if err != nil || !reflect.DeepEqual(files.reads, []string{savedLocationsFile}) {
				t.Fatal("opening list read full forecast caches", files.reads, err)
			}
			profile, err := loaded.profile(stringOf(before["primary"]))
			if err != nil || ValidateProfile(profile) != nil || !reflect.DeepEqual(profile["forecast"], legacy["forecast"]) {
				t.Fatal("migration lost cached weather", err)
			}
			object(object(profile["forecast"])["current"])["temperature_c"] = 99.0
			again, _ := loaded.profile(stringOf(before["primary"]))
			if object(object(again["forecast"])["current"])["temperature_c"] == 99.0 {
				t.Fatal("cache returned mutable shared state")
			}
			savedEntry(before, stringOf(before["primary"]))["label"] = "Mutated"
			if savedEntry(loaded.doc, stringOf(loaded.doc["primary"]))["label"] != "" {
				t.Fatal("document leaked mutable state")
			}
			// Repeated migration must honor the committed document, not the old profile.
			reopened, err := createSavedLocations(files, savedFixture(1))
			if err != nil || !reflect.DeepEqual(reopened.doc, loaded.doc) {
				t.Fatal("migration was not idempotent", err)
			}
		})
	}
}

func TestSavedLocationsEmptyForecastAndStableIdentity(t *testing.T) {
	profile := savedFixture(0)
	profile["forecast"] = nil
	store, err := createSavedLocations(testState(t), profile)
	if err != nil {
		t.Fatal(err)
	}
	entry := savedEntry(store.doc, "place-100")
	if entry["summary"] != nil || entry["cache_slot"] != nil {
		t.Fatal("invented cache for unloaded place")
	}
	if err := ValidateProfile(profile); err == nil {
		t.Fatal("legacy full-profile validation was weakened")
	}
	for _, mode := range []string{"auto", "custom", "place"} {
		p := savedFixture(0)
		p["mode"] = mode
		if mode != "place" {
			p["place"] = nil
		}
		id, err := savedLocationID(p)
		if err != nil {
			t.Fatal(err)
		}
		object(p["location"])["name"] = "A renamed provider label"
		if mode == "auto" {
			object(p["location"])["latitude"] = -40.0
		}
		next, err := savedLocationID(p)
		if err != nil || next != id {
			t.Fatal("identity depended on mutable label/current-location fix", mode)
		}
	}
	p := savedFixture(0)
	p["mode"], p["place"] = "custom", nil
	object(p["location"])["latitude"] = math.Copysign(0, -1)
	negative, _ := savedLocationID(p)
	object(p["location"])["latitude"] = 0.0
	positive, _ := savedLocationID(p)
	if positive != negative {
		t.Fatal("signed zero created different coordinate identities")
	}
}

func TestSavedLocationsOwnershipOrderingAndDeletion(t *testing.T) {
	files := &savedFaultFiles{Directory: testState(t)}
	store, err := createSavedLocations(files, savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 4; i++ {
		if err := store.put(savedFixture(i), true, false); err != nil {
			t.Fatal(err)
		}
	}
	if store.doc["primary"] != "place-100" || store.doc["viewed"] != "place-103" {
		t.Fatal("browsing redirected primary", store.doc)
	}
	files.reads = nil
	if err := store.rename("place-102", "Work"); err != nil {
		t.Fatal(err)
	}
	if err := store.move("place-102", 0); err != nil {
		t.Fatal(err)
	}
	if err := store.view("place-101"); err != nil {
		t.Fatal(err)
	}
	if err := store.makePrimary("place-102"); err != nil {
		t.Fatal(err)
	}
	for _, name := range files.reads {
		if name != savedLocationsFile {
			t.Fatal("metadata action read forecast", name)
		}
	}
	if object(store.doc["places"].([]any)[0])["label"] != "Work" || store.doc["viewed"] != "place-101" {
		t.Fatal("rename/reorder/primary changed view")
	}
	before := store.document()
	for _, action := range []func() error{
		func() error { return store.remove("place-102", "") },
		func() error { return store.remove("place-102", "place-102") },
		func() error { return store.remove("place-102", "missing") },
		func() error { return store.remove("place-101", "place-100") },
		func() error { return store.move("place-102", 4) },
		func() error { return store.view("missing") },
		func() error { return store.rename("place-102", "bad\nlabel") },
		func() error { return store.rename("place-102", strings.Repeat("x", 81)) },
	} {
		if err := action(); err == nil || !reflect.DeepEqual(before, store.doc) {
			t.Fatal("invalid action changed committed state", err)
		}
	}
	if err := store.remove("place-101", ""); err != nil || store.doc["viewed"] != "place-102" {
		t.Fatal("deleting view did not return to primary", err)
	}
	if err := store.remove("place-102", "place-100"); err != nil || store.doc["primary"] != "place-100" || store.doc["viewed"] != "place-100" {
		t.Fatal("primary replacement was not atomic", err)
	}
	loaded, err := loadSavedLocations(files)
	if err != nil || !reflect.DeepEqual(loaded.doc, store.doc) {
		t.Fatal("restart lost ownership or order", err)
	}
}

func TestSavedLocationsBoundedCacheAndPinnedOwners(t *testing.T) {
	state := testState(t)
	store, err := createSavedLocations(state, savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < savedLocationLimit; i++ {
		if err := store.put(savedFixture(i), true, false); err != nil {
			t.Fatal(i, err)
		}
	}
	if len(store.doc["cache_order"].([]any)) != savedForecastLimit || savedEntry(store.doc, "place-100")["cache_slot"] == nil || savedEntry(store.doc, "place-119")["cache_slot"] == nil {
		t.Fatal("cache bound/pinning failed")
	}
	if err := store.put(savedFixture(20), true, false); err == nil {
		t.Fatal("location limit exceeded")
	}
	if savedEntry(store.doc, "place-101")["cache_slot"] != nil || savedEntry(store.doc, "place-101")["summary"] == nil {
		t.Fatal("eviction removed summary or retained full cache")
	}
	profile, err := store.profile("place-101")
	if err != nil || profile["forecast"] != nil {
		t.Fatal("evicted cache returned forecast", err)
	}
	// Refresh repeatedly with both owners pinned; every write still has one
	// spare slot and never grows the number of files with the number of updates.
	for i := 0; i < 30; i++ {
		if err := store.put(savedFixture(0), false, false); err != nil {
			t.Fatal(err)
		}
	}
	files, err := filepath.Glob(filepath.Join(state.Path, "saved-forecast-*.json"))
	if err != nil || len(files) > savedForecastSlots {
		t.Fatal("unbounded cache files", len(files), err)
	}
	total := int64(0)
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.Size() > weather.MaxBytes {
			t.Fatal("cache file bound", err)
		}
		total += info.Size()
	}
	if total > savedForecastSlots*weather.MaxBytes {
		t.Fatal("total cache disk budget exceeded", total)
	}
	before := store.document()
	oversized := savedFixture(0)
	object(oversized["forecast"])["provider_extra"] = strings.Repeat("x", weather.MaxBytes)
	if err := store.put(oversized, false, false); err == nil || !reflect.DeepEqual(before, store.doc) {
		t.Fatal("oversized write changed manifest", err)
	}
}

func TestSavedLocationsInterruptedPublication(t *testing.T) {
	for _, target := range []string{"cache", "manifest"} {
		for _, after := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/after=%v", target, after), func(t *testing.T) {
				files := &savedFaultFiles{Directory: testState(t)}
				original := savedFixture(0)
				store, err := createSavedLocations(files, original)
				if err != nil {
					t.Fatal(err)
				}
				old := store.document()
				files.fail, files.after = savedSlotName(1), after
				if target == "manifest" {
					files.fail = savedLocationsFile
				}
				changed := savedFixture(0)
				object(object(changed["forecast"])["current"])["temperature_c"] = 25.0
				err = store.put(changed, false, false)
				if err == nil {
					t.Fatal("injected failure ignored")
				}
				published := target == "manifest" && after
				if errors.Is(err, errSavedLocationsUnconfirmed) != published {
					t.Fatal("wrong durability result", err)
				}
				files.fail = ""
				reopened, err := loadSavedLocations(files)
				if err != nil {
					t.Fatal(err)
				}
				want := original
				if published {
					want = changed
				} else if !reflect.DeepEqual(old, store.doc) || !reflect.DeepEqual(old, reopened.doc) {
					t.Fatal("failed publication changed committed metadata")
				}
				actual, err := reopened.profile("place-100")
				if err != nil || !reflect.DeepEqual(actual, want) {
					t.Fatal("interrupted write damaged last committed forecast", err)
				}
			})
		}
	}
}

func TestSavedLocationsFailedMigrationRetriesAndStaleWriter(t *testing.T) {
	files := &savedFaultFiles{Directory: testState(t), fail: savedLocationsFile}
	legacy := savedFixture(0)
	if err := files.Write("location-profile.json", legacy, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := createSavedLocations(files, legacy); err == nil {
		t.Fatal("failed migration accepted")
	}
	old, _ := files.Read("location-profile.json", weather.MaxBytes)
	if !reflect.DeepEqual(old, legacy) {
		t.Fatal("failed migration damaged rollback")
	}
	files.fail = ""
	store, err := createSavedLocations(files, legacy)
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := loadSavedLocations(files)
	if err := store.put(savedFixture(1), true, false); err != nil {
		t.Fatal(err)
	}
	// The old store believed slot 1 was free. It is now the viewed forecast.
	files.writes = nil
	if err := stale.put(savedFixture(2), true, false); !errors.Is(err, errSavedLocationsChanged) || len(files.writes) != 0 {
		t.Fatal("stale writer touched another owner's slot", err, files.writes)
	}
	profile, err := store.profile("place-101")
	if err != nil || !reflect.DeepEqual(profile, savedFixture(1)) {
		t.Fatal("stale write corrupted viewed cache", err)
	}
}

func TestSavedLocationsInvalidMetadataAndCacheIsolation(t *testing.T) {
	files := &savedFaultFiles{Directory: testState(t)}
	store, err := createSavedLocations(files, savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.put(savedFixture(1), true, false); err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string]func(M){
		"missing primary":    func(d M) { d["primary"] = "missing" },
		"missing view":       func(d M) { d["viewed"] = "missing" },
		"duplicate identity": func(d M) { d["places"] = append(d["places"].([]any), weather.Clone(d["places"].([]any)[0])) },
		"duplicate slot":     func(d M) { savedEntry(d, "place-101")["cache_slot"] = savedEntry(d, "place-100")["cache_slot"] },
		"slot traversal":     func(d M) { savedEntry(d, "place-101")["cache_slot"] = "../../forecast" },
		"unknown summary key": func(d M) {
			summary := object(savedEntry(d, "place-100")["summary"])
			delete(summary, "is_day")
			summary["unknown"] = nil
		},
		"foreign badge":        func(d M) { object(savedEntry(d, "place-100")["profile"])["country_code"] = "DE" },
		"forecast in metadata": func(d M) { object(savedEntry(d, "place-100")["profile"])["forecast"] = savedFixture(0)["forecast"] },
		"duplicate recency":    func(d M) { d["cache_order"] = []any{"place-100", "place-100"} },
		"invalid label":        func(d M) { savedEntry(d, "place-100")["label"] = "\u202ehidden" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := store.document()
			corrupt(bad)
			if err := validateSavedLocations(bad); err == nil {
				t.Fatal("accepted corrupt metadata")
			}
		})
	}
	entry := savedEntry(store.doc, "place-101")
	name := savedSlotName(int(entry["cache_slot"].(float64)))
	// A slot may have been recycled after a reader took an old manifest.
	if err := files.Write(name, M{"schema_version": 1.0, "token": entry["cache_token"], "profile": savedFixture(0)}, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	profile, err := store.profile("place-101")
	if err == nil || profile["forecast"] != nil || object(profile["place"])["id"] != 101.0 {
		t.Fatal("another city's forecast escaped through a slot", err)
	}
	if _, err := store.profile("place-100"); err != nil {
		t.Fatal("one bad cache damaged another city", err)
	}
	if _, err := loadSavedLocations(files); err != nil {
		t.Fatal("bad cache erased usable metadata", err)
	}
}

func TestSavedLocationsMetadataWorstCaseBudget(t *testing.T) {
	store, err := createSavedLocations(testState(t), savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < savedLocationLimit; i++ {
		profile := savedFixture(i)
		object(profile["location"])["name"] = strings.Repeat("🌧", 244)
		object(profile["forecast"])["location"] = profile["location"]
		if err := store.put(profile, true, false); err != nil {
			t.Fatal(err)
		}
		if err := store.rename(fmt.Sprintf("place-%d", i+100), strings.Repeat("🌧", 80)); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(store.doc)
	if err != nil || len(raw) > savedMetadataBytes {
		t.Fatal("full unicode location list exceeds byte budget", len(raw), err)
	}
}

func TestSavedLocationsRejectsMalformedMigrationAndReusedToken(t *testing.T) {
	for _, kind := range []string{"version", "identity", "forecast"} {
		t.Run(kind, func(t *testing.T) {
			files := &savedFaultFiles{Directory: testState(t)}
			legacy := savedFixture(0)
			switch kind {
			case "version":
				legacy["schema_version"] = 3.0
			case "identity":
				object(legacy["place"])["provider"] = "unknown"
			case "forecast":
				object(legacy["forecast"])["location"] = savedFixture(1)["location"]
			}
			if _, err := createSavedLocations(files, legacy); err == nil || len(files.writes) > 0 {
				t.Fatal("invalid migration wrote state", err, files.writes)
			}
		})
	}
	files := &savedFaultFiles{Directory: testState(t)}
	store, err := createSavedLocations(files, savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	entry := savedEntry(store.doc, "place-100")
	name := savedSlotName(int(entry["cache_slot"].(float64)))
	cache, _ := files.Read(name, weather.MaxBytes)
	cache["token"] = strings.Repeat("0", 32)
	if err := files.Write(name, cache, weather.MaxBytes); err != nil {
		t.Fatal(err)
	}
	profile, err := store.profile("place-100")
	if err == nil || profile["forecast"] != nil {
		t.Fatal("recycled slot passed generation check", err)
	}
	// Even a syntactically valid malicious local summary cannot cross country coverage.
	bad := store.document()
	object(savedEntry(bad, "place-100")["summary"])["alert_fetched_at"] = nil
	if err := validateSavedLocations(bad); err == nil {
		t.Fatal("current badge without feed time accepted")
	}
}

func TestSavedLocationsMissingOrUnsafeCacheDoesNotChangeIdentity(t *testing.T) {
	state := testState(t)
	store, err := createSavedLocations(state, savedFixture(0))
	if err != nil {
		t.Fatal(err)
	}
	entry := savedEntry(store.doc, "place-100")
	path := filepath.Join(state.Path, savedSlotName(int(entry["cache_slot"].(float64))))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	identity, err := store.profile("place-100")
	if err == nil || identity["forecast"] != nil || identity["mode"] != "place" {
		t.Fatal("missing cache changed identity", err)
	}
	if err := os.Symlink(filepath.Join(state.Path, savedLocationsFile), path); err != nil {
		t.Fatal(err)
	}
	if profile, err := store.profile("place-100"); err == nil || profile["forecast"] != nil {
		t.Fatal("cache link followed", err)
	}
	if _, err := loadSavedLocations(state); err != nil {
		t.Fatal("unsafe cache damaged location list", err)
	}
}

func TestSavedLocationsSummaryIsCompactAndCanonical(t *testing.T) {
	profile := savedFixture(0)
	forecast := object(profile["forecast"])
	forecast["fetched_at"] = "2026-10-08T08:00:00.123456789-04:00"
	object(forecast["current"])["time"] = forecast["fetched_at"]
	alerts := object(forecast["alerts"])
	alerts["items"] = []any{M{"expires": "2026-10-08T18:00:00Z"}, M{"expires": "2026-10-08T16:00:00Z"}}
	summary := savedSummary(profile)
	if summary["fetched_at"] != "2026-10-08T12:00:00.123456Z" || summary["valid_at"] != summary["fetched_at"] || summary["alert_expires"] != "2026-10-08T18:00:00Z" {
		t.Fatal("summary time or alert horizon", summary)
	}
	if err := validateSavedSummary(summary, "US"); err != nil {
		t.Fatal(err)
	}
	profile["country_code"] = "DE"
	summary = savedSummary(profile)
	if summary["alert_status"] != "not_supported_here" || summary["alert_expires"] != nil {
		t.Fatal("US badge leaked to foreign place")
	}
}

func BenchmarkSavedLocationsMetadata(b *testing.B) {
	for _, count := range []int{1, savedLocationLimit} {
		b.Run(fmt.Sprintf("places=%d", count), func(b *testing.B) {
			path := b.TempDir()
			if err := os.Chmod(path, 0700); err != nil {
				b.Fatal(err)
			}
			state, err := safeio.OpenDir(path, false)
			if err != nil {
				b.Fatal(err)
			}
			defer state.Close()
			store, err := createSavedLocations(state, savedFixture(0))
			if err != nil {
				b.Fatal(err)
			}
			for i := 1; i < count; i++ {
				if err := store.put(savedFixture(i), true, false); err != nil {
					b.Fatal(err)
				}
			}
			raw, _ := json.Marshal(store.doc)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := loadSavedLocations(state); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(len(raw)), "metadata-B")
		})
	}
}

func savedAlertRows(count int, expires time.Time) []any {
	rows := make([]any, count)
	for i := range rows {
		rows[i] = M{"id": fmt.Sprintf("saved-alert-%d", i), "event": "Weather warning", "headline": "Weather warning", "severity": "Severe", "urgency": "Immediate", "description": "Warning details", "instruction": "Check official guidance", "effective": savedRuntimeNow.Format(time.RFC3339), "expires": expires.Format(time.RFC3339Nano)}
	}
	return rows
}

func TestSavedLocationsAlertExpiriesRetainFullFeedAndLegacyCache(t *testing.T) {
	files := &savedFaultFiles{Directory: testState(t)}
	profile := savedFixture(0)
	alerts := object(object(profile["forecast"])["alerts"])
	alerts["items"] = savedAlertRows(12, savedRuntimeNow.Add(time.Hour))
	store, err := createSavedLocations(files, profile)
	if err != nil {
		t.Fatal(err)
	}
	entry := savedEntry(store.doc, "place-100")
	summary := object(entry["summary"])
	if len(summary["alert_expiries"].([]any)) != 12 {
		t.Fatal("full feed truncated to frontend display limit", summary)
	}
	deadline := savedRuntimeNow.Add(time.Minute)
	if row := savedSummaryPresentation(summary, savedRuntimeNow, &deadline); row["alert_count"] != 12 || row["alert_status"] != "active" {
		t.Fatal("full-feed badge count", row)
	}
	// A schema-1 manifest written by the prior app keeps its owned full profile.
	legacy := store.document()
	delete(object(savedEntry(legacy, "place-100")["summary"]), "alert_expiries")
	if err := files.Write(savedLocationsFile, legacy, savedMetadataBytes); err != nil {
		t.Fatal(err)
	}
	reopened, err := loadSavedLocations(files)
	if err != nil {
		t.Fatal("legacy manifest rejected", err)
	}
	loaded, err := reopened.profile("place-100")
	if err != nil || !reflect.DeepEqual(loaded, profile) {
		t.Fatal("optional count metadata invalidated legacy full forecast", err)
	}
	legacySummary := object(savedEntry(reopened.doc, "place-100")["summary"])
	if row := savedSummaryPresentation(legacySummary, savedRuntimeNow, &deadline); row["alert_count"] != nil || row["alert_status"] != "active" {
		t.Fatal("legacy unknown count invented", row)
	}
	// Original binding checks remain exact even for a legacy summary.
	legacySummary["temperature_c"] = 99.0
	if loaded, err := reopened.profile("place-100"); err == nil || loaded["forecast"] != nil {
		t.Fatal("legacy comparison weakened original summary ownership")
	}
}

func TestSavedLocationsAlertExpiryMetadataValidationAndBound(t *testing.T) {
	profile := savedFixture(0)
	alerts := object(object(profile["forecast"])["alerts"])
	alerts["items"] = savedAlertRows(256, savedRuntimeNow.Add(time.Hour))
	summary := savedSummary(profile)
	if err := validateSavedSummary(summary, "US"); err != nil {
		t.Fatal("validated full-feed limit rejected", err)
	}
	for _, mutate := range []func(M){
		func(s M) { s["alert_expiries"] = "bad" },
		func(s M) { s["alert_expiries"] = []any{"bad"} },
		func(s M) {
			s["alert_expiries"] = []any{savedRuntimeNow.Add(time.Hour).Format("2006-01-02T15:04:05-0700")}
		},
		func(s M) { s["alert_expiries"] = []any{} },
		func(s M) { s["alert_expiries"] = append(s["alert_expiries"].([]any), s["alert_expires"]) },
		func(s M) { s["alert_expires"] = savedRuntimeNow.Add(2 * time.Hour).Format(time.RFC3339) },
	} {
		bad := weather.Clone(summary).(M)
		mutate(bad)
		if err := validateSavedSummary(bad, "US"); err == nil {
			t.Fatal("malformed per-alert metadata accepted", bad)
		}
	}
	// Twenty worst-case summaries fit the bounded metadata file without slots.
	doc := M{"schema_version": 1.0, "generation": strings.Repeat("a", 32), "primary": "place-100", "viewed": "place-100", "places": []any{}, "cache_order": []any{}}
	for i := 0; i < savedLocationLimit; i++ {
		place := savedFixture(i)
		object(object(place["forecast"])["alerts"])["items"] = savedAlertRows(256, savedRuntimeNow.Add(time.Hour+123456*time.Microsecond))
		id, _ := savedLocationID(place)
		doc["places"] = append(doc["places"].([]any), M{"id": id, "label": "", "profile": savedProfileIdentity(place), "cache_slot": nil, "cache_token": nil, "summary": savedSummary(place)})
	}
	if err := validateSavedLocations(doc); err != nil {
		t.Fatal(err)
	}
	if err := testState(t).Write(savedLocationsFile, doc, savedMetadataBytes); err != nil {
		t.Fatal("bounded full-feed summaries exceed metadata allowance", err)
	}
}
