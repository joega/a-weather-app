package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

func compactSavedFixture(at time.Time, temperature float64) M {
	return M{"fetched_at": savedTime(at.Format(time.RFC3339Nano)), "valid_at": savedTime(at.Format(time.RFC3339Nano)), "temperature_c": temperature, "condition": "clear", "is_day": true}
}

func currentSummaryCacheRow(a *App, id string, summary M) M {
	return M{"id": id, "identity": savedProfileIdentity(object(savedEntry(a.saved.doc, id)["profile"])), "summary": summary}
}

func TestSavedCurrentSummaryCacheRestartPreservesForecastAndAlertAge(t *testing.T) {
	now := savedRuntimeNow.Add(20 * time.Minute)
	a, state := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	entry := savedEntry(a.saved.doc, "place-100")
	beforeEntry := weather.Clone(entry)
	beforeProfile, err := a.saved.profile("place-100")
	if err != nil {
		t.Fatal(err)
	}
	current := compactSavedFixture(now, 31)
	a.savedSummaries["place-100"] = savedCurrentSummary{identity: savedProfileIdentity(object(entry["profile"])), summary: current}
	if err := a.persistSavedSummaries(); err != nil {
		t.Fatal(err)
	}
	a.savedList.items = nil
	row := object(object(a.Snapshot()["saved_locations"])["items"].([]any)[0])
	summary := object(row["summary"])
	if summary["temperature_c"] != 31.0 || summary["freshness"] != "fresh" || summary["alert_status"] != "unavailable" {
		t.Fatal("compact weather renewed old alert status", summary)
	}
	combined := a.overlaySavedSummary(entry)
	if combined["alert_fetched_at"] != object(entry["summary"])["alert_fetched_at"] {
		t.Fatal("compact weather renewed alert timestamp", combined)
	}
	afterProfile, err := a.saved.profile("place-100")
	if err != nil || !reflect.DeepEqual(beforeProfile, afterProfile) || !reflect.DeepEqual(beforeEntry, entry) {
		t.Fatal("compact cache changed forecast-slot integrity", err)
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(state, Options{Offline: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.Close(context.Background()) })
	row = object(object(restarted.Snapshot()["saved_locations"])["items"].([]any)[0])
	if object(row["summary"])["temperature_c"] != 31.0 || len(restarted.savedSummaries) != 1 || restarted.forecast == nil {
		t.Fatal("offline restart lost current cache or full forecast", row)
	}
	now = now.Add(3 * time.Hour)
	row = object(object(restarted.Snapshot()["saved_locations"])["items"].([]any)[0])
	if object(row["summary"])["freshness"] != "expired" {
		t.Fatal("persisted current conditions never expired", row)
	}
}

func TestSavedCurrentSummaryCachePrunesRemovedAndChangedIdentity(t *testing.T) {
	a, state := runtimeLocations(t, Options{Offline: true}, 0)
	for _, id := range []string{"place-101", "place-102"} {
		entry := savedEntry(a.saved.doc, id)
		a.savedSummaries[id] = savedCurrentSummary{identity: savedProfileIdentity(object(entry["profile"])), summary: compactSavedFixture(savedRuntimeNow.Add(time.Minute), 29)}
	}
	if err := a.persistSavedSummaries(); err != nil {
		t.Fatal(err)
	}
	if err := a.saved.remove("place-101", ""); err != nil {
		t.Fatal(err)
	}
	profile := savedFixture(2)
	profile["country_code"] = "DE"
	profile["forecast"] = nil
	object(profile["location"])["latitude"] = 23.0
	if err := a.saved.put(profile, false, false); err != nil {
		t.Fatal(err)
	}
	if a.overlaySavedSummary(savedEntry(a.saved.doc, "place-102")) != nil {
		t.Fatal("old current conditions crossed a location identity change")
	}
	a.initSavedSummaries()
	if len(a.savedSummaries) != 0 {
		t.Fatal("restart admitted deleted or relocated identities", a.savedSummaries)
	}
	// Publication also prunes records already resident when a place changes.
	a.savedSummaries["place-101"] = savedCurrentSummary{identity: savedProfileIdentity(savedFixture(1)), summary: compactSavedFixture(savedRuntimeNow, 29)}
	a.savedSummaries["place-102"] = savedCurrentSummary{identity: savedProfileIdentity(savedFixture(2)), summary: compactSavedFixture(savedRuntimeNow, 29)}
	if err := a.persistSavedSummaries(); err != nil {
		t.Fatal(err)
	}
	doc, err := state.Read(savedCurrentSummariesFile, savedMetadataBytes)
	if err != nil || len(doc["items"].([]any)) != 0 || len(a.savedSummaries) != 0 {
		t.Fatal("publication retained invalid identities", doc, err)
	}
}

func TestSavedCurrentSummaryCacheRejectsCorruptionWithoutChangingLocations(t *testing.T) {
	for _, kind := range []string{"schema", "duplicate", "fields", "summary", "identity", "limit"} {
		t.Run(kind, func(t *testing.T) {
			a, state := runtimeLocations(t, Options{Offline: true}, 0)
			before := weather.Clone(a.saved.doc)
			first := currentSummaryCacheRow(a, "place-100", compactSavedFixture(savedRuntimeNow.Add(time.Minute), 27))
			second := currentSummaryCacheRow(a, "place-101", compactSavedFixture(savedRuntimeNow.Add(time.Minute), 28))
			doc := M{"schema_version": 1.0, "items": []any{first, second}}
			switch kind {
			case "schema":
				doc["schema_version"] = 2.0
			case "duplicate":
				doc["items"] = []any{first, first}
			case "fields":
				second["extra"] = true
			case "summary":
				object(second["summary"])["temperature_c"] = 999.0
			case "identity":
				object(second["identity"])["forecast"] = appFixture(savedRuntimeNow)
			case "limit":
				items := []any{}
				for i := 0; i <= savedLocationLimit; i++ {
					items = append(items, first)
				}
				doc["items"] = items
			}
			if err := state.Write(savedCurrentSummariesFile, doc, savedMetadataBytes); err != nil {
				t.Fatal(err)
			}
			a.initSavedSummaries()
			if len(a.savedSummaries) != 0 || !reflect.DeepEqual(before, a.saved.doc) || a.forecast == nil {
				t.Fatal("corrupt optional cache damaged saved identity or forecast")
			}
		})
	}
}

func TestSavedCurrentSummaryOverlayUsesBothTimestampsAndIndependentAlerts(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 0)
	entry := savedEntry(a.saved.doc, "place-100")
	previous := object(entry["summary"])
	for _, tc := range []struct {
		fetched, valid time.Duration
		useCurrent     bool
	}{
		{0, 0, false},
		{time.Minute, time.Minute, true},
		{time.Minute, -time.Minute, false},
		{-time.Minute, time.Minute, false},
		{0, time.Minute, true},
	} {
		current := compactSavedFixture(savedRuntimeNow.Add(tc.fetched), 30)
		current["valid_at"] = savedTime(savedRuntimeNow.Add(tc.valid).Format(time.RFC3339))
		a.savedSummaries["place-100"] = savedCurrentSummary{identity: savedProfileIdentity(object(entry["profile"])), summary: current}
		combined := a.overlaySavedSummary(entry)
		if (combined["temperature_c"] == 30.0) != tc.useCurrent {
			t.Fatal("timestamp order selected incorrect current conditions", tc, combined)
		}
		for _, field := range []string{"alert_expires", "alert_fetched_at", "alert_status", "alert_expiries"} {
			if !reflect.DeepEqual(combined[field], previous[field]) {
				t.Fatal("current cache changed independent alerts", field, combined)
			}
		}
	}
	// Weather for an uncached foreign place must not invent US alert coverage.
	foreign := savedEntry(a.saved.doc, "place-101")
	foreign["summary"] = nil
	a.savedSummaries["place-101"] = savedCurrentSummary{identity: savedProfileIdentity(object(foreign["profile"])), summary: compactSavedFixture(savedRuntimeNow, 25)}
	if a.overlaySavedSummary(foreign)["alert_status"] != "not_supported_here" {
		t.Fatal("foreign compact cache invented US alert coverage")
	}
}

