// Package app owns weather state and serializes mutations independently of Qt.
package app

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
	"github.com/joega/a-weather-app/internal/weathermap"
)

// M is an application JSON object with float64 wire numbers.
type M = map[string]any

// Effects supplies the native backend used exclusively by the effects coordinator.
// Methods must respect cancellation; the coordinator serializes calls and
// maintains the independent heartbeat and bounded recovery lifecycle.
type Effects interface {
	SetupSnapshot() M
	Check(context.Context) M
	SelectOutput(context.Context, string) (M, error)
	Status() M
	Start(context.Context, int, bool, M) error
	Tick(context.Context, M, M) error
	Stop(context.Context) error
}

// Options supplies immutable dependencies and runtime behavior to New.
// Now defaults to time.Now; weather fetch and location resolution have defaults.
// Optional callbacks may run on workers and must honor their contexts.
// Callbacks returning JSON objects must provide compatible trees and transfer
// ownership to App.
// UpdateStatus returns a caller-owned snapshot and should not block.
// Offline suppresses automatic fetches and update checks.
type Options struct {
	UpdateStatus      func() M
	AcknowledgeUpdate func(string) error
	CheckUpdates      func(context.Context, bool) error
	StartUpdate       func() error
	Root              string
	Now               func() time.Time
	Fetch             func(context.Context, M, time.Time) (M, error)
	Resolve           func(context.Context, M) (M, error)
	ResolveSelection  func(context.Context, M) (M, error)
	FetchCountry      func(context.Context, M, time.Time, string) (M, error)
	// FetchAlerts is independent when supplied; nil preserves custom combined fetches.
	FetchAlerts func(context.Context, M, time.Time) (M, error)
	// Nil leaves air quality cache-only. The production service supplies Fetch.
	FetchAirQuality func(context.Context, M, time.Time) (M, error)
	FetchMap        func(context.Context, float64, float64, string, time.Time) (weathermap.Data, error)
	SearchPlaces    func(context.Context, M) ([]any, error)
	Effects         Effects
	Sender          notifications.Sender
	Offline         bool
}
type completion struct {
	point                         *forecastPoint
	makePrimary                   bool
	generation                    uint64
	selection, location, forecast M
	err                           error
	country                       any
	place                         M
	alerts                        M
	alertsPending                 bool
}

// App serializes state mutations and exposes independent cached snapshots.
// Use Handle, Tick, Snapshot, and Close rather than sharing its internal state.
// The caller retains ownership of the safeio.Directory passed to New.
type App struct {
	// The embedded point is the city displayed by the frontend. Primary is
	// shared with it whenever both consumers select the same saved identity.
	*forecastPoint
	primary         *forecastPoint
	saved           *savedLocations
	savedList       savedListPresentation
	primaryOnly     bool
	barRefreshUntil time.Time
	presented       bool
	workGeneration  uint64
	forecastJobs    map[*forecastWork]bool
	forecastDone    chan *forecastWork
	alertSlots      chan struct{}
	mu              contextMutex
	cacheMu         sync.RWMutex
	cached          M
	displayRows     displayRows
	revision        uint64
	fx              *effectsCoordinator
	closeDone       chan struct{}
	state           *safeio.Directory
	options         Options
	controls        M
	search          placeSearch
	aq              airQualityState
	wmap            mapState
	notifications   *notifications.Watcher
	results         chan completion
	Changed         chan struct{}
	launcherStatus  string
	updates         updateState
	closed          bool
	closeErr        error
}

// DefaultControls returns a fresh controls object in the persisted JSON format.
func DefaultControls() M {
	return M{"mode": "live", "strength": "subtle", "manual": M{"condition": "rain"}, "reduced_motion": false, "visual_quality": "auto", "lightning_enabled": false, "fps": float64(30), "window_physics": true, "accumulation": true, "pause_fullscreen": true, "units": "F", "units_mode": "auto", "wind_units": "auto"}
}
func stringOf(v any) string { s, _ := v.(string); return s }
func object(v any) M        { m, _ := v.(map[string]any); return m }

