#!/usr/bin/env bash
# Verify the complete candidate with the exact verifier shipped in 0.70.2.
set -euo pipefail
compat_source=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $# == 1 && $1 == /* ]] || { printf 'Usage: bash scripts/test_update_compatibility.sh ABSOLUTE_RUNTIME\n' >&2; exit 2; }
compat_root=$(mktemp -d "${TMPDIR:-/tmp}/weather-update-compat.XXXXXXXX")
trap 'rm -rf -- "$compat_root"' EXIT
git -C "$compat_source" archive v0.70.2 go.mod internal/release internal/safeio | tar -x -C "$compat_root"
mkdir -p "$compat_root/cmd/verify-update"
cat > "$compat_root/cmd/verify-update/main.go" <<'GO'
package main
import (
    "fmt"
    "os"
    "github.com/joega/a-weather-app/internal/release"
)
func main() {
    if err := release.Verify(os.Args[1]); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    fmt.Println("PASS: complete runtime accepted by the shipped 0.70.2 verifier")
}
GO
(cd "$compat_root" && go run ./cmd/verify-update "$1")
