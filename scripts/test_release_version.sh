#!/usr/bin/bash
set -euo pipefail
source_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d /tmp/weather-release-version.XXXXXXXX)
trap 'find "$fixture" -depth -delete' EXIT
mkdir "$fixture/packaging"
printf '0.51\n' > "$fixture/packaging/release-series.txt"
git -C "$fixture" init -q
git -C "$fixture" -c user.name=Test -c user.email=test@example.com commit -q --allow-empty -m initial
select_version() { (cd "$fixture" && bash "$source_root/scripts/next_release_version.sh"); }
[[ $(select_version) == $'version=0.51.0\nskip=false' ]]
git -C "$fixture" tag v0.51.0
[[ $(select_version) == $'version=0.51.0\nskip=true' ]]
git -C "$fixture" -c user.name=Test -c user.email=test@example.com commit -q --allow-empty -m fix
[[ $(select_version) == $'version=0.51.1\nskip=false' ]]
git -C "$fixture" tag v0.51.1
git -C "$fixture" -c user.name=Test -c user.email=test@example.com commit -q --allow-empty -m another-fix
[[ $(select_version) == $'version=0.51.2\nskip=false' ]]
printf '0.52\n' > "$fixture/packaging/release-series.txt"
[[ $(select_version) == $'version=0.52.0\nskip=false' ]]
printf '0.50\n' > "$fixture/packaging/release-series.txt"
if select_version >/dev/null 2>&1; then
  printf 'A stale release series was accepted.\n' >&2; exit 1
fi
printf 'PASS: release versions advance by patch until the minor series changes.\n'
