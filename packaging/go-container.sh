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
# Configuration below belongs exclusively to this disposable container.
printf 'Server = https://archive.archlinux.org/repos/2026/09/26/$repo/os/$arch\n' > /etc/pacman.d/mirrorlist
pacman -Syyuu --noconfirm --needed base-devel git go jq hyprland qt6-base qt6-declarative qt6-tools qt6-shadertools quickshell gtk4 gtk4-layer-shell json-glib libepoxy libnotify strace
export XDG_RUNTIME_DIR
XDG_RUNTIME_DIR=$(mktemp -d /tmp/weather-qt-runtime.XXXXXXXX)
export WEATHER_REQUIRE_BAR_TEST=1
bash /source/packaging/build-go-release.sh
