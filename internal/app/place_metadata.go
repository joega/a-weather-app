package app

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/locale"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

type metadataRequest struct {
	generation      uint64
	point           *forecastPoint
	id              string
	location, place M
	country         any
	language        string
}
type metadataCompletion struct {
	request  *metadataRequest
	resolved M
	err      error
}
type placeMetadata struct {
	generation uint64
	running    bool
	cancel     context.CancelFunc
	pending    *metadataRequest
	results    chan metadataCompletion
}

func samePlaceCoordinates(left, right M) bool {
	return left != nil && right != nil && left["latitude"] == right["latitude"] && left["longitude"] == right["longitude"] && left["timezone"] == right["timezone"]
}
func (a *App) cancelMetadata() {
	m := &a.metadata
	m.generation++
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.pending = nil
}
func (a *App) refreshMetadata() {
	a.cancelMetadata()
	p := a.forecastPoint
	if a.closed || a.options.Offline || a.options.ResolveSelection == nil || p == nil || p.locationBusy || p.mode != "place" || p.place == nil {
		return
	}
	m := &a.metadata
	m.pending = &metadataRequest{generation: m.generation, point: p, id: p.id, location: safeio.Clone(p.location), place: safeio.Clone(p.place), country: p.country, language: a.effectiveLanguage()}
	a.startMetadata()
}

// One running call and one replaceable pending request. A canceled call retains
// its slot until completion; rapidly switching languages never grows a queue.
func (a *App) startMetadata() {
	m := &a.metadata
	if m.running || m.pending == nil || a.closed {
		return
	}
	if m.results == nil {
		m.results = make(chan metadataCompletion, 1)
	}
	request := m.pending
	m.pending = nil
	m.running = true
	ctx, cancel := context.WithTimeout(locale.WithLanguage(context.Background(), request.language), 8*time.Second)
	m.cancel = cancel
	go func() {
		defer cancel()
		resolved, err := a.options.ResolveSelection(ctx, M{"mode": "place", "place_id": request.place["id"], "search_generation": 0.0})
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		m.results <- metadataCompletion{request, resolved, err}
		a.signal()
	}()
}
func (a *App) pollMetadata() {
	m := &a.metadata
	select {
	case result := <-m.results:
		m.running, m.cancel = false, nil
		r, p := result.request, result.request.point
		if result.err == nil && !a.closed && r.generation == m.generation && r.language == a.effectiveLanguage() && p == a.forecastPoint && p.id == r.id && !p.locationBusy && p.country == r.country && reflect.DeepEqual(p.place, r.place) && samePlaceCoordinates(p.location, r.location) {
			location, err := weather.ValidateLocation(object(result.resolved["location"]))
			if err == nil && result.resolved["country_code"] == r.country && reflect.DeepEqual(object(result.resolved["place"]), r.place) && samePlaceCoordinates(location, r.location) {
				a.applyMetadataLabel(p, stringOf(location["name"]))
			}
		}
		a.startMetadata()
	default:
	}
}
func (a *App) applyMetadataLabel(p *forecastPoint, name string) {
	if p.location["name"] == name {
		return
	}
	candidate := safeio.Clone(p.profile)
	location := safeio.Clone(p.location)
	location["name"] = name
	candidate["location"] = location
	if p.forecast != nil {
		forecast := safeio.Clone(p.forecast)
		forecast["location"] = safeio.Clone(location)
		candidate["forecast"] = forecast
	}
	if _, err := validateProfileIdentity(candidate); err != nil {
		return
	}
	if candidate["forecast"] != nil && ValidateProfile(candidate) != nil {
		return
	}
	err := a.saved.put(candidate, false, false)
	if err != nil && !errors.Is(err, errSavedLocationsUnconfirmed) {
		return
	}
	// Name-only metadata must not adopt/reset scheduling, alerts, selected hour,
	// units, forecast fetched_at, or optional map/detail ownership.
	p.profile, p.location, p.forecast = candidate, location, object(candidate["forecast"])
	if a.primary != p && a.primary.id == p.id && samePlaceCoordinates(a.primary.location, location) {
		primary := a.primary
		primary.location = safeio.Clone(location)
		primary.profile = safeio.Clone(primary.profile)
		primary.profile["location"] = safeio.Clone(location)
		if primary.forecast != nil {
			primary.forecast = safeio.Clone(primary.forecast)
			primary.forecast["location"] = safeio.Clone(location)
			primary.profile["forecast"] = primary.forecast
		}
	}
	a.signal()
}
