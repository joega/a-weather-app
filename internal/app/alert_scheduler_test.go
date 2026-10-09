package app

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/weather"
)

type alertProbeReply struct {
	page weather.AlertMessagePage
	err  error
}
type alertProbeCall struct {
	query weather.AlertMessageQuery
	now   time.Time
	reply chan alertProbeReply
}

func alertProbeFetcher(calls chan alertProbeCall) alertPageFetcher {
	return func(ctx context.Context, q weather.AlertMessageQuery, now time.Time) (weather.AlertMessagePage, error) {
		call := alertProbeCall{q, now, make(chan alertProbeReply, 1)}
		select {
		case calls <- call:
		case <-ctx.Done():
			return weather.AlertMessagePage{}, ctx.Err()
		}
		select {
		case r := <-call.reply:
			return r.page, r.err
		case <-ctx.Done():
			return weather.AlertMessagePage{}, ctx.Err()
		}
	}
}
func alertTestMessage(id string, now time.Time, prior ...weather.AlertMessage) weather.AlertMessage {
	kind := "Alert"
	refs := []weather.AlertReference{}
	for _, m := range prior {
		refs = append(refs, m.Identity)
		kind = "Update"
	}
	return weather.AlertMessage{Identity: weather.AlertReference{ID: id, Sender: "fixture@noaa.gov", Sent: now}, Type: kind, References: refs, Issuer: "NWS Fixture", Event: "Flood Warning", Severity: "Severe", Urgency: "Immediate", Certainty: "Observed", Headline: "Flood Warning", Description: "Water is rising.", Instruction: "Move to higher ground.", Effective: now, Expires: savedRuntimeNow.Add(24 * time.Hour)}
}
func alertTestDemand(t testing.TB, index int, monitor bool) alertDemand {
	t.Helper()
	loc := object(savedFixture(index)["location"])
	key, err := notifications.WarningLocationKey(loc)
	if err != nil {
		t.Fatal(err)
	}
	return alertDemand{key, loc, monitor}
}
func alertTestScheduler(t *testing.T, demands ...alertDemand) (*alertScheduler, chan alertProbeCall) {
	t.Helper()
	calls := make(chan alertProbeCall, 4)
	s := newAlertScheduler(alertProbeFetcher(calls), nil)
	if err := s.setDemands(demands, savedRuntimeNow); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s, calls
}
func alertAwaitCall(t *testing.T, calls chan alertProbeCall) alertProbeCall {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("alert request not admitted")
		return alertProbeCall{}
	}
}
func alertComplete(t *testing.T, s *alertScheduler, call alertProbeCall, now time.Time, page weather.AlertMessagePage, err error) []alertObservation {
	t.Helper()
	call.reply <- alertProbeReply{page, err}
	select {
	case result := <-s.done:
		s.done <- result
	case <-time.After(time.Second):
		t.Fatal("alert callback did not return")
	}
	return s.tick(now)
}
func alertStep(t *testing.T, s *alertScheduler, calls chan alertProbeCall, now time.Time, cursor string, messages ...weather.AlertMessage) (alertProbeCall, []alertObservation) {
	t.Helper()
	s.tick(now)
	call := alertAwaitCall(t, calls)
	return call, alertComplete(t, s, call, now, weather.AlertMessagePage{Messages: messages, FetchedAt: call.now, NextCursor: cursor, Complete: cursor == ""}, nil)
}

