package radar

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureProvider struct {
	metadata atomic.Int32
	images   atomic.Int32
	legends  atomic.Int32
	timeline func(context.Context, time.Time) (Timeline, error)
	frame    func(context.Context, Timeline, time.Time, View, time.Time) ([]byte, error)
}

func testTimeline(now time.Time) Timeline {
	t := Timeline{fetchedAt: now, coverage: Coverage{-130, 20, -60, 55}}
	for i := 59; i >= 0; i-- {
		t.times = append(t.times, now.Add(-time.Duration(i)*2*time.Minute))
	}
	return t
}
func (p *fixtureProvider) FetchTimeline(ctx context.Context, now time.Time) (Timeline, error) {
	p.metadata.Add(1)
	if p.timeline != nil {
		return p.timeline(ctx, now)
	}
	return testTimeline(now), nil
}
func (p *fixtureProvider) FetchFrame(ctx context.Context, t Timeline, at time.Time, v View, now time.Time) ([]byte, error) {
	p.images.Add(1)
	if p.frame != nil {
		return p.frame(ctx, t, at, v, now)
	}
	return []byte(at.Format(time.RFC3339Nano)), nil
}
func (p *fixtureProvider) FetchLegend(context.Context) ([]byte, error) {
	p.legends.Add(1)
	return []byte("validated legend fixture"), nil
}
func settle(t *testing.T, c *Controller, now time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for c.active {
		c.Poll(now)
		if time.Now().After(deadline) {
			t.Fatal("worker did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}
func fillHistory(t *testing.T, c *Controller, now *time.Time) {
	t.Helper()
	if err := c.SetHistory(true, *now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		*now = now.Add(requestSpacing)
		c.Poll(*now)
		settle(t, c, *now)
	}
}
func radarView(t *testing.T, lat, lon float64) View {
	t.Helper()
	v, e := CenteredView(lat, lon, 7)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestControllerDemandHistoryReuseAndExpiration(t *testing.T) {
	now := time.Date(2026, 10, 9, 16, 0, 0, 0, time.UTC)
	p := &fixtureProvider{}
	c := NewController(p, false, func() {})
	for i := 0; i < 50; i++ {
		c.Poll(now)
	}
	if p.metadata.Load() != 0 || c.active || c.bytes != 0 {
		t.Fatal("unused radar did work")
	}
	if err := c.Open(radarView(t, 42, -71), now); err != nil {
		t.Fatal(err)
	}
	fillHistory(t, c, &now)
	_, state := c.Since(nil, now)
	if state.Status != "current" || len(state.Frames) != CachedFrames || p.images.Load() != CachedFrames || p.metadata.Load() != 1 || p.legends.Load() != 1 {
		t.Fatalf("unexpected completed history: %+v calls=%d/%d/%d", state, p.metadata.Load(), p.images.Load(), p.legends.Load())
	}
	for _, f := range state.Frames {
		if f.State != "ready" || len(f.ID) != 64 {
			t.Fatal(f)
		}
	}
	last := state.Frames[len(state.Frames)-1]
	body, total, e := c.Chunk(last.ID, 0, now)
	if e != nil || string(body) != last.Time.Format(time.RFC3339Nano) || total != len(body) {
		t.Fatal("wrong exact observation bytes")
	}
	revision, _ := c.Since(nil, now)
	if _, v := c.Since(&revision, now); v != nil {
		t.Fatal("unchanged poll serialized")
	}
	c.Close(now)
	if _, _, e = c.Chunk(last.ID, 0, now); e == nil {
		t.Fatal("closed image accessible")
	}
	if err := c.Open(c.view, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if c.active || p.images.Load() != CachedFrames {
		t.Fatal("reopening refetched cached data")
	}
	c.Close(now)
	c.Poll(now.Add(closedRetention))
	if c.bytes != 0 || c.frames != nil || c.legend != nil || !c.timeline.FetchedAt().IsZero() {
		t.Fatal("closed cache retained past TTL")
	}
	if c.NextWake(now) != time.Minute {
		t.Fatal("closed radar wakes frequently")
	}
}
func TestControllerRollingHistoryKeepsActualTimes(t *testing.T) {
	now := time.Date(2026, 10, 9, 16, 0, 3, 123000000, time.UTC)
	p := &fixtureProvider{timeline: func(_ context.Context, at time.Time) (Timeline, error) {
		manifest := testTimeline(at.Truncate(2 * time.Minute).Add(3*time.Second + 123*time.Millisecond))
		manifest.fetchedAt = at
		return manifest, nil
	}}
	c := NewController(p, false, nil)
	c.Open(radarView(t, 42, -71), now)
	fillHistory(t, c, &now)
	first := p.images.Load()
	now = now.Add(2 * time.Minute)
	c.Poll(now)
	fillHistory(t, c, &now)
	if delta := p.images.Load() - first; delta < 1 || delta > 3 {
		t.Fatalf("rolling view downloaded whole history: %d", delta)
	}
	for _, f := range c.frames {
		if f.Time.Nanosecond() != 123000000 {
			t.Fatal("rounded observation timestamp")
		}
	}
}
func TestControllerCloseAndMoveKeepOneWorker(t *testing.T) {
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	p := &fixtureProvider{timeline: func(context.Context, time.Time) (Timeline, error) {
		close(started)
		<-release
		return testTimeline(now), nil
	}}
	c := NewController(p, false, nil)
	c.Open(radarView(t, 42, -71), now)
	<-started
	for i := 0; i < 20; i++ {
		c.Close(now)
		c.Open(radarView(t, 40, -74-float64(i)/100), now)
	}
	if p.metadata.Load() != 1 {
		t.Fatal("canceled callback multiplied workers")
	}
	c.Close(now)
	close(release)
	settle(t, c, now)
	if !c.timeline.FetchedAt().IsZero() || c.bytes != 0 {
		t.Fatal("canceled result entered cache")
	}
}
func TestControllerSupersededImageAndShutdown(t *testing.T) {
	now := time.Now().UTC()
	started := make(chan struct{})
	release := make(chan struct{})
	p := &fixtureProvider{frame: func(ctx context.Context, _ Timeline, _ time.Time, _ View, _ time.Time) ([]byte, error) {
		close(started)
		<-release
		return []byte("old-view"), nil
	}}
	c := NewController(p, false, nil)
	c.Open(radarView(t, 42, -71), now)
	settle(t, c, now)
	now = now.Add(requestSpacing)
	c.Poll(now)
	<-started
	c.Open(radarView(t, 35, -90), now)
	c.Shutdown()
	close(release)
	settle(t, c, now)
	if c.bytes != 0 || c.frames != nil || p.images.Load() != 1 {
		t.Fatal("shutdown accepted obsolete image")
	}
}
func TestControllerFailureBackoffOfflineCoverageAndStale(t *testing.T) {
	now := time.Now().UTC()
	p := &fixtureProvider{timeline: func(context.Context, time.Time) (Timeline, error) { return Timeline{}, errors.New("provider down") }}
	c := NewController(p, false, nil)
	c.Open(radarView(t, 42, -71), now)
	settle(t, c, now)
	for i := 0; i < 30; i++ {
		c.Close(now)
		c.Open(radarView(t, 40, -74-float64(i)/10), now)
		c.Poll(now)
	}
	if p.metadata.Load() != 1 {
		t.Fatal("view changes bypassed backoff")
	}
	c.Poll(now.Add(retryInterval))
	settle(t, c, now.Add(retryInterval))
	if p.metadata.Load() != 2 {
		t.Fatal("did not retry after backoff")
	}
	offline := NewController(p, true, nil)
	offline.Open(radarView(t, 42, -71), now)
	if _, s := offline.Since(nil, now); s.Status != "offline" || p.metadata.Load() != 2 {
		t.Fatal(s)
	}
	p = &fixtureProvider{}
	c = NewController(p, false, nil)
	c.Open(radarView(t, 35, 139), now)
	settle(t, c, now)
	if _, s := c.Since(nil, now); s.Status != "unsupported" || p.images.Load() != 0 || c.NextWake(now) < time.Minute {
		t.Fatal("unsupported view fetched or hot-polled", s)
	}
	c.Open(radarView(t, 42, -71), now)
	fillHistory(t, c, &now)
	c.offline = true
	if _, s := c.Since(nil, now); s.Status != "stale" {
		t.Fatal(s)
	}
	if _, s := c.Since(nil, now.Add(ExpireAfter+time.Minute)); s.Status != "unavailable" || len(s.Frames) > 0 {
		t.Fatal("expired images remained visible", s)
	}
	if _, s := c.Since(nil, now.Add(-time.Hour)); s.Status != "unavailable" || len(s.Frames) > 0 {
		t.Fatal("clock rollback showed future observations", s)
	}
}
func TestControllerCacheAndChunkBounds(t *testing.T) {
	now := time.Now().UTC()
	p := &fixtureProvider{frame: func(context.Context, Timeline, time.Time, View, time.Time) ([]byte, error) {
		return bytes.Repeat([]byte{42}, MaxImageBytes), nil
	}}
	c := NewController(p, false, nil)
	c.Open(radarView(t, 42, -71), now)
	fillHistory(t, c, &now)
	if c.bytes > CacheBytes || p.images.Load() > CacheBytes/MaxImageBytes {
		t.Fatal("image cache exceeded bound", c.bytes, p.images.Load())
	}
	_, s := c.Since(nil, now)
	if s.Error != "history_limited" || s.Frames[len(s.Frames)-1].State != "ready" {
		t.Fatal("latest frame missing on limited history", s)
	}
	id := s.Frames[len(s.Frames)-1].ID
	for _, offset := range []int{-1, 1, MaxImageBytes + ImageChunkBytes} {
		if _, _, e := c.Chunk(id, offset, now); e == nil {
			t.Fatal("invalid offset accepted", offset)
		}
	}
	if _, _, e := c.Chunk("wrong", 0, now); e == nil {
		t.Fatal("unknown image accepted")
	}
	totalBytes := 0
	for offset := 0; offset < MaxImageBytes; offset += ImageChunkBytes {
		b, total, e := c.Chunk(id, offset, now)
		if e != nil || total != MaxImageBytes || len(b) > ImageChunkBytes {
			t.Fatal("chunk bound", e)
		}
		totalBytes += len(b)
	}
	if totalBytes != MaxImageBytes {
		t.Fatal("truncated image")
	}
}
func TestCenteredViewBounds(t *testing.T) {
	for _, coord := range [][2]float64{{42, -71}, {85, 180}, {-85, -180}} {
		v, e := CenteredView(coord[0], coord[1], 4)
		if e != nil || !v.valid() {
			t.Fatal(v, e)
		}
	}
	for _, zoom := range []int{3, 11} {
		if _, e := CenteredView(42, -71, zoom); e == nil {
			t.Fatal("invalid zoom")
		}
	}
	if _, e := CenteredView(90, 0, 7); e == nil {
		t.Fatal("invalid latitude")
	}
}

func BenchmarkControllerUnchanged(b *testing.B) {
	for _, open := range []bool{false, true} {
		name := "closed"
		if open {
			name = "current"
		}
		b.Run(name, func(b *testing.B) {
			now := time.Now().UTC()
			c := NewController(&fixtureProvider{}, false, nil)
			if open {
				c.open = true
				c.view, _ = CenteredView(42, -71, 7)
				c.timeline = testTimeline(now)
				c.reconcileFrames()
				for i := range c.frames {
					c.frames[i].State = "ready"
					c.frames[i].body = []byte("image")
				}
				c.nextMetadata = now.Add(refreshInterval)
				c.legendID = "legend"

			}
			revision, _ := c.Since(nil, now)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, p := c.Since(&revision, now); p != nil {
					b.Fatal("unchanged event")
				}
			}
		})
	}
}

func TestControllerMissingFrameIsExplicitAndRefreshRetries(t *testing.T) {
	now := testNow
	fail := true
	p := &fixtureProvider{frame: func(context.Context, Timeline, time.Time, View, time.Time) ([]byte, error) {
		if fail {
			return nil, ErrUnavailable
		}
		return []byte("frame"), nil
	}}
	c := NewController(p, false, nil)
	c.Open(testView, now)
	settle(t, c, now)
	now = now.Add(requestSpacing)
	c.Poll(now)
	settle(t, c, now)
	if c.frames[len(c.frames)-1].State != "failed" {
		t.Fatal("missing observation not marked")
	}
	count := p.images.Load()
	now = now.Add(30 * time.Second)
	c.Poll(now)
	if p.images.Load() != count {
		t.Fatal("failed image bypassed backoff")
	}
	fail = false
	now = now.Add(2 * time.Minute)
	c.Poll(now)
	fillHistory(t, c, &now)
	if _, state := c.Since(nil, now); state.Status != "current" {
		t.Fatal("refresh did not recover", state)
	}
}

func TestControllerLatestOnlyUntilExplicitHistoryDemand(t *testing.T) {
	now := testNow
	p := &fixtureProvider{}
	c := NewController(p, false, nil)
	if err := c.SetHistory(true, now); err == nil {
		t.Fatal("closed controller accepted history demand")
	}
	if err := c.Open(testView, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		settle(t, c, now)
		now = now.Add(requestSpacing)
		c.Poll(now)
	}
	_, state := c.Since(nil, now)
	if p.images.Load() != 1 || p.legends.Load() != 1 || len(state.Frames) != CachedFrames || state.Frames[len(state.Frames)-1].State != "ready" {
		t.Fatalf("paused initial radar downloaded history: calls=%d/%d state=%+v", p.images.Load(), p.legends.Load(), state)
	}
	for _, f := range state.Frames[:len(state.Frames)-1] {
		if f.State != "pending" {
			t.Fatal("available history metadata lost", f)
		}
	}
	if c.active || c.NextWake(now) != time.Minute {
		t.Fatal("paused pending history caused hot polling", c.NextWake(now))
	}
	fillHistory(t, c, &now)
	if p.images.Load() != CachedFrames {
		t.Fatal("explicit history did not fill available frames", p.images.Load())
	}
	if err := c.SetHistory(false, now); err != nil {
		t.Fatal(err)
	}
	c.Close(now)
	if err := c.Open(testView, now); err != nil {
		t.Fatal(err)
	}
	if c.history || c.active || p.images.Load() != CachedFrames || c.bytes == 0 {
		t.Fatal("reopen restored history demand or discarded useful cache")
	}
	if err := c.SetHistory(true, now); err != nil {
		t.Fatal(err)
	}
	c.Open(radarView(t, 40, -74), now)
	if c.history {
		t.Fatal("view change retained history demand")
	}
	for i := 0; i < 10; i++ {
		settle(t, c, now)
		now = now.Add(requestSpacing)
		c.Poll(now)
	}
	if p.images.Load() != CachedFrames+1 {
		t.Fatal("new view fetched older history without interaction", p.images.Load())
	}
}

func TestControllerDisableHistoryCancelsOnlyOlderFrame(t *testing.T) {
	now := testNow
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	p := &fixtureProvider{frame: func(ctx context.Context, timeline Timeline, at time.Time, _ View, _ time.Time) ([]byte, error) {
		if at.Equal(timeline.Latest()) {
			return []byte("latest"), nil
		}
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, ctx.Err()
	}}
	c := NewController(p, false, nil)
	c.Open(testView, now)
	for i := 0; i < 8; i++ {
		settle(t, c, now)
		now = now.Add(requestSpacing)
		c.Poll(now)
	}
	c.SetHistory(true, now)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("history worker did not start")
	}
	if err := c.SetHistory(false, now); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("disabling history did not cancel older image")
	}
	settle(t, c, now)
	if c.active || c.errorCode != "" || c.frames[len(c.frames)-1].State != "ready" || p.images.Load() != 2 {
		t.Fatal("canceled history damaged latest radar or initiated more work")
	}
	if c.frames[len(c.frames)-2].State != "pending" {
		t.Fatal("canceled history cannot be requested later")
	}
}

func TestControllerDisableHistoryPreservesLatestWork(t *testing.T) {
	now := testNow
	started := make(chan struct{})
	release := make(chan struct{})
	p := &fixtureProvider{frame: func(ctx context.Context, _ Timeline, _ time.Time, _ View, _ time.Time) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return []byte("latest"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	c := NewController(p, false, nil)
	c.Open(testView, now)
	settle(t, c, now)
	now = now.Add(requestSpacing)
	c.Poll(now)
	<-started
	c.SetHistory(true, now)
	generation := c.generation
	c.SetHistory(false, now)
	if c.generation != generation || !c.active {
		t.Fatal("disabling history canceled newest observation")
	}
	close(release)
	settle(t, c, now)
	if c.frames[len(c.frames)-1].State != "ready" {
		t.Fatal("latest work failed after disabling history")
	}
}
