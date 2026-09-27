#!/usr/bin/python3
"""Create or verify source-bound runtime metadata for an explicit release build."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
from ui.release import ARTIFACTS, MANIFEST, manifest, verify_artifacts

IMAGE = "archlinux@sha256:917e543c9d0f1f495d70907bdf05bf53607e791b351e1b01ccd3aec2442303ed"
SNAPSHOT = "2026/09/26"


def command(*args):
    output = subprocess.check_output(args, cwd=ROOT, text=True, timeout=30)
    # NUL-delimited Git paths are data: leading whitespace can be part of a
    # filename, and must not be silently stripped from the first entry.
    return output if "-z" in args else output.strip()


def source_files():
    paths = command("/usr/bin/git", "ls-files", "--cached", "--others", "--exclude-standard", "-z").split("\0")
    return sorted(p for p in set(paths) if p and p not in ARTIFACTS and (
        Path(p).suffix in {".py", ".qml", ".js", ".c", ".cpp", ".h", ".hpp", ".gdshader", ".frag", ".sh", ".yml", ".svg", ".desktop"}
        or Path(p).name in {"Makefile", "qmldir", "a-weather-app", "manifest.json"}))


def digest(path):
    data = path.read_bytes()
    return dict(bytes=len(data), sha256=hashlib.sha256(data).hexdigest())


def create():
    files = source_files()
    untracked = set(command("/usr/bin/git", "ls-files", "--others", "--exclude-standard", "-z").split("\0"))
    if untracked.intersection(files):
        raise RuntimeError("commit untracked release sources before building")
    if command("/usr/bin/git", "diff", "HEAD", "--", *files):
        raise RuntimeError("commit release sources before building")
    header = Path('/usr/include/hyprland/src/version.h').read_text()
    match = re.search(r'#define GIT_COMMIT_HASH\s+"([0-9a-f]{40})"', header)
    if not match:
        raise RuntimeError("cannot identify the compiled Hyprland ABI")
    metadata = dict(schema_version=1, architecture="x86_64",
                    source_commit=command("/usr/bin/git", "rev-parse", "HEAD"),
                    source_date_epoch=int(command("/usr/bin/git", "show", "-s", "--format=%ct", "HEAD")),
                    build_image=IMAGE, arch_snapshot=SNAPSHOT,
                    hyprland_commit=match.group(1),
                    packages=command("/usr/bin/pacman", "-Q").splitlines(),
                    artifacts={p: digest(ROOT / p) for p in ARTIFACTS},
                    sources={p: digest(ROOT / p)["sha256"] for p in files})
    (ROOT / MANIFEST).write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n")
    os.chmod(ROOT / MANIFEST, 0o644)


def verify():
    metadata = manifest(ROOT)
    if metadata is None:
        raise RuntimeError("packaged runtime manifest missing")
    verify_artifacts(ARTIFACTS, root=ROOT, metadata=metadata)
    files = source_files()
    if set(metadata.get("sources", {})) != set(files):
        raise RuntimeError("release source inventory changed")
    for path in files:
        if digest(ROOT / path)["sha256"] != metadata["sources"][path]:
            raise RuntimeError("release sources changed: " + path)
    print("PASS: packaged artifacts match their source inventory and checksums")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("operation", choices=("create", "verify"))
    args = parser.parse_args()
    create() if args.operation == "create" else verify()
