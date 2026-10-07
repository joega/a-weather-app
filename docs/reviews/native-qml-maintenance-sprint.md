# Native and QML maintenance sprint — October 7, 2026

Release target: **v0.61.0**. Baseline: `49e63ee` (clean checkout).
The completed [Go review](go-maintenance-sprint.md) and its Docker/live evidence
are the baseline, not verification of this sprint's changed source.
No repository AGENTS.md applies. Preserve C17, Qt C++17, compositor C++23.

## Working checklist

- [ ] Audit maintained native/atmosphere, frame-alignment, physics, snow, Qt,
  tests, ui/qml, Forecast.js, and Quickshell widget; establish conventions.
- [ ] Evaluate and scope clang-format/tidy and qmlformat/qmllint gates.
- [ ] C: fix option ownership on early exits (P2; leaked GOption strings),
  review sources/timers, GL resources, bounded input and numeric conversions.
- [ ] C++: investigate map reply teardown (P1; manager is destroyed after
  callback state), review transport, signal pipe, scene graph/plugin unload,
  simulation invariants; add ownership/cancellation regressions.
- [ ] QML: audit bridge coalescing, stale search replies/debounce, map playback,
  presentation/reduced motion, settings/update controls and bar subprocesses.
- [ ] Keep mechanical formatting separate; run focused checks and commit stages.
- [ ] Combined Go/native/Qt/offscreen verification, generated consistency,
  release notes, final diff/status review and clean-source Docker build.
- [ ] Record exact unavailable/manual checks and finish a lasting report.

## Inventory and tooling baseline

Maintained source: the directories above, including test fixtures. Generated:
`native/atmosphere/sky_shader.h` from the Godot shader, QSB shader from
`ui/shaders/atmosphere.frag`, qmake/moc/rcc objects and binaries (ignored).
Third-party headers/libraries are external (Qt, GLib/GTK, Hyprland); do not
format or rewrite them. Godot is the retained shader/reference implementation,
not a reason to delete code from the native/QML scope. No inactive code has
been established by evidence.

Host tools: LLVM 22.1.8, Qt 6.11.2; Qt tooling is under `/usr/lib/qt6/bin`.
Release container uses the existing September 26 Arch snapshot. Tool gates
must report missing tools, not silently pass. Docker availability is being
checked separately.

## Guidance

