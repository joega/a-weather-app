package notifications

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

type warningSendCall struct {
	notice WarningNotice
	ctx    context.Context
	reply  chan error
}

func warningProbe(calls chan<- warningSendCall, ignoreCancel bool) WarningSender {
	return func(ctx context.Context, notice WarningNotice) error {
		reply := make(chan error, 1)
		select {
		case calls <- warningSendCall{notice, ctx, reply}:
		case <-ctx.Done():
			return ctx.Err()
		}
		if ignoreCancel {
			return <-reply
		}
		select {
		case err := <-reply:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func warningCall(t *testing.T, calls <-chan warningSendCall) warningSendCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("warning sender did not start")
		return warningSendCall{}
	}
}

func warningTarget() M {
	return M{"name": "New York, NY", "latitude": 40.7128, "longitude": -74.006, "timezone": "America/New_York"}
}

func warningWatch(t *testing.T, state *safeio.Directory, sender WarningSender) *WarningWatcher {
	t.Helper()
	w := NewWarningWatcher(state, sender, nil)
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Error(err)
		}
	})
	return w
}

func warningEnable(t *testing.T, w *WarningWatcher, location M, now time.Time) {
	t.Helper()
	if err := w.Configure(M{"enabled": true, "quiet_enabled": false}); err != nil {
		t.Fatal(err)
	}
	w.Tick(location, "US", true, now)
}

func warningFeed(w *WarningWatcher, now time.Time, messages ...weather.AlertMessage) {
	w.Observe(WarningBatch{Location: w.key, Messages: messages, FetchedAt: now, Complete: true, CurrentKeys: warningCurrentKeys(messages, now)}, "", now)
}

func warningFinish(t *testing.T, w *WarningWatcher, call warningSendCall, location M, now time.Time, err error) {
	t.Helper()
	call.reply <- err
	select {
	case result := <-w.job.done:
		w.job.done <- result
	case <-time.After(time.Second):
		t.Fatal("warning callback did not return")
	}
	w.Tick(location, "US", true, now)
}

func TestWarningWatcherDisabledHasNoLedgerOrWorker(t *testing.T) {
	state := testState(t)
	calls := make(chan warningSendCall, 1)
	w := warningWatch(t, state, warningProbe(calls, false))
	for i := range 10 {
		now := warningTestNow.Add(time.Duration(i) * time.Minute)
		w.Tick(warningTarget(), "US", true, now)
		warningFeed(w, now, warningFixture("disabled", "Alert", warningTestNow))
		if w.Interval(now) <= 0 || w.Interval(now) < time.Minute {
			t.Fatal("disabled watcher requested rapid polling", w.Interval(now))
		}
	}
	entries, err := os.ReadDir(state.Path)
	if err != nil || len(entries) != 0 || w.ledger != nil || w.lock != nil || w.job != nil || len(calls) != 0 || w.WantsFeed() {
		t.Fatal("disabled watcher created state or work", entries, err)
	}
}