// PatchControls validates and merges a patch into an independent copy of old.
// The old object must already satisfy the trusted internal JSON contract.
func PatchControls(old, patch M) (M, error) {
	if patch == nil {
		return nil, errors.New("controls object")
	}
	v := safeio.Clone(old)
	for k, x := range patch {
		if _, ok := v[k]; !ok {
			return nil, errors.New("unknown control")
		}
		v[k] = x
	}
	// Selecting a unit is an explicit preference. Legacy saved unit selections
	// also remain explicit when they have no units_mode field.
	if _, chosen := patch["units"]; chosen {
		if _, mode := patch["units_mode"]; !mode {
			v["units_mode"] = "manual"
		}
	}
	if v["mode"] != "live" && v["mode"] != "manual" {
		return nil, errors.New("mode")
	}
	if v["strength"] != "subtle" && v["strength"] != "normal" && v["strength"] != "immersive" {
		return nil, errors.New("strength")
	}
	if v["visual_quality"] != "auto" && v["visual_quality"] != "full" && v["visual_quality"] != "economical" && v["visual_quality"] != "static" {
		return nil, errors.New("visual quality")
	}
	manual := object(v["manual"])
	if len(manual) != 1 || !weather.ValidCondition(stringOf(manual["condition"])) {
		return nil, errors.New("manual")
	}
	if v["fps"] != float64(15) && v["fps"] != float64(30) && v["fps"] != float64(60) {
		return nil, errors.New("fps")
	}
	if v["units"] != "F" && v["units"] != "C" {
		return nil, errors.New("units")
	}
	if v["units_mode"] != "auto" && v["units_mode"] != "manual" {
		return nil, errors.New("units mode")
	}
	if v["wind_units"] != "auto" && v["wind_units"] != "mph" && v["wind_units"] != "km/h" && v["wind_units"] != "m/s" && v["wind_units"] != "kn" {
		return nil, errors.New("wind units")
	}
	for _, k := range []string{"reduced_motion", "lightning_enabled", "window_physics", "accumulation", "pause_fullscreen"} {
		if _, ok := v[k].(bool); !ok {
			return nil, errors.New("boolean")
		}
	}
	return v, nil
}

// ValidateProfile checks a persisted location identity and its forecast together.
func ValidateProfile(v M) error {
	location, err := validateProfileIdentity(v)
	if err != nil {
		return err
	}
	return weather.ValidateSnapshot(object(v["forecast"]), location)
}

// Identity validation is shared with saved places whose forecast cache may be
// absent or evicted. Legacy profiles still require a valid forecast above.
func validateProfileIdentity(v M) (M, error) {
	v1 := v["schema_version"] == float64(1) && len(v) == 5
	v2 := v["schema_version"] == float64(2) && len(v) == 7
	if !v1 && !v2 {
		return nil, errors.New("location profile")
	}
	for k := range v {
		if k != "schema_version" && k != "mode" && k != "zip_code" && k != "location" && k != "forecast" && !(v2 && (k == "country_code" || k == "place")) {
			return nil, errors.New("location profile fields")
		}
	}
	if v2 {
		if !validCountry(v["country_code"]) || (v["mode"] == "zip" && v["country_code"] != "US") {
			return nil, errors.New("location country")
		}
		if v["mode"] == "place" {
			p := object(v["place"])
			if len(p) != 2 || p["provider"] != "open-meteo" || !placeInteger(p["id"], 1) || v["country_code"] == nil || v["zip_code"] != nil {
				return nil, errors.New("place identity")
			}
		} else if v["place"] != nil {
			return nil, errors.New("unexpected place identity")
		}
	}
	selection := M{"mode": v["mode"]}
	if v["mode"] == "zip" {
		selection["zip_code"] = v["zip_code"]
	}
	if v["mode"] == "custom" || v["mode"] == "default" || (v2 && v["mode"] == "place") {
		if v["zip_code"] != nil {
			return nil, errors.New("unexpected ZIP")
		}
	} else {
		s, e := weather.ValidateSelection(selection)
		if e != nil || s["zip_code"] != v["zip_code"] {
			return nil, errors.New("location selection")
		}
	}
	location, e := weather.ValidateLocation(object(v["location"]))
	if e != nil {
		return nil, e
	}
	return location, nil
}
func readLegacySaved(state *safeio.Directory) (location, forecast, profile M, mode string, zip any, err error) {
	profile, err = state.Read("location-profile.json", weather.MaxBytes)
	if err != nil {
		return location, forecast, profile, mode, zip, err
	}
	if profile != nil {
		err = ValidateProfile(profile)
		if err != nil {
			return location, forecast, profile, mode, zip, err
		}
		location = object(profile["location"])
		forecast = object(profile["forecast"])
		mode = stringOf(profile["mode"])
		zip = profile["zip_code"]
		return location, forecast, profile, mode, zip, err
	}
	location, err = state.Read("location.json", 8192)
	if err != nil {
		return location, forecast, profile, mode, zip, err
	}
	mode = "default"
	if location == nil {
		location = weather.DefaultLocation()
	} else {
		mode = "custom"
		location, err = weather.ValidateLocation(location)
		if err != nil {
			return location, forecast, profile, mode, zip, err
		}
	}
	var identity M
	identity, err = readZIPIdentity(state, location)
	if err != nil {
		return location, forecast, profile, mode, zip, err
	}
	if identity != nil {
		mode, zip = "zip", identity["zip_code"]
	}
	forecast, err = state.Read("forecast.json", weather.MaxBytes)
	if err != nil {
		return location, forecast, profile, mode, zip, err
	}
	if forecast != nil {
		if mode == "default" && !reflect.DeepEqual(forecast["location"], location) {
			err = weather.ValidateSnapshot(forecast, object(forecast["location"]))
			forecast = nil
			return location, forecast, profile, mode, zip, err
		}
		err = weather.ValidateSnapshot(forecast, location)
	}
	return location, forecast, profile, mode, zip, err
}

