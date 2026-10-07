# Native and QML maintenance sprint — October 7, 2026

Completed independently available implementation and validation for **v0.61.0**,
against clean baseline `49e63ee8296a3f9268264b9ae2aac1aa1afdc78b` (the completed Go sprint).
[Go review](go-maintenance-sprint.md) records earlier Docker/live evidence;
that evidence does not verify this sprint's changed code. Final Docker and live
checks remain explicitly pending below. No push, repository tag or publication.

## Completed review and fixes

- [x] Established [conventions](../../NATIVE_QML_STYLE.md), scoped format/static
  gates, and release-container gates. Preserved C17, Qt C++17 and plugin C++23.
  Mechanical formatting is a separate commit, without include/import/attribute
  reordering. No applicable repository AGENTS.md or unrelated changes existed.
- [x] C atmosphere: scoped GOption allocations on every exit (P2), separated
  finite-expiry and signal callbacks to retain accurate source ownership (P2),
  wrapped lightning cycles before uint32 conversion (P2), and replaced
  padding-sensitive test memcmp with field comparison. Added extreme-time and
  allocated-option early-return sanitizer regressions.
- [x] Qt: quiesced reply callbacks before MapTiles member destruction (P1);
  guarded owner/reply/generation around synchronous delivery so listener close
  or destruction cancels tileReady (P2). Added active-download teardown and both
  reentrant regressions. Scoped signal-pipe descriptors cover early main returns.
- [x] Compositor: checked fadeout doubles before narrowing (P2). Reviewed
  listener/timer removal, retained-pass purge, shared GPU ownership, EGL context
  restoration and unload. Existing ABI symbols and -fno-gnu-unique preserved.
- [x] QML: explicit required transport and optional tile dependencies with initial
  property injection; qualified delegate/control references and bound lexical
  IDs; typed writable Flickable access; six layout divider corrections and
  completed qmldir. Appearance, accessibility and object names retained.
- [x] Search/shutdown: inactive edits no longer schedule debounce; queued triggers
  reject hidden/disconnected/location-busy state (P2). Final close is exactly
  once, clears queues and stops both deadlines (P2). Integration found a brief
  busy pulse when clearing pendingOp before pending ID; corrected order and
  added a dedicated binding regression. Preserved stop/shutdown priority,
  search tokens, snapshot revisions and map/presentation coalescing.
- [x] Reviewed Forecast.js, maps/playback, Atmosphere, settings/update controls,
  and bar subprocess deadlines/size bounds. Main frontend runtime/binding
  warnings now fail tests. No simulation or shader algorithm changes, broad
  component migration, saved-state changes or arbitrary cleanup quotas.
- [x] Updated combined [release notes](../../packaging/releases/v0.61.0.md),
  refreshed tracked native binaries, checked final diff and generated resources.

Stage commits: `1779d84` conventions; `48d047e` formatting;
`5decdd6` C ownership/conversion; `e0392b9` Qt ownership/native analysis;
`3d96eb6` QML contracts/lifecycle. Final audit corrections, generated binaries
and release documentation follow these commits.

## Scope and retained invariants

| Area | Audit outcome |
| --- | --- |
| atmosphere and tests | Checked bounded no-follow owned-file reads, strict duplicate/depth/type validation, atomic last-good outputs, policy freshness, finite renewable leases, sources/references and current-context GL cleanup. |
| frame-alignment and headers | Checked request limits, generation guards, callbacks/exception boundaries, weak monitor references, queued scene passes, unload/symbol behavior, geometry and cadence. At most 64 supports plus masks bound exposed-top spans below the 65-slot array. |
| physics/snow and native tests | Checked finite/bounded parameters before indices, fixed-capacity pools, nonallocating steps, conservation sinks, topology clipping, reset/suppression, residual activity and temperature leases. No substantive simulation changes justified. |
| Qt/protocol/frontend tests | Reviewed QObject and member destruction order, context-bound connections, thread affinity, socket buffering/peer checks, strict JSON, cancellation, disk cache ownership, download/decode budgets and shutdown acknowledgments. |
| QML/JS/widget | Reviewed bindings, lexical dependencies, async ordering, search generation/token latches, timers, hidden/minimized and reduced-motion gating, layout/implicit sizes, forecast validation, maps and bar process ownership. |

Maintained source includes all named native directories, tests, ui/qml and
Quickshell widget. Generated deliverables: tracked sky_shader.h (Godot shader
source), QSB, native executable/plugin; qmake/moc/rcc/compiler outputs are local
build products. External Qt/GTK/GLib/Hyprland code was not edited or formatted.
Godot remains the shader/reference implementation. No genuinely inactive code
was established, so none was removed.

## Verification

Host: LLVM 22.1.8, Qt 6.11.2, GCC 16.2.1, Go 1.27.1, Hyprland 0.56.2.

- `make check-native-qml`: format checks, scoped clang-tidy on production native
  translation units plus native simulation tests, and QML checks pass. Main
  QML/Forecast.js has zero diagnostics. Widget uses actual Omarchy modules.
