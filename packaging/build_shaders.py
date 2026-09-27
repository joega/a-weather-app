#!/usr/bin/python3 -I
"""Explicit, offline preparation of the forecast window's Qt shader pack.

Only ui/shaders/atmosphere.frag.qsb is published into the checkout. No package
installation, network, desktop configuration, or native compositor build runs.
Run again after a source or Qt upgrade, before opening the app.
"""
import os
from pathlib import Path
import secrets
import stat
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.dont_write_bytecode = True
sys.path.insert(0, str(ROOT))
from ui.effects import bounded_run, open_directory, read_regular
from ui.shaders.generate import translate

QSB = "/usr/lib/qt6/bin/qsb"
PACK = "atmosphere.frag.qsb"
MAX_PACK = 2 * 1024 * 1024


def write_all(descriptor, data):
    view = memoryview(data)
    while view:
        count = os.write(descriptor, view)
        if count <= 0:
            raise OSError("shader write failed")
        view = view[count:]


def publish(directory, data):
    """Replace only the fixed build output; never follow its old destination."""
    if not data or len(data) > MAX_PACK:
        raise ValueError("shader pack exceeds its size limit")
    name = ".atmosphere-" + secrets.token_hex(16) + ".tmp"
    descriptor = os.open(name, os.O_WRONLY | os.O_CREAT | os.O_EXCL |
                         os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=directory)
    try:
        write_all(descriptor, data)
        os.fsync(descriptor)
        os.replace(name, PACK, src_dir_fd=directory, dst_dir_fd=directory)
        os.fsync(directory)
    finally:
        os.close(descriptor)
        try:
            os.unlink(name, dir_fd=directory)
        except FileNotFoundError:
            pass


def build(root=ROOT):
    root = Path(root)
    # Existing ancestors must be real, owned directories. A symlinked checkout
    # should be addressed by its canonical path explicitly, never repaired here.
    directory = open_directory(root / "ui/shaders")
    try:
        info = os.fstat(directory)
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) & 0o022:
            raise ValueError("shader directory must be owned and not writable by others")
        canonical = read_regular(root / "godot/shaders/atmosphere.gdshader", 65536)
        generated = translate(canonical)
        if generated != read_regular(root / "ui/shaders/atmosphere.frag", 65536):
            raise ValueError("shader source is out of date; regenerate ui/shaders/atmosphere.frag before release")
        environment = {"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"}
        # qsb selects the shader stage from the input extension. The input is a
        # new exclusive file in a new private directory. Its output goes to an
        # anonymous held descriptor, so no qsb output pathname can be planted.
        with tempfile.TemporaryDirectory(prefix="a-weather-app-shader-") as temporary:
            source = Path(temporary) / "atmosphere.frag"
            source_fd = os.open(source, os.O_WRONLY | os.O_CREAT | os.O_EXCL |
                                os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
            try:
                write_all(source_fd, generated.encode())
            finally:
                os.close(source_fd)
            pack_fd = os.memfd_create("a-weather-app-shader", os.MFD_CLOEXEC)
            try:
                output = f"/proc/{os.getpid()}/fd/{pack_fd}"
                bounded_run([QSB, "--glsl", "300 es,330", "--hlsl", "50", "--msl", "12",
                             "-o", output, str(source)], timeout=30, env=environment)
                if not 0 < os.fstat(pack_fd).st_size <= MAX_PACK:
                    raise ValueError("shader pack exceeds its size limit")
                bounded_run([QSB, "--dump", output], timeout=10, env=environment)
                os.lseek(pack_fd, 0, os.SEEK_SET)
                pack = os.read(pack_fd, MAX_PACK + 1)
                publish(directory, pack)
                return len(pack)
            finally:
                os.close(pack_fd)
    finally:
        os.close(directory)


def main():
    if len(sys.argv) != 1:
        print("Usage: python3 -I -B packaging/build_shaders.py", file=sys.stderr)
        return 2
    try:
        size = build()
    except (OSError, ValueError, RuntimeError, TimeoutError) as error:
        print(f"Forecast shader preparation failed: {error}", file=sys.stderr)
        return 1
    print(f"Prepared ui/shaders/{PACK} ({size} bytes). Ready to open the forecast window.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
