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
capped. Following the softer wind-map refinement, work is limited to 64 trails,
12 points each, and about 15 animation ticks per second. Each trail is also
limited to 18 logical pixels, independently of refresh rate. Both fields and trails are immediately invalidated before coalesced
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

## Follow-up: visible cloud scene behind forecast cards

The additional eight-second Apple Weather recording was inspected at the start,
middle, and end. The original window drift and cloud contrast were too faint to
communicate that the forecast backdrop was an animated scene.

The window now has a stronger calm-wind drift floor and a capped response to
surface wind speed. The existing feathered cloud layer has more definition for
partly and mostly cloudy skies, while its coverage-dependent strength keeps
nearly clear skies mostly blue. Some cloud relief remains at the bottom of the
window, behind the lower cards. A lighter header scrim makes the sky visible
while retaining readable white text. The shared canonical desktop shader and
generated native header remain unchanged, as do the rendering bounds, Reduced
motion, hidden/minimized gating, and software-renderer fallback.

Native Wayland/OpenGL review again covered 1200 × 850 and 700 × 850 logical
windows, clear/partly cloudy/overcast skies, opposing wind directions, night,
Reduced motion, and the unsupported-shader fallback. The hardware regression
check advances **only cloud phase** by four seconds at the calm-wind floor,
with precipitation and lighting fixed. It checks actual rendered pixel changes
in both partly cloudy and overcast skies, rather than merely checking that a
timer runs. Lifecycle tests additionally enforce the motion floor and storm
speed cap.

Representative native captures (synthetic “Demo forecast,” sampled at three
frames per second) are saved as:

- [Desktop cloud recording](../../../media/flow-review/clouds-desktop.webm)
- [Narrow cloud recording](../../../media/flow-review/clouds-narrow.webm)
- [Animated narrow preview](../../../media/flow-review/clouds-narrow.gif)
- [Narrow atmosphere screenshot](../../../media/flow-review/atmosphere-narrow.png)

The updated development preview was rebuilt and reopened with its isolated
saved location/settings. The production menu-bar service was verified running.
Hardware coverage remains limited to this machine's Wayland/OpenGL renderer.

Follow-up checks: shader generation and the reviewed-artifact translation test,
Go lint plus fresh shader-translator race tests and vet, native/QML formatting
and analysis, Qt protocol/frontend suites, service/frontend end-to-end tests,
and the native hardware rendering checks. The regular software suite skips
the hardware cloud pixel test; it was run successfully on the desktop alongside
the screenshot review. The QML analysis helper now also honors `TMPDIR` for its
temporary import tree, so all checks can use on-disk scratch space without
changing `/tmp` quotas or mount settings.

To capture another cloud recording, add `WEATHER_QT_CLOUD_CLIP` with a private
output directory to the native review command above. It saves 24 PNG frames for
each window width. Include `atmosphereCloudDriftIsVisible` in the test arguments
to repeat the hardware cloud-motion regression check.

## Follow-up: softer, lower-cost wind trails

The wind map now uses short blue-gray streaks with no separate bright head
dot. Opacity still tapers downwind, and motion and point inspection use the
same forecast vectors. Trail density is reduced to at most 64, history to at
most 12 points, and animation to about 15 updates per second. Visible arc
length is capped at 18 logical pixels independently of update frequency;
fractional tail trimming avoids abrupt whole-segment cuts. Reduced motion
uses the same short trail bounds. Painting also reuses the cached field values
for temperature/precipitation instead of allocating an unused values array
on every wind frame. The accepted cloud background is unchanged.

Native Wayland/OpenGL review at 1200 × 850 and 700 × 850 inspected the revised
trails over copied offline map tiles, selected-point readouts, changed forecast
hours, and Reduced motion. Updated representative wind screenshots are linked
above. Tests cover short animated/static trails under calm, strong, and curved
fields at different time steps, including a partial tail segment, plus existing
viewport/minimized/hidden pausing and location/hour invalidation. The unchanged
cloud pixel-motion check also passed on the native renderer.

Validation passed: native/QML formatting and analysis, 43 Qt protocol cases,
60 frontend cases (six optional/hardware cases skipped in the software run),
and native hardware rendering checks. Service/frontend end-to-end checks were
rerun as well. These changes reduce the bounded map drawing budget; no
controlled CPU/GPU before-and-after benchmark was performed. Hardware coverage
remains limited to this machine. Network requests and tile-cache behavior are
unchanged, and no version number or release was changed.

## Follow-up: adaptive sky refresh

The in-app sky now updates at 20 FPS for cloud-only conditions, including clear,
partly cloudy, overcast, and fog. Rain or snow, and enabled thunderstorm
lightning, select approximately 30 FPS. Both rates integrate elapsed time;
switching between them preserves cloud position and motion speed. Existing
hidden/minimized, inactive-presentation, Reduced motion, and shader-fallback
gates still stop animation. The texture budget, shaders, desktop atmosphere,
network requests, and map cache behavior are unchanged.

The new native regression checks weather-driven cadence, explicit rain/snow
amounts, dry lightning, and switching 20 → 30 → 20 FPS without resetting phase.
On the hardware renderer it also measures timer ticks and verifies that cloud
distance matches elapsed time at both rates. Reduced motion stays paused during
weather changes. Native desktop/narrow review and the cloud pixel-motion check
were repeated with the new cadence.

A controlled native Wayland/OpenGL comparison used the same embedded sky,
922 × 1030 logical window, desktop 2× scale, wind speed 8 m/s, and fixed daytime
lighting at each rate. Each run warmed up for 1.5 seconds, then sampled for about
10 seconds. CPU uses process CPU time as a percentage of one core; GPU uses the
process's DRM graphics-engine busy-time counters, deduplicated by client ID.

| Sky | Timer rate | CPU, one core | GPU graphics-engine busy |
| --- | ---: | ---: | ---: |
| Overcast | 30.30 Hz | 4.56% | 10.77% |
| Overcast | 20.07 Hz | 2.66% | 7.19% |
| Partly cloudy | 30.32 Hz | 4.03% | 12.60% |
| Partly cloudy | 20.07 Hz | 2.67% | 8.52% |

The cloud sky used approximately 32–33% less graphics-engine time and 34–42%
less CPU time at the lower cadence. Measured drift remained approximately
0.0136 phase units per second at both rates. This is one sample per condition
and rate on this laptop, isolating the sky from forecast cards and map rendering;
it is not a whole-app, battery-life, or cross-device result. Qt frame callbacks
were about 60/40 Hz even though the animation timers ran at 30/20 Hz, so those
callbacks should not be interpreted as additional animation updates.

Validation passed: native/QML formatting and analysis, 43 protocol cases,
61 frontend cases (six optional/hardware cases skipped in the software run),
24 service/frontend end-to-end cases (two opt-in cases skipped), and seven
native hardware review cases including setup/cleanup. The isolated development
preview was refreshed and verified visible at 922 × 1030 logical pixels.
No version number or release was changed.
