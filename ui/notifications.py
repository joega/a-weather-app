"""Quiet opt-in hourly precipitation watching; no effects or autostart ownership.

Reserve an event before delivery. An uncertain delivery is never retried: this
favors a missed notification over duplicate weather messages after a crash.
All helper work is asynchronous and shares the app's owned process session.
"""
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import fcntl
import hashlib
import math
import os
import re
import select
import signal
import stat
import subprocess
import time
from zoneinfo import ZoneInfo

from weather import runtime

STATE_FILE = "notifications.json"
LOCK_FILE = "notifications.lock"
STATE_LIMIT = 32768
MAX_EVENTS = 64
RETENTION = 14 * 86400
COOLDOWN = 6 * 3600
REVISION_GAP = 3 * 3600
LOOKAHEAD = 3 * 3600
DELIVERY_TIMEOUT = 3.0
DELIVERY_REAP_DEADLINE = 3.5
DEFAULT_SETTINGS = dict(enabled=False, quiet_enabled=True, quiet_start=22,
                        quiet_end=7, probability=50)


def settings_patch(previous, patch):
    if not isinstance(patch, dict) or not patch or set(patch) - set(DEFAULT_SETTINGS):
        raise ValueError("notification settings")
    result = dict(previous, **patch)
    for name in ("enabled", "quiet_enabled"):
        if type(result[name]) is not bool:
            raise ValueError("notification flag")
    for name in ("quiet_start", "quiet_end"):
        if type(result[name]) is not int or not 0 <= result[name] <= 23:
            raise ValueError("quiet hour")
    if result["quiet_start"] == result["quiet_end"]:
        raise ValueError("empty quiet period")
    if type(result["probability"]) is not int or result["probability"] not in (50, 70):
        raise ValueError("notification probability")
    return result


def text(value, limit):
    # Notification daemons may interpret markup even when QML does not.
    return re.sub(r"[<>&\x00-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]", "", value)[:limit]


def location_key(location):
    # Renames and timezone corrections do not create a new physical location.
    values = [location.get("latitude"), location.get("longitude")]
    if any(type(value) not in (int, float) or not math.isfinite(value) for value in values):
        raise ValueError("notification coordinates")
    if not -90 <= values[0] <= 90 or not -180 <= values[1] <= 180:
        raise ValueError("notification coordinates")
    return hashlib.sha256((f"{values[0]:.4f},{values[1]:.4f}").encode("ascii")).hexdigest()


def candidate(forecast, location, now, probability):
    """A qualifying hourly window, never a minute-level onset prediction."""
    if not isinstance(forecast, dict) or forecast.get("location") != location:
        return None
    age = (now - runtime.instant(forecast["fetched_at"])).total_seconds()
    if not 0 <= age <= runtime.STALE_SECONDS:
        return None
    hourly = forecast.get("hourly")
    if not isinstance(hourly, list) or len(hourly) > 240:
        return None
    wet = []
    for row in hourly:
        chance = row.get("precipitation_probability") if isinstance(row, dict) else None
        if type(chance) not in (int, float) or not math.isfinite(chance) or not probability / 100 <= chance <= 1:
            continue
        stamp = runtime.instant(row["time"])
        if abs((stamp - now).total_seconds()) > 11 * 86400:
            continue
        # Open-Meteo precipitation probability describes the preceding hour.
        # Work with the interval start internally; never shift the forecast an
        # hour later merely to label it as an upcoming precipitation window.
        wet.append((stamp.timestamp() - 3600, chance))
    wet.sort()
    groups = []
    for stamp, chance in wet:
        if groups and stamp <= groups[-1]["end"] + REVISION_GAP:
            groups[-1]["end"] = max(groups[-1]["end"], stamp + 3600)
            groups[-1]["hours"].append((stamp, chance))
        else:
            groups.append(dict(start=stamp, end=stamp + 3600, hours=[(stamp, chance)]))
    instant = now.timestamp()
    for event in groups:
        selected = next(((stamp, chance) for stamp, chance in event["hours"]
                         if stamp + 3600 > instant and stamp <= instant + LOOKAHEAD), None)
        if selected is not None:
            stamp, chance = selected
            zone = ZoneInfo(location["timezone"])
            start = datetime.fromtimestamp(stamp, timezone.utc).astimezone(zone)
            end = datetime.fromtimestamp(stamp + 3600, timezone.utc).astimezone(zone)
            # Include date and timezone to make midnight/DST boundaries honest.
            window = start.strftime("%a %-I %p %Z") + "–" + end.strftime("%-I %p %Z")
            body = (f"{text(location['name'], 96)}: {round(chance * 100)}% chance of precipitation "
                    f"for {window}. Open-Meteo hourly forecast; timing may change.")
            return dict(location=location_key(location), start=event["start"], end=event["end"],
                        title="Hourly precipitation outlook", body=text(body, 320))
    return None


