#!/usr/bin/bash
# Compare temporary builds of current sources; never publish release artifacts.
set -euo pipefail
umask 077
export LC_ALL=C.UTF-8 TZ=UTC PYTHONHASHSEED=0
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-0}
comparison_root=$(mktemp -d /tmp/weather-reproducibility.XXXXXXXX)
printf 'Temporary reproducibility builds: %s\n' "$comparison_root"
artifacts=(ui/shaders/atmosphere.frag.qsb native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere)
for copy in first second; do
  mkdir "$comparison_root/$copy"
  for folder in native ui weather scripts bridge godot packaging tests; do
    cp -a "$source_root/$folder" "$comparison_root/$copy/"
  done
  (
    cd "$comparison_root/$copy"
    export CFLAGS="-O2 -ffile-prefix-map=$PWD=."
    export CXXFLAGS="-O2 -ffile-prefix-map=$PWD=."
    python3 -I -B packaging/build_shaders.py
    make -B -C native/frame-alignment
    make -B -C native/atmosphere
    strip --strip-unneeded native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    python3 -I -B packaging/check_hardening.py native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    python3 -I -B scripts/check_plugin_symbols.py native/frame-alignment/a-weather-app-frame-alignment.so
    native/atmosphere/a-weather-app-atmosphere --self-test
    python3 -I -B tests/check_native_inputs.py "$PWD/native/atmosphere/a-weather-app-atmosphere"
  )
done
for artifact in "${artifacts[@]}"; do
  cmp "$comparison_root/first/$artifact" "$comparison_root/second/$artifact"
  sha256sum "$comparison_root/first/$artifact"
done
printf 'PASS: three byte-identical temporary artifact pairs; not release provenance.\n'
