#!/usr/bin/bash
set -euo pipefail
umask 077
if [[ ! -f /.dockerenv ]] || ! awk '$5 == "/source" && $6 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'Refusing package install: Docker with a read-only /source mount is required.\n' >&2
  exit 2
fi
if ! awk '$5 == "/output" && $6 ~ /(^|,)rw(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'An isolated writable /output bind mount is required.\n' >&2; exit 2
fi
if [[ -n $(find /output -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  printf 'Output directory must be a new empty directory.\n' >&2; exit 2
fi
if [[ ! ${OUTPUT_UID:-} =~ ^[0-9]+$ || ! ${OUTPUT_GID:-} =~ ^[0-9]+$ ]]; then
  printf 'Output owner IDs are required.\n' >&2; exit 2
fi
trap 'chown -R "$OUTPUT_UID:$OUTPUT_GID" /output' EXIT
source /source/packaging/release-phases.sh
export PHASE_TIMINGS
# At most two compiler jobs; do not turn host CPU count into unbounded RAM use.
export WEATHER_BUILD_JOBS=${WEATHER_BUILD_JOBS:-2}
[[ $WEATHER_BUILD_JOBS =~ ^[12]$ ]] || { printf 'Build jobs must be 1 or 2.\n' >&2; exit 2; }
# Configuration below belongs exclusively to this disposable container.
printf 'Server = https://archive.archlinux.org/repos/2026/09/26/$repo/os/$arch\n' > /etc/pacman.d/mirrorlist
run_phase dependency-setup pacman -Syyuu --noconfirm --needed base-devel git go jq hyprland qt6-base qt6-declarative qt6-tools qt6-shadertools quickshell gtk4 gtk4-layer-shell json-glib libepoxy libnotify strace clang python curl
# Analyze the widget against real, pinned Omarchy modules without installing
# the desktop package or inheriting mutable host configuration.
omarchy_start=$(date +%s%N)
omarchy_imports=$(mktemp -d /tmp/weather-omarchy-imports.XXXXXXXX)
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent --show-error \
  https://pkgs.omarchy.org/stable/x86_64/omarchy-4.0.4-1-x86_64.pkg.tar.zst \
  -o "$omarchy_imports/omarchy.pkg.tar.zst"
printf '78fa778d48180aaf776b44f89ae86780f78e7b47440aa20faeb4a1be9240339c  %s\n' \
  "$omarchy_imports/omarchy.pkg.tar.zst" | sha256sum --check --strict
tar --extract --file "$omarchy_imports/omarchy.pkg.tar.zst" --directory "$omarchy_imports" \
  usr/share/omarchy/shell
printf 'omarchy-modules\t%s\t0\n' "$((($(date +%s%N)-omarchy_start)/1000000))" >> "$PHASE_TIMINGS"
export OMARCHY_SHELL_ROOT="$omarchy_imports/usr/share/omarchy/shell"
export XDG_RUNTIME_DIR
XDG_RUNTIME_DIR=$(mktemp -d /tmp/weather-qt-runtime.XXXXXXXX)
export WEATHER_REQUIRE_BAR_TEST=1
run_phase release-total bash /source/packaging/build-go-release.sh
