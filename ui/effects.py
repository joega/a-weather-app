"""Explicit effects ownership; construction and forecast ticks never activate.

One selected compositor/output, one plugin load, one native generation. No config
writes, desktop dispatch, or automatic build. Live sessions renew guarded finite
leases while their owner remains healthy; previews retain fixed expiry. Backend injection
allows lifecycle tests without touching a desktop.
"""
import json
from datetime import datetime, timedelta, timezone
import math
import os
from pathlib import Path
import re
import resource
import secrets
import selectors
import signal
import shutil
import stat
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
PLUGIN = ROOT / "native/frame-alignment/a-weather-app-frame-alignment.so"
HOST = ROOT / "native/atmosphere/a-weather-app-atmosphere"
TEMPERATURE_SELECTION_SECONDS = 8


def live_temperature(selected_weather, *, now=None):
    return live_temperature_lease(selected_weather, now=now)[0]


def live_temperature_lease(selected_weather, *, now=None):
    """Only current live observations can drive melting; unknown never means warm.

    Recheck the source timestamps on every owner tick, including cached worker
    ticks. A freshness label alone cannot keep an old observation usable.
    """
    from weather.runtime import instant, STALE_SECONDS
    try:
        if (type(selected_weather.get("schema_version")) is not int
                or selected_weather["schema_version"] != 1
                or selected_weather.get("mode") != "live"
                or selected_weather.get("freshness") != "fresh"):
            return None, None
        current = selected_weather["current"]
        value = current["temperature_c"]
        if type(value) not in (int, float) or not math.isfinite(value) or not -100 <= value <= 65:
            return None, None
        now = now or datetime.now(timezone.utc)
        valid_until = now + timedelta(seconds=TEMPERATURE_SELECTION_SECONDS)
        for timestamp, limit in ((selected_weather["selected_at"], TEMPERATURE_SELECTION_SECONDS),
                                 (selected_weather["forecast"]["fetched_at"], STALE_SECONDS),
                                 (current["time"], STALE_SECONDS)):
            if not isinstance(timestamp, str) or len(timestamp) > 64:
                return None, None
            parsed = instant(timestamp)
            if not 0 <= (now - parsed).total_seconds() <= limit:
                return None, None
            valid_until = min(valid_until, parsed + timedelta(seconds=limit))
        expiry_ms = math.floor(valid_until.timestamp() * 1000)
        if expiry_ms <= now.timestamp() * 1000:
            return None, None
        return value, expiry_ms
    except (AttributeError, KeyError, TypeError, ValueError, OverflowError):
        return None, None


def validate_native_output(monitor):
    """Match persistent output restrictions in geometry.cpp/segment_pass.cpp.

    Hyprland's monitor JSON exposes the resolved colorManagementPreset and
    mirrorOf ("none" for an independent output). Dynamic HDR/fullscreen,
    render-context and copy-framebuffer suppression remain native checks.
    """
    if (not isinstance(monitor, dict) or type(monitor.get("transform")) is not int
            or monitor["transform"] != 0 or monitor.get("mirrorOf") != "none"
            or monitor.get("colorManagementPreset") not in ("srgb", "wide", "edid", "dcip3", "dp3", "adobe")):
        raise ValueError("unsupported native output configuration")
    values = [monitor.get(key) for key in ("width", "height", "scale", "x", "y")]
    if any(type(value) not in (int, float) or not math.isfinite(value) for value in values):
        raise ValueError("unsupported native output geometry")
    width, height, scale, x, y = values
    # Hyprland rounds positive pixel-size / scale to its logical monitor size.
    if (not 0 < scale <= 8 or not .5 <= width / scale < 16384.5
            or not .5 <= height / scale < 16384.5 or abs(x) > 32768 or abs(y) > 32768):
        raise ValueError("unsupported native output geometry")


def open_directory(path):
    path = Path(path)
    if not path.is_absolute() or ".." in path.parts:
        raise ValueError("absolute nontraversing directory required")
    fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
    system_owner = os.fstat(fd).st_uid
    try:
        for component in path.parts[1:]:
            child = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=fd)
            os.close(fd)
            fd = child
            info = os.fstat(fd)
            if info.st_uid not in (os.getuid(), system_owner) or (info.st_mode & 0o022 and not info.st_mode & stat.S_ISVTX):
                raise ValueError("untrusted directory ancestor")
        return fd
    except BaseException:
        os.close(fd)
        raise


