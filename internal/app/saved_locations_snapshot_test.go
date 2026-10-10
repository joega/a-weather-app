package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func BenchmarkFullSavedLocationsSnapshot(b *testing.B) {
	a := benchmarkIdleApp(b)
	doc := M{"schema_version": 1.0, "generation": strings.Repeat("a", 32), "primary": "place-100", "viewed": "place-100", "places": []any{}, "cache_order": []any{}}
	for i := 0; i < savedLocationLimit; i++ {
		profile := savedFixture(i)
		id, err := savedLocationID(profile)
		if err != nil {
			b.Fatal(err)
		}
		doc["places"] = append(doc["places"].([]any), M{"id": id, "label": "", "profile": savedProfileIdentity(profile), "cache_slot": nil, "cache_token": nil, "summary": savedSummary(profile)})
	}
	if err := validateSavedLocations(doc); err != nil {
		b.Fatal(err)
	}
	a.saved.doc = doc
	a.savedList = savedListPresentation{}
	a.Snapshot()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := json.Marshal(a.Snapshot()); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSavedListSnapshotUsesMetadataAndReusesPresentation(t *testing.T) {
	a, _ := runtimeLocations(t, Options{Offline: true}, 1)
	files := &savedFaultFiles{Directory: a.state}
	a.saved.files = files
	first := object(a.Snapshot()["saved_locations"])
	cached := a.savedList.items[0]
	if first["primary"] != "place-100" || first["viewed"] != "place-101" || first["primary_forecast_available"] != true || len(first["items"].([]any)) != 3 {
		t.Fatal("saved list context", first)
	}
	object(first["items"].([]any)[0])["name"] = "Caller changed name"
	object(object(first["items"].([]any)[0])["summary"])["temperature_c"] = 99.0
	second := object(a.Snapshot()["saved_locations"])
	if object(second["items"].([]any)[0])["name"] != "City 0" || object(object(second["items"].([]any)[0])["summary"])["temperature_c"] != 15.0 {
		t.Fatal("saved list leaked mutable state")
	}
	if reflect.ValueOf(cached).Pointer() != reflect.ValueOf(a.savedList.items[0]).Pointer() {
		t.Fatal("unchanged list rebuilt")
	}
	if len(files.reads) != 0 || len(files.writes) != 0 {
		t.Fatal("list presentation performed storage I/O", files.reads, files.writes)
	}
	if err := a.saved.rename("place-101", "Work"); err != nil {
		t.Fatal(err)
	}
	third := object(a.Snapshot()["saved_locations"])
	if object(third["items"].([]any)[1])["label"] != "Work" {
		t.Fatal("published metadata did not invalidate list")
	}
}

func TestSavedSummaryPresentationTimeAndWarningBoundaries(t *testing.T) {
	summary := savedSummary(savedFixture(0))
	delete(summary, "alert_expiries") // Exercise the prior manifest representation.
	summary["alert_expires"] = savedRuntimeNow.Add(time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		age            time.Duration
		weather, alert string
	}{
		{0, "fresh", "active"},
		{15 * time.Minute, "fresh", "active"},
		{15*time.Minute + time.Nanosecond, "fresh", "cached"},
		{45 * time.Minute, "fresh", "cached"},
		{45*time.Minute + time.Nanosecond, "stale", "unavailable"},
		{2 * time.Hour, "stale", "unavailable"},
		{2*time.Hour + time.Nanosecond, "expired", "unavailable"},
		{-5 * time.Minute, "fresh", "unavailable"},
		{-5*time.Minute - time.Nanosecond, "invalid_future", "unavailable"},
	} {
		t.Run(tc.age.String(), func(t *testing.T) {
			now := savedRuntimeNow.Add(tc.age)
			expires := now.Add(time.Minute)
			row := savedSummaryPresentation(summary, now, &expires)
			if row["freshness"] != tc.weather || row["alert_status"] != tc.alert {
				t.Fatal("saved status", row, tc)
			}
			if !expires.After(now) || expires.After(now.Add(time.Minute)) {
				t.Fatal("invalid freshness deadline", expires)
			}
		})
	}
	expires := savedRuntimeNow.Add(time.Minute)
	summary["alert_expires"] = savedRuntimeNow.Add(time.Second).Format(time.RFC3339)
	row := savedSummaryPresentation(summary, savedRuntimeNow, &expires)
	if expires != savedRuntimeNow.Add(time.Second) || row["alert_status"] != "active" {
		t.Fatal("alert expiry failed to invalidate badge")
	}
	expires = savedRuntimeNow.Add(time.Minute)
	if row := savedSummaryPresentation(summary, savedRuntimeNow.Add(time.Second), &expires); row["alert_status"] != "none" {
		t.Fatal("expired alert retained", row)
	}
	summary["alert_status"] = "stale"
	if row := savedSummaryPresentation(summary, savedRuntimeNow.Add(time.Second), &expires); row["alert_status"] != "unavailable" {
		t.Fatal("stale empty feed claimed all clear", row)
	}
}

func TestSavedListPresentationInvalidatesOnClockChanges(t *testing.T) {
	now := savedRuntimeNow
	a, _ := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	a.Snapshot()
	before := a.savedList.items[0]
	now = now.Add(45*time.Minute + time.Nanosecond)
	value := object(a.Snapshot()["saved_locations"])
	if object(object(value["items"].([]any)[0])["summary"])["freshness"] != "stale" || reflect.ValueOf(before).Pointer() == reflect.ValueOf(a.savedList.items[0]).Pointer() {
		t.Fatal("aged cached summary stayed fresh")
	}
	now = savedRuntimeNow
	value = object(a.Snapshot()["saved_locations"])
	if object(object(value["items"].([]any)[0])["summary"])["freshness"] != "fresh" {
		t.Fatal("backwards clock kept stale summary")
	}
	var tree any
	raw, err := json.Marshal(value)
	if err != nil || json.Unmarshal(raw, &tree) != nil {
		t.Fatal("invalid list JSON", err)
	}
	if len(raw) > 24*1024 {
		t.Fatal("compact list exceeded its byte allowance", len(raw))
	}
}

func TestSavedSummaryAlertCountExpiryAndIndependentFreshness(t *testing.T) {
	profile := savedFixture(0)
	alerts := object(object(profile["forecast"])["alerts"])
	alerts["items"] = append(savedAlertRows(2, savedRuntimeNow.Add(2*time.Second)), savedAlertRows(1, savedRuntimeNow.Add(time.Hour))...)
	alerts["items"] = append(alerts["items"].([]any), savedAlertRows(1, savedRuntimeNow.Add(-time.Second))...)
	summary := savedSummary(profile)
	for _, tc := range []struct {
		age    time.Duration
		count  int
		status string
	}{
		{0, 3, "active"},
		{2 * time.Second, 1, "active"},
		{16 * time.Minute, 1, "cached"},
		{45 * time.Minute, 1, "cached"},
		{45*time.Minute + time.Nanosecond, 0, "unavailable"},
		{-time.Second, 0, "unavailable"},
	} {
		now := savedRuntimeNow.Add(tc.age)
		deadline := now.Add(time.Minute)
		row := savedSummaryPresentation(summary, now, &deadline)
		if row["alert_count"] != tc.count || row["alert_status"] != tc.status {
			t.Fatal("count/expiry/freshness mismatch", tc, row)
		}
		if tc.age == 0 && deadline != savedRuntimeNow.Add(2*time.Second) {
			t.Fatal("individual expiry did not invalidate list presentation", deadline)
		}
	}
	alerts["items"] = []any{}
	summary = savedSummary(profile)
	deadline := savedRuntimeNow.Add(time.Minute)
	if row := savedSummaryPresentation(summary, savedRuntimeNow, &deadline); row["alert_status"] != "none" || row["alert_count"] != 0 {
		t.Fatal("empty official feed count", row)
	}
}

func TestSavedListAlertCountRebuildUsesNoForecastReads(t *testing.T) {
	now := savedRuntimeNow
	a, _ := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	profile := savedFixture(0)
	object(object(profile["forecast"])["alerts"])["items"] = append(savedAlertRows(1, now.Add(time.Second)), savedAlertRows(1, now.Add(time.Hour))...)
	savedEntry(a.saved.doc, "place-100")["summary"] = savedSummary(profile)
	a.savedList = savedListPresentation{}
	files := &savedFaultFiles{Directory: a.state}
	a.saved.files = files
	list := object(a.savedLocationsSnapshot())
	if count := object(object(list["items"].([]any)[0])["summary"])["alert_count"]; count != 2 {
		t.Fatal("initial count", count)
	}
	now = now.Add(time.Second)
	list = object(a.savedLocationsSnapshot())
	if count := object(object(list["items"].([]any)[0])["summary"])["alert_count"]; count != 1 {
		t.Fatal("cached presentation missed individual expiry", count)
	}
	if len(files.reads) != 0 {
		t.Fatal("list count opened forecast slots", files.reads)
	}
}
