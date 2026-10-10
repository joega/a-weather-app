package radar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"
)

const (
	CachedFrames    = 24
	CacheBytes      = 12 * 1024 * 1024
	ImageChunkBytes = 96 * 1024
	refreshInterval = 2 * time.Minute
	retryInterval   = time.Minute
	requestSpacing  = 500 * time.Millisecond
	closedRetention = 5 * time.Minute
)

// Provider returns validated images and transfers ownership of the returned bytes.
// Calls must honor cancellation. Controller methods are serialized by the app;
// only provider calls run on its single worker. Notify must be safe on that worker.
type Provider interface {
	FetchTimeline(context.Context, time.Time) (Timeline, error)
	FetchFrame(context.Context, Timeline, time.Time, View, time.Time) ([]byte, error)
	FetchLegend(context.Context) ([]byte, error)
}

type Frame struct {
	Label string    `json:"label"`
	Time  time.Time `json:"time"`
	ID    string    `json:"id"`
	State string    `json:"state"`
}
type Presentation struct {
	ClientToken int       `json:"client_token"`
	LatestLabel string    `json:"latest_label"`
	Status      string    `json:"status"`
	Refreshing  bool      `json:"refreshing"`
	Error       string    `json:"error"`
	Frames      []Frame   `json:"frames"`
	Legend      string    `json:"legend"`
	View        View      `json:"view"`
	Latest      time.Time `json:"latest"`
}
type cachedFrame struct {
	Frame
	body []byte
}
type completion struct {
	generation uint64
	kind       string
	at         time.Time
	timeline   Timeline
	body       []byte
	err        error
}

type Controller struct {
	provider                            Provider
	notify                              func()
	results                             chan completion
	open, active, offline, history      bool
	activeKind                          string
	activeAt                            time.Time
	view                                View
	generation, revision                uint64
	cancel                              context.CancelFunc
	timeline                            Timeline
	frames                              []cachedFrame
	legend                              []byte
	legendID                            string
	bytes                               int
	errorCode                           string
	nextMetadata, nextRequest, closedAt time.Time
	status                              string
}

func NewController(provider Provider, offline bool, notify func()) *Controller {
	return &Controller{provider: provider, offline: offline, notify: notify, results: make(chan completion, 1)}
}

// CenteredView covers a 512-pixel XYZ viewport. Zoom limits bound requests and
// do not imply radar detail beyond the provider's roughly kilometre-scale grid.
func CenteredView(lat, lon float64, zoom int) (View, error) {
	if !finite(lat) || !finite(lon) || lat < -85 || lat > 85 || lon < -180 || lon > 180 || zoom < 4 || zoom > 10 {
		return View{}, fmt.Errorf("invalid radar view")
	}
	half := 2 * mercatorExtent / math.Exp2(float64(zoom))
	x := math.Max(-mercatorExtent+half, math.Min(mercatorExtent-half, lon/180*mercatorExtent))
	y := math.Max(-mercatorExtent+half, math.Min(mercatorExtent-half, math.Asinh(math.Tan(lat*math.Pi/180))/math.Pi*mercatorExtent))
	return View{x - half, y - half, x + half, y + half}, nil
}

func (c *Controller) Open(view View, now time.Time) error {
	if !view.valid() || now.IsZero() {
		return fmt.Errorf("invalid radar demand")
	}
	c.history = false
	if c.open && c.view == view {
		c.cancelHistoryWork()
	}
	if !c.open {
		c.Poll(now)
	}
	if c.view != view {
		c.cancelWork()
		c.view = view
		c.clearFrames()
		// A validated manifest and legend may be reused across view changes, but
		// imagery never crosses view generations. Provider backoff remains global.
	}
	c.open = true
	c.closedAt = time.Time{}
	c.revision++
	c.advance(now)
	return nil
}

// SetHistory admits older observations only after explicit timeline interaction.
// Disabling demand preserves ready images and leaves metadata/latest work intact.
func (c *Controller) SetHistory(enabled bool, now time.Time) error {
	if !c.open || now.IsZero() {
		return fmt.Errorf("radar history unavailable")
	}
	if c.history == enabled {
		return nil
	}
	c.history = enabled
	if !enabled {
		c.cancelHistoryWork()
	}
	c.revision++
	c.advance(now)
	return nil
}

func (c *Controller) cancelHistoryWork() {
	if c.active && c.activeKind == "frame" && len(c.frames) > 0 && !c.activeAt.Equal(c.frames[len(c.frames)-1].Time) {
		c.cancelWork()
	}
}

