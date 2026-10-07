package app

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
)

func TestPrivateSnapshotEncodingAndCachedFallback(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	public := a.Snapshot()
	delete(public, "snapshot_revision")
	for _, locked := range []bool{false, true} {
		if locked {
			a.mu.Lock()
		}
		comparison, full, err := a.snapshotEncoded()
		if locked {
			a.mu.Unlock()
		}
		if err != nil {
			t.Fatal(err)
		}
		var c, f M
		if err = json.Unmarshal(comparison, &c); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(full, &f); err != nil {
			t.Fatal(err)
		}
		if _, ok := f["snapshot_revision"].(float64); !ok {
			t.Fatal("missing revision")
		}
		delete(f, "snapshot_revision")
		if !reflect.DeepEqual(c, f) {
			t.Fatal("comparison content differs from wire content")
		}
		// Decode the public copy too, to compare numbers through the same JSON representation.
		raw, _ := json.Marshal(public)
		var decoded M
		_ = json.Unmarshal(raw, &decoded)
		// Age is clock-derived; stable comparison is covered by frozen-clock fixtures.
		delete(decoded, "source")
		delete(c, "source")
		if !reflect.DeepEqual(decoded, c) {
			t.Fatal("encoded snapshot changed public API content")
		}
	}
}

func TestChangedEventEncodingLimitAndDeduplication(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	now := a.options.Now()
	a.options.Now = func() time.Time { return now }
	p := &peer{out: make(chan outbound, 4), closed: make(chan struct{}), subscribed: true, presentationActive: true}
	s := &Server{app: a, peers: map[*peer]bool{p: true}}
	s.snapshotTo(true)
	event := <-p.out
	if len(event.raw) > ipc.ResponseLimit+1 || event.raw[len(event.raw)-1] != '\n' || !json.Valid(event.raw) {
		t.Fatal("invalid framed event")
	}
	s.snapshotTo(true)
	if len(p.out) != 0 {
		t.Fatal("unchanged content was sent")
	}
	a.launcherStatus = strings.Repeat("x", ipc.ResponseLimit)
	previous := s.last
	s.snapshotTo(true)
	if len(p.out) != 0 || !bytes.Equal(previous, s.last) {
		t.Fatal("oversized event escaped or suppressed future retry")
	}
}
