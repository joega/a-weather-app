#!/usr/bin/bash
# Offline native memory-safety checks in disposable copies, never installed paths.
set -euo pipefail
umask 077
export ASAN_OPTIONS=detect_leaks=1:halt_on_error=1
export UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
make -C "$source_root" native-tools
native_check="$source_root/build/weather-native-check"
sanitizer_root=$(mktemp -d /tmp/weather-native-sanitizers.XXXXXXXX)
printf 'Temporary sanitizer checks: %s\n' "$sanitizer_root"
mkdir -p "$sanitizer_root/native" "$sanitizer_root/tests" "$sanitizer_root/godot"
mkdir -p "$sanitizer_root/native/frame-alignment"
for header in activity.hpp rain_control.hpp schedule.hpp; do
  cp "$source_root/native/frame-alignment/$header" "$sanitizer_root/native/frame-alignment/"
done
for component in atmosphere physics snow; do
  cp -a "$source_root/native/$component" "$sanitizer_root/native/"
done
cp -a "$source_root/godot/shaders" "$sanitizer_root/godot/"
cp "$source_root/tests/native_simulation.cpp" "$sanitizer_root/tests/"
c++ -std=c++23 -O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer \
  "$sanitizer_root/tests/native_simulation.cpp" \
  "$sanitizer_root/native/physics/simulation.cpp" \
  "$sanitizer_root/native/snow/simulation.cpp" -o "$sanitizer_root/simulations"
"$sanitizer_root/simulations"
# Supply flags via the environment so the Makefile still adds include paths and
# its warning/hardening flags; command-line CFLAGS would override those additions.
env CFLAGS='-O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer' NATIVE_CHECK="$native_check" \
  make -B -C "$sanitizer_root/native/atmosphere"
"$sanitizer_root/native/atmosphere/a-weather-app-atmosphere" --self-test
"$native_check" inputs "$sanitizer_root/native/atmosphere/a-weather-app-atmosphere"
# GOption allocations must also be released on display-free early returns.
atmosphere="$sanitizer_root/native/atmosphere/a-weather-app-atmosphere"
"$atmosphere" --weather unused --preset clear --self-test
for failure in conflicting-options unknown-option invalid-preset missing-policy; do
  case "$failure" in
    conflicting-options) args=(--monitor 0 --weather unused --preset clear) ;;
    unknown-option) args=(--weather unused --unknown-option) ;;
    invalid-preset) args=(--monitor 0 --preset invalid) ;;
    missing-policy) args=(--weather unused --renewable-lease) ;;
  esac
  status=0
  "$atmosphere" "${args[@]}" > "$sanitizer_root/$failure.log" 2>&1 || status=$?
  if [[ $status != 1 ]] || grep -Eq 'Sanitizer|runtime error:' "$sanitizer_root/$failure.log"; then
    cat "$sanitizer_root/$failure.log" >&2
    printf 'FAIL: atmosphere early return %s (exit %s)\n' "$failure" "$status" >&2
    exit 1
  fi
 done

printf 'PASS: simulation and native input checks with address, undefined-behavior and leak sanitizers.\n'
