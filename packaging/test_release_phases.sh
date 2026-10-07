#!/usr/bin/bash
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
evidence=$(mktemp -d)
trap 'rm -rf -- "$evidence"' EXIT
export PHASE_TIMINGS="$evidence/timings.tsv"
source "$root/packaging/release-phases.sh"
run_phase clean true
if run_phase failure bash -c 'exit 17'; then exit 1; fi
awk -F '\t' '$1=="failure" && $3==17 {found=1} END {exit !found}' "$PHASE_TIMINGS"
worker() { if [[ $1 == fail ]]; then return 7; fi; printf '%s\n' "$1" >> "$evidence/reaped"; }
if run_parallel 2 worker fail completed; then
  printf 'Failed child was incorrectly accepted.\n' >&2; exit 1
fi
[[ $(cat "$evidence/reaped") == completed ]]
run_parallel 2 worker first second third
if run_parallel 0 worker first; then exit 1; fi
printf 'PASS: timings retain failures; parallel children are reaped and failures block the gate.\n'
