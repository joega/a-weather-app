#!/usr/bin/bash
# A known-vulnerable dependency stays in a disposable module, never the app.
set -euo pipefail
scanner=${1:?pinned govulncheck path}
evidence=${2:?evidence output}
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
source "$root/packaging/govulncheck_adapter.sh"
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$evidence"
evidence=$(cd "$evidence" && pwd -P)
cat > "$fixture/go.mod" <<'MOD'
module fixture.local/vulnerable

go 1.24

require golang.org/x/text v0.3.5
MOD
cat > "$fixture/main.go" <<'GO'
package main
import (
 "fmt"
 "golang.org/x/text/language"
)
func main() { tag, err := language.Parse("en"); fmt.Println(tag, err) }
GO
cd "$fixture"
env GOTOOLCHAIN=local GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
  GOPRIVATE= GONOSUMDB= GONOPROXY= go mod download golang.org/x/text
go build -o vulnerable .
for mode in source binary; do
  args=(./...)
  [[ $mode == binary ]] && args=(./vulnerable)
  status=0
  scan_go_advisories "$scanner" "$mode" "$evidence" "known-vulnerable-$mode" "$(go env GOVERSION)" "${args[@]}" \
    > "$evidence/known-vulnerable-$mode.json" 2> "$evidence/known-vulnerable-$mode.stderr" || status=$?
  # Ensure this is a real finding, not merely an advisory record or a failure.
  jq -se 'any(.[]; .finding.osv == "GO-2021-0113")' "$evidence/known-vulnerable-$mode.json"
  if bash "$root/packaging/vulnerability_policy.sh" go "$evidence/known-vulnerable-$mode.json" "$status"; then
    printf 'Known vulnerable fixture incorrectly accepted.\n' >&2; exit 1
  fi
done
cp go.mod go.sum "$evidence/"
printf 'PASS: pinned scanner detects GO-2021-0113 in source and binary; release policy blocks both.\n'
