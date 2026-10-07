#!/usr/bin/bash
# Desktop entry point: no packages or configuration are installed on the host.
set -euo pipefail
umask 077
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P) || exit 1
image='archlinux@sha256:917e543c9d0f1f495d70907bdf05bf53607e791b351e1b01ccd3aec2442303ed'
build_jobs=${WEATHER_BUILD_JOBS:-2}
if [[ ! $build_jobs =~ ^[12]$ ]]; then
  printf 'WEATHER_BUILD_JOBS must be 1 (serial comparison) or 2 (release default).\n' >&2; exit 2
fi
if ! command -v docker >/dev/null; then
  printf 'Docker is unavailable; build was not started.\n' >&2; exit 2
fi
if [[ -n ${WEATHER_RELEASE_VERSION:-} && ! ${WEATHER_RELEASE_VERSION} =~ ^0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]]; then
  printf 'WEATHER_RELEASE_VERSION must be a 0.MINOR.PATCH version.\n' >&2; exit 2
fi
runner=(docker)
if ! docker info >/dev/null 2>&1; then
  if ! command -v sudo >/dev/null || ! sudo -v; then
    printf 'Docker access requires interactive sudo authorization; build was not started.\n' >&2; exit 2
  fi
  runner=(sudo docker)
fi
# The container receives a frozen copy, including independent Git metadata for
# accurate commit/dirty evidence. Later edits to the working checkout cannot
# alter this build's input bytes or its read-only source manifest.
snapshot_root=$(mktemp -d /tmp/weather-go-source.XXXXXXXX)
build_log=$(mktemp /tmp/weather-go-migration.XXXXXXXX.log)
trap 'rm -rf -- "$snapshot_root"' EXIT
if ! (
  git clone --bare --no-hardlinks --quiet "$source_root" "$snapshot_root/.git" || exit $?
  git --git-dir="$snapshot_root/.git" config core.bare false || exit $?
  tar --exclude='./.git' --exclude='./dist' --exclude='./build' \
      --exclude='./artifacts' --exclude='./docs' --exclude='./.agents' --exclude='./.codex' --exclude='./node_modules' \
      --exclude='.build' --exclude='.test-build' --exclude='.frontend-test-build' --exclude='.service-test-build' \
      -C "$source_root" -cf - . | tar -C "$snapshot_root" -xf - || exit $?
  # Preserve tracked documentation/evidence exactly as checked out. Omitting
  # tracked files makes an otherwise clean release appear dirty. Untracked
  # personal audit material in these directories remains excluded.
  git -C "$source_root" ls-files -z -- docs artifacts |
    while IFS= read -r -d '' tracked; do
      if [[ -f "$source_root/$tracked" || -L "$source_root/$tracked" ]]; then
        printf '%s\0' "$tracked"
      fi
    done |
    tar -C "$source_root" --null -T - -cf - |
    tar -C "$snapshot_root" -xf - || exit $?
  source_index=$(git -C "$source_root" rev-parse --path-format=absolute --git-path index) || exit $?
  if [[ -f "$source_index" ]]; then cp -- "$source_index" "$snapshot_root/.git/index" || exit $?; fi
  git -C "$snapshot_root" status --porcelain --untracked-files=all || exit $?
) > "$build_log" 2>&1; then
  mkdir -p "$source_root/dist"
  output_root=$(mktemp -d "$source_root/dist/go-migration.XXXXXXXX")
  mkdir -p "$output_root/evidence"
  gzip -c "$build_log" > "$output_root/evidence/build.log.gz"
  printf 'Source freeze failed. Private full log: %s\n' "$build_log" >&2
  tail -n 30 "$build_log" >&2
  exit 1
fi
mkdir -p "$source_root/dist" || exit 1
output_root=$(mktemp -d "$source_root/dist/go-migration.XXXXXXXX") || exit 1
printf 'Running pinned Docker Go/Qt build from frozen source.\nFrozen source: %s\nArtifacts: %s\nPrivate full log: %s\n' "$snapshot_root" "$output_root" "$build_log"
build_start=$(date +%s%N)
if (ulimit -f 65536; "${runner[@]}" run --rm \
  --mount "type=bind,src=$snapshot_root,dst=/source,readonly" \
  --mount "type=bind,src=$output_root,dst=/output" \
  --env "OUTPUT_UID=$(id -u)" --env "OUTPUT_GID=$(id -g)" \
  --env "WEATHER_RELEASE_VERSION=${WEATHER_RELEASE_VERSION:-}" \
  --env "WEATHER_BUILD_JOBS=$build_jobs" \
  "$image" bash /source/packaging/go-container.sh) >> "$build_log" 2>&1; then
  build_status=0
else
  build_status=$?
fi
mkdir -p "$output_root/evidence"
printf 'build_jobs\t%s\n' "$build_jobs" > "$output_root/evidence/build-settings.tsv"
printf 'runner-total\t%s\t%s\n' "$((($(date +%s%N)-build_start)/1000000))" "$build_status" >> "$output_root/evidence/phase-timings.tsv"
gzip -c "$build_log" > "$output_root/evidence/build.log.gz"
printf 'Build exit status: %s\nArtifacts: %s\nPrivate full log: %s\n' "$build_status" "$output_root" "$build_log"
if [[ "$build_status" != 0 ]]; then tail -n 30 "$build_log" >&2; fi
exit "$build_status"