class Notifier:
    """One low-urgency helper; no pipes, shell, new session, or renewed deadlines."""
    def __init__(self, *, command=("/usr/bin/notify-send",), monotonic=time.monotonic):
        self.command, self.monotonic = tuple(command), monotonic
        self.process = self.pidfd = None
        self.deadline = self.reap_deadline = 0
        self.cancelled = False

    @property
    def available(self):
        return os.path.isfile(self.command[0]) and os.access(self.command[0], os.X_OK)

    def start(self, title, body):
        if self.process is not None:
            raise RuntimeError("delivery already pending")
        command = [*self.command, "--urgency=low", "--expire-time=8000", "--transient",
                   "--app-name=A Weather App", "--hint=boolean:suppress-sound:true",
                   "--", text(title, 80), text(body, 320)]
        began = self.monotonic()
        self.deadline, self.reap_deadline = began + DELIVERY_TIMEOUT, began + DELIVERY_REAP_DEADLINE
        self.cancelled = False
        self.process = subprocess.Popen(command, stdin=subprocess.DEVNULL,
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, close_fds=True,
            preexec_fn=delivery_alarm)
        try:
            self.pidfd = os.pidfd_open(self.process.pid)
        except BaseException:
            self.process.kill()  # Still an unreaped direct child; PID is reserved.
            raise

    def cancel(self):
        if self.process is not None:
            self.cancelled = True
            try:
                if self.pidfd is not None:
                    signal.pidfd_send_signal(self.pidfd, signal.SIGKILL)
                else:
                    self.process.kill()
            except ProcessLookupError:
                pass

    def poll(self):
        if self.process is None:
            return None
        result = self.process.poll()
        if result is None:
            if self.monotonic() >= self.deadline:
                self.cancel()
            if self.monotonic() >= self.reap_deadline:
                raise RuntimeError("notification helper exit unconfirmed")
            return None
        if self.pidfd is not None:
            os.close(self.pidfd)
        self.process = self.pidfd = None
        return "sent" if result == 0 and not self.cancelled else "failed"

    def finish_close(self):
        # cancel() runs before all other Bridge cleanup. Waiting here can only
        # use the helper's original lifetime deadline, never a new close grace.
        while self.process is not None and self.monotonic() < self.reap_deadline:
            if self.poll() is not None:
                return
            remaining = max(0, self.reap_deadline - self.monotonic())
            if self.pidfd is not None:
                select.select([self.pidfd], [], [], min(.02, remaining))
            else:
                time.sleep(min(.02, remaining))
        if self.process is not None and self.poll() is None:
            raise RuntimeError("notification helper cleanup could not be confirmed")


def delivery_alarm():
    # The real-time alarm survives exec. Even a busy bridge cannot extend the
    # installed notifier's active lifetime by delaying its next poll.
    signal.signal(signal.SIGALRM, signal.SIG_DFL)
    signal.setitimer(signal.ITIMER_REAL, DELIVERY_TIMEOUT)


