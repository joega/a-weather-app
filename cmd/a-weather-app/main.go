package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/app"
	"github.com/joega/a-weather-app/internal/effects"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/precipitation"
	"github.com/joega/a-weather-app/internal/radar"
	"github.com/joega/a-weather-app/internal/release"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
	"github.com/joega/a-weather-app/internal/updater"
	"github.com/joega/a-weather-app/internal/weather"
	"github.com/joega/a-weather-app/internal/weathermap"
)

type M = map[string]any

// Package builds set this with -X. An installation directory may have any name;
// only a development binary uses the source checkout's build/ convention.
var buildMode = "development"
var appVersion = "0.62.2"

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	defer weather.CloseIdleConnections()
	defer airquality.CloseIdleConnections()
	defer precipitation.CloseIdleConnections()
	if len(args) > 0 && args[0] == "--effects-worker" {
		return effects.RunWorker(args[1:])
	}
	if len(args) > 0 && args[0] == "--guardian" {
		return supervision.RunGuardian(args[1:])
	}
	if e := launch(args); e != nil {
		fmt.Fprintln(os.Stderr, "A Weather App:", e)
		return 1
	}
	return 0
}
func defaultState() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".local/state")
	}
	return filepath.Join(base, "a-weather-app")
}
func rootPath() string {
	exe, e := os.Executable()
	if e != nil {
		return ""
	}
	exe, e = filepath.EvalSymlinks(exe)
	if e != nil {
		return ""
	}
	return executableRoot(exe, buildMode)
}
func executableRoot(exe, mode string) string {
	root := filepath.Dir(exe)
	if mode == "development" && filepath.Base(root) == "build" {
		return filepath.Dir(root)
	}
	return root
}
func runtimePath(state string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if !filepath.IsAbs(base) {
		base = "/tmp"
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(state)))
	return filepath.Join(base, fmt.Sprintf("a-weather-app-%d-%s", os.Geteuid(), hash[:16]))
}
func childEnvironment() []string {
	// These are request-driven services, not parallel compute workers. Keep the
	// Go runtime's scheduler sized to their actual workload instead of allowing
	// host CPU count to create unnecessary runnable Ps and scheduler work.
	env := []string{"PATH=/usr/bin:/bin", "GOMAXPROCS=1"}
	for _, k := range []string{"HOME", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "WAYLAND_DISPLAY", "DISPLAY", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "DBUS_SESSION_BUS_ADDRESS", "HYPRLAND_INSTANCE_SIGNATURE", "QT_QPA_PLATFORM", "QT_QPA_PLATFORMTHEME", "QT_QUICK_BACKEND", "QT_QUICK_CONTROLS_STYLE", "QT_IM_MODULE", "A_WEATHER_APP_QML_DIAGNOSTIC"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}
func serviceEnvironment() []string {
	env := childEnvironment()
	// Weather downloads retain explicit user network configuration. The IP
	// location transport separately disables proxies, as in the existing app.
	for _, k := range []string{"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// launch resolves verified runtime/state and dispatches the selected operation.
func launch(args []string) error {
	options, err := parseLaunchOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if options.version {
		fmt.Println("A Weather App " + appVersion)
		return nil
	}

	statePath := options.stateDir
	if statePath == "" {
		statePath = defaultState()
		if options.zipCode != "" {
			statePath = filepath.Join(statePath, "locations", options.zipCode)
		}
		if options.demoLocation != "" {
			statePath = filepath.Join(statePath, "demos", options.demoLocation)
		}
	}
	if !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath {
		return errors.New("state directory must be absolute and normalized")
	}
	runtimeDir := runtimePath(statePath)
	updates := updater.Config{Installed: appVersion, Development: buildMode == "development", StatePath: statePath}
	socket := filepath.Join(runtimeDir, "service.sock")
	if options.printSocket {
		fmt.Println(socket)
		return nil
	}
	if options.bar {
		state, e := safeio.OpenDir(statePath, false)
		if e == nil {
			defer state.Close()
		}
		value := app.Bar(state, time.Now())
		status := updates.Status()
		value["update"] = status.Map()
		if status.State == "available" {
			value["tooltip"], _ = weather.Plain(fmt.Sprintf("Update %s available — open the app to install · %s", status.Available, value["tooltip"]), 256, false)
		}
		return json.NewEncoder(os.Stdout).Encode(value)
	}
	root := options.root
	if root == "" {
		root = rootPath()
	}
	if !filepath.IsAbs(root) {
		return errors.New("application root unavailable")
	}
	root, e := filepath.EvalSymlinks(root)
	if e != nil {
		return e
	}
	pluginRoot := updater.DefaultPluginRoot()
	if _, err := os.Lstat(pluginRoot); errors.Is(err, os.ErrNotExist) {
		pluginRoot = ""
	}
	installation := &updater.LinuxInstallation{Config: updates, RuntimeRoot: root, DataRoot: updater.DefaultDataRoot(), PluginRoot: pluginRoot, BinRoot: updater.DefaultBinRoot(), Socket: socket}
	engine := updater.Engine{Config: updates, Installation: installation}
	startUpdate := func(recover bool) error {
		if updates.Development {
			return errors.New("development checkouts are built locally")
		}
		if err := verifyRuntime(root); err != nil {
			return err
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		args := []string{"--update-worker", "--state-dir", statePath}
		if recover {
			args = append(args, "--recover-updates")
		}
		return updater.StartDetached(executable, args, serviceEnvironment())
	}
	if options.updateWorker {
		if e = verifyRuntime(root); e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if options.bootstrapUpdate {
			updates, e = installation.Bootstrap()
			if e != nil {
				return e
			}
			engine.Config = updates
		}
		if options.recoverUpdates {
			return engine.Recover(ctx)
		}
		if options.bootstrapUpdate {
			// Never install merely because a plugin contains a pin. Confirm that
			// it is supported by the currently published stable release first.
			var status updater.Status
			if status, e = updates.Check(ctx, true); e != nil {
				return e
			}
			if status.State != "available" {
				return errors.New("no newer verified release is ready to install; check for updates again later")
			}
		}
		return engine.Run(ctx)
	}
	if options.installUpdate {
		return startUpdate(false)
	}
	if !updates.Development {
		needed, err := engine.NeedsRecovery()
		if err != nil {
			return fmt.Errorf("update recovery: %w", err)
		}
		if needed {
			return startUpdate(true)
		}
	}
	if options.checkUpdates {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		status, err := updates.Check(ctx, options.forceUpdateCheck)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status.Map())
	}
	if options.refreshBar {
		return refreshSavedBar(root, statePath, runtimeDir)
	}
	if !options.service {
		op := "toggle_window"
		budget := 2 * time.Second
		if options.stop {
			op = "stop_effects"
			budget = 120 * time.Second
		}
		if options.quit {
			op = "quit"
			budget = 90 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), budget)
		reply, err := ipc.Call(ctx, socket, M{"op": op})
		cancel()
		if err == nil {
			if reply["ok"] != true {
				return fmt.Errorf("%s failed", op)
			}
			return nil
		}
		if options.stop || options.quit {
			return errors.New("weather service unavailable")
		}
		if options.toggle && !mayStartAfterIPCError(socket, err) {
			return fmt.Errorf("toggle unavailable: %w", err)
		}
	}
	// Only the explicitly built development executable may use source-tree
	// assets. An installed bundle must never fall back after a missing or broken
	// integrity manifest, including when a development root override is given.
	if e = verifyRuntime(root); e != nil {
		return fmt.Errorf("runtime package verification failed: %w", e)
	}
	state, e := safeio.OpenDir(statePath, true)
	if e != nil {
		return e
	}
	defer state.Close()
	if options.zipCode != "" || options.demoLocation != "" {
		if e = prepareLocation(state, options.zipCode, options.demoLocation); e != nil {
			return e
		}
	}
	runtimeState, e := safeio.OpenDir(runtimeDir, true)
	if e != nil {
		return e
	}
	defer runtimeState.Close()
	if !options.service {
		exe, e := os.Executable()
		if e != nil {
			return e
		}
		childArgs := []string{exe, "--service", "--state-dir", statePath, "--root", root}
		if options.instance != "" {
			childArgs = append(childArgs, "--instance", options.instance)
		}
		if options.output != "" {
			childArgs = append(childArgs, "--output", options.output)
		}
		if options.offline {
			childArgs = append(childArgs, "--offline")
		}
		if options.headless {
			childArgs = append(childArgs, "--headless")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		return supervision.Guard(ctx, childArgs, serviceEnvironment(), statePath, time.Duration(options.duration)*time.Second)
	}
	lock, e := serviceLock(runtimeState)
	if e != nil {
		return errors.New("weather service already owns this state")
	}
	defer lock.Close()
	manager := effects.New(root, statePath, options.instance, options.output)
	radarClient := radar.New()
	defer radarClient.CloseIdleConnections()
	a, e := app.New(state, app.Options{Root: root, Effects: manager, Offline: options.offline, Radar: radarClient,
		UpdateStatus:       func() M { return updates.PresentationStatus().Map() },
		AcknowledgeUpdate:  updates.AcknowledgeUpdate,
		CheckUpdates:       func(ctx context.Context, force bool) error { _, err := updates.Check(ctx, force); return err },
		StartUpdate:        func() error { return startUpdate(false) },
		FetchPrecipitation: precipitation.Fetch,
		FetchAirQuality:    airquality.Fetch, FetchMap: func(ctx context.Context, lat, lon float64, country string, now time.Time) (weathermap.Data, error) {
			return weathermap.Fetch(ctx, nil, lat, lon, country, now)
		}})
	if e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if options.duration > 0 {
		var durationCancel context.CancelFunc
		ctx, durationCancel = context.WithTimeout(ctx, time.Duration(options.duration)*time.Second)
		defer durationCancel()
	}
	var child *supervision.Process
	var startErr error
	ready := func() {
		if options.headless {
			return
		}
		command := []string{filepath.Join(root, "native/qt/a-weather-app-qt"), "--socket", socket}
		if options.measureFrames {
			command = append(command, "--measure-frames")
		}
		if os.Getenv("A_WEATHER_APP_QML_DIAGNOSTIC") == "1" {
			command = append(command, "--diagnostic")
		}
		child, startErr = supervision.StartChild(command, childEnvironment(), root, nil, nil, os.Stderr)
		if startErr != nil {
			cancel()
			return
		}
		go func() {
			select {
			case <-ctx.Done():
			case <-child.Done():
				cancel()
			}
		}()
	}
	serveErr := app.Serve(ctx, socket, a, ready)
	if child != nil {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		e = child.Cleanup(cleanup, 2*time.Second)
		done()
	}
	return errors.Join(serveErr, startErr, e)
}

func verifyRuntime(root string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	if buildMode != "development" || executable != filepath.Join(root, "build", "a-weather-app") {
		return release.Verify(root)
	}
	return nil
}

func serviceLock(runtimeState *safeio.Directory) (*os.File, error) {
	lock, err := runtimeState.Lock("service.lock")
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		return lock, err
	}
	deadline := time.Now().Add(31 * time.Second)
	for time.Now().Before(deadline) {
		marker, markerErr := runtimeState.Lock("bar-refresh.lock")
		if markerErr == nil {
			marker.Close()
			return nil, err
		}
		if !errors.Is(markerErr, syscall.EWOULDBLOCK) {
			return nil, markerErr
		}
		time.Sleep(100 * time.Millisecond)
		lock, err = runtimeState.Lock("service.lock")
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return lock, err
		}
	}
	return nil, err
}

func mayStartAfterIPCError(socket string, err error) bool {
	// net.Dial wraps connect(ENOENT) in net.OpError/os.SyscallError when a
	// previous clean shutdown retained the private directory but unlinked the
	// socket. os.IsNotExist does not traverse that complete error chain.
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return false
	}
	// A stale socket is recoverable only in our private directory, and the
	// service still acquires its exclusive lock before removing it. Never turn
	// a protocol timeout or hostile endpoint into a second service instance.
	d, e := safeio.OpenDir(filepath.Dir(socket), false)
	if e != nil {
		return false
	}
	defer d.Close()
	info, e := os.Lstat(socket)
	if e != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0600 && st.Uid == uint32(os.Geteuid()) && st.Nlink == 1
}
func prepareLocation(state *safeio.Directory, zip, demo string) error {
	return prepareLocationWithResolver(state, zip, demo, weather.Resolve)
}

