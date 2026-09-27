"""Bounded JSON-lines forecast and control bridge for an owned UI child."""
import argparse
from contextlib import contextmanager
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
import math
import os
import secrets
import select
import signal
import stat
import sys
import time
from zoneinfo import ZoneInfo

# Absolute script invocation works with Python -I -S and never imports from cwd.
if __package__ in (None, ""):
    sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from weather import runtime
from weather.mapping import CONDITIONS, STRENGTHS
from weather.location import validate_location, validate_zip_code, LocationError
from weather.provider import DEFAULT_LOCATION
from weather.solar import solar_position
from ui.effects_setup import EffectsSetup
from ui.notifications import PrecipitationWatcher
from scripts.run_weather_app import EFFECTS_ACTION_BUDGET, FORECAST_TERM_GRACE, FORECAST_KILL_GRACE

REQUEST_BYTES = 8192
RESPONSE_BYTES = 256 * 1024
CACHE_BYTES = runtime.MAX_BYTES
FETCH_DEADLINE = 25
# Reserved helper exit status: owned cleanup completed without confirmation.
# QML must propagate this failure even when the guardian initiated shutdown;
# ordinary helper failures retain the cached forecast until the user closes it.
BRIDGE_CLEANUP_FAILED_EXIT = 70
LOCATION_ERRORS = {"lookup_failed", "zip_not_found", "zip_ambiguous", "timeout", "state_io_failed", "save_unconfirmed"}
PROFILE_FILE = "location-profile.json"
DEFAULT_CONTROLS = {
    "mode": "live", "strength": "subtle", "manual": {"condition": "rain"},
    "reduced_motion": False, "lightning_enabled": False, "fps": 30,
    "window_physics": True, "accumulation": True, "pause_fullscreen": True, "units": "F",
}


def decode(raw, limit):
    """Bound bytes and syntactic nesting before the JSON decoder allocates."""
    if len(raw) > limit:
        raise ValueError("size")
    depth, quoted, escaped = 0, False, False
    for byte in raw:
        if quoted:
            if escaped:
                escaped = False
            elif byte == 92:
                escaped = True
            elif byte == 34:
                quoted = False
        elif byte == 34:
            quoted = True
        elif byte in (91, 123):
            depth += 1
            if depth > 8:
                raise ValueError("depth")
        elif byte in (93, 125):
            depth -= 1
    def pairs(items):
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("duplicate")
            result[key] = value
        return result
    value = json.loads(raw, object_pairs_hook=pairs,
                       parse_constant=lambda _: (_ for _ in ()).throw(ValueError("number")))
    count = 0
    def check(item):
        nonlocal count
        count += 1
        if count > 20000:
            raise ValueError("nodes")
        if isinstance(item, dict):
            if len(item) > 64:
                raise ValueError("object")
            for key, child in item.items():
                if len(key) > 64:
                    raise ValueError("key")
                check(child)
        elif isinstance(item, list):
            if len(item) > 1000:
                raise ValueError("array")
            for child in item:
                check(child)
        elif isinstance(item, str):
            if len(item) > 32000:
                raise ValueError("string")
        elif item is not None and not isinstance(item, bool):
            if not isinstance(item, (int, float)) or not math.isfinite(item) or abs(item) > 1e15:
                raise ValueError("number")
    check(value)
    return value


class StateDirectory:
    """Hold the final directory and resolve every file relative to its descriptor."""
    def __init__(self, path):
        if not os.path.isabs(path):
            raise ValueError("state directory must be absolute")
        parts = path.split("/")[1:]
        if not parts or any(part in ("", ".", "..") for part in parts):
            raise ValueError("invalid state directory")
        fd = os.open("/", os.O_RDONLY | os.O_DIRECTORY | os.O_CLOEXEC)
        try:
            for part in parts:
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW |
                                os.O_NONBLOCK | os.O_CLOEXEC, dir_fd=fd)
                os.close(fd)
                fd = child
            info = os.fstat(fd)
            if info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
                raise PermissionError("state directory must be owned and private")
            self.fd = fd
        except BaseException:
            os.close(fd)
            raise

    def close(self):
        os.close(self.fd)

    def read(self, name, limit):
        try:
            fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK |
                         os.O_CLOEXEC, dir_fd=self.fd)
        except FileNotFoundError:
            return None
        try:
            info = os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid() or
                    info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600 or
                    info.st_size > limit):
                raise PermissionError("unsafe state file")
            data = bytearray()
            while len(data) <= limit:
                chunk = os.read(fd, min(65536, limit + 1 - len(data)))
                if not chunk:
                    break
                data.extend(chunk)
            return decode(data, limit)
        finally:
            os.close(fd)

    def write(self, name, value, limit):
        raw = json.dumps(value, allow_nan=False, separators=(",", ":")).encode()
        if len(raw) > limit:
            raise ValueError("state size")
        temporary = ".bridge-" + secrets.token_hex(16)
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL |
                     os.O_NOFOLLOW | os.O_CLOEXEC, 0o600, dir_fd=self.fd)
        try:
            os.fchmod(fd, 0o600)
            remaining = memoryview(raw)
            while remaining:
                written = os.write(fd, remaining)
                if written <= 0:
                    raise OSError("short write")
                remaining = remaining[written:]
            os.fsync(fd)
            os.replace(temporary, name, src_dir_fd=self.fd, dst_dir_fd=self.fd)
            os.fsync(self.fd)
        finally:
            os.close(fd)
            try:
                os.unlink(temporary, dir_fd=self.fd)
            except FileNotFoundError:
                pass


