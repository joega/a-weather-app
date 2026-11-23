#!/usr/bin/bash
# Fetch data only. The separate policy command parses it; nothing is executed.
set -euo pipefail
umask 077
evidence=${1:?evidence directory}
maximum=16777216
body=$(mktemp "$evidence/.arch-advisories-body.XXXXXXXX")
headers=$(mktemp "$evidence/.arch-advisories-headers.XXXXXXXX")
trap 'rm -f -- "$body" "$headers"' EXIT
# Cap the stream even when Content-Length is absent or incorrect. Retain the
# previous evidence on a failed, empty or oversized response.
curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --max-redirs 3 \
  --connect-timeout 10 --max-time 60 --max-filesize "$maximum" --silent --show-error \
  --dump-header "$headers" -- https://security.archlinux.org/issues/all.json \
  | head -c "$((maximum + 1))" > "$body"
size=$(stat --printf='%s' -- "$body")
if (( size == 0 || size > maximum )); then
  printf 'Arch advisory response is empty or exceeds the byte limit.\n' >&2
  exit 1
fi
mv -f -T -- "$headers" "$evidence/arch-advisories.headers"
mv -f -T -- "$body" "$evidence/arch-advisories.json"
