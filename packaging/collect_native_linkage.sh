#!/usr/bin/bash
# Evidence only: never infer complete runtime coverage from ELF dependencies.
set -euo pipefail
python3 - "${1:?runtime root}" "${2:?evidence directory}" <<'PY'
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

root, evidence = map(Path, sys.argv[1:])
evidence.mkdir(parents=True, exist_ok=True)
(evidence / "native-linkage.json").unlink(missing_ok=True)
manifest = json.loads((root / "packaging/runtime.json").read_text())
packages = dict(line.split(" ", 1) for line in manifest["packages"])
artifacts = ("a-weather-app", "native/qt/a-weather-app-qt",
             "native/frame-alignment/a-weather-app-frame-alignment.so",
             "native/atmosphere/a-weather-app-atmosphere")
results, owners = {}, {}

def run(args, log=None):
    result = subprocess.run(args, text=True, capture_output=True, timeout=30)
    if log:
        log.write_text(result.stdout + result.stderr)
    if result.returncode:
        raise ValueError(f"{args[0]} failed with status {result.returncode}")
    return result.stdout

for artifact in artifacts:
    path = root / artifact
    digest = hashlib.sha256(path.read_bytes()).hexdigest()
    if digest != manifest["artifacts"][artifact]["sha256"]:
        raise ValueError(f"artifact differs from verified inventory: {artifact}")
    label = artifact.replace("/", "_")
    run(["readelf", "--dynamic", str(path)], evidence / (label + ".dynamic.txt"))
    linked = run(["ldd", str(path)], evidence / (label + ".ldd.txt"))
    if "not found" in linked:
        raise ValueError(f"unresolved dependency: {artifact}")
    libraries = []
    for line in linked.splitlines():
        match = re.search(r"(?:=>\s+|^\s*)(/\S+)\s+\(", line)
        if not match:
            if "linux-vdso" in line or "statically linked" in line:
                continue
            if line.strip():
                raise ValueError(f"unrecognized dependency record: {line}")
            continue
        library = str(Path(match[1]).resolve(strict=True))
        if library not in owners:
            package = run(["pacman", "-Qoq", "--", library]).strip()
            if package not in packages:
                raise ValueError(f"dependency owner absent from build inventory: {library}")
            owners[library] = {"package": package, "version": packages[package]}
        libraries.append({"path": library, **owners[library]})
    results[artifact] = {"sha256": digest, "libraries": libraries}

linked_packages = sorted({row["package"] for row in owners.values()})
output = {"schema_version": 1, "inventory_sha256": hashlib.sha256(json.dumps(
    {k: manifest[k] for k in ("packages", "build_image", "arch_snapshot", "hyprland_commit")},
    sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
    "artifacts": results, "observed_system_runtime_packages": linked_packages,
    "build_inventory_packages_without_observed_elf_linkage": sorted(set(packages) - set(linked_packages)),
    "limitations": "Pinned build ELF closure only. Does not enumerate dlopen/QML/graphics plugins, compositor dependencies or libraries on user systems. Unobserved packages are not proven build-only or safe."}
(evidence / "native-linkage.json").write_text(json.dumps(output, indent=2) + "\n")
print(f"PASS: native linkage evidence; {len(linked_packages)} observed system-runtime package owners")
PY