func TestWarningWatcherReservesBeforeSendAndDeduplicatesRestart(t *testing.T) {
	state := testState(t)
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, state, warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("initial", "Alert", warningTestNow)
	warningFeed(w, warningTestNow, m)
	call := warningCall(t, calls)
	doc, err := state.Read(warningLedgerFile, warningLedgerLimit)
	if err != nil || len(doc["receipts"].([]any)) != 1 || doc["receipts"].([]any)[0].(M)["status"] != "reserved" {
		t.Fatal("desktop effect preceded durable reservation", err)
	}
	if call.notice.Key != m.Identity.Key() || call.notice.Place != location["name"] || call.notice.Urgency != "normal" {
		t.Fatal("wrong notice identity or default urgency", call.notice)
	}
	warningFinish(t, w, call, location, warningTestNow, nil)
	if w.Snapshot()["delivery"] != "sent" {
		t.Fatal("successful acceptance not recorded")
	}
	_, details, ok := w.Detail(w.key, m.Identity.Key(), warningTestNow)
	if !ok || details.Instruction != m.Instruction || details.Identity != m.Identity {
		t.Fatal("original detail unavailable")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w = warningWatch(t, state, warningProbe(calls, false))
	now := warningTestNow.Add(time.Minute)
	w.Tick(location, "US", true, now)
	warningFeed(w, now, m)
	if w.job != nil || len(calls) != 0 || w.lastNotice != nil || len(w.ledger.Receipts()) != 1 || w.ledger.Receipts()[0].Status != "sent" {
		t.Fatal("restart replayed an attempted warning")
	}
}

func TestWarningWatcherFailedAndUncertainNeverRetry(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status string
	}{{"failed", errors.New("daemon rejected"), "failed"}, {"timeout", context.DeadlineExceeded, "uncertain"}, {"canceled", context.Canceled, "uncertain"}} {
		t.Run(test.name, func(t *testing.T) {
			state := testState(t)
			calls := make(chan warningSendCall, 2)
			w := warningWatch(t, state, warningProbe(calls, false))
			location := warningTarget()
			warningEnable(t, w, location, warningTestNow)
			m := warningFixture("attempt", "Alert", warningTestNow)
			warningFeed(w, warningTestNow, m)
			warningFinish(t, w, warningCall(t, calls), location, warningTestNow, test.err)
			if w.delivery != test.status || w.ledger.Receipts()[0].Status != test.status {
				t.Fatal("wrong uncertain/failed result", w.delivery)
			}
			w.Close()
			w = warningWatch(t, state, warningProbe(calls, false))
			now := warningTestNow.Add(time.Minute)
			w.Tick(location, "US", true, now)
			warningFeed(w, now, m)
			if w.job != nil || len(calls) != 0 {
				t.Fatal("failed/uncertain reservation retried after restart")
			}
		})
	}
}

func TestWarningWatcherSeverityFilteringDoesNotReserve(t *testing.T) {
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("advisory", "Alert", warningTestNow)
	m.Severity = "Moderate"
	warningFeed(w, warningTestNow, m)
	if len(w.ledger.Receipts()) != 0 || w.job != nil {
		t.Fatal("filtered warning reserved")
	}
	if err := w.Configure(M{"minimum_severity": "moderate"}); err != nil {
		t.Fatal(err)
	}
	w.Tick(location, "US", true, warningTestNow)
	warningFinish(t, w, warningCall(t, calls), location, warningTestNow, nil)
	updated := warningFixture("downgrade", "Update", warningTestNow.Add(time.Minute), m)
	updated.Severity = "Minor"
	warningFeed(w, warningTestNow.Add(time.Minute), m, updated)
	call := warningCall(t, calls)
	if call.notice.Kind != "updated" {
		t.Fatal("downgrade of prior warning was hidden or called new")
	}
	warningFinish(t, w, call, location, warningTestNow.Add(time.Minute), nil)
	cancel := warningCancel("cancel", warningTestNow.Add(2*time.Minute), updated)
	warningFeed(w, warningTestNow.Add(2*time.Minute), updated, cancel)
	call = warningCall(t, calls)
	if call.notice.Kind != "canceled" || call.notice.Urgency != "normal" {
		t.Fatal("explicit cancellation was filtered or urgent")
	}
	warningFinish(t, w, call, location, warningTestNow.Add(2*time.Minute), nil)
}

func TestWarningWatcherQuietBoundaryPauseAndUrgentOverride(t *testing.T) {
	calls := make(chan warningSendCall, 3)
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	location := warningTarget()
	location["timezone"] = "UTC"
	warningEnable(t, w, location, warningTestNow)
	if err := w.Configure(M{"quiet_enabled": true, "quiet_start": 11.0, "quiet_end": 13.0}); err != nil {
		t.Fatal(err)
	}
	m := warningFixture("quiet", "Alert", warningTestNow)
	warningFeed(w, warningTestNow, m)
	if w.mode != "quiet" || w.job != nil || len(w.ledger.Receipts()) != 0 {
		t.Fatal("quiet time delivered or reserved")
	}
	now := warningTestNow.Add(59 * time.Minute)
	warningFeed(w, now, m)
	w.Tick(location, "US", true, now)
	if w.Interval(now) != time.Minute {
		t.Fatal("quiet boundary was not scheduled", w.Interval(now))
	}
	now = now.Add(time.Minute)
	w.Tick(location, "US", true, now)
	warningFinish(t, w, warningCall(t, calls), location, now, nil)
	if err := w.Configure(M{"quiet_start": 12.0, "quiet_end": 14.0, "urgent_override": true}); err != nil {
		t.Fatal(err)
	}
	if err := w.Pause(now, false); err != nil {
		t.Fatal(err)
	}
	ur := warningFixture("urgent", "Alert", now)
	warningFeed(w, now, m, ur)
	if w.mode != "paused" || w.job != nil || !w.WantsFeed() || len(w.ledger.Receipts()) != 1 {
		t.Fatal("pause failed to suppress urgent delivery or stopped monitoring")
	}
	if err := w.Pause(now, true); err != nil {
		t.Fatal(err)
	}
	now = now.Add(warningDeliverySpacing)
	w.Tick(location, "US", true, now)
	call := warningCall(t, calls)
	if call.notice.Urgency != "critical" || call.notice.Key != ur.Identity.Key() {
		t.Fatal("explicit urgent delivery was not selected")
	}
	warningFinish(t, w, call, location, now, nil)
}

