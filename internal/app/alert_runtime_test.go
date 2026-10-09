package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func alertAppComplete(t *testing.T, a *App, call alertProbeCall, messages ...weather.AlertMessage) M {
	t.Helper()
	call.reply <- alertProbeReply{page: weather.AlertMessagePage{Messages: messages, FetchedAt: call.now, Complete: true}}
	select {
	case result := <-a.alerts.done:
		a.alerts.done <- result
	case <-time.After(time.Second):
		t.Fatal("app alert callback did not complete")
	}
	return a.Snapshot()
}

func TestSharedAlertRuntimeDoesNotWaitForForecastOrDuplicateFeed(t *testing.T) {
	calls := make(chan alertProbeCall, 4)
	forecastEntered, forecastGate := make(chan struct{}), make(chan struct{})
	var legacyCalls atomic.Int32
	a, _ := runtimeLocations(t, Options{
		FetchAlertMessages: alertProbeFetcher(calls),
		FetchAlerts: func(context.Context, M, time.Time) (M, error) {
			legacyCalls.Add(1)
			return weather.UnavailableAlerts(), nil
		},
		FetchCountry: func(ctx context.Context, location M, now time.Time, _ string) (M, error) {
			close(forecastEntered)
			select {
			case <-forecastGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			f := appFixture(now)
			f["location"] = location
			object(f["current"])["temperature_c"] = 99.0
			return f, nil
		},
	}, 0)
	a.beginFetch(nil)
	<-forecastEntered
	generation := a.generation
	a.Tick(context.Background())
	call := alertAwaitCall(t, calls)
	m := alertTestMessage("current warning", call.now)
	snapshot := alertAppComplete(t, a, call, m)
	if object(snapshot["alerts"])["status"] != "available" || len(object(snapshot["alerts"])["items"].([]any)) != 1 || !a.fetchBusy || a.generation != generation {
		t.Fatal("alerts completed or waited for forecast work", snapshot["alerts"], a.fetchBusy)
	}
	if legacyCalls.Load() != 0 || len(calls) != 0 {
		t.Fatal("primary feed was fetched twice")
	}
	close(forecastGate)
	settleForecasts(t, a)
	snapshot = a.Snapshot()
	if object(snapshot["current"])["temperature_c"] != 99.0 || object(snapshot["alerts"])["freshness"] != "current" || len(object(snapshot["alerts"])["items"].([]any)) != 1 {
		t.Fatal("forecast replaced newer independent alerts", snapshot["alerts"])
	}
}

func TestSharedAlertsAvailableWithoutForecast(t *testing.T) {
	state := testState(t)
	profile := savedFixture(0)
	profile["forecast"] = nil
	if _, err := createSavedLocations(state, profile); err != nil {
		t.Fatal(err)
	}
	calls := make(chan alertProbeCall, 2)
	a, err := New(state, Options{Now: func() time.Time { return savedRuntimeNow }, FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(context.Context, M, time.Time, string) (M, error) { return nil, errors.New("forecast outage") }})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	a.Tick(context.Background())
	call := alertAwaitCall(t, calls)
	alertAppComplete(t, a, call, alertTestMessage("warning during weather outage", call.now))
	settleForecasts(t, a)
	snapshot := a.Snapshot()
	if snapshot["current"] != nil || object(snapshot["alerts"])["status"] != "available" || len(object(snapshot["alerts"])["items"].([]any)) != 1 || object(snapshot["source"])["error"] == nil {
		t.Fatal("weather outage suppressed official alerts", snapshot["alerts"], snapshot["source"])
	}
}

type ledgerWarningConsumer struct {
	ledger  *notifications.WarningLedger
	enabled bool
	batches int
	err     error
}

func (c *ledgerWarningConsumer) Enabled() bool { return c.enabled }
func (c *ledgerWarningConsumer) Observe(batch notifications.WarningBatch, _ string, now time.Time) {
	c.batches++
	c.err = c.ledger.Reconcile(batch, now)
}

func TestSharedMonitorUsesPrimaryWhileHiddenWithoutForecastPolling(t *testing.T) {
	now := savedRuntimeNow
	calls := make(chan alertProbeCall, 4)
	var forecasts atomic.Int32
	a, state := runtimeLocations(t, Options{Now: func() time.Time { return now }, FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		forecasts.Add(1)
		return nil, errors.New("must not fetch")
	}}, 1)
	ledger, err := notifications.OpenWarningLedger(state)
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	consumer := &ledgerWarningConsumer{ledger: ledger, enabled: true}
	a.warningObserver = consumer
	a.setPresented(false)
	call := alertAwaitCall(t, calls)
	if call.query.Location["latitude"] != a.primary.location["latitude"] {
		t.Fatal("monitor followed viewed city")
	}
	if call.query.Active {
		t.Fatal("fresh cached current feed was fetched again before history")
	}
	old := alertTestMessage("history only", savedRuntimeNow)
	alertAppComplete(t, a, call, old)
	now = now.Add(30 * time.Second)
	a.Tick(context.Background())
	call = alertAwaitCall(t, calls)
	if !call.query.Active {
		t.Fatal("final current check missing")
	}
	alertAppComplete(t, a, call)
	if consumer.batches != 1 || consumer.err != nil || len(ledger.Candidates(pointAlertKey(a.primary), now)) != 0 {
		t.Fatal("history-only warning offered as current", consumer.batches, consumer.err)
	}
	if forecasts.Load() != 0 || a.presented {
		t.Fatal("warning-only demand started weather polling")
	}
	consumer.enabled = false
	a.Tick(context.Background())
	if len(a.alerts.entries) != 0 || a.alerts.job != nil {
		t.Fatal("disabled hidden monitor retained work")
	}
}

