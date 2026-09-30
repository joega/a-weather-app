package notifications

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestQuietTransitionsAcrossDST(t *testing.T) {
	for _, tc := range []struct {
		name, zone, now, want string
		start, end            float64
	}{
		{"fall repeated hour", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T07:00:00Z", 22, 2},
		{"spring missing end", "America/New_York", "2026-03-08T06:30:00Z", "2026-03-08T07:00:00Z", 22, 2},
		{"spring missing start", "America/New_York", "2026-03-08T06:30:00Z", "2026-03-08T07:00:00Z", 2, 7},
		{"half hour fall", "Australia/Lord_Howe", "2026-04-04T14:45:00Z", "2026-04-04T15:30:00Z", 22, 2},
		{"half hour spring", "Australia/Lord_Howe", "2026-10-03T15:15:00Z", "2026-10-03T15:30:00Z", 22, 2},
		{"nonintegral offset", "Asia/Kathmandu", "2026-09-29T00:30:00Z", "2026-09-29T01:15:00Z", 22, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zone, err := time.LoadLocation(tc.zone)
			if err != nil {
				t.Fatal(err)
			}
			settings := Defaults()
			settings["quiet_start"], settings["quiet_end"] = tc.start, tc.end
			got := nextQuietTransition(settings, stamp(tc.now), zone)
			if !got.Equal(stamp(tc.want)) {
				t.Fatalf("transition %s, want %s", got, tc.want)
			}
			if quietAt(settings, got.Add(-time.Nanosecond), zone) == quietAt(settings, got, zone) {
				t.Fatal("deadline does not change quiet policy")
			}
		})
	}
}

func TestIntervalSnoozeFreshnessFutureAndClockChanges(t *testing.T) {
	w := testWatcher(t, testState(t), func(context.Context, string, string) error { t.Error("dry sender invoked"); return nil })
	enable(t, w)
	f, l := testForecast(testNow)
	f["hourly"] = []any{}
	if err := w.Snooze(testNow, false); err != nil {
		t.Fatal(err)
	}
	w.Tick(f, l, testNow, true)
	expires := testNow.Add(45*time.Minute + time.Nanosecond)
	if got := w.Interval(testNow); got != expires.Sub(testNow) {
		t.Fatalf("freshness interval %s", got)
	}
	w.Tick(f, l, expires, true)
	if w.mode != "paused" || w.Interval(expires) != testNow.Add(time.Hour).Sub(expires) {
		t.Fatal("lost snooze deadline after stale boundary")
	}
	w.Tick(f, l, testNow.Add(time.Hour), true)
	if w.mode != "waiting" {
		t.Fatalf("resume mode %s", w.mode)
	}
	if err := w.Snooze(testNow, true); err != nil {
		t.Fatal(err)
	}
	f["fetched_at"] = testNow.Add(10 * time.Second).Format(time.RFC3339Nano)
	w.Tick(f, l, testNow, true)
	if w.mode != "waiting" || w.Interval(testNow) != 10*time.Second {
		t.Fatal("future fetched-at deadline missing")
	}
	w.Tick(f, l, testNow.Add(10*time.Second), true)
	if w.mode != "watching" || w.Interval(testNow.Add(10*time.Second)) != 45*time.Minute+time.Nanosecond {
		t.Fatal("fresh forecast did not become eligible at timestamp")
	}
	if w.Interval(testNow) != time.Nanosecond {
		t.Fatal("backwards clock did not request reevaluation")
	}
}

func TestWatcherQuietDeadlineAndZoneMutation(t *testing.T) {
	w := testWatcher(t, testState(t), func(context.Context, string, string) error { t.Error("quiet sender invoked"); return nil })
	if err := w.Configure(M{"enabled": true, "quiet_end": float64(2)}); err != nil {
		t.Fatal(err)
	}
	now := stamp("2026-11-01T05:30:00Z")
	f, l := testForecast(now)
	f["fetched_at"] = now.Add(-time.Hour).Format(time.RFC3339)
	w.Tick(f, l, now, true)
	if w.mode != "quiet" || w.Interval(now) != 90*time.Minute {
		t.Fatalf("quiet interval %s, mode %s", w.Interval(now), w.mode)
	}
	l["timezone"] = "UTC"
	w.Tick(f, l, now, true)
	if w.mode != "waiting" || w.zone.String() != "UTC" || w.Interval(now) != 16*time.Hour+30*time.Minute {
		t.Fatal("in-place timezone change retained quiet cache")
	}
	l["timezone"] = "invalid/zone"
	w.Tick(f, l, now, true)
	if w.zone != nil || w.mode != "waiting" {
		t.Fatal("invalid timezone reused last valid zone")
	}
	l["timezone"] = "UTC"
	w.Tick(f, l, now, true)
	if w.zone == nil {
		t.Fatal("valid timezone failed to reactivate")
	}
}