func TestWarningWatcherKeepsCanceledSlotAndRejectsOldTarget(t *testing.T) {
	calls := make(chan warningSendCall, 3)
	w := warningWatch(t, testState(t), warningProbe(calls, true))
	a, b := warningTarget(), warningTarget()
	b["latitude"], b["name"] = 41.0, "Other city"
	warningEnable(t, w, a, warningTestNow)
	m := warningFixture("old city", "Alert", warningTestNow)
	warningFeed(w, warningTestNow, m)
	old := warningCall(t, calls)
	now := warningTestNow.Add(time.Second)
	w.Tick(b, "US", true, now)
	select {
	case <-old.ctx.Done():
	default:
		t.Fatal("target change did not cancel old sender")
	}
	next := warningFixture("new city", "Alert", now)
	warningFeed(w, now, next)
	for range 5 {
		now = now.Add(time.Second)
		w.Tick(b, "US", true, now)
	}
	if len(calls) != 0 || w.job == nil || w.delivery != "uncertain" {
		t.Fatal("canceled callback released the only slot")
	}
	now = warningTestNow.Add(warningDeliverySpacing)
	warningFinish(t, w, old, b, now, nil)
	call := warningCall(t, calls)
	if call.notice.Place != "Other city" || call.notice.Key != next.Identity.Key() {
		t.Fatal("late worker confused target or revision", call.notice)
	}
	if w.ledger.Receipts()[0].Status != "uncertain" {
		t.Fatal("canceled old acceptance claimed sent")
	}
	warningFinish(t, w, call, b, now, nil)
}

func TestWarningWatcherIncompleteStaleOfflineAndSupersededCancelDelivery(t *testing.T) {
	for _, scenario := range []string{"incomplete", "stale", "offline", "unsupported", "superseded", "backward", "disable", "pause"} {
		t.Run(scenario, func(t *testing.T) {
			calls := make(chan warningSendCall, 2)
			w := warningWatch(t, testState(t), warningProbe(calls, true))
			location := warningTarget()
			warningEnable(t, w, location, warningTestNow)
			m := warningFixture("pending", "Alert", warningTestNow)
			warningFeed(w, warningTestNow, m)
			call := warningCall(t, calls)
			now := warningTestNow.Add(time.Second)
			switch scenario {
			case "incomplete":
				w.Observe(WarningBatch{Location: w.key, FetchedAt: now, Complete: false}, "history_limit", now)
			case "stale":
				w.Tick(location, "US", true, warningTestNow.Add(warningFreshness+time.Second))
			case "offline":
				w.Tick(location, "US", false, now)
			case "unsupported":
				w.Tick(location, "DE", true, now)
			case "superseded":
				u := warningFixture("revision", "Update", now, m)
				u.Instruction = "New instructions"
				warningFeed(w, now, m, u)
			case "backward":
				w.Tick(location, "US", true, warningTestNow.Add(-time.Second))
			case "disable":
				if err := w.Configure(M{"enabled": false}); err != nil {
					t.Fatal(err)
				}
			case "pause":
				if err := w.Pause(now, false); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-call.ctx.Done():
			default:
				t.Fatal("obsolete delivery was not canceled")
			}
			if w.job == nil || len(calls) != 0 {
				t.Fatal("canceled sender lost slot or replacement escaped")
			}
			call.reply <- nil
			w.Close() // Leaves an uncertain reservation for the next owner.
		})
	}
}

