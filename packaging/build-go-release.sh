#!/usr/bin/bash
# Development-only release build. Packages and compiler work stay in Docker.
set -euo pipefail
umask 077
if [[ ! -f /.dockerenv ]] || ! awk '$5 == "/source" && $6 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'Refusing build: Docker with a read-only /source mount is required.\n' >&2
  exit 2
fi
source_root=/source
source /source/packaging/release-phases.sh
export MAKEFLAGS="-j${WEATHER_BUILD_JOBS:-2}"
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
    run_phase "$copy-native-tools" go build -o build/weather-native-check ./cmd/weather-native-check
    run_phase "$copy-package-tools" go build -o build/weather-package ./cmd/weather-package
    if [[ "$copy" == first ]]; then
      run_phase release-log-tests bash packaging/test_release_log_retention.sh
      run_phase release-phase-tests bash packaging/test_release_phases.sh
      run_phase vulnerability-policy-tests bash packaging/vulnerability_policy.sh test
      run_phase vulnerability-adapter-tests bash packaging/test_govulncheck_adapter.sh
      run_phase native-linkage-tests bash packaging/test_native_linkage.sh
      run_phase vulnerability-source bash packaging/check_release_vulnerabilities.sh source "$output_root/evidence"
      run_phase vulnerability-known-fixture bash packaging/test_govulncheck_fixture.sh /tmp/weather-release-scanner/govulncheck "$output_root/evidence/known-fixture"
      run_phase png-memory-budget bash packaging/test_map_png_budget.sh
      run_phase go-lint make lint
      run_phase go-race go test -race ./...
      run_phase go-vet go vet ./...
      run_phase qt-tests make -C native/qt test
      run_phase native-format make check-native-qml-format
      run_phase native-analysis make check-native-analysis
      run_phase qml-analysis make check-qml-analysis
      run_phase native-sanitizers bash packaging/check_native_sanitizers.sh
      run_phase qt-sanitizers bash packaging/check_qt_sanitizers.sh
    fi
    run_phase "$copy-shaders" build/weather-native-check shaders --root "$checkout"
    run_phase "$copy-go-build" make go APP_MODE=package APP_VERSION="$release_version"
    (
      cd native/qt
      run_phase "$copy-qmake" qmake6 a-weather-app-qt.pro -o .build.mk \
        "QMAKE_CXXFLAGS+=-ffile-prefix-map=$checkout=. -fstack-protector-strong -D_FORTIFY_SOURCE=3" \
        'QMAKE_LFLAGS+=-Wl,-z,relro,-z,now,-z,noexecstack'
      run_phase "$copy-qt-build" make -B -f .build.mk
    )
    run_phase "$copy-plugin-build" make -B -C native/frame-alignment
    run_phase "$copy-atmosphere-build" make -B -C native/atmosphere
    if [[ "$copy" == first ]]; then
      run_phase vulnerability-binary bash packaging/check_release_vulnerabilities.sh binary "$output_root/evidence" build/a-weather-app
    fi
    run_phase "$copy-strip" strip --strip-unneeded native/qt/a-weather-app-qt native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    run_phase "$copy-symbols" build/weather-native-check symbols native/frame-alignment/a-weather-app-frame-alignment.so
    run_phase "$copy-hardening" build/weather-native-check hardening build/a-weather-app native/qt/a-weather-app-qt native/frame-alignment/a-weather-app-frame-alignment.so native/atmosphere/a-weather-app-atmosphere
    run_phase "$copy-atmosphere-self-test" native/atmosphere/a-weather-app-atmosphere --self-test
    run_phase "$copy-native-inputs" build/weather-native-check inputs native/atmosphere/a-weather-app-atmosphere
    for executable in native/qt/a-weather-app-qt native/atmosphere/a-weather-app-atmosphere; do
      run_phase "$copy-dependencies-$(basename "$executable")" bash -c 'ldd "$1" > "$2"' bash "$executable" "$build_root/$(basename "$executable")-$copy.dependencies"
      if grep -q 'not found' "$build_root/$(basename "$executable")-$copy.dependencies"; then exit 1; fi
    done
    # QML resource loading is display-free; actual compositor/shader QA remains a desktop check.
    status=0
    qml_start=$(date +%s%N)
    env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software \
      timeout 3s native/qt/a-weather-app-qt --socket "$build_root/absent.sock" --diagnostic > "$build_root/qml-$copy.log" 2>&1 || status=$?
    printf '%s-qml-smoke\t%s\t%s\n' "$copy" "$((($(date +%s%N)-qml_start)/1000000))" "$status" >> "$PHASE_TIMINGS"
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
  run_phase "compare-$artifact" cmp "$build_root/first/$relative" "$build_root/second/$relative"
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
run_phase package-manifest "$build_root/first/build/weather-package" create --root "$runtime_root" --source /source
run_phase native-linkage bash "$build_root/first/packaging/collect_native_linkage.sh" "$runtime_root" "$output_root/evidence/native-linkage"
run_phase packaged-integration make -C "$build_root/first/native/qt" test-e2e GO_APP="$runtime_root/a-weather-app"
run_phase runtime-verification bash "$build_root/first/scripts/verify_go_runtime.sh" "$runtime_root" /source "$build_root/first/build/weather-package"
# Complete runtime validation before the reviewed native-coverage gate. A
# confirmed finding or assessment failure still prevents release archive creation.
run_phase vulnerability-native bash "$build_root/first/packaging/check_release_vulnerabilities.sh" native "$output_root/evidence" "$runtime_root/packaging/runtime.json"
run_phase archive tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 --numeric-owner \
  -C "$runtime_root" -cf "$output_root/a-weather-app-go-qt-linux-x86_64.tar" .
(
  cd "$output_root"
  sha256sum a-weather-app-go-qt-linux-x86_64.tar > SHA256SUMS
)
install -m 644 "$runtime_root/packaging/runtime.json" "$output_root/go-runtime.json"
printf 'PASS: local Go/Qt package built. Source commit and dirty status are recorded; no release attestation is implied.\n'
