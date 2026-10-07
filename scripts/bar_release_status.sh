#!/usr/bin/bash
# Add release information for a runtime that predates the update protocol.
# This is a cached, bounded read: the bar never downloads a release here.
set -euo pipefail
source_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
source "$source_root/scripts/runtime_paths.sh"
installed=$1
state=$2
[[ $installed =~ ^0\.[1-9][0-9]*\.(0|[1-9][0-9]*)$ ]] || exit 2
pin='{}'
cached='{}'
weather_open_directory "$source_root/packaging" 0
weather_open_file "$weather_dir_fd" release-lock.json
if (( $(stat -L --printf='%s' "/proc/self/fd/$weather_file_fd") <= 4096 )); then
  pin=$(cat "/proc/self/fd/$weather_file_fd")
fi
if [[ -d $state && ! -L $state && -f $state/update-status.json && ! -L $state/update-status.json ]]; then
  # A bad cache does not prevent an old runtime's weather from displaying.
  cached=$( (
    weather_open_directory "$state" 0
    weather_open_file "$weather_dir_fd" update-status.json
    (( $(stat -L --printf='%s' "/proc/self/fd/$weather_file_fd") <= 16384 ))
    cat "/proc/self/fd/$weather_file_fd"
  ) 2>/dev/null) || cached='{}'
fi
jq -e 'type == "object"' <<<"$cached" >/dev/null 2>&1 || cached='{}'
jq -e 'type == "object"' <<<"$pin" >/dev/null 2>&1 || pin='{}'
case $(jq -r '.state // ""' <<<"$cached") in
  downloading|verifying|restarting)
    if ! (
      weather_open_directory "$state" 0
      weather_open_file "$weather_dir_fd" update-install.lock
      [[ $(stat -L --printf='%a %u %h %s' "/proc/self/fd/$weather_file_fd") == "600 $EUID 1 0" ]]
      if flock -n "$weather_file_fd"; then exit 1; else [[ $? == 1 ]]; fi
    ) 2>/dev/null; then
      cached=$(jq '.state="failed" | .message="The previous update stopped. Open the app to recover, or retry the update."' <<<"$cached")
    fi
    ;;
esac
jq --arg installed "$installed" --argjson pin "$pin" --argjson cached "$cached" '
  def version: type == "string" and test("^0\\.[1-9][0-9]*\\.(0|[1-9][0-9]*)$");
  def validpin: type == "object" and keys == ["archive_sha256", "schemaVersion", "source_commit", "tag"]
    and .schemaVersion == 1 and (.tag | type == "string" and test("^v0\\.[1-9][0-9]*\\.(0|[1-9][0-9]*)$"))
    and (.archive_sha256 | type == "string" and test("^[a-f0-9]{64}$"))
    and (.source_commit | type == "string" and test("^[a-f0-9]{40}$"));
  def validstatus: type == "object" and .installed == $installed
    and (.state | IN("idle", "current", "available", "publishing", "failed", "downloading", "verifying", "restarting", "updated", "rolled_back"))
    and (.available | type == "string" and (. == "" or version))
    and (.message | type == "string" and length <= 512)
    and (.checked_at | type == "number" and . >= 0 and floor == .);
  if .update? != null then .
  else
    .update = {state:"current", installed:$installed, available:"", message:"", checked_at:0}
    | if ($cached | validstatus) then .update = ($cached | {state, installed, available, message, checked_at})
      elif ($pin | validpin) and (($pin.tag[1:] | split(".") | map(tonumber)) > ($installed | split(".") | map(tonumber))) then
        .update = {state:"available", installed:$installed, available:$pin.tag[1:], message:"The plugin supports a newer runtime. Update and restart to install it.", checked_at:0}
      else . end
    | if .update.state == "available" then
        .tooltip = (("Update " + .update.available + " available · Installed " + $installed + " · " + .tooltip) | .[0:256])
      else . end
  end
'