Use [C++ Core Guidelines](https://isocpp.github.io/CppCoreGuidelines/CppCoreGuidelines)
R.1/R.12 for ownership and scoped cleanup;
[SEI CERT C](https://wiki.sei.cmu.edu/confluence/display/c/SEI+CERT+C+Coding+Standard)
MEM31-C, INT31-C, FLP34-C for allocations and conversions;
[Qt ownership](https://doc.qt.io/qt-6/objecttrees.html),
[Qt coding style](https://wiki.qt.io/Qt_Coding_Style),
[QML conventions](https://doc.qt.io/qt-6/qml-codingconventions.html), and
[Qt Quick practices](https://doc.qt.io/qt-6/qtquick-bestpractices.html).
Apply stable guidance compatible with installed/supported Qt; no framework
migration or blanket ownership/exception rules at ABI boundaries.

Conventions are in [NATIVE_QML_STYLE.md](../../NATIVE_QML_STYLE.md).
Formatting gates cover Git-tracked maintained C/C++/QML/JS only, excluding
sky_shader.h. qmlformat supports the existing library JS. Keep ordering intact.
Baseline qmllint: 105 unqualified references, six missing-property findings,
six layout-size findings, one unused import; investigate with runtime types.
clang-tidy wildcard evaluation includes unsuitable WebKit ownership checks;
select portable correctness checks instead. No blanket suppressions planned.

### Formatting stage

- Expanded maintained native/QML/JS code with the configured tools; preserved
  include/import/attribute order and excluded generated files.
- `make check-native-qml-format` passes with LLVM 22.1.8 / Qt 6.11.2.
- Qt offscreen tests: 43 protocol, 28 frontend pass; four private/live captures
  skip. First sandbox attempt failed local socket creation; unrestricted rerun
  passes. Native sanitizer validation reruns outside ptrace restrictions.
- Native simulation/self-test/adversarial input checks pass with ASan, UBSan
  and leak detection outside the sandbox; no sanitizer findings.

### C lifecycle and conversion stage

- P2: GOption strings now use scoped GLib cleanup on parse failures, self-test,
  policy/validation modes and invalid CLI exits; State borrows them only while
  the application runs. Removed repeated frees.
- P2: expiry callback clears its one-shot source ID; signal sources remain
  registered until teardown, avoiding stale IDs. Tick already clears its ID.
- P2: lightning cycle wraps before float-to-uint32 conversion, avoiding undefined
  conversion for extreme finite input; normal envelopes/shader behavior unchanged.
- Added extreme-time self-tests and sanitizer regressions for allocated-option
  early exits. Strict JSON duplicate/depth/type/freshness, no-follow bounded file
  reads, GL-context deletion and finite renewable policy leases remain intact.
- C focused build/self-test/adversarial input tests and full native
  address/undefined/leak sanitizer suite (including new early-return cases)
  pass. Formatting and diff checks pass.

### C++ ownership and analysis stage

- P1: MapTiles disconnects/aborts replies before derived callback state dies;
  includes replies waiting for deferred deletion. Added active-download
  destruction tests. Transport already correctly disconnects before abort.
- P2: guard owner/reply and generation across synchronous drain/tileReply
  callbacks; close or destruction by a listener cancels tileReady. Added both
  reentrant regressions. Limits, cooldowns and offline caching are unchanged.
- P2: scoped signal-pipe descriptors now close on all main() returns, after
  notifier destruction. Fadeout geometry is range-checked before double-to-float
  conversion. No simulation/rendering hot-path algorithm changes.
- Scoped clang-tidy correctness checks use system includes for external headers,
  C17/C++17/C++23 per target. GCC-only -fno-gnu-unique stays in real plugin builds,
  not Clang analysis. Replaced padding-sensitive Weather memcmp with field
  equality; no suppressions. Full Qt tests: 43 protocol / 31 frontend pass,
  four optional capture cases skip. Plugin rebuild and symbol checks pass.
- Correction to inventory: native executables/plugin and QSB are tracked
  generated deliverables, not ignored; refresh and verify before final commit.
- Scoped clang-tidy passes all production native translation units and native
  simulation tests; external/compiler diagnostics outside selected checks are
  reported by Clang, not hidden with source suppressions. Qt protocol and map
  fixtures pass ASan/UBSan/leak detection (43 protocol, seven map cases).

### QML contracts, binding and lifecycle stage

- P2: shell/Bridge take explicit required transport input; optional tile input
  defaults to null. C++ and fixtures inject initial properties. Dynamic API
  properties deliberately support real and fake QObject implementations with
  documented contracts, without fabricated runtime type stubs.
- Qualified delegate fields/outer IDs and control hover/focus references;
  ComponentBehavior Bound preserves intended lexical dependencies. Corrected
  six layout divider sizes, removed unused import and completed qmldir entries.
  Typed Flickable access preserves scrolling writes as well as reads.
- P2: inactive search rejects queued debounce triggers and cancels on hidden,
  disconnected or location-busy transitions. Existing actual 350ms cadence,
  client-token/generation ordering and stop/search coalescing tests retained.
- P2: extracted queued-action cleanup and guarded final close notification;
  service-stopped, rejected quit, synchronous disconnect and deadline failures
  report once, stop both deadlines and discard queued work. Six new regression
  cases cover close and inactive search. Frontend QML runtime/binding errors
  now fail tests instead of being buried in warning output.
- Main QML/Forecast.js qmllint: zero diagnostics, no suppressions. Widget uses
  actual Omarchy qs imports with documented dynamic host/font contracts.
  Its six exact missing QProcess::ExitStatus diagnostics are an upstream
  Quickshell metadata defect; gate classifies only that exact message/ID and
  fails all other diagnostics. No host configs or external modules edited.
- Isolated Quickshell update-action fixture passes. Offscreen captures taken
  before QML edits and after; pixel/byte comparison will be repeated after the
  final writable-scroll correction. Shader/simulation algorithms remain unchanged.
- Final QML stage: 43 protocol / 37 frontend cases pass (four optional captures
  skip in the standard suite); all 12 explicit private capture images match
  byte-for-byte after writable-scroll fix, with no runtime warnings. Main
  qmllint, widget classification, format and shell syntax checks pass.
