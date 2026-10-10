package app

import (
	"context"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

type viewedEffects struct {
	coordinatedEffects
	packets chan M
}

func (f *viewedEffects) Tick(ctx context.Context, selected, controls M) error {
	if err := f.coordinatedEffects.Tick(ctx, selected, controls); err != nil {
		return err
	}
	select {
	case f.packets <- M{"weather": weather.Clone(selected), "controls": weather.Clone(controls)}:
	default:
	}
	return nil
}

func assertViewedEffects(t *testing.T, a *App, temperature float64, units string) {
	t.Helper()
	a.fx.mu.RLock()
	defer a.fx.mu.RUnlock()
	if object(a.fx.weather["current"])["temperature_c"] != temperature || a.fx.controls["units"] != units {
		t.Fatal("effects lost selected city", a.fx.weather, a.fx.controls)
	}
}

func TestLiveEffectsFollowViewedCityAndKeepHomeIndependent(t *testing.T) {
	fx := &viewedEffects{packets: make(chan M, 8)}
	a, _ := runtimeLocations(t, Options{Offline: true, Effects: fx}, 1)
	current := object(a.forecast["current"])
	current["condition"], current["precipitation_rate_mm_hr"] = "rain", 8.0
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal("start selected-city effects", reply)
	}
	var packet M
	select {
	case packet = <-fx.packets:
	case <-time.After(time.Second):
		t.Fatal("initial native packet missing")
	}
	selected := object(packet["weather"])
	if object(selected["current"])["temperature_c"] != 21.0 || object(packet["controls"])["units"] != "C" || object(selected["effects"])["rain_intensity"].(float64) <= 0 {
		t.Fatal("initial native packet used Home instead of viewed rain", packet)
	}
	locationAction(t, a, M{"action": "view", "id": "place-102"})
	assertViewedEffects(t, a, 22, "C")
	if a.primary.id != "place-100" {
		t.Fatal("effects browsing changed Home")
	}
	// The independent native heartbeat must receive the replacement packet,
	// not retain the rainy city after the app switches to clear conditions.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case packet = <-fx.packets:
			selected = object(packet["weather"])
			if object(selected["current"])["temperature_c"] != 22.0 {
				continue
			}
			if object(selected["effects"])["rain_intensity"] != 0.0 {
				t.Fatal("old city's rain survived selection", packet)
			}
		case <-deadline:
			t.Fatal("native heartbeat retained the previous city")
		}
		break
	}
	locationAction(t, a, M{"action": "primary", "id": "place-101"})
	assertViewedEffects(t, a, 22, "C")
	locationAction(t, a, M{"action": "remove", "id": "place-102", "replacement": ""})
	assertViewedEffects(t, a, 21, "C")
	if a.id != "place-101" || a.primary.id != "place-101" {
		t.Fatal("removing viewed city did not return effects to Home")
	}
}

func TestLiveEffectsRetainViewedRefreshWhileHidden(t *testing.T) {
	calls := make(chan string, 4)
	started := make(chan context.Context, 1)
	release := make(chan struct{}, 1)
	defer close(release)
	a, _ := runtimeLocations(t, Options{Effects: &coordinatedEffects{}, Fetch: func(ctx context.Context, location M, now time.Time) (M, error) {
		name := stringOf(location["name"])
		calls <- name
		if name == "City 1" {
			started <- ctx
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		forecast := appFixture(now)
		forecast["location"] = location
		object(forecast["current"])["temperature_c"] = 32.0
		return forecast, nil
	}}, 1)
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.nextFetch, a.primary.nextFetch = savedRuntimeNow, savedRuntimeNow
	a.Tick(context.Background())
	var fetchContext context.Context
	select {
	case fetchContext = <-started:
	case <-time.After(time.Second):
		t.Fatal("viewed refresh did not start")
	}
	a.setPresented(false)
	if fetchContext.Err() != nil || !a.fetchBusy {
		t.Fatal("hiding canceled weather used by live desktop")
	}
	release <- struct{}{}
	settleForecasts(t, a)
	a.Tick(context.Background())
	assertViewedEffects(t, a, 32, "C")
	if len(calls) != 1 || <-calls != "City 1" {
		t.Fatal("effects refreshed Home or unused favorites")
	}
	reply, _ = a.Handle(context.Background(), request("stop_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.nextFetch, a.primary.nextFetch = savedRuntimeNow, savedRuntimeNow
	a.Tick(context.Background())
	settleForecasts(t, a)
	if len(calls) != 0 {
		t.Fatal("stopped hidden desktop retained forecast demand")
	}
	if err := a.notifications.Configure(M{"enabled": true, "quiet_enabled": false}); err != nil {
		t.Fatal(err)
	}
	a.Tick(context.Background())
	settleForecasts(t, a)
	if len(calls) != 1 || <-calls != "City 0" {
		t.Fatal("Home notification demand followed the viewed city")
	}
}

func TestLiveEffectsRejectCanceledCityRefresh(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{}, 1)
	defer close(release)
	a, _ := runtimeLocations(t, Options{Effects: &coordinatedEffects{}, Fetch: func(ctx context.Context, location M, now time.Time) (M, error) {
		started <- ctx
		// Deliberately finish after cancellation to exercise the ownership
		// guard instead of depending on the fetcher honoring its context.
		<-release
		forecast := appFixture(now)
		forecast["location"] = location
		object(forecast["current"])["temperature_c"] = 32.0
		return forecast, nil
	}}, 1)
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.nextFetch = savedRuntimeNow
	a.Tick(context.Background())
	var fetchContext context.Context
	select {
	case fetchContext = <-started:
	case <-time.After(time.Second):
		t.Fatal("viewed refresh did not start")
	}
	locationAction(t, a, M{"action": "view", "id": "place-102"})
	if fetchContext.Err() == nil {
		t.Fatal("switching cities did not cancel the old refresh")
	}
	assertViewedEffects(t, a, 22, "C")
	// Let the canceled callback return, then process its actual completion.
	// Neither the selected snapshot nor the next native packet may adopt it.
	release <- struct{}{}
	settleForecasts(t, a)
	a.Tick(context.Background())
	assertViewedEffects(t, a, 22, "C")
	if a.id != "place-102" || object(a.forecast["current"])["temperature_c"] != 22.0 {
		t.Fatal("canceled city's forecast replaced the selected city")
	}
}
