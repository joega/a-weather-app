// Package app owns weather state and serializes mutations independently of Qt.
package app

import (
	"context"
	"errors"
	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
	"github.com/joega/a-weather-app/internal/weathermap"
	"math"
	"reflect"
	"sync"
	"time"
)

type M = map[string]any
type Effects interface {
	SetupSnapshot() M
	Check(context.Context) M
	SelectOutput(context.Context, string) (M, error)
	Status() M
	Start(context.Context, int, bool, M) error
	Tick(context.Context, M, M) error
	Stop(context.Context) error
}
type Options struct {
	UpdateStatus     func() M
	CheckUpdates     func(context.Context, bool) error
	StartUpdate      func() error
	Root             string
	Now              func() time.Time
	Fetch            func(context.Context, M, time.Time) (M, error)
	Resolve          func(context.Context, M) (M, error)
	ResolveSelection func(context.Context, M) (M, error)
	FetchCountry     func(context.Context, M, time.Time, string) (M, error)
	// Nil leaves air quality cache-only. The production service supplies Fetch.
	FetchAirQuality func(context.Context, M, time.Time) (M, error)
	FetchMap        func(context.Context, float64, float64, string, time.Time) (weathermap.Data, error)
	SearchPlaces    func(context.Context, M) ([]any, error)
	Effects         Effects
	Sender          notifications.Sender
	Offline         bool
}
type completion struct {
	generation                    uint64
	selection, location, forecast M
	err                           error
	country                       any
	place                         M
}
type App struct {
	mu                                    contextMutex
	cacheMu                               sync.RWMutex
	cached                                M
	displayRows                           displayRows
	revision                              uint64
	fx                                    *effectsCoordinator
	closeDone                             chan struct{}
	state                                 *safeio.Directory
	options                               Options
	location, forecast, profile, controls M
	mode                                  string
	zip                                   any
	country                               any
	place                                 M
	search                                placeSearch
	aq                                    airQualityState
	wmap                                  mapState
	errorCode, locationError              any
	notifications                         *notifications.Watcher
	nextFetch                             time.Time
	fetchCancel                           context.CancelFunc
	fetchBusy, locationBusy               bool
	generation                            uint64
	results                               chan completion
	Changed                               chan struct{}
	launcherStatus                        string
	updates                               updateState
	closed                                bool
	closeErr                              error
}