def controls_patch(previous, patch):
    if not isinstance(patch, dict) or set(patch) - set(DEFAULT_CONTROLS):
        raise ValueError("controls")
    result = deepcopy(previous)
    result.update(deepcopy(patch))
    if result["mode"] not in ("live", "manual") or result["strength"] not in STRENGTHS:
        raise ValueError("controls")
    manual = result["manual"]
    if (not isinstance(manual, dict) or set(manual) != {"condition"} or
            not isinstance(manual["condition"], str) or manual["condition"] not in CONDITIONS):
        raise ValueError("manual")
    if type(result["fps"]) is not int or result["fps"] not in (15, 30, 60):
        raise ValueError("fps")
    if result["units"] not in ("F", "C"):
        raise ValueError("units")
    for key in ("reduced_motion", "lightning_enabled", "window_physics", "accumulation", "pause_fullscreen"):
        if type(result[key]) is not bool:
            raise ValueError("boolean")
    return result


def manual_weather(condition):
    # Synthetic values are only visual previews, never current/forecast observations.
    rate = {"drizzle": .7, "rain": 4., "snow": 2., "sleet": 3., "thunderstorm": 8.}.get(condition, 0.)
    clouds = {"clear": .05, "partly_cloudy": .45, "cloudy": .95, "fog": .7}.get(condition, 1.)
    return {"condition": condition, "precipitation_rate_mm_hr": rate,
            "cloud_cover": clouds, "visibility_m": 500. if condition == "fog" else 10000.,
            "wind_speed_m_s": 6., "wind_direction_deg": 270., "is_day": True}


@contextmanager
def fetch_deadline():
    """Linux main-thread absolute deadline includes DNS and both provider feeds."""
    def expired(signum, frame):
        # BaseException bypasses runtime.fetch_snapshot's optional-alert catch.
        raise FetchTimeout()
    previous = signal.signal(signal.SIGALRM, expired)
    timer = signal.setitimer(signal.ITIMER_REAL, FETCH_DEADLINE)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, *timer)
        signal.signal(signal.SIGALRM, previous)


class FetchTimeout(BaseException):
    pass


class RefreshWorker:
    """One owned fork, private result pipe, zero stdout/stderr and finite lifetime."""
    def __init__(self, state, fetcher, location, now, clock=time.monotonic):
        self.state, self.fetcher, self.location, self.now = state, fetcher, location, now
        self.clock, self.pid, self.pipe = clock, None, None
        self.deadline = 0
        self.close_in_child = ()

    def produce(self):
        candidate = self.fetcher(self.location, self.now())
        candidate = decode(json.dumps(candidate, allow_nan=False).encode(), CACHE_BYTES)
        validate_forecast(candidate, self.location)
        self.state.write("forecast.json", candidate, CACHE_BYTES)

    def failure_code(self, problem):
        return b"timeout" if isinstance(problem, FetchTimeout) else b"failed"

    def start(self):
        read_fd, write_fd = os.pipe2(os.O_CLOEXEC | os.O_NONBLOCK)
        try:
            pid = os.fork()
        except BaseException:
            os.close(read_fd)
            os.close(write_fd)
            raise
        if pid == 0:
            try:
                os.close(read_fd)
                # Fork does not honor CLOEXEC. These parent-owned descriptors
                # must not keep watcher locks alive after the bridge exits.
                # Close only: flock(LOCK_UN) would unlock the parent too.
                for fd in self.close_in_child:
                    os.close(fd)
                os.setsid()
                for signum in (signal.SIGTERM, signal.SIGINT):
                    signal.signal(signum, signal.SIG_DFL)
                null = os.open(os.devnull, os.O_RDWR | os.O_CLOEXEC)
                for fd in (0, 1, 2):
                    os.dup2(null, fd)
                os.close(null)
                with fetch_deadline():
                    self.produce()
                os.write(write_fd, b"ok")
            except BaseException as problem:
                try:
                    os.write(write_fd, self.failure_code(problem))
                except OSError:
                    pass
            finally:
                os._exit(0)
        os.close(write_fd)
        self.pid, self.pipe = pid, read_fd
        self.deadline = self.clock() + FETCH_DEADLINE

    def poll(self):
        if self.pid is None:
            return None
        pid, status = os.waitpid(self.pid, os.WNOHANG)
        if pid:
            raw = os.read(self.pipe, 33)
            os.close(self.pipe)
            self.pid, self.pipe = None, None
            if status == 0 and raw != b"timeout" and raw.decode("ascii", "replace") in LOCATION_ERRORS:
                return raw.decode("ascii")
            return ("ok" if raw == b"ok" and status == 0 else
                    "fetch_timeout" if raw == b"timeout" else "refresh_failed")
        if self.clock() >= self.deadline:
            self.stop()
            return "fetch_timeout"
        return None

    def stop(self):
        if self.pid is None:
            return
        # It is our unreaped child; PID cannot be reused until waitpid completes.
        self._signal(signal.SIGTERM)
        deadline = time.monotonic() + FORECAST_TERM_GRACE
        while time.monotonic() < deadline:
            if os.waitpid(self.pid, os.WNOHANG)[0]:
                break
            time.sleep(.01)
        else:
            self._signal(signal.SIGKILL)
            deadline = time.monotonic() + FORECAST_KILL_GRACE
            while time.monotonic() < deadline:
                if os.waitpid(self.pid, os.WNOHANG)[0]:
                    break
                time.sleep(min(.01, max(0, deadline - time.monotonic())))
            else:
                # Keep the unreaped identity and pipe: unknown is not exited.
                # Bridge.close still attempts effects cleanup in its finally.
                raise RuntimeError("weather worker cleanup could not be confirmed")
        os.close(self.pipe)
        self.pid, self.pipe = None, None

    def _signal(self, signum):
        try:
            os.killpg(self.pid, signum)
        except ProcessLookupError:
            # Child may be interrupted before setsid; no descendants exist yet.
            try:
                os.kill(self.pid, signum)
            except ProcessLookupError:
                pass