def child_environment():
    allowed = ("HOME", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "LANG", "LC_ALL",
               "DBUS_SESSION_BUS_ADDRESS", "GSK_RENDERER")
    # The renderer retains a 1 MiB RLIMIT_FSIZE for bounded child logs.
    # Mesa's shared shader-cache index is larger than that; writing it can
    # terminate the renderer with SIGXFSZ even when its own log is tiny.
    # Disable only this child's disk cache rather than relaxing the log bound.
    return {"PATH": "/usr/bin:/bin", "MESA_SHADER_CACHE_DISABLE": "true",
            **{key: os.environ[key] for key in allowed if key in os.environ}}


def read_regular(path, limit=4096):
    path = Path(path)
    directory = open_directory(path.parent)
    try:
        fd = os.open(path.name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=directory)
    finally:
        os.close(directory)
    try:
        info = os.fstat(fd)
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                or info.st_nlink != 1 or info.st_size > limit or info.st_mode & 0o022):
            raise ValueError("untrusted identity file")
        data = bytearray()
        while len(data) <= limit:
            chunk = os.read(fd, min(4096, limit + 1 - len(data)))
            if not chunk:
                break
            data.extend(chunk)
        if len(data) > limit:
            raise ValueError("identity file byte limit")
        return bytes(data).decode("utf-8", "strict")
    finally:
        os.close(fd)


def bounded_run(command, *, timeout=3, stdout_limit=262144, stderr_limit=8192, env=None):
    """Concurrent raw reads cap both streams before assembly; reap the owned group."""
    process = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True, env=env)
    output = [bytearray(), bytearray()]
    deadline = time.monotonic() + timeout
    try:
        with selectors.DefaultSelector() as selector:
            for index, stream in enumerate((process.stdout, process.stderr)):
                os.set_blocking(stream.fileno(), False)
                selector.register(stream, selectors.EVENT_READ, index)
            while selector.get_map():
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError("command deadline")
                for key, _events in selector.select(remaining):
                    index = key.data
                    limit = stdout_limit if index == 0 else stderr_limit
                    chunk = os.read(key.fileobj.fileno(), min(65536, limit + 1 - len(output[index])))
                    if not chunk:
                        selector.unregister(key.fileobj)
                        continue
                    output[index].extend(chunk)
                    if len(output[index]) > limit:
                        raise ValueError("command output byte limit")
        # Observe exit without releasing the PID/PGID anchor. Reaping first and
        # then killpg would risk signaling a newly reused, unrelated group.
        while True:
            exited = os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOWAIT | os.WNOHANG)
            if exited is not None:
                break
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("command exit deadline")
            time.sleep(min(.01, remaining))
        returncode = exited.si_status if exited.si_code == os.CLD_EXITED else -exited.si_status
        if returncode:
            raise RuntimeError(f"command failed ({returncode}): " + bytes(output[1]).decode("utf-8", "replace")[:512])
        return bytes(output[0]).decode("utf-8", "strict")
    finally:
        try:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        finally:
            try:
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
            finally:
                process.stdout.close()
                process.stderr.close()


def child_limits():
    resource.setrlimit(resource.RLIMIT_FSIZE, (1048576, 1048576))
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))


