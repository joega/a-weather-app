package app

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/weathermap"
)

const mapCacheBytes = 160 * 1024

type mapCompletion struct {
	generation uint64
	data       weathermap.Data
	err        error
}

type mapState struct {
	open, active         bool
	generation, revision uint64
	cancel               context.CancelFunc
	results              chan mapCompletion
	data                 *weathermap.Data
	errorCode            string
	nextAttempt          time.Time
	attemptWindow        time.Time
	attempts             int
}

func (a *App) initMap() {
	a.wmap.results = make(chan mapCompletion, 1)
	v, e := a.state.Read("weather-map.json", mapCacheBytes)
	if e != nil || v == nil {
		return
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return
	}
	var data weathermap.Data
	if json.Unmarshal(raw, &data) != nil || validateMapData(data) != nil {
		return
	}
	if math.Abs(data.Latitude-a.location["latitude"].(float64)) > 0.00001 || math.Abs(data.Longitude-a.location["longitude"].(float64)) > 0.00001 {
		return
	}
	a.wmap.data = &data
}

func validateMapData(d weathermap.Data) error {
	return weathermap.Validate(d)
}

func (a *App) closeMap() {
	a.wmap.open = false
	a.wmap.generation++
	if a.wmap.cancel != nil {
		a.wmap.cancel()
		if a.wmap.attempts < 2 {
			a.wmap.nextAttempt = a.options.Now()
		}
	}
	a.wmap.revision++
	a.signal()
}

func (a *App) openMap() {
	a.wmap.open = true
	a.wmap.revision++
	a.signal()
	if a.options.Offline || a.options.FetchMap == nil || a.locationBusy || a.wmap.active || a.options.Now().Before(a.wmap.nextAttempt) {
		return
	}
	if a.wmap.data != nil && a.options.Now().Sub(a.wmap.data.FetchedAt) < 20*time.Minute {
		return
	}
	if a.wmap.attemptWindow.IsZero() || !a.options.Now().Before(a.wmap.attemptWindow.Add(20*time.Minute)) {
		a.wmap.attemptWindow = a.options.Now()
		a.wmap.attempts = 0
	}
	if a.wmap.attempts >= 2 {
		return
	}
	location := a.location
	lat, latOK := location["latitude"].(float64)
	lon, lonOK := location["longitude"].(float64)
	if !latOK || !lonOK {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	a.wmap.cancel = cancel
	a.wmap.active = true
	a.wmap.errorCode = ""
	a.wmap.nextAttempt = a.options.Now().Add(20 * time.Minute)
	a.wmap.attempts++
	generation, fetch, now, country := a.wmap.generation, a.options.FetchMap, a.options.Now(), stringOf(a.country)
	go func() {
		defer cancel()
		data, e := fetch(ctx, lat, lon, country, now)
		if ctx.Err() != nil {
			e = ctx.Err()
		}
		select {
		case a.wmap.results <- mapCompletion{generation, data, e}:
			a.signal()
		default:
		}
	}()
}

func (a *App) pollMap() {
	for {
		select {
		case result := <-a.wmap.results:
			a.wmap.active = false
			a.wmap.cancel = nil
			if result.generation != a.wmap.generation || !a.wmap.open || a.closed {
				if a.wmap.open {
					a.openMap()
				}
				continue
			}
			if result.err != nil || validateMapData(result.data) != nil {
				a.wmap.errorCode = "fetch_failed"
				a.wmap.revision++
				a.signal()
				continue
			}
			if math.Abs(result.data.Latitude-a.location["latitude"].(float64)) > 0.00001 || math.Abs(result.data.Longitude-a.location["longitude"].(float64)) > 0.00001 {
				continue
			}
			// A failed cache write does not erase the valid in-memory forecast.
			if e := a.state.Write("weather-map.json", result.data, mapCacheBytes); e != nil {
				a.wmap.errorCode = "save_failed"
			}
			data := result.data
			a.wmap.data = &data
			a.wmap.revision++
			a.signal()
		default:
			return
		}
	}
}

func (a *App) Map() (uint64, M) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pollMap()
	s := &a.wmap
	status := "closed"
	var payload any
	var fetched any
	var fetchedLabel any
	labels := []string{}
	if s.open {
		status = "unavailable"
		if s.active {
			status = "loading"
		}
		if s.data != nil {
			age := a.options.Now().Sub(s.data.FetchedAt)
			if age >= -5*time.Minute && age <= 6*time.Hour {
				payload = s.data
				fetched = s.data.FetchedAt
				zone, e := time.LoadLocation(stringOf(a.location["timezone"]))
				if e != nil {
					zone = time.UTC
				}
				fetchedLabel = s.data.FetchedAt.In(zone).Format("Mon Jan 2, 3:04 PM MST")
				for _, hour := range s.data.Hours {
					labels = append(labels, time.Unix(hour, 0).In(zone).Format("Mon Jan 2, 3:04 PM MST"))
				}
				status = "fresh"
				if age > 20*time.Minute {
					status = "stale"
				}
			}
		}
	}
	return s.revision, M{"status": status, "data": payload, "fetched_at": fetched, "fetched_label": fetchedLabel, "hour_labels": labels, "offline": a.options.Offline, "error": s.errorCode}
}

func (a *App) mapLocationChanged(previous M) {
	if !reflect.DeepEqual(previous, a.location) {
		a.closeMap()
		a.wmap.data = nil
		a.wmap.errorCode = ""
		a.wmap.nextAttempt = time.Time{}
		a.wmap.attemptWindow = time.Time{}
		a.wmap.attempts = 0
	}
}
