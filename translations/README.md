# Native offline translations

`messages/<language>.json` is the authored input. Keys are exact English source
messages in the `App` context. A string value is a translation; a string array is
an ordered Qt/Go numerus translation. `%1` through `%9` and `%n` must survive
unchanged, with the same occurrence counts. Arrays contain one form for Japanese,
both Chinese scripts, Korean and Indonesian; three forms (one/few/many) for
Ukrainian and Polish; and two forms (one/other) for all other supported languages.
Code selects forms; dictionaries do not encode numeric business rules.

Run `bash scripts/i18n_catalogs.sh` after changing UI sources or dictionaries.
This extracts source strings and locations, writes `sources.json`, generates
`app_<language>.ts` Qt XML and `internal/locale/catalogs/<language>.json`, updates
the filtered Quickshell dictionary payload and localized desktop Comments, and checks
placeholders, plural counts and protected product/provider/legal attribution. Generated
artifacts are replaced atomically after validation. The Quickshell payload contains
only its UI, bar labels and dynamic updater-error sources (currently 131 per locale). It uses Python 3 as a development tool only; the
packaged application loads bundled XML/JSON and has no Python runtime dependency.
`bash scripts/i18n_catalogs.sh --check --complete` must succeed before claiming
complete translation coverage. Without `--complete`, unfinished translations are
reported rather than treated as successful coverage. Missing Qt messages carry
`type="unfinished"`; missing Go/widget entries fall back to English source text.

`coverage.json` records missing and obsolete keys, plus source-identical values
requiring review. `reviewed-identical.json` documents deliberately unchanged
technical/legal tokens or terms spelled identically in a locale. Obsolete authored
keys may be retained during UI changes so translation work is not silently lost;
generated catalogs include current sources only. Final reviewed dictionaries should
prune obsolete entries after confirming the replacement source is translated.

`bash scripts/i18n_reuse_website.sh` can seed exact meaning-identical terms from
the read-only website dictionaries in `/home/joeg/Projects/a-weather-site`. It
preserves existing authored work. Website dictionaries are not packaged or loaded
by the native application. Website reviewed wording was reused for conditions,
basic metrics and freshness plural forms where meaning matched.

All fourteen languages now cover every current source. Native-specific wording was
authored by the session agents and checked for source coverage, interpolation,
plural structure and protected identifiers. Website reviewed terminology was
reused where meaning matched. This is agent review, not native-speaker
certification. Spanish and Portuguese share one broadly neutral dictionary each;
regional vocabulary preferences remain subject to linguistic review. French uses
« ancien » for stale data and « expiré » for unusable expired data. Indonesian
uses « presipitasi » for rain/snow-inclusive precipitation; short metric labels
retain established technical abbreviations. AQI remains AQI in native copy to
match named index standards. Some UI fragments still inherit grammatical
composition limits; fully parameterized sentences should be preferred for
subsequent copy changes.

Provider names, executable commands, legal attribution, scientific units and
source-identical words native to a language are deliberately preserved.
`reviewed-identical.json` records the manually checked source-identical values.
Bundled resource presence alone does not establish full localization: run the
completeness check whenever changing sources. The script validates structure and
identity preservation; it cannot certify linguistic meaning or rendered layout.

Extraction tracks explicit `I18n.t`/`I18n.source`, `qsTranslate("App", ...)`,
C++ App translation markers, Go locale calls and scoped Quickshell
`text(code, "source", args)` calls (excluding generated dictionary data). It cannot infer arbitrary
strings carried in service fields. Such app-authored dynamic values must have
an explicit frontend translation map or be recorded in `extra-sources.json`.
Read-only review of Go `message`/`label`/`summary`/`title`/`description`/`hint`/`notice`
map literals found the unsupported-installation updater status was missing;
updater `Message` assignments and parameterized rollback messages were also
outside the original marker extraction. The native updater presentation map
covers these known authored statuses; system diagnostics retain their original
text under an explicit diagnostic label so recovery detail is not discarded.

The final updater inventory adds 99 known statuses/diagnostics and nine standard
CAP urgency/certainty labels. All 1,025 current source messages are covered in each
of the fourteen locales. Parameterized security/recovery diagnostics retain
checksum, release-reference, metadata, ownership, archive-bound and restoration
distinctions; unknown operating-system/provider text is labeled as original
technical detail. These additions reuse the same offline catalogs and validation.