func TestWarningWatcherPrioritizesAndSpacesDelivery(t *testing.T) {
	calls := make(chan warningSendCall, 3)
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	severe := warningFixture("older", "Alert", warningTestNow.Add(-time.Minute))
	extreme := warningFixture("newer", "Alert", warningTestNow)
	extreme.Severity = "Extreme"
	warningFeed(w, warningTestNow, severe, extreme)
	call := warningCall(t, calls)
	if call.notice.Key != extreme.Identity.Key() {
		t.Fatal("less serious warning took the first delivery slot")
	}
	warningFinish(t, w, call, location, warningTestNow, nil)
	w.Tick(location, "US", true, warningTestNow.Add(9*time.Second))
	if w.job != nil || len(calls) != 0 || w.Interval(warningTestNow.Add(9*time.Second)) != time.Second {
		t.Fatal("delivery spacing not enforced")
	}
	now := warningTestNow.Add(warningDeliverySpacing)
	w.Tick(location, "US", true, now)
	call = warningCall(t, calls)
	if call.notice.Key != severe.Identity.Key() {
		t.Fatal("second eligible warning not delivered")
	}
	warningFinish(t, w, call, location, now, nil)
}

type warningFaultFiles struct {
	warningFiles
	name    string
	publish bool
}

func (f warningFaultFiles) Write(name string, value any, limit int) error {
	if name != f.name || f.publish {
		if err := f.warningFiles.Write(name, value, limit); err != nil {
			return err
		}
	}
	if name == f.name {
		return errors.New("fixture uncertain write")
	}
	return nil
}

func TestWarningWatcherReservationFailureNeverSends(t *testing.T) {
	for _, publish := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-rename", true: "after-rename"}[publish], func(t *testing.T) {
			state := testState(t)
			calls := make(chan warningSendCall, 2)
			w := warningWatch(t, state, warningProbe(calls, false))
			location := warningTarget()
			warningEnable(t, w, location, warningTestNow)
			if err := w.Pause(warningTestNow, false); err != nil {
				t.Fatal(err)
			}
			m := warningFixture("durability", "Alert", warningTestNow)
			warningFeed(w, warningTestNow, m)
			w.ledger.files = warningFaultFiles{w.ledger.files, warningLedgerFile, publish}
			if err := w.Pause(warningTestNow, true); err != nil {
				t.Fatal(err)
			}
			w.Tick(location, "US", true, warningTestNow)
			if w.fault == "" || w.WantsFeed() || w.job != nil || len(calls) != 0 {
				t.Fatal("uncertain reservation allowed delivery")
			}
			w.Close()
			if publish {
				w = warningWatch(t, state, warningProbe(calls, false))
				w.Tick(location, "US", true, warningTestNow)
				warningFeed(w, warningTestNow, m)
				if w.job != nil || len(calls) != 0 || w.ledger.Receipts()[0].Status != "uncertain" {
					t.Fatal("published uncertain reservation retried after reopen")
				}
			}
		})
	}
}

func TestWarningWatcherCorruptStateOwnershipAndUnsupported(t *testing.T) {
	state := testState(t)
	calls := make(chan warningSendCall, 1)
	w := warningWatch(t, state, warningProbe(calls, false))
	warningEnable(t, w, warningTarget(), warningTestNow)
	other := warningWatch(t, state, warningProbe(calls, false))
	if other.fault == "" || other.WantsFeed() || other.Configure(M{"enabled": true}) == nil {
		t.Fatal("two owners acquired warning delivery")
	}
	other.Close()
	w.Close()
	before, err := state.Read(warningSettingsFile, warningSettingsLimit)
	if err != nil {
		t.Fatal(err)
	}
	unsupported := warningWatch(t, state, nil)
	unsupported.Tick(warningTarget(), "US", true, warningTestNow)
	if unsupported.WantsFeed() || unsupported.ledger != nil || unsupported.Snapshot()["reason"] != "delivery_unsupported" {
		t.Fatal("missing adapter started monitoring")
	}
	unsupported.Close()
	bad := safeio.Clone(before)
	bad["settings"].(M)["urgent_override"] = "yes"
	if err := state.Write(warningSettingsFile, bad, warningSettingsLimit); err != nil {
		t.Fatal(err)
	}
	broken := warningWatch(t, state, warningProbe(calls, false))
	broken.Tick(warningTarget(), "US", true, warningTestNow)
	if broken.fault == "" || broken.mode != "unavailable" || broken.WantsFeed() || broken.Configure(M{"enabled": false}) == nil {
		t.Fatal("malformed state silently reset")
	}
	after, err := state.Read(warningSettingsFile, warningSettingsLimit)
	if err != nil || !reflect.DeepEqual(bad, after) {
		t.Fatal("corrupt state overwritten", err)
	}
}