def location_selection(value):
    if not isinstance(value, dict):
        raise ValueError("location selection")
    if value.get("mode") == "zip" and set(value) == {"mode", "zip_code"}:
        return {"mode": "zip", "zip_code": validate_zip_code(value["zip_code"])}
    if value == {"mode": "auto"}:
        return {"mode": "auto", "zip_code": None}
    raise ValueError("location selection")


def validate_profile(value):
    if (not isinstance(value, dict) or set(value) != {"schema_version", "mode", "zip_code", "location", "forecast"}
            or type(value["schema_version"]) is not int or value["schema_version"] != 1):
        raise ValueError("location profile")
    selection = location_selection({"mode": value["mode"], "zip_code": value["zip_code"]}
                                   if value["mode"] == "zip" else {"mode": value["mode"]})
    if selection["zip_code"] != value["zip_code"]:
        raise ValueError("location profile ZIP")
    location = validate_location(value["location"])
    validate_forecast(value["forecast"], location)
    return value


def read_profile(state):
    value = state.read(PROFILE_FILE, CACHE_BYTES)
    if value is None:
        try:
            os.stat(PROFILE_FILE, dir_fd=state.fd, follow_symlinks=False)
        except FileNotFoundError:
            return None
        raise ValueError("location profile must be an object")
    return validate_profile(value)


class ProfileCache:
    """Forecast refreshes preserve the selected location in one atomic document."""
    def __init__(self, state, profile):
        self.state, self.profile = state, deepcopy(profile)

    def write(self, name, forecast, limit):
        if name != "forecast.json":
            raise ValueError("profile cache write")
        candidate = dict(self.profile, forecast=forecast)
        validate_profile(candidate)
        self.state.write(PROFILE_FILE, candidate, CACHE_BYTES)


def resolve_selection(selection):
    from weather.location import resolve_zip_code, resolve_local_location
    return (resolve_zip_code(selection["zip_code"]) if selection["mode"] == "zip"
            else resolve_local_location())


class LocationWorker(RefreshWorker):
    """Resolve and fetch off-thread; only the parent can commit the active profile."""
    def __init__(self, state, fetcher, selection, now, resolver=resolve_selection, clock=time.monotonic):
        super().__init__(state, fetcher, selection, now, clock)
        self.resolver = resolver
        self.result_name = ".location-result-" + secrets.token_hex(16) + ".json"

    def produce(self):
        location = validate_location(self.resolver(self.location))
        forecast = self.fetcher(location, self.now())
        candidate = {"schema_version": 1, **self.location, "location": location, "forecast": forecast}
        candidate = decode(json.dumps(candidate, allow_nan=False).encode(), CACHE_BYTES)
        validate_profile(candidate)
        try:
            self.state.write(self.result_name, candidate, CACHE_BYTES)
        except OSError as error:
            raise LocationError("Location could not be saved", code="state_io_failed") from error

    def failure_code(self, problem):
        if isinstance(problem, FetchTimeout):
            return b"timeout"
        code = getattr(problem, "code", "lookup_failed")
        return (code if code in LOCATION_ERRORS else "lookup_failed").encode("ascii")

    def result(self):
        return validate_profile(self.state.read(self.result_name, CACHE_BYTES))

    def discard(self):
        try:
            os.unlink(self.result_name, dir_fd=self.state.fd)
        except FileNotFoundError:
            pass
        except OSError:
            # Staging leftovers are inert private data. Their cleanup must not
            # prevent stopping live effects or leave an exited worker busy.
            return False
        return True

    def stop(self):
        super().stop()
        self.discard()


def plain(value, limit, multiline=False):
    if value is None:
        return None
    if not isinstance(value, str):
        raise ValueError("text")
    return "".join(c for c in value[:limit] if c not in "<>&" and
                   (ord(c) >= 32 or (multiline and c == "\n")) and not 127 <= ord(c) <= 159 and
                   not 0x202a <= ord(c) <= 0x202e and not 0x2066 <= ord(c) <= 0x2069)


WEATHER_NUMBERS = {"temperature_c": (-150, 100), "apparent_temperature_c": (-200, 150),
                   "humidity": (0, 1), "cloud_cover": (0, 1),
                   "precipitation_rate_mm_hr": (0, 10000), "precipitation_probability": (0, 1),
                   "wind_speed_m_s": (0, 1000), "wind_direction_deg": (0, 360),
                   "wind_gust_m_s": (0, 1000), "visibility_m": (0, 1e7)}


def number(value, bounds):
    if value is None:
        return None
    if type(value) not in (int, float) or not math.isfinite(value) or not bounds[0] <= value <= bounds[1]:
        raise ValueError("weather number")
    return value


def weather_record(row):
    if not isinstance(row, dict) or row.get("condition") not in CONDITIONS:
        raise ValueError("weather record")
    stamp = runtime.instant(row["time"]).isoformat()
    day = row.get("is_day")
    if day is not None and type(day) is not bool:
        raise ValueError("day")
    return dict({key: number(row.get(key), bounds) for key, bounds in WEATHER_NUMBERS.items()},
                time=stamp, condition=row["condition"], is_day=day)


def validate_forecast(value, location):
    runtime.validate_snapshot(value, location)
    weather_record(value["current"])
    for row in value["hourly"]:
        weather_record(row)
    for row in value["daily"]:
        daily_record(row)
    alerts = value.get("alerts", {})
    if not isinstance(alerts, dict) or alerts.get("status") not in ("available", "unavailable"):
        raise ValueError("alerts")
    items = alerts.get("items")
    if not isinstance(items, list) or len(items) > 256:
        raise ValueError("alerts")
    for item in items:
        alert_record(item)
    return value


