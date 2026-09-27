"""Read-only, explicit desktop-effects compatibility checks.

The environment names a display, never a trusted compositor. Its socket and the
matching Hyprland lock/process are verified afresh. No configuration is changed,
no plugin is loaded, and no state is saved. Activation repeats NativeBackend's
preflight before loading anything; a successful check is never authorization.
"""
from copy import deepcopy
import math
import os
from pathlib import Path
import re
import stat

from ui.effects import NativeBackend, PLUGIN, HOST, open_directory, read_regular, validate_native_output

INSTANCE = re.compile(r"[a-f0-9]+_[0-9]+_[0-9]+")
OUTPUT = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}")


def unchecked():
    return dict(status="unchecked", reason="not_checked", outputs=[], selected_output=None)


class SetupUnavailable(Exception):
    def __init__(self, reason):
        self.reason = reason


def socket_at(directory, name):
    info = os.stat(name, dir_fd=directory, follow_symlinks=False)
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid():
        raise SetupUnavailable("session_unavailable")


def process_identity(pid):
    backend = NativeBackend()
    backend.pid = pid
    backend.starttime = read_regular(f"/proc/{pid}/stat").rsplit(")", 1)[1].split()[19]
    backend.identity()
    return backend.starttime


def current_instance(environment, explicit=None):
    """Find one live session matching the app's actual Wayland display.

    Never use HYPRLAND_INSTANCE_SIGNATURE: a launcher can retain one after a
    compositor restart. Enumeration, lock reads and process metadata are bounded.
    """
    runtime = environment.get("XDG_RUNTIME_DIR", "")
    display = environment.get("WAYLAND_DISPLAY", "")
    if not runtime or not Path(runtime).is_absolute():
        raise SetupUnavailable("session_unavailable")
    if display.startswith(runtime + "/"):
        display = display[len(runtime) + 1:]
    if not re.fullmatch(r"wayland-[0-9]{1,10}", display):
        raise SetupUnavailable("wayland_required")
    if explicit is not None and not INSTANCE.fullmatch(explicit):
        raise SetupUnavailable("session_unavailable")
    runtime_fd = open_directory(runtime)
    try:
        info = os.fstat(runtime_fd)
        if info.st_uid != os.getuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise SetupUnavailable("session_unavailable")
        socket_at(runtime_fd, display)
        directory = os.open("hypr", os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC,
                            dir_fd=runtime_fd)
        try:
            names = []
            with os.scandir(directory) as entries:
                for entry in entries:
                    if len(names) >= 64:
                        raise SetupUnavailable("session_unavailable")
                    names.append(entry.name)
        finally:
            os.close(directory)
    finally:
        os.close(runtime_fd)
    matches = []
    for name in names:
        if not INSTANCE.fullmatch(name) or (explicit is not None and name != explicit):
            continue
        base = Path(runtime) / "hypr" / name
        try:
            fields = read_regular(base / "hyprland.lock").splitlines()
            if len(fields) != 2 or not fields[0].isdigit() or not 1 <= int(fields[0]) <= 2147483647 or fields[1] != display:
                continue
            descriptor = open_directory(base)
            try:
                socket_at(descriptor, ".socket.sock")
            finally:
                os.close(descriptor)
            # A compositor crash can leave its lock and socket inode behind.
            # Count only live Hyprland identities before deciding ambiguity;
            # otherwise a restarted display remains unusable indefinitely.
            process_identity(int(fields[0]))
            matches.append((name, int(fields[0])))
        except (OSError, ValueError, RuntimeError, IndexError, SetupUnavailable):
            continue
    if len(matches) != 1:
        raise SetupUnavailable("session_unavailable")
    return matches[0]


def monitor_records(value):
    if not isinstance(value, list) or not 1 <= len(value) <= 32:
        raise SetupUnavailable("output_unavailable")
    result, names = [], set()
    for row in value:
        if (not isinstance(row, dict) or not isinstance(row.get("name"), str)
                or not OUTPUT.fullmatch(row["name"]) or row["name"] in names
                or type(row.get("disabled")) is not bool):
            raise SetupUnavailable("output_unavailable")
        names.add(row["name"])
        disabled = row["disabled"]
        numbers = [row.get("width", 0), row.get("height", 0), row.get("scale", 1)]
        if any(type(n) not in (int, float) or not math.isfinite(n) for n in numbers):
            raise SetupUnavailable("output_unavailable")
        width, height, scale = numbers
        if not 0 <= width <= 32768 or not 0 <= height <= 32768 or not .25 <= scale <= 8:
            raise SetupUnavailable("output_unavailable")
        if not disabled and (width <= 0 or height <= 0):
            raise SetupUnavailable("output_unavailable")
        result.append(dict(name=row["name"], width=width, height=height,
                           scale=scale, enabled=not disabled))
    return result