func TestWarningWatcherSettingsWriteFailureStopsCurrentWorker(t *testing.T) {
	state := testState(t)
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, state, warningProbe(calls, true))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	warningFeed(w, warningTestNow, warningFixture("write failure", "Alert", warningTestNow))
	call := warningCall(t, calls)
	w.files = warningFaultFiles{w.files, warningSettingsFile, true}
	if err := w.Pause(warningTestNow, false); err == nil || w.WantsFeed() {
		t.Fatal("uncertain settings write kept monitoring")
	}
	select {
	case <-call.ctx.Done():
	default:
		t.Fatal("uncertain policy did not cancel delivery")
	}
	call.reply <- nil
	w.Close()
	w = warningWatch(t, state, warningProbe(calls, false))
	w.Tick(location, "US", true, warningTestNow)
	if !w.paused(warningTestNow) || w.ledger.Receipts()[0].Status != "uncertain" {
		t.Fatal("restart did not use authoritative pause/reservation")
	}
}

func TestWarningWatcherFutureValidityAndOwnedBodies(t *testing.T) {
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("future", "Alert", warningTestNow)
	m.Effective = warningTestNow.Add(time.Minute)
	// An active source may publish a warning before its effective time.
	w.Observe(WarningBatch{Location: w.key, Messages: []weather.AlertMessage{m}, FetchedAt: warningTestNow, Complete: true, CurrentKeys: []string{m.Identity.Key()}}, "", warningTestNow)
	if w.job != nil || len(w.ledger.Receipts()) != 0 || w.Interval(warningTestNow) != time.Minute {
		t.Fatal("future warning delivered early or missed its effective boundary")
	}
	now := warningTestNow.Add(time.Minute)
	w.Tick(location, "US", true, now)
	warningFinish(t, w, warningCall(t, calls), location, now, nil)
	updated := warningFixture("owned", "Update", now.Add(time.Minute), m)
	updated.Instruction = "Original full instruction"
	rows := []weather.AlertMessage{updated}
	warningFeed(w, now.Add(time.Minute), rows...)
	rows[0].Instruction = "Caller mutation"
	rows[0].References[0].ID = "Mutated reference"
	call := warningCall(t, calls)
	warningFinish(t, w, call, location, now.Add(time.Minute), nil)
	_, detail, ok := w.Detail(w.key, updated.Identity.Key(), now.Add(time.Minute))
	if !ok || detail.Instruction != "Original full instruction" || detail.References[0].ID != m.Identity.ID {
		t.Fatal("source body retained caller-owned slices")
	}
	detail.References[0].ID = "Detail caller mutation"
	_, again, _ := w.Detail(w.key, updated.Identity.Key(), now.Add(time.Minute))
	if again.References[0].ID != m.Identity.ID {
		t.Fatal("detail exposed watcher-owned references")
	}
}

func TestWarningWatcherWrongTargetAndInvalidObservationDoNotDeliver(t *testing.T) {
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("scope", "Alert", warningTestNow)
	w.Observe(WarningBatch{Location: warningTestLocation, Messages: []weather.AlertMessage{m}, FetchedAt: warningTestNow, Complete: true, CurrentKeys: []string{m.Identity.Key()}}, "", warningTestNow)
	if !w.fetched.IsZero() || len(w.ledger.Receipts()) != 0 {
		t.Fatal("wrong-target observation affected current owner")
	}
	m.Severity = "INVALID"
	warningFeed(w, warningTestNow, m)
	if w.mode != "unavailable" || w.reason != "invalid_observation" || w.job != nil || len(calls) != 0 {
		t.Fatal("invalid observation silently accepted", w.Snapshot())
	}
	m.Severity = "Severe"
	warningFeed(w, warningTestNow, m)
	warningFinish(t, w, warningCall(t, calls), location, warningTestNow, nil)
}

