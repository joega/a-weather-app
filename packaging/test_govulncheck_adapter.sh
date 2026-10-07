#!/usr/bin/bash
# Check adapter boundaries without a network or a real executable build.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/evidence" "$fixture/bin"
export ADAPTER_CALLS="$fixture/calls"
cat > "$fixture/bin/go" <<'SH'
#!/usr/bin/bash
[[ $1 == version && $2 == -m ]] || exit 2
printf 'fixture: go1.27.1-X:nodwarf5\n'
SH
cat > "$fixture/bin/scanner" <<'SH'
#!/usr/bin/bash
printf '%s GOVERSION=%s\n' "$*" "${GOVERSION:-}" >> "$ADAPTER_CALLS"
if [[ $1 == -mode=extract ]]; then
  [[ ${EXTRACT_FAIL:-0} == 0 ]] || exit 17
  printf '%s\n' '{"name":"govulncheck-extract","version":"0.1.0"}' \
    '{"goVersion":"go1.27.1-X:nodwarf5","modules":[],"pkgSymbols":[],"goos":"linux","goarch":"amd64"}'
fi
exit "${SCAN_STATUS:-0}"
SH
chmod +x "$fixture/bin/go" "$fixture/bin/scanner"
export PATH="$fixture/bin:$PATH"
source "$root/packaging/govulncheck_adapter.sh"
scanner="$fixture/bin/scanner"
evidence="$fixture/evidence"
printf 'unchanged executable fixture\n' > "$fixture/executable"
sha256sum "$fixture/executable" > "$fixture/before.sha256"
scan_go_advisories "$scanner" source "$evidence" source go1.27.1-X:nodwarf5 ./...
grep -Fxq -- '-json -db https://vuln.go.dev -mode=source ./... GOVERSION=go1.27.1' "$ADAPTER_CALLS"
scan_go_advisories "$scanner" binary "$evidence" binary go1.27.1-X:nodwarf5 "$fixture/executable"
sha256sum -c "$fixture/before.sha256"
grep -Fq -- "-mode=binary $evidence/binary-extract.scan.json" "$ADAPTER_CALLS"
[[ $(cat "$evidence/binary-compiler-version.txt") == go1.27.1-X:nodwarf5 ]]
[[ $(cat "$evidence/binary-advisory-version.txt") == go1.27.1 ]]
# A failed extraction must not run the final scanner, even inside an OR list
# where Bash disables errexit for commands in the function.
: > "$ADAPTER_CALLS"
export EXTRACT_FAIL=1
status=0
scan_go_advisories "$scanner" binary "$evidence" failed go1.27.1-X:nodwarf5 "$fixture/executable" || status=$?
[[ $status == 17 && $(wc -l < "$ADAPTER_CALLS") == 1 ]]
unset EXTRACT_FAIL
export SCAN_STATUS=23
status=0
scan_go_advisories "$scanner" source "$evidence" failed-source go1.27.1 ./... || status=$?
[[ $status == 23 ]]
: > "$ADAPTER_CALLS"
if scan_go_advisories "$scanner" source "$evidence" unsupported devel ./...; then exit 1; fi
[[ ! -s $ADAPTER_CALLS ]]
printf 'PASS: adapter preserves executable identity, normalizes advisory input and propagates failures.\n'
