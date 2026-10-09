package app

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

// Only the viewed and primary points stay decoded. They share this object when
// their identities match; saved list entries carry summaries, not extra workers.
// All fields are owned by App.mu, including pending work and generations.
type forecastPoint struct {
	needsResolve, resolving        bool
	id                             string
	location, forecast, profile    M
	mode                           string
	zip, country                   any
	place                          M
	alertKey                       string
	alerts                         M
	alertRefresh                   bool
	errorCode, locationError       any
	nextFetch                      time.Time
	fetchCancel                    context.CancelFunc
	fetchBusy, locationBusy        bool
	generation, forecastGeneration uint64
	pending                        *forecastWork
}

// A canceled worker retains its slot until it returns. Two forecast/resolution
// workers are the hard limit, even if a callback takes time to honor cancellation.
// Production alerts use a separate shared single-worker scheduler; the legacy
// injected alert hook can accompany each forecast. Each consumer has one replaceable
// pending request. Publication requires both point ownership and generation.
type forecastWork struct {
	key                        string
	point                      *forecastPoint
	generation                 uint64
	selection, location, place M
	country                    any
	makePrimary                bool
	ctx                        context.Context
	cancel                     context.CancelFunc
}

func forecastWorkKey(p *forecastPoint, selection M) string {
	if selection == nil {
		return p.id
	}
	switch selection["mode"] {
	case "auto":
		return "current"
	case "zip":
		return "zip-" + stringOf(selection["zip_code"])
	case "place":
		id, _ := selection["place_id"].(float64)
		return "place-" + strconv.FormatFloat(id, 'f', 0, 64)
	default:
		return p.id
	}
}

func (a *App) cancelPointFetch(p *forecastPoint) {
	if p == nil {
		return
	}
	if p.fetchCancel != nil {
		p.fetchCancel()
	}
	if p.locationBusy && p.resolving {
		p.needsResolve = true
	}
	if p.fetchBusy || p.locationBusy {
		p.nextFetch = a.options.Now()
	}
	a.workGeneration++
	p.generation = a.workGeneration
	p.fetchCancel, p.pending = nil, nil
	p.fetchBusy, p.locationBusy = false, false
}

func (a *App) beginFetch(selection M) {
	if selection != nil {
		a.beginLocation(selection, true)
		return
	}
	a.beginPointFetch(a.forecastPoint, nil, false)
}

// Legacy set_location explicitly changes the primary city. The new add action
// only browses it, except when replacing the unchosen default on first use.
func (a *App) beginLocation(selection M, primary bool) {
	if a.closed || a.options.Offline {
		return
	}
	if len(a.saved.doc["places"].([]any)) == 1 && a.primary.mode == "default" {
		primary = true
	}
	previous := a.forecastPoint
	if previous != a.primary {
		a.cancelPointFetch(previous)
	}
	// Keep immutable last-good trees, but allocate a fresh request owner. Old
	// selection callbacks may still be unwinding when the user picks again.
	copy := *previous
	copy.pending, copy.fetchCancel = nil, nil
	copy.fetchBusy, copy.locationBusy = false, false
	a.forecastPoint = &copy
	a.cancelAirQuality()
	a.beginPointFetch(a.forecastPoint, selection, primary)
}

func (a *App) beginPointFetch(p *forecastPoint, selection M, primary bool) {
	if a.closed || a.options.Offline || p == nil {
		return
	}
	if selection == nil && (p.fetchBusy || p.locationBusy) {
		return
	}
	a.cancelPointFetch(p)
	if selection == nil && p.needsResolve {
		selection = M{"mode": "auto", "zip_code": nil}
	}
	p.resolving = selection["mode"] == "auto"
	p.needsResolve = false
	ctx, cancel := context.WithCancel(context.Background())
	p.fetchCancel = cancel
	p.fetchBusy, p.locationBusy = selection == nil, selection != nil
	p.nextFetch = a.options.Now().Add(weather.RefreshSeconds * time.Second)
	if selection != nil {
		p.locationError = nil
	}
	p.pending = &forecastWork{key: forecastWorkKey(p, selection), point: p, generation: p.generation, selection: safeio.Clone(selection), location: safeio.Clone(p.location), country: p.country, place: safeio.Clone(p.place), makePrimary: primary, ctx: ctx, cancel: cancel}
	if selection == nil {
		p.alertRefresh = true
	}
	a.startForecastWork()
	a.signal()
}

func (a *App) startForecastWork() {
	if a.closed {
		return
	}
	for _, p := range []*forecastPoint{a.forecastPoint, a.primary} {
		if len(a.forecastJobs) >= 2 {
			return
		}
		if p == nil || p.pending == nil {
			continue
		}
		// Do not overlap refreshes of one point, including a canceled request.
		busy := false
		for work := range a.forecastJobs {
			if work.point == p || work.key == p.pending.key {
				busy = true
			}
		}
		if busy {
			continue
		}
		work := p.pending
		p.pending = nil
		a.forecastJobs[work] = true
		go a.runForecastWork(work)
	}
}