def daily_record(row):
    if not isinstance(row, dict) or row.get("condition") not in CONDITIONS:
        raise ValueError("daily")
    date = row.get("date")
    if not isinstance(date, str) or len(date) != 10:
        raise ValueError("date")
    datetime.strptime(date, "%Y-%m-%d")
    return {"date": date, "condition": row["condition"],
            "high_c": number(row.get("high_c"), (-150, 100)),
            "low_c": number(row.get("low_c"), (-150, 100)),
            "precipitation_probability": number(row.get("precipitation_probability"), (0, 1)),
            **{key: None if row.get(key) is None else runtime.instant(row[key]).isoformat()
               for key in ("sunrise", "sunset")}}


def alert_record(row):
    if not isinstance(row, dict):
        raise ValueError("alert")
    return {**{key: plain(row.get(key), limit, multiline=key in ("description", "instruction")) for key, limit in
              (("id", 256), ("event", 128), ("headline", 512), ("severity", 32),
               ("urgency", 32), ("description", 4096), ("instruction", 2048))},
            "effective": runtime.instant(row["effective"]).isoformat(),
            "expires": runtime.instant(row["expires"]).isoformat(),
            "source": "National Weather Service",
            "text_truncated": any(isinstance(row.get(key), str) and len(row[key]) > limit
                                  for key, limit in (("description", 4096), ("instruction", 2048)))}