func TestWarningWatcherDeadlineKeepsSlotUntilCallbackReturns(t *testing.T) {
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, testState(t), warningProbe(calls, true))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("timeout slot", "Alert", warningTestNow)
	warningFeed(w, warningTestNow, m)
	call := warningCall(t, calls)
	select {
	case <-call.ctx.Done():
	case <-time.After(4 * time.Second):
		t.Fatal("sender deadline missing")
	}
	if call.ctx.Err() != context.DeadlineExceeded {
		t.Fatal("fixture did not reach actual deadline")
	}
	now := warningTestNow.Add(warningDeliverySpacing)
	w.Tick(location, "US", true, now)
	other := warningFixture("waiting after timeout", "Alert", now)
	warningFeed(w, now, m, other)
	if w.job == nil || len(calls) != 0 || w.delivery != "uncertain" || w.Interval(now) < time.Minute {
		t.Fatal("timed-out callback lost slot or caused tight polling")
	}
	warningFinish(t, w, call, location, now, nil)
	call = warningCall(t, calls)
	if call.notice.Key != other.Identity.Key() || w.ledger.Receipts()[0].Status != "uncertain" {
		t.Fatal("late success was retried or called confirmed")
	}
	warningFinish(t, w, call, location, now, nil)
}

func TestWarningWatcherClosePendingRestartsUncertain(t *testing.T) {
	state := testState(t)
	calls := make(chan warningSendCall, 2)
	w := warningWatch(t, state, warningProbe(calls, false))
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	m := warningFixture("closing", "Alert", warningTestNow)
	warningFeed(w, warningTestNow, m)
	warningCall(t, calls)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	w = warningWatch(t, state, warningProbe(calls, false))
	w.Tick(location, "US", true, warningTestNow)
	warningFeed(w, warningTestNow, m)
	if w.job != nil || len(calls) != 0 || w.ledger.Receipts()[0].Status != "uncertain" {
		t.Fatal("pending close replayed after restart")
	}
}

func TestWarningWatcherCloseBoundsIgnoredCancellation(t *testing.T) {
	calls := make(chan warningSendCall, 1)
	w := NewWarningWatcher(testState(t), warningProbe(calls, true), nil)
	warningEnable(t, w, warningTarget(), warningTestNow)
	warningFeed(w, warningTestNow, warningFixture("uncooperative sender", "Alert", warningTestNow))
	call := warningCall(t, calls)
	started := time.Now()
	err := w.Close()
	call.reply <- nil // Allow the isolated fixture callback to finish after timeout.
	if err == nil || time.Since(started) > 5*time.Second || w.Close() != err || w.mode != "off" || w.delivery != "uncertain" || w.reason != "cleanup_unconfirmed" {
		t.Fatal("cleanup was unbounded or falsely confirmed", err, w.Snapshot())
	}
}

func TestWarningPauseCannotPersistOutOfRangeTimestamp(t *testing.T) {
	w := warningWatch(t, testState(t), func(context.Context, WarningNotice) error { return nil })
	warningEnable(t, w, warningTarget(), warningTestNow)
	before := safeio.Clone(w.document)
	if w.Pause(time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), false) == nil || !reflect.DeepEqual(before, w.document) {
		t.Fatal("pause persisted an out-of-range timestamp")
	}
}

func BenchmarkWarningWatcherIdle(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		b.Run(map[bool]string{false: "disabled", true: "enabled-paused"}[enabled], func(b *testing.B) {
			w := &WarningWatcher{document: defaultWarningSettings(), mode: "off", delivery: "none", dirty: true}
			location := warningTarget()
			if enabled {
				w.document["settings"].(M)["enabled"] = true
				w.document["paused_until"] = float64(warningTestNow.Unix() + 3600)
				w.send = func(context.Context, WarningNotice) error { b.Error("paused benchmark sent"); return nil }
				w.ledger, _ = newWarningMemory(b)
			}
			w.Tick(location, "US", true, warningTestNow)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w.Tick(location, "US", true, warningTestNow)
			}
		})
	}
}

