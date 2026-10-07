package main

import (
	"errors"
	"flag"
	"regexp"

	"github.com/joega/a-weather-app/internal/weather"
)

// launchOptions keeps flag parsing separate from state, runtime, and process
// ownership. Validation preserves the existing order, including --version.
type launchOptions struct {
	stateDir         string
	instance         string
	output           string
	bar              bool
	refreshBar       bool
	toggle           bool
	stop             bool
	quit             bool
	version          bool
	checkUpdates     bool
	forceUpdateCheck bool
	installUpdate    bool
	updateWorker     bool
	bootstrapUpdate  bool
	recoverUpdates   bool
	service          bool
	headless         bool
	offline          bool
	duration         int
	zipCode          string
	demoLocation     string
	root             string
	printSocket      bool
	measureFrames    bool
}

func parseLaunchOptions(args []string) (launchOptions, error) {
	var options launchOptions
	fs := flag.NewFlagSet("a-weather-app", flag.ContinueOnError)
	fs.StringVar(&options.stateDir, "state-dir", "", "private state directory")
	fs.StringVar(&options.instance, "instance", "", "Hyprland instance")
	fs.StringVar(&options.output, "output", "", "monitor connector")
	fs.BoolVar(&options.bar, "bar", false, "print cached bar status")
	fs.BoolVar(&options.refreshBar, "refresh-bar", false, "refresh saved bar weather without a window")
	fs.BoolVar(&options.toggle, "toggle-window", false, "open or toggle the forecast window")
	fs.BoolVar(&options.stop, "stop-effects", false, "stop the owned desktop effects")
	fs.BoolVar(&options.quit, "quit", false, "quit this app and its owned effects")
	fs.BoolVar(&options.version, "version", false, "show version")
	fs.BoolVar(&options.checkUpdates, "check-updates", false, "check for a published release")
	fs.BoolVar(&options.forceUpdateCheck, "force-update-check", false, "check even if checked today")
	fs.BoolVar(&options.installUpdate, "install-update", false, "install the available release and restart")
	fs.BoolVar(&options.updateWorker, "update-worker", false, "internal detached update worker")
	fs.BoolVar(&options.bootstrapUpdate, "bootstrap-update", false, "internal staged updater for older runtimes")
	fs.BoolVar(&options.recoverUpdates, "recover-updates", false, "internal interrupted-update recovery")
	fs.BoolVar(&options.service, "service", false, "internal service mode")
	fs.BoolVar(&options.headless, "headless", false, "service without a window for testing")
	fs.BoolVar(&options.offline, "offline", false, "use only saved weather")
	fs.IntVar(&options.duration, "duration", 0, "finite development run, 1..3600 seconds")
	fs.StringVar(&options.zipCode, "zip-code", "", "use a separate exact US ZIP location")
	fs.StringVar(&options.demoLocation, "demo-location", "", "use isolated Boston demo location")
	fs.StringVar(&options.root, "root", "", "development asset root")
	fs.BoolVar(&options.printSocket, "print-socket", false, "print the app's local socket path")
	fs.BoolVar(&options.measureFrames, "measure-frames", false, "development: report Qt frame callbacks in direct service mode")
	if e := fs.Parse(args); e != nil {
		return options, e
	}
	if fs.NArg() != 0 {
		return options, errors.New("unexpected arguments")
	}
	if options.bootstrapUpdate && (!options.updateWorker || buildMode == "development") {
		return options, errors.New("bootstrap requires a packaged update worker")
	}
	if options.version {
		return options, nil
	}
	if options.measureFrames && (!options.service || options.headless) {
		return options, errors.New("frame measurement requires --service without --headless")
	}
	durationSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "duration" {
			durationSet = true
		}
	})
	if (durationSet && options.duration < 1) || options.duration > 3600 {
		return options, errors.New("duration must be 1..3600")
	}
	if options.instance != "" && !regexp.MustCompile(`^[a-f0-9]+_[0-9]+_[0-9]+$`).MatchString(options.instance) {
		return options, errors.New("invalid explicit compositor instance")
	}
	if options.output != "" && !regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`).MatchString(options.output) {
		return options, errors.New("invalid explicit output connector")
	}
	if options.zipCode != "" && options.demoLocation != "" {
		return options, errors.New("choose ZIP or demo")
	}
	if options.zipCode != "" {
		if _, e := weather.ValidateSelection(M{"mode": "zip", "zip_code": options.zipCode}); e != nil {
			return options, e
		}
		if options.offline {
			return options, errors.New("ZIP lookup requires network; use the saved state directory in offline mode")
		}
	}
	if options.demoLocation != "" && options.demoLocation != "boston" {
		return options, errors.New("unknown demo")
	}
	return options, nil
}
