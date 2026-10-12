# Native internationalization implementation and QA

## Scope

The native app now supports English, Spanish, French, German, Portuguese,
Japanese, Simplified Chinese, Traditional Chinese, Ukrainian, Hindi, Italian,
Korean, Indonesian and Polish. Automatic selection follows ordered system
preferences; Russian silently aliases Ukrainian. Right-to-left layout is outside
this milestone. Catalogs and the Quickshell widget dictionary are bundled
offline. No translator service, font download, installed app or desktop setting
is used.

The catalog generator currently finds 1,025 App-context messages. Every locale
passes the complete coverage gate, including placeholders, plural-form shape,
and protected product, provider and legal text. Regional number/date formatting
remains tied to the system locale, while translated weekday names follow the
interface language and forecast instants use the selected city's timezone.
Language changes preserve canonical weather data, selected city, units, viewed
hour and open panels. Localized geocoding and metadata work uses bounded requests
with cancellation and identity/generation checks; metadata-only updates do not
refresh forecast age or reset presentation state.

## Verification

- `bash scripts/i18n_catalogs.sh --check --complete`: all 14 locales report
  1,025/1,025 messages.
- `make lint`, `make check-native-qml`, and `go vet ./...`: passed. Native
  analysis retained its two existing filtered warnings; QML analysis reports
  six exact upstream Quickshell QProcess enum metadata diagnostics and no
  project findings.
- `go test -race ./...`: passed when run with the repository's private socket
  fixtures outside the sandbox's socket restriction.
- `make -C native/qt test`: passed, including the new eight-case i18n test,
  protocol, full frontend/state-preservation, radar-service and warning
  journeys. Existing opt-in hardware-pixel and capture probes were skipped.
- The Qt i18n test binary passed again from `/tmp`, with no checkout-relative
  catalog files available. It covers locale matching and aliases, bundled
  catalogs and plural categories, shared JS formatting, Indian number grouping,
  six-script font shaping, and repeated New York 01:30 times across the DST
  transition.
- Captured and reviewed all 14 Settings views at narrow (700 px) and wide
  (1,180 px) sizes in `build/i18n-evidence-final/`. The interaction test also
  changes all 14 languages while a refresh is pending and verifies that stale
  snapshots cannot revert the latest choice or reset selected forecast state.
- Built a package-shaped runtime tree under `build/i18n-package-root/` from the
  current Go, Qt, shader, widget and native helper outputs. From that copied
  tree, `a-weather-app --version` returned 0.70.2; the offscreen Qt launch
  loaded one frontend root without QML errors. The copied Qt executable's
  notification formatter produced `01.11.26 01:30 EDT` and `01.11.26 01:30
  EST` for the two repeated New York times with `LC_TIME=de_DE.UTF-8`. This
  verifies the shipped Qt catalogs and widget files are available offline; it
  is not a signed or published release archive.

The pinned reproducible release-container build and its complete runtime source
manifest were not run: Docker access is denied to this account and noninteractive
sudo requires a password. A host-side Hyprland plugin rebuild also could not
complete its symbol check because the installed compositor binary is owned by
`nobody`, which the repository's immutable-asset reader correctly refuses. The
plugin source was not changed for this check. These host packaging limits do not
affect the Qt catalog smoke test or the Go race suite.

## Linguistic and visual limits

Catalogs were agent-authored and reviewed for source coverage, interpolation,
plural structure, protected identifiers and visible layout. This is not
native-speaker certification. Spanish and Portuguese use broadly neutral
dictionaries; regional vocabulary preferences need fluent-speaker review. The
screenshots cover Settings and the app behind it at two widths, while automated
frontend tests cover language-switch state retention. A separate visual review
of every forecast panel and every error/notification in all 14 languages was
not performed. Provider names and external diagnostics remain provider text or
are labeled as original technical details.

No push, tag, publication, installation, or live desktop/compositor
configuration change was performed.
