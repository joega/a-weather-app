# Atmosphere and wind-map review — October 8, 2026

The six-second reference recording was inspected at six one-second intervals.
Its soft clouds and small flowing wind streaks informed the in-app changes.
The existing Qt shader translator remains the source of the window shader;
the canonical desktop shader and its generated native header are unchanged.
No desktop activation, compatibility probe, version change, or release was used.

## Rendering and behavior

Native Qt 6.11.2 rendered the embedded production QML on Wayland with OpenGL
(graphics API 3), at **1200 × 850** and **700 × 850** logical pixels. Captures
are at the desktop's 2× pixel scale. The review covered clear and partly cloudy
skies, zero east–west wind (north/south), positive/negative east–west wind,
Reduced motion, and the forced unsupported-shader fallback. The native tests
also verified minimized/hidden presentation, viewport pausing, forecast-hour
changes, visible location changes with replacement wind vectors, and mouse and
keyboard point inspection. Live desktop remained inactive.

The atmosphere uses a small drift floor and surface wind speed for an artistic
cloud drift, without presenting it as a geographic wind simulation. Low-cover
wisps remain faint; forecast readability is retained with a dark header scrim.
The static fallback uses weather-dependent sky colors and quiet cloud shapes.

The wind map interpolates **components** from actual returned forecast-cell
coordinates, deduplicates snapped cells, caches a 33 × 25 visual field, and
samples that field bilinearly. Its inverse-distance interpolation describes a
smooth model pattern, not new model detail. The UI distinguishes the native
model grid from the 5 × 5 requested sample lattice (~5 miles / 8 km spacing).
Inspection uses the forecast samples directly and the existing unit formatter.
Tapered trails indicate downwind direction; pixel speed is illustrative and
capped. Work is limited to 96 trails, 20 points each, and 25 animation ticks per
second. Both fields and trails are immediately invalidated before coalesced
rebuilds on a new hour, location, or geometry. Reduced motion retains static
trails and point inspection, disables timeline Play, and keeps manual hours.

## Representative captures

- [Partly cloudy desktop forecast](../../../media/flow-review/atmosphere-desktop.png)
- [Wind map with point inspection](../../../media/flow-review/wind-desktop.png)
- [Narrow window](../../../media/flow-review/wind-narrow.png)
- [Reduced motion](../../../media/flow-review/wind-reduced-motion.png)

The forecast/sky fixture is synthetic and labelled “Demo forecast.” The map
uses the saved North Attleboro model forecast fetched at 2026-10-08 15:07:58 UTC,
with OpenStreetMap tiles loaded **offline from a copied cache**. These images
are controlled validation captures, not current-weather claims. Private cache
and forecast files remain in ignored build output, not in the repository.

## Checks

- Shader generation succeeded; the reviewed Qt source and unchanged native
  header match translations from the canonical shader.
- `make check-native-qml` passed formatting, native analysis, and QML analysis.
  The widget analysis retains six exact, classified upstream Quickshell enum
  metadata diagnostics; there are no project findings.
- `make -C native/qt test`: 43 protocol and 59 frontend tests passed. Five
  opt-in screenshot cases were skipped by the regular suite; the new native
  rendering case was run separately and passed on the real desktop.
- `make -C native/qt test-e2e`: 24 passed, two opt-in live-provider cases skipped.
  This includes cached/offline map lifecycle coverage.
- Follow-up verification: Go lint, **all** Go race tests (`-count=1`, no
  exclusions), and `go vet ./...` passed. The six updater restart/rollback
  scenarios also passed in a separate targeted run with the real Qt frontend.
  The earlier `/tmp` quota limitation is resolved for verification: the fixture
  now honors `TMPDIR` instead of hardcoding `/tmp`, and its copied runtime
  bundles were placed in a short private directory on the main disk. The short
  path also accommodates nested Unix socket names. No system quota or mount
  setting was changed.

The `/tmp` mount on this machine is a separate 7.2 GiB tmpfs with user quotas,
independent of the main disk's free space. To repeat Go checks using disk space:

```sh
(
    weather_test_tmp=$(mktemp -d "$HOME/w.XXX")
    trap 'rm -rf -- "$weather_test_tmp"' EXIT
    TMPDIR="$weather_test_tmp" GOTMPDIR="$weather_test_tmp" make test-go
)
```

Keep the scratch path short because some tests create Unix sockets under it.
The fixture and shell cleanup remove their temporary files after the run.

Hardware/backend coverage is limited to this machine's Wayland/OpenGL setup
and Qt's offscreen software renderer. The forced shader-unavailable state was
visually inspected; arbitrary driver/compiler failures, other GPUs/backends,
and multi-day animation stability were not tested.

## Repeat the native review

Build with `make shaders qt` and `make -C native/qt test`. To render controlled
fixtures without map downloads, set `WEATHER_QT_FLOW_SCREENSHOTS` to an output
directory and run:

```sh
env -u QT_QUICK_BACKEND QT_QPA_PLATFORM=wayland \
  QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none \
  WEATHER_QT_FLOW_SCREENSHOTS="$PWD/build/flow-review/screenshots" \
  native/qt/frontend-test atmosphereLifecycleAndFallback \
  windAnimationLifecycleAndInspection renderAtmosphereAndWindReview
```

Optionally set `WEATHER_QT_FLOW_MAP` to a copied saved `weather-map.json` and
`WEATHER_QT_FLOW_TILE_CACHE` to a copied `map-tiles` cache directory. The renderer
uses that copy in offline mode; without it, the map has the useful static
geographic-background-unavailable presentation. Never point review tooling at
an active cache when testing cache mutations.