func TestSavedCurrentSummaryFutureCacheDoesNotMaskValidWeather(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 0)
	entry := savedEntry(a.saved.doc, "place-100")
	previous := object(entry["summary"])
	identity := savedProfileIdentity(object(entry["profile"]))
	a.savedSummaries["place-100"] = savedCurrentSummary{identity: identity, summary: compactSavedFixture(savedRuntimeNow.Add(6*time.Minute), 30)}
	if err := a.persistSavedSummaries(); err != nil {
		t.Fatal(err)
	}
	a.initSavedSummaries()
	if !reflect.DeepEqual(a.overlaySavedSummary(entry), previous) {
		t.Fatal("future compact cache masked valid forecast weather")
	}
	// A legitimate response can recover from an invalid future full summary,
	// even though its real timestamps are earlier than the corrupt timestamps.
	invalid := weather.Clone(previous).(M)
	invalid["fetched_at"] = savedTime(savedRuntimeNow.Add(time.Hour).Format(time.RFC3339))
	invalid["valid_at"] = invalid["fetched_at"]
	entry["summary"] = invalid
	a.savedSummaries["place-100"] = savedCurrentSummary{identity: identity, summary: compactSavedFixture(savedRuntimeNow, 30)}
	combined := a.overlaySavedSummary(entry)
	if combined["temperature_c"] != 30.0 || combined["fetched_at"] != savedTime(savedRuntimeNow.Format(time.RFC3339)) {
		t.Fatal("valid current conditions could not recover future forecast summary", combined)
	}
}

