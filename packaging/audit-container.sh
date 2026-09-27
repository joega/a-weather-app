#!/usr/bin/bash
# Local validation only: includes uncommitted sources, creates no release attestation.
set -euo pipefail
# Fail closed before touching package-manager configuration. This helper is
# intentionally Docker-only, with the checkout mounted read-only at /source.
if [[ ! -f /.dockerenv ]] || ! awk '$5 == "/source" && $6 ~ /(^|,)ro(,|$)/ { found=1 } END { exit !found }' /proc/self/mountinfo; then
  printf 'Refusing audit: run inside Docker with a read-only /source bind mount.\n' >&2
  exit 2
fi
umask 077
export LC_ALL=C.UTF-8 TZ=UTC PYTHONHASHSEED=0
printf 'Server = https://archive.archlinux.org/repos/2026/09/26/$repo/os/$arch\n' > /etc/pacman.d/mirrorlist
pacman -Syyuu --noconfirm --needed base-devel git python hyprland qt6-shadertools gtk4 gtk4-layer-shell json-glib libepoxy
audit_root=$(mktemp -d /tmp/weather-container-audit.XXXXXXXX)
# The bind mount belongs to the desktop user, but this container runs as root.
# Keep modes/contents while owning the disposable copy as the container user;
# runtime asset reads correctly refuse files owned by a different user.
cp -a --no-preserve=ownership /source/. "$audit_root/"
cd "$audit_root"
python3 -I -B -c 'import os; from pathlib import Path; assert all(p.stat().st_uid == os.geteuid() for p in (Path("packaging"), Path("packaging/a-weather-app.desktop"), Path("packaging/icons"), Path("packaging/icons/a-weather-app.svg"))), "audit copy ownership mismatch"'
python3 -B -m unittest discover -s tests -v
bash packaging/check_native_sanitizers.sh
bash packaging/check_reproducibility.sh
printf '\nPASS: pinned-container local audit; not a release or provenance attestation.\n'
