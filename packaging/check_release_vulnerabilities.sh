#!/usr/bin/bash
# Assessment is mutable evidence, never an input to the reproducible binaries.
set -euo pipefail
mode=${1:?source, binary or native}
evidence=${2:?evidence directory}
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
mkdir -p "$evidence"
evidence=$(cd "$evidence" && pwd -P)
source "$root/packaging/govulncheck_adapter.sh"
scanner_version=v1.8.0
scanner_root=${WEATHER_SCANNER_ROOT:-/tmp/weather-release-scanner}
case "$mode" in
  source|binary)
    if [[ ! -x $scanner_root/govulncheck ]]; then
      mkdir -p "$scanner_root"
      env GOTOOLCHAIN=local GOBIN="$scanner_root" GOPROXY=https://proxy.golang.org \
        GOSUMDB=sum.golang.org GOPRIVATE= GONOSUMDB= GONOPROXY= \
        go install "golang.org/x/vuln/cmd/govulncheck@$scanner_version" \
        > "$evidence/scanner-install.log" 2>&1
    fi
    sha256sum "$scanner_root/govulncheck" > "$evidence/scanner-sha256.txt"
    go version > "$evidence/go-toolchain.txt"
    go version -m "$scanner_root/govulncheck" > "$evidence/scanner-buildinfo.txt"
    grep -Eq 'mod[[:space:]]+golang.org/x/vuln[[:space:]]+v1\.8\.0[[:space:]]' "$evidence/scanner-buildinfo.txt"
    args=()
    if [[ $mode == source ]]; then
      cd "$root"
      args+=(./...)
    else
      args+=("${3:?Go executable}")
    fi
    status=0
    scan_go_advisories "$scanner_root/govulncheck" "$mode" "$evidence" "go-$mode" "$(go env GOVERSION)" "${args[@]}" > "$evidence/go-$mode.json" \
      2> "$evidence/go-$mode.stderr" || status=$?
    printf '%s\n' "$status" > "$evidence/go-$mode.exit"
    bash "$root/packaging/vulnerability_policy.sh" go "$evidence/go-$mode.json" "$status" "$(bash "$root/packaging/vulnerability_policy.sh" normalize-go "$(go env GOVERSION)")"
    ;;
  native)
    inventory=${3:?runtime manifest}
    install -m 644 "$inventory" "$evidence/assessed-runtime.json"
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
      --max-time 60 --max-filesize 16777216 --silent --show-error \
      -D "$evidence/arch-advisories.headers" https://security.archlinux.org/issues/all.json \
      -o "$evidence/arch-advisories.json"
    bash "$root/packaging/vulnerability_policy.sh" native "$inventory" \
      "$evidence/arch-advisories.json" "$evidence/native-assessment.json" \
      "$root/packaging/native-advisory-notes.json"
    ;;
  *) exit 2;;
esac