def validate_document(value):
    if (not isinstance(value, dict) or set(value) != {"schema_version", "settings", "snoozed_until", "events"}
            or value["schema_version"] != 1 or type(value["schema_version"]) is not int
            or not isinstance(value["settings"], dict) or set(value["settings"]) != set(DEFAULT_SETTINGS)):
        raise ValueError("notification state")
    settings = settings_patch(DEFAULT_SETTINGS, value["settings"])
    until = value["snoozed_until"]
    def timestamp(number):
        return type(number) in (int, float) and math.isfinite(number) and 0 <= number <= 253402300799
    if until is not None and not timestamp(until):
        raise ValueError("notification pause")
    events = value["events"]
    if not isinstance(events, list) or len(events) > MAX_EVENTS:
        raise ValueError("notification history")
    for row in events:
        if (not isinstance(row, dict) or set(row) != {"location", "start", "end", "reserved", "expires"}
                or not isinstance(row["location"], str) or not re.fullmatch(r"[a-f0-9]{64}", row["location"])
                or any(not timestamp(row[key]) for key in ("start", "end", "reserved", "expires"))
                or not row["start"] < row["end"] or row["end"] - row["start"] > 22 * 86400
                or row["expires"] != row["reserved"] + RETENTION):
            raise ValueError("notification event")
    return dict(schema_version=1, settings=settings, snoozed_until=until, events=deepcopy(events))