- `make test-go`: pinned goimports/Staticcheck, race tests and vet pass. The
  isolated Quickshell update-action fixture also passed explicitly.
- `make -C native/qt test`: **43 protocol / 38 frontend** cases pass; four
  optional live/private capture cases skip. Actual debounce timing retained;
  new teardown/cancellation/binding checks use direct deterministic transitions.
- Fresh `make go APP_VERSION=0.61.0` and hardened Qt build succeed; executable
  reports 0.61.0. `make -C native/qt test-e2e`: **24 pass**, two optional
  private-map/live-provider cases skip, against that freshly built executable.
- Native simulation/self-tests/adversarial inputs pass ASan, UBSan and leak
  detection; new option-error cases included. Qt protocol and seven map ownership
  cases also pass all three sanitizers in disposable offline copies. System Qt,
  its renderer/plugins and the Go service are not sanitizer-instrumented.
- `make native shaders`, fresh atmosphere self-test/input checks, plugin symbols
  and hardening for Go/Qt/plugin/atmosphere pass. Generated shader header and QSB
  remain consistent. Tracked native binaries were rebuilt with the host tools;
  the release container independently rebuilds them before packaging.
- Twelve deterministic offscreen screenshots before/after QML cleanup are
  byte-identical, with final runtime warnings treated as failures. No rendering
  or simulation hot-path algorithm was changed; no performance gain claimed.
- Release selector tests pass and choose 0.61.0. `git diff --check` passes.
  Implementation is committed before the final Docker attempt.

Initial sandbox runs could not create local sockets or run LeakSanitizer under
ptrace; unrestricted reruns passed. These failures were environmental, not
counted as passing checks. Go compiler caches also required writable/host access.

## Guidance and justified exceptions

Applied [C++ Core Guidelines](https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines)
R.1/R.12, [CERT MEM31-C](https://cmu-sei.github.io/secure-coding-standards/sei-cert-c-coding-standard/rules/memory-management-mem/mem31-c/),
[CERT FLP34-C](https://cmu-sei.github.io/secure-coding-standards/sei-cert-c-coding-standard/rules/floating-point-flp/flp34-c/),
[Qt style](https://wiki.qt.io/Qt_Coding_Style),
[Qt 6.11 ownership](https://doc.qt.io/qt-6.11/objecttrees.html),
[QML conventions](https://doc.qt.io/qt-6/qml-codingconventions.html) and
[Qt 6.11 performance guidance](https://doc.qt.io/qt-6.11/qtquick-performance.html)
with judgment. Qt/GLib framework ownership, raw GL handles with context-aware
cleanup, strict JSON numeric representation, finite budgets, existing ABI/error
handling and real cadence tests remain appropriate.

clang-tidy selects portable correctness checks, not all guideline/style rules or
external WebKit ownership conventions. External includes are system headers;
GCC-only -fno-gnu-unique is excluded from Clang parsing, retained in real builds.
No source suppressions. Tests are covered by compiler/regression/sanitizer checks;
Qt generated test moc files are not part of the static-analysis scope.

Quickshell Io metadata omits QProcess::ExitStatus. Widget analysis records six
exact upstream signal-parameter diagnostics. The gate permits only that exact
ID/message and fails every other diagnostic; no blanket suppression or fake
runtime type. Dynamic injected transport/tile and Omarchy host/font APIs retain
documented duck-typed contracts, verified with real runtime/offline fixtures.
Tools must be present; missing tooling/modules fail explicitly. qmlformat and
qmllint accept duplicate ComponentBehavior pragmas that runtime rejects, so the
gate adds a direct duplicate-pragma check.

## Pending user checks

Docker access is denied to the current user and `sudo -n docker info` requires a
password. The final release-build attempt cannot obtain interactive sudo in this
session. No new archive, checksum, source inventory, packaged startup/shutdown or
independent-build reproducibility result is claimed. Earlier Go artifacts do not
cover these native/QML changes. Run from the final clean checkout:

```sh
cd /home/joeg/Projects/a-weather-app
WEATHER_RELEASE_VERSION=0.61.0 bash scripts/run_go_migration_build.sh
```

The build performs independent builds, compares five artifacts, checks source
metadata/version, and tests packaged startup/shutdown. In its printed output
directory run `sha256sum --check SHA256SUMS`; verify go-runtime.json has
`source_dirty=false`, empty source_status and the final checkout's source_commit,
and the archive manifest declares 0.61.0. Do not push, tag or publish.

On the live desktop: open the current build, rapidly edit/select city searches,
hide/reopen Settings, disconnect/close with work pending, open/close maps during
downloads and verify keyboard scrolling. Check finite rain/snow/sky effects,
reduced motion, hidden/minimized presentation, Stop and clean unload. Check bar
updates/open/close against the normal Omarchy host. Earlier user-reported live
checks remain baseline evidence only. Optional live-provider/private map checks
and live GPU/performance measurements remain unrun.

Published runtime pins/source defaults remain 0.60.0 until the existing release
workflow verifies and pins new artifacts; the release target stays 0.61.0.
