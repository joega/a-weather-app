package app

import (
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

// A committed manifest is authoritative. Damaged caches degrade to the saved
// identity with no weather; damaged metadata never silently rolls back a city.
// Bar readers do not migrate, rename, or touch recency on read.
func readSaved(state *safeio.Directory) (location, forecast, profile M, mode string, zip any, err error) {
	store, err := loadSavedLocations(state)
	if err != nil {
		return nil, nil, nil, "", nil, err
	}
	if store == nil {
		return readLegacySaved(state)
	}
	profile, _ = store.profile(stringOf(store.doc["primary"]))
	return object(profile["location"]), object(profile["forecast"]), profile, stringOf(profile["mode"]), profile["zip_code"], nil
}

// ReadPrimaryProfile returns caller-owned saved identity and optional weather.
// It does not migrate state or start workers. Nil means no persisted identity.
// A damaged new-format cache yields identity without forecast; metadata errors
// are returned. Legacy files are consulted only before manifest publication.
func ReadPrimaryProfile(state *safeio.Directory) (M, error) {
	location, forecast, profile, mode, zip, err := readSaved(state)
	if err != nil || profile != nil {
		return profile, err
	}
	if mode == "default" {
		return nil, nil
	}
	country, _ := profileIdentity(nil, mode)
	profile = M{"schema_version": 2.0, "mode": mode, "zip_code": zip, "location": location, "forecast": nil, "country_code": country, "place": nil}
	if forecast != nil {
		profile["forecast"] = forecast
	}
	return profile, nil
}

func (a *App) restoreLocations() error {
	var err error
	a.saved, err = loadSavedLocations(a.state)
	if err != nil {
		return err
	}
	if a.saved == nil {
		location, forecast, profile, mode, zip, e := readLegacySaved(a.state)
		if e != nil {
			return e
		}
		if profile == nil {
			country, _ := profileIdentity(nil, mode)
			profile = M{"schema_version": 2.0, "mode": mode, "zip_code": zip, "location": location, "forecast": nil, "country_code": country, "place": nil}
			if forecast != nil {
				profile["forecast"] = forecast
			}
		}
		a.saved, err = createSavedLocations(a.state, profile)
		if err != nil {
			return err
		}
	}
	// Home is the startup destination. Browsing another saved place lasts for
	// this session; bar-only readers must not change the open window's choice.
	if !a.primaryOnly && a.saved.doc["viewed"] != a.saved.doc["primary"] {
		err = a.saved.view(stringOf(a.saved.doc["primary"]))
		if err != nil && !errors.Is(err, errSavedLocationsUnconfirmed) {
			return err
		}
	}
	a.primary = a.loadPoint(stringOf(a.saved.doc["primary"]))
	a.forecastPoint = a.primary
	if err != nil {
		a.locationError = "save_unconfirmed"
	}
	return nil
}

func (a *App) loadPoint(id string) *forecastPoint {
	profile, err := a.saved.profile(id)
	p := &forecastPoint{needsResolve: profile["mode"] == "auto"}
	p.adopt(profile, a.options.Now())
	p.nextFetch = a.options.Now()
	if err != nil {
		p.errorCode = "refresh_failed"
	}
	if p.forecast != nil {
		fetched, _ := weather.Instant(p.forecast["fetched_at"])
		age := math.Max(0, a.options.Now().Sub(fetched).Seconds())
		p.nextFetch = a.options.Now().Add(time.Duration(math.Max(0, weather.RefreshSeconds-age) * float64(time.Second)))
		if alerts := object(p.forecast["alerts"]); alerts != nil && (alerts["refreshing"] == true || alerts["freshness"] == "pending") {
			alerts["refreshing"] = false
			if alerts["freshness"] == "pending" {
				alerts["freshness"] = "unavailable"
			}
		}
	}
	return p
}

func (a *App) viewPointChanged(oldID string, oldLocation M) {
	a.controls = controlsForCountry(a.controls, a.country)
	if oldID == a.id && reflect.DeepEqual(oldLocation, a.location) {
		return
	}
	a.closeMap()
	a.closeRadar()
	a.closePrecipitation()
	a.closeAirOutlook()
	a.mapLocationChanged(oldLocation)
	a.cancelAirQuality()
	if !reflect.DeepEqual(oldLocation, a.location) {
		a.aq.record, a.aq.errorCode, a.aq.nextFetch = nil, nil, a.options.Now()
	}
	a.displayRows = displayRows{}
}

func (a *App) primaryNeeded() bool {
	return a.primaryOnly || a.options.Now().Before(a.barRefreshUntil) || a.notifications.Enabled() || a.effectsStatus()["state"] == "running"
}

func (a *App) refreshDuePoints() {
	for _, p := range a.demandedPoints() {
		if p != nil && (p.needsResolve || !a.options.Now().Before(p.nextFetch)) {
			a.beginPointFetch(p, nil, false)
		}
	}
}

func (a *App) demandedPoints() [2]*forecastPoint {
	points := [2]*forecastPoint{}
	if a.presented && !a.primaryOnly {
		points[0] = a.forecastPoint
	}
	if a.primaryNeeded() && points[0] != a.primary {
		points[1] = a.primary
	}
	return points
}

func (a *App) nextPointFetch() time.Time {
	next := a.options.Now().Add(time.Minute)
	for _, p := range a.demandedPoints() {
		if p == nil {
			continue
		}
		if p.needsResolve {
			return a.options.Now()
		}
		if p.nextFetch.Before(next) {
			next = p.nextFetch
		}
	}
	return next
}

func (a *App) tickNotifications() {
	p := a.primary
	// A failed attempt to browse another city leaves primary authoritative.
	// Its UI selection error must not suspend watching valid primary weather.
	a.notifications.Tick(p.forecast, p.location, a.options.Now(), p.errorCode == nil && !p.locationBusy)
}

// Server visibility controls optional viewed-city work. The explicit bar
// refresh uses a primary-only App; a saved favorite is never a polling demand.
func (a *App) setPresented(active bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.presented == active {
		return
	}
	a.presented = active
	if !active {
		a.closeMap()
		a.closeRadar()
		// Socket-owned detail demand is closed by that exact peer's hide or
		// disconnect, not a delayed aggregate visibility notification.
		if a.precipitation != nil && a.precipitation.owner == nil {
			a.closePrecipitation()
		}
		if a.airOutlook != nil && a.airOutlook.owner == nil {
			a.closeAirOutlook()
		}
		a.cancelAirQuality()
		if a.forecastPoint != a.primary || !a.primaryNeeded() {
			a.cancelPointFetch(a.forecastPoint)
		}
	} else {
		a.refreshDuePoints()
	}
	a.tickAlertWork()
	a.signal()
}

var errInvalidSavedAction = errors.New("invalid saved location action")

// Validate every action before changing disk or canceling workers. Storage is
// the publication boundary; post-rename uncertainty adopts the visible state.
func (a *App) savedLocationAction(action M) error {
	id, ok := action["id"].(string)
	if !ok || savedEntry(a.saved.doc, id) == nil {
		return errInvalidSavedAction
	}
	var err error
	switch action["action"] {
	case "view", "primary":
		if !savedFields(action, "action", "id") {
			return errInvalidSavedAction
		}
		if action["action"] == "view" {
			err = a.saved.view(id)
		} else {
			err = a.saved.makePrimary(id)
		}
	case "rename":
		if !savedFields(action, "action", "id", "label") || savedLabel(action["label"]) != nil {
			return errInvalidSavedAction
		}
		err = a.saved.rename(id, action["label"].(string))
	case "move":
		index, ok := action["index"].(float64)
		if !savedFields(action, "action", "id", "index") || !ok || math.IsNaN(index) || index < 0 || index >= float64(len(a.saved.doc["places"].([]any))) || math.Trunc(index) != index {
			return errInvalidSavedAction
		}
		err = a.saved.move(id, int(index))
	case "remove":
		replacement, ok := action["replacement"].(string)
		if !savedFields(action, "action", "id", "replacement") || !ok || len(a.saved.doc["places"].([]any)) <= 1 {
			return errInvalidSavedAction
		}
		if a.saved.doc["primary"] == id && (replacement == id || savedEntry(a.saved.doc, replacement) == nil) {
			return errInvalidSavedAction
		}
		if a.saved.doc["primary"] != id && replacement != "" {
			return errInvalidSavedAction
		}
		err = a.saved.remove(id, replacement)
	default:
		return errInvalidSavedAction
	}
	if err != nil && !errors.Is(err, errSavedLocationsUnconfirmed) {
		return err
	}
	if action["action"] == "remove" && !a.forgetForecastHistory(id) {
		err = errSavedLocationsUnconfirmed
	}
	if action["action"] == "rename" || action["action"] == "move" {
		if err != nil {
			a.locationError = "save_unconfirmed"
		}
		return err
	}
	oldView, oldPrimary := a.forecastPoint, a.primary
	oldID, oldLocation := oldView.id, oldView.location
	// A view action also cancels an unresolved selection for the old city.
	if action["action"] == "view" || oldView.locationBusy || (action["action"] == "remove" && oldView.id == id) {
		a.cancelPointFetch(oldView)
	}
	lookup := func(id string) *forecastPoint {
		if id == oldPrimary.id {
			return oldPrimary
		}
		if id == oldView.id {
			return oldView
		}
		return a.loadPoint(id)
	}
	a.primary = lookup(stringOf(a.saved.doc["primary"]))
	a.forecastPoint = a.primary
	if a.saved.doc["viewed"] != a.saved.doc["primary"] {
		a.forecastPoint = lookup(stringOf(a.saved.doc["viewed"]))
	}
	for _, p := range []*forecastPoint{oldView, oldPrimary} {
		if p != a.forecastPoint && p != a.primary {
			a.cancelPointFetch(p)
		}
	}
	a.viewPointChanged(oldID, oldLocation)
	a.refreshDuePoints()
	if err != nil {
		a.locationError = "save_unconfirmed"
	}
	return err
}
