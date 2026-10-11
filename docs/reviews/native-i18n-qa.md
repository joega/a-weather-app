# Native internationalization candidate — work in progress

Objective: `.codex/attachments/5501fe9b-17b1-4f6e-95f0-78dd90500b24/goal-objective.md`.
The initial checkout was clean and no applicable AGENTS.md was present. Website
sources in `/home/joeg/Projects/a-weather-site` are read-only references. Its
contracts, language implementation, i18n and SEO QA were read before editing.

This is not yet a completed release candidate. Missing catalog entries remain
explicitly unfinished and fall back to usable English. Coverage completion and
all-language rendered acceptance remain required.

## Architecture and boundaries

QML/JS use App-context English source keys and Qt translation interpolation.
`I18n.qml` exposes revision-driven rendering, language choice and regional display
helpers. `NativeI18n` installs a native QTranslator subclass and calls engine
retranslate without recreating the engine or canonical weather objects. Qt
Linguist tools are unavailable on this host; the translator reads bundled TS
resources using QXmlStreamReader, with explicit Qt numerus form selection.
Resources live inside the native executable RCC; no network or translation
service is involved. English is bundled and is the fallback.

Choice persists in existing controls state. `auto` uses ordered system preference
matching. Russian matches Ukrainian without an explanatory UI. Chinese script
precedes region; unsupported scripts must not select Simplified Chinese.
QLocale regional formatting remains independent of translated interface language;
canonical instants convert using QTimeZone, retaining city DST and date identity.
Native font selection uses installed Noto script families when available and
falls back to system sans; no font downloads or desktop changes are involved.

The service injects validated effective language into geocoding requests. Search
cancellation and generations reject obsolete results; one running and one pending
request remain bounded. Selection resolves with a captured language context rather
than reading GUI language from a worker goroutine. Provider text and notices are
not treated as app-authored translation sources. Forecast age, country codes,
units, coordinates and weather calculation inputs remain canonical.

Go desktop notifications use embedded catalogs, and the standalone Quickshell
widget uses generated offline dictionaries. Packaging includes the widget helper
and resource sources in provenance; native/Qt embeds its own catalogs.

## Observed evidence so far

- `make qt`: native executable compiled after core integration.
- `make -C native/qt test-i18n`: six test functions passed, including hi-IN,
  en-IN, Chinese script/region variants, Russian alias, supported preference
  ordering and unsupported fallback. en-IN retains `12,34,567` after a Hindi
  choice. New York repeated 01:30 across November 1, 2026 DST transition renders
  the same wall time from distinct UTC timestamps.
- QTextLayout script coverage: Japanese, both Chinese scripts, Ukrainian, Hindi
  and Korean produce valid nonzero glyphs. Hindi uses Noto Sans Devanagari and
  produces shaped glyph runs. This is not a visual/native-speaker certification.
  Initial generic CJK fallback selected KR; language-specific family selection
  was added and needs the final rendered verification.
- Focused Go application/weather/locale/notification regression tests passed
  in agent runs; final lint/race/full package evidence is still pending.
- Initial full Qt regression encountered local socket-listen restrictions in
  the sandbox. These checks require the isolated fixture run outside that
  restriction; failed sandbox runs are not passing evidence.
- Root review found missing set_language serialization and repeated-choice
  queue risks. UI owner added explicit serialization, latest-wins language
  coalescing, old-snapshot sync protection, and immediate search token
  invalidation. Focused native regression is being added.

## Linguistic and verification limits

Translations reuse website terminology only where English meaning matches.
They receive agent review, not native-speaker verification. App-specific long
sentences, grammatical interpolation, plurals, script fonts and narrow/scaled
layouts require per-language checks before completion. External provider place
names may use English or native-name fallback. Existing place labels currently
retain last-known metadata on switches; independent localized metadata refresh
must be resolved or explicitly recorded as a gap.

All agents inherit this Codex session's model; no explicit model override was
used. The tools do not expose a verified serving-model identifier, so none is
invented here. UI work used one bounded JS child with separate file ownership;
service and catalog work have separate ownership. Independent review evidence
will be retained here when available.

No push, tag, publication, installed-runtime replacement, or live desktop or
compositor configuration change has been performed.
