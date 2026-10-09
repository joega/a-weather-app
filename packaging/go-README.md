# A Weather App for Omarchy

This package contains the compiled weather app, its Qt Quick forecast window,
native desktop effects, icons, and an optional Omarchy bar widget. The forecast
window does not require Python or Quickshell; the bar widget uses Omarchy's
existing Quickshell.

Extract the archive into a directory you own, then launch `./a-weather-app`.
On first launch, search for a worldwide city (optionally filter by country code),
choose a five-digit US ZIP code in Settings or opt in to
approximate local detection. The app keeps cached forecasts when a provider is
unavailable and marks stale data. Settings and cache use
`$XDG_STATE_HOME/a-weather-app` or `~/.local/state/a-weather-app`.

City results show region and country; choose a result to save it. Saved cities
reopen offline. Alerts are provided by NWS only for verified US locations;
unsupported or unknown coverage is explicitly labeled and is not an all-clear.
Open-Meteo's free API is for non-commercial use; commercial use requires an
appropriate provider plan. Geocoding data is provided by GeoNames via Open-Meteo.

The launcher also supports `--bar`, `--refresh-bar`, `--toggle-window`,
`--stop-effects`, and `--quit`. The optional bar widget calls the same launcher
and refreshes previously saved weather in the background after login; local
detection is used only if previously enabled in Settings. To update or remove
the package, stop any active desktop effects and quit the app before switching
the plugin or launcher link. Keep the previous package and a backup of saved
state until the new version is verified.
Startup migrates older location state into `saved-locations.json`, retaining the
original files unchanged for rollback. The manifest becomes authoritative; saved
forecast caches are bounded and loaded only for active cities. The saved-city
picker is under development; current city selection still changes the primary
place used by the bar and desktop effects. Keep a complete pre-update state
backup for downgrades, especially to versions without worldwide city support.

Desktop effects require a compatible Hyprland build and explicit activation in
Settings. If effects are unavailable after a compositor update, the forecast
window remains usable. Reduced motion disables precipitation and lightning.

The package does not install itself, change desktop configuration, or enable
autostart. See the repository's root README for privacy, notifications, manual
launcher setup, the separate Omarchy plugin installation, troubleshooting, and
source-build instructions.
