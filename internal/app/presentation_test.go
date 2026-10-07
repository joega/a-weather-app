package app

import (
	"bufio"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestPresentationSubscriptionAndRestore(t *testing.T) {
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
	restored := send("set_presentation", M{"active": true})
	if restored["ok"] != true || object(restored["snapshot"])["snapshot_revision"].(float64) <= revision {
		t.Fatal("restore lacks fresh snapshot", restored)
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
