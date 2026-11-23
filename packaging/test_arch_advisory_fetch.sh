#!/usr/bin/bash
# Exercise bounded data acquisition without accessing a public service.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir "$fixture/bin" "$fixture/evidence"
cat > "$fixture/bin/curl" <<'SH'
#!/usr/bin/bash
set -euo pipefail
[[ $1 == -q ]] || exit 90
while (( $# )); do
  if [[ $1 == --dump-header ]]; then printf 'HTTP/2 200\n' > "$2"; fi
  shift
done
case ${FETCH_CASE:-success} in
  success) printf '[{"name":"fixture","packages":[]}]' ;;
  failure) printf 'partial'; exit 22 ;;
  empty) : ;;
  oversized) head -c 16777218 /dev/zero ;;
esac
SH
chmod +x "$fixture/bin/curl"
export PATH="$fixture/bin:$PATH"
bash "$root/packaging/fetch_arch_advisories.sh" "$fixture/evidence"
[[ $(cat "$fixture/evidence/arch-advisories.json") == '[{"name":"fixture","packages":[]}]' ]]
sha256sum "$fixture/evidence/arch-advisories.json" "$fixture/evidence/arch-advisories.headers" > "$fixture/before"
for case_name in failure empty oversized; do
  if FETCH_CASE=$case_name bash "$root/packaging/fetch_arch_advisories.sh" "$fixture/evidence"; then
    printf 'Invalid advisory response accepted: %s\n' "$case_name" >&2; exit 1
  fi
  sha256sum --status -c "$fixture/before"
  [[ $(find "$fixture/evidence" -maxdepth 1 -name '.arch-advisories-*' | wc -l) == 0 ]]
done
[[ $(stat --printf='%a' "$fixture/evidence/arch-advisories.json") == 600 ]]
printf 'PASS: advisory fetch caps streaming bytes and retains evidence on failure.\n'