def cleanup_owned_directory(path, identity):
    parent = open_directory(path.parent)
    directory = None
    try:
        directory = os.open(path.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
        info = os.fstat(directory)
        if (info.st_dev, info.st_ino) != identity:
            raise ValueError("effects runtime directory identity changed")
        entries = []
        with os.scandir(directory) as iterator:
            for entry in iterator:
                if len(entries) >= 64 or not re.fullmatch(r"selected\.json|policy\.json|child-[01]\.jsonl|host-restart-[0-9]+\.jsonl", entry.name):
                    raise ValueError("unmanaged effects runtime entry")
                record = entry.stat(follow_symlinks=False)
                if not stat.S_ISREG(record.st_mode) or record.st_uid != os.getuid() or record.st_nlink != 1:
                    raise ValueError("unmanaged effects runtime object")
                entries.append(entry.name)
        for name in entries:
            os.unlink(name, dir_fd=directory)
        current = os.stat(path.name, dir_fd=parent, follow_symlinks=False)
        if (current.st_dev, current.st_ino) != identity:
            raise ValueError("effects runtime path replaced during cleanup")
        os.rmdir(path.name, dir_fd=parent)
    finally:
        if directory is not None:
            os.close(directory)
        os.close(parent)


def validate_controls(controls):
    if not isinstance(controls, dict):
        raise ValueError("invalid effects controls")
    result = {key: controls.get(key, default) for key, default in
              (("reduced_motion", False), ("window_physics", True),
               ("accumulation", True), ("lightning_enabled", False), ("fps", 30))}
    if any(type(result[key]) is not bool for key in result if key != "fps"):
        raise ValueError("effects flags must be boolean")
    if type(result["fps"]) is not int or result["fps"] not in (15, 30, 60):
        raise ValueError("effects fps must be 15, 30 or 60")
    return result


class NativeBackend:
    """Current-machine CLI adapter. Never uses inherited instance selection."""
    def __init__(self):
        self.instance = None
        self.pid = None
        self.starttime = None
        self.env = child_environment()
        self.processes = []
        self.logs = []
        self.plugin_inventory = None
        self.host_restarts = 0

    def ctl(self, *args, json_output=True):
        self.identity()
        raw = bounded_run(["/usr/bin/hyprctl", "-i", self.instance,
                                 *(["-j"] if json_output else []), *args],
                          stdout_limit=2 * 1024 * 1024, env=self.env)
        value = json.loads(raw) if json_output else raw.strip()
        if isinstance(value, dict) and value.get("error"):
            raise RuntimeError(str(value["error"]))
        return value

    def identity(self):
        if self.pid is None:
            raise RuntimeError("compositor identity missing")
        fields = read_regular(f"/proc/{self.pid}/stat").rsplit(")", 1)[1].split()
        if fields[19] != self.starttime or Path(f"/proc/{self.pid}/exe").resolve() != Path("/usr/bin/Hyprland").resolve():
            raise RuntimeError("compositor process changed")

    def preflight(self, instance, output):
        runtime = os.environ.get("XDG_RUNTIME_DIR")
        if not runtime or not Path(runtime).is_absolute():
            raise RuntimeError("absolute XDG_RUNTIME_DIR required")
        base = Path(runtime) / "hypr" / instance
        fields = read_regular(base / "hyprland.lock").splitlines()
        if len(fields) != 2 or not fields[0].isdigit() or not re.fullmatch(r"wayland-[0-9]+", fields[1]):
            raise RuntimeError("invalid compositor lock identity")
        if not stat.S_ISSOCK((base / ".socket.sock").stat().st_mode):
            raise RuntimeError("compositor socket missing")
        self.instance, self.pid = instance, int(fields[0])
        self.starttime = read_regular(f"/proc/{self.pid}/stat").rsplit(")", 1)[1].split()[19]
        self.identity()
        if self.ctl("plugin", "list") != []:
            raise RuntimeError("plugin inventory must be empty")
        monitors = self.ctl("monitors", "all")
        # GDK indexes and Hyprland IDs differ. Single-output restriction avoids guessing.
        if not isinstance(monitors, list) or len(monitors) != 1:
            raise RuntimeError("effects currently require one configured output")
        monitor = monitors[0]
        if (monitor.get("name") != output or type(monitor.get("id")) is not int
                or monitor["id"] < 0 or monitor.get("disabled") is not False):
            raise RuntimeError("selected output must be unique and enabled")
        validate_native_output(monitor)
        if not PLUGIN.is_file() or not HOST.is_file() or not os.access(HOST, os.X_OK):
            raise RuntimeError("prebuilt native effects unavailable")
        bounded_run(["/usr/bin/prlimit", "--as=268435456", "--core=0", "--",
                     "/usr/bin/python3", "-I", "-B", str(ROOT / "scripts/check_plugin_symbols.py"), str(PLUGIN)], timeout=10, env=self.env)
        # PLUGIN_INIT independently rejects server/client hash mismatch before registration.
        self.env.update(WAYLAND_DISPLAY=fields[1], HYPRLAND_INSTANCE_SIGNATURE=instance)
        return monitor["id"]

    def inventory(self):
        return self.ctl("plugin", "list")

    def load(self):
        error = None
        try:
            reply = self.ctl("plugin", "load", str(PLUGIN), json_output=False)
            if reply.lower() != "ok":
                error = RuntimeError(f"plugin load rejected: {reply}")
        except Exception as failure:
            error = failure
        # Recover ownership after an accepted load whose reply was lost. Never
        # adopt a foreign record merely because the inventory contains one item.
        inventory = self.inventory()
        if (not isinstance(inventory, list) or len(inventory) != 1 or not isinstance(inventory[0], dict)
                or inventory[0].get("name") != "a-weather-app-frame-alignment"):
            raise RuntimeError("unexpected plugin inventory after load")
        self.plugin_inventory = inventory
        if error:
            raise error

    def native(self, command):
        if self.plugin_inventory is None:
            if command == "status" and self.inventory() == []:
                return {"enabled": False}
            raise RuntimeError("plugin ownership unknown")
        if self.plugin_inventory is not None and self.inventory() != self.plugin_inventory:
            raise RuntimeError("plugin ownership changed")
        return self.ctl("a-weather-app:rain", command)

    def publish(self, path, value):
        encoded = json.dumps(value, allow_nan=False).encode()
        if len(encoded) > 2 * 1024 * 1024:
            raise ValueError("selected weather size limit")
        directory = open_directory(path.parent)
        temporary = f".{path.name}.{secrets.token_hex(16)}.tmp"
        fd = None
        try:
            info = os.fstat(directory)
            if info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise ValueError("effects runtime must be private")
            fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                         0o600, dir_fd=directory)
            os.fchmod(fd, 0o600)
            remaining = memoryview(encoded)
            while remaining:
                written = os.write(fd, remaining)
                if written <= 0:
                    raise OSError("short effects publication write")
                remaining = remaining[written:]
            os.fsync(fd)
            os.replace(temporary, path.name, src_dir_fd=directory, dst_dir_fd=directory)
            os.fsync(directory)
        finally:
            if fd is not None:
                os.close(fd)
                try:
                    os.unlink(temporary, dir_fd=directory)
                except FileNotFoundError:
                    pass
            os.close(directory)

    def spawn(self, directory, duration, instance, output, fps):
        commands = [
            ["/usr/bin/python3", "-B", str(ROOT / "bridge/presentation_policy.py"), "--instance", instance,
             "--output", output, "--output-file", str(directory / "policy.json"),
             "--duration", str(duration), "--native-lock-status"],
            [str(HOST), "--monitor", "0", "--duration", str(duration), "--fps", str(fps),
             "--weather", str(directory / "selected.json"), "--policy", str(directory / "policy.json"),
             "--policy-session", instance, "--output", output]]
        if getattr(self, "persistent", False):
            for command in commands:
                command.append("--renewable-lease")
        for index, command in enumerate(commands):
            log = self.open_log(directory / f"child-{index}.jsonl")
            self.logs.append(log)
            self.processes.append(subprocess.Popen(command, env=self.env, cwd=ROOT,
                                                   stdin=subprocess.DEVNULL, stdout=log, stderr=log, preexec_fn=child_limits))

    def renew_children(self):
        if len(self.processes) != 2 or not self.children_alive():
            raise RuntimeError("effects lease child missing")
        for process in self.processes:
            process.send_signal(signal.SIGUSR1)

    def open_log(self, path):
        directory = open_directory(path.parent)
        try:
            info = os.fstat(directory)
            if info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise ValueError("effects logs require private runtime")
            fd = os.open(path.name, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=directory)
        finally:
            os.close(directory)
        return os.fdopen(fd, "wb")

    def children_alive(self):
        if getattr(self, "persistent", False):
            self.bound_logs()
        return len(self.processes) == 2 and all(p.poll() is None for p in self.processes)

    def bound_logs(self):
        # Operate only on held descriptors. O_APPEND keeps child writes at the
        # new EOF after truncation, rather than leaving sparse files at old offsets.
        # Retain current diagnostics between rotations; RLIMIT_FSIZE still
        # terminates an unexpectedly noisy child before it can exhaust storage.
        for stream in self.logs:
            info = os.fstat(stream.fileno())
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid()
                    or info.st_nlink != 1 or info.st_mode & 0o077):
                raise ValueError("effects log identity changed")
            if info.st_size >= 262144:
                os.ftruncate(stream.fileno(), 0)

    def restart_host(self, directory, duration, instance, output, fps):
        restart_started = time.monotonic()
        if self.host_restarts >= 8:
            raise RuntimeError("finite atmosphere restart budget exceeded")
        if len(self.processes) != 2:
            raise RuntimeError("owned atmosphere process missing")
        old = self.processes[1]
        if old.poll() is None:
            old.terminate()
            try:
                old.wait(timeout=3)
            except subprocess.TimeoutExpired:
                old.kill()
                old.wait(timeout=3)
        if old.returncode not in (0, -15):
            raise RuntimeError(f"owned atmosphere stop failed: {old.returncode}")
        duration = 30 if getattr(self, "persistent", False) else math.floor(duration - (time.monotonic() - restart_started))
        if duration < 1:
            raise RuntimeError("effects expiry reached during atmosphere restart")
        self.logs[1].close()
        self.host_restarts += 1
        log = self.open_log(directory / f"host-restart-{self.host_restarts}.jsonl")
        self.logs[1] = log
        command = [str(HOST), "--monitor", "0", "--duration", str(duration), "--fps", str(fps),
                   "--weather", str(directory / "selected.json"), "--policy", str(directory / "policy.json"),
                   "--policy-session", instance, "--output", output]
        if getattr(self, "persistent", False):
            command.append("--renewable-lease")
        self.processes[1] = subprocess.Popen(command, env=self.env, cwd=ROOT,
                                             stdin=subprocess.DEVNULL, stdout=log, stderr=log, preexec_fn=child_limits)

    def stop_children(self):
        errors = []
        for process in reversed(self.processes):
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=3)
            if process.returncode not in (0, -15):
                errors.append(f"child exit {process.returncode}")
        self.processes.clear()
        for stream in self.logs:
            stream.close()
        self.logs.clear()
        return errors

    def unload(self):
        inventory = self.inventory()
        if inventory == []:
            self.plugin_inventory = None
            return
        if self.plugin_inventory is None or inventory != self.plugin_inventory:
            raise RuntimeError("plugin ownership ambiguous; refusing unload")
        reply = self.ctl("plugin", "unload", str(PLUGIN), json_output=False)
        if reply.lower() != "ok" or self.inventory() != []:
            raise RuntimeError("owned plugin unload verification failed")
        self.plugin_inventory = None


