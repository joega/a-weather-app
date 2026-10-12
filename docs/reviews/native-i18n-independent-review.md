# Focused independent runtime/request review

Reviewed native/qt/i18n.cpp,h and ui/qml/I18n.qml; also service search/request ownership boundary. No edits to reviewed root files. Reviewer model inherited from parent (no override); no native-speaker verification.

## Initial findings, resolved in the current tree

1. NativeI18n::match checks zh region before rejecting unknown four-character script. Thus zh-Latn-TW currently resolves Hant, while corrected Go and widget reject it. Move unsupported-script rejection before region matching. Script overrides known regions correctly for zh-Hant-CN and zh-Hans-TW.
2. `NativeI18n::date(kind="day")` always uses `ddd, MMM d`, imposing English-style date order even when regional locale uses day-month-year. Use a regional date format plus localized day name if appropriate. Also dates' weekday/month names come from the system formatting locale, so switching interface language under an English system retains English names. Confirm intended meaning of independent regional format versus translated dates and cover explicitly.
3. CatalogTranslator::read returns true for valid empty/irrelevant XML and does not filter context or validate placeholder/numerus completeness at runtime. Generated catalogs are trusted bundled resources, so no current network attack, but reliable English fallback should consider empty catalogs a load failure. Existing malformed XML path clears entries and falls back safely; App-only lookup prevents translating external contexts.
4. Current city/saved provider metadata does not refresh on language switch. Search and selection requests now carry effective language and cancellation blocks prior search results. Previously selected provider labels remain last-known fallback; online users need a separate bounded metadata refresh with generation/identity guards, never resetting forecast fetched_at or units. Root will address this remaining acceptance gap.
5. Go native notification dates now use neutral numeric local ISO dates, 24-hour times and zone, preserving city/DST but not system regional ordering/digits. Requirement for regional display independently of interface language should be explicitly tested or expanded.

## Verified properties

- Native matching hi-IN -> hi, en-IN -> en, ru -> uk, Hant-CN -> Hant and Hans-TW -> Hans is sound except finding 1.
- Qt plural code uk 1/2/5/11/21/22 and Polish 1/2/5/11/21/22 aligns Go forms; Hindi n<=1, French/Portuguese n<=1, one-form language handling is safe when generated forms count is correct.
- `QCoreApplication::installTranslator` installing the empty subclass before read works on installed Qt6: compiled /tmp/check-qt-translator.cpp probe output `installed=1 translation=translated`; no issue.
- `engine->retranslate()` plus changed/revision signals support immediate dynamic bindings; QML `.arg()` should be used at source placeholders so provider text is not recursively translated.
- Offline resource path only resolves whitelisted language code from Native apply. No runtime translation network or web runtime.
- Go weather values/country/timezones/forecast age unchanged by language setting; localized search invalidates generation/cancels pending worker, replacement waits for old worker slot. Canonical selected id/country/coords/timezone remain validated.
- /v1/get metadata receives captured language via context, not a mutable App.controls read in worker. Provider zh-Hant is lowercased zh-hant, never generic zh; documented provider fallback retains English/native names if translation unavailable.
- Source packaging now includes .ts assets in buildmeta source suffixes and widget Translations.js in copy/runtime manifest/fixtures. Go catalogs compile via embed.

## Evidence

`GOCACHE=/tmp/aweather-i18n-go-cache go test ./internal/app ./internal/weather ./internal/notifications ./internal/locale ./internal/release` passed (socket fixture escalation used). Additional package/buildmeta/updater regression remains in progress; source buildmeta currently rejects parent's Python generator per existing no-Python source policy.

Provider reference checked: https://open-meteo.com/en/docs/geocoding-api language parameter is lowercased and translated names fall back to English/native when unavailable. Qt reference: https://doc.qt.io/qt-6/qcoreapplication.html#installTranslator

## Resolution checkpoint

Finding 4 resolved by internal/app/place_metadata.go separate bounded metadata owner; full provider identity guards, one running/replacement pending, 8s context, cancellation at shutdown. Name-only persisted update avoids adopt resets. Pending weather completion aligns label after metadata change when canonical coordinates/timezone/country match; air-quality caching now ignores name-only differences. Dedicated tests latest-language cancellation/no overlap, five identity mutation rejections, offline retention, persistence, in-flight weather label retention passed.

Package/buildmeta and updater tests passed. Root owns remaining native date/empty catalog findings and reports fixes. English missing-catalog effective fallback allowed while explicit choice remains persisted.

## Final focused Qt global/lifetime review

Reviewed repaired NativeI18n constructor exports and native/qt/main.cpp ownership order. `weatherFormatNumber`/`weatherFormatDate` are per-engine bound invokable method values, not a raw QObject inserted as a JS global. Explicit CppOwnership prevents JS GC from deleting the stack-owned NativeI18n; main constructs the engine before NativeI18n, so NativeI18n is destroyed first and removes its process translator before engine teardown. QML QObject wrappers invalidate on QObject destruction; the retained method values do not transfer ownership. Current application lifetime has no actionable dangling-engine path. Source-level lifetime review agrees with root's passing shared-library formatting tests; no broad re-audit performed.

Number/date functions change display only. Canonical timestamps enter QDateTime and IANA QTimeZone before conversion; date keys stay QDate, not UTC-midnight instants. Numeric/regional layout stays QLocale(system), translated day-name uses effective interface locale. Chinese unsupported-script rejection now precedes region fallback; empty catalog now falls back English. Bundled resources are generated/trusted and coverage gates enforce placeholder/numerus shape, with unknown App sources falling back through QCoreApplication.

Regional notification gap resolved by root headless Qt helper + internal/locale/notification_time.go. Helper is rooted at trusted absolute Options.Root/native/qt/a-weather-app-qt, regular executable, no PATH/shell, 1-second context deadline, at most256 stdout bytes, validated UTF8/plain output, 32-label FIFO. Both notification owners call it only inside their existing bounded delivery goroutines after ledger reservation, preserving owner-mutex responsiveness, per-tick/cache bounds, and parent3-second cancellation. Failed/development helper returns local numeric ISO fallback. Scope includes immutable captured LC_TIME environment independent of interface choice.

Direct actual helper proof: LC_ALL empty, LC_TIME=de_DE.UTF-8, LANG=en_US.UTF-8 and LANGUAGE hi/uk, 2026-11-01T05:30Z/06:30Z in America/New_York -> 01.11.26 01:30 EDT / 01.11.26 01:30 EST, preserving DST distinction and regional date order without GUI.

## Final regression checkpoint

Full `GOCACHE=<repo>/.i18n-test-build/service-go-cache GOTMPDIR=/tmp/aw-ir go test -race ./...` passed every package, including updater72.7s. Initial race compile hit temporary filesystem quota; only this task's disposable /tmp/aweather-i18n-go-cache was cleaned, source/evidence preserved, and cache moved to the already excluded test-build directory on the main filesystem. First filesystem-backed app run exceeded Unix-domain socket path length with a long GOTMPDIR; short scratch path fixed the fixture environment.

After full suite, language matcher core strips singleton extensions before script/region matching, so zh-TW-u-nu-latn and zh-TW-x-Hans retain Hant, zh-u-nu-latn selects Hans, and unsupported core zh-Latn-TW remains rejected. Widget matcher8cases passed separately. NWS Unknown severity now maps to existing translated unknown display source while canonical code remains unchanged. Focused post-hardening race regressions on locale/notifications/app/weather passed.
