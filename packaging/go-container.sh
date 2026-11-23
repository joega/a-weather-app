#!/usr/bin/bash
set -euo pipefail
umask 077
if [[ ! -f /.dockerenv ]] || ! awk '$5 == "/source" && $6 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'Refusing package install: Docker with a read-only /source mount is required.\n' >&2
  exit 2
fi
if ! awk '$5 == "/output" && $6 ~ /(^|,)rw(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'An isolated writable /output bind mount is required.\n' >&2; exit 2
fi
if [[ -n $(find /output -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  printf 'Output directory must be a new empty directory.\n' >&2; exit 2
fi
if [[ ! ${OUTPUT_UID:-} =~ ^[0-9]+$ || ! ${OUTPUT_GID:-} =~ ^[0-9]+$ ]]; then
  printf 'Output owner IDs are required.\n' >&2; exit 2
fi
source /source/packaging/release-profile.sh
select_release_profile
finish_build() {
  local archive
  if [[ -d /package-cache ]]; then
    mkdir -p /output/package-cache
    for archive in /var/cache/pacman/pkg/*.pkg.tar.zst /var/cache/pacman/pkg/*.pkg.tar.zst.sig; do
      if [[ -f $archive && ! -L $archive ]]; then cp -- "$archive" /output/package-cache/; fi
    done
  fi
  chown -R "$OUTPUT_UID:$OUTPUT_GID" /output
}
trap finish_build EXIT
# Cache only downloaded inputs. Pacman still verifies repository checksums and
# package signatures on every install; compiler output caches stay independent.
if [[ -d /package-cache ]]; then
  for archive in /package-cache/*.pkg.tar.zst /package-cache/*.pkg.tar.zst.sig; do
    if [[ -f $archive && ! -L $archive ]]; then cp -- "$archive" /var/cache/pacman/pkg/; fi
  done
fi
source /source/packaging/release-phases.sh
export PHASE_TIMINGS
# At most two compiler jobs; do not turn host CPU count into unbounded RAM use.
export WEATHER_BUILD_JOBS=${WEATHER_BUILD_JOBS:-2}
[[ $WEATHER_BUILD_JOBS =~ ^[12]$ ]] || { printf 'Build jobs must be 1 or 2.\n' >&2; exit 2; }
# Configuration below belongs exclusively to this disposable container.
printf 'Server = https://archive.archlinux.org/repos/2026/10/22/$repo/os/$arch\n' > /etc/pacman.d/mirrorlist
dependency_packages=(base-devel git jq hyprland qt6-base qt6-declarative qt6-shadertools gtk4 gtk4-layer-shell json-glib libepoxy libnotify strace python curl noto-fonts)
if [[ $release_validation_mode == full ]]; then
  dependency_packages+=(quickshell qt6-tools clang noto-fonts-cjk)
fi
install_dependencies() {
  local attempt
  # Archive downloads can stall briefly. Retry the verified transaction using
  # pacman's retained download cache. Replace its short per-transfer timeout
  # with the explicit overall deadline, so slow archive connections can finish.
  for attempt in 1 2 3; do
    if timeout --kill-after=10s 600 pacman -Syyuu --noconfirm --needed --disable-download-timeout "${dependency_packages[@]}"; then
      return 0
    fi
    if (( attempt < 3 )); then
      printf 'Dependency download/transaction failed; retrying (%s/3).\n' "$attempt" >&2
      sleep "$((attempt * 5))"
    fi
  done
  return 1
}
run_phase dependency-setup install_dependencies
# Match Omarchy 4.5's native ABI, rather than a newer Arch compositor release.
[[ $(pacman -Q hyprland) == 'hyprland 0.56.2-4' ]]
[[ $(pacman -Q qt6-base) == 'qt6-base 6.12.0-3' ]]
[[ $(pacman -Q qt6-declarative) == 'qt6-declarative 6.12.0-2' ]]
# Keep the native snapshot while advancing the security-patched compiler. Both
# the archive checksum and Arch signature are required; installation stays in
# this disposable container, and pacman records the actual compiler version.
compiler_start=$(date +%s%N)
compiler_package=/var/cache/pacman/pkg/go-2:1.27.4-1-x86_64.pkg.tar.zst
if ! printf '80efa5d5375cdfc3ed5748fc1d9ba074190233c8250c08d58e206ca3869c60ed  %s\n' "$compiler_package" | sha256sum --check --status 2>/dev/null; then
  compiler_download=$(mktemp /tmp/weather-go-compiler.XXXXXXXX.pkg.tar.zst)
  curl -q --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
    --max-time 120 --max-filesize 67108864 --silent --show-error \
    https://archive.archlinux.org/repos/2026/11/22/extra/os/x86_64/go-2%3A1.27.4-1-x86_64.pkg.tar.zst \
    -o "$compiler_download"
  mv -- "$compiler_download" "$compiler_package"
fi
printf '80efa5d5375cdfc3ed5748fc1d9ba074190233c8250c08d58e206ca3869c60ed  %s\n' \
  "$compiler_package" | sha256sum --check --strict
curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location \
  --max-time 60 --max-filesize 65536 --silent --show-error \
  https://archive.archlinux.org/repos/2026/11/22/extra/os/x86_64/go-2%3A1.27.4-1-x86_64.pkg.tar.zst.sig \
  -o "$compiler_package.sig"
run_phase compiler-signature pacman-key --verify "$compiler_package.sig" "$compiler_package"
run_phase compiler-security-update pacman -U --noconfirm "$compiler_package"
[[ $(pacman -Q go) == 'go 2:1.27.4-1' ]]
[[ $(go env GOVERSION) == 'go1.27.4-X:nodwarf5' ]]
printf 'compiler-pin\t%s\t0\n' "$((($(date +%s%N)-compiler_start)/1000000))" >> "$PHASE_TIMINGS"
# Full audits analyze and exercise the widget against real, pinned Omarchy
# modules. Routine releases rely on the already-completed local widget tests.
if [[ $release_validation_mode == full ]]; then
  omarchy_start=$(date +%s%N)
  omarchy_imports=$(mktemp -d /tmp/weather-omarchy-imports.XXXXXXXX)
  curl --proto '=https' --proto-redir '=https' --tlsv1.2 --fail --location --silent --show-error \
    --max-time 180 --max-filesize 134217728 \
    https://pkgs.omarchy.org/stable/x86_64/omarchy-4.5.0-3-x86_64.pkg.tar.zst \
    -o "$omarchy_imports/omarchy.pkg.tar.zst"
  printf 'bfe75d086c8b5c919280372afdd72e8cc7b0f66745f052d826b5a588fdad33eb  %s\n' \
    "$omarchy_imports/omarchy.pkg.tar.zst" | sha256sum --check --strict
  tar --extract --file "$omarchy_imports/omarchy.pkg.tar.zst" --directory "$omarchy_imports" \
    usr/share/omarchy/shell
  printf 'omarchy-modules\t%s\t0\n' "$((($(date +%s%N)-omarchy_start)/1000000))" >> "$PHASE_TIMINGS"
  export OMARCHY_SHELL_ROOT="$omarchy_imports/usr/share/omarchy/shell"
  export WEATHER_REQUIRE_BAR_TEST=1
fi
export XDG_RUNTIME_DIR
XDG_RUNTIME_DIR=$(mktemp -d /tmp/weather-qt-runtime.XXXXXXXX)
run_phase release-total bash /source/packaging/build-go-release.sh
