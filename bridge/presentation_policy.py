#!/usr/bin/env python3
"""Finite read-only presentation heartbeat; no renderer/compositor coupling."""
import argparse
import math
import os
from pathlib import Path
import re
import signal
import time

from hyprland_geometry import Publisher, log, request

STALE_MS = 1500
REASONS = frozenset({"none", "invalid_metadata", "output_missing", "output_ambiguous",
                     "output_disabled", "power_unknown", "dpms_off", "fullscreen",
                     "session_lock", "lock_unknown", "ipc_error", "producer_stopped"})


def validate_selector(instance, output):
    if not isinstance(instance, str) or not re.fullmatch(r"[a-f0-9]+_[0-9]+_[0-9]+", instance):
        raise ValueError("explicit compositor instance required")
    if not isinstance(output, str) or not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", output):
        raise ValueError("explicit output connector required")


def decision(monitors, clients, output, lock_status=None):
    """Strict metadata-only policy; unknown lock state never grants rendering."""
    if not isinstance(monitors, list) or len(monitors) > 64 or not isinstance(clients, list) or len(clients) > 1024:
        return "invalid_metadata"
    if any(not isinstance(m, dict) for m in monitors) or any(not isinstance(c, dict) for c in clients):
        return "invalid_metadata"
    selected = [m for m in monitors if m.get("name") == output]
    if len(selected) != 1:
        return "output_missing" if not selected else "output_ambiguous"
    monitor = selected[0]
    if type(monitor.get("id")) is not int:
        return "invalid_metadata"
    if monitor.get("disabled") is True:
        return "output_disabled"
    if monitor.get("disabled") is not False or type(monitor.get("dpmsStatus")) is not bool:
        return "power_unknown"
    if not monitor["dpmsStatus"]:
        return "dpms_off"
    workspace = monitor.get("activeWorkspace")
    special = monitor.get("specialWorkspace")
    if not isinstance(workspace, dict) or type(workspace.get("id")) is not int:
        return "invalid_metadata"
    visible_workspaces = {workspace["id"]}
    if isinstance(special, dict) and type(special.get("id")) is int and special["id"] != 0:
        visible_workspaces.add(special["id"])
    for client in clients:
        if type(client.get("monitor")) is not int:
            return "invalid_metadata"
        if client.get("monitor") != monitor["id"]:
            continue
        if any(type(client.get(k)) is not bool for k in ("mapped", "hidden", "pinned")):
            return "invalid_metadata"
        if not client["mapped"] or client["hidden"]:
            continue
        ws = client.get("workspace")
        if (not isinstance(ws, dict) or type(ws.get("id")) is not int
                or type(client.get("fullscreen")) is not int or client["fullscreen"] not in (0, 1, 2)):
            return "invalid_metadata"
        if (client["pinned"] or ws["id"] in visible_workspaces) and client["fullscreen"] == 2:
            return "fullscreen"
    # Existing plugin status is optional evidence, never an enable/load request.
    if isinstance(lock_status, dict) and lock_status.get("enabled") is True:
        # New native sessions expose lock evidence independently of visual
        # suppression (e.g. reduced motion). Partial/malformed new evidence
        # must not fall back to a less precise legacy suppression string.
        if "lock_state_known" in lock_status or "session_locked" in lock_status:
            if (lock_status.get("lock_state_known") is not True
                    or type(lock_status.get("session_locked")) is not bool
                    or lock_status.get("cleanup_failed")):
                return "lock_unknown"
            return "session_lock" if lock_status["session_locked"] else "none"
        if lock_status.get("suppression") == "session_lock":
            return "session_lock"
        if lock_status.get("suppression") == "none" and not lock_status.get("cleanup_failed"):
            return "none"
    return "lock_unknown"


def envelope(instance, output, sequence, reason, now_ms):
    return dict(schema_version=1, session=instance, output=output, sequence=sequence,
                generated_at_unix_ms=now_ms, stale_after_ms=STALE_MS,
                render_allowed=reason == "none", reason=reason)


