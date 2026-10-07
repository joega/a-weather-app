#!/usr/bin/bash
# Disposable Qt protocol and map ownership fixtures; no live providers/display.
set -euo pipefail
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
qt_root=$(mktemp -d /tmp/weather-qt-sanitizers.XXXXXXXX)
printf 'Temporary Qt sanitizer checks: %s\n' "$qt_root"
mkdir -p "$qt_root/native"
cp -a "$source_root/native/qt" "$qt_root/native/"
cp -a "$source_root/ui" "$qt_root/"
cd "$qt_root/native/qt"
rm -rf .test-build .frontend-test-build
flags='-O1 -g -fsanitize=address,undefined -fno-omit-frame-pointer'
qmake6 tests/protocol-test.pro -o .test.mk "QMAKE_CXXFLAGS+=$flags" 'QMAKE_LFLAGS+=-fsanitize=address,undefined'
make -f .test.mk -j2
export ASAN_OPTIONS=detect_leaks=1:halt_on_error=1
export UBSAN_OPTIONS=halt_on_error=1:print_stacktrace=1
./protocol-test
qmake6 tests/frontend-test.pro -o .frontend-test.mk "QMAKE_CXXFLAGS+=$flags" 'QMAKE_LFLAGS+=-fsanitize=address,undefined'
make -f .frontend-test.mk -j2
env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software \
  ./frontend-test mapTileMetadataPolicy mapTileInvalidPNGs mapTileDestructionWithActiveReplies mapTileObserverCancelsDelivery mapTileCloseDiscardsDelayedReply mapTileDownloadLimit
printf 'PASS: Qt protocol and map ownership checks with address, undefined-behavior and leak sanitizers.\n'
