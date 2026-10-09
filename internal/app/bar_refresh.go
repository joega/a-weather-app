package app

import (
	"context"
	"errors"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

// BarRefreshDue avoids network access for a fresh forecast or an installation
// where the user has not chosen a location yet.
func BarRefreshDue(state *safeio.Directory, now time.Time) (bool, error) {
	_, forecast, _, mode, _, err := readSaved(state)
	if err != nil {
		return false, err
	}
	if mode == "default" {
		return false, nil
	}
	if forecast != nil {
		fetched, err := weather.Instant(forecast["fetched_at"])
		if err != nil {
			return false, err
		}
		if now.Sub(fetched) < 15*time.Minute {
			return false, nil
		}
	}
	return true, nil
}

// RefreshBarSaved performs one bounded refresh with no Qt, effects or
// notifications. The caller must hold the same state lock as the full service.
func RefreshBarSaved(ctx context.Context, state *safeio.Directory, options Options) (err error) {
	if options.Now == nil {
		options.Now = time.Now
	}
	due, err := BarRefreshDue(state, options.Now())
	if err != nil || !due {
		return err
	}
	a, err := newApp(state, options, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, a.Close(context.Background())) }()
	if a.mode != "auto" {
		a.beginFetch(nil)
	}
	for {
		a.poll()
		if !a.fetchBusy && !a.locationBusy && a.fetchCancel == nil && a.alertRefreshFinished() {
			if a.errorCode != nil || a.locationError != nil {
				return errors.New("bar weather refresh failed")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-a.Changed:
		}
	}
}
