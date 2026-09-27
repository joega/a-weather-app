"""Finite weather polling service. Native effects must already be enabled."""
import argparse
from datetime import datetime, timezone
import json
import math
import os
from pathlib import Path
import signal
import time

from . import native, runtime
from .mapping import visual_targets
from .provider import DEFAULT_LOCATION


class Service:
    def __init__(self, cache, output, controls, *, location=DEFAULT_LOCATION,
                 loader=runtime.load_live, writer=runtime.write_cache,
                 instance=None, caller=native.request):
        self.cache, self.output, self.controls = cache, output, Path(controls)
        self.location, self.loader, self.writer = location, loader, writer
        self.snapshot = runtime.read_cache(cache, location)
        self.config = {"mode": "live", "strength": "subtle", "manual": {"condition": "unknown"}}
        self.next_fetch, self.backoff, self.error = 0.0, 30.0, None
        self.instance, self.caller = instance, caller
        self.last_targets, self.saved = None, None
        if instance is not None:
            status = caller(instance, "status")
            if status.get("enabled") is not True:
                raise RuntimeError("native rain must already be enabled explicitly")
            self.saved = {key: status["target_parameters"][key] for key in ("rain_intensity", "wind_x")}
            if isinstance(status.get("snow_target_parameters"), dict):
                self.saved["snow_intensity"] = status["snow_target_parameters"]["intensity"]
            for key, value in self.saved.items():
                low, high = (-500, 500) if key == "wind_x" else (0, 1)
                if isinstance(value, bool) or not isinstance(value, (int, float)) or not low <= value <= high:
                    raise RuntimeError("invalid native parameter snapshot")

    def read_controls(self):
        try:
            if not self.controls.exists():
                return None
            with self.controls.open("rb") as stream:
                raw = stream.read(65537)
            if len(raw) > 65536:
                raise ValueError("controls too large")
            value = json.loads(raw, parse_constant=lambda s: (_ for _ in ()).throw(ValueError(s)))
            if not isinstance(value, dict):
                raise ValueError("controls must be an object")
            candidate = {"mode": value.get("mode", "live"), "strength": value.get("strength", "subtle"),
                         "manual": value.get("manual", {"condition": "unknown"}),
                         "reduced_motion": value.get("reduced_motion", False),
                         "lightning_enabled": value.get("lightning_enabled", False)}
            if candidate["mode"] not in ("live", "manual"):
                raise ValueError("mode must be live or manual")
            visual_targets(candidate["manual"], candidate["strength"])
            for key in ("reduced_motion", "lightning_enabled"):
                if not isinstance(candidate[key], bool):
                    raise ValueError("accessibility controls must be boolean")
            runtime.select_weather(None, mode="manual", manual=candidate["manual"],
                                   strength=candidate["strength"])
            self.config = candidate
            return None
        except (OSError, ValueError, TypeError) as error:
            return str(error)[:300]

    def step(self, now, monotonic):
        controls_error = self.read_controls()
        if self.config["mode"] == "live" and monotonic >= self.next_fetch:
            try:
                snapshot, error = self.loader(self.cache, self.location, now=now)
                if snapshot is not None:
                    self.snapshot = snapshot
                self.error = error
            except Exception as error:
                self.error = str(error)[:300]
            if self.error:
                self.next_fetch = monotonic + self.backoff
                self.backoff = min(900.0, self.backoff * 2)
            else:
                self.backoff = 30.0
                age = (now - runtime.instant(self.snapshot["fetched_at"])).total_seconds() if self.snapshot else 0
                self.next_fetch = monotonic + max(1, runtime.REFRESH_SECONDS - max(0, age))
        selected = runtime.select_weather(self.snapshot, now=now, error=self.error, **self.config)
        selected.update(location=self.location, service_pid=os.getpid(), controls_error=controls_error)
        targets = {key: selected["effects"][key] for key in ("rain_intensity", "wind_x", "snow_intensity")}
        if self.instance is not None and targets != self.last_targets:
            try:
                selected["native"] = native.apply_targets(self.instance, targets, caller=self.caller)
                self.last_targets = targets
            except Exception as error:
                selected["native_error"] = str(error)[:300]
        self.writer(self.output, selected)
        return selected

    def close(self):
        if self.saved is not None:
            # Restore only owned target values; never disable the plugin.
            errors = []
            for key, value in self.saved.items():
                try:
                    command = f"snow intensity {value:.9g}" if key == "snow_intensity" else f"set {key} {value:.9g}"
                    response = self.caller(self.instance, command)
                    if not isinstance(response, dict) or response.get("error"):
                        raise RuntimeError(f"native restore response rejected: {response}")
                except Exception as error:
                    errors.append(str(error))
            try:
                status = self.caller(self.instance, "status")
                if not isinstance(status, dict) or status.get("error") or status.get("enabled") is not True:
                    raise RuntimeError("native restore verification requires an enabled session")
                actual = dict(status["target_parameters"])
                if "snow_intensity" in self.saved:
                    actual["snow_intensity"] = status["snow_target_parameters"]["intensity"]
                for key, value in self.saved.items():
                    restored = actual.get(key)
                    if isinstance(restored, bool) or not isinstance(restored, (int, float)) or not math.isfinite(restored) or not math.isclose(restored, value, rel_tol=1e-5, abs_tol=1e-6):
                        raise RuntimeError(f"native restore target verification failed: {key}")
            except Exception as error:
                errors.append(str(error))
            if errors:
                raise RuntimeError("native restore failed: " + "; ".join(errors))
            self.saved = None


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for key in ("cache", "output", "controls"):
        parser.add_argument("--" + key, required=True)
    parser.add_argument("--duration", type=int, default=60)
    parser.add_argument("--instance")
    args = parser.parse_args(argv)
    if not 1 <= args.duration <= 300:
        parser.error("duration must be between 1 and 300 seconds")
    paths = [Path(getattr(args, key)).resolve() for key in ("cache", "output", "controls")]
    if len(set(paths)) != 3:
        parser.error("cache, output and controls must be different paths")
    service = Service(args.cache, args.output, args.controls, instance=args.instance)
    deadline = time.monotonic() + args.duration
    stopped = False
    previous_handlers = {}
    def stop(signum, frame):
        nonlocal stopped
        # Finish an in-flight native update before restoring its owned targets.
        stopped = True
    try:
        for signum in (signal.SIGTERM, signal.SIGINT):
            previous_handlers[signum] = signal.signal(signum, stop)
        while not stopped and time.monotonic() < deadline:
            service.step(datetime.now(timezone.utc), time.monotonic())
            if not stopped:
                time.sleep(min(1.0, max(0.0, deadline - time.monotonic())))
    finally:
        try:
            service.close()
        finally:
            for signum, handler in previous_handlers.items():
                signal.signal(signum, handler)


if __name__ == "__main__":
    main()
