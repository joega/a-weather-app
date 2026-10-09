package airquality

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

type outlookFetchRequest struct {
	ctx      context.Context
	location weather.Object
	at       time.Time
	answer   chan outlookCompletion
}
type outlookFetchFixture struct {
	requests chan outlookFetchRequest
	wake     chan struct{}
	stop     chan struct{}
	calls    atomic.Int32
}

func newOutlookFetchFixture(t *testing.T) *outlookFetchFixture {
	t.Helper()
	f := &outlookFetchFixture{requests: make(chan outlookFetchRequest, 1), wake: make(chan struct{}, 1), stop: make(chan struct{})}
	t.Cleanup(func() { close(f.stop) })
	return f
}

func (f *outlookFetchFixture) fetch(ctx context.Context, location weather.Object, at time.Time) (Outlook, error) {
	f.calls.Add(1)
	r := outlookFetchRequest{ctx: ctx, location: location, at: at, answer: make(chan outlookCompletion, 1)}
	select {
	case f.requests <- r:
	case <-f.stop:
		return Outlook{}, context.Canceled
	}
	// Intentionally ignore ctx until the test releases this callback. It must
	// remain the only active slot even if a provider is slow to cancel.
	select {
	case answer := <-r.answer:
		return answer.data, answer.err
	case <-f.stop:
		return Outlook{}, context.Canceled
	}
}
func (f *outlookFetchFixture) notify() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}
func (f *outlookFetchFixture) request(t *testing.T) outlookFetchRequest {
	t.Helper()
	select {
	case r := <-f.requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not start")
		return outlookFetchRequest{}
	}
}
func (f *outlookFetchFixture) complete(t *testing.T, r outlookFetchRequest, data Outlook, err error) {
	t.Helper()
	r.answer <- outlookCompletion{data: data, err: err}
	select {
	case <-f.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("outlookCompletion did not wake owner")
	}
}
func outlookRequestData(r outlookFetchRequest) Outlook {
	return Outlook{Latitude: r.location["latitude"].(float64), Longitude: r.location["longitude"].(float64), GridLatitude: r.location["latitude"].(float64), GridLongitude: r.location["longitude"].(float64), Timezone: r.location["timezone"].(string), FetchedAt: r.at, Hours: []OutlookHour{{Time: r.at.Truncate(time.Hour), Values: [8]OutlookValue{{Number: 1, Known: true}}}}}
}

func TestOutlookDemandAndBoundedRetention(t *testing.T) {
	f := newOutlookFetchFixture(t)
	loads, saves := 0, 0
	var disk *Outlook
	c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify, Load: func() (*Outlook, error) { loads++; return disk, nil }, Save: func(d Outlook) error {
		saves++
		disk = &d
		return nil
	}})
	now := testTime()
	for range 5 {
		_, p := c.Since(nil, now)
		if p.Status != "closed" || p.Outlook != nil || c.NextPoll(now) != 0 {
			t.Fatal("unused feature retained work")
		}
	}
	if loads != 0 || saves != 0 || f.calls.Load() != 0 {
		t.Fatal("closed controller performed I/O")
	}
	if err := c.Open(testLocation(), now); err != nil {
		t.Fatal(err)
	}
	r := f.request(t)
	f.complete(t, r, outlookRequestData(r), nil)
	revision, p := c.Since(nil, now)
	if p.Status != "fresh" || p.Outlook == nil || p.Refreshing || p.SaveFailed || loads != 1 || saves != 1 || c.NextPoll(now) != OutlookRefreshAfter {
		t.Fatalf("successful on-demand fetch: %+v loads=%d saves=%d", p, loads, saves)
	}
	p.Outlook.Hours[0].Values[0] = OutlookValue{Number: 99, Known: true}
	if c.data.Hours[0].Values[0] != (OutlookValue{Number: 1, Known: true}) || disk.Hours[0].Values[0] != (OutlookValue{Number: 1, Known: true}) {
		t.Fatal("presentation shares retained or saved data")
	}
	if _, same := c.Since(&revision, now); same != nil {
		t.Fatal("unchanged read rebuilt the presentation")
	}
	for range 40 {
		c.Close(now)
		_, closed := c.Since(nil, now)
		if closed.Outlook != nil || closed.Status != "closed" || closed.Refreshing || c.NextPoll(now) != 0 {
			t.Fatal("closed view exposed data or scheduled work")
		}
		if err := c.Open(testLocation(), now); err != nil {
			t.Fatal(err)
		}
	}
	if f.calls.Load() != 1 || loads != 1 || saves != 1 {
		t.Fatal("reopening fresh data caused I/O")
	}
	c.Close(now)
	c.Poll(now.Add(OutlookClosedRetention))
	if c.data != nil {
		t.Fatal("closed retention exceeded its bound")
	}
	if err := c.Open(testLocation(), now.Add(OutlookClosedRetention)); err != nil {
		t.Fatal(err)
	}
	_, p = c.Since(nil, now.Add(OutlookClosedRetention))
	if loads != 2 || p.Status != "fresh" || p.Outlook == nil || f.calls.Load() != 1 {
		t.Fatal("on-demand cache reload failed after memory eviction")
	}
}

