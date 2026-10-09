package precipitation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

type fetchRequest struct {
	ctx      context.Context
	location weather.Object
	at       time.Time
	answer   chan completion
}
type fetchFixture struct {
	requests chan fetchRequest
	wake     chan struct{}
	stop     chan struct{}
	calls    atomic.Int32
}

func newFetchFixture(t *testing.T) *fetchFixture {
	t.Helper()
	f := &fetchFixture{requests: make(chan fetchRequest, 1), wake: make(chan struct{}, 1), stop: make(chan struct{})}
	t.Cleanup(func() { close(f.stop) })
	return f
}

func (f *fetchFixture) fetch(ctx context.Context, location weather.Object, at time.Time) (Data, error) {
	f.calls.Add(1)
	r := fetchRequest{ctx: ctx, location: location, at: at, answer: make(chan completion, 1)}
	select {
	case f.requests <- r:
	case <-f.stop:
		return Data{}, context.Canceled
	}
	// Intentionally ignore ctx until the test releases this callback. It must
	// remain the only active slot even if a provider is slow to cancel.
	select {
	case answer := <-r.answer:
		return answer.data, answer.err
	case <-f.stop:
		return Data{}, context.Canceled
	}
}
func (f *fetchFixture) notify() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}
func (f *fetchFixture) request(t *testing.T) fetchRequest {
	t.Helper()
	select {
	case r := <-f.requests:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("fetch did not start")
		return fetchRequest{}
	}
}
func (f *fetchFixture) complete(t *testing.T, r fetchRequest, data Data, err error) {
	t.Helper()
	r.answer <- completion{data: data, err: err}
	select {
	case <-f.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("completion did not wake owner")
	}
}
func requestData(r fetchRequest) Data {
	return Data{Latitude: r.location["latitude"].(float64), Longitude: r.location["longitude"].(float64), GridLatitude: r.location["latitude"].(float64), GridLongitude: r.location["longitude"].(float64), Timezone: r.location["timezone"].(string), FetchedAt: r.at, Hours: []Hour{{End: r.at.Truncate(time.Second), TotalMM: Number(1)}}}
}

func TestDemandAndBoundedRetention(t *testing.T) {
	f := newFetchFixture(t)
	loads, saves := 0, 0
	var disk *Data
	c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify, Load: func() (*Data, error) { loads++; return disk, nil }, Save: func(d Data) error {
		saves++
		disk = &d
		return nil
	}})
	now := testTime()
	for range 5 {
		_, p := c.Since(nil, now)
		if p.Status != "closed" || p.Data != nil || c.NextPoll(now) != 0 {
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
	f.complete(t, r, requestData(r), nil)
	revision, p := c.Since(nil, now)
	if p.Status != "fresh" || p.Data == nil || p.Refreshing || p.SaveFailed || loads != 1 || saves != 1 || c.NextPoll(now) != RefreshAfter {
		t.Fatalf("successful on-demand fetch: %+v loads=%d saves=%d", p, loads, saves)
	}
	p.Data.Hours[0].TotalMM = Number(99)
	if c.data.Hours[0].TotalMM != Number(1) || disk.Hours[0].TotalMM != Number(1) {
		t.Fatal("presentation shares retained or saved data")
	}
	if _, same := c.Since(&revision, now); same != nil {
		t.Fatal("unchanged read rebuilt the presentation")
	}
	for range 40 {
		c.Close(now)
		_, closed := c.Since(nil, now)
		if closed.Data != nil || closed.Status != "closed" || closed.Refreshing || c.NextPoll(now) != 0 {
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
	c.Poll(now.Add(ClosedRetention))
	if c.data != nil {
		t.Fatal("closed retention exceeded its bound")
	}
	if err := c.Open(testLocation(), now.Add(ClosedRetention)); err != nil {
		t.Fatal(err)
	}
	_, p = c.Since(nil, now.Add(ClosedRetention))
	if loads != 2 || p.Status != "fresh" || p.Data == nil || f.calls.Load() != 1 {
		t.Fatal("on-demand cache reload failed after memory eviction")
	}
}

func TestRapidPlaceAndCloseChangesCannotMultiplyWorkers(t *testing.T) {
	f := newFetchFixture(t)
	saves := 0
	c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify, Save: func(Data) error { saves++; return nil }})
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
	f.complete(t, old, requestData(old), nil)
	_, p := c.Since(nil, now.Add(10*time.Minute))
	if p.Data != nil || saves != 0 {
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
	f.complete(t, r, requestData(r), nil)
	_, p = c.Since(nil, now.Add(10*time.Minute))
	if p.Data == nil || p.Data.Longitude != 86 || saves != 1 {
		t.Fatal("latest place result was not adopted")
	}
}

func TestInvalidResultsBackoffAndStaleData(t *testing.T) {
	for name, mutate := range map[string]func(*Data){
		"wrong place":     func(d *Data) { d.Latitude += .1 },
		"wrong retrieval": func(d *Data) { d.FetchedAt = d.FetchedAt.Add(time.Second) },
		"bad values":      func(d *Data) { d.Hours[0].Probability = Number(100) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFetchFixture(t)
			c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify})
			now := testTime()
			_ = c.Open(testLocation(), now)
			r := f.request(t)
			d := requestData(r)
			mutate(&d)
			f.complete(t, r, d, nil)
			_, p := c.Since(nil, now)
			if p.Status != "unavailable" || p.Error != "fetch_failed" || p.Data != nil || c.NextPoll(now) != retryAfter {
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
			f.complete(t, r, requestData(r), nil)
			_, p = c.Since(nil, now.Add(time.Minute))
			if p.Status != "fresh" || p.Data == nil {
				t.Fatal("retry did not recover")
			}
			now = now.Add(time.Minute + RefreshAfter)
			_, p = c.Since(nil, now)
			if p.Status != "stale" || !p.Refreshing || p.Data == nil {
				t.Fatal("refresh hid retained data or did not mark stale")
			}
			r = f.request(t)
			f.complete(t, r, Data{}, errors.New("offline"))
			_, p = c.Since(nil, now)
			if p.Status != "stale" || p.Refreshing || p.Error != "fetch_failed" || p.Data == nil {
				t.Fatal("refresh failure erased cached data")
			}
			c.Close(now)
		})
	}
}

