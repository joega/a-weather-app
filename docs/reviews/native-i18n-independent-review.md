# Focused independent runtime/request review

Reviewed native/qt/i18n.cpp,h and ui/qml/I18n.qml; also service search/request ownership boundary. No edits to reviewed root files. Reviewer model inherited from parent (no override); no native-speaker verification.

## Findings requiring resolution before acceptance

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