func TestOutlookRapidPlaceAndCloseChangesCannotMultiplyWorkers(t *testing.T) {
	f := newOutlookFetchFixture(t)
	saves := 0
	c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify, Save: func(Outlook) error { saves++; return nil }})
	now := testTime()
	_ = c.Open(testLocation(), now)
	old := f.request(t)
	l := testLocation()
	l["longitude"] = 86.0
	for range 100 {
		c.Close(now)
		_ = c.Open(l, now.Add(10*time.Minute))
		c.Poll(now.Add(10 * time.Minute))
	}
	if old.ctx.Err() != context.Canceled || f.calls.Load() != 1 || !c.active || saves != 0 {
		t.Fatal("cancellation released or multiplied the occupied slot")
	}
	f.complete(t, old, outlookRequestData(old), nil)
	_, p := c.Since(nil, now.Add(10*time.Minute))
	if p.Outlook != nil || saves != 0 {
		t.Fatal("late canceled result adopted or persisted")
	}
	r := f.request(t)
	if r.location["longitude"] != 86.0 || f.calls.Load() != 2 {
		t.Fatal("wrong replacement fetch")
	}
	l["longitude"] = 87.0
	if r.location["longitude"] != 86.0 {
		t.Fatal("worker location aliases caller map")
	}
	f.complete(t, r, outlookRequestData(r), nil)
	_, p = c.Since(nil, now.Add(10*time.Minute))
	if p.Outlook == nil || p.Outlook.Longitude != 86 || saves != 1 {
		t.Fatal("latest place result was not adopted")
	}
}

func TestOutlookInvalidResultsBackoffAndStaleData(t *testing.T) {
	for name, mutate := range map[string]func(*Outlook){
		"wrong place":     func(d *Outlook) { d.Latitude += .1 },
		"wrong retrieval": func(d *Outlook) { d.FetchedAt = d.FetchedAt.Add(time.Second) },
		"bad values":      func(d *Outlook) { d.Hours[0].Values[0] = OutlookValue{Number: 1001, Known: true} },
	} {
		t.Run(name, func(t *testing.T) {
			f := newOutlookFetchFixture(t)
			c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify})
			now := testTime()
			_ = c.Open(testLocation(), now)
			r := f.request(t)
			d := outlookRequestData(r)
			mutate(&d)
			f.complete(t, r, d, nil)
			_, p := c.Since(nil, now)
			if p.Status != "unavailable" || p.Error != "fetch_failed" || p.Outlook != nil || c.NextPoll(now) != outlookRetryAfter {
				t.Fatalf("bad result or retry: %+v", p)
			}
			c.Close(now)
			l := testLocation()
			l["longitude"] = 86.0
			_ = c.Open(l, now.Add(59*time.Second))
			if c.active || f.calls.Load() != 1 {
				t.Fatal("place switching bypassed failure backoff")
			}
			c.Poll(now.Add(time.Minute))
			r = f.request(t)
			f.complete(t, r, outlookRequestData(r), nil)
			_, p = c.Since(nil, now.Add(time.Minute))
			if p.Status != "fresh" || p.Outlook == nil {
				t.Fatal("retry did not recover")
			}
			now = now.Add(time.Minute + OutlookRefreshAfter)
			_, p = c.Since(nil, now)
			if p.Status != "stale" || !p.Refreshing || p.Outlook == nil {
				t.Fatal("refresh hid retained data or did not mark stale")
			}
			r = f.request(t)
			f.complete(t, r, Outlook{}, errors.New("offline"))
			_, p = c.Since(nil, now)
			if p.Status != "stale" || p.Refreshing || p.Error != "fetch_failed" || p.Outlook == nil {
				t.Fatal("refresh failure erased cached data")
			}
			c.Close(now)
		})
	}
}