class Bridge:
    def __init__(self, state, *, fetcher=runtime.fetch_snapshot, now=None, monotonic=None,
                 effects_factory=None, instance=None, output=None, worker_factory=RefreshWorker,
                 location_worker_factory=LocationWorker, location_resolver=resolve_selection,
                 effects_setup=None, notifier=None):
        self.state, self.fetcher = state, fetcher
        self.now = now or (lambda: datetime.now(timezone.utc))
        self.monotonic = monotonic or time.monotonic
        self.profile = read_profile(state)
        configured_location = None if self.profile else read_location(state)
        self.location = deepcopy(self.profile["location"] if self.profile else configured_location or DEFAULT_LOCATION)
        self.location_mode = self.profile["mode"] if self.profile else ("custom" if configured_location else "default")
        self.zip_code = self.profile["zip_code"] if self.profile else None
        self.location_error = None
        self.location_worker = None
        self.location_worker_factory, self.location_resolver = location_worker_factory, location_resolver
        self.controls = controls_patch(DEFAULT_CONTROLS, state.read("controls.json", REQUEST_BYTES) or {})
        cached = self.profile["forecast"] if self.profile else read_legacy_forecast(state, self.location, fallback=self.location_mode == "default")
        self.forecast = validate_forecast(cached, self.location) if cached is not None else None
        self.error = None
        age = (self.now() - runtime.instant(cached["fetched_at"])).total_seconds() if cached else 900
        self.next_fetch = self.monotonic() + max(0, 900 - max(0, age))
        self.effects_factory = effects_factory
        self.supervisor = None
        self.instance, self.output = instance, output
        self.explicit_instance = instance
        self.setup = effects_setup if effects_setup is not None else EffectsSetup()
        self.close_attempted = False
        self.close_failed = False
        self.effect_request_deadline = None
        self.worker_factory, self.worker = worker_factory, None
        self.notifications = PrecipitationWatcher(state, now=self.now, notifier=notifier)
        # A saved auto profile records the user's explicit permission. Refresh
        # its approximate current city once each launch, keeping matching cached
        # weather visible until success. Fresh installs never contact an IP
        # provider until the user chooses Use local location.
        if self.location_mode == "auto":
            self.set_location({"mode": "auto"})

    def selected(self):
        return self.select_weather(now=self.now(),
                    mode="live" if self.effects_status().get("persistent") else self.controls["mode"], strength=self.controls["strength"],
                    manual=manual_weather(self.controls["manual"]["condition"]),
                    reduced_motion=self.controls["reduced_motion"],
                    lightning_enabled=self.controls["lightning_enabled"])

    def select_weather(self, **kwargs):
        selected = runtime.select_weather(self.forecast, **kwargs)
        if self.forecast is None:
            # The runtime has no location to infer before a forecast exists.
            selected["solar"] = (solar_position(kwargs["now"], self.location["latitude"], self.location["longitude"])
                if self.location else {"elevation_deg": -90., "azimuth_deg": 180.,
                                       "is_day": False, "twilight": "night", "daylight": 0.})
            if selected["mode"] == "live":
                solar = selected["solar"]
                selected["effects"].update(sun_elevation=solar["elevation_deg"],
                    sun_azimuth=solar["azimuth_deg"], is_day=solar["elevation_deg"] >= 0)
        return selected

    def effects_status(self):
        if self.supervisor is None:
            return {"state": "stopped", "persistent": False}
        status = self.supervisor.status()
        state = status.get("state")
        if state not in ("stopped", "starting", "running", "cleanup_failed"):
            state = "cleanup_failed"
        generation = status.get("session_generation")
        remaining = status.get("remaining_seconds", 0)
        return {"state": state, "error": "effects_failed" if status.get("error") else None,
                "persistent": status.get("persistent") is True and state == "running",
                "session_generation": generation if type(generation) is int and 0 < generation <= 2147483647 else None,
                "remaining_seconds": number(remaining, (0, 300))}

    def tick_notifications(self):
        self.notifications.tick(self.forecast, self.location,
            data_ok=self.location is not None and self.error is None
                and self.location_error is None and self.location_worker is None)

    def heartbeat(self):
        self.poll_location()
        self.poll_refresh()
        # The owned app remains the watcher; no autostart or detached daemon.
        if self.notifications.enabled and self.monotonic() >= self.next_fetch:
            self.refresh()
            self.poll_refresh()
        self.tick_notifications()
        # Only an explicitly started owned session receives background ticks.
        if self.supervisor is not None and self.supervisor.status().get("state") == "running":
            self.supervisor.tick(self.selected(), self.controls)

    def close(self):
        # serve() and main() both have finally cleanup. A failed bounded attempt
        # retains owned identities and propagates its error; finalizers must not
        # start a fresh grace period and invalidate the guardian's outer bound.
        if self.close_attempted:
            return
        previous = {}
        try:
            # A normal quit request can overlap launcher death. Do not let its
            # TERM interrupt the already-running, once-only cleanup halfway
            # through acknowledged native recovery.
            for signum in (signal.SIGTERM, signal.SIGINT):
                previous[signum] = signal.signal(signum, signal.SIG_IGN)
            self._set_effect_deadline(None)
            self.close_attempted = True
            self._close_once()
        except Exception:
            self.close_failed = True
            raise
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)

    def _close_once(self):
        try:
            try:
                self.notifications.begin_close()
            finally:
                try:
                    if self.location_worker is not None:
                        self.location_worker.stop()
                        self.location_worker = None
                finally:
                    try:
                        if self.worker is not None:
                            self.worker.stop()
                            self.worker = None
                    finally:
                        if self.supervisor is not None:
                            self.supervisor.stop()
                            if self.effects_status()["state"] != "stopped":
                                raise RuntimeError("effects cleanup could not be confirmed")
        finally:
            # Cancellation overlaps other teardown; this waits only to the
            # delivery's original deadline, never adds a fresh cleanup grace.
            self.notifications.finish_close()

    def refresh(self):
        if self.location is None or self.worker is not None or self.location_worker is not None:
            return
        self.next_fetch = self.monotonic() + runtime.REFRESH_SECONDS
        try:
            cache = ProfileCache(self.state, self.profile) if self.profile else self.state
            self.worker = self.worker_factory(cache, self.fetcher, self.location, self.now)
            self.worker.close_in_child = (() if self.notifications.lock is None
                                          else (self.notifications.lock,))
            self.worker.start()
        except Exception:
            if self.worker is not None:
                self.worker.stop()
            self.worker = None
            self.error = "refresh_failed"

    def poll_refresh(self):
        if self.worker is None:
            return
        result = self.worker.poll()
        if result is None:
            return
        self.worker = None
        if result == "ok":
            try:
                profile = read_profile(self.state) if self.profile else None
                candidate = profile["forecast"] if profile else self.state.read("forecast.json", CACHE_BYTES)
                validate_forecast(candidate, self.location)
                if profile is not None:
                    if (profile["mode"], profile["zip_code"]) != (self.location_mode, self.zip_code):
                        raise ValueError("refresh changed location selection")
                    self.profile = profile
                self.forecast = candidate
                self.error = None
            except Exception:
                self.error = "refresh_failed"
        else:
            self.error = "fetch_timeout" if result == "fetch_timeout" else "refresh_failed"

    def set_location(self, value):
        selection = location_selection(value)
        # Reap an old producer before starting a new one; it cannot publish a
        # stale completion after the selected location changes.
        if self.worker is not None:
            self.worker.stop()
            self.worker = None
        if self.location_worker is not None:
            self.location_worker.stop()
            self.location_worker = None
        self.location_error = None
        try:
            self.location_worker = self.location_worker_factory(self.state, self.fetcher,
                selection, self.now, resolver=self.location_resolver)
            self.location_worker.close_in_child = (() if self.notifications.lock is None
                                                   else (self.notifications.lock,))
            self.location_worker.start()
        except Exception:
            if self.location_worker is not None:
                self.location_worker.stop()
                self.location_worker = None
            self.location_error = "lookup_failed"

    def poll_location(self):
        worker = self.location_worker
        if worker is None:
            return
        result = worker.poll()
        if result is None:
            return
        self.location_worker = None
        candidate = None
        committing = False
        try:
            if result != "ok":
                self.location_error = ("timeout" if result == "fetch_timeout" else
                                       result if result in LOCATION_ERRORS else "lookup_failed")
                return
            candidate = validate_profile(worker.result())
            if (candidate["mode"], candidate["zip_code"]) != (worker.location["mode"], worker.location["zip_code"]):
                raise ValueError("location result does not match the request")
            # One authoritative atomic file: neither the bar nor the next
            # launch can pair new coordinates with old-city cached weather.
            committing = True
            self.state.write(PROFILE_FILE, candidate, CACHE_BYTES)
            self.adopt_profile(candidate)
        except OSError:
            self.location_error = "state_io_failed"
            # A directory fsync can fail *after* atomic replacement. Reconcile
            # against validated authoritative bytes instead of presenting two
            # different cities in this window and the bar/restarted app.
            if committing:
                try:
                    if read_profile(self.state) == candidate:
                        self.adopt_profile(candidate)
                        self.location_error = "save_unconfirmed"
                except (OSError, ValueError, TypeError):
                    pass
        except Exception:
            self.location_error = "lookup_failed"
        finally:
            worker.discard()

    def adopt_profile(self, candidate):
        self.profile = deepcopy(candidate)
        self.location = deepcopy(candidate["location"])
        self.location_mode, self.zip_code = candidate["mode"], candidate["zip_code"]
        self.forecast = candidate["forecast"]
        self.location_error, self.error = None, None
        self.next_fetch = self.monotonic() + runtime.REFRESH_SECONDS

    def snapshot(self, *, poll=True):
        self.poll_location()
        self.poll_refresh()
        if poll and self.monotonic() >= self.next_fetch:
            self.refresh()
            self.poll_refresh()
        now = self.now()
        live = self.select_weather(now=now,
                    reduced_motion=self.controls["reduced_motion"],
                    lightning_enabled=self.controls["lightning_enabled"])
        preview = self.selected()
        # Atmosphere consumes the established weather envelope independently of UI.
        self.state.write("selected.json", preview, CACHE_BYTES)
        if self.supervisor is not None and (not hasattr(self.supervisor, "action_time_available") or
                                            self.supervisor.action_time_available()):
            self.supervisor.tick(preview, self.controls)
        self.tick_notifications()
        forecast = live["forecast"]
        alerts = forecast.get("alerts", {}) if forecast else {}
        hourly = [] if forecast is None else [weather_record(row) for row in forecast["hourly"]
                                              if runtime.instant(row["time"]) >= now][:240]
        zone = ZoneInfo(self.location["timezone"] if self.location else "UTC")
        def clock_label(value):
            return runtime.instant(value).astimezone(zone).strftime("%I:%M %p").lstrip("0")
        for row in hourly:
            stamp = runtime.instant(row["time"])
            local = stamp.astimezone(zone)
            row["local_hour"] = local.strftime("%I %p").lstrip("0")
            row["local_date"] = local.date().isoformat()
            row["local_label"] = local.strftime("%a %b %d · %I %p %Z")
            start = (stamp - timedelta(hours=1)).astimezone(zone)
            row["period_label"] = start.strftime("%I:%M %p %Z").lstrip("0") + " – " + local.strftime("%I:%M %p %Z").lstrip("0")
        daily = [daily_record(row) for row in forecast["daily"]][:10] if forecast else []
        for row in daily:
            date = datetime.strptime(row["date"], "%Y-%m-%d").date()
            row["day_label"] = "Today" if date == now.astimezone(zone).date() else date.strftime("%a")
            for key in ("sunrise", "sunset"):
                row[key + "_label"] = clock_label(row[key]) if row[key] else None
        # Rank the bounded provider collection before the display cap, so a
        # severe warning cannot be hidden behind eight lower-priority notices.
        severity = {"Extreme": 0, "Severe": 1, "Moderate": 2, "Minor": 3}
        urgency = {"Immediate": 0, "Expected": 1, "Future": 2, "Past": 3}
        ranked = sorted(alerts.get("items", []), key=lambda a: (
            severity.get(a.get("severity"), 4), urgency.get(a.get("urgency"), 4),
            runtime.instant(a["expires"])))
        display_alerts = [alert_record(row) for row in ranked[:8]]
        for row in display_alerts:
            row["expires_label"] = runtime.instant(row["expires"]).astimezone(zone).strftime("%a %I:%M %p %Z")
        display_location = self.location or {"name": "Current location", "timezone": "UTC", "latitude": None, "longitude": None}
        return {"schema_version": 1, "location": deepcopy(display_location),
                "location_settings": {"mode": self.location_mode, "zip_code": self.zip_code,
                                      "busy": self.location_worker is not None, "error": self.location_error},
                "current": weather_record(forecast["current"]) if forecast else None,
                "hourly": hourly,
                "daily": daily,
                "alerts": {"status": alerts.get("status", "unavailable"),
                           "items": display_alerts},
                "source": {"name": "Open-Meteo", "attribution": "Weather data by Open-Meteo.com (CC BY 4.0)",
                           "fetched_at": forecast["fetched_at"] if forecast else None,
                           "freshness": live["freshness"], "age_seconds": live["age_seconds"],
                           "error": self.error, "refreshing": self.worker is not None},
                "notifications": self.notifications.snapshot(),
                "controls": deepcopy(self.controls),
                "atmosphere": live["effects"],
                "preview": {"current": preview["current"], "effects": preview["effects"],
                            "freshness": preview["freshness"]},
                "effect_status": self.effects_status(), "effects_setup": self.setup.snapshot()}

    def request(self, request):
        # Preview->live transition and its result snapshot share one absolute
        # proxy budget. Neither stop, start nor the final tick can reset it.
        self.effect_request_deadline = time.monotonic() + EFFECTS_ACTION_BUDGET
        self._set_effect_deadline(self.effect_request_deadline)
        try:
            return self._request(request)
        finally:
            self.effect_request_deadline = None
            self._set_effect_deadline(None)

    def _set_effect_deadline(self, deadline):
        if self.supervisor is not None and hasattr(self.supervisor, "set_request_deadline"):
            self.supervisor.set_request_deadline(deadline)

    def _request(self, request):
        request_id = None
        try:
            if not isinstance(request, dict):
                raise ValueError("request")
            request_id = request.get("request_id")
            if type(request_id) is not int or not 0 <= request_id <= 2147483647:
                request_id = None
                raise ValueError("request id")
            op = request.get("op")
            allowed = ({"request_id", "op", "notifications"} if op == "set_notifications" else
                       {"request_id", "op", "controls"} if op == "set_controls" else
                       {"request_id", "op", "location"} if op == "set_location" else
                       {"request_id", "op", "output"} if op == "select_output" else
                       {"request_id", "op", "duration"} if op == "start_effects" else {"request_id", "op"})
            if set(request) != allowed or op not in ("snapshot", "set_controls", "set_location", "refresh", "quit", "check_effects", "select_output", "start_effects", "start_live_effects", "stop_effects", "set_notifications", "snooze_notifications", "resume_notifications"):
                raise ValueError("operation")
            if self.close_attempted:
                return ({"request_id": request_id, "ok": not self.close_failed}, True) if op == "quit" else (
                    {"request_id": request_id, "ok": False, "error": "service_closed"}, False)
            if op == "quit":
                self.close()
                return {"request_id": request_id, "ok": True}, True
            if op in ("check_effects", "select_output"):
                if self.effects_status()["state"] != "stopped":
                    return {"request_id": request_id, "ok": False, "error": "effects_active"}, False
                if op == "check_effects":
                    self.instance = None
                    setup = self.setup.check(self.explicit_instance, self.output)
                else:
                    setup = self.setup.select(request["output"])
                self.instance = self.setup.instance
                self.output = setup["selected_output"]
            if op in ("start_effects", "start_live_effects"):
                if op == "start_effects" and (type(request["duration"]) is not int or not 1 <= request["duration"] <= 300):
                    raise ValueError("duration")
                if not self.instance or not self.output or self.location is None:
                    return {"request_id": request_id, "ok": False, "error": "effects_not_configured"}, False
                try:
                    self.setup.verify_selection(self.instance, self.output)
                    if self.supervisor is None:
                        if self.effects_factory is None:
                            from ui.effects_proxy import OwnedEffectsSupervisor
                            self.effects_factory = OwnedEffectsSupervisor
                        self.supervisor = self.effects_factory()
                        self._set_effect_deadline(self.effect_request_deadline)
                    if op == "start_live_effects":
                        status = self.effects_status()
                        if not (status["state"] == "running" and status["persistent"]):
                            if status["state"] != "stopped":
                                self.supervisor.stop()
                                if self.effects_status()["state"] != "stopped":
                                    raise RuntimeError("preview cleanup incomplete")
                            self.supervisor.start_live(self.instance, self.output, self.controls)
                    else:
                        self.supervisor.start(request["duration"], self.instance, self.output, self.controls)
                except Exception:
                    self.setup.failed_activation()
                    self.instance = None
                    return {"request_id": request_id, "ok": False, "error": "effects_start_failed",
                            "snapshot": self.snapshot(poll=False)}, False
            if op == "stop_effects":
                # Stopping precipitation must not silently cancel a user's
                # in-flight location lookup. Full application close still does.
                if self.worker is not None:
                    self.worker.stop()
                    self.worker = None
                if self.supervisor is not None:
                    self.supervisor.stop()
                if self.effects_status()["state"] == "cleanup_failed":
                    return {"request_id": request_id, "ok": False, "error": "effects_stop_failed",
                            "snapshot": self.snapshot(poll=False)}, False
            if op == "set_notifications":
                self.notifications.configure(request["notifications"])
            if op in ("snooze_notifications", "resume_notifications"):
                self.notifications.snooze(resume=op == "resume_notifications")
            if op == "set_controls":
                candidate = controls_patch(self.controls, request["controls"])
                self.state.write("controls.json", candidate, REQUEST_BYTES)
                self.controls = candidate
            if op == "set_location":
                self.set_location(request["location"])
            if op == "refresh":
                self.refresh()
            return {"request_id": request_id, "ok": True,
                    "snapshot": self.snapshot(poll=op == "snapshot")}, False
        except (ValueError, TypeError, KeyError):
            return {"request_id": request_id, "ok": False, "error": "invalid_request"}, False
        except OSError:
            return {"request_id": request_id, "ok": False, "error": "state_io_failed"}, self.close_attempted
        except Exception:
            return {"request_id": request_id, "ok": False, "error": "effects_failed"}, self.close_attempted