// New restores bounded saved state and starts configured asynchronous work.
// New atomically migrates legacy state when needed, including offline startup.
// The caller holds the state owner lock and closes App before closing state.
// Options are copied and must not change after construction; backend objects
// must outlive Close.
func New(state *safeio.Directory, o Options) (*App, error) {
	return newApp(state, o, false)
}

func newApp(state *safeio.Directory, o Options, primaryOnly bool) (*App, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Fetch == nil && o.FetchCountry == nil {
		o.FetchCountry = weather.FetchForecastForCountry
		o.FetchAlerts = weather.FetchAlerts
	}
	if o.Resolve == nil && o.ResolveSelection == nil {
		o.ResolveSelection = weather.ResolveSelection
	}
	if o.SearchPlaces == nil {
		o.SearchPlaces = weather.SearchPlaces
	}
	a := &App{state: state, options: o, primaryOnly: primaryOnly, presented: !primaryOnly, controls: DefaultControls(), results: make(chan completion, 8), forecastDone: make(chan *forecastWork, 2), forecastJobs: make(map[*forecastWork]bool), alertSlots: make(chan struct{}, 2), Changed: make(chan struct{}, 1), launcherStatus: "ready"}
	if e := a.restoreLocations(); e != nil {
		return nil, e
	}
	var e error
	a.search.init()
	a.initAirQuality()
	a.initMap()
	a.controls, e = readControls(state, a.country)
	if e != nil {
		return nil, e
	}
	a.notifications = notifications.New(state, o.Sender)
	if a.mode == "auto" && !o.Offline {
		a.beginPointFetch(a.forecastPoint, M{"mode": "auto", "zip_code": nil}, false)
	}
	if o.Effects != nil && !primaryOnly {
		a.fx = newEffectsCoordinator(o.Effects, a.signal)
	}
	a.snapshotLocked()
	if !primaryOnly {
		a.beginUpdateCheck(false)
	}
	return a, nil
}
func (a *App) signal() {
	select {
	case a.Changed <- struct{}{}:
	default:
	}
}
func (a *App) effectsStatus() M {
	if a.fx == nil {
		return M{"state": "stopped", "persistent": false}
	}
	return a.fx.Status()
}
func (a *App) setupStatus() M {
	if a.fx == nil {
		return M{"status": "unchecked", "reason": "not_checked", "outputs": []any{}, "selected_output": nil}
	}
	return a.fx.SetupSnapshot()
}
func (a *App) selected(live bool) M {
	return a.selectedPoint(a.forecastPoint, live)
}
func (a *App) selectedPoint(p *forecastPoint, live bool) M {
	mode := stringOf(a.controls["mode"])
	if live || a.effectsStatus()["persistent"] == true {
		mode = "live"
	}
	v := weather.SelectView(p.forecast, a.options.Now(), mode, weather.Manual(stringOf(object(a.controls["manual"])["condition"])), stringOf(a.controls["strength"]), a.controls["reduced_motion"] == true, a.controls["lightning_enabled"] == true)
	if p.forecast == nil && p.location != nil {
		solar := weather.SolarPosition(a.options.Now(), p.location["latitude"].(float64), p.location["longitude"].(float64))
		v["solar"] = solar
		if mode == "live" {
			fx := object(v["effects"])
			fx["sun_elevation"] = solar["elevation_deg"]
			fx["sun_azimuth"] = solar["azimuth_deg"]
			fx["is_day"] = solar["elevation_deg"].(float64) >= 0
		}
	}
	return v
}

