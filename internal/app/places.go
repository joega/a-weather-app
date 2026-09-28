package app

import (
	"context"
	"errors"
	"math"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func placeInteger(v any, low float64) bool {
	n, ok := v.(float64)
	return ok && n >= low && n <= 2147483647 && math.Trunc(n) == n
}

func validCountry(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

func profileIdentity(profile M, mode string) (any, M) {
	if profile != nil && profile["schema_version"] == float64(2) {
		return profile["country_code"], safeio.Clone(object(profile["place"]))
	}
	if mode == "zip" || mode == "default" {
		return "US", nil
	}
	// Legacy custom and auto locations have no authoritative country identity.
	return nil, nil
}

// SaveZIPIdentity carries the CLI's successfully resolved US identity across
// the guardian restart without another geocoding call. Old separate cache files
// remain usable; the normal profile takes precedence once one is published.
func SaveZIPIdentity(state *safeio.Directory, zip string, location M) error {
	if _, err := weather.ValidateSelection(M{"mode": "zip", "zip_code": zip}); err != nil {
		return err
	}
	validated, err := weather.ValidateLocation(location)
	if err != nil {
		return err
	}
	return state.Write("location-identity.json", M{"schema_version": 1.0, "mode": "zip", "zip_code": zip, "country_code": "US", "location": validated}, 8192)
}

func readZIPIdentity(state *safeio.Directory, location M) (M, error) {
	v, err := state.Read("location-identity.json", 8192)
	if err != nil || v == nil {
		return v, err
	}
	if len(v) != 5 || v["schema_version"] != 1.0 || v["mode"] != "zip" || v["country_code"] != "US" {
		return nil, errors.New("invalid saved ZIP identity")
	}
	if _, err = weather.ValidateSelection(M{"mode": "zip", "zip_code": v["zip_code"]}); err != nil {
		return nil, err
	}
	validated, err := weather.ValidateLocation(object(v["location"]))
	if err != nil || !reflect.DeepEqual(validated, location) {
		return nil, errors.New("saved ZIP identity does not match location")
	}
	return v, nil
}

// Keep the last schema-1 profile available for an explicit rollback. The new
// profile is still published in one rename only after its forecast succeeds.
func (a *App) backupLegacyProfile() error {
	if a.profile == nil || a.profile["schema_version"] != float64(1) {
		return nil
	}
	previous, err := a.state.Read("location-profile-v1.json", 2*1024*1024)
	if err != nil {
		return err
	}
	if previous != nil {
		if previous["schema_version"] != float64(1) {
			return errors.New("invalid rollback profile")
		}
		return ValidateProfile(previous)
	}
	return a.state.Write("location-profile-v1.json", a.profile, 2*1024*1024)
}

type searchCompletion struct {
	generation float64
	rows       []any
	err        error
}

type placeSearch struct {
	generation  float64
	clientToken float64
	status      string
	rows        []any
	errorCode   any
	issued      time.Time
	cancel      context.CancelFunc
	running     bool
	pending     M
	results     chan searchCompletion
}

func (s *placeSearch) init() {
	s.status, s.rows, s.results = "idle", []any{}, make(chan searchCompletion, 1)
}

func (a *App) cancelSearch() {
	s := &a.search
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation = math.Mod(s.generation+1, 2147483648)
	s.clientToken = 0
	s.pending, s.errorCode = nil, nil
	s.status, s.rows, s.issued = "idle", []any{}, time.Time{}
}

func (a *App) beginSearch(query M) {
	a.cancelSearch()
	s := &a.search
	s.clientToken, _ = query["client_token"].(float64)
	if a.options.Offline {
		s.status, s.errorCode = "error", "offline"
		return
	}
	s.status, s.pending = "loading", safeio.Clone(query)
	a.startSearch()
}

// At most one network call and one replaceable pending query exist. A canceled
// worker has to finish before its replacement starts; keystrokes cannot spawn
// an unbounded goroutine or connection queue.
func (a *App) startSearch() {
	s := &a.search
	if s.running || s.pending == nil || a.closed {
		return
	}
	query, generation := s.pending, s.generation
	s.pending, s.running = nil, true
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	s.cancel = cancel
	go func() {
		defer cancel()
		rows, err := a.options.SearchPlaces(ctx, query)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		s.results <- searchCompletion{generation, rows, err}
		a.signal()
	}()
}

func (a *App) pollSearch() {
	s := &a.search
	select {
	case result := <-s.results:
		s.running, s.cancel = false, nil
		if result.generation == s.generation && !a.closed {
			s.rows = []any{}
			if result.err != nil {
				s.status, s.errorCode = "error", "lookup_failed"
				if errors.Is(result.err, context.DeadlineExceeded) {
					s.errorCode = "timeout"
				}
			} else {
				s.status, s.errorCode, s.rows, s.issued = "ready", nil, result.rows, a.options.Now()
				if s.rows == nil {
					s.rows = []any{}
				}
			}
		}
		a.startSearch()
	default:
	}
}

func (a *App) consumePlace(selection M) error {
	s := &a.search
	age := a.options.Now().Sub(s.issued)
	if s.status != "ready" || selection["search_generation"] != s.generation || age < 0 || age > 5*time.Minute {
		return errors.New("stale place selection")
	}
	count := 0
	for _, row := range s.rows {
		if object(row)["id"] == selection["place_id"] {
			count++
		}
	}
	if count != 1 {
		return errors.New("place was not uniquely offered")
	}
	return nil
}

func (a *App) searchSnapshot() M {
	s := &a.search
	if s.status == "" {
		return M{"generation": 0.0, "client_token": 0.0, "status": "idle", "results": []any{}, "error": nil}
	}
	return M{"generation": s.generation, "client_token": s.clientToken, "status": s.status, "results": weather.Clone(s.rows), "error": s.errorCode}
}