func BenchmarkWarningWatcherObserve(b *testing.B) {
	for _, count := range []int{1, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			w := &WarningWatcher{document: defaultWarningSettings(), mode: "off", delivery: "none", dirty: true}
			w.document["settings"].(M)["enabled"] = true
			w.document["paused_until"] = float64(warningTestNow.Unix() + 3600)
			w.send = func(context.Context, WarningNotice) error { b.Error("paused benchmark sent"); return nil }
			w.ledger, _ = newWarningMemory(b)
			w.Tick(warningTarget(), "US", true, warningTestNow)
			messages := make([]weather.AlertMessage, count)
			for i := range messages {
				messages[i] = warningFixture(fmt.Sprint(i), "Alert", warningTestNow)
				if count > 1 {
					messages[i].Description = strings.Repeat("d", 4096)
					messages[i].Instruction = strings.Repeat("i", 2048)
				}
			}
			batch := WarningBatch{Location: w.key, Messages: messages, FetchedAt: warningTestNow, Complete: true, CurrentKeys: warningCurrentKeys(messages, warningTestNow)}
			w.Observe(batch, "", warningTestNow)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w.Observe(batch, "", warningTestNow)
			}
			if w.observationError != "" || w.fault != "" || len(w.bodies) != count {
				b.Fatal("invalid observation benchmark", w.Snapshot())
			}
		})
	}
}

func TestWarningDetailsStayScopedAndBounded(t *testing.T) {
	w := &WarningWatcher{}
	first := warningFixture("shared-source", "Alert", warningTestNow)
	for i := range warningDetailLimit + 1 {
		key := warningHash(fmt.Sprint(i))
		m := warningFixture(fmt.Sprint(i), "Alert", warningTestNow)
		if i == 0 || i == 1 {
			m = first // One CAP warning can cover different monitored places.
		}
		n := WarningNotice{Location: key, Key: m.Identity.Key(), Place: fmt.Sprintf("Place %d", i)}
		w.rememberDetail(n, m, warningTestNow)
	}
	if len(w.details) != warningDetailLimit || w.detailBytes > warningDetailBytes {
		t.Fatal("detail count/bytes unbounded")
	}
	if _, _, found := w.Detail(warningHash("0"), first.Identity.Key(), warningTestNow); found {
		t.Fatal("evicted detail routed to another place with the same CAP identity")
	}
	n, m, found := w.Detail(warningHash("1"), first.Identity.Key(), warningTestNow)
	if !found || n.Place != "Place 1" || m.Instruction != first.Instruction {
		t.Fatal("retained detail lost place or instructions")
	}
	if _, _, found := w.Detail(n.Location, n.Key, warningTestNow.Add(warningDetailRetention)); found {
		t.Fatal("expired detail returned as retained")
	}
	// Each valid typed message has bounded text/reference fields; a large page
	// can still fill the independent detail byte cap before the count limit.
	for i := range 12 {
		m := warningFixture(fmt.Sprintf("large-%d", i), "Alert", warningTestNow)
		m.Description, m.Instruction = strings.Repeat("雨", 32000), strings.Repeat("雪", 16000)
		m.Headline, m.Area = strings.Repeat("雷", 2048), strings.Repeat("風", 8192)
		w.rememberDetail(WarningNotice{Location: warningTestLocation, Key: m.Identity.Key()}, m, warningTestNow)
	}
	if w.detailBytes > warningDetailBytes || len(w.details) >= warningDetailLimit {
		t.Fatal("detail text budget failed to evict", len(w.details), w.detailBytes)
	}
	latest := w.details[len(w.details)-1]
	_, m, found = w.Detail(latest.notice.Location, latest.notice.Key, warningTestNow)
	if !found || len([]rune(m.Instruction)) != 16000 {
		t.Fatal("original instructions were truncated to fit the cache")
	}
	w.pruneDetails(warningTestNow.Add(warningDetailRetention))
	if w.details != nil || w.detailBytes != 0 {
		t.Fatal("expired details retained source bodies")
	}
}