// resolve returns a validated location; the default retains provider restrictions.
func prepareLocationWithResolver(state *safeio.Directory, zip, demo string, resolve func(context.Context, M) (M, error)) error {
	var loc M
	var e error
	if zip != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		loc, e = resolve(ctx, M{"mode": "zip", "zip_code": zip})
		if e != nil {
			return e
		}
	} else {
		loc = M{"name": "Boston, MA", "latitude": 42.3601, "longitude": -71.0589, "timezone": "America/New_York"}
	}
	profile, e := app.ReadPrimaryProfile(state)
	if e != nil {
		return e
	}
	if profile != nil {
		b, _ := json.Marshal(profile["location"])
		c, _ := json.Marshal(loc)
		if string(b) != string(c) {
			return errors.New("state belongs to another location")
		}
		return nil
	}
	old, e := state.Read("location.json", 8192)
	if e != nil {
		return e
	}
	if old != nil {
		b, _ := json.Marshal(old)
		c, _ := json.Marshal(loc)
		if string(b) != string(c) {
			return errors.New("state belongs to another location")
		}
	}
	cached, e := state.Read("forecast.json", weather.MaxBytes)
	if e != nil {
		return e
	}
	if cached != nil {
		if e = weather.ValidateSnapshot(cached, loc); e != nil {
			return e
		}
	}
	if e = state.Write("location.json", loc, 8192); e != nil {
		return e
	}
	if zip != "" {
		return app.SaveZIPIdentity(state, zip, loc)
	}
	return nil
}

