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

**Source builds only for now.** Prebuilt binaries and automated release builds
are not available yet. Native desktop effects are experimental and require
headers matching the supported Hyprland version. Forecast viewing works without
the native effects plugin.

## Build and run

Use an installed Python 3, Quickshell with Qt Quick, Qt 6 Shader Tools (`qsb`),
and a graphical Wayland session. On Omarchy/Arch, the relevant packages are
`python`, `quickshell` and `qt6-shadertools`. No pip installation is needed.
Optional notifications use `notify-send` from `libnotify`.

From the repository root:

```sh
python3 -I -B packaging/build_shaders.py
./a-weather-app
```

Shader compilation is offline and must be repeated after shader or Qt upgrades.
The canonical shader is in `godot/shaders/`; running Godot is not required.
The launcher uses `/usr/bin/python3` and `/usr/bin/quickshell`; shader compilation
uses `/usr/lib/qt6/bin/qsb`.

On first launch, open Settings and choose a five-digit US ZIP, or explicitly
choose approximate local detection. New York is the fallback location. Refresh
failures retain cached weather with freshness indicators; unavailable alerts
are never treated as an all-clear.

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
`a-weather-app.weather`. Install it from GitHub:

```sh
omarchy plugin add https://github.com/joega/a-weather-app.git
python3 -I -B "$HOME/.config/omarchy/plugins/a-weather-app.weather/packaging/build_shaders.py"
omarchy plugin enable a-weather-app.weather
```

The bar reads cached conditions without fetching weather or loading native
code. Clicking it opens or hides the app. No marketplace approval is implied.

Before an update, stop desktop effects and quit the app. Then run
`omarchy plugin update a-weather-app.weather` and rebuild the shader. Rebuild
optional native effects too; old local binaries may survive a source update.

## Desktop effects

Effects additionally require a C++23 compiler, make, pkg-config, GTK4,
gtk4-layer-shell, JSON-GLib, libepoxy, EGL/GLES development libraries, and
Hyprland 0.56.2 development headers matching the running compositor. On Arch,
these include `base-devel`, `gtk4`, `gtk4-layer-shell`, `json-glib`, `libepoxy`,
`mesa`, `libglvnd`, and the matching `hyprland` package and dependencies.

```sh
make -C native/frame-alignment
make -C native/atmosphere
```

Never replace a loaded native library: stop effects and quit the app before
rebuilding. Building does not activate effects. In Settings, check compatibility
and select the current output.

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

- **Missing shader:** rerun `packaging/build_shaders.py` and reopen the app.
- **Effects unavailable:** use the compatibility check; verify the running
  compositor version, current monitor and freshly built native binaries.
- **Old manual bar setup:** clear stale overrides with
  `omarchy bar set a-weather-app.weather projectPath null --json`, and similarly
  clear `instance` and `output`. Retain a custom `statePath` if needed.
- **Weather unavailable:** check connectivity and try Refresh. US alerts have
  US-only coverage; provider outages remain visible in the app.

Licensed under [MIT](LICENSE). See [third-party notices](THIRD_PARTY_NOTICES.md)
for shader attribution and weather-data providers.
