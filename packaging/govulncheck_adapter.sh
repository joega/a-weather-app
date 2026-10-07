#!/usr/bin/bash
# Source this library. Advisory identity is normalized; build inputs are not.
govuln_adapter_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
scan_go_advisories() {
  local scanner=$1 mode=$2 evidence=$3 label=$4 raw_go=$5
  shift 5
  local canonical input
  canonical=$(bash "$govuln_adapter_root/packaging/vulnerability_policy.sh" normalize-go "$raw_go") || return $?
  printf '%s\n' "$raw_go" > "$evidence/$label-compiler-version.txt" || return $?
  printf '%s\n' "$canonical" > "$evidence/$label-advisory-version.txt" || return $?
  case "$mode" in
    source)
      # Official source-mode GOVERSION override affects advisory matching, not
      # package loading, the compiler executable, flags or reproducible output.
      env GOVERSION="$canonical" "$scanner" -json -db https://vuln.go.dev -mode=source "$@"
      ;;
    binary)
      [[ $# == 1 ]] || return 2
      input=$1
      sha256sum "$input" > "$evidence/$label-executable.sha256" || return $?
      go version -m "$input" > "$evidence/$label-executable.buildinfo" || return $?
      "$scanner" -mode=extract "$input" > "$evidence/$label-extract.raw.json" || return $?
      bash "$govuln_adapter_root/packaging/vulnerability_policy.sh" normalize-binary \
        "$evidence/$label-extract.raw.json" "$evidence/$label-extract.scan.json" \
        "$raw_go" "$evidence/$label-version-binding.json" || return $?
      "$scanner" -json -db https://vuln.go.dev -mode=binary "$evidence/$label-extract.scan.json"
      ;;
    *) return 2;;
  esac
}
