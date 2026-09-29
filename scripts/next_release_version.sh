#!/usr/bin/bash
# Patch releases advance automatically; a deliberate release-series edit starts a new minor.
set -euo pipefail
commit=${1:-HEAD}
commit=$(git rev-parse --verify "${commit}^{commit}")
series=$(<packaging/release-series.txt)
if [[ ! $series =~ ^0\.([1-9][0-9]*)$ ]]; then
  printf 'packaging/release-series.txt must contain 0.MINOR.\n' >&2; exit 2
fi
target_minor=$((10#${BASH_REMATCH[1]}))
highest_minor=0
highest_patch=0
existing=
while IFS= read -r tag; do
  if [[ $tag =~ ^v0\.([1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    minor=$((10#${BASH_REMATCH[1]}))
    patch=$((10#${BASH_REMATCH[2]}))
    if (( minor > highest_minor || (minor == highest_minor && patch > highest_patch) )); then
      highest_minor=$minor; highest_patch=$patch
    fi
    if [[ $(git rev-list -n 1 "$tag") == "$commit" ]]; then
      existing=$tag
    fi
  fi
done < <(git tag --list)
if [[ -n $existing ]]; then
  printf 'version=%s\nskip=true\n' "${existing#v}"
else
  if (( highest_minor > target_minor )); then
    printf 'Release series 0.%s is behind the highest tag 0.%s.%s.\n' "$target_minor" "$highest_minor" "$highest_patch" >&2
    exit 2
  fi
  if (( highest_minor == target_minor )); then
    printf 'version=0.%s.%s\nskip=false\n' "$target_minor" "$((highest_patch + 1))"
  else
    printf 'version=0.%s.0\nskip=false\n' "$target_minor"
  fi
fi