class PrecipitationWatcher:
    def __init__(self, state, *, now=None, notifier=None):
        self.state = state
        self.now = now or (lambda: datetime.now(timezone.utc))
        self.notifier = notifier if notifier is not None else Notifier()
        self.document = dict(schema_version=1, settings=dict(DEFAULT_SETTINGS), snoozed_until=None, events=[])
        self.lock = None
        self.problem = None
        self.mode, self.delivery = "off", "none"
        self.closing = False
        try:
            saved = self.state.read(STATE_FILE, STATE_LIMIT)
            if saved is not None:
                self.document = validate_document(saved)
            if self.enabled:
                self._acquire()
                # The previous owner may have reserved an event or disabled
                # watching between our first read and this lock handoff.
                saved = self.state.read(STATE_FILE, STATE_LIMIT)
                if saved is None:
                    raise ValueError("notification state disappeared")
                self.document = validate_document(saved)
                if not self.enabled:
                    self._release()
        except (OSError, ValueError, TypeError, KeyError):
            self.problem = "state_unavailable"
            self._release()

    @property
    def enabled(self):
        return self.document["settings"]["enabled"]

    def _acquire(self):
        if self.lock is not None:
            return
        fd = os.open(LOCK_FILE, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC,
                     0o600, dir_fd=self.state.fd)
        try:
            info = os.fstat(fd)
            if (not stat.S_ISREG(info.st_mode) or info.st_uid != os.geteuid()
                    or info.st_nlink != 1 or stat.S_IMODE(info.st_mode) != 0o600 or info.st_size != 0):
                raise PermissionError("unsafe watcher lock")
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            self.lock, fd = fd, None
        finally:
            if fd is not None:
                os.close(fd)

    def _release(self):
        if self.lock is not None:
            fd, self.lock = self.lock, None
            try:
                # A just-forked worker may not have closed its inherited copy
                # yet. Disable must release ownership immediately regardless.
                fcntl.flock(fd, fcntl.LOCK_UN)
            finally:
                os.close(fd)

    def _save(self, value):
        self.state.write(STATE_FILE, validate_document(value), STATE_LIMIT)
        self.document = value

    def configure(self, patch):
        if self.closing:
            raise ValueError("watcher closed")
        settings_patch(self.document["settings"], patch)
        if self.problem:
            raise ValueError("notification state unavailable")
        previous_enabled = self.enabled
        self._acquire()
        try:
            value = deepcopy(self.document)
            # Another manually launched watcher may have updated reservations
            # since this off instance was created. Merge from authoritative state.
            saved = self.state.read(STATE_FILE, STATE_LIMIT)
            if saved is not None:
                value = validate_document(saved)
            settings = settings_patch(value["settings"], patch)
            quiet_changed = any(settings[key] != value["settings"][key]
                                for key in ("quiet_enabled", "quiet_start", "quiet_end"))
            value["settings"] = settings
            self._save(value)
        except BaseException:
            if not previous_enabled:
                self._release()
            raise
        if not self.enabled or quiet_changed:
            # A settings response may do other bridge work before its next
            # tick. Cancel here so that work cannot delay a quiet-policy change.
            self.notifier.cancel()
        if not self.enabled:
            self._release()
        self.mode = "waiting" if self.enabled else "off"

    def snooze(self, resume=False):
        if not self.enabled or self.closing or self.problem or self.lock is None:
            raise ValueError("watcher unavailable")
        value = deepcopy(self.document)
        value["snoozed_until"] = None if resume else self.now().timestamp() + 3600
        self._save(value)
        if not resume:
            self.notifier.cancel()

    def tick(self, forecast, location, *, data_ok=True):
        now = self.now()
        try:
            data_ok = (data_ok and isinstance(forecast, dict)
                       and forecast.get("location") == location
                       and 0 <= (now - runtime.instant(forecast["fetched_at"])).total_seconds() <= runtime.STALE_SECONDS)
        except (ValueError, TypeError, KeyError):
            data_ok = False
        location_ok = isinstance(location, dict) and isinstance(location.get("timezone"), str)
        settings = self.document["settings"]
        quiet = False
        if self.enabled and settings["quiet_enabled"] and location_ok:
            try:
                hour = now.astimezone(ZoneInfo(location["timezone"])).hour
                start, end = settings["quiet_start"], settings["quiet_end"]
                quiet = start <= hour < end if start < end else hour >= start or hour < end
            except (ValueError, TypeError, KeyError):
                location_ok = False
        data_ok = data_ok and location_ok
        # Evaluate changed policy before polling: a pending helper must not
        # continue delivering while this watcher reports quiet hours.
        if not data_ok or quiet:
            self.notifier.cancel()
        try:
            completed = self.notifier.poll()
        except (OSError, RuntimeError):
            self.delivery, self.mode = "failed", "unavailable"
            return
        if completed is not None:
            self.delivery = completed
        if self.closing or not self.enabled:
            self.mode = "off"
            return
        if self.problem or self.lock is None:
            self.mode = "unavailable"
            return
        if not self.notifier.available:
            self.mode = "unavailable"
            return
        if not location_ok:
            self.mode = "waiting"
            return
        until = self.document["snoozed_until"]
        if until is not None and now.timestamp() < until:
            self.mode = "paused"
            return
        if quiet:
            self.mode = "quiet"
            return
        self.mode = "watching" if data_ok else "waiting"
        if not data_ok or self.notifier.process is not None:
            return
        try:
            event = candidate(forecast, location, now, settings["probability"])
            if event is None:
                if (forecast is None or not 0 <= (now - runtime.instant(forecast["fetched_at"])).total_seconds() <= runtime.STALE_SECONDS):
                    self.mode = "waiting"
                return
            value = deepcopy(self.document)
            value["events"] = [row for row in value["events"] if row["expires"] > now.timestamp()]
            for row in value["events"]:
                if row["location"] == event["location"] and (
                        (event["start"] <= row["end"] + REVISION_GAP and event["end"] >= row["start"] - REVISION_GAP)
                        or now.timestamp() - row["reserved"] < COOLDOWN):
                    before = (row["start"], row["end"])
                    row["start"] = min(row["start"], event["start"])
                    row["end"] = max(row["end"], event["end"])
                    if before != (row["start"], row["end"]):
                        self._save(value)
                    return
            if len(value["events"]) >= MAX_EVENTS:
                self.mode = "unavailable"  # Never evict an unexpired reservation to send more.
                return
            value["events"].append({key: event[key] for key in ("location", "start", "end")}
                                   | dict(reserved=now.timestamp(), expires=now.timestamp() + RETENTION))
            self._save(value)
            self.notifier.start(event["title"], event["body"])
            self.delivery = "pending"
        except (OSError, ValueError, TypeError, KeyError, RuntimeError):
            self.delivery = "failed"
            self.mode = "unavailable"

    def snapshot(self):
        return dict(settings=dict(self.document["settings"]), state=self.mode,
                    snoozed_until=self.document["snoozed_until"], delivery=self.delivery,
                    supported=self.notifier.available)

    def begin_close(self):
        self.closing = True
        self.notifier.cancel()

    def finish_close(self):
        try:
            self.notifier.finish_close()
        finally:
            self._release()
