package app

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func currentSummaryFixture(at time.Time, temperature float64) M {
	return M{"fetched_at": at.Format(time.RFC3339), "valid_at": at.Format(time.RFC3339), "temperature_c": temperature, "condition": "clear", "is_day": true}
}

type currentSummaryCall struct {
	ctx      context.Context
	location M
	at       time.Time
	release  chan struct{}
}

type currentSummaryProbe struct {
	calls chan currentSummaryCall
	mu    sync.Mutex
	all   []currentSummaryCall
}

func newCurrentSummaryProbe(t *testing.T) *currentSummaryProbe {
	t.Helper()
	p := &currentSummaryProbe{calls: make(chan currentSummaryCall, 20)}
	t.Cleanup(func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, call := range p.all {
			select {
			case <-call.release:
			default:
				close(call.release)
			}
		}
	})
	return p
}

// Deliberately ignore cancellation until released: the scheduler must retain
// its single admission slot even when a provider has not returned yet.
func (p *currentSummaryProbe) fetch(ctx context.Context, location M, at time.Time) (M, error) {
	call := currentSummaryCall{ctx: ctx, location: location, at: at, release: make(chan struct{})}
	p.mu.Lock()
	p.all = append(p.all, call)
	p.mu.Unlock()
	p.calls <- call
	<-call.release
	return currentSummaryFixture(at, 41), nil
}

func (p *currentSummaryProbe) next(t *testing.T) currentSummaryCall {
	t.Helper()
	select {
	case call := <-p.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("current summary worker did not start")
		return currentSummaryCall{}
	}
}

