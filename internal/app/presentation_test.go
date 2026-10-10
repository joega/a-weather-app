package app

import (
	"bufio"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestPresentationSubscriptionAndRestore(t *testing.T) {
	for _, cached := range []bool{false, true} {
		name := "available"
		if cached {
			name = "cached_during_mutation"
		}
		t.Run(name, func(t *testing.T) {
			testPresentationSubscriptionAndRestore(t, cached)
		})
	}
}

func testPresentationSubscriptionAndRestore(t *testing.T, cached bool) {
	f := serveFixture(t)
	c := connect(t, f.path)
	r := bufio.NewReader(c)
	send := func(op string, patch M) M {
		t.Helper()
		if err := ipc.Send(c, request(op, patch), ipc.RequestLimit); err != nil {
			t.Fatal(err)
		}
		return readReply(t, r)
	}
	if v := send("set_presentation", M{"active": false}); v["ok"] != false {
		t.Fatal("unsubscribed presentation accepted", v)
	}
	initial := send("subscribe", nil)
	revision := object(initial["snapshot"])["snapshot_revision"].(float64)
	for _, patch := range []M{{"active": "false"}, {"active": false, "extra": true}, {}} {
		if v := send("set_presentation", patch); v["ok"] != false {
			t.Fatal("malformed presentation accepted", v)
		}
	}
	hidden := send("set_presentation", M{"active": false})
	if hidden["ok"] != true || hidden["snapshot"] != nil {
		t.Fatal("hide returned full snapshot", hidden)
	}
	changed := send("set_controls", M{"controls": M{"units": "C"}})
	changedSnapshot := object(changed["snapshot"])
	if changed["ok"] != true || object(changedSnapshot["controls"])["units"] != "C" {
		t.Fatal("hidden control change failed", changed)
	}
	changedRevision := changedSnapshot["snapshot_revision"].(float64)
	if changedRevision <= revision {
		t.Fatal("hidden control change did not advance snapshot", changed)
	}
	if cached {
		// Snapshot deliberately serves the last complete state while a mutation
		// owns the app mutex. Restoring visibility must retain the acknowledged
		// change without requiring a new revision or waiting for that mutex.
		f.a.mu.Lock()
		defer f.a.mu.Unlock()
	}
	restored := send("set_presentation", M{"active": true})
	restoredSnapshot := object(restored["snapshot"])
	if restored["ok"] != true || restoredSnapshot["snapshot_revision"].(float64) < changedRevision || object(restoredSnapshot["controls"])["units"] != "C" {
		t.Fatal("restore lost acknowledged hidden change", restored)
	}
}

func TestDisplayRowsInvalidateAtBoundariesAndReplacement(t *testing.T) {
	now := time.Date(2026, 9, 29, 23, 30, 0, 0, time.UTC)
	a := newTestApp(t, Options{Offline: true, Now: func() time.Time { return now }})
	a.location["timezone"] = "UTC"
	a.forecast = appFixture(now)
	a.forecast["hourly"] = []any{M{"time": now.Add(30 * time.Minute).Format(time.RFC3339), "condition": "rain", "is_day": false}}
	a.forecast["daily"] = []any{M{"date": "2026-09-30", "condition": "clear"}}
	first := a.Snapshot()
	if len(first["hourly"].([]any)) != 1 {
		t.Fatal(first)
	}
	now = now.Add(30 * time.Minute)
	equal := a.Snapshot()
	if len(equal["hourly"].([]any)) != 1 {
		t.Fatal("row expired at equal timestamp")
	}
	if object(equal["daily"].([]any)[0])["day_label"] != "Today" {
		t.Fatal("local day label stale")
	}
	now = now.Add(time.Nanosecond)
	if len(a.Snapshot()["hourly"].([]any)) != 0 {
		t.Fatal("expired row retained")
	}
	a.forecast = weather.Clone(a.forecast).(M)
	a.forecast["hourly"] = []any{M{"time": now.Add(time.Hour).Format(time.RFC3339), "condition": "snow", "is_day": false}}
	if object(a.Snapshot()["hourly"].([]any)[0])["condition"] != "snow" {
		t.Fatal("forecast replacement stale")
	}
	now = now.Add(-time.Hour)
	if len(a.Snapshot()["hourly"].([]any)) != 1 {
		t.Fatal("backward clock stale")
	}
}

func TestPeriodicSnapshotsOnlyReachPresentedPeers(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, Options{Offline: true, Now: func() time.Time { return now }})
	visible := &peer{subscribed: true, presentationActive: true, out: make(chan outbound, 4), closed: make(chan struct{})}
	hidden := &peer{subscribed: true, out: make(chan outbound, 4), closed: make(chan struct{})}
	s := &Server{app: a, peers: map[*peer]bool{visible: true, hidden: true}}
	s.snapshotTo(true)
	if len(visible.out) != 1 || len(hidden.out) != 0 {
		t.Fatal("periodic broadcast reached hidden peer")
	}
	a.controls["units"] = "C"
	s.snapshot()
	if len(visible.out) != 2 || len(hidden.out) != 1 {
		t.Fatal("state change did not reach all subscribers")
	}
}