func TestSharedAlertsHiddenCancellationHasNoSavedPendingPromise(t *testing.T) {
	calls := make(chan alertProbeCall, 2)
	a, state := runtimeLocations(t, Options{FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(_ context.Context, loc M, now time.Time, _ string) (M, error) {
		f := appFixture(now)
		f["location"] = loc
		f["alerts"] = weather.UnavailableAlerts()
		object(f["alerts"])["freshness"], object(f["alerts"])["refreshing"] = "pending", true
		return f, nil
	}}, 0)
	a.beginFetch(nil)
	a.Tick(context.Background())
	alertAwaitCall(t, calls)
	settleForecasts(t, a)
	if object(a.Snapshot()["alerts"])["refreshing"] != true {
		t.Fatal("live active request did not indicate checking")
	}
	a.setPresented(false)
	snapshot := a.Snapshot()
	if object(snapshot["alerts"])["freshness"] == "pending" || object(snapshot["alerts"])["refreshing"] == true {
		t.Fatal("hidden app kept checking indicator", snapshot["alerts"])
	}
	profile, err := ReadPrimaryProfile(state)
	if err != nil {
		t.Fatal(err)
	}
	alerts := object(object(profile["forecast"])["alerts"])
	if alerts["freshness"] == "pending" || alerts["refreshing"] == true {
		t.Fatal("canceled checking state was persisted", alerts)
	}
}

func TestSharedBarRefreshWaitsForIndependentAlerts(t *testing.T) {
	state := testState(t)
	profile := savedFixture(0)
	object(profile["forecast"])["fetched_at"] = savedRuntimeNow.Add(-time.Hour).Format(time.RFC3339)
	if _, err := createSavedLocations(state, profile); err != nil {
		t.Fatal(err)
	}
	calls := make(chan alertProbeCall, 2)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go func() {
		done <- RefreshBarSaved(ctx, state, Options{Now: func() time.Time { return savedRuntimeNow }, FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(_ context.Context, loc M, now time.Time, _ string) (M, error) {
			f := appFixture(now)
			f["location"] = loc
			return f, nil
		}})
	}()
	call := alertAwaitCall(t, calls)
	select {
	case err := <-done:
		t.Fatal("headless refresh exited before alerts", err)
	default:
	}
	call.reply <- alertProbeReply{page: weather.AlertMessagePage{FetchedAt: call.now, Complete: true, Messages: []weather.AlertMessage{alertTestMessage("bar warning", call.now)}}}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("headless refresh did not finish")
	}
	saved, err := ReadPrimaryProfile(state)
	if err != nil || len(object(object(saved["forecast"])["alerts"])["items"].([]any)) != 1 {
		t.Fatal("headless current feed was not saved", err)
	}
}

func TestSharedAlertWarmCacheDoesNotAddStartupRequests(t *testing.T) {
	calls := make(chan alertProbeCall, 2)
	a, _ := runtimeLocations(t, Options{FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		t.Error("fresh forecast fetched at startup")
		return nil, errors.New("unexpected fetch")
	}}, 0)
	for range 5 {
		a.Tick(context.Background())
		a.Snapshot()
	}
	if a.alerts.job != nil || len(calls) != 0 {
		t.Fatal("fresh cached feed was fetched at startup")
	}
	e := a.alerts.entries[pointAlertKey(a.primary)]
	if e == nil || !e.next.Equal(savedRuntimeNow.Add(weather.RefreshSeconds*time.Second)) {
		t.Fatal("cached current-feed cadence was not retained")
	}
	if object(a.Snapshot()["alerts"])["freshness"] == "pending" {
		t.Fatal("idle cache shown as pending")
	}
}

func TestSharedBarRefreshSurvivesViewedCityThrottle(t *testing.T) {
	now := savedRuntimeNow
	calls := make(chan alertProbeCall, 3)
	a, state := runtimeLocations(t, Options{Now: func() time.Time { return now }, FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(_ context.Context, loc M, at time.Time, _ string) (M, error) {
		f := appFixture(at)
		f["location"] = loc
		return f, nil
	}}, 1)
	view := safeio.Clone(a.profile)
	view["country_code"] = "US"
	object(view["forecast"])["alerts"] = weather.UnavailableAlerts()
	if err := a.saved.put(view, true, false); err != nil {
		t.Fatal(err)
	}
	a.forecastPoint.adopt(view, now)
	a.Tick(context.Background())
	viewCall := alertAwaitCall(t, calls)
	if viewCall.query.Location["latitude"] != a.location["latitude"] {
		t.Fatal("viewed city did not consume the shared slot")
	}
	alertAppComplete(t, a, viewCall)
	a.primary.nextFetch = now
	reply, _ := a.Handle(context.Background(), request("refresh_primary", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	settleForecasts(t, a)
	if len(calls) != 0 {
		t.Fatal("primary bypassed the provider throttle")
	}
	now = now.Add(alertRequestSpacing)
	a.Tick(context.Background())
	primaryCall := alertAwaitCall(t, calls)
	if primaryCall.query.Location["latitude"] != a.primary.location["latitude"] {
		t.Fatal("queued bar refresh did not use primary city")
	}
	alertAppComplete(t, a, primaryCall, alertTestMessage("queued primary warning", primaryCall.now))
	profile, err := ReadPrimaryProfile(state)
	if err != nil || len(object(object(profile["forecast"])["alerts"])["items"].([]any)) != 1 {
		t.Fatal("queued primary feed was not persisted", err)
	}
	now = now.Add(15 * time.Second)
	a.Tick(context.Background())
	if a.primaryNeeded() || a.alerts.entries[pointAlertKey(a.primary)] != nil {
		t.Fatal("bar refresh retained background demand after its lease")
	}
}