// Tick advances timers and admits asynchronous weather/effects work.
// It respects cancellation before acquiring the application mutation lock.
func (a *App) Tick(ctx context.Context) {
	if a.mu.LockContext(ctx) != nil {
		return
	}
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.poll()
	a.beginUpdateCheck(false)
	a.refreshDuePoints()
	a.tickNotifications()
	a.updateEffectsLocked()
}
func (a *App) Interval() time.Duration { return a.interval(false) }
func (a *App) interval(presented bool) time.Duration {
	if !a.mu.TryLock() {
		return 100 * time.Millisecond
	}
	defer a.mu.Unlock()
	if updateInProgress(a.updateSnapshot()) {
		return time.Second
	}
	if a.effectsStatus()["state"] == "running" {
		// Native policy/status has its own independent 500 ms coordinator.
		// Poll weather and notification inputs at 1 Hz; fresh data is forwarded
		// to that coordinator as soon as it is observed.
		return time.Second
	}
	now := a.options.Now()
	d := a.nextPointFetch().Sub(now)
	if a.options.Offline || d <= 0 {
		d = time.Minute
	}
	if d > time.Minute {
		d = time.Minute
	}
	if a.notifications.Enabled() {
		if notificationDelay := a.notifications.Interval(now); notificationDelay < d {
			d = notificationDelay
		}
		// Visible watching previously received a clock/freshness snapshot every
		// five seconds. Preserve that cadence independently of watcher deadlines.
		if presented && d > 5*time.Second {
			d = 5 * time.Second
		}
	}
	return d
}
func (a *App) updateEffectsLocked() {
	// Start requests capture their own current weather/controls. While stopped
	// there is no native lease to refresh, so do not clone a full forecast on
	// every notification tick merely to prepare an unused effects packet.
	if a.fx != nil && a.fx.Status()["state"] == "running" {
		a.fx.update(a.selectedPoint(a.primary, false), controlsForCountry(a.controls, a.primary.country))
	}
}
func (a *App) snapshotPrivateLocked() M {
	v := a.snapshot()
	a.revision++
	v["snapshot_revision"] = float64(a.revision)
	a.cacheMu.Lock()
	// Keep the normalized display tree private. Only snapshotLocked makes
	// caller-owned structural copies; the server encodes under the ownership
	// lock. File and socket decoding retain their strict validators.
	a.cached = v
	a.cacheMu.Unlock()
	return v
}

func (a *App) snapshotLocked() M {
	return weather.Clone(a.snapshotPrivateLocked()).(M)
}

// Snapshot returns an independent JSON snapshot without running native commands
// while the app mutex is held. If a filesystem
// mutation is in progress, readers receive the last complete snapshot.
func (a *App) Snapshot() M {
	if a.mu.TryLock() {
		defer a.mu.Unlock()
		a.poll()
		return a.snapshotLocked()
	}
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	if a.cached == nil {
		return nil
	}
	return weather.Clone(a.cached).(M)
}

// Handle validates a protocol request and serializes its state mutation.
// Cancellation before admission prevents mutation. The returned bool requests
// service shutdown; a successful response does not itself close the App.
// Callers must not mutate request until Handle returns.
func (a *App) Handle(ctx context.Context, request M) (M, bool) {
	return a.handle(ctx, request, false)
}

