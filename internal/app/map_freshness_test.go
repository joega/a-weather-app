package app

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMapFreshnessEvents(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, Options{Now: func() time.Time { return now }, Offline: true})
	data := mapFixture(now, 40.7128, -74.006)
	a.wmap.data = &data
	a.wmap.open = true
	p := &peer{out: make(chan outbound, 4), closed: make(chan struct{}), subscribed: true}
	s := &Server{app: a, peers: map[*peer]bool{p: true}}
	for _, tc := range []struct {
		age     time.Duration
		status  string
		payload bool
	}{
		{0, "fresh", true}, {20*time.Minute + time.Nanosecond, "stale", true},
		{6*time.Hour + time.Nanosecond, "unavailable", false},
		{time.Minute, "fresh", true}, {-6 * time.Minute, "unavailable", false},
		{-5 * time.Minute, "fresh", true},
	} {
		now = data.FetchedAt.Add(tc.age)
		s.mapEvent()
		select {
		case event := <-p.out:
			var v M
			if err := json.Unmarshal(event.raw, &v); err != nil {
				t.Fatal(err)
			}
			m := object(v["map"])
			if m["status"] != tc.status || (m["data"] != nil) != tc.payload || m["offline"] != true {
				t.Fatalf("age %s: %v", tc.age, m)
			}
		default:
			t.Fatalf("missing %s event at %s", tc.status, tc.age)
		}
		s.mapEvent()
		if len(p.out) != 0 {
			t.Fatal("unchanged tick sent a map")
		}
	}
	a.closeMap()
	s.mapEvent()
	<-p.out
	now = data.FetchedAt.Add(7 * time.Hour)
	s.mapEvent()
	if len(p.out) != 0 {
		t.Fatal("hidden map age generated event")
	}
	a.openMap()
	s.mapEvent()
	if len(p.out) != 1 {
		t.Fatal("reopening did not deliver current state")
	}
	if a.wmap.attempts != 0 {
		t.Fatal("freshness notifications caused provider traffic")
	}
}
