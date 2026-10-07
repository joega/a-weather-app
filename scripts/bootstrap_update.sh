#!/usr/bin/bash
# Stage the first updater-capable release without closing the older app. The
# detached, verified helper then owns the complete update transaction.
set -euo pipefail
umask 077
source_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
source "$source_root/scripts/runtime_paths.sh"
args=(--update-worker --bootstrap-update)
while (( $# )); do
  case $1 in
    --install-update) shift ;;
    --state-dir)
      (( $# >= 2 )) || { printf 'Missing state directory.\n' >&2; exit 2; }
      args+=(--state-dir "$2"); shift 2 ;;
    *) printf 'Unsupported bootstrap argument.\n' >&2; exit 2 ;;
  esac
done
if [[ -e "$source_root/build/a-weather-app" ]]; then
  printf 'Development checkouts are built locally.\n' >&2; exit 1
fi
plugin_root=${XDG_CONFIG_HOME:-${HOME:-}/.config}/omarchy/plugins/a-weather-app.weather
# The worker also checks origin, branch and local changes. Refuse linked or
# development plugins before staging anything, even when no build exists yet.
if [[ -L $plugin_root || $source_root != "$plugin_root" ]]; then
  printf 'Linked or development plugins must be updated locally.\n' >&2; exit 1
fi
command -v setsid >/dev/null || { printf 'Missing required command: setsid\n' >&2; exit 2; }
bash "$source_root/scripts/install_release_runtime.sh" --prepare-only
release_tag=$(jq -er '.tag | select(test("^v0\\.[1-9][0-9]*\\.(0|[1-9][0-9]*)$"))' "$source_root/packaging/release-lock.json")
data_home=${XDG_DATA_HOME:-${HOME:-}/.local/share}
weather_open_directory "$data_home" 0
weather_open_child "$weather_dir_fd" a-weather-app 0 0 1
weather_open_child "$weather_child_fd" releases 0 0 1
weather_open_child "$weather_child_fd" "$release_tag" 0 0 1
release_fd=$weather_child_fd
weather_check_tree "$release_fd"
weather_open_file "$release_fd" a-weather-app
executable="/proc/self/fd/$weather_file_fd"
help_output=$("$executable" --help 2>&1)
if [[ $help_output != *-bootstrap-update* ]]; then
  printf 'This release predates in-app updating. Update the plugin after the first updater-enabled release is published.\n' >&2
  exit 1
fi
# No app-owned process or bar reload can terminate this session. Descriptor
# inheritance keeps the verified executable selected above anchored until exec.
setsid --fork "$executable" "${args[@]}" </dev/null >/dev/null 2>&1
printf 'Update helper started.\n'