func DefaultControls() M {
	return M{"mode": "live", "strength": "subtle", "manual": M{"condition": "rain"}, "reduced_motion": false, "lightning_enabled": false, "fps": float64(30), "window_physics": true, "accumulation": true, "pause_fullscreen": true, "units": "F", "units_mode": "auto", "wind_units": "auto"}
}
func stringOf(v any) string { s, _ := v.(string); return s }
func object(v any) M        { m, _ := v.(map[string]any); return m }
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
func ValidateProfile(v M) error {
	v1 := v["schema_version"] == float64(1) && len(v) == 5
	v2 := v["schema_version"] == float64(2) && len(v) == 7
	if !v1 && !v2 {
		return errors.New("location profile")
	}
	for k := range v {
		if k != "schema_version" && k != "mode" && k != "zip_code" && k != "location" && k != "forecast" && !(v2 && (k == "country_code" || k == "place")) {
			return errors.New("location profile fields")
		}
	}
	if v2 {
		if !validCountry(v["country_code"]) || (v["mode"] == "zip" && v["country_code"] != "US") {
			return errors.New("location country")
		}
		if v["mode"] == "place" {
			p := object(v["place"])
			if len(p) != 2 || p["provider"] != "open-meteo" || !placeInteger(p["id"], 1) || v["country_code"] == nil || v["zip_code"] != nil {
				return errors.New("place identity")
			}
		} else if v["place"] != nil {
			return errors.New("unexpected place identity")
		}
	}
	selection := M{"mode": v["mode"]}
	if v["mode"] == "zip" {
		selection["zip_code"] = v["zip_code"]
	}
	if v["mode"] == "custom" || v["mode"] == "default" || (v2 && v["mode"] == "place") {
		if v["zip_code"] != nil {
			return errors.New("unexpected ZIP")
		}
	} else {
		s, e := weather.ValidateSelection(selection)
		if e != nil || s["zip_code"] != v["zip_code"] {
			return errors.New("location selection")
		}
	}
	location, e := weather.ValidateLocation(object(v["location"]))
	if e != nil {
		return e
	}
	return weather.ValidateSnapshot(object(v["forecast"]), location)
}
func readSaved(state *safeio.Directory) (location, forecast, profile M, mode string, zip any, err error) {
	profile, err = state.Read("location-profile.json", weather.MaxBytes)
	if err != nil {
		return
	}
	if profile != nil {
		err = ValidateProfile(profile)
		if err != nil {
			return
		}
		location = object(profile["location"])
		forecast = object(profile["forecast"])
		mode = stringOf(profile["mode"])
		zip = profile["zip_code"]
		return
	}
	location, err = state.Read("location.json", 8192)
	if err != nil {
		return
	}
	mode = "default"
	if location == nil {
		location = weather.DefaultLocation()
	} else {
		mode = "custom"
		location, err = weather.ValidateLocation(location)
		if err != nil {
			return
		}
	}
	var identity M
	identity, err = readZIPIdentity(state, location)
	if err != nil {
		return
	}
	if identity != nil {
		mode, zip = "zip", identity["zip_code"]
	}
	forecast, err = state.Read("forecast.json", weather.MaxBytes)
	if err != nil {
		return
	}
	if forecast != nil {
		if mode == "default" && !reflect.DeepEqual(forecast["location"], location) {
			err = weather.ValidateSnapshot(forecast, object(forecast["location"]))
			forecast = nil
			return
		}
		err = weather.ValidateSnapshot(forecast, location)
	}
	return
}
func New(state *safeio.Directory, o Options) (*App, error) {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Fetch == nil && o.FetchCountry == nil {
		o.FetchCountry = weather.FetchForCountry
	}
	if o.Resolve == nil && o.ResolveSelection == nil {
		o.ResolveSelection = weather.ResolveSelection
	}
	if o.SearchPlaces == nil {
		o.SearchPlaces = weather.SearchPlaces
	}
	a := &App{state: state, options: o, controls: DefaultControls(), results: make(chan completion, 8), Changed: make(chan struct{}, 1), launcherStatus: "ready"}
	var e error
	a.location, a.forecast, a.profile, a.mode, a.zip, e = readSaved(state)
	if e != nil {
		return nil, e
	}
	a.country, a.place = profileIdentity(a.profile, a.mode)
	a.search.init()
	a.initAirQuality()
	a.initMap()
	a.controls, e = readControls(state, a.country)
	if e != nil {
		return nil, e
	}
	a.notifications = notifications.New(state, o.Sender)
	a.nextFetch = o.Now()
	if a.forecast != nil {
		t, _ := weather.Instant(a.forecast["fetched_at"])
		age := math.Max(0, o.Now().Sub(t).Seconds())
		a.nextFetch = o.Now().Add(time.Duration(math.Max(0, 900-age) * float64(time.Second)))
	}
	if a.mode == "auto" && !o.Offline {
		a.beginFetch(M{"mode": "auto", "zip_code": nil})
	}
	if o.Effects != nil {
		a.fx = newEffectsCoordinator(o.Effects, a.signal)
	}
	a.snapshotLocked()
	a.beginUpdateCheck(false)
	return a, nil
}
func (a *App) signal() {
	select {
	case a.Changed <- struct{}{}:
	default:
	}
}
func (a *App) beginFetch(selection M) {
	if a.closed || a.options.Offline {
		return
	}
	if selection == nil && (a.fetchBusy || a.locationBusy) {
		return
	}
	if a.fetchCancel != nil {
		a.fetchCancel()
	}
	a.generation++
	gen := a.generation
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	a.fetchCancel = cancel
	a.fetchBusy = selection == nil
	a.locationBusy = selection != nil
	a.nextFetch = a.options.Now().Add(900 * time.Second)
	if selection != nil {
		a.locationError = nil
		a.cancelAirQuality()
	}
	location := safeio.Clone(a.location)
	country, place := a.country, safeio.Clone(a.place)
	selection = safeio.Clone(selection)
	now := a.options.Now()
	a.signal()
	go func() {
		defer cancel()
		var e error
		if selection != nil {
			requestSelection := safeio.Clone(selection)
			if requestSelection["mode"] == "auto" {
				delete(requestSelection, "zip_code")
			}
			if a.options.ResolveSelection != nil {
				var resolved M
				resolved, e = a.options.ResolveSelection(ctx, requestSelection)
				if e == nil {
					location, e = weather.ValidateLocation(object(resolved["location"]))
					country, place = resolved["country_code"], object(resolved["place"])
					if !validCountry(country) {
						e = errors.New("invalid resolved country")
					}
				}
			} else {
				location, e = a.options.Resolve(ctx, requestSelection)
				country, place = profileIdentity(nil, stringOf(requestSelection["mode"]))
			}
		}
		var forecast M
		if e == nil {
			if a.options.FetchCountry != nil {
				forecast, e = a.options.FetchCountry(ctx, location, now, stringOf(country))
			} else {
				forecast, e = a.options.Fetch(ctx, location, now)
			}
		}
		if e == nil {
			e = weather.ValidateSnapshot(forecast, location)
		}
		if ctx.Err() != nil {
			e = ctx.Err()
		}
		c := completion{generation: gen, selection: selection, location: location, forecast: forecast, err: e, country: country, place: place}
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
	}()
}
func (a *App) poll() {
	a.pollUpdates()
	defer a.pollAirQuality()
	defer a.pollMap()
	a.pollSearch()
	for {
		select {
		case c := <-a.results:
			if c.generation != a.generation || a.closed {
				continue
			}
			a.fetchBusy = false
			a.locationBusy = false
			a.fetchCancel = nil
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
					a.locationError = code
				} else {
					a.errorCode = code
				}
				a.signal()
				continue
			}
			var candidate M
			if c.selection != nil {
				candidate = M{"schema_version": float64(2), "mode": c.selection["mode"], "zip_code": c.selection["zip_code"], "location": c.location, "forecast": c.forecast, "country_code": c.country, "place": nil}
				if c.place != nil {
					candidate["place"] = c.place
				}
			} else if a.profile != nil {
				candidate = safeio.Clone(a.profile)
				candidate["schema_version"] = float64(2)
				candidate["country_code"] = a.country
				candidate["place"] = nil
				if a.place != nil {
					candidate["place"] = safeio.Clone(a.place)
				}
				candidate["forecast"] = c.forecast
			} else if a.mode == "zip" {
				candidate = M{"schema_version": 2.0, "mode": "zip", "zip_code": a.zip, "location": c.location, "forecast": c.forecast, "country_code": "US", "place": nil}
			}
			name, value := "forecast.json", c.forecast
			if candidate != nil {
				name, value = "location-profile.json", candidate
			}
			var e error
			if candidate != nil {
				e = ValidateProfile(candidate)
				if e == nil {
					e = a.backupLegacyProfile()
				}
			}
			if e == nil {
				e = a.state.Write(name, value, weather.MaxBytes)
			}
			if e != nil {
				if c.selection != nil {
					a.locationError = "state_io_failed"
					actual, readErr := a.state.Read(name, weather.MaxBytes)
					if readErr == nil && reflect.DeepEqual(actual, candidate) {
						a.adopt(candidate)
						a.locationError = "save_unconfirmed"
					}
				} else {
					a.errorCode = "refresh_failed"
				}
				a.signal()
				continue
			}
			if candidate != nil {
				a.adopt(candidate)
			} else {
				a.forecast = c.forecast
				a.errorCode = nil
			}
			a.signal()
		default:
			return
		}
	}
}
func (a *App) adopt(v M) {
	oldLocation := a.location
	a.profile = v
	a.location = object(v["location"])
	a.forecast = object(v["forecast"])
	a.mode = stringOf(v["mode"])
	a.zip = v["zip_code"]
	a.country, a.place = profileIdentity(v, a.mode)
	a.controls = controlsForCountry(a.controls, a.country)
	a.errorCode = nil
	a.locationError = nil
	a.nextFetch = a.options.Now().Add(900 * time.Second)
	if !reflect.DeepEqual(oldLocation, a.location) {
		a.mapLocationChanged(oldLocation)
		a.cancelAirQuality()
		a.aq.record, a.aq.errorCode = nil, nil
		a.aq.nextFetch = a.options.Now()
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
	mode := stringOf(a.controls["mode"])
	if live || a.effectsStatus()["persistent"] == true {
		mode = "live"
	}
	v := weather.SelectView(a.forecast, a.options.Now(), mode, weather.Manual(stringOf(object(a.controls["manual"])["condition"])), stringOf(a.controls["strength"]), a.controls["reduced_motion"] == true, a.controls["lightning_enabled"] == true)
	if a.forecast == nil && a.location != nil {
		solar := weather.SolarPosition(a.options.Now(), a.location["latitude"].(float64), a.location["longitude"].(float64))
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
	if !a.options.Now().Before(a.nextFetch) {
		a.beginFetch(nil)
	}
	a.notifications.Tick(a.forecast, a.location, a.options.Now(), a.errorCode == nil && a.locationError == nil && !a.locationBusy)
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
	d := a.nextFetch.Sub(now)
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
		a.fx.update(a.selected(false), a.controls)
	}
}
func (a *App) snapshotLocked() M {
	v := a.snapshot()
	a.revision++
	v["snapshot_revision"] = float64(a.revision)
	a.cacheMu.Lock()
	// Keep the normalized display tree private. Callers receive a structural
	// copy without encoding and tokenizing the forecast again. File and socket
	// decoding retain their strict validators.
	a.cached = v
	a.cacheMu.Unlock()
	return weather.Clone(v).(M)
}

// Native commands never run while the app mutex is held. If a filesystem
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
func (a *App) Handle(ctx context.Context, request M) (M, bool) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	id, ok := request["request_id"].(float64)
	reply := M{"version": float64(1), "request_id": request["request_id"], "ok": false, "error": "invalid_request"}
	if !ok || id < 0 || id > 2147483647 || math.Trunc(id) != id || request["version"] != float64(1) {
		return reply, false
	}
	op := stringOf(request["op"])
	allowed := map[string]string{"set_controls": "controls", "set_notifications": "notifications", "set_location": "location", "search_places": "search", "select_output": "output", "start_effects": "duration"}
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
			a.fetchCancel()
			a.generation++
			a.fetchBusy = false
			a.fetchCancel = nil
		}
		if a.fx == nil {
			if op == "stop_effects" {
				return M{"version": 1.0, "request_id": id, "ok": true, "snapshot": a.snapshotLocked()}, false
			}
			reply["error"] = "effects_failed"
			return reply, false
		}
		selected := a.selected(op == "start_live_effects")
		flags := safeio.Clone(a.controls)
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
	case "set_location":
		var v M
		v, e = weather.ValidateSelection(object(request["location"]))
		if e == nil && v["mode"] == "place" {
			e = a.consumePlace(v)
			if e != nil {
				code = "stale_selection"
				a.locationError = code
			}
		}
		if e == nil {
			a.closeMap()
			a.cancelSearch()
			a.beginFetch(v)
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
	a.notifications.Tick(a.forecast, a.location, a.options.Now(), a.errorCode == nil && a.locationError == nil && !a.locationBusy)
	a.updateEffectsLocked()
	if op != "snapshot" && op != "subscribe" {
		a.signal()
	}
	reply = M{"version": float64(1), "request_id": id, "ok": e == nil, "snapshot": a.snapshotLocked()}
	if e != nil {
		reply["error"] = code
	}
	return reply, false
}
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
	if a.fetchCancel != nil {
		a.fetchCancel()
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
