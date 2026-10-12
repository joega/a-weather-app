#!/usr/bin/env bash
# Keep the shipped widget self-contained for the pre-0.71 runtime inventory.
set -euo pipefail
widget_source=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
python3 - "$widget_source" "$@" <<'PY'
import pathlib, sys
if len(sys.argv) != 3:
    raise SystemExit('Usage: bash scripts/package_widget.sh OUTPUT_QML')
root, output = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
widget = (root/'quickshell/a-weather-app.weather/WeatherWidget.qml').read_text()
translations = (root/'quickshell/a-weather-app.weather/Translations.js').read_text()
if not widget.startswith('import "Translations.js" as Translations\n') or not translations.startswith('.pragma library\n'):
    raise SystemExit('Unexpected widget translation module layout')
widget = widget.split('\n', 1)[1].replace('Translations.', 'root.bundledTranslations.')
module = translations.split('\n', 1)[1]
exports = 'return {match: match, detect: detect, text: text, updateMessage: updateMessage};'
binding = '    readonly property var bundledTranslations: (function() {\n' + '\n'.join('        '+line for line in module.splitlines()) + '\n        '+exports+'\n    })()\n'
marker = '    id: root\n'
if widget.count(marker) != 1:
    raise SystemExit('Unexpected widget root layout')
output.parent.mkdir(parents=True, exist_ok=True)
output.write_text(widget.replace(marker, marker+binding, 1))
PY
