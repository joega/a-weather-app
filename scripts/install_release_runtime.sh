#!/usr/bin/bash
# Explicit, unprivileged setup for the native runtime used by the Omarchy bar plugin.
set -euo pipefail
umask 077
if (( $# > 1 )) || { (( $# == 1 )) && [[ $1 != --help ]]; }; then
  printf 'Usage: bash scripts/install_release_runtime.sh\n' >&2
  exit 2
fi
if [[ ${1:-} == --help ]]; then
  printf 'Usage: bash scripts/install_release_runtime.sh\n'
  exit 0
fi
for tool in curl jq sha256sum tar flock; do
  command -v "$tool" >/dev/null || { printf 'Missing required command: %s\n' "$tool" >&2; exit 2; }
done
source_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
release_lock="$source_root/packaging/release-lock.json"
if [[ ! -f $release_lock || -L $release_lock ]]; then
  printf 'Missing pinned release metadata in %s\n' "$release_lock" >&2; exit 2
fi
release_metadata=$(cat -- "$release_lock")
if ! jq -e '
  type == "object" and keys == ["archive_sha256", "schemaVersion", "source_commit", "tag"]
  and .schemaVersion == 1
  and (.tag | type == "string" and test("^v0\\.[1-9][0-9]*\\.(0|[1-9][0-9]*)$"))
  and (.archive_sha256 | type == "string" and test("^[a-f0-9]{64}$"))
  and (.source_commit | type == "string" and test("^[a-f0-9]{40}$"))
' <<<"$release_metadata" >/dev/null; then
  printf 'Invalid pinned release metadata in %s\n' "$release_lock" >&2; exit 2
fi
release_tag=$(jq -r '.tag' <<<"$release_metadata")
pinned_hash=$(jq -r '.archive_sha256' <<<"$release_metadata")
pinned_commit=$(jq -r '.source_commit' <<<"$release_metadata")
data_home=${XDG_DATA_HOME:-${HOME:-}/.local/share}
if [[ $data_home != /* || $data_home == / ]]; then
  printf 'XDG_DATA_HOME (or HOME) must be an absolute directory.\n' >&2; exit 2
fi
app_root="$data_home/a-weather-app"
mkdir -p -- "$app_root/releases"
exec 9>"$app_root/.install.lock"
flock -x 9
temporary=$(mktemp -d "$app_root/.install.XXXXXXXX")
trap 'find "$temporary" -depth -delete' EXIT
archive="a-weather-app-${release_tag}-linux-x86_64.tar"
base_url="https://github.com/joega/a-weather-app/releases/download/$release_tag"
for asset in "$archive" SHA256SUMS go-runtime.json; do
  curl --proto '=https' --tlsv1.2 --fail --silent --show-error --location --retry 2 --max-time 180 \
    --output "$temporary/$asset" "$base_url/$asset"
done
if [[ $(wc -l < "$temporary/SHA256SUMS") != 1 ]]; then
  printf 'Expected exactly one archive checksum.\n' >&2; exit 1
fi
read -r expected_hash expected_name < "$temporary/SHA256SUMS"
if [[ ! $expected_hash =~ ^[a-f0-9]{64}$ || $expected_name != "$archive" ]]; then
  printf 'Release checksum does not name the expected archive.\n' >&2; exit 1
fi
if [[ $expected_hash != "$pinned_hash" ]]; then
  printf 'Release checksum differs from the reviewed repository pin.\n' >&2; exit 1
fi
(
  cd "$temporary"
  sha256sum --check SHA256SUMS
)
if ! tar -tf "$temporary/$archive" | awk '
  $0 !~ /^\.\/[A-Za-z0-9._\/-]*$/ || $0 ~ /(^|\/)\.\.($|\/)/ { bad=1 }
  END { exit bad }
' || ! tar -tvf "$temporary/$archive" | awk '
  substr($1,1,1) != "-" && substr($1,1,1) != "d" { bad=1 }
  END { exit bad }
'; then
  printf 'Release archive contains an unsafe path or link.\n' >&2; exit 1
fi
mkdir "$temporary/runtime"
tar --extract --file "$temporary/$archive" --directory "$temporary/runtime" --no-same-owner --no-same-permissions
jq -e --arg version "${release_tag#v}" '.version == $version' "$temporary/runtime/manifest.json" >/dev/null
jq -e --arg source "$pinned_commit" '.source_commit == $source and .source_dirty == false and .source_status == [] and .runtime == "go-qt" and .architecture == "x86_64"' \
  "$temporary/go-runtime.json" >/dev/null
cmp "$temporary/go-runtime.json" "$temporary/runtime/packaging/runtime.json"
jq -e '(.artifacts + .runtime_files) | keys | all(test("^[A-Za-z0-9._/-]+$") and (split("/") | all(. != "." and . != "..")))' \
  "$temporary/go-runtime.json" >/dev/null
jq -r '(.artifacts + .runtime_files) | to_entries[] | "\(.value.sha256)  \(.key)"' \
  "$temporary/go-runtime.json" > "$temporary/FILES.sha256"
(cd "$temporary/runtime" && sha256sum --status --check "$temporary/FILES.sha256")
[[ -x "$temporary/runtime/a-weather-app" ]] || { printf 'Release executable is not executable.\n' >&2; exit 1; }
target="$app_root/releases/$release_tag"
if [[ -e $target || -L $target ]]; then
  if [[ ! -d $target || -L $target ]] || ! cmp "$temporary/go-runtime.json" "$target/packaging/runtime.json" \
      || ! (cd "$target" && sha256sum --status --check "$temporary/FILES.sha256"); then
    printf 'Existing release directory differs: %s\n' "$target" >&2; exit 1
  fi
else
  mv -- "$temporary/runtime" "$target"
fi
current="$app_root/current"
if [[ -e $current || -L $current ]]; then
  if [[ ! -L $current || ! $(readlink -- "$current") =~ ^releases/v0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]]; then
    printf 'Refusing to replace an unrelated current runtime: %s\n' "$current" >&2; exit 1
  fi
fi
link="$app_root/.current.$$.tmp"
ln -s "releases/$release_tag" "$link"
mv -Tf -- "$link" "$current"
printf 'Installed %s at %s\n' "$release_tag" "$target"
printf 'The plugin launcher now uses %s\n' "$current"
