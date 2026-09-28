#!/usr/bin/bash
# Development audit only; not part of the published application's runtime.
set -euo pipefail
umask 077
runtime_root=$(realpath -- "${1:?Usage: verify_go_runtime.sh RUNTIME_ROOT SOURCE_ROOT}")
source_root=$(realpath -- "${2:?Usage: verify_go_runtime.sh RUNTIME_ROOT SOURCE_ROOT}")
package_tool=${3:-"$source_root/build/weather-package"}
if [[ ! -x "$package_tool" ]]; then
  printf 'Compiled weather-package development tool is required.\n' >&2; exit 1
fi
"$package_tool" verify --root "$runtime_root" --source "$source_root"
if find "$runtime_root" \( -type f \( -iname '*.py' -o -iname '*.pyc' -o -iname '*.pyo' -o -iname '*.pyi' -o -iname '*.pyw' \) -o -type d -name '__pycache__' \) -print -quit | grep -q .; then
  printf 'Runtime contains Python files.\n' >&2; exit 1
fi
for executable in a-weather-app native/qt/a-weather-app-qt native/atmosphere/a-weather-app-atmosphere; do
  file "$runtime_root/$executable" | grep -q 'ELF 64-bit' || { printf 'Expected compiled ELF: %s\n' "$executable" >&2; exit 1; }
done
audit_root=$(mktemp -d /tmp/weather-go-runtime-check.XXXXXXXX)
cleanup_audit() {
  local runtime_status=$?
  if (( runtime_status != 0 )); then
    # The disposable Docker container will vanish. Emit these private logs into
    # the outer build log before removing temporary evidence, including failures
    # that set -e catches before the ordinary success-path output below.
    printf 'Runtime gate failed (status %s); diagnostic files follow.\n' "$runtime_status" >&2
    local runtime_log
    for runtime_log in "$audit_root/runtime.log" "$audit_root/service.log" \
        "$audit_root/exec.log" "$audit_root/service-exec.log" \
        "$audit_root/state/guardian-last-error.log"; do
      if [[ -f "$runtime_log" ]]; then
        printf 'Diagnostic file: %s\n' "$runtime_log" >&2
        cat -- "$runtime_log" >&2 || true
      fi
    done
  fi
  rm -rf -- "$audit_root"
  return "$runtime_status"
}
trap cleanup_audit EXIT
mkdir "$audit_root/state" "$audit_root/service-state" "$audit_root/xdg"
release_version=${WEATHER_RELEASE_VERSION:-}
if [[ -n "$release_version" ]]; then
  [[ $("$runtime_root/a-weather-app" --version) == "A Weather App $release_version" ]] || {
    printf 'Binary version differs from release version %s.\n' "$release_version" >&2; exit 1;
  }
  [[ $(sed -nE 's/^[[:space:]]*"version":[[:space:]]*"([^"]+)",?$/\1/p' "$runtime_root/manifest.json") == "$release_version" ]] || {
    printf 'Runtime manifest version differs from release version %s.\n' "$release_version" >&2; exit 1;
  }
fi
"$runtime_root/a-weather-app" --bar --state-dir "$audit_root/state" > "$audit_root/bar.json"
# A new private offline instance exercises Go supervisor + Qt startup/shutdown.
env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software XDG_RUNTIME_DIR="$audit_root/xdg" \
  timeout 20s strace -f -qq -e trace=execve -o "$audit_root/exec.log" \
  "$runtime_root/a-weather-app" --offline --duration 2 --state-dir "$audit_root/state" > "$audit_root/runtime.log" 2>&1
cat "$audit_root/runtime.log"
# Direct service mode retains Qt diagnostics (the normal guardian owns private logs).
env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software A_WEATHER_APP_QML_DIAGNOSTIC=1 XDG_RUNTIME_DIR="$audit_root/xdg" \
  timeout 20s strace -f -qq -e trace=execve -o "$audit_root/service-exec.log" \
  "$runtime_root/a-weather-app" --service --offline --duration 2 --state-dir "$audit_root/service-state" > "$audit_root/service.log" 2>&1
cat "$audit_root/service.log"
if ! grep -q 'Weather service snapshot accepted:' "$audit_root/service.log"; then
  printf 'Qt did not accept the initial service snapshot.\n' >&2; exit 1
fi
if ! grep -Fxq 'Weather frontend roots: 1' "$audit_root/service.log"; then
  printf 'Qt did not load exactly one application root.\n' >&2; exit 1
fi
if grep -Eq 'execve\("[^"]*(python|quickshell)' "$audit_root/exec.log" "$audit_root/service-exec.log"; then
  printf 'Runtime spawned a Python or Quickshell process.\n' >&2; exit 1
fi
if ! grep -Eq 'execve\("[^"]*/native/qt/a-weather-app-qt"' "$audit_root/exec.log"; then
  printf 'Runtime did not launch the compiled Qt frontend.\n' >&2; exit 1
fi
if grep -Eq 'QQmlApplicationEngine failed|Error:|ReferenceError:|TypeError:|is not installed|returned invalid data|Cannot assign|Unable to assign|Binding loop|qrc:/[^ ]+:[0-9]+:' "$audit_root/runtime.log" "$audit_root/service.log"; then
  printf 'Runtime QML validation failed.\n' >&2; exit 1
fi
printf 'PASS: compiled offline Go/Qt runtime starts and stops without Python or Quickshell execution.\n'
