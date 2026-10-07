#!/usr/bin/bash
# Analyze maintained source with each target's real language and external includes.
set -euo pipefail
cd -- "$(dirname -- "$0")/.."
command -v clang-tidy >/dev/null
external_flags() { pkg-config --cflags "$@" | sed 's/-I/-isystem /g'; }
read -r -a c_flags <<< "$(external_flags gtk4 gtk4-layer-shell-0 json-glib-1.0 epoxy)"
read -r -a qt_flags <<< "$(external_flags Qt6Quick Qt6Network Qt6Test)"
read -r -a plugin_flags <<< "$(external_flags hyprland glesv2)"
clang-tidy native/atmosphere/atmosphere.c -- -std=c17 "${c_flags[@]}"
for file in native/qt/*.cpp; do
  clang-tidy "$file" -- -std=c++17 -fPIC "${qt_flags[@]}"
done
for file in native/frame-alignment/*.cpp native/physics/*.cpp native/snow/*.cpp tests/native_simulation.cpp; do
  clang-tidy "$file" -- -std=c++23 -fPIC "${plugin_flags[@]}"
done