def fresh(snapshot, instance, output, now_ms):
    """Consumer selector/freshness helper; every mismatch fails closed."""
    try:
        return (type(snapshot["schema_version"]) is int and snapshot["schema_version"] == 1
                and snapshot["session"] == instance and snapshot["output"] == output
                and type(snapshot["sequence"]) is int and snapshot["sequence"] >= 0
                and type(snapshot["generated_at_unix_ms"]) is int
                and snapshot["stale_after_ms"] == STALE_MS
                and type(snapshot["stale_after_ms"]) is int
                and -500 <= now_ms - snapshot["generated_at_unix_ms"] <= STALE_MS
                and type(snapshot["render_allowed"]) is bool
                and isinstance(snapshot["reason"], str) and snapshot["reason"] in REASONS
                and snapshot["render_allowed"] == (snapshot["reason"] == "none"))
    except (KeyError, TypeError):
        return False


def run(args, *, requester=request, publisher=None, clock=time.monotonic,
        wall=time.time, sleep=time.sleep):
    validate_selector(args.instance, args.output)
    if not 1 <= args.duration <= 300 or not math.isfinite(args.rate) or not 1 <= args.rate <= 2:
        raise ValueError("duration must be 1..300 and rate 1..2Hz")
    runtime = os.environ.get("XDG_RUNTIME_DIR")
    if not runtime or not Path(runtime).is_absolute():
        raise ValueError("absolute XDG_RUNTIME_DIR required")
    path = Path(runtime) / "hypr" / args.instance / ".socket.sock"
    publisher = publisher or Publisher(args.output_file, False)
    stopped, sequence = False, 0
    previous = {}

    def stop(_signum, _frame):
        nonlocal stopped
        stopped = True

    deadline = clock() + args.duration

    def renew(_signum, _frame):
        nonlocal deadline
        if clock() < deadline:
            deadline = clock() + args.duration

    def publish(reason):
        nonlocal sequence
        sequence += 1
        publisher.publish(envelope(args.instance, args.output, sequence, reason, int(wall() * 1000)))

    try:
        for signum in (signal.SIGINT, signal.SIGTERM):
            previous[signum] = signal.signal(signum, stop)
        if getattr(args, "renewable_lease", False):
            previous[signal.SIGUSR1] = signal.signal(signal.SIGUSR1, renew)
        next_tick = clock()
        while not stopped and clock() < deadline:
            if clock() >= next_tick:
                try:
                    monitors = requester(path, "j/monitors all")
                    clients = requester(path, "j/clients")
                    lock = requester(path, "a-weather-app:rain status") if args.native_lock_status else None
                    reason = decision(monitors, clients, args.output, lock)
                except (OSError, ValueError, TimeoutError) as error:
                    reason = "ipc_error"
                    log("presentation_policy_error", error=type(error).__name__)
                publish(reason)
                next_tick = clock() + 1 / args.rate
            sleep(min(.1, max(0, deadline - clock()), max(0, next_tick - clock())))
    finally:
        try:
            publish("producer_stopped")
        finally:
            for signum, handler in previous.items():
                signal.signal(signum, handler)
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--instance", required=True)
    parser.add_argument("--output", required=True, help="connector name, not monitor index")
    parser.add_argument("--output-file", required=True)
    parser.add_argument("--duration", type=int, default=30)
    parser.add_argument("--renewable-lease", action="store_true", help="allow owner SIGUSR1 to renew finite duration")
    parser.add_argument("--rate", type=float, default=1)
    parser.add_argument("--native-lock-status", action="store_true", help="read lock evidence from an already enabled native session")
    args = parser.parse_args()
    try:
        return run(args)
    except (OSError, ValueError) as error:
        parser.error(str(error))


if __name__ == "__main__":
    raise SystemExit(main())
