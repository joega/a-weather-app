#!/usr/bin/bash
# Run from the desktop; package installation happens only in disposable Docker.
set -uo pipefail
umask 077
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P) || exit 1
image='archlinux@sha256:917e543c9d0f1f495d70907bdf05bf53607e791b351e1b01ccd3aec2442303ed'
if ! command -v docker >/dev/null; then
  printf 'Docker is not installed; no host packages will be installed by this script.\n' >&2
  exit 2
fi
runner=(docker)
if ! docker info >/dev/null 2>&1; then
  if ! command -v sudo >/dev/null || ! sudo -v; then
    printf 'Docker access requires authorization; audit was not started.\n' >&2
    exit 2
  fi
  runner=(sudo docker)
fi
audit_log=$(mktemp /tmp/weather-docker-audit.XXXXXXXX.log) || exit 1
printf 'Running isolated Docker audit. Packages stay inside the container.\nLog file: %s\n' "$audit_log"
if "${runner[@]}" run --rm \
  --mount "type=bind,src=$source_root,dst=/source,readonly" \
  "$image" bash /source/packaging/audit-container.sh >"$audit_log" 2>&1; then
  audit_status=0
else
  audit_status=$?
fi
printf 'Audit exit status: %s\nLog file: %s\n' "$audit_status" "$audit_log"
exit "$audit_status"
