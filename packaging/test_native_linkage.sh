#!/usr/bin/bash
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
mkdir -p "$fixture/bin" "$fixture/runtime/packaging"
export LINKAGE_FIXTURE="$fixture"
python3 - "$fixture" <<'PY'
import hashlib, json, sys
from pathlib import Path
p = Path(sys.argv[1])
(p / "library.so").write_text("fixture library")
artifacts = {}
for name in ("a-weather-app", "native/qt/a-weather-app-qt", "native/frame-alignment/a-weather-app-frame-alignment.so", "native/atmosphere/a-weather-app-atmosphere"):
    path = p / "runtime" / name
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("fixture ELF " + name)
    artifacts[name] = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
(p / "runtime/packaging/runtime.json").write_text(json.dumps({"artifacts": artifacts,
    "packages": ["linked 1:1.0-2", "unobserved 2.0-1"], "build_image": "fixture",
    "arch_snapshot": "fixture", "hyprland_commit": "fixture"}))
PY
cat > "$fixture/bin/readelf" <<'SH'
#!/usr/bin/bash
printf 'fixture dynamic section\n'
SH
cat > "$fixture/bin/ldd" <<'SH'
#!/usr/bin/bash
case ${LINKAGE_FAILURE:-} in
    missing) printf 'libfixture.so => not found\n';;
    malformed) printf 'unknown dependency format\n';;
    command) exit 17;;
    *) printf 'libfixture.so => %s/library.so (0x1)\n' "$LINKAGE_FIXTURE";;
esac
SH
cat > "$fixture/bin/pacman" <<'SH'
#!/usr/bin/bash
if [[ ${LINKAGE_FAILURE:-} == owner ]]; then printf 'absent\n'; else printf 'linked\n'; fi
SH
chmod +x "$fixture/bin/"*
export PATH="$fixture/bin:$PATH"
bash "$root/packaging/collect_native_linkage.sh" "$fixture/runtime" "$fixture/clean"
python3 - "$fixture/clean/native-linkage.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))
assert p["observed_system_runtime_packages"] == ["linked"]
assert p["build_inventory_packages_without_observed_elf_linkage"] == ["unobserved"]
assert len(p["artifacts"]) == 4
assert all(a["libraries"][0]["version"] == "1:1.0-2" for a in p["artifacts"].values())
assert "not proven build-only or safe" in p["limitations"]
PY
for failure in missing malformed command owner; do
    if LINKAGE_FAILURE=$failure bash "$root/packaging/collect_native_linkage.sh" "$fixture/runtime" "$fixture/$failure" > "$fixture/$failure.log" 2>&1; then
        printf 'Linkage failure incorrectly accepted: %s\n' "$failure" >&2; exit 1
    fi
    [[ ! -f $fixture/$failure/native-linkage.json ]]
done
printf 'tampered bytes\n' >> "$fixture/runtime/a-weather-app"
if bash "$root/packaging/collect_native_linkage.sh" "$fixture/runtime" "$fixture/tampered" > "$fixture/tampered.log" 2>&1; then
    printf 'Artifact hash mismatch incorrectly accepted.\n' >&2; exit 1
fi
[[ ! -f $fixture/tampered/native-linkage.json ]]
if bash "$root/packaging/collect_native_linkage.sh" "$fixture/runtime" "$fixture/clean" > "$fixture/repeated.log" 2>&1; then exit 1; fi
[[ ! -f $fixture/clean/native-linkage.json ]]
printf 'PASS: linkage hashes, exact package owners and coverage limitations; missing/malformed/unowned dependencies and command failures block.\n'
