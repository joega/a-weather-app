#!/usr/bin/bash
# Select the next 0.MINOR.0 tag from the fetched repository tags.
set -euo pipefail
commit=${1:-HEAD}
commit=$(git rev-parse --verify "${commit}^{commit}")
highest=49
existing=
while IFS= read -r tag; do
  if [[ $tag =~ ^v0\.([1-9][0-9]*)\.0$ ]]; then
    minor=$((10#${BASH_REMATCH[1]}))
    if (( minor > highest )); then highest=$minor; fi
    if [[ $(git rev-list -n 1 "$tag") == "$commit" ]]; then existing=$tag; fi
  fi
done < <(git tag --list)
if [[ -n $existing ]]; then
  printf 'version=%s\nskip=true\n' "${existing#v}"
else
  printf 'version=0.%s.0\nskip=false\n' "$((highest + 1))"
fi
