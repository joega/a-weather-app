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
the Quickshell dictionary payload and localized desktop Comments, and checks
placeholders and plural counts. It uses Python 3 as a development tool only; the
packaged application loads bundled XML/JSON and has no Python runtime dependency.
`bash scripts/i18n_catalogs.sh --check --complete` must succeed before claiming
complete translation coverage. Without `--complete`, unfinished translations are
reported rather than treated as successful coverage. Missing Qt messages carry
`type="unfinished"`; missing Go/widget entries fall back to English source text.

`coverage.json` records missing and obsolete keys, plus source-identical values
requiring review. `reviewed-identical.json` documents deliberately unchanged
technical/legal tokens or terms spelled identically in a locale. Obsolete authored
keys are retained during UI changes so translation work is not silently lost;
generated catalogs include current sources only.

`bash scripts/i18n_reuse_website.sh` can seed exact meaning-identical terms from
the read-only website dictionaries in `/home/joeg/Projects/a-weather-site`. It
preserves existing authored work. Website dictionaries are not packaged or loaded
by the native application. Website reviewed wording was reused for conditions,
basic metrics and freshness plural forms where meaning matched.

Spanish native-specific wording was authored by the session agent and checked for
source coverage, interpolation and plural structure. It has not received
native-speaker certification. Spanish uses neutral wording with some regional
preferences (pronóstico, ordenador/desktop concepts, AQI rather than ICA) still
subject to linguistic review. Provider names, legal attribution, technical units
and source-identical Spanish terms are deliberately preserved. Some remaining
UI fragments inherit grammatical composition limits; fully parameterized
sentences should be preferred for subsequent copy changes.

Other locale dictionaries currently contain only matched website terms until
authored translations are supplied. Generated resource presence does not establish
full localization; inspect `coverage.json` and use `--complete`.
