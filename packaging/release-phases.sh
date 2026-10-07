#!/usr/bin/bash
# Source from strict release scripts. Logs contain no environment/credentials.
: "${PHASE_TIMINGS:=/output/evidence/phase-timings.tsv}"
run_phase() {
  local phase=$1 start end status=0
  shift
  mkdir -p "$(dirname -- "$PHASE_TIMINGS")"
  start=$(date +%s%N)
  printf 'PHASE START %s\n' "$phase"
  "$@" || status=$?
  end=$(date +%s%N)
  printf '%s\t%s\t%s\n' "$phase" "$(((end-start)/1000000))" "$status" >> "$PHASE_TIMINGS"
  printf 'PHASE END %s elapsed_ms=%s status=%s\n' "$phase" "$(((end-start)/1000000))" "$status"
  return "$status"
}

# Explicit, bounded batches. Always reap all children; any failure blocks the
# caller. Jobs are commands supplied by the caller, never strings evaluated here.
run_parallel() {
  local limit=$1 worker=$2 status=0 pid
  shift 2
  [[ $limit =~ ^[1-4]$ ]] || return 2
  local -a pending=()
  for job in "$@"; do
    "$worker" "$job" &
    pending+=("$!")
    if (( ${#pending[@]} == limit )); then
      for pid in "${pending[@]}"; do wait "$pid" || status=1; done
      pending=()
      (( status == 0 )) || return "$status"
    fi
  done
  for pid in "${pending[@]}"; do wait "$pid" || status=1; done
  return "$status"
}
