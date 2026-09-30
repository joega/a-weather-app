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
for tool in curl jq sha256sum tar flock stat; do
  command -v "$tool" >/dev/null || { printf 'Missing required command: %s\n' "$tool" >&2; exit 2; }
done
source_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
source "$source_root/scripts/runtime_paths.sh"
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
weather_open_directory "$data_home" 1
weather_open_child "$weather_dir_fd" a-weather-app 1 0 1
app_fd=$weather_child_fd
app_path="/proc/self/fd/$app_fd"
# Check every preexisting path we will use before creating the lock or releases.
if [[ -e $app_path/.install.lock || -L $app_path/.install.lock ]]; then
  weather_validate_lock "$app_fd"
  preflight_lock_fd=$weather_lock_fd
  exec {preflight_lock_fd}<&-
fi
current="$app_path/current"
if [[ -e $current || -L $current ]]; then
  if [[ ! -L $current || ! $(readlink -- "$current") =~ ^releases/v0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]]; then
    printf 'Refusing to replace an unrelated current runtime: %s/current\n' "$app_root" >&2; exit 1
  fi
fi
if [[ -e $app_path/releases || -L $app_path/releases ]]; then
  weather_open_child "$app_fd" releases 0 0 1
  preflight_releases_fd=$weather_child_fd
  weather_check_tree "$preflight_releases_fd"
  exec {preflight_releases_fd}<&-
fi
weather_open_child "$app_fd" releases 1 0 1
releases_fd=$weather_child_fd
weather_install_lock "$app_fd"
# All subsequent operations use held directory descriptors, including cleanup.
releases_path="/proc/self/fd/$releases_fd"
temporary=$(mktemp -d "$app_path/.install.XXXXXXXX")
trap 'find "$temporary" -depth -delete' EXIT
archive="a-weather-app-${release_tag}-linux-x86_64.tar"
base_url="https://github.com/joega/a-weather-app/releases/download/$release_tag"
download_asset() {
  local asset=$1 max_bytes=$2
  (
    # curl 8.4+ enforces this during a streamed response. The process file
    # limit also bounds older curl versions and responses without a length.
    set +o posix
    ulimit -c 0
    ulimit -f "$((max_bytes / 1024))"
    curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --silent --show-error \
      --location --max-redirs 5 --retry 2 --max-time 180 --max-filesize "$max_bytes" \
      --output "$temporary/$asset" "$base_url/$asset"
  )
}
download_asset SHA256SUMS 4096
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
download_asset go-runtime.json 1048576
jq -e --arg source "$pinned_commit" '.source_commit == $source and .source_dirty == false and .source_status == [] and .runtime == "go-qt" and .architecture == "x86_64"' \
  "$temporary/go-runtime.json" >/dev/null
download_asset "$archive" 67108864
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
cmp "$temporary/go-runtime.json" "$temporary/runtime/packaging/runtime.json"
jq -e '(.artifacts + .runtime_files) | keys | all(test("^[A-Za-z0-9._/-]+$") and (split("/") | all(. != "." and . != "..")))' \
  "$temporary/go-runtime.json" >/dev/null
jq -r '(.artifacts + .runtime_files) | to_entries[] | "\(.value.sha256)  \(.key)"' \
  "$temporary/go-runtime.json" > "$temporary/FILES.sha256"
(cd "$temporary/runtime" && sha256sum --status --check "$temporary/FILES.sha256")
[[ -x "$temporary/runtime/a-weather-app" ]] || { printf 'Release executable is not executable.\n' >&2; exit 1; }
target="$releases_path/$release_tag"
if [[ -e $target || -L $target ]]; then
  weather_open_child "$releases_fd" "$release_tag" 0 0 1
  target_fd=$weather_child_fd
  target="/proc/self/fd/$target_fd"
  weather_check_tree "$target_fd"
  if ! cmp "$temporary/go-runtime.json" "$target/packaging/runtime.json" \
      || ! (cd "$target" && sha256sum --status --check "$temporary/FILES.sha256"); then
    printf 'Existing release directory differs: %s\n' "$target" >&2; exit 1
  fi
else
  mv -- "$temporary/runtime" "$target"
fi
current="$app_path/current"
if [[ -e $current || -L $current ]]; then
  if [[ ! -L $current || ! $(readlink -- "$current") =~ ^releases/v0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]]; then
    printf 'Refusing to replace an unrelated current runtime: %s\n' "$current" >&2; exit 1
  fi
fi
link="$app_path/.current.$$.tmp"
ln -s "releases/$release_tag" "$link"
mv -Tf -- "$link" "$current"
printf 'Installed %s at %s/releases/%s\n' "$release_tag" "$app_root" "$release_tag"
printf 'The plugin launcher now uses %s/current\n' "$app_root"