func TestAlertSchedulerSharesSpacingAndHistory(t *testing.T) {
	primary, viewed := alertTestDemand(t, 0, true), alertTestDemand(t, 1, false)
	s, calls := alertTestScheduler(t, primary, viewed)
	a := alertTestMessage("a", savedRuntimeNow)
	u := alertTestMessage("u", savedRuntimeNow.Add(time.Minute), a)
	u.Instruction = "Use a different evacuation route."
	call, events := alertStep(t, s, calls, savedRuntimeNow, "", a)
	if !call.query.Active || call.query.Location["latitude"] != primary.location["latitude"] || len(events) != 1 || events[0].batch != nil {
		t.Fatal("initial active page was not display-only", call.query, events)
	}
	s.tick(savedRuntimeNow.Add(alertRequestSpacing - time.Nanosecond))
	if len(calls) != 0 || s.job != nil {
		t.Fatal("provider throttle bypassed")
	}
	call, events = alertStep(t, s, calls, savedRuntimeNow.Add(30*time.Second), "")
	if !call.query.Active || call.query.Location["latitude"] != viewed.location["latitude"] || events[0].batch != nil {
		t.Fatal("viewed demand starved", call.query)
	}
	call, events = alertStep(t, s, calls, savedRuntimeNow.Add(time.Minute), "", a, u)
	if call.query.Active || !call.query.Since.Equal(savedRuntimeNow.Add(-7*24*time.Hour+alertCycleBudget)) || len(events) != 0 {
		t.Fatal("history query or assembly wrong", call.query, events)
	}
	call, events = alertStep(t, s, calls, savedRuntimeNow.Add(90*time.Second), "", u)
	if !call.query.Active || len(events) != 1 || events[0].batch == nil || !events[0].batch.Complete {
		t.Fatal("final active page did not complete reconciliation", events)
	}
	batch := events[0].batch
	if len(batch.Messages) != 2 || !reflect.DeepEqual(batch.CurrentKeys, []string{u.Identity.Key()}) || !batch.FetchedAt.Equal(call.now) {
		t.Fatal("current identities mixed with history", batch)
	}
	ledger, err := notifications.OpenWarningLedger(testState(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	if err := ledger.Reconcile(*batch, call.now); err != nil {
		t.Fatal(err)
	}
	decisions := ledger.Candidates(primary.key, call.now)
	if len(decisions) != 1 || decisions[0].Key != u.Identity.Key() {
		t.Fatal("superseded history authorized delivery", decisions)
	}
	// The watermark remains the prior cycle's start, including overlap. It
	// must not skip changes between historical pagination and the active read.
	call, _ = alertStep(t, s, calls, savedRuntimeNow.Add(210*time.Second), "")
	if call.query.Active || !call.query.Since.Equal(savedRuntimeNow.Add(-2*time.Minute)) {
		t.Fatal("history overlap gap", call.query)
	}
}

func TestAlertSchedulerBoundsPagesAndRejectsCursorCycles(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		t.Run(fmt.Sprint(cycle), func(t *testing.T) {
			d := alertTestDemand(t, 0, true)
			s, calls := alertTestScheduler(t, d)
			alertStep(t, s, calls, savedRuntimeNow, "")
			alertStep(t, s, calls, savedRuntimeNow.Add(30*time.Second), "a")
			call, _ := alertStep(t, s, calls, savedRuntimeNow.Add(time.Minute), "b")
			if call.query.Cursor != "a" {
				t.Fatal("cursor lost")
			}
			cursor := "c"
			if cycle {
				cursor = "a"
			}
			_, events := alertStep(t, s, calls, savedRuntimeNow.Add(90*time.Second), cursor)
			if cycle {
				if len(events) != 1 || events[0].err != "repeated_cursor" || events[0].batch.Complete || events[0].activeFailed {
					t.Fatal("cursor cycle looked complete", events)
				}
				return
			}
			call, events = alertStep(t, s, calls, savedRuntimeNow.Add(2*time.Minute), "")
			if !call.query.Active || len(events) != 1 || events[0].err != "history_limit" || events[0].batch.Complete || events[0].active["status"] != "available" {
				t.Fatal("page budget did not preserve honest partial history", events)
			}
			if s.entries[d.key].cycle != nil {
				t.Fatal("large assembly retained after publication")
			}
		})
	}
}

func TestAlertSchedulerCancellationRetainsSingleWorker(t *testing.T) {
	var running, peak atomic.Int32
	calls := make(chan alertProbeCall, 4)
	s := newAlertScheduler(func(_ context.Context, q weather.AlertMessageQuery, now time.Time) (weather.AlertMessagePage, error) {
		n := running.Add(1)
		defer running.Add(-1)
		if n > peak.Load() {
			peak.Store(n)
		}
		call := alertProbeCall{q, now, make(chan alertProbeReply, 1)}
		calls <- call
		r := <-call.reply // Deliberately slow cancellation to test the hard slot.
		return r.page, r.err
	}, nil)
	defer s.close()
	a, b := alertTestDemand(t, 0, true), alertTestDemand(t, 1, true)
	if err := s.setDemands([]alertDemand{a}, savedRuntimeNow); err != nil {
		t.Fatal(err)
	}
	s.tick(savedRuntimeNow)
	old := alertAwaitCall(t, calls)
	if err := s.setDemands([]alertDemand{b}, savedRuntimeNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	s.tick(savedRuntimeNow.Add(time.Minute))
	if err := s.setDemands([]alertDemand{a}, savedRuntimeNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.tick(savedRuntimeNow.Add(time.Minute))
	if len(calls) != 0 || running.Load() != 1 {
		t.Fatal("canceled callback released capacity too early")
	}
	events := alertComplete(t, s, old, savedRuntimeNow.Add(time.Minute), weather.AlertMessagePage{FetchedAt: old.now, Complete: true, Messages: []weather.AlertMessage{alertTestMessage("obsolete", old.now)}}, nil)
	if len(events) != 0 {
		t.Fatal("obsolete A visit published", events)
	}
	fresh := alertAwaitCall(t, calls)
	if fresh.query.Location["latitude"] != a.location["latitude"] || peak.Load() != 1 {
		t.Fatal("latest demand or concurrency cap lost")
	}
	alertComplete(t, s, fresh, savedRuntimeNow.Add(time.Minute), weather.AlertMessagePage{FetchedAt: fresh.now, Complete: true}, nil)
}

func TestAlertSchedulerFailureBackoffAndHistoryHonesty(t *testing.T) {
	d := alertTestDemand(t, 0, true)
	s, calls := alertTestScheduler(t, d)
	alertStep(t, s, calls, savedRuntimeNow, "")
	s.tick(savedRuntimeNow.Add(30 * time.Second))
	call := alertAwaitCall(t, calls)
	events := alertComplete(t, s, call, call.now, weather.AlertMessagePage{}, errors.New("history unavailable"))
	if len(events) != 1 || events[0].activeFailed || events[0].batch == nil || events[0].batch.Complete {
		t.Fatal("history failure invalidated current feed or claimed completeness", events)
	}
	if !s.entries[d.key].next.Equal(call.now.Add(time.Minute)) {
		t.Fatal("first retry not bounded")
	}
	s.tick(call.now.Add(time.Minute - time.Nanosecond))
	if len(calls) != 0 {
		t.Fatal("backoff bypassed")
	}
	s.tick(call.now.Add(time.Minute))
	call = alertAwaitCall(t, calls)
	events = alertComplete(t, s, call, call.now, weather.AlertMessagePage{}, errors.New("still unavailable"))
	if len(events) != 1 || !s.entries[d.key].next.Equal(call.now.Add(2*time.Minute)) {
		t.Fatal("backoff did not increase", events)
	}
	if s.entries[d.key].cycle != nil {
		t.Fatal("failed assembly retained bodies")
	}
}

func TestAlertSchedulerSuspendClockAndDisabled(t *testing.T) {
	d := alertTestDemand(t, 0, true)
	s, calls := alertTestScheduler(t, d)
	alertStep(t, s, calls, savedRuntimeNow, "")
	alertStep(t, s, calls, savedRuntimeNow.Add(30*time.Second), "next")
	later := savedRuntimeNow.Add(30 * time.Minute)
	call, events := alertStep(t, s, calls, later, "")
	if !call.query.Active || len(events) != 1 || events[0].batch != nil {
		t.Fatal("suspend reused old partial history", events)
	}
	back := later.Add(-time.Hour)
	s.tick(back)
	if s.job != nil || len(calls) != 0 {
		t.Fatal("backward clock bypassed provider throttle")
	}
	call, events = alertStep(t, s, calls, back.Add(30*time.Second), "")
	if !call.query.Active || events[0].batch != nil {
		t.Fatal("future-dated feed reused after clock change")
	}
	if err := s.setDemands(nil, back.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.tick(back.Add(time.Hour))
	if len(calls) != 0 || s.job != nil || len(s.entries) != 0 || s.interval(back.Add(time.Hour)) != time.Hour {
		t.Fatal("disabled scheduler kept work")
	}
}

func TestAlertSchedulerRejectsMalformedPagesAndConflicts(t *testing.T) {
	for name, mutate := range map[string]func(*weather.AlertMessagePage){
		"wrong fetch time":      func(p *weather.AlertMessagePage) { p.FetchedAt = p.FetchedAt.Add(time.Second) },
		"paginated active feed": func(p *weather.AlertMessagePage) { p.Complete = false; p.NextCursor = "x" },
		"missing next cursor":   func(p *weather.AlertMessagePage) { p.Complete = false },
		"invalid cursor":        func(p *weather.AlertMessagePage) { p.NextCursor = "\n"; p.Complete = false },
		"too many messages":     func(p *weather.AlertMessagePage) { p.Messages = make([]weather.AlertMessage, 257) },
		"invalid message":       func(p *weather.AlertMessagePage) { p.Messages[0].Identity.ID = "" },
		"future issue":          func(p *weather.AlertMessagePage) { p.Messages[0].Identity.Sent = p.FetchedAt.Add(time.Second) },
		"oversized envelope":    func(p *weather.AlertMessagePage) { p.Messages[0].Description = strings.Repeat("x", 2<<20) },
	} {
		t.Run(name, func(t *testing.T) {
			s, calls := alertTestScheduler(t, alertTestDemand(t, 0, true))
			s.tick(savedRuntimeNow)
			call := alertAwaitCall(t, calls)
			page := weather.AlertMessagePage{FetchedAt: call.now, Complete: true, Messages: []weather.AlertMessage{alertTestMessage("a", call.now)}}
			mutate(&page)
			events := alertComplete(t, s, call, call.now, page, nil)
			if len(events) != 1 || events[0].err != "invalid_feed" || !events[0].activeFailed || events[0].active != nil || events[0].batch.Complete {
				t.Fatal("malformed response looked current", events)
			}
		})
	}
	s, calls := alertTestScheduler(t, alertTestDemand(t, 0, true))
	a := alertTestMessage("a", savedRuntimeNow)
	alertStep(t, s, calls, savedRuntimeNow, "")
	alertStep(t, s, calls, savedRuntimeNow.Add(30*time.Second), "a", a)
	a.Instruction = "Conflicting same-identity instruction."
	_, events := alertStep(t, s, calls, savedRuntimeNow.Add(time.Minute), "", a)
	if len(events) != 1 || events[0].err != "conflicting_history" || events[0].batch.Complete {
		t.Fatal("cross-page identity conflict accepted", events)
	}
}

func TestAlertSchedulerDemandLimitsAndOrdinaryCadence(t *testing.T) {
	d := alertTestDemand(t, 0, false)
	s, calls := alertTestScheduler(t, d)
	for _, demands := range [][]alertDemand{
		{d, d, d},
		{alertTestDemand(t, 0, true), alertTestDemand(t, 1, true)},
		{{key: "wrong", location: d.location}},
		{{key: d.key, location: M{"name": "broken"}}},
	} {
		if err := s.setDemands(demands, savedRuntimeNow); err == nil {
			t.Fatal("invalid demand accepted")
		}
	}
	if len(s.entries) != 1 {
		t.Fatal("invalid demand mutated ownership")
	}
	_, events := alertStep(t, s, calls, savedRuntimeNow, "")
	if len(events) != 1 || events[0].batch != nil || !s.entries[d.key].next.Equal(savedRuntimeNow.Add(weather.RefreshSeconds*time.Second)) {
		t.Fatal("ordinary viewing added warning history work")
	}
	for minute := 1; minute < weather.RefreshSeconds/60; minute++ {
		s.tick(savedRuntimeNow.Add(time.Duration(minute) * time.Minute))
	}
	if len(calls) != 0 {
		t.Fatal("ordinary viewing polled early")
	}
	if err := s.setDemands([]alertDemand{d, d}, savedRuntimeNow); err != nil || len(s.entries) != 1 {
		t.Fatal("same point did not share work", err)
	}
}

func BenchmarkAlertSchedulerIdle(b *testing.B) {
	d := alertTestDemand(b, 0, false)
	s := newAlertScheduler(func(context.Context, weather.AlertMessageQuery, time.Time) (weather.AlertMessagePage, error) {
		b.Fatal("unexpected fetch")
		return weather.AlertMessagePage{}, nil
	}, nil)
	if err := s.setDemands([]alertDemand{d}, savedRuntimeNow); err != nil {
		b.Fatal(err)
	}
	s.entries[d.key].next = savedRuntimeNow.Add(time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := s.setDemands([]alertDemand{d}, savedRuntimeNow); err != nil {
			b.Fatal(err)
		}
		s.tick(savedRuntimeNow)
	}
}

func TestAlertSchedulerUsesReceiptTimeWithoutRenewingQueuedData(t *testing.T) {
	s, calls := alertTestScheduler(t, alertTestDemand(t, 0, false))
	finished := savedRuntimeNow.Add(time.Second)
	s.finishedNow = func() time.Time { return finished }
	s.tick(savedRuntimeNow)
	call := alertAwaitCall(t, calls)
	// The API can include an alert issued while the HTTP request is in flight.
	message := alertTestMessage("issued in flight", savedRuntimeNow.Add(500*time.Millisecond))
	events := alertComplete(t, s, call, savedRuntimeNow.Add(2*time.Minute), weather.AlertMessagePage{FetchedAt: call.now, Complete: true, Messages: []weather.AlertMessage{message}}, nil)
	if len(events) != 1 || events[0].err != "" || events[0].active["fetched_at"] != finished.Format(time.RFC3339Nano) {
		t.Fatal("in-flight issue rejected or queued data falsely refreshed", events)
	}
}

func TestAlertSchedulerTimeoutReportsFailureBeforeSlowCallbackReturns(t *testing.T) {
	s, calls := alertTestScheduler(t, alertTestDemand(t, 0, true))
	s.tick(savedRuntimeNow)
	call := alertAwaitCall(t, calls)
	// Model an expired owned deadline without sleeping twelve seconds. The
	// callback still occupies its slot; changing this owner-only field does
	// not change the callback's captured context.
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	s.job.ctx = expired
	events := s.tick(savedRuntimeNow.Add(12 * time.Second))
	if len(events) != 1 || events[0].err != "fetch_timeout" || !events[0].activeFailed || events[0].batch.Complete || s.job == nil {
		t.Fatal("timeout was hidden or released the worker slot", events)
	}
	if events := s.tick(savedRuntimeNow.Add(13 * time.Second)); len(events) != 0 {
		t.Fatal("timeout repeated every tick", events)
	}
	events = alertComplete(t, s, call, savedRuntimeNow.Add(14*time.Second), weather.AlertMessagePage{FetchedAt: call.now, Complete: true}, nil)
	if len(events) != 0 || s.job != nil {
		t.Fatal("late timed-out success revived the feed", events)
	}
}