func finishCurrentSummary(t *testing.T, a *App) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for a.summaryWork != nil {
		a.pollSavedSummaries()
		if time.Now().After(deadline) {
			t.Fatal("current summary worker did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func summaryCard(t *testing.T, a *App, id string) M {
	t.Helper()
	for _, value := range object(a.savedLocationsSnapshot())["items"].([]any) {
		row := object(value)
		if row["id"] == id {
			return object(row["summary"])
		}
	}
	return nil
}

func summaryRuntime(t *testing.T, now *time.Time, fetch func(context.Context, M, time.Time) (M, error), viewed int) (*App, *atomic.Int32) {
	t.Helper()
	full := &atomic.Int32{}
	a, _ := runtimeLocations(t, Options{
		Now:                 func() time.Time { return *now },
		FetchCurrentSummary: fetch,
		Fetch: func(context.Context, M, time.Time) (M, error) {
			full.Add(1)
			return nil, errors.New("unexpected full forecast for summary test")
		},
		FetchAlerts: func(context.Context, M, time.Time) (M, error) {
			return nil, errors.New("fixture alerts unavailable")
		},
	}, viewed)
	// The viewed/Home full forecasts are already cached. Keep their ordinary
	// refresh deadline separate from the saved-card scheduler under test.
	a.nextFetch, a.primary.nextFetch = now.Add(2*time.Hour), now.Add(2*time.Hour)
	return a, full
}

func TestSavedCurrentSummarySingleWorkerSpacingAndRefresh(t *testing.T) {
	now := savedRuntimeNow.Add(31 * time.Minute)
	probe := newCurrentSummaryProbe(t)
	a, full := summaryRuntime(t, &now, probe.fetch, 0)
	a.Tick(context.Background())
	first := probe.next(t)
	if first.location["name"] != "City 1" {
		t.Fatal("summary targeted viewed city or wrong favorite", first.location)
	}
	if deadline, ok := first.ctx.Deadline(); !ok || time.Until(deadline) > 10*time.Second || time.Until(deadline) < 9*time.Second {
		t.Fatal("current summary lacks its ten-second request deadline")
	}
	for range 5 {
		a.Tick(context.Background())
	}
	if len(probe.calls) != 0 || a.summaryWork == nil {
		t.Fatal("active worker admitted another request")
	}
	close(first.release)
	finishCurrentSummary(t, a)
	if card := summaryCard(t, a, "place-101"); card["temperature_c"] != 41.0 || card["freshness"] != "fresh" {
		t.Fatal("current summary did not reach its saved card", card)
	}
	profile, err := a.saved.profile("place-101")
	if err != nil || object(object(profile["forecast"])["current"])["temperature_c"] != 21.0 {
		t.Fatal("compact summary replaced the full forecast cache", err, profile)
	}
	now = now.Add(9 * time.Second)
	a.tickSavedSummaries()
	if len(probe.calls) != 0 || a.summaryWork != nil {
		t.Fatal("global spacing was bypassed")
	}
	if delay := a.Interval(); delay > time.Second {
		t.Fatal("scheduler would sleep past the next summary deadline", delay)
	}
	now = now.Add(time.Second)
	a.tickSavedSummaries()
	second := probe.next(t)
	if second.location["name"] != "City 2" {
		t.Fatal("successful favorite ignored its thirty-minute cooldown", second.location)
	}
	close(second.release)
	finishCurrentSummary(t, a)
	now = first.at.Add(30*time.Minute - time.Second)
	a.tickSavedSummaries()
	if len(probe.calls) != 0 || a.summaryWork != nil {
		t.Fatal("successful current data refreshed before thirty minutes")
	}
	now = first.at.Add(30 * time.Minute)
	a.tickSavedSummaries()
	third := probe.next(t)
	if third.location["name"] != "City 1" {
		t.Fatal("eligible favorite was not refreshed", third.location)
	}
	close(third.release)
	finishCurrentSummary(t, a)
	if full.Load() != 0 {
		t.Fatal("warming favorites fetched full forecasts", full.Load())
	}
}

func TestSavedCurrentSummaryFailureAndInvalidDataRetry(t *testing.T) {
	for _, bad := range []string{"fetch error", "invalid temperature", "missing field", "extra field"} {
		t.Run(bad, func(t *testing.T) {
			now := savedRuntimeNow.Add(31 * time.Minute)
			calls := make(chan time.Time, 4)
			var attempts atomic.Int32
			a, full := summaryRuntime(t, &now, func(_ context.Context, _ M, at time.Time) (M, error) {
				calls <- at
				v := currentSummaryFixture(at, 44)
				if attempts.Add(1) == 1 {
					switch bad {
					case "fetch error":
						return nil, errors.New("fixture current unavailable")
					case "invalid temperature":
						v["temperature_c"] = "44"
					case "missing field":
						delete(v, "condition")
					case "extra field":
						v["hourly"] = []any{}
					}
				}
				return v, nil
			}, 0)
			if err := a.saved.remove("place-102", ""); err != nil {
				t.Fatal(err)
			}
			a.tickSavedSummaries()
			finishCurrentSummary(t, a)
			if len(calls) != 1 || summaryCard(t, a, "place-101")["temperature_c"] != 21.0 {
				t.Fatal("failed or invalid summary replaced cached weather")
			}
			now = now.Add(5*time.Minute - time.Second)
			a.tickSavedSummaries()
			if a.summaryWork != nil || len(calls) != 1 {
				t.Fatal("failed current request retried too soon")
			}
			now = now.Add(time.Second)
			a.tickSavedSummaries()
			finishCurrentSummary(t, a)
			if len(calls) != 2 || summaryCard(t, a, "place-101")["temperature_c"] != 44.0 {
				t.Fatal("failed current request did not retry after five minutes")
			}
			if full.Load() != 0 {
				t.Fatal("summary retry fetched a full forecast")
			}
		})
	}
}

func TestSavedCurrentSummaryPresentationAndDemandGates(t *testing.T) {
	for _, gate := range []string{"hidden", "offline", "primary-only", "demanded home", "unresolved auto"} {
		t.Run(gate, func(t *testing.T) {
			now := savedRuntimeNow.Add(31 * time.Minute)
			calls := make(chan string, 4)
			a, full := summaryRuntime(t, &now, func(_ context.Context, location M, at time.Time) (M, error) {
				calls <- stringOf(location["name"])
				return currentSummaryFixture(at, 42), nil
			}, 1)
			switch gate {
			case "hidden":
				a.setPresented(false)
			case "offline":
				a.options.Offline = true
			case "primary-only":
				a.primaryOnly = true
			case "demanded home":
				a.barRefreshUntil = now.Add(time.Hour)
			case "unresolved auto":
				if err := a.saved.remove("place-100", "place-101"); err != nil {
					t.Fatal(err)
				}
				profile := object(savedEntry(a.saved.doc, "place-102")["profile"])
				profile["mode"], profile["location"], profile["place"] = "auto", nil, nil
			}
			a.tickSavedSummaries()
			finishCurrentSummary(t, a)
			if gate == "demanded home" {
				if len(calls) != 1 || <-calls != "City 2" {
					t.Fatal("demanded Home or viewed city received compact work")
				}
			} else if len(calls) != 0 {
				t.Fatal("summary admitted without eligible visible favorite", gate)
			}
			if full.Load() != 0 {
				t.Fatal("summary gate fetched a full forecast")
			}
		})
	}
}

func TestSavedCurrentSummaryHideRetainsSlotAndIgnoresLateResult(t *testing.T) {
	now := savedRuntimeNow.Add(31 * time.Minute)
	probe := newCurrentSummaryProbe(t)
	a, full := summaryRuntime(t, &now, probe.fetch, 0)
	a.tickSavedSummaries()
	first := probe.next(t)
	a.setPresented(false)
	select {
	case <-first.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("hiding did not cancel the current request")
	}
	now = now.Add(11 * time.Second)
	a.setPresented(true)
	for range 3 {
		a.tickSavedSummaries()
	}
	if a.summaryWork == nil || len(probe.calls) != 0 {
		t.Fatal("hide/show discarded a still-running admission slot")
	}
	close(first.release)
	finishCurrentSummary(t, a)
	if summaryCard(t, a, "place-101")["temperature_c"] != 21.0 {
		t.Fatal("late hidden work was published after showing again")
	}
	if full.Load() != 0 {
		t.Fatal("showing warm cards fetched a full forecast")
	}
}

func TestSavedCurrentSummaryRejectsRemovedChangedOrNewerForecast(t *testing.T) {
	for _, mutation := range []string{"remove", "identity", "full forecast"} {
		t.Run(mutation, func(t *testing.T) {
			now := savedRuntimeNow.Add(31 * time.Minute)
			probe := newCurrentSummaryProbe(t)
			a, _ := summaryRuntime(t, &now, probe.fetch, 0)
			a.tickSavedSummaries()
			call := probe.next(t)
			switch mutation {
			case "remove":
				if err := a.saved.remove("place-101", ""); err != nil {
					t.Fatal(err)
				}
			case "identity":
				candidate := a.saved.document()
				object(object(savedEntry(candidate, "place-101")["profile"])["location"])["latitude"] = 89.0
				if err := a.saved.commit(candidate); err != nil {
					t.Fatal(err)
				}
			case "full forecast":
				profile := savedFixture(1)
				profile["country_code"] = "DE"
				forecast := object(profile["forecast"])
				forecast["fetched_at"] = now.Add(time.Minute).Format(time.RFC3339)
				current := object(forecast["current"])
				current["time"], current["temperature_c"] = now.Add(time.Minute).Format(time.RFC3339), 55.0
				if err := a.saved.put(profile, false, false); err != nil {
					t.Fatal(err)
				}
			}
			close(call.release)
			finishCurrentSummary(t, a)
			card := summaryCard(t, a, "place-101")
			if mutation == "remove" && card != nil {
				t.Fatal("removed favorite was resurrected", card)
			}
			if mutation == "identity" && card["temperature_c"] == 41.0 {
				t.Fatal("old identity received a late current result", card)
			}
			if mutation == "full forecast" && card["temperature_c"] != 55.0 {
				t.Fatal("old compact result superseded newer full weather", card)
			}
		})
	}
}

func TestSavedCurrentSummaryCloseCancelsWorker(t *testing.T) {
	now := savedRuntimeNow.Add(31 * time.Minute)
	started := make(chan context.Context, 1)
	a, _ := summaryRuntime(t, &now, func(ctx context.Context, _ M, _ time.Time) (M, error) {
		started <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}, 0)
	original := a.saved.document()
	a.tickSavedSummaries()
	var worker context.Context
	select {
	case worker = <-started:
	case <-time.After(time.Second):
		t.Fatal("current worker did not start")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-worker.Done():
	default:
		t.Fatal("Close did not cancel current weather")
	}
	a.pollSavedSummaries()
	if !reflect.DeepEqual(original, a.saved.doc) {
		t.Fatal("closing changed the saved full forecast registry")
	}
}