func TestGlobalRequestBudgetAcrossPlaces(t *testing.T) {
	f := newFetchFixture(t)
	c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify})
	now := testTime()
	for i := 0; i < requestsPerMinute; i++ {
		l := testLocation()
		l["longitude"] = 85.0 + float64(i)
		at := now.Add(time.Duration(i) * requestSpacing)
		_ = c.Open(l, at)
		r := f.request(t)
		f.complete(t, r, requestData(r), nil)
		c.Poll(at)
	}
	l := testLocation()
	l["longitude"] = 100.0
	_ = c.Open(l, now.Add(12*time.Second))
	_, p := c.Since(nil, now.Add(59*time.Second))
	if c.active || f.calls.Load() != requestsPerMinute || p.Status != "waiting" || !p.RetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("global rate budget bypassed: %+v calls=%d", p, f.calls.Load())
	}
	c.Poll(now.Add(time.Minute))
	r := f.request(t)
	f.complete(t, r, requestData(r), nil)
	c.Poll(now.Add(time.Minute))
	if f.calls.Load() != requestsPerMinute+1 || c.data.Longitude != 100 {
		t.Fatal("rate-limited view did not recover")
	}
}

func TestOfflineCacheFailureExpiryAndShutdown(t *testing.T) {
	now := testTime()
	d := requestData(fetchRequest{location: testLocation(), at: now.Add(-time.Hour)})
	for name, mutate := range map[string]func(*Data){
		"valid stale": func(*Data) {},
		"expired":     func(d *Data) { d.FetchedAt = now.Add(-ExpireAfter) },
		"future":      func(d *Data) { d.FetchedAt = now.Add(6 * time.Minute) },
		"invalid":     func(d *Data) { d.Hours = nil },
		"wrong place": func(d *Data) { d.Latitude += .1 },
	} {
		t.Run(name, func(t *testing.T) {
			copy := clone(d)
			mutate(&copy)
			c := NewController(Dependencies{Offline: true, Load: func() (*Data, error) { return &copy, nil }, Fetch: func(context.Context, weather.Object, time.Time) (Data, error) {
				t.Error("offline fetch")
				return Data{}, nil
			}})
			_ = c.Open(testLocation(), now)
			_, p := c.Since(nil, now)
			if p.Error != "offline" || c.NextPoll(now) != 0 || (p.Data != nil) != (name == "valid stale") {
				t.Fatalf("bad offline result: %+v", p)
			}
			if name == "valid stale" {
				if p.Status != "stale" {
					t.Fatal("old cache marked fresh")
				}
				_, p = c.Since(nil, now.Add(5*time.Hour))
				if p.Status != "unavailable" || p.Data != nil {
					t.Fatal("expired cache still presented")
				}
			}
		})
	}
	f := newFetchFixture(t)
	c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify, Load: func() (*Data, error) { return nil, errors.New("corrupt cache") }, Save: func(Data) error { return errors.New("disk full") }})
	_ = c.Open(testLocation(), now)
	r := f.request(t)
	f.complete(t, r, requestData(r), nil)
	_, p := c.Since(nil, now)
	if p.Status != "fresh" || p.Data == nil || !p.SaveFailed || p.Error != "" {
		t.Fatal("optional storage failure blocked weather")
	}
	c.Poll(now.Add(RefreshAfter))
	r = f.request(t)
	c.Shutdown()
	if r.ctx.Err() != context.Canceled || c.data != nil || c.NextPoll(now) != 0 {
		t.Fatal("shutdown retained active demand or data")
	}
	f.complete(t, r, requestData(r), nil)
	_, p = c.Since(nil, now.Add(time.Hour))
	if p.Status != "closed" || p.Data != nil || c.active || c.Open(testLocation(), now) == nil {
		t.Fatal("late result reopened shutdown controller")
	}
}

func TestClockRollbackAndMinimumSpacing(t *testing.T) {
	f := newFetchFixture(t)
	c := NewController(Dependencies{Fetch: f.fetch, Notify: f.notify})
	now := testTime()
	_ = c.Open(testLocation(), now)
	r := f.request(t)
	f.complete(t, r, requestData(r), nil)
	c.Poll(now)
	l := testLocation()
	l["longitude"] = 86.0
	_ = c.Open(l, now.Add(time.Second))
	if c.active || c.NextPoll(now.Add(time.Second)) != time.Second {
		t.Fatal("request spacing bypassed")
	}
	back := now.Add(-time.Hour)
	c.Poll(back)
	if c.active || c.NextPoll(back) != requestSpacing {
		t.Fatal("clock rollback bypassed spacing or stalled until old wall time")
	}
	c.Poll(back.Add(requestSpacing))
	r = f.request(t)
	f.complete(t, r, requestData(r), nil)
	c.Poll(back.Add(requestSpacing))
	if c.data == nil || c.data.FetchedAt != back.Add(requestSpacing) {
		t.Fatal("clock-corrected fetch rejected")
	}
}
