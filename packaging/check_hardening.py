#!/usr/bin/python3
"""Reject native release artifacts lacking ELF memory-protection flags."""
from pathlib import Path
import re
import subprocess
import sys


def check(path):
    headers = subprocess.check_output(
        ["/usr/bin/readelf", "-W", "-l", str(path)], text=True, timeout=10)
    dynamic = subprocess.check_output(
        ["/usr/bin/readelf", "-W", "-d", str(path)], text=True, timeout=10)
    segments = [line.split() for line in headers.splitlines() if line.split()
                and line.split()[0] in {"GNU_STACK", "GNU_RELRO", "LOAD"}]
    stack = [fields for fields in segments if fields[0] == "GNU_STACK"]
    if len(stack) != 1 or "E" in "".join(stack[0][6:-1]):
        raise ValueError(f"{path}: missing or executable GNU_STACK")
    tags = {}
    for line in dynamic.splitlines():
        match = re.match(r"\s*0x[0-9a-fA-F]+\s+\((FLAGS|FLAGS_1|BIND_NOW)\)\s*(.*)$", line)
        if match:
            tags.setdefault(match[1], set()).update(match[2].split())
    now = ("BIND_NOW" in tags or "BIND_NOW" in tags.get("FLAGS", set())
           or "NOW" in tags.get("FLAGS_1", set()))
    if not any(fields[0] == "GNU_RELRO" for fields in segments) or not now:
        raise ValueError(f"{path}: full RELRO required")
    for fields in segments:
        if fields[0] == "LOAD":
            flags = "".join(fields[6:-1])
            if "W" in flags and "E" in flags:
                raise ValueError(f"{path}: writable executable load segment")
    if Path(path).suffix != ".so" and "PIE" not in tags.get("FLAGS_1", set()):
        raise ValueError(f"{path}: position-independent executable required")


if __name__ == "__main__":
    if len(sys.argv) < 2:
        raise SystemExit("Usage: check_hardening.py <native-artifact> ...")
    for artifact in sys.argv[1:]:
        check(artifact)
    print("PASS: full RELRO, non-executable stack, and protected load segments")
