#!/usr/bin/bash
# Release automation backs up local validation; full audits remain opt-in.
select_release_profile() {
  release_validation_mode=${WEATHER_VALIDATION_MODE:-release}
  case "$release_validation_mode" in
    release) release_copies=(first) ;;
    full) release_copies=(first second) ;;
    *) printf 'WEATHER_VALIDATION_MODE must be release or full.\n' >&2; return 2 ;;
  esac
}
