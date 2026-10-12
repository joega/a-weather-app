package notifications

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/locale"
)

func TestPrecipitationRegionalFormatterRunsOnlyForReservedDelivery(t *testing.T) {
	bodies := make(chan string, 2)
	var calls atomic.Int32
	w := testWatcher(t, testState(t), func(_ context.Context, _ string, body string) error { bodies <- body; return nil })
	w.SetLanguage("uk")
	w.SetDateFormatter(func(ctx context.Context, instant time.Time, zone *time.Location) string {
		calls.Add(1)
		if ctx.Err() != nil || zone.String() != "America/New_York" {
			t.Error("lost context or city timezone", ctx.Err(), zone)
		}
		return instant.In(zone).Format("02/01/2006 15:04 MST")
	})
	enable(t, w)
	forecast, location := testForecast(testNow)
	w.Tick(forecast, location, testNow, true)
	finish(t, w, forecast, location)
	body := <-bodies
	if !strings.Contains(body, "27/09/2026 12:00 EDT–27/09/2026 13:00 EDT") {
		t.Fatal("regional time not delivered", body)
	}
	expected := locale.T("uk", "%1: %2% chance of precipitation for %3. Open-Meteo hourly forecast; timing may change.", location["name"], 100, "27/09/2026 12:00 EDT–27/09/2026 13:00 EDT")
	if body != expected {
		t.Fatal("interface-language composition mismatch", body, expected)
	}
	for range 10 {
		w.Tick(forecast, location, testNow, true)
	}
	if calls.Load() != 2 {
		t.Fatal("formatter ran per tick", calls.Load())
	}
}
func TestWarningRegionalFormatterPreservesCanonicalExpiry(t *testing.T) {
	calls := make(chan warningSendCall, 2)
	var dates atomic.Int32
	w := warningWatch(t, testState(t), warningProbe(calls, false))
	w.SetLanguage("hi")
	message := warningFixture("region", "Alert", warningTestNow)
	w.SetDateFormatter(func(ctx context.Context, instant time.Time, zone *time.Location) string {
		dates.Add(1)
		if !instant.Equal(message.Expires) || zone.String() != "America/New_York" || ctx.Err() != nil {
			t.Error("lost canonical expiry/timezone", instant, zone, ctx.Err())
		}
		return "02/11/2026 17:30 EST"
	})
	location := warningTarget()
	warningEnable(t, w, location, warningTestNow)
	warningFeed(w, warningTestNow, message)
	call := warningCall(t, calls)
	if !strings.Contains(call.notice.Body, "02/11/2026 17:30 EST") || !strings.Contains(call.notice.Body, locale.T("hi", "%1 · valid until %2", locale.T("hi", message.Severity), "02/11/2026 17:30 EST")) {
		t.Fatal(call.notice.Body)
	}
	warningFinish(t, w, call, location, warningTestNow, nil)
	for range 10 {
		w.Tick(location, "US", true, warningTestNow)
		warningFeed(w, warningTestNow, message)
	}
	if dates.Load() != 1 {
		t.Fatal("warning formatter ran per tick", dates.Load())
	}
}
