#!/usr/bin/bash
# Linux/Bash path checks shared by the source launcher and release installer.
# Open directories read-only, compare lstat with the descriptor, then use the
# descriptor as the next path anchor. A trusted sticky system ancestor (e.g.
# /tmp) is permitted; selected data/install directories must belong to this UID.
weather_path_error() {
  printf 'Unsafe runtime path: %s\n' "$1" >&2
  return 1
}
weather_stat() {
  stat "$@" --printf='%f %u %h %d %i' -- "$weather_path"
}
weather_check_directory() {
  local mode uid links dev inode
  read -r mode uid links dev inode <<< "$1"
  mode=$((16#$mode))
  (( (mode & 0170000) == 0040000 && (uid == EUID || uid == weather_system_uid) && (mode & 06000) == 0 )) || return 1
  (( (mode & 0022) == 0 || ($2 == 1 && uid == weather_system_uid && (mode & 01000) != 0) ))
}
weather_open_child() {
  local parent=$1 name=$2 create=$3 sticky=$4 owned=$5 before after child
  local weather_path="/proc/self/fd/$parent/$name"
  if [[ ! -e $weather_path && ! -L $weather_path && $create == 1 ]]; then
    mkdir -m 700 -- "$weather_path" || return 1
  fi
  before=$(weather_stat) || return 1
  weather_check_directory "$before" "$sticky" || { weather_path_error "$name"; return 1; }
  if [[ $owned == 1 && $(stat --printf='%u' -- "$weather_path") != "$EUID" ]]; then
    weather_path_error "$name"; return 1
  fi
  exec {child}<"$weather_path" || return 1
  weather_path="/proc/self/fd/$child"
  after=$(weather_stat -L) || { exec {child}<&-; return 1; }
  if [[ $before != "$after" ]]; then
    exec {child}<&-; weather_path_error "$name changed while opening"; return 1
  fi
  weather_child_fd=$child
}
weather_open_directory() {
  local path=$1 create=$2 owned=${3:-1} part fd next
  local -a parts
  [[ $path == /* && $path != / ]] || { weather_path_error "$path"; return 1; }
  weather_system_uid=$(stat --printf='%u' /) || return 1
  exec {fd}</ || return 1
  if ! weather_check_directory "$(stat -L --printf='%f %u %h %d %i' -- "/proc/self/fd/$fd")" 0; then
    exec {fd}<&-; weather_path_error /; return 1
  fi
  IFS=/ read -r -d '' -a parts < <(printf '%s\0' "$path")
  for part in "${parts[@]}"; do
    [[ -z $part || $part == . ]] && continue
    if [[ $part == .. ]]; then
      exec {fd}<&-; weather_path_error "$path"; return 1
    fi
    if ! weather_open_child "$fd" "$part" "$create" 1 0; then
      exec {fd}<&-; return 1
    fi
    next=$weather_child_fd
    exec {fd}<&-
    fd=$next
  done
  if ! weather_check_directory "$(stat -L --printf='%f %u %h %d %i' -- "/proc/self/fd/$fd")" 0 ||
      [[ $owned == 1 && $(stat -L --printf='%u' -- "/proc/self/fd/$fd") != "$EUID" ]]; then
    exec {fd}<&-; weather_path_error "$path ownership or permissions"; return 1
  fi
  weather_dir_fd=$fd
}
weather_check_file() {
  local mode uid links dev inode
  read -r mode uid links dev inode <<< "$1"
  mode=$((16#$mode))
  (( (mode & 0170000) == 0100000 && links == 1 && (uid == EUID || uid == weather_system_uid) && (mode & 07022) == 0 ))
}
weather_open_file() {
  local parent=$1 name=$2 before after fd
  local weather_path="/proc/self/fd/$parent/$name"
  before=$(weather_stat) || return 1
  weather_check_file "$before" || { weather_path_error "$name"; return 1; }
  exec {fd}<"$weather_path" || return 1
  weather_path="/proc/self/fd/$fd"
  after=$(weather_stat -L) || { exec {fd}<&-; return 1; }
  if [[ $before != "$after" ]]; then
    exec {fd}<&-; weather_path_error "$name changed while opening"; return 1
  fi
  weather_file_fd=$fd
}
weather_validate_lock() {
  local parent=$1 fd mode uid links
  weather_open_file "$parent" .install.lock || return 1
  fd=$weather_file_fd
  read -r mode uid links <<< "$(stat -L --printf='%a %u %h' -- "/proc/self/fd/$fd")"
  if [[ $mode != 600 || $uid != "$EUID" || $links != 1 ]]; then
    exec {fd}<&-; weather_path_error '.install.lock'; return 1
  fi
  weather_lock_fd=$fd
}
weather_install_lock() {
  local parent=$1 lock="/proc/self/fd/$1/.install.lock" fd
  if [[ ! -e $lock && ! -L $lock ]]; then
    # O_EXCL creation under a validated, descriptor-anchored owned directory.
    (umask 077; set -o noclobber; : > "$lock") || return 1
  fi
  weather_validate_lock "$parent" || return 1
  fd=$weather_lock_fd
  # flock does not require a writable descriptor. Never truncate an old lock.
  flock -x "$fd" || { exec {fd}<&-; return 1; }
}
weather_check_tree() {
  local parent=$1 entry fd
  local weather_path="/proc/self/fd/$parent"
  local -a entries
  # Expand only one level inside the held directory, never traverse a link.
  local saved_dotglob saved_nullglob
  saved_dotglob=$(shopt -p dotglob) || :
  saved_nullglob=$(shopt -p nullglob) || :
  shopt -s dotglob nullglob
  entries=("$weather_path"/*)
  eval "$saved_dotglob"
  eval "$saved_nullglob"
  for entry in "${entries[@]}"; do
    if [[ -d $entry && ! -L $entry ]]; then
      weather_open_child "$parent" "${entry##*/}" 0 0 0 || return 1
      fd=$weather_child_fd
      if ! weather_check_tree "$fd"; then exec {fd}<&-; return 1; fi
      exec {fd}<&-
    else
      weather_path=$entry
      weather_check_file "$(weather_stat)" || { weather_path_error "$entry"; return 1; }
    fi
  done
}
