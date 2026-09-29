#!/usr/bin/bash
# Development-only release build. Packages and compiler work stay in Docker.
set -euo pipefail
umask 077
if [[ ! -f /.dockerenv ]] || ! awk '$5 == "/source" && $6 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'Refusing build: Docker with a read-only /source mount is required.\n' >&2
  exit 2
fi
source_root=/source
output_root=${GO_RELEASE_OUTPUT:-/output}
if [[ "$output_root" != /output || ! -d "$output_root" ]]; then
  printf 'Build output must be the isolated /output bind mount.\n' >&2
  exit 2
fi
source_version=$(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)",?$/\1/p' "$source_root/manifest.json")
release_version=${WEATHER_RELEASE_VERSION:-$source_version}
if [[ ! "$source_version" =~ ^0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ || ! "$release_version" =~ ^0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]]; then
  printf 'Expected 0.MINOR.PATCH source and release versions.\n' >&2; exit 2
fi
export WEATHER_RELEASE_VERSION="$release_version"
export LC_ALL=C.UTF-8 TZ=UTC
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(git -c safe.directory=/source -C /source show -s --format=%ct HEAD)
export GOTOOLCHAIN=local
build_root=$(mktemp -d /tmp/weather-go-release.XXXXXXXX)
trap 'rm -rf -- "$build_root"' EXIT
artifacts=(a-weather-app native/qt/a-weather-app-qt ui/shaders/atmosphere.frag.qsb native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere)
for copy in first second; do
  checkout="$build_root/$copy"
  mkdir "$checkout"
  cp -a --no-preserve=ownership "$source_root/." "$checkout/"
  # Only remove compiler products in this newly allocated disposable copy.
  rm -rf -- "$checkout/build" "$checkout/native/qt/.build" "$checkout/native/qt/.test-build" "$checkout/native/qt/.frontend-test-build" "$checkout/native/qt/.service-test-build"
  rm -f -- "$checkout/native/qt/.build.mk" "$checkout/native/qt/.test.mk" "$checkout/native/qt/.frontend-test.mk" "$checkout/native/qt/.service-test.mk" "$checkout/native/qt/.qmake.stash" "$checkout/native/qt/a-weather-app-qt" "$checkout/native/qt/protocol-test" "$checkout/native/qt/frontend-test" "$checkout/native/qt/service-frontend-test"
  (
    cd "$checkout"
    export CFLAGS="-O2 -ffile-prefix-map=$PWD=."
    export CXXFLAGS="-O2 -ffile-prefix-map=$PWD=."
    export GOFLAGS='-trimpath -buildvcs=false'
    # Independent compiler caches prevent the second reproducibility pass from
    # merely reusing the first Go executable or intermediate object files.
    export GOCACHE="$build_root/go-cache-$copy"
    mkdir -p build
    go build -o build/weather-native-check ./cmd/weather-native-check
    go build -o build/weather-package ./cmd/weather-package
    if [[ "$copy" == first ]]; then
      go test -race ./...
      go vet ./...
      make -C native/qt test
      bash packaging/check_native_sanitizers.sh
    fi
    build/weather-native-check shaders --root "$checkout"
    make go APP_MODE=package APP_VERSION="$release_version"
    (
      cd native/qt
      qmake6 a-weather-app-qt.pro -o .build.mk \
        "QMAKE_CXXFLAGS+=-ffile-prefix-map=$checkout=. -fstack-protector-strong -D_FORTIFY_SOURCE=3" \
        'QMAKE_LFLAGS+=-Wl,-z,relro,-z,now,-z,noexecstack'
      make -B -f .build.mk
    )
    make -B -C native/frame-alignment
    make -B -C native/atmosphere
    strip --strip-unneeded native/qt/a-weather-app-qt native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    build/weather-native-check symbols native/frame-alignment/a-weather-app-frame-alignment.so
    build/weather-native-check hardening build/a-weather-app native/qt/a-weather-app-qt native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    native/atmosphere/a-weather-app-atmosphere --self-test
    build/weather-native-check inputs native/atmosphere/a-weather-app-atmosphere
    for executable in native/qt/a-weather-app-qt native/atmosphere/a-weather-app-atmosphere; do
      ldd "$executable" > "$build_root/$(basename "$executable")-$copy.dependencies"
      if grep -q 'not found' "$build_root/$(basename "$executable")-$copy.dependencies"; then exit 1; fi
    done
    # QML resource loading is display-free; actual compositor/shader QA remains a desktop check.
    status=0
    env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software \
      timeout 3s native/qt/a-weather-app-qt --socket "$build_root/absent.sock" --diagnostic > "$build_root/qml-$copy.log" 2>&1 || status=$?
    cat "$build_root/qml-$copy.log"
    if [[ "$status" != 124 ]] || ! grep -Fxq 'Weather frontend roots: 1' "$build_root/qml-$copy.log"; then
      printf 'Offscreen QML smoke check failed (status %s).\n' "$status" >&2; exit 1
    fi
    if grep -Eq 'QQmlApplicationEngine failed|ReferenceError:|TypeError:|Error:|is not installed|returned invalid data|Cannot assign|Unable to assign|Binding loop|qrc:/[^ ]+:[0-9]+:' "$build_root/qml-$copy.log"; then
      printf 'Offscreen QML reported a loading or validation error.\n' >&2; exit 1
    fi
  )
done
for artifact in "${artifacts[@]}"; do
  relative=$artifact
  [[ "$relative" == a-weather-app ]] && relative=build/a-weather-app
  cmp "$build_root/first/$relative" "$build_root/second/$relative"
done
printf 'PASS: five artifacts byte-identical across independent build paths.\n'
runtime_root="$build_root/runtime"
mkdir "$runtime_root"
for artifact in "${artifacts[@]}"; do
  relative=$artifact
  mode=644
  case "$artifact" in a-weather-app) relative=build/a-weather-app;mode=755;; native/qt/a-weather-app-qt|native/atmosphere/a-weather-app-atmosphere) mode=755;; esac
  install -D -m "$mode" "$build_root/first/$relative" "$runtime_root/$artifact"
done
for asset in LICENSE THIRD_PARTY_NOTICES.md manifest.json packaging/a-weather-app.desktop packaging/icons/a-weather-app.svg quickshell/a-weather-app.weather/WeatherWidget.qml packaging/go-README.md; do
  install -D -m 644 "$build_root/first/$asset" "$runtime_root/$asset"
done
sed -i "s/\"version\": \"$source_version\"/\"version\": \"$release_version\"/" "$runtime_root/manifest.json"
cp -a --no-preserve=ownership "$build_root/first/licenses" "$runtime_root/licenses"
install -m 644 "$build_root/first/packaging/go-README.md" "$runtime_root/README.md"
git -c safe.directory=/source -C /source status --porcelain --untracked-files=all
"$build_root/first/build/weather-package" create --root "$runtime_root" --source /source
make -C "$build_root/first/native/qt" test-e2e GO_APP="$runtime_root/a-weather-app"
bash "$build_root/first/scripts/verify_go_runtime.sh" "$runtime_root" /source "$build_root/first/build/weather-package"
tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
  -C "$runtime_root" -cf "$output_root/a-weather-app-go-qt-linux-x86_64.tar" .
(
  cd "$output_root"
  sha256sum a-weather-app-go-qt-linux-x86_64.tar > SHA256SUMS
)
install -m 644 "$runtime_root/packaging/runtime.json" "$output_root/go-runtime.json"
printf 'PASS: local Go/Qt package built. Source commit and dirty status are recorded; no release attestation is implied.\n'
