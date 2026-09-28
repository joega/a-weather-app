package notifications

import (
	"context"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

var testNow = time.Date(2026, 9, 27, 16, 0, 0, 0, time.UTC)

func testForecast(now time.Time) (M, M) {
	location := M{"name": "New York, NY", "latitude": 40.7128, "longitude": -74.0060, "timezone": "America/New_York"}
	return M{"location": location, "fetched_at": now.Format(time.RFC3339), "hourly": []any{M{"time": now.Add(time.Hour).Format(time.RFC3339), "precipitation_probability": float64(1)}}}, location
}
func testState(t *testing.T) *safeio.Directory {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	d, err := safeio.OpenDir(path, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func testWatcher(t *testing.T, state *safeio.Directory, sender Sender) *Watcher {
	t.Helper()
	w := New(state, sender)
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}
func enable(t *testing.T, w *Watcher) {
	t.Helper()
	if err := w.Configure(M{"enabled": true, "quiet_enabled": false}); err != nil {
		t.Fatal(err)
	}
}
func finish(t *testing.T, w *Watcher, forecast, location M) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for w.done != nil && time.Now().Before(until) {
		w.Tick(forecast, location, testNow, true)
		time.Sleep(time.Millisecond)
	}
	if w.done != nil {
		t.Fatal("sender did not complete")
	}
}

func TestDisabledCreatesNothing(t *testing.T) {
	d := testState(t)
	w := testWatcher(t, d, func(context.Context, string, string) error { t.Error("disabled sender invoked"); return nil })
	f, l := testForecast(testNow)
	w.Tick(f, l, testNow, true)
	entries, err := os.ReadDir(d.Path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("disabled watcher wrote files: %v %v", entries, err)
	}
	if w.mode != "off" {
		t.Fatalf("mode %s", w.mode)
	}
}
func TestRepeatAndRestartDeduplicateUncertainDelivery(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "uncertain"}[uncertain], func(t *testing.T) {
			d := testState(t)
			messages := make(chan string, 4)
			sender := func(_ context.Context, _ string, body string) error {
				messages <- body
				if uncertain {
					return errors.New("delivery uncertain")
				}
				return nil
			}
			w := testWatcher(t, d, sender)
			enable(t, w)
			f, l := testForecast(testNow)
			w.Tick(f, l, testNow, true)
			finish(t, w, f, l)
			for range 5 {
				w.Tick(f, l, testNow, true)
			}
			if len(messages) != 1 {
				t.Fatalf("messages %d", len(messages))
			}
			v, err := d.Read("notifications.json", limit)
			if err != nil {
				t.Fatal(err)
			}
			rows := v["events"].([]any)
			if len(rows) != 1 || len(rows[0].(M)) != 5 {
				t.Fatalf("private reservation history: %v", rows)
			}
			for _, forbidden := range []string{"title", "body", "name"} {
				if _, ok := rows[0].(M)[forbidden]; ok {
					t.Fatalf("stored %s", forbidden)
				}
			}
			if uncertain && w.delivery != "failed" {
				t.Fatalf("delivery %s", w.delivery)
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := testWatcher(t, d, sender)
			restarted.Tick(f, l, testNow, true)
			if len(messages) != 1 || restarted.done != nil {
				t.Fatal("reserved event delivered after restart")
			}
		})
	}
}
func TestSuppressStaleQuietPausedAndMismatchedLocation(t *testing.T) {
	for _, kind := range []string{"stale", "future", "quiet", "paused", "mismatch", "not-ready", "invalid-zone"} {
		t.Run(kind, func(t *testing.T) {
			w := testWatcher(t, testState(t), func(context.Context, string, string) error { t.Error("suppressed sender invoked"); return nil })
			enable(t, w)
			f, l := testForecast(testNow)
			mode := "waiting"
			dataOK := true
			switch kind {
			case "stale":
				f["fetched_at"] = testNow.Add(-2 * time.Hour).Format(time.RFC3339)
			case "future":
				f["fetched_at"] = testNow.Add(time.Minute).Format(time.RFC3339)
			case "quiet":
				if err := w.Configure(M{"quiet_enabled": true, "quiet_start": float64(11), "quiet_end": float64(13)}); err != nil {
					t.Fatal(err)
				}
				mode = "quiet"
			case "paused":
				if err := w.Snooze(testNow, false); err != nil {
					t.Fatal(err)
				}
				mode = "paused"
			case "mismatch":
				f["location"] = M{"timezone": "UTC"}
			case "not-ready":
				dataOK = false
			case "invalid-zone":
				l["timezone"] = "invalid/zone"
			}
			w.Tick(f, l, testNow, dataOK)
			if w.mode != mode || w.done != nil || len(w.document["events"].([]any)) != 0 {
				t.Fatalf("mode %s, expected %s, events %v", w.mode, mode, w.document["events"])
			}
		})
	}
}
func TestPendingDeliveryCancelledByPolicy(t *testing.T) {
	for _, policy := range []string{"disable", "quiet-setting", "snooze", "stale", "quiet-tick", "close"} {
		t.Run(policy, func(t *testing.T) {
			started, cancelled := make(chan struct{}), make(chan struct{})
			w := testWatcher(t, testState(t), func(ctx context.Context, _ string, _ string) error {
				close(started)
				<-ctx.Done()
				close(cancelled)
				return ctx.Err()
			})
			enable(t, w)
			f, l := testForecast(testNow)
			w.Tick(f, l, testNow, true)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("sender not started")
			}
			switch policy {
			case "disable":
				if err := w.Configure(M{"enabled": false}); err != nil {
					t.Fatal(err)
				}
			case "quiet-setting":
				if err := w.Configure(M{"quiet_start": float64(11)}); err != nil {
					t.Fatal(err)
				}
			case "snooze":
				if err := w.Snooze(testNow, false); err != nil {
					t.Fatal(err)
				}
			case "stale":
				w.Tick(f, l, testNow, false)
			case "quiet-tick":
				w.document["settings"].(M)["quiet_enabled"] = true
				quietNow := time.Date(2026, 9, 28, 3, 0, 0, 0, time.UTC)
				f["fetched_at"] = quietNow.Format(time.RFC3339)
				w.Tick(f, l, quietNow, true)
			case "close":
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("policy did not cancel pending helper")
			}
		})
	}
}
func TestLockContentionAndOffInstanceMergesHistory(t *testing.T) {
	d := testState(t)
	messages := make(chan struct{}, 4)
	sender := func(context.Context, string, string) error { messages <- struct{}{}; return nil }
	first := testWatcher(t, d, sender)
	second := testWatcher(t, d, sender)
	enable(t, first)
	if err := second.Configure(M{"enabled": true}); err == nil {
		t.Fatal("second owner acquired lock")
	}
	contender := testWatcher(t, d, sender)
	f, l := testForecast(testNow)
	contender.Tick(f, l, testNow, true)
	if contender.mode != "unavailable" || contender.lock != nil {
		t.Fatal("contending startup did not fail closed")
	}
	first.Tick(f, l, testNow, true)
	finish(t, first, f, l)
	if err := first.Configure(M{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	enable(t, second)
	second.Tick(f, l, testNow, true)
	if len(messages) != 1 || len(second.document["events"].([]any)) != 1 {
		t.Fatal("off instance lost authoritative reservation")
	}
}
func TestCandidatePrecedingHourAndPlainText(t *testing.T) {
	f, l := testForecast(testNow)
	l["name"] = "--icon=evil <b>&test</b>\x00\n\u202e$(touch /tmp/not-executed)"
	event := Candidate(f, l, testNow, 50)
	if event == nil || event["start"] != float64(testNow.Unix()) || event["end"] != float64(testNow.Add(time.Hour).Unix()) {
		t.Fatalf("preceding-hour candidate %v", event)
	}
	body := event["body"].(string)
	if !strings.Contains(body, "Sun 12 PM EDT–1 PM EDT") {
		t.Fatalf("honest timezone label: %s", body)
	}
	for _, r := range "<>&\x00\n\u202e" {
		if strings.ContainsRune(body, r) {
			t.Fatalf("markup/control %q", r)
		}
	}
	if len([]rune(Text(strings.Repeat("☁", 400), 320))) != 320 {
		t.Fatal("text not rune bounded")
	}
	renamed := safeio.Clone(l)
	renamed["name"] = "renamed"
	f["location"] = renamed
	if Candidate(f, renamed, testNow, 50)["location"] != event["location"] {
		t.Fatal("rename changed physical-location reservation")
	}
}
func TestSettingsAndHistoryRejectInvalidNumbers(t *testing.T) {
	for _, patch := range []M{{"enabled": float64(1)}, {"quiet_start": true}, {"quiet_start": math.NaN()}, {"probability": true}, {"probability": float64(65)}, {"quiet_start": float64(7)}, {"unknown": true}, {}} {
		if _, err := Patch(Defaults(), patch); err == nil {
			t.Fatalf("accepted invalid patch %v", patch)
		}
	}
	for _, invalid := range []any{true, math.NaN(), math.Inf(1), float64(-1)} {
		v := DefaultDocument()
		v["snoozed_until"] = invalid
		if Validate(v) == nil {
			t.Fatalf("accepted timestamp %v", invalid)
		}
	}
}

func TestSenderHasDeadlineAndPersistedReservation(t *testing.T) {
	d := testState(t)
	checked := make(chan error, 1)
	w := testWatcher(t, d, func(ctx context.Context, _, _ string) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second || time.Until(deadline) < 2*time.Second {
			checked <- errors.New("sender lacks fixed three-second deadline")
			return nil
		}
		v, err := d.Read("notifications.json", limit)
		if err == nil && (v == nil || len(v["events"].([]any)) != 1) {
			err = errors.New("sender invoked before durable reservation")
		}
		checked <- err
		return nil
	})
	enable(t, w)
	f, l := testForecast(testNow)
	w.Tick(f, l, testNow, true)
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sender not called")
	}
	finish(t, w, f, l)
}

