#!/usr/bin/bash
# Offline native memory-safety checks in disposable copies, never installed paths.
set -euo pipefail
umask 077
export ASAN_OPTIONS=detect_leaks=1:halt_on_error=1
export UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
sanitizer_root=$(mktemp -d /tmp/weather-native-sanitizers.XXXXXXXX)
printf 'Temporary sanitizer checks: %s\n' "$sanitizer_root"
mkdir -p "$sanitizer_root/native" "$sanitizer_root/tests" "$sanitizer_root/godot"
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
env CFLAGS='-O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer' \
  make -B -C "$sanitizer_root/native/atmosphere"
"$sanitizer_root/native/atmosphere/a-weather-app-atmosphere" --self-test
python3 -I -B "$source_root/tests/check_native_inputs.py" \
  "$sanitizer_root/native/atmosphere/a-weather-app-atmosphere"
printf 'PASS: simulation and native input checks with address, undefined-behavior and leak sanitizers.\n'
