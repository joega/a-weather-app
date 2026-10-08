#!/usr/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")/.."
qt_bins=$(qmake6 -query QT_INSTALL_BINS)
linter=${QMLLINT:-$qt_bins/qmllint}
[[ -x $linter ]]
# qmlformat/qmllint currently accept duplicate behavior pragmas; runtime rejects them.
python3 - <<'PY'
from pathlib import Path
for file in Path('ui/qml').rglob('*.qml'):
    if file.read_text().count('pragma ComponentBehavior:') > 1:
        raise SystemExit(f'{file}: duplicate ComponentBehavior pragma')
PY
"$linter" -W 0 ui/qml/*.qml ui/qml/backend/*.qml ui/qml/Forecast.js
shell_root=${OMARCHY_SHELL_ROOT:-/usr/share/omarchy/shell}
if [[ ! -f $shell_root/Ui/BarWidget.qml || ! -f $shell_root/Commons/qmldir ]]; then
  printf 'Omarchy modules unavailable; widget analysis did not run. Set OMARCHY_SHELL_ROOT.\n' >&2
  exit 2
fi
imports=$(mktemp -d "${TMPDIR:-/tmp}/weather-qml-analysis.XXXXXXXX")
trap 'rm -rf -- "$imports"' EXIT
mkdir "$imports/qs"
ln -s "$shell_root/Ui" "$imports/qs/Ui"
ln -s "$shell_root/Commons" "$imports/qs/Commons"
# Use real modules. The exact upstream enum metadata defect is classified below;
# every other diagnostic fails, including imports, syntax, and unqualified access.
"$linter" -I "$imports" --json "$imports/widget.json" quickshell/a-weather-app.weather/WeatherWidget.qml
python3 - "$imports/widget.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))
files = data['files']
if len(files) != 1:
    raise SystemExit('widget analysis did not return exactly one file')
known = 'Type QProcess::ExitStatus of parameter exitStatus in signal called exited was not found, but is required to compile onExited. Did you add all imports and dependencies?'
exceptions = 0
for diagnostic in files[0]['warnings']:
    if diagnostic['id'] == 'signal-handler-parameters' and diagnostic['message'] == known:
        exceptions += 1
    else:
        raise SystemExit(str(diagnostic))
print(f'Widget analysis: no project findings; {exceptions} exact upstream Quickshell QProcess enum metadata diagnostics remain.')
PY