class EffectsSetup:
    def __init__(self, *, environment=None, backend_factory=NativeBackend,
                 discover=current_instance, artifacts=None):
        self.environment = dict(os.environ if environment is None else environment)
        self.backend_factory, self.discover = backend_factory, discover
        self.artifacts = artifacts or (lambda: PLUGIN.is_file() and HOST.is_file() and os.access(HOST, os.X_OK))
        self.instance = None
        self.provenance = None
        self.current = unchecked()

    def snapshot(self):
        return deepcopy(self.current)

    def check(self, explicit=None, output=None):
        # Fail closed before I/O: stale successful discovery cannot survive any
        # exception, disappearance, compositor restart or failed re-check.
        self.instance = None
        self.provenance = None
        self.current = dict(status="unavailable", reason="session_unavailable", outputs=[], selected_output=None)
        try:
            instance, pid = self.discover(self.environment, explicit)
            backend = self.backend_factory()
            backend.instance, backend.pid = instance, pid
            backend.starttime = read_regular(f"/proc/{pid}/stat").rsplit(")", 1)[1].split()[19]
            backend.identity()
            native_outputs = backend.ctl("monitors", "all")
            outputs = monitor_records(native_outputs)
            self.current["outputs"] = outputs
            enabled = [row["name"] for row in outputs if row["enabled"]]
            selected = output if output in enabled else enabled[0] if len(enabled) == 1 else None
            self.current["selected_output"] = selected
            if output is not None and output not in enabled:
                raise SetupUnavailable("output_unavailable")
            if len(outputs) != 1:
                raise SetupUnavailable("multiple_outputs")
            if selected is None:
                raise SetupUnavailable("output_unavailable")
            try:
                validate_native_output(native_outputs[0])
            except ValueError:
                raise SetupUnavailable("unsupported_output") from None
            if backend.inventory() != []:
                raise SetupUnavailable("plugin_conflict")
            if not self.artifacts():
                raise SetupUnavailable("native_missing")
            try:
                # Existing read-only preflight checks host/plugin symbol
                # compatibility. It does not load code or mutate the desktop.
                backend.preflight(instance, selected)
            except (OSError, ValueError, RuntimeError, TimeoutError):
                raise SetupUnavailable("native_incompatible") from None
            self.instance = instance
            self.provenance = (instance, backend.pid, backend.starttime)
            self.current.update(status="ready", reason="ready")
        except SetupUnavailable as error:
            self.current["reason"] = error.reason
        except Exception:
            # Includes malformed bounded compositor JSON and metadata races.
            # Never retain a previous successful check after any failed probe.
            self.current["reason"] = "session_unavailable"
        return self.snapshot()

    def select(self, output):
        if not isinstance(output, str) or not OUTPUT.fullmatch(output):
            raise ValueError("output")
        if not any(row["name"] == output and row["enabled"] for row in self.current["outputs"]):
            raise ValueError("output unavailable")
        self.current["selected_output"] = output
        return self.snapshot()

    def failed_activation(self):
        self.instance = None
        self.provenance = None
        self.current.update(status="unavailable", reason="activation_failed")

    def verify_selection(self, instance, output):
        """Revalidate UI selection provenance immediately before the owner starts.

        Only bounded metadata reads here: the effects owner independently queries
        monitors and rechecks native compatibility before loading its plugin.
        """
        if self.current["status"] != "ready":
            return  # Explicit CLI selectors still receive native preflight.
        try:
            current, pid = self.discover(self.environment, instance)
            starttime = read_regular(f"/proc/{pid}/stat").rsplit(")", 1)[1].split()[19]
            if ((current, pid, starttime) != self.provenance
                    or instance != self.instance or output != self.current["selected_output"]):
                raise SetupUnavailable("activation_failed")
        except Exception:
            self.failed_activation()
            raise SetupUnavailable("activation_failed") from None
