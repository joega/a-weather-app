package precipitation

import (
	"context"
	"errors"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

const (
	RefreshAfter      = 30 * time.Minute
	ExpireAfter       = 6 * time.Hour
	ClosedRetention   = 15 * time.Minute
	retryAfter        = time.Minute
	requestSpacing    = 2 * time.Second
	requestsPerMinute = 6
)

// Dependencies are immutable. Load/Save run on the serialized owner and must
// use bounded local storage; nil callbacks disable persistence. Returned data
// transfers ownership. Fetch must honor cancellation; Notify must be safe on a
// worker and nonblocking. The controller never creates a periodic timer.
type Dependencies struct {
	Fetch   func(context.Context, weather.Object, time.Time) (Data, error)
	Load    func() (*Data, error)
	Save    func(Data) error
	Notify  func()
	Offline bool
}

type Presentation struct {
	Status     string
	Refreshing bool
	Error      string
	SaveFailed bool
	RetryAt    time.Time
	Data       *Data
}

type completion struct {
	generation uint64
	at         time.Time
	data       Data
	err        error
}

// Controller methods must be serialized by their owner. At most one callback
// and one result slot exist, including when a canceled callback is slow to exit.
// A single place is retained. No work or cache read occurs before Open.
type Controller struct {
	deps                           Dependencies
	location                       weather.Object
	data                           *Data
	results                        chan completion
	open, active, stopped          bool
	generation, revision           uint64
	cancel                         context.CancelFunc
	nextAttempt, closedAt, lastNow time.Time
	attempts                       [requestsPerMinute]time.Time
	errorCode, status              string
	saveFailed                     bool
}

func NewController(deps Dependencies) *Controller {
	return &Controller{deps: deps, results: make(chan completion, 1)}
}

func (c *Controller) Open(location weather.Object, now time.Time) error {
	l, err := weather.ValidateLocation(location)
	if err != nil || now.IsZero() || c.stopped {
		return errors.New("invalid precipitation demand")
	}
	// Poll while closed to reclaim expired retention before considering reuse.
	// While already open, adopt results only after validating the new identity.
	if !c.open {
		c.Poll(now)
	}
	changed := !sameLocation(c.location, l)
	if changed {
		c.cancelWork()
		c.data = nil
		c.errorCode, c.saveFailed = "", false
	}
	opening := !c.open || changed
	c.location, c.open, c.closedAt = l, true, time.Time{}
	if opening && c.data == nil && c.deps.Load != nil {
		data, loadErr := c.deps.Load()
		if loadErr != nil {
			c.errorCode = "cache_unavailable"
		} else if data != nil && data.Validate() == nil && matches(*data, l) && usable(*data, now) {
			c.data = data
		}
	}
	c.revision++
	c.Poll(now)
	return nil
}

func (c *Controller) Close(now time.Time) {
	if c.open {
		c.open, c.closedAt = false, now
		c.cancelWork()
		c.revision++
	}
}

func (c *Controller) Shutdown() {
	c.stopped, c.open, c.data = true, false, nil
	c.cancelWork()
	c.revision++
}

func (c *Controller) cancelWork() {
	c.generation++
	if c.cancel != nil {
		c.cancel()
	}
	// Do not release the active slot until its completion has been drained.
}

func (c *Controller) Poll(now time.Time) {
	if now.IsZero() {
		return
	}
	if !c.lastNow.IsZero() && now.Before(c.lastNow) {
		// A clock correction must not freeze retries until the old wall time,
		// nor permit a burst. Freshness still checks the original retrieval.
		c.attempts = [requestsPerMinute]time.Time{}
		c.nextAttempt = now.Add(requestSpacing)
		if c.errorCode == "fetch_failed" {
			c.nextAttempt = now.Add(retryAfter)
		}
	}
	c.lastNow = now
	select {
	case r := <-c.results:
		c.active, c.cancel = false, nil
		c.revision++
		if !c.stopped && c.open && r.generation == c.generation {
			if r.err != nil || r.data.Validate() != nil || !r.data.FetchedAt.Equal(r.at) || !matches(r.data, c.location) || !usable(r.data, now) {
				c.errorCode, c.nextAttempt = "fetch_failed", now.Add(retryAfter)
			} else {
				c.data, c.errorCode, c.saveFailed = &r.data, "", false
				if c.deps.Save != nil {
					c.saveFailed = c.deps.Save(clone(r.data)) != nil
				}
			}
		}
	default:
	}
	if !c.open && !c.closedAt.IsZero() && (now.Before(c.closedAt) || now.Sub(c.closedAt) >= ClosedRetention) {
		c.data, c.closedAt = nil, time.Time{}
		c.errorCode, c.saveFailed = "", false
	}
	if c.data != nil && !usable(*c.data, now) {
		c.data = nil
		c.revision++
	}
	c.advance(now)
	status := c.currentStatus(now)
	if c.status != status {
		c.status = status
		c.revision++
	}
}

func (c *Controller) eligibleAt() time.Time {
	at := c.nextAttempt
	if c.data != nil && c.data.FetchedAt.Add(RefreshAfter).After(at) {
		at = c.data.FetchedAt.Add(RefreshAfter)
	}
	oldest := c.attempts[0]
	for _, t := range c.attempts[1:] {
		if t.Before(oldest) {
			oldest = t
		}
	}
	if !oldest.IsZero() && oldest.Add(time.Minute).After(at) {
		at = oldest.Add(time.Minute)
	}
	return at
}

func (c *Controller) advance(now time.Time) {
	if c.stopped || !c.open || c.active || c.deps.Offline || c.deps.Fetch == nil || now.Before(c.eligibleAt()) {
		return
	}
	slot := 0
	for i, t := range c.attempts {
		if t.Before(c.attempts[slot]) {
			slot = i
		}
	}
	c.attempts[slot] = now
	c.nextAttempt = now.Add(requestSpacing)
	c.active, c.errorCode = true, ""
	c.revision++
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	c.cancel = cancel
	generation, fetch, notify, results := c.generation, c.deps.Fetch, c.deps.Notify, c.results
	location := weather.Clone(c.location).(weather.Object)
	go func() {
		defer cancel()
		data, err := fetch(ctx, location, now)
		if ctx.Err() != nil {
			data, err = Data{}, ctx.Err()
		}
		results <- completion{generation: generation, at: now, data: data, err: err}
		if notify != nil {
			notify()
		}
	}()
}

// NextPoll lets the owner reuse its existing service timer for a pending retry
// or refresh. An active callback wakes the owner through Notify instead.
func (c *Controller) NextPoll(now time.Time) time.Duration {
	if !c.open || c.stopped || c.active || c.deps.Offline || c.deps.Fetch == nil {
		return 0
	}
	return max(time.Millisecond, c.eligibleAt().Sub(now))
}

func (c *Controller) currentStatus(now time.Time) string {
	if !c.open || c.stopped {
		return "closed"
	}
	if c.data != nil {
		if now.Sub(c.data.FetchedAt) >= RefreshAfter {
			return "stale"
		}
		return "fresh"
	}
	if c.active {
		return "loading"
	}
	if c.errorCode == "" && !c.deps.Offline && c.deps.Fetch != nil && now.Before(c.eligibleAt()) {
		return "waiting"
	}
	return "unavailable"
}

// Since returns a caller-owned snapshot only on a revision or freshness change.
// Closed views receive no retained dataset. Reading cannot initiate work unless
// a prior Open still demands it.
func (c *Controller) Since(last *uint64, now time.Time) (uint64, *Presentation) {
	c.Poll(now)
	if last != nil && *last == c.revision {
		return c.revision, nil
	}
	p := &Presentation{Status: c.status}
	if c.open && !c.stopped {
		p.Refreshing, p.Error, p.SaveFailed = c.active, c.errorCode, c.saveFailed
		if c.deps.Offline {
			p.Error = "offline"
		} else if c.deps.Fetch == nil {
			p.Error = "provider_unavailable"
		}
		if c.data != nil {
			d := clone(*c.data)
			p.Data = &d
		}
		if !c.active && !c.deps.Offline && c.deps.Fetch != nil {
			p.RetryAt = c.eligibleAt()
		}
	}
	return c.revision, p
}

func sameLocation(a, b weather.Object) bool {
	return a != nil && b != nil && a["latitude"] == b["latitude"] && a["longitude"] == b["longitude"] && a["timezone"] == b["timezone"]
}
func matches(d Data, l weather.Object) bool {
	return d.Latitude == l["latitude"] && d.Longitude == l["longitude"] && d.Timezone == l["timezone"]
}
func usable(d Data, now time.Time) bool {
	age := now.Sub(d.FetchedAt)
	return age >= -5*time.Minute && age < ExpireAfter
}
func clone(d Data) Data {
	d.Hours = append([]Hour(nil), d.Hours...)
	return d
}
