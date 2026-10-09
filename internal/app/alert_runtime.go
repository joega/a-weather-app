package app

import (
	"errors"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

// A warning consumer adds primary alert demand, not a second network poller or
// an extra forecast refresh. Its policy/delivery owner consumes full reconciled
// bodies only while enabled. Ordinary app startup has no warning consumer.
type warningObservationConsumer interface {
	Enabled() bool
	Observe(notifications.WarningBatch, string, time.Time)
}

func pointAlertKey(p *forecastPoint) string {
	if p == nil || p.country != "US" {
		return ""
	}
	if p.alertKey == "" {
		p.alertKey, _ = notifications.WarningLocationKey(p.location)
	}
	return p.alertKey
}

func (a *App) tickAlertWork() {
	if a.alerts == nil || a.closed {
		return
	}
	now := a.options.Now()
	var demandBuffer [2]alertDemand
	demands := demandBuffer[:0]
	if !a.options.Offline {
		for _, p := range a.demandedPoints() {
			if p != nil && !p.needsResolve && !p.locationBusy {
				if key := pointAlertKey(p); key != "" {
					demands = append(demands, alertDemand{key: key, location: p.location})
				}
			}
		}
		if a.warningObserver != nil && a.warningObserver.Enabled() && !a.primary.needsResolve && !a.primary.locationBusy {
			if key := pointAlertKey(a.primary); key != "" {
				found := false
				for i := range demands {
					if demands[i].key == key {
						demands[i].monitor = true
						found = true
					}
				}
				if !found {
					demands = append(demands, alertDemand{key: key, location: a.primary.location, monitor: true})
				}
			}
		}
	}
	if err := a.alerts.setDemands(demands, now); err != nil {
		return
	} // Only validated app-owned identities enter here.
	for _, p := range []*forecastPoint{a.forecastPoint, a.primary} {
		e := a.alerts.entries[pointAlertKey(p)]
		if e == nil {
			continue
		}
		if !e.seeded {
			e.seeded = true
			cached := cachedAlerts(p.forecast, p.location)
			fetched, err := weather.Instant(cached["fetched_at"])
			if err == nil && cached["status"] == "available" && cached["freshness"] != "stale" && !fetched.After(now) && now.Sub(fetched) < weather.RefreshSeconds*time.Second {
				e.lastActive = fetched
				if !e.demand.monitor {
					e.next = fetched.Add(weather.RefreshSeconds * time.Second)
				}
			}
		}
		if p.alertRefresh {
			if !e.demand.monitor && (a.alerts.job == nil || a.alerts.job.entry != e) {
				e.next = now
			}
			p.alertRefresh = false
		}
	}
	for _, observation := range a.alerts.tick(now) {
		if observation.active != nil || observation.activeFailed {
			a.applyAlertObservation(observation, now)
		}
		if observation.batch != nil && a.warningObserver != nil && a.warningObserver.Enabled() && observation.key == pointAlertKey(a.primary) {
			a.warningObserver.Observe(*observation.batch, observation.err, now)
		}
		a.signal()
	}
}

func (a *App) applyAlertObservation(observation alertObservation, now time.Time) {
	seen := map[*forecastPoint]bool{}
	for _, p := range []*forecastPoint{a.forecastPoint, a.primary} {
		if p == nil || seen[p] || pointAlertKey(p) != observation.key {
			continue
		}
		seen[p] = true
		prior := p.alerts
		if prior == nil {
			prior = cachedAlerts(p.forecast, p.location)
		}
		incoming := observation.active
		if incoming == nil {
			incoming = weather.UnavailableAlerts()
		}
		if incoming["status"] == "available" {
			oldTime, _ := weather.Instant(prior["fetched_at"])
			newTime, _ := weather.Instant(incoming["fetched_at"])
			if newTime.Before(oldTime) {
				continue
			}
		}
		next := mergeAlerts(incoming, prior, now, false)
		if weather.ValidateAlerts(next) != nil {
			continue
		}
		p.alerts = safeio.Clone(next)
		if p.forecast == nil || reflect.DeepEqual(object(p.forecast["alerts"]), next) {
			continue
		}
		candidate := safeio.Clone(p.profile)
		forecast := safeio.Clone(p.forecast)
		forecast["alerts"] = p.alerts
		candidate["forecast"] = forecast
		if ValidateProfile(candidate) != nil {
			continue
		}
		err := a.saved.put(candidate, p == a.forecastPoint && !a.primaryOnly, p == a.primary)
		if err != nil && !errors.Is(err, errSavedLocationsUnconfirmed) {
			p.errorCode = "refresh_failed"
			continue
		}
		// Alert publication never completes/cancels a forecast worker, changes
		// its generation, resets its deadline, or clears a weather fetch error.
		p.profile, p.forecast = candidate, forecast
		if err != nil {
			p.locationError = "save_unconfirmed"
		}
	}
}

func (a *App) alertRefreshFinished() bool {
	if a.alerts == nil || a.options.Offline || a.primary.country != "US" {
		return true
	}
	e := a.alerts.entries[pointAlertKey(a.primary)]
	return e != nil && e.completed > 0
}
