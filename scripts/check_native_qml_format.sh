#!/usr/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")/.."
mode=${1:-check}
[[ $mode == check || $mode == format ]]
qt_bins=$(qmake6 -query QT_INSTALL_BINS)
formatter=${QMLFORMAT:-$qt_bins/qmlformat}
command -v clang-format >/dev/null
[[ -x $formatter ]]
mapfile -t native_files < <(git ls-files 'native/*.c' 'native/*.h' 'native/*.cpp' 'native/*.hpp' 'tests/*.cpp' | sed '\|native/atmosphere/sky_shader.h|d')
mapfile -t qml_files < <(git ls-files 'ui/*.qml' 'ui/*.js' 'quickshell/*.qml')
if [[ $mode == format ]]; then
  clang-format -i "${native_files[@]}"
  for file in "${qml_files[@]}"; do "$formatter" -i "$file"; done
else
  clang-format --dry-run --Werror "${native_files[@]}"
  temporary=$(mktemp)
  trap 'rm -f -- "$temporary"' EXIT
  for file in "${qml_files[@]}"; do
    "$formatter" "$file" > "$temporary"
    if ! cmp -s "$file" "$temporary"; then
      printf 'Run make format-native-qml: %s\n' "$file" >&2; exit 1
    fi
  done
fi