func (a *App) runForecastWork(work *forecastWork) {
	defer func() { a.forecastDone <- work; a.signal() }()
	defer work.cancel()
	ctx, cancel := context.WithTimeout(work.ctx, 25*time.Second)
	defer cancel()
	location, country, place := work.location, work.country, work.place
	var err error
	if work.selection != nil {
		selection := safeio.Clone(work.selection)
		if selection["mode"] == "auto" {
			delete(selection, "zip_code")
		}
		if a.options.ResolveSelection != nil {
			var resolved M
			resolved, err = a.options.ResolveSelection(ctx, selection)
			if err == nil {
				location, err = weather.ValidateLocation(object(resolved["location"]))
				country, place = resolved["country_code"], object(resolved["place"])
				if !validCountry(country) {
					err = errors.New("invalid resolved country")
				}
			}
		} else {
			location, err = a.options.Resolve(ctx, selection)
			if err == nil {
				location, err = weather.ValidateLocation(location)
			}
			country, place = profileIdentity(nil, stringOf(selection["mode"]))
		}
	}
	var alertResults chan M
	if err == nil && ctx.Err() == nil && country == "US" && a.options.FetchAlerts != nil && a.options.FetchAlertMessages == nil {
		// No waiting goroutine or unbounded queue when canceled alert providers
		// still occupy both slots. Forecast publication remains independent.
		select {
		case a.alertSlots <- struct{}{}:
			alertResults = make(chan M, 1)
			results, alertLocation := alertResults, safeio.Clone(location)
			go func() {
				defer func() { <-a.alertSlots }()
				alerts, e := a.options.FetchAlerts(ctx, alertLocation, a.options.Now())
				if e != nil || weather.ValidateAlerts(alerts) != nil {
					alerts = weather.UnavailableAlerts()
				}
				results <- alerts
			}()
		default:
		}
	}
	var forecast M
	if err == nil && ctx.Err() == nil {
		if a.options.FetchCountry != nil {
			forecast, err = a.options.FetchCountry(ctx, location, a.options.Now(), stringOf(country))
		} else {
			forecast, err = a.options.Fetch(ctx, location, a.options.Now())
		}
		if err == nil {
			err = weather.ValidateSnapshot(forecast, location)
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && country == "US" && a.options.FetchAlerts != nil && a.options.FetchAlertMessages == nil && alertResults == nil {
		forecast["alerts"] = weather.UnavailableAlerts()
	}
	c := completion{point: work.point, generation: work.generation, makePrimary: work.makePrimary, selection: work.selection, location: location, forecast: forecast, country: country, place: place, err: err, alertsPending: err == nil && alertResults != nil}
	a.sendForecastCompletion(ctx, c)
	if err == nil && alertResults != nil {
		alerts := weather.UnavailableAlerts()
		select {
		case alerts = <-alertResults:
		case <-ctx.Done():
		}
		a.sendForecastCompletion(ctx, completion{point: work.point, generation: work.generation, location: location, alerts: alerts})
	}
}

func (a *App) sendForecastCompletion(ctx context.Context, c completion) {
	select {
	case a.results <- c:
		a.signal()
	case <-ctx.Done():
		select {
		case a.results <- c:
			a.signal()
		default:
		}
	}
}

func (a *App) poll() {
	defer a.tickAlertWork()
	a.pollUpdates()
	defer a.pollAirQuality()
	defer a.pollMap()
	defer a.pollRadar()
	a.pollSearch()
	// Completions precede the done marker. Drain them first so a new request
	// cannot reuse a consumer until its old publication has been considered.
	for {
		select {
		case c := <-a.results:
			a.applyForecastCompletion(c)
		default:
			goto finished
		}
	}
finished:
	for {
		select {
		case work := <-a.forecastDone:
			delete(a.forecastJobs, work)
		default:
			a.startForecastWork()
			return
		}
	}
}

func (a *App) applyForecastCompletion(c completion) {
	p := c.point
	if p == nil {
		return
	}
	if a.closed || (p != a.forecastPoint && p != a.primary) || c.generation != p.generation {
		return
	}
	alertOnly := c.alerts != nil
	if alertOnly {
		if p.forecastGeneration != c.generation || !reflect.DeepEqual(c.location, p.location) {
			return
		}
		c.forecast = safeio.Clone(p.forecast)
		c.forecast["alerts"] = mergeAlerts(c.alerts, object(p.forecast["alerts"]), a.options.Now(), false)
		if weather.ValidateSnapshot(c.forecast, p.location) != nil {
			return
		}
	} else if c.err == nil && c.alertsPending {
		c.forecast["alerts"] = mergeAlerts(object(c.forecast["alerts"]), cachedAlerts(p.forecast, c.location), a.options.Now(), true)
	}
	if !alertOnly && c.err == nil && a.alerts != nil && c.country == "US" {
		// Pending is live scheduler state, never a promise saved to disk. A
		// stopped/hidden process must not leave a perpetual checking indicator.
		c.forecast["alerts"] = weather.UnavailableAlerts()
		if reflect.DeepEqual(c.location, p.location) {
			c.forecast["alerts"] = mergeAlerts(weather.UnavailableAlerts(), cachedAlerts(p.forecast, p.location), a.options.Now(), false)
			if p.alerts != nil {
				c.forecast["alerts"] = p.alerts
			}
		}
	}
	p.fetchBusy, p.locationBusy, p.resolving = false, false, false
	if !c.alertsPending {
		p.fetchCancel = nil
	}
	if c.err != nil {
		code := "refresh_failed"
		if c.selection != nil {
			code = "lookup_failed"
			var locationErr *weather.LocationError
			if errors.As(c.err, &locationErr) {
				switch locationErr.Code {
				case "zip_not_found", "zip_ambiguous", "place_not_found", "stale_selection", "timeout", "state_io_failed", "save_unconfirmed":
					code = locationErr.Code
				}
			}
		}
		if errors.Is(c.err, context.DeadlineExceeded) {
			code = "fetch_timeout"
			if c.selection != nil {
				code = "timeout"
			}
		}
		if c.selection != nil {
			p = a.failedSelectionPoint(p)
			p.locationError = code
		} else {
			p.errorCode = code
		}
		a.signal()
		return
	}
	candidate := safeio.Clone(p.profile)
	if c.selection != nil {
		candidate = M{"schema_version": 2.0, "mode": c.selection["mode"], "zip_code": c.selection["zip_code"], "location": c.location, "forecast": c.forecast, "country_code": c.country, "place": nil}
		if c.place != nil {
			candidate["place"] = c.place
		}
	} else {
		candidate["forecast"] = c.forecast
	}
	if err := ValidateProfile(candidate); err != nil {
		p.errorCode = "refresh_failed"
		a.signal()
		return
	}
	makePrimary := c.makePrimary || p == a.primary
	err := a.saved.put(candidate, p == a.forecastPoint && !a.primaryOnly, makePrimary)
	if err != nil && !errors.Is(err, errSavedLocationsUnconfirmed) {
		if c.selection != nil {
			p = a.failedSelectionPoint(p)
			p.locationError = "state_io_failed"
		} else {
			p.errorCode = "refresh_failed"
		}
		a.signal()
		return
	}
	oldLocation, oldID := p.location, p.id
	nextFetch := p.nextFetch
	p.adopt(candidate, a.options.Now())
	if alertOnly {
		p.nextFetch = nextFetch
	}
	p.forecastGeneration = c.generation
	if makePrimary {
		previous := a.primary
		a.primary = p
		if previous != p && previous != a.forecastPoint {
			a.cancelPointFetch(previous)
		}
	} else if p == a.forecastPoint && p.id == a.primary.id {
		// A user selected the primary place again. Keep the new request owner
		// so its independent alert reply can still be published.
		a.cancelPointFetch(a.primary)
		a.primary = p
	}
	if p == a.forecastPoint {
		a.viewPointChanged(oldID, oldLocation)
	}
	if err != nil {
		p.locationError = "save_unconfirmed"
	}
	a.signal()
}

func (p *forecastPoint) adopt(profile M, now time.Time) {
	oldLocation, oldCountry := p.location, p.country
	p.profile = profile
	p.location, p.forecast = object(profile["location"]), object(profile["forecast"])
	p.mode, p.zip = stringOf(profile["mode"]), profile["zip_code"]
	p.country, p.place = profileIdentity(profile, p.mode)
	if !reflect.DeepEqual(oldLocation, p.location) || oldCountry != p.country {
		p.alertKey, p.alerts = "", nil
	}
	p.id, _ = savedLocationID(profile) // Validated at the storage boundary.
	p.errorCode, p.locationError = nil, nil
	p.nextFetch = now.Add(weather.RefreshSeconds * time.Second)
}

// Rejoin shared primary weather after a failed tentative selection; a failed
// lookup must not leave two independent refresh consumers for the same city.
func (a *App) failedSelectionPoint(p *forecastPoint) *forecastPoint {
	if p == a.forecastPoint && p != a.primary && p.id == a.primary.id {
		a.cancelPointFetch(p)
		a.forecastPoint = a.primary
		return a.primary
	}
	return p
}
