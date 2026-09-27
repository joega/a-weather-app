#!/usr/bin/python3
"""Internal bounded UI cleanup owner; never select or signal an external PID.

No public desktop/config operations. Its only liveness input is a passed private
pipe; SIGKILL of the original launcher produces EOF without Python cooperation.
The owned UI leader stays unreaped until process-group cleanup completes.
"""
import argparse
import math
import os
from pathlib import Path
import stat
import select
import sys
import re

# Explicit local bootstrap is required with system Python isolated mode.
sys.path.insert(0, str(Path(__file__).resolve().parent))
from run_weather_app import BRIDGE_CLEANUP_GRACE, supervise


class BoundedStderr:
    """Guardian-only diagnostics; owned UI never inherits this descriptor."""
    def __init__(self, descriptor=2, limit=8192):
        self.descriptor, self.remaining = descriptor, limit
    def write(self, text):
        if self.remaining:
            data = text[:self.remaining].encode("utf-8", "replace")[:self.remaining]
            view = memoryview(data)
            while view:
                count = os.write(self.descriptor, view)
                if count <= 0:
                    raise OSError("guardian diagnostic short write")
                view = view[count:]
            self.remaining -= len(data)
        return len(text)
    def flush(self):
        pass


def finalize_diagnostic(directory, name, success, descriptor=2):
    if directory is None:
        return
    if not re.fullmatch(r"\.guardian-[a-f0-9]{32}\.log", name or ""):
        raise ValueError("invalid owned guardian diagnostic name")
    parent = os.fstat(directory)
    file = os.fstat(descriptor)
    current = os.stat(name, dir_fd=directory, follow_symlinks=False)
    if (not stat.S_ISDIR(parent.st_mode) or parent.st_uid != os.getuid() or parent.st_mode & 0o077
            or not stat.S_ISREG(file.st_mode) or file.st_uid != os.getuid() or file.st_mode & 0o077
            or (current.st_dev, current.st_ino) != (file.st_dev, file.st_ino)):
        raise ValueError("guardian diagnostic ownership changed")
    if success:
        os.unlink(name, dir_fd=directory)
    else:
        os.fsync(descriptor)
        # Only this owned last-error artifact is replaced, never a user settings file.
        os.replace(name, "guardian-last-error.log", src_dir_fd=directory, dst_dir_fd=directory)
    os.fsync(directory)


def main(argv=None):
    sys.stderr = BoundedStderr()
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--owner-fd", type=int, required=True)
    parser.add_argument("--duration", type=float)
    parser.add_argument("--cleanup-grace", type=float, default=BRIDGE_CLEANUP_GRACE)
    parser.add_argument("--force-grace", type=float, default=3)
    parser.add_argument("--diagnostic-dir-fd", type=int)
    parser.add_argument("--diagnostic-name")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args(argv)
    if args.owner_fd < 3 or not stat.S_ISFIFO(os.fstat(args.owner_fd).st_mode):
        parser.error("private liveness pipe required")
    if args.duration is not None and (not math.isfinite(args.duration) or not 0 < args.duration <= 3600):
        parser.error("bounded duration required")
    if (not math.isfinite(args.cleanup_grace) or not 0 <= args.cleanup_grace <= BRIDGE_CLEANUP_GRACE or
            not math.isfinite(args.force_grace) or not 0 <= args.force_grace <= 3):
        parser.error("bounded cleanup grace required")
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or not Path(command[0]).is_absolute():
        parser.error("absolute owned UI command required")
    success = False
    try:
        if select.select([args.owner_fd], [], [], 0)[0]:
            if os.read(args.owner_fd, 1):
                raise RuntimeError("unexpected launcher liveness data")
            success = True
            return 0  # Owner died during startup: never create a late UI surface.
        result = supervise(command, os.environ.copy(), args.duration, owner_fd=args.owner_fd,
                           cleanup_grace=args.cleanup_grace, force_grace=args.force_grace, quiet_child=True)
        success = result == 0
        if not success:
            print(f"Owned weather UI exited with failure ({result})", file=sys.stderr)
        return result
    except Exception as error:
        print(f"Weather guardian cleanup failed: {str(error)[:512]}", file=sys.stderr)
        return 1
    finally:
        try:
            finalize_diagnostic(args.diagnostic_dir_fd, args.diagnostic_name, success)
        finally:
            os.close(args.owner_fd)
            if args.diagnostic_dir_fd is not None:
                os.close(args.diagnostic_dir_fd)


if __name__ == "__main__":
    raise SystemExit(main())