func TestCandidateCacheLookaheadHourAndMutableInputs(t *testing.T) {
	f, l := testForecast(testNow)
	zone, err := time.LoadLocation(str(l["timezone"]))
	if err != nil {
		t.Fatal(err)
	}
	var cache candidateCache
	row := f["hourly"].([]any)[0].(M)
	row["time"] = testNow.Add(5 * time.Hour).Format(time.RFC3339)
	event, next := cache.candidate(f, l, testNow, 50, zone)
	if event != nil || !next.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("lookahead deadline %s, event %v", next, event)
	}
	event, _ = cache.candidate(f, l, next.Add(-time.Nanosecond), 50, zone)
	if event != nil {
		t.Fatal("candidate entered lookahead early")
	}
	event, next = cache.candidate(f, l, next, 50, zone)
	if event == nil || !next.Equal(testNow.Add(5*time.Hour)) {
		t.Fatal("candidate failed at exact lookahead boundary")
	}
	row["precipitation_probability"] = float64(.6)
	event, _ = cache.candidate(f, l, testNow.Add(time.Hour), 70, zone)
	if event != nil {
		t.Fatal("probability policy reused candidate")
	}
	event, _ = cache.candidate(f, l, testNow.Add(time.Hour), 50, zone)
	if event == nil || !strings.Contains(str(event["body"]), "60%") {
		t.Fatal("in-place chance change retained body")
	}
	row["time"] = testNow.Add(time.Hour).Format(time.RFC3339)
	event, next = cache.candidate(f, l, testNow, 50, zone)
	if event["start"] != float64(testNow.Unix()) || !next.Equal(testNow.Add(time.Hour)) {
		t.Fatal("in-place time change retained interval")
	}
	f["hourly"] = append(f["hourly"].([]any), M{"time": testNow.Add(2 * time.Hour).Format(time.RFC3339), "precipitation_probability": float64(.8)})
	event, next = cache.candidate(f, l, testNow, 50, zone)
	if event["end"] != float64(testNow.Add(2*time.Hour).Unix()) {
		t.Fatal("appended row did not extend group")
	}
	event, _ = cache.candidate(f, l, next, 50, zone)
	if !strings.Contains(str(event["body"]), "80%") {
		t.Fatal("hour boundary retained earlier hour body")
	}
	l["name"] = "Renamed place"
	event, _ = cache.candidate(f, l, testNow, 50, zone)
	if !strings.Contains(str(event["body"]), "Renamed place") {
		t.Fatal("rename retained body")
	}
	key := event["location"]
	l["latitude"] = float64(41)
	event, _ = cache.candidate(f, l, testNow, 50, zone)
	if event["location"] == key {
		t.Fatal("coordinate change retained dedup key")
	}
	l["latitude"] = "bad"
	if event, _ = cache.candidate(f, l, testNow, 50, zone); event != nil {
		t.Fatal("invalid coordinates retained event")
	}
}

func TestCandidateCacheTimeFilterAndSnapshotIsolation(t *testing.T) {
	f, l := testForecast(testNow)
	zone := time.UTC
	var cache candidateCache
	row := f["hourly"].([]any)[0].(M)
	row["time"] = testNow.Add(11*24*time.Hour + time.Second).Format(time.RFC3339Nano)
	cache.candidate(f, l, testNow, 50, zone)
	if len(cache.groups) != 0 || !cache.groupUntil.Equal(testNow.Add(time.Second)) {
		t.Fatal("future filter entry deadline missing")
	}
	cache.candidate(f, l, testNow.Add(time.Second), 50, zone)
	if len(cache.groups) != 1 {
		t.Fatal("inclusive future time bound not honored")
	}
	cache.candidate(f, l, testNow, 50, zone)
	if len(cache.groups) != 0 {
		t.Fatal("backwards clock retained time-filtered groups")
	}
	row["time"] = testNow.Add(-11 * 24 * time.Hour).Format(time.RFC3339Nano)
	cache.candidate(f, l, testNow, 50, zone)
	if len(cache.groups) != 1 || !cache.groupUntil.Equal(testNow.Add(time.Nanosecond)) {
		t.Fatal("inclusive past bound not honored")
	}
	cache.candidate(f, l, testNow.Add(time.Nanosecond), 50, zone)
	if len(cache.groups) != 0 {
		t.Fatal("expired past row retained")
	}
	w := testWatcher(t, testState(t), func(context.Context, string, string) error { t.Error("sender invoked"); return nil })
	enable(t, w)
	snapshot := w.Snapshot()
	snapshot["settings"].(M)["probability"] = float64(70)
	if reflect.DeepEqual(snapshot["settings"], w.document["settings"]) {
		t.Fatal("snapshot aliases live settings")
	}
}

func TestPendingIntervalKeepsNearPolicyDeadlineAndCancels(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	w := testWatcher(t, testState(t), func(ctx context.Context, _, _ string) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	enable(t, w)
	f, l := testForecast(testNow)
	f["fetched_at"] = testNow.Add(-45*time.Minute + 500*time.Millisecond).Format(time.RFC3339Nano)
	w.Tick(f, l, testNow, true)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("sender not started")
	}
	if got := w.Interval(testNow); got != 500*time.Millisecond+time.Nanosecond {
		t.Fatalf("pending interval skipped freshness deadline: %s", got)
	}
	w.Tick(f, l, testNow.Add(w.Interval(testNow)), true)
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("stale deadline failed to cancel pending sender")
	}
}