def read_requests(bridge, incoming):
    """Bound raw framing before allocation; idle polls also supervise finite effects."""
    try:
        fd = incoming.fileno()
    except (AttributeError, OSError):
        while True:
            raw = incoming.readline(REQUEST_BYTES + 1)
            if not raw:
                return
            yield raw
    pending = bytearray()
    next_tick = time.monotonic()
    while True:
        ready, _, _ = select.select([fd], [], [], max(0., next_tick - time.monotonic()))
        if time.monotonic() >= next_tick:
            bridge.heartbeat()
            next_tick = time.monotonic() + .5
        if not ready:
            continue
        chunk = os.read(fd, min(4096, REQUEST_BYTES + 1 - len(pending)))
        if not chunk:
            if pending:
                yield bytes(pending)
            return
        pending.extend(chunk)
        while b"\n" in pending:
            index = pending.index(10) + 1
            raw = bytes(pending[:index])
            del pending[:index]
            yield raw
        if len(pending) > REQUEST_BYTES:
            yield bytes(pending)
            return


def serve(bridge, incoming, outgoing):
    try:
        _serve(bridge, incoming, outgoing)
    finally:
        bridge.close()


def _serve(bridge, incoming, outgoing):
    for raw in read_requests(bridge, incoming):
        # Oversized framing is terminal: never drain an unbounded adversarial line.
        if len(raw) > REQUEST_BYTES:
            response, stopped = {"request_id": None, "ok": False, "error": "request_too_large"}, True
        else:
            try:
                response, stopped = bridge.request(decode(raw, REQUEST_BYTES))
            except (ValueError, TypeError, RecursionError, UnicodeError, OverflowError):
                response, stopped = {"request_id": None, "ok": False, "error": "invalid_json"}, False
        encoded = json.dumps(response, allow_nan=False, separators=(",", ":")).encode() + b"\n"
        if len(encoded) > RESPONSE_BYTES:
            encoded = json.dumps({"request_id": response.get("request_id"), "ok": False,
                                  "error": "response_too_large"}, separators=(",", ":")).encode() + b"\n"
        outgoing.write(encoded)
        outgoing.flush()
        if stopped:
            return


