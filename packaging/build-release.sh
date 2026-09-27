#!/usr/bin/bash
# Run inside the pinned build container; never installs anything on the desktop.
set -euo pipefail
umask 077
export LC_ALL=C.UTF-8 TZ=UTC PYTHONHASHSEED=0
SOURCE_DATE_EPOCH="$(git show -s --format=%ct HEAD)"
export SOURCE_DATE_EPOCH
source_root="$PWD"
build_root=$(mktemp -d)
trap 'rm -rf -- "$build_root"' EXIT
artifacts=(ui/shaders/atmosphere.frag.qsb native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere)
for copy in first second; do
  mkdir "$build_root/$copy"
  git archive HEAD | tar --no-same-owner --no-same-permissions -x -C "$build_root/$copy"
  (
    cd "$build_root/$copy"
    rm -f -- "${artifacts[@]}" packaging/runtime.json
    export CFLAGS="-O2 -ffile-prefix-map=$PWD=."
    export CXXFLAGS="-O2 -ffile-prefix-map=$PWD=."
    python3 -I -B packaging/build_shaders.py
    make -C native/frame-alignment
    make -C native/atmosphere
    strip --strip-unneeded native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    python3 -I -B scripts/check_plugin_symbols.py native/frame-alignment/a-weather-app-frame-alignment.so
    ldd native/atmosphere/a-weather-app-atmosphere > atmosphere-dependencies.txt
    if grep -q 'not found' atmosphere-dependencies.txt; then exit 1; fi
  )
done
mkdir -p "$source_root/dist"
for artifact in "${artifacts[@]}"; do
  cmp "$build_root/first/$artifact" "$build_root/second/$artifact"
  install -D -m 644 "$build_root/first/$artifact" "$source_root/$artifact"
done
chmod 755 "$source_root/native/atmosphere/a-weather-app-atmosphere"
python3 -I -B packaging/release.py create
python3 -I -B packaging/release.py verify
# Stable paths, order, ownership and timestamps make the transport reproducible.
tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
  -cf dist/a-weather-app-linux-x86_64.tar "${artifacts[@]}" packaging/runtime.json
sha256sum dist/a-weather-app-linux-x86_64.tar > dist/SHA256SUMS