func (c *Controller) Close(now time.Time) {
	if c.open {
		c.closedAt = now
		c.open = false
		c.history = false
		c.cancelWork()
		c.revision++
	}
}
func (c *Controller) Shutdown() {
	c.open = false
	c.history = false
	c.cancelWork()
	c.clearFrames()
	c.legend = nil
	c.legendID = ""
	c.timeline = Timeline{}
}
func (c *Controller) cancelWork() {
	c.generation++
	if c.cancel != nil {
		c.cancel()
	}
	// Keep the slot occupied until the old callback returns, even if it ignores
	// cancellation. Rapid open/close cannot multiply workers or pending results.
}
func (c *Controller) clearFrames() { c.frames = nil; c.bytes = 0 }
func (c *Controller) Poll(now time.Time) {
	select {
	case r := <-c.results:
		c.active = false
		c.cancel = nil
		c.activeKind = ""
		c.activeAt = time.Time{}
		if r.generation == c.generation && c.open {
			c.accept(r, now)
			c.revision++
		}
	default:
	}
	if !c.open && !c.closedAt.IsZero() && (now.Before(c.closedAt) || now.Sub(c.closedAt) >= closedRetention) {
		c.clearFrames()
		c.legend = nil
		c.legendID = ""
		c.timeline = Timeline{}
		c.closedAt = time.Time{}
	}
	c.advance(now)
	status := c.currentStatus(now)
	if status != c.status {
		c.status = status
		c.revision++
	}
}
func (c *Controller) accept(r completion, now time.Time) {
	if r.err != nil {
		if r.kind == "frame" {
			for i := range c.frames {
				if c.frames[i].Time.Equal(r.at) {
					c.frames[i].State = "failed"
					break
				}
			}
		}
		c.errorCode = "fetch_failed"
		c.nextRequest = now.Add(retryInterval)
		if r.kind == "metadata" {
			c.nextMetadata = c.nextRequest
		}
		return
	}
	switch r.kind {
	case "metadata":
		if r.timeline.FetchedAt().IsZero() || r.timeline.Freshness(now) == "unavailable" {
			c.errorCode = "unavailable"
			c.nextMetadata = now.Add(retryInterval)
			return
		}
		c.timeline = r.timeline
		c.nextMetadata = now.Add(refreshInterval)
		c.errorCode = ""
		c.reconcileFrames()
	case "frame":
		if len(r.body) == 0 || len(r.body) > MaxImageBytes {
			c.errorCode = "invalid_image"
			c.nextRequest = now.Add(retryInterval)
			return
		}
		for i := range c.frames {
			f := &c.frames[i]
			if f.Time.Equal(r.at) {
				if c.bytes+len(r.body) > CacheBytes {
					f.State = "limited"
					c.errorCode = "history_limited"
					return
				}
				f.body = r.body
				f.ID = imageID(c.view, r.at, r.body)
				f.State = "ready"
				c.bytes += len(r.body)
				break
			}
		}
		if c.errorCode == "fetch_failed" {
			c.errorCode = ""
		}
	case "legend":
		if len(r.body) == 0 || len(r.body) > MaxLegendBytes {
			c.errorCode = "invalid_image"
			c.nextRequest = now.Add(retryInterval)
			return
		}
		c.legend = r.body
		c.legendID = imageID(View{}, time.Time{}, r.body)
		if c.errorCode == "fetch_failed" {
			c.errorCode = ""
		}
	}
}
func imageID(view View, at time.Time, body []byte) string {
	h := sha256.New()
	fmt.Fprintf(h, "%v/%s/", view, at.UTC().Format(time.RFC3339Nano))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// Keep the last advertised observation per five-minute bucket, then the latest
// 24 buckets. Stable buckets reuse bytes as the manifest advances; no invented
// or rounded observation time is sent to NOAA or shown to the user.
func (c *Controller) reconcileFrames() {
	times := c.timeline.Times()
	selected := make([]time.Time, 0, CachedFrames+1)
	for _, at := range times {
		if len(selected) > 0 && selected[len(selected)-1].Unix()/300 == at.Unix()/300 {
			selected[len(selected)-1] = at
		} else {
			selected = append(selected, at)
		}
	}
	if len(selected) > CachedFrames {
		selected = selected[len(selected)-CachedFrames:]
	}
	frames := make([]cachedFrame, 0, len(selected))
	total := 0
	for _, at := range selected {
		f := cachedFrame{Frame: Frame{Time: at, State: "pending"}}
		for _, old := range c.frames {
			if old.Time.Equal(at) {
				f = old
				if f.State == "failed" {
					f.State = "pending"
				}
				break
			}
		}
		if f.State == "limited" {
			c.errorCode = "history_limited"
		}
		frames = append(frames, f)
		total += len(f.body)
	}
	c.frames = frames
	c.bytes = total
}
func (c *Controller) advance(now time.Time) {
	if !c.open || c.active || c.offline || c.provider == nil || now.IsZero() || now.Before(c.nextRequest) {
		return
	}
	if c.timeline.FetchedAt().IsZero() || !now.Before(c.nextMetadata) || now.Before(c.timeline.FetchedAt().Add(-time.Minute)) {
		c.start("metadata", time.Time{}, now)
		return
	}
	if !c.view.intersects(c.timeline.Coverage()) || c.timeline.Freshness(now) == "unavailable" {
		return
	}
	if len(c.frames) == 0 {
		c.reconcileFrames()
	}
	// Show the newest image and legend first. Older history needs explicit demand.
	for i := len(c.frames) - 1; i >= 0; i-- {
		f := &c.frames[i]
		if f.State != "pending" || (i < len(c.frames)-1 && !c.history) {
			continue
		}
		if i < len(c.frames)-1 && c.legendID == "" {
			c.start("legend", time.Time{}, now)
			return
		}
		// Reserve a full maximum-sized response. Older frames yield to newer ones.
		for c.bytes > CacheBytes-MaxImageBytes {
			evicted := false
			for j := 0; j < i; j++ {
				if len(c.frames[j].body) > 0 {
					c.bytes -= len(c.frames[j].body)
					c.frames[j].body = nil
					c.frames[j].ID = ""
					c.frames[j].State = "limited"
					evicted = true
					break
				}
			}
			if !evicted {
				f.State = "limited"
				c.errorCode = "history_limited"
				c.revision++
				break
			}
		}
		if f.State == "pending" {
			c.start("frame", f.Time, now)
			return
		}
	}
	if c.legendID == "" {
		c.start("legend", time.Time{}, now)
	}
}
func (c *Controller) start(kind string, at, now time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	c.cancel = cancel
	c.active = true
	c.activeKind = kind
	c.activeAt = at
	c.nextRequest = now.Add(requestSpacing)
	c.revision++
	generation, provider, timeline, view := c.generation, c.provider, c.timeline, c.view
	go func() {
		defer cancel()
		r := completion{generation: generation, kind: kind, at: at}
		switch kind {
		case "metadata":
			r.timeline, r.err = provider.FetchTimeline(ctx, now)
		case "frame":
			r.body, r.err = provider.FetchFrame(ctx, timeline, at, view, now)
		case "legend":
			r.body, r.err = provider.FetchLegend(ctx)
		}
		if ctx.Err() != nil {
			r.body = nil
			r.err = ctx.Err()
		}
		c.results <- r
		if c.notify != nil {
			c.notify()
		}
	}()
}
func (c *Controller) currentStatus(now time.Time) string {
	if !c.open {
		return "closed"
	}
	if c.timeline.FetchedAt().IsZero() {
		if c.active {
			return "loading"
		}
		if c.offline {
			return "offline"
		}
		return "unavailable"
	}
	if !c.view.intersects(c.timeline.Coverage()) {
		return "unsupported"
	}
	s := c.timeline.Freshness(now)
	if s == "unavailable" {
		return s
	}
	ready := false
	for _, f := range c.frames {
		ready = ready || len(f.body) > 0
	}
	if !ready {
		if c.active {
			return "loading"
		}
		if !c.offline && c.errorCode == "" {
			for _, f := range c.frames {
				if f.State == "pending" {
					return "loading"
				}
			}
		}
		return "unavailable"
	}
	if c.offline || c.errorCode == "fetch_failed" || c.errorCode == "unavailable" {
		return "stale"
	}
	return s
}

// Since returns only small metadata. Encoded imagery never enters snapshots.
func (c *Controller) Since(last *uint64, now time.Time) (uint64, *Presentation) {
	c.Poll(now)
	if last != nil && *last == c.revision {
		return c.revision, nil
	}
	p := &Presentation{Status: c.status, Refreshing: c.open && c.active, Error: c.errorCode, Frames: []Frame{}, View: c.view, Latest: c.timeline.Latest()}
	if c.open && (c.status == "current" || c.status == "stale" || c.status == "loading") {
		for _, f := range c.frames {
			p.Frames = append(p.Frames, f.Frame)
		}
		p.Legend = c.legendID
	}
	return c.revision, p
}

// Chunk serves only existing current-view images; it never initiates a fetch.
// The caller must encode/copy the borrowed bytes before the next controller call.
func (c *Controller) Chunk(id string, offset int, now time.Time) ([]byte, int, error) {
	if !c.open || offset < 0 || offset%ImageChunkBytes != 0 || c.timeline.Freshness(now) == "unavailable" {
		return nil, 0, ErrUnavailable
	}
	var body []byte
	if id != "" && id == c.legendID {
		body = c.legend
	} else {
		for _, f := range c.frames {
			if f.ID != "" && f.ID == id {
				body = f.body
				break
			}
		}
	}
	if len(body) == 0 || offset >= len(body) {
		return nil, 0, ErrUnavailable
	}
	end := min(offset+ImageChunkBytes, len(body))
	return body[offset:end], len(body), nil
}

// NextWake bounds foreground scheduling; closed caches need only the app's
// ordinary minute tick, and no provider work is initiated after close.
func (c *Controller) NextWake(now time.Time) time.Duration {
	if !c.open || c.offline || c.provider == nil {
		return time.Minute
	}
	if c.active {
		return time.Minute
	} // Completion signals the owner.
	next := c.nextMetadata
	if c.view.intersects(c.timeline.Coverage()) && c.timeline.Freshness(now) != "unavailable" {
		for i, f := range c.frames {
			if f.State == "pending" && (c.history || i == len(c.frames)-1) {
				next = c.nextRequest
				break
			}
		}
		if c.legendID == "" {
			next = c.nextRequest
		}
	}
	if next.Before(c.nextRequest) {
		next = c.nextRequest
	}
	return max(100*time.Millisecond, min(time.Minute, next.Sub(now)))
}