FRIENDLY_CONDITIONS = {"clear": "Clear", "partly_cloudy": "Partly cloudy", "cloudy": "Cloudy",
                       "fog": "Fog", "drizzle": "Drizzle", "rain": "Rain", "snow": "Snow",
                       "sleet": "Sleet", "thunderstorm": "Thunderstorm", "unknown": "Unknown"}


def read_location(state):
    value = state.read("location.json", REQUEST_BYTES)
    if value is None:
        try:
            os.stat("location.json", dir_fd=state.fd, follow_symlinks=False)
        except FileNotFoundError:
            return None
        raise ValueError("location configuration must be an object")
    return validate_location(value)


def read_legacy_forecast(state, location, *, fallback=False):
    value = state.read("forecast.json", CACHE_BYTES)
    # Older builds cached the developer's default without an explicit location
    # choice. Never relabel that cache or turn it into a saved user override.
    # Validate even discarded data; explicit configuration mismatches still fail.
    if fallback and isinstance(value, dict) and value.get("location") != location:
        validate_forecast(value, validate_location(value.get("location")))
        return None
    return value


def bar_snapshot(state, now=None):
    """Read cached observations only. No fetch, selected publication or controls write."""
    result = {"schema_version": 1, "label": "--° · Unavailable",
              "tooltip": "Weather and alerts unavailable", "freshness": "unavailable"}
    try:
        profile = read_profile(state)
        configured_location = None if profile else read_location(state)
        location = profile["location"] if profile else configured_location or DEFAULT_LOCATION
        name = plain(location["name"], 244)
        result["tooltip"] = plain(name + " · Weather and alerts unavailable", 256)
        value = profile["forecast"] if profile else read_legacy_forecast(state, location, fallback=configured_location is None)
        if value is None:
            return result
        validate_forecast(value, location)
        selected = runtime.select_weather(value, now=now or datetime.now(timezone.utc))
        freshness = selected["freshness"]
        result["freshness"] = freshness
        if freshness not in ("fresh", "stale"):
            return result
        current = weather_record(value["current"])
        temperature = current["temperature_c"]
        degrees = "--" if temperature is None else str(round(temperature * 9 / 5 + 32))
        condition = FRIENDLY_CONDITIONS[current["condition"]]
        result["label"] = degrees + "° · " + condition + (" · Stale" if freshness == "stale" else "")
        alerts = selected["forecast"]["alerts"]
        if alerts.get("status") == "available" and alerts.get("items"):
            count = len(alerts["items"])
            result["label"] += f" · {count} alert" + ("s" if count != 1 else "")
        alert_text = "Alerts unavailable" if alerts.get("status") != "available" else (
            "; ".join(plain(row.get("event"), 80) or "Weather alert" for row in alerts.get("items", [])[:2])
            or "No active alerts in cached feed")
        result["tooltip"] = name + " · " + condition + " · " + alert_text
        result["label"] = plain(result["label"], 96)
        result["tooltip"] = plain(result["tooltip"], 256)
    except (ValueError, TypeError, KeyError, OSError, OverflowError):
        pass
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", required=True)
    parser.add_argument("--bar-snapshot", action="store_true")
    args = parser.parse_args(argv)
    state = None
    bridge = None
    exit_code = 0
    previous_handlers = {}
    def stop(signum, frame):
        for stopping_signal in (signal.SIGTERM, signal.SIGINT):
            signal.signal(stopping_signal, signal.SIG_IGN)
        raise BridgeStopped()
    try:
        state = StateDirectory(args.state_dir)
        if args.bar_snapshot:
            raw = json.dumps(bar_snapshot(state), allow_nan=False, separators=(",", ":")).encode() + b"\n"
            if len(raw) > 4096:
                raise ValueError("bar output")
            sys.stdout.buffer.write(raw)
            return 0
        for signum in (signal.SIGTERM, signal.SIGINT):
            previous_handlers[signum] = signal.signal(signum, stop)
        bridge = Bridge(state, instance=os.environ.get("A_WEATHER_APP_INSTANCE"),
                        output=os.environ.get("A_WEATHER_APP_OUTPUT"))
        serve(bridge, sys.stdin.buffer, sys.stdout.buffer)
    except BridgeStopped:
        pass
    except (Exception, FetchTimeout):
        if args.bar_snapshot:
            sys.stdout.write('{"schema_version":1,"label":"--° · Unavailable","tooltip":"Weather and alerts unavailable","freshness":"unavailable"}\n')
            return 0
        # Static, small stderr only; never echo exceptions, paths, API data or requests.
        sys.stderr.write("A Weather App UI bridge failed.\n")
        exit_code = 1
    finally:
        try:
            if bridge is not None:
                # Repeat signals must not interrupt owned child teardown halfway.
                for signum in previous_handlers:
                    signal.signal(signum, signal.SIG_IGN)
                try:
                    bridge.close()
                except (Exception, FetchTimeout):
                    # A signal can reach main before serve's own finalizer. Map
                    # that cleanup failure here as well, without a traceback or
                    # a fresh retry that would renew the shutdown deadline.
                    bridge.close_failed = True
        finally:
            try:
                if state is not None:
                    state.close()
            finally:
                for signum, handler in previous_handlers.items():
                    signal.signal(signum, handler)
    # Compute the process result only after the once-only finalizer. In
    # particular, a SIGTERM caught before close must not precompute success.
    return BRIDGE_CLEANUP_FAILED_EXIT if bridge is not None and bridge.close_failed else exit_code


class BridgeStopped(BaseException):
    """Interrupt network activity immediately while retaining finally cleanup."""


if __name__ == "__main__":
    raise SystemExit(main())
