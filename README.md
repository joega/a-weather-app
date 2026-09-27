# A Weather App

A native Qt Quick weather app for Linux, with an animated sky and optional
weather effects across your Hyprland desktop. Designed for Omarchy.

![A Weather App with animated sky and live desktop rain](media/demo.gif)

[Watch the five-second demo in full quality](media/demo.mp4).
Recorded weather is illustrative, not a current forecast.

- Current conditions, hourly and ten-day forecasts, and selectable forecast details.
- Temperature, feels-like, precipitation and wind information in your chosen units.
- Official US weather alerts with individual instructions and expiry times.
- Saved US ZIP locations and optional approximate local detection.
- Quiet, opt-in hourly precipitation notifications with pause and quiet hours.
- Optional desktop rain, runoff and pooling; snow accumulation and melting.

## Screenshots

Captured from the packaged app with synthetic Boston weather, labeled in the
window. These illustrate the interface, not current conditions.

| Forecast and animated sky | Hourly forecast details | Desktop effects controls |
| --- | --- | --- |
| [![Forecast](preview.png)](preview.png) | [![Hourly wind chart](media/forecast-details.png)](media/forecast-details.png) | [![Desktop effects settings](media/desktop-effects.png)](media/desktop-effects.png) |

Select an image for full size. **Live desktop** keeps weather effects on until
you stop them; the separate preview runs for five minutes.

## Install on Omarchy

On a current **x86_64 Omarchy** installation:

```sh
omarchy plugin add https://github.com/joega/a-weather-app.git --enable
```

Click the weather widget to open the app. The repository includes the compiled
forecast shader and native effects components: no compiler, shader build, binary
download or extra setup command is needed. Effects remain off until you enable
them in the app.

On first launch, open Settings and choose a five-digit US ZIP, or explicitly
choose approximate local detection. New York is the fallback location. Refresh
failures retain cached weather with freshness indicators; unavailable alerts
are never treated as an all-clear.

The packaged runtime targets Omarchy's Python 3, Quickshell/Qt 6, GTK4,
gtk4-layer-shell, JSON-GLib, libepoxy and Mesa libraries. Omarchy supplies these
through its standard packages and dependencies. Notifications use `notify-send`
from `libnotify`. Other Linux distributions may need to install these dependencies.

For a standalone checkout on the same supported system, run `./a-weather-app`.
See [building and verifying releases](packaging/README.md) for source builds,
artifact provenance and the release pipeline.

### Optional launcher

From the repository root, for a new user installation:

```sh
mkdir -p "$HOME/.local/bin" "$HOME/.local/share/applications"
ln -s "$PWD/a-weather-app" "$HOME/.local/bin/a-weather-app"
install -m 644 packaging/a-weather-app.desktop "$HOME/.local/share/applications/a-weather-app.desktop"
```

Keep the checkout at that location and ensure `~/.local/bin` is on your PATH.
If a launcher already exists, update its symlink deliberately instead of
replacing an unrelated command.

## Omarchy bar widget

The entire repository is the plugin; its root `manifest.json` declares
`a-weather-app.weather`. The bar reads cached conditions without fetching weather
or loading native code. Clicking it opens or hides the app.

Before updating, stop desktop effects and quit the app, then run:

```sh
omarchy plugin update a-weather-app.weather
```

The update includes the matching compiled artifacts. No rebuild is needed.
Do not replace binaries while effects are running. No marketplace approval is
implied by installation.

## Desktop effects

The bundled effects target **Hyprland 0.56.2** (the exact source build is
recorded in `packaging/runtime.json`). Settings checks compatibility before
activation, and incompatible compositor builds are refused before loading the
plugin. After a Hyprland update, effects may remain unavailable until a matching
A Weather App release is available; forecast viewing continues to work.

In Settings, check compatibility and select the current output. No build step
is required for the supported packaged runtime.

- **Live desktop**, in the main window header, follows actual weather until you
  stop it. Closing the window keeps it running; reopen through the bar to stop it.
- **Start 5-minute preview**, in Settings, temporarily previews chosen weather.
- **Stop** turns effects off. Live mode does not automatically resume after login.
- Reduced motion disables precipitation and lightning; lightning is off by default.

Native plugins run inside the compositor. Support is experimental on the tested
AMD RENOIR/Mesa 26.2.2/Hyprland 0.56.2 system. Multiple configured monitors,
rotated/mirrored outputs and configured HDR are refused. Other GPUs/ABIs, general
dynamic output recreation, arbitrary fractional scales and multi-day stability
are unverified. Short isolated tests do not establish broad hardware support.

## Notifications

Enable precipitation watching in Settings → Notifications. Choose a probability
threshold, quiet hours in the selected location's timezone, or a one-hour pause.
Outlooks use hourly forecast periods, not minute-precise onset predictions.
Stale/unavailable forecasts suppress delivery, and duplicate events are reserved
to avoid repeated notifications. The desktop daemon controls presentation.

Watching continues while the window is hidden. Use Stop watching or Quit app to
end it. Saved opt-in resumes on your next manual launch; no autostart or service
is installed. Notifications do not require native desktop effects.

## Privacy and removal

Forecasts and ZIP geocoding use Open-Meteo; US alerts use `api.weather.gov`.
Approximate local detection contacts `ipwho.is` only after opt-in. Providers
receive requests, including location parameters where needed. The app does not
store an IP-address response field. Normal refresh is limited to once per
15 minutes; manual refresh can happen sooner.

Private settings, locations, forecasts and bounded notification reservations
live in `$XDG_STATE_HOME/a-weather-app`, or `~/.local/state/a-weather-app`.
The app does not store notification body history; your desktop daemon may.
Effects use a private `a-weather-app-effects-*` directory under the system
temporary directory (`$TMPDIR` when configured, normally `/tmp`), with bounded
logs. Failed cleanup retains that directory for inspection. Launcher failures
may retain `guardian-last-error.log` in the state directory.

The optional `python3 -m weather` command uses
`~/.cache/a-weather-app/weather.json` by default; its `--cache` option and
`python3 -m weather.service` file options select separate explicit paths.
Output directories must be owned by you with mode `0700`; input files must be
regular, owned by you, and not writable by other users. Symlinks are refused.

Stop effects and quit the app before uninstalling:

```sh
omarchy plugin remove a-weather-app.weather
```

Removal disables the widget and removes the installed source/builds, preserving
private settings and separately managed checkouts. Installed dependency packages
also remain. Remove any separately created `~/.local/bin/a-weather-app` symlink
and `~/.local/share/applications/a-weather-app.desktop` individually. To erase
saved data, stop the app first and remove only its configured state directory,
any standalone CLI cache/output files you selected, and any retained effects
diagnostic directory after inspecting its exact path.

## Troubleshooting

- **Missing or changed packaged files:** quit the app, then update or reinstall it.
  Source builders should follow [the build instructions](packaging/README.md).
- **Effects unavailable:** use the compatibility check; verify the running
  compositor version, current monitor and a matching app release.
- **Old manual bar setup:** clear stale overrides with
  `omarchy bar set a-weather-app.weather projectPath null --json`, and similarly
  clear `instance` and `output`. Retain a custom `statePath` if needed.
- **Weather unavailable:** check connectivity and try Refresh. US alerts have
  US-only coverage; provider outages remain visible in the app.

Licensed under [MIT](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md)
for shader attribution and weather-data providers.
