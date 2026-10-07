#!/usr/bin/bash
# Analyze maintained source with each target's real language and external includes.
set -euo pipefail
cd -- "$(dirname -- "$0")/.."
command -v clang-tidy >/dev/null
external_flags() { pkg-config --cflags "$@" | sed 's/-I/-isystem /g'; }
read -r -a c_flags <<< "$(external_flags gtk4 gtk4-layer-shell-0 json-glib-1.0 epoxy)"
read -r -a qt_flags <<< "$(external_flags Qt6Quick Qt6Network Qt6Test)"
read -r -a plugin_flags <<< "$(external_flags hyprland glesv2)"
source packaging/release-phases.sh
jobs=${WEATHER_BUILD_JOBS:-2}
[[ $jobs =~ ^[1-4]$ ]]
analyze_c() { clang-tidy "$1" -- -std=c17 "${c_flags[@]}"; }
analyze_qt() { clang-tidy "$1" -- -std=c++17 -fPIC "${qt_flags[@]}"; }
analyze_plugin() { clang-tidy "$1" -- -std=c++23 -fPIC "${plugin_flags[@]}"; }
run_parallel "$jobs" analyze_c native/atmosphere/atmosphere.c
run_parallel "$jobs" analyze_qt native/qt/*.cpp
run_parallel "$jobs" analyze_plugin native/frame-alignment/*.cpp native/physics/*.cpp native/snow/*.cpp tests/native_simulation.cpp