func TestOutlookGlobalRequestBudgetAcrossPlaces(t *testing.T) {
	f := newOutlookFetchFixture(t)
	c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify})
	now := testTime()
	for i := 0; i < outlookRequestsPerMinute; i++ {
		l := testLocation()
		l["longitude"] = 85.0 + float64(i)
		at := now.Add(time.Duration(i) * outlookRequestSpacing)
		_ = c.Open(l, at)
		r := f.request(t)
		f.complete(t, r, outlookRequestData(r), nil)
		c.Poll(at)
	}
	l := testLocation()
	l["longitude"] = 100.0
	_ = c.Open(l, now.Add(12*time.Second))
	_, p := c.Since(nil, now.Add(59*time.Second))
	if c.active || f.calls.Load() != outlookRequestsPerMinute || p.Status != "waiting" || !p.RetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("global rate budget bypassed: %+v calls=%d", p, f.calls.Load())
	}
	c.Poll(now.Add(time.Minute))
	r := f.request(t)
	f.complete(t, r, outlookRequestData(r), nil)
	c.Poll(now.Add(time.Minute))
	if f.calls.Load() != outlookRequestsPerMinute+1 || c.data.Longitude != 100 {
		t.Fatal("rate-limited view did not recover")
	}
}

func TestOutlookOfflineCacheFailureExpiryAndShutdown(t *testing.T) {
	now := testTime()
	d := outlookRequestData(outlookFetchRequest{location: testLocation(), at: now.Add(-time.Hour)})
	for name, mutate := range map[string]func(*Outlook){
		"valid stale": func(*Outlook) {},
		"expired":     func(d *Outlook) { d.FetchedAt = now.Add(-OutlookExpireAfter) },
		"future":      func(d *Outlook) { d.FetchedAt = now.Add(6 * time.Minute) },
		"invalid":     func(d *Outlook) { d.Hours = nil },
		"wrong place": func(d *Outlook) { d.Latitude += .1 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := cloneOutlook(d)
			mutate(&copy)
			c := NewOutlookController(OutlookDependencies{Offline: true, Load: func() (*Outlook, error) { return &copy, nil }, Fetch: func(context.Context, weather.Object, time.Time) (Outlook, error) {
				t.Error("offline fetch")
				return Outlook{}, nil
			}})
			_ = c.Open(testLocation(), now)
			_, p := c.Since(nil, now)
			if p.Error != "offline" || c.NextPoll(now) != 0 || (p.Outlook != nil) != (name == "valid stale") {
				t.Fatalf("bad offline result: %+v", p)
			}
			if name == "valid stale" {
				if p.Status != "stale" {
					t.Fatal("old cache marked fresh")
				}
				_, p = c.Since(nil, now.Add(5*time.Hour))
				if p.Status != "unavailable" || p.Outlook != nil {
					t.Fatal("expired cache still presented")
				}
			}
		})
	}
	f := newOutlookFetchFixture(t)
	c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify, Load: func() (*Outlook, error) { return nil, errors.New("corrupt cache") }, Save: func(Outlook) error { return errors.New("disk full") }})
	_ = c.Open(testLocation(), now)
	r := f.request(t)
	f.complete(t, r, outlookRequestData(r), nil)
	_, p := c.Since(nil, now)
	if p.Status != "fresh" || p.Outlook == nil || !p.SaveFailed || p.Error != "" {
		t.Fatal("optional storage failure blocked weather")
	}
	c.Poll(now.Add(OutlookRefreshAfter))
	r = f.request(t)
	c.Shutdown()
	if r.ctx.Err() != context.Canceled || c.data != nil || c.NextPoll(now) != 0 {
		t.Fatal("shutdown retained active demand or data")
	}
	f.complete(t, r, outlookRequestData(r), nil)
	_, p = c.Since(nil, now.Add(time.Hour))
	if p.Status != "closed" || p.Outlook != nil || c.active || c.Open(testLocation(), now) == nil {
		t.Fatal("late result reopened shutdown controller")
	}
}

func TestOutlookClockRollbackAndMinimumSpacing(t *testing.T) {
	f := newOutlookFetchFixture(t)
	c := NewOutlookController(OutlookDependencies{Fetch: f.fetch, Notify: f.notify})
	now := testTime()
	_ = c.Open(testLocation(), now)
	r := f.request(t)
	f.complete(t, r, outlookRequestData(r), nil)
	c.Poll(now)
	l := testLocation()
	l["longitude"] = 86.0
	_ = c.Open(l, now.Add(time.Second))
	if c.active || c.NextPoll(now.Add(time.Second)) != time.Second {
		t.Fatal("request spacing bypassed")
	}
	back := now.Add(-time.Hour)
	c.Poll(back)
	if c.active || c.NextPoll(back) != outlookRequestSpacing {
		t.Fatal("clock rollback bypassed spacing or stalled until old wall time")
	}
	c.Poll(back.Add(outlookRequestSpacing))
	r = f.request(t)
	f.complete(t, r, outlookRequestData(r), nil)
	c.Poll(back.Add(outlookRequestSpacing))
	if c.data == nil || c.data.FetchedAt != back.Add(outlookRequestSpacing) {
		t.Fatal("clock-corrected fetch rejected")
	}
}