func TestRevisionExtendsReservationAndCapacityFailsClosed(t *testing.T) {
	d := testState(t)
	messages := make(chan struct{}, 4)
	w := testWatcher(t, d, func(context.Context, string, string) error { messages <- struct{}{}; return nil })
	enable(t, w)
	f, l := testForecast(testNow)
	w.Tick(f, l, testNow, true)
	finish(t, w, f, l)
	f["hourly"] = append(f["hourly"].([]any), M{"time": testNow.Add(4 * time.Hour).Format(time.RFC3339), "precipitation_probability": float64(1)})
	w.Tick(f, l, testNow, true)
	v, err := d.Read("notifications.json", limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || v["events"].([]any)[0].(M)["end"] != float64(testNow.Add(4*time.Hour).Unix()) {
		t.Fatal("revision duplicated or failed to extend reservation")
	}
	rows := []any{}
	for range 64 {
		rows = append(rows, M{"location": strings.Repeat("f", 64), "start": float64(testNow.Unix() - 86400), "end": float64(testNow.Unix() - 82800), "reserved": float64(testNow.Unix() - 86400), "expires": float64(testNow.Unix() - 86400 + retention)})
	}
	w.document["events"] = rows
	w.Tick(f, l, testNow, true)
	if w.mode != "unavailable" || len(messages) != 1 || len(w.document["events"].([]any)) != 64 {
		t.Fatal("unexpired reservations evicted to send notification")
	}
}
