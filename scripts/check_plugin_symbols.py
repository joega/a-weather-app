#!/usr/bin/env python3
"""Reject plugin-private copies of the compositor globals used by the diagnostic.

This reads ELF metadata only. It never loads a library or invokes Hyprland.
"""
import argparse
import os
from pathlib import Path
import subprocess
import sys

# Explicit repository-local bootstrap also works under the system interpreter -I.
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ui.effects import bounded_run, child_environment


REQUIRED = (
    "g_pHyprRenderer",
    "_ZN10NProtocols11sessionLockE",
    "_ZGV15g_pHyprRenderer",
    "_ZGVN10NProtocols11sessionLockE",
    "g_pEventLoopManager",
    "_ZGV19g_pEventLoopManager",
)


def binds_locally(path: Path) -> bool:
    output = bounded_run(["/usr/bin/readelf", "--dynamic", "--wide", str(path)],
                         env=dict(child_environment(), LC_ALL="C"), timeout=10)
    return any("(SYMBOLIC)" in line or ("(FLAGS)" in line and "SYMBOLIC" in line)
               for line in output.splitlines())


def symbols(path: Path) -> dict[str, tuple[int, str, str, str, str]]:
    output = bounded_run(["/usr/bin/readelf", "--dyn-syms", "--wide", str(path)],
                         env=dict(child_environment(), LC_ALL="C"), timeout=10,
                         stdout_limit=32 * 1024 * 1024)
    found = {}
    for line in output.splitlines():
        fields = line.split()
        if len(fields) >= 8 and fields[0].endswith(":") and fields[0][:-1].isdigit():
            name = fields[7].split("@", 1)[0]
            if name in REQUIRED:
                found[name] = (int(fields[2]), fields[3], fields[4], fields[5], fields[6])
    return found


def check(plugin: Path, host: Path) -> list[str]:
    plugin_symbols, host_symbols = symbols(plugin), symbols(host)
    errors = []
    if binds_locally(plugin):
        errors.append("plugin has SYMBOLIC binding: compositor globals may resolve to plugin-private storage")
    for name in REQUIRED:
        candidate = plugin_symbols.get(name)
        exported = host_symbols.get(name)
        if candidate is None:
            errors.append(f"plugin {name}: missing from dynamic symbol table (possibly LOCAL/HIDDEN)")
        elif candidate[1] != "OBJECT" or candidate[2] not in {"GLOBAL", "WEAK"} or candidate[3] != "DEFAULT":
            errors.append(f"plugin {name}: must be DEFAULT GLOBAL/WEAK OBJECT, got {candidate}")
        if exported is None or exported[1] != "OBJECT" or exported[2] not in {"GLOBAL", "WEAK", "UNIQUE"} or exported[3] != "DEFAULT" or exported[4] == "UND":
            errors.append(f"host {name}: missing compatible defined dynamic export")
        elif candidate is not None and candidate[4] != "UND" and candidate[0] != exported[0]:
            errors.append(f"plugin {name}: object size {candidate[0]} differs from host {exported[0]}")
    return errors


def main() -> int:
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("plugin", nargs="?", type=Path,
                        default=root / "native/frame-alignment/a-weather-app-frame-alignment.so")
    parser.add_argument("--host", type=Path, default=Path("/usr/bin/Hyprland"))
    args = parser.parse_args()
    try:
        errors = check(args.plugin, args.host)
    except (OSError, ValueError, RuntimeError, TimeoutError, subprocess.SubprocessError) as error:
        print(f"FAIL: cannot inspect ELF symbols: {error}", file=sys.stderr)
        return 1
    if errors:
        for error in errors:
            print(f"FAIL: {error}", file=sys.stderr)
        return 1
    print("PASS: compositor globals and initialization guards are dynamically preemptible; host exports match")
    return 0


if __name__ == "__main__":
    sys.exit(main())