func TestSavedCurrentSummaryOverlayPreservesExactAlertCount(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 0)
	entry := savedEntry(a.saved.doc, "place-100")
	profile := savedFixture(0)
	object(object(profile["forecast"])["alerts"])["items"] = savedAlertRows(12, savedRuntimeNow.Add(time.Hour))
	entry["summary"] = savedSummary(profile)
	a.savedSummaries["place-100"] = savedCurrentSummary{identity: savedProfileIdentity(object(entry["profile"])), summary: compactSavedFixture(savedRuntimeNow.Add(time.Minute), 30)}
	combined := a.overlaySavedSummary(entry)
	if combined["temperature_c"] != 30.0 || !reflect.DeepEqual(combined["alert_expiries"], object(entry["summary"])["alert_expiries"]) {
		t.Fatal("weather summary warming lost official alert metadata", combined)
	}
	deadline := savedRuntimeNow.Add(time.Minute)
	if row := savedSummaryPresentation(combined, savedRuntimeNow, &deadline); row["alert_status"] != "active" || row["alert_count"] != 12 {
		t.Fatal("weather summary warming changed exact official alert count", row)
	}
}

func TestSavedLegacyAlertCountUsesOnlyAlreadyLoadedOwnedForecast(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 0)
	profile := savedFixture(0)
	object(object(profile["forecast"])["alerts"])["items"] = savedAlertRows(12, savedRuntimeNow.Add(time.Hour))
	if err := a.saved.put(profile, false, false); err != nil {
		t.Fatal(err)
	}
	legacy := a.saved.document()
	delete(object(savedEntry(legacy, "place-100")["summary"]), "alert_expiries")
	if err := a.saved.commit(legacy); err != nil {
		t.Fatal(err)
	}
	// Normal navigation/load reads the one requested slot before presentation.
	a.primary = a.loadPoint("place-100")
	a.forecastPoint = a.primary
	files := &savedFaultFiles{Directory: a.state}
	a.saved.files = files
	entry := savedEntry(a.saved.doc, "place-100")
	before := weather.Clone(entry)
	deadline := savedRuntimeNow.Add(time.Minute)
	if row := savedSummaryPresentation(a.overlaySavedSummary(entry), savedRuntimeNow, &deadline); row["alert_count"] != 12 || row["alert_status"] != "active" {
		t.Fatal("owned loaded legacy cache did not reveal exact count", row)
	}
	if !reflect.DeepEqual(entry, before) || len(files.reads) != 0 || len(files.writes) != 0 {
		t.Fatal("presentation enrichment changed storage or opened forecast slots")
	}
	// Compact summary warming keeps the independently inferred official count.
	a.savedSummaries["place-100"] = savedCurrentSummary{identity: savedProfileIdentity(object(entry["profile"])), summary: compactSavedFixture(savedRuntimeNow.Add(time.Minute), 30)}
	if row := savedSummaryPresentation(a.overlaySavedSummary(entry), savedRuntimeNow, &deadline); row["alert_count"] != 12 || row["temperature_c"] != 30.0 {
		t.Fatal("summary warming lost inferred legacy count", row)
	}
	delete(a.savedSummaries, "place-100")
	// A same-ID point with different geographic identity cannot lend alerts.
	ownedProfile := a.primary.profile
	a.primary.profile = weather.Clone(ownedProfile).(M)
	object(a.primary.profile["location"])["latitude"] = 21.0
	if row := savedSummaryPresentation(a.overlaySavedSummary(entry), savedRuntimeNow, &deadline); row["alert_count"] != nil {
		t.Fatal("different point identity invented a legacy count", row)
	}
	a.primary.profile = ownedProfile
	// Exact original weather/alert summary fields guard newer/other caches too.
	object(a.primary.forecast["current"])["temperature_c"] = 99.0
	if row := savedSummaryPresentation(a.overlaySavedSummary(entry), savedRuntimeNow, &deadline); row["alert_count"] != nil {
		t.Fatal("mismatched full forecast invented a legacy count", row)
	}
	if len(files.reads) != 0 || len(files.writes) != 0 {
		t.Fatal("legacy count guard performed extra I/O", files.reads, files.writes)
	}
}