class EffectsSupervisor:
    def __init__(self, backend=None, *, clock=time.monotonic, directory_factory=None, wall_clock=None):
        self.backend = backend or NativeBackend()
        self.clock = clock
        self.wall_clock = wall_clock or (lambda: datetime.now(timezone.utc))
        self.directory_factory = directory_factory or (lambda: Path(tempfile.mkdtemp(prefix="a-weather-app-effects-")))
        self.state, self.error = "stopped", None
        self.loaded = False
        self.generation = None
        self.directory = None
        self.deadline = 0
        self.last_updates = {}
        self.persistent = False
        self.native_cleanup_failed = False

    def status(self):
        return dict(state=self.state, error=self.error, session_generation=self.generation,
                    persistent=self.persistent and self.state == "running",
                    remaining_seconds=max(0, self.deadline - self.clock()) if self.state == "running" else 0)

    def start_live(self, instance, output, controls):
        return self.start(30, instance, output, controls, persistent=True)

    def start(self, duration, instance, output, controls, *, persistent=False):
        if self.state != "stopped":
            raise RuntimeError("effects already active or cleanup unresolved")
        if type(duration) is not int or not 1 <= duration <= 300:
            raise ValueError("effects duration must be 1..300")
        if not isinstance(instance, str) or not re.fullmatch(r"[a-f0-9]+_[0-9]+_[0-9]+", instance):
            raise ValueError("select compositor instance")
        if not isinstance(output, str) or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", output):
            raise ValueError("select output connector")
        flags = validate_controls(controls)
        self.persistent = persistent
        self.backend.persistent = persistent
        self.instance, self.output = instance, output
        self.error = None
        self.last_updates = {}
        self.state = "starting"
        try:
            monitor = self.backend.preflight(instance, output)
            candidate = self.directory_factory()
            info = os.stat(candidate, follow_symlinks=False)
            if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise ValueError("effects runtime must be a private owned directory")
            self.directory_identity = (info.st_dev, info.st_ino)
            self.directory = candidate
            self.backend.publish(self.directory / "policy.json", dict(schema_version=1, session=instance,
                output=output, sequence=1, generated_at_unix_ms=0, stale_after_ms=1500,
                render_allowed=False, reason="producer_stopped"))
            # Mark attempted load: an accepted write with a lost reply still needs rollback.
            self.loaded = True
            self.backend.load()
            if self.backend.native("status").get("enabled") is not False:
                raise RuntimeError("new plugin must start disabled")
            for key in ("reduced_motion", "window_physics", "accumulation"):
                self.backend.native(f"{key} {str(flags[key]).lower()}")
            self.backend.native(f"fps {flags['fps']}")
            self.backend.native("set rain_intensity 0")
            self.backend.native("snow intensity 0")
            self.deadline = self.clock() + duration
            status = self.backend.native(f"on {duration} {monitor}")
            generation = status.get("session_generation")
            if status.get("enabled") is not True or type(generation) is not int or generation <= 0:
                raise RuntimeError("native session identity missing")
            self.generation = generation
            self.verify_status(status, flags)
            self.backend.spawn(self.directory, duration, instance, output, flags["fps"])
            self.host_fps = flags["fps"]
            self.state = "running"
            return self.status()
        except Exception as error:
            self.error = str(error)
            self.stop()
            raise RuntimeError(self.error) from error

    def tick(self, selected_weather, controls):
        if self.state != "running":
            return self.status()
        if self.clock() >= self.deadline:
            return self.stop()
        try:
            flags = validate_controls(controls)
            if flags["fps"] != self.host_fps and self.deadline - self.clock() < 1:
                return self.stop()
            status = self.backend.native("status")
            if status.get("enabled") is not True or status.get("session_generation") != self.generation:
                raise RuntimeError("native session replaced or expired")
            if not self.backend.children_alive():
                raise RuntimeError("effects child stopped")
            if self.persistent and self.deadline - self.clock() <= 20:
                renewed = self.backend.native(f"guard {self.generation} renew 30")
                if renewed.get("enabled") is not True or renewed.get("session_generation") != self.generation:
                    raise RuntimeError("native lease renewal identity mismatch")
                self.backend.renew_children()
                self.deadline = self.clock() + 30
            effects = dict(selected_weather["effects"])
            updates = {key: str(flags[key]).lower() for key in ("reduced_motion", "window_physics", "accumulation")}
            updates["fps"] = str(flags["fps"])
            for key, low, high, command in (("rain_intensity", 0, 1, "set rain_intensity"),
                    ("snow_intensity", 0, 1, "snow intensity"), ("wind_x", -500, 500, "set wind_x")):
                value = effects[key]
                if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or not low <= value <= high:
                    raise ValueError(f"invalid selected {key}")
                updates[command] = f"{value:.9g}"
            for command, value in updates.items():
                if self.last_updates.get(command) != value:
                    self.backend.native(f"guard {self.generation} {command} {value}")
            # Earlier control IPC may be slow. Validate immediately before the
            # thermal command rather than letting it inherit their elapsed time.
            temperature, valid_until_ms = live_temperature_lease(selected_weather, now=self.wall_clock())
            updates["snow temperature"] = "unknown" if temperature is None else f"{temperature:.9g} {valid_until_ms}"
            if self.last_updates.get("snow temperature") != updates["snow temperature"]:
                self.backend.native(f"guard {self.generation} snow temperature {updates['snow temperature']}")
            after = self.backend.native("status")
            self.verify_status(after, flags)
            actual = dict(after.get("target_parameters", {}))
            actual["snow_intensity"] = after.get("snow_target_parameters", {}).get("intensity")
            for key in ("rain_intensity", "snow_intensity", "wind_x"):
                value = actual.get(key)
                if (isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value)
                        or not math.isclose(value, effects[key], rel_tol=1e-5, abs_tol=1e-6)):
                    raise RuntimeError("native target verification failed")
            snow = after.get("snow_target_parameters", {})
            # The reply may have been sampled on either side of expiry. Accept
            # an elapsed lease's conservative clear or its last valid sample;
            # native expiry itself does not depend on this reply arriving.
            lease_elapsed = temperature is not None and self.wall_clock().timestamp() * 1000 >= valid_until_ms
            known = snow.get("temperature_known")
            if known is not (temperature is not None) and not (lease_elapsed and known is False):
                raise RuntimeError("native temperature verification failed")
            if temperature is not None and known:
                value = snow.get("temperature_c")
                if (type(value) not in (int, float) or not math.isfinite(value)
                        or not math.isclose(value, temperature, rel_tol=1e-5, abs_tol=1e-6)):
                    raise RuntimeError("native temperature verification failed")
            self.last_updates = updates
            effects["reduced_motion"] = flags["reduced_motion"]
            effects["lightning_enabled"] = bool(flags["lightning_enabled"] and effects.get("lightning_enabled") is True
                                                  and not flags["reduced_motion"])
            envelope = dict(selected_weather, effects=effects)
            self.backend.publish(self.directory / "selected.json", envelope)
            if flags["fps"] != self.host_fps:
                # A live host gets a complete finite lease, while preview
                # restarts cannot extend the original session expiry.
                remaining = 30 if self.persistent else math.floor(self.deadline - self.clock())
                if remaining < 1:
                    return self.stop()
                self.backend.restart_host(self.directory, remaining, self.instance, self.output, flags["fps"])
                self.host_fps = flags["fps"]
            return self.status()
        except Exception as error:
            self.error = str(error)
            return self.stop()

    def stop(self):
        try:
            errors = list(self.backend.stop_children())
        except Exception as error:
            errors = [f"effects child cleanup failed: {error}"]
        if self.loaded:
            try:
                status = self.backend.native("status")
                if self.generation is not None and status.get("session_generation") != self.generation:
                    raise RuntimeError("native session ownership changed; refusing stop/unload")
                self.native_cleanup_failed |= status.get("cleanup_failed") is True
                if status.get("enabled"):
                    if self.generation is None:
                        raise RuntimeError("native session ownership changed; refusing stop/unload")
                    stopped = self.backend.native(f"guard {self.generation} off")
                    if stopped.get("session_generation") != self.generation or stopped.get("enabled") is not False:
                        raise RuntimeError("native stop acknowledgement invalid; refusing unload")
                    self.native_cleanup_failed |= stopped.get("cleanup_failed") is True
                self.backend.unload()
                self.loaded = False
            except Exception as error:
                errors.append(str(error))
        if self.native_cleanup_failed:
            errors.append("native resource cleanup failed; runtime evidence retained")
        if not errors and self.directory is not None:
            try:
                cleanup_owned_directory(self.directory, self.directory_identity)
                self.directory = None
            except FileNotFoundError:
                self.directory = None
            except (OSError, ValueError) as error:
                errors.append(f"owned runtime cleanup failed: {error}")
        self.state = "cleanup_failed" if errors else "stopped"
        if errors:
            self.error = "; ".join(([self.error] if self.error else []) + errors)
        self.generation = None if not self.loaded else self.generation
        return self.status()

    def verify_status(self, status, flags):
        if status.get("enabled") is not True or status.get("session_generation") != self.generation:
            raise RuntimeError("native session generation verification failed")
        if (type(status.get("fps")) is not int or status["fps"] != flags["fps"] or
                any(type(status.get(key)) is not bool or status[key] != flags[key]
                    for key in ("reduced_motion", "window_physics", "accumulation"))):
            raise RuntimeError("native control verification failed")
