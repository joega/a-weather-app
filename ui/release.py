"""Verify packaged artifacts before use; source builds may omit the manifest."""
import hashlib
import json
from pathlib import Path
import platform
import re

from weather.files import read_file

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = "packaging/runtime.json"
ARTIFACTS = (
    "ui/shaders/atmosphere.frag.qsb",
    "native/frame-alignment/a-weather-app-frame-alignment.so",
    "native/atmosphere/a-weather-app-atmosphere",
)
MAX_ARTIFACT = 16 * 1024 * 1024


def manifest(root=ROOT):
    try:
        raw = read_file(Path(root) / MANIFEST, 128 * 1024)
    except FileNotFoundError:
        return None
    def pairs(items):
        result = {}
        for key, child in items:
            if key in result:
                raise ValueError("duplicate packaged runtime field")
            result[key] = child
        return result
    value = json.loads(raw, object_pairs_hook=pairs)
    if (not isinstance(value, dict) or type(value.get("schema_version")) is not int
            or value["schema_version"] != 1
            or value.get("architecture") != "x86_64"
            or not isinstance(value.get("source_commit"), str)
            or not re.fullmatch(r"[0-9a-f]{40}", value.get("source_commit", ""))
            or not isinstance(value.get("artifacts"), dict)
            or set(value["artifacts"]) != set(ARTIFACTS)):
        raise ValueError("invalid packaged runtime manifest")
    return value


def verify_artifacts(names, *, root=ROOT, metadata=None):
    metadata = manifest(root) if metadata is None else metadata
    if metadata is None:
        return None
    if platform.machine() != metadata["architecture"]:
        raise RuntimeError("this release requires x86_64 Omarchy")
    for name in names:
        if name not in ARTIFACTS:
            raise ValueError("unknown runtime artifact")
        expected = metadata["artifacts"][name]
        if (not isinstance(expected, dict) or type(expected.get("bytes")) is not int
                or not 0 < expected["bytes"] <= MAX_ARTIFACT
                or not isinstance(expected.get("sha256"), str)
                or not re.fullmatch(r"[0-9a-f]{64}", expected["sha256"])):
            raise ValueError("invalid runtime artifact record")
        data = read_file(Path(root) / name, expected["bytes"])
        if len(data) != expected["bytes"] or hashlib.sha256(data).hexdigest() != expected["sha256"]:
            raise RuntimeError("packaged runtime changed; reinstall or update A Weather App")
    return metadata


def verify_effects(version, *, root=ROOT):
    metadata = verify_artifacts(ARTIFACTS[1:], root=root)
    if metadata is None:
        return
    version = version() if callable(version) else version
    expected = metadata.get("hyprland_commit")
    if (not isinstance(expected, str) or not re.fullmatch(r"[0-9a-f]{40}", expected)
            or not isinstance(version, dict) or version.get("commit") != expected):
        raise RuntimeError("desktop effects need a release matching this Hyprland build; forecasts remain available")