// The server defers only a valid subscribe snapshot until atomic registration.
func (a *App) handle(ctx context.Context, request M, deferSubscribe bool) (M, bool) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	id, ok := request["request_id"].(float64)
	reply := M{"version": float64(1), "request_id": request["request_id"], "ok": false, "error": "invalid_request"}
	if !ok || id < 0 || id > 2147483647 || math.Trunc(id) != id || request["version"] != float64(1) {
		return reply, false
	}
	op := stringOf(request["op"])
	allowed := map[string]string{"acknowledge_update": "installed", "set_controls": "controls", "set_notifications": "notifications", "set_location": "location", "add_location": "location", "saved_location": "location", "search_places": "search", "select_output": "output", "start_effects": "duration"}
	extra := allowed[op]
	for k := range request {
		if k != "version" && k != "request_id" && k != "op" && k != extra {
			return reply, false
		}
	}
	if extra != "" {
		if _, ok = request[extra]; !ok {
			return reply, false
		}
	}
	if e := a.mu.LockContext(ctx); e != nil {
		return M{"version": 1.0, "request_id": id, "ok": false, "error": "request_timeout"}, false
	}
	locked := true
	defer func() {
		if locked {
			a.mu.Unlock()
		}
	}()
	a.poll()
	if a.closed {
		return M{"version": float64(1), "request_id": id, "ok": false, "error": "service_closed"}, false
	}
	if op == "check_effects" || op == "select_output" || op == "start_effects" || op == "start_live_effects" || op == "stop_effects" {
		duration := 300
		if op == "start_effects" {
			n, ok := request["duration"].(float64)
			if !ok || n < 1 || n > 300 || math.Trunc(n) != n {
				return reply, false
			}
			duration = int(n)
		}
		if op == "stop_effects" && a.fetchBusy && a.fetchCancel != nil {
			a.cancelPointFetch(a.forecastPoint)
		}
		if a.fx == nil {
			if op == "stop_effects" {
				return M{"version": 1.0, "request_id": id, "ok": true, "snapshot": a.snapshotLocked()}, false
			}
			reply["error"] = "effects_failed"
			return reply, false
		}
		selected := a.selectedPoint(a.primary, op == "start_live_effects")
		flags := safeio.Clone(controlsForCountry(a.controls, a.primary.country))
		a.fx.update(selected, flags)
		action := effectAction{op: op, output: stringOf(request["output"]), duration: duration, weather: effectWeather(selected), controls: flags}
		locked = false
		a.mu.Unlock()
		result := a.fx.submit(ctx, action)
		if e := a.mu.LockContext(ctx); e != nil {
			return M{"version": 1.0, "request_id": id, "ok": false, "error": "request_timeout"}, false
		}
		locked = true
		a.poll()
		a.signal()
		reply = M{"version": 1.0, "request_id": id, "ok": result.err == nil, "snapshot": a.snapshotLocked()}
		if result.err != nil {
			reply["error"] = result.code
		}
		return reply, false
	}
	var e error
	code := "invalid_request"
	switch op {
	case "snapshot", "subscribe":
		if !a.options.Now().Before(a.nextFetch) {
			a.beginFetch(nil)
		}
	case "refresh":
		a.beginFetch(nil)
	case "refresh_primary":
		if a.primary.mode != "default" && (a.primary.needsResolve || !a.options.Now().Before(a.primary.nextFetch)) {
			a.barRefreshUntil = a.options.Now().Add(30 * time.Second)
			a.beginPointFetch(a.primary, nil, false)
		}
	case "set_controls":
		var v M
		v, e = PatchControls(a.controls, object(request["controls"]))
		if e == nil {
			v = controlsForCountry(v, a.country)
			code = "state_io_failed"
			e = a.state.Write("controls.json", v, 8192)
			if e == nil {
				a.controls = v
			}
		}
	case "set_location", "add_location":
		var v M
		v, e = weather.ValidateSelection(object(request["location"]))
		if e == nil && v["mode"] == "place" {
			e = a.consumePlace(v)
			if e != nil {
				code = "stale_selection"
				a.locationError = code
			}
		}
		if e == nil && len(a.saved.doc["places"].([]any)) >= savedLocationLimit && savedEntry(a.saved.doc, forecastWorkKey(a.forecastPoint, v)) == nil {
			code, e = "location_limit", errors.New("saved location limit reached")
		}
		if e == nil && op == "add_location" && a.options.Offline {
			code, e = "offline", errors.New("location lookup unavailable offline")
		}
		if e == nil {
			a.closeMap()
			a.cancelSearch()
			a.beginLocation(v, op == "set_location")
		}
	case "saved_location":
		e = a.savedLocationAction(object(request["location"]))
		if e != nil && !errors.Is(e, errInvalidSavedAction) {
			code = "state_io_failed"
			if errors.Is(e, errSavedLocationsUnconfirmed) {
				code = "save_unconfirmed"
			}
		}
	case "map_open":
		a.openMap()
	case "map_close":
		a.closeMap()
	case "search_places":
		var v M
		v, e = weather.ValidatePlaceSearch(object(request["search"]))
		if e == nil {
			a.beginSearch(v)
		}
	case "cancel_place_search":
		a.cancelSearch()
	case "set_notifications":
		e = a.notifications.Configure(object(request["notifications"]))
	case "snooze_notifications", "resume_notifications":
		e = a.notifications.Snooze(a.options.Now(), op == "resume_notifications")
	case "acknowledge_update":
		if a.options.AcknowledgeUpdate == nil {
			reply["error"] = "updates_unavailable"
			return reply, false
		}
		e = a.options.AcknowledgeUpdate(stringOf(request["installed"]))
	case "check_updates":
		if a.options.CheckUpdates == nil {
			reply["error"] = "updates_unavailable"
			return reply, false
		}
		a.beginUpdateCheck(true)
	case "install_update":
		if a.updates.installing || updateInProgress(a.updateSnapshot()) {
			reply["error"] = "update_in_progress"
			return reply, false
		}
		if a.options.StartUpdate == nil || a.options.StartUpdate() != nil {
			reply["error"] = "update_start_failed"
			return reply, false
		}
		a.updates.installing = true
		a.updates.installStarted = a.options.Now()
		a.updates.installError = ""
	case "install_launcher":
		a.launcherStatus = InstallLauncher(a.options.Root)
	case "quit":
		return M{"version": float64(1), "request_id": id, "ok": true}, true
	default:
		return reply, false
	}
	a.tickNotifications()
	a.updateEffectsLocked()
	if op != "snapshot" && op != "subscribe" {
		a.signal()
	}
	if op == "subscribe" && deferSubscribe && e == nil {
		return M{"version": 1.0, "request_id": id, "ok": true}, false
	}
	reply = M{"version": float64(1), "request_id": id, "ok": e == nil, "snapshot": a.snapshotLocked()}
	if e != nil {
		reply["error"] = code
	}
	return reply, false
}

// Close cancels owned work and stops effects within CloseBudget or the caller deadline.
// Concurrent calls wait for the same cleanup result. It does not close the
// caller-owned state directory; native recovery may reserve independent time.
func (a *App) Close(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, CloseBudget)
	defer cancel()
	if e := a.mu.LockContext(ctx); e != nil {
		return e
	}
	if a.closed {
		done := a.closeDone
		a.mu.Unlock()
		if done != nil {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		a.cacheMu.RLock()
		defer a.cacheMu.RUnlock()
		return a.closeErr
	}
	a.closed = true
	a.closeDone = make(chan struct{})
	a.cancelAirQuality()
	a.closeMap()
	for work := range a.forecastJobs {
		work.cancel()
	}
	a.cancelPointFetch(a.forecastPoint)
	if a.primary != a.forecastPoint {
		a.cancelPointFetch(a.primary)
	}
	a.cancelSearch()
	e := a.notifications.Close()
	a.mu.Unlock()
	if a.fx != nil {
		e = errors.Join(e, a.fx.close(ctx))
	}
	a.cacheMu.Lock()
	a.closeErr = e
	a.cacheMu.Unlock()
	close(a.closeDone)
	return e
}