// refreshSavedBar shares the service lock and performs one bounded headless
// refresh. Missing state and another refresh/service owner are harmless no-ops.
func refreshSavedBar(root, statePath, runtimeDir string) error {
	state, err := safeio.OpenDir(statePath, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer state.Close()
	due, err := app.BarRefreshDue(state, time.Now())
	if err != nil || !due {
		return err
	}
	if err := verifyRuntime(root); err != nil {
		return fmt.Errorf("runtime package verification failed: %w", err)
	}
	// A running GUI service owns publication. This lock also
	// serializes separate bar instances on multiple monitors.
	runtimeState, err := safeio.OpenDir(runtimeDir, true)
	if err != nil {
		return err
	}
	defer runtimeState.Close()
	marker, err := runtimeState.Lock("bar-refresh.lock")
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil
	}
	if err != nil {
		return err
	}
	defer marker.Close()
	lock, err := runtimeState.Lock("service.lock")
	if errors.Is(err, syscall.EWOULDBLOCK) {
		// The service owns the state lock. Admit one primary-city refresh
		// through its bounded scheduler, even when the frontend browses elsewhere.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reply, err := ipc.Call(ctx, filepath.Join(runtimeDir, "service.sock"), M{"op": "refresh_primary"})
		if err != nil {
			return err
		}
		if reply["ok"] != true {
			return errors.New("bar refresh request failed")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	ctx, deadline := context.WithTimeout(ctx, 30*time.Second)
	defer deadline()
	return app.RefreshBarSaved(ctx, state, app.Options{})
}
