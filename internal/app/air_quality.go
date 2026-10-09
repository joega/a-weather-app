package app

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const airQualityAttribution = airquality.Attribution

type airQualityCompletion struct {
	generation uint64
	record     M
	err        error
}

type airQualityState struct {
	record     M
	errorCode  any
	nextFetch  time.Time
	generation uint64
	active     bool
	cancel     context.CancelFunc
	results    chan airQualityCompletion
}

func (a *App) initAirQuality() {
	a.aq.results = make(chan airQualityCompletion, 1)
	a.aq.nextFetch = a.options.Now()
	v, err := a.state.Read("air-quality.json", airquality.MaxBytes)
	if err != nil {
		a.aq.errorCode = "cache_invalid"
		return
	}
	if v == nil {
		return
	}
	// A single bounded cache may belong to the previously selected location.
	// Its absence or damage must never prevent core weather from opening.
	if err := airquality.ValidateRecord(v, object(v["location"])); err != nil {
		a.aq.errorCode = "cache_invalid"
		return
	}
	if !reflect.DeepEqual(v["location"], a.location) {
		return
	}
	a.aq.record = v
	fetched, _ := weather.Instant(v["fetched_at"])
	valid, _ := weather.Instant(v["valid_at"])
	if !fetched.After(a.options.Now().Add(5*time.Minute)) && !valid.After(a.options.Now().Add(5*time.Minute)) {
		a.aq.nextFetch = fetched.Add(time.Hour)
	}
}

// A canceled worker remains active until its completion is drained. Location
// changes therefore coalesce behind one worker, even when transport cancellation
// takes time. No obsolete worker can publish to memory or disk.
func (a *App) cancelAirQuality() {
	a.aq.generation++
	if a.aq.cancel != nil {
		a.aq.cancel()
	}
}

func (a *App) beginAirQuality() {
	if a.closed || !a.presented || a.primaryOnly || !a.dashboardVisible("air_quality") || a.options.Offline || a.options.FetchAirQuality == nil || a.locationBusy || a.aq.active || a.options.Now().Before(a.aq.nextFetch) {
		return
	}
	location := safeio.Clone(a.location)
	if _, err := weather.ValidateLocation(location); err != nil {
		return
	}
	if a.aq.results == nil {
		a.aq.results = make(chan airQualityCompletion, 1)
	}
	now, generation := a.options.Now(), a.aq.generation
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	a.aq.cancel, a.aq.active = cancel, true
	// Failed calls also wait an hour; weather/manual refresh never overrides it.
	a.aq.nextFetch = now.Add(time.Hour)
	fetch, results := a.options.FetchAirQuality, a.aq.results
	a.signal()
	go func() {
		defer cancel()
		record, err := fetch(ctx, location, now)
		if err == nil {
			err = airquality.ValidateRecord(record, location)
			if err == nil {
				record = safeio.Clone(record)
			}
		}
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		// One active worker and a one-element channel bound both work and
		// completion storage. Only the owner thread writes cache files.
		results <- airQualityCompletion{generation: generation, record: record, err: err}
		a.signal()
	}()
}

func (a *App) pollAirQuality() {
	select {
	case c := <-a.aq.results:
		a.aq.active, a.aq.cancel = false, nil
		if !a.closed && c.generation == a.aq.generation {
			if c.err == nil && !reflect.DeepEqual(c.record["location"], a.location) {
				c.err = errors.New("air quality location changed")
			}
			if c.err == nil && a.aq.record != nil && reflect.DeepEqual(a.aq.record["location"], a.location) {
				oldTime, _ := weather.Instant(a.aq.record["valid_at"])
				oldFetched, _ := weather.Instant(a.aq.record["fetched_at"])
				newTime, _ := weather.Instant(c.record["valid_at"])
				futureLimit := a.options.Now().Add(5 * time.Minute)
				if !oldTime.After(futureLimit) && !oldFetched.After(futureLimit) && newTime.Before(oldTime) {
					c.err = errors.New("air quality valid time regressed")
				}
			}
			if c.err != nil {
				a.aq.errorCode = "fetch_failed"
				if errors.Is(c.err, context.DeadlineExceeded) {
					a.aq.errorCode = "timeout"
				}
			} else if err := a.state.Write("air-quality.json", c.record, airquality.MaxBytes); err != nil {
				a.aq.errorCode = "save_failed"
			} else {
				a.aq.record, a.aq.errorCode = c.record, nil
			}
			a.signal()
		}
	default:
	}
	a.beginAirQuality()
}

func (a *App) airQualitySnapshot() M {
	s := M{"freshness": "unavailable", "refreshing": a.aq.active, "offline": a.options.Offline, "error": a.aq.errorCode,
		"domain": "cams_global", "source": "CAMS global model data", "attribution": airQualityAttribution,
		"valid_at": nil, "fetched_at": nil, "valid_label": nil, "fetched_label": nil, "age_seconds": nil, "us_aqi": nil, "european_aqi": nil, "pm2_5_ug_m3": nil}
	v := a.aq.record
	if v == nil || !reflect.DeepEqual(v["location"], a.location) {
		return s
	}
	fetched, err := weather.Instant(v["fetched_at"])
	if err != nil {
		return s
	}
	valid, err := weather.Instant(v["valid_at"])
	if err != nil {
		return s
	}
	s["fetched_at"], s["valid_at"] = v["fetched_at"], v["valid_at"]
	zone, err := time.LoadLocation(stringOf(a.location["timezone"]))
	if err != nil {
		zone = time.UTC
	}
	// Qt's locale formatter does not implement JavaScript Intl timeZone.
	// Format authoritative IANA times here, including offsets across DST.
	const labelFormat = "2006-01-02 15:04 MST (-07:00)"
	s["valid_label"], s["fetched_label"] = valid.In(zone).Format(labelFormat), fetched.In(zone).Format(labelFormat)
	now := a.options.Now()
	if fetched.After(now.Add(5*time.Minute)) || valid.After(now.Add(5*time.Minute)) {
		s["freshness"] = "invalid_future"
		return s
	}
	age := math.Max(0, math.Max(now.Sub(fetched).Seconds(), now.Sub(valid).Seconds()))
	s["age_seconds"] = math.Min(age, 315360000)
	s["freshness"] = "fresh"
	if age > 6*time.Hour.Seconds() {
		s["freshness"] = "expired"
		return s
	}
	if age > 2*time.Hour.Seconds() {
		s["freshness"] = "stale"
	}
	for _, key := range []string{"us_aqi", "european_aqi", "pm2_5_ug_m3"} {
		s[key] = v[key]
	}
	return s
}
