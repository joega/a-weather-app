#!/usr/bin/env python3
"""Read-only, bounded Hyprland geometry stream; Python standard library only."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import selectors
import signal
import socket
import sys
import tempfile
import time
import uuid

MAX_REPLY = 4 * 1024 * 1024
MAX_RECORDS = 1024
MAX_MONITORS = 64
MAX_EVENT_BUFFER = 65536


def number(value, default=None):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return default
    return value if math.isfinite(value) and abs(value) <= 10000000 else default


def integer(value, default=None):
    value = number(value)
    return int(value) if value is not None and value == int(value) else default


def workspace(value):
    return integer(value.get("id")) if isinstance(value, dict) else None


def pair(value):
    if not isinstance(value, list) or len(value) != 2:
        return None
    result = [number(x) for x in value]
    return result if None not in result else None


def merge_current_geometry(clients, geometry):
    """Require a complete mapped-client sample; never fall back to target data."""
    if (not isinstance(clients, list) or not isinstance(geometry, dict) or "error" in geometry or
            type(geometry.get("schema_version")) is not int or geometry["schema_version"] != 1 or
            not isinstance(geometry.get("windows"), list) or
            len(clients) > MAX_RECORDS or len(geometry["windows"]) > MAX_RECORDS):
        raise ValueError("invalid current geometry schema")
    addresses = set()
    mapped = set()
    for raw in clients:
        if not isinstance(raw, dict):
            raise ValueError("invalid client record")
        address = raw.get("address")
        if not isinstance(address, str) or not address.startswith("0x") or len(address) > 128 or address in addresses:
            raise ValueError("invalid or duplicate client address")
        addresses.add(address)
        if raw.get("mapped") is True:
            mapped.add(address)
    samples = {}
    by_address = {raw["address"]: raw for raw in clients}
    for raw in geometry["windows"]:
        if not isinstance(raw, dict):
            raise ValueError("invalid current geometry record")
        address = raw.get("address")
        if not isinstance(address, str) or address not in mapped or address in samples:
            raise ValueError("unexpected or duplicate geometry address")
        client_stable = by_address[address].get("stableId")
        sample_stable = raw.get("stableId")
        if client_stable is not None and sample_stable is not None and str(client_stable) != str(sample_stable):
            raise ValueError("current geometry identity mismatch")
        fields = {name: pair(raw.get(name)) for name in ("at", "size", "goal_at", "goal_size")}
        if any(value is None for value in fields.values()) or min(fields["size"]) <= 0 or min(fields["goal_size"]) <= 0:
            raise ValueError("invalid current geometry coordinates")
        samples[address] = fields
    if set(samples) != mapped:
        raise ValueError("incomplete current geometry sample")
    return [{**raw, **({"at": samples[raw["address"]]["at"],
                         "size": samples[raw["address"]]["size"]} if raw["address"] in samples else {})}
            for raw in clients]


class Normalizer:
    """IDs survive snapshots, but an address reused after closing gets a new ID."""
    def __init__(self, session, current_geometry=False):
        self.current_geometry = current_geometry
        self.session = hashlib.sha256(session.encode()).hexdigest()[:16]
        self.identities = {}
        self.next_identity = 0
        self.provider_nonce = uuid.uuid4().hex[:12]
        self.address_keys = {}

    def close_address(self, address):
        key = self.address_keys.pop(address, None)
        if key is not None:
            self.identities.pop(key, None)

    def empty(self):
        return self.envelope(False, [], [], [], 0)

    def envelope(self, connected, monitors, windows, suppressed, invalid):
        return {"schema_version": 1, "session": self.session,
                "generated_at_unix_ms": int(time.time() * 1000),
                "connected": connected, "coordinate_space": "global_logical",
                "geometry_kind": "animation_current" if self.current_geometry else "animation_target",
                "exact_animated_geometry": self.current_geometry,
                "stale_after_ms": 1000,
                "fullscreen_policy": "suppress_monitor_for_visible_mode_2",
                "monitors": monitors, "windows": windows,
                "suppressed_monitors": suppressed, "invalid_records": invalid}

    def normalize(self, clients, outputs, active):
        if not isinstance(clients, list) or not isinstance(outputs, list) or not isinstance(active, dict):
            raise ValueError("unexpected IPC JSON shape")
        if len(clients) > MAX_RECORDS or len(outputs) > MAX_MONITORS:
            # Fail closed: truncating occluders could invent exposed supports.
            raise ValueError("IPC record limit exceeded")
        monitors = []
        monitor_map = {}
        invalid = 0
        for raw in outputs:
            if not isinstance(raw, dict):
                invalid += 1
                continue
            mid = integer(raw.get("id"))
            x, y = number(raw.get("x")), number(raw.get("y"))
            width, height = number(raw.get("width")), number(raw.get("height"))
            scale = number(raw.get("scale"))
            transform = integer(raw.get("transform"), 0)
            if (None in (mid, x, y, width, height, scale) or scale <= 0 or
                    width <= 0 or height <= 0 or transform not in range(8) or mid in monitor_map):
                invalid += 1
                continue
            if transform % 2:
                width, height = height, width
            reserved = raw.get("reserved", [])
            if not isinstance(reserved, list) or len(reserved) != 4 or any(number(v) is None for v in reserved):
                reserved = [0, 0, 0, 0]
            monitor = {"id": mid, "rect": {"x": x, "y": y, "w": width / scale, "h": height / scale},
                       "scale": scale, "transform": transform, "reserved": reserved,
                       "active_workspace": workspace(raw.get("activeWorkspace")),
                       "special_workspace": workspace(raw.get("specialWorkspace")),
                       "disabled": raw.get("disabled") is True}
            monitor_map[mid] = monitor
            monitors.append(monitor)
        windows = []
        live_keys = set()
        active_address = active.get("address")
        for source_order, raw in enumerate(clients):
            if not isinstance(raw, dict):
                invalid += 1
                continue
            pos, size = pair(raw.get("at")), pair(raw.get("size"))
            mid = integer(raw.get("monitor"))
            address = raw.get("address")
            stable = raw.get("stableId")
            if not isinstance(address, str) or len(address) > 128 or not address.startswith("0x"):
                invalid += 1
                continue
            if pos is None or size is None or min(size) <= 0 or mid not in monitor_map:
                invalid += 1
                continue
            stable_valid = isinstance(stable, (str, int)) and not isinstance(stable, bool) and len(str(stable)) <= 128
            key = ("stable", str(stable)) if stable_valid else ("address", address)
            if key in live_keys:
                invalid += 1
                continue
            live_keys.add(key)
            if key not in self.identities:
                self.next_identity += 1
                if stable_valid:
                    stable_hash = hashlib.sha256(str(stable).encode()).hexdigest()[:24]
                    self.identities[key] = f"{self.session}:stable:{stable_hash}"
                else:
                    self.identities[key] = f"{self.session}:fallback:{self.provider_nonce}:{self.next_identity}"
            self.address_keys[address] = key
            monitor = monitor_map[mid]
            wid = workspace(raw.get("workspace"))
            pinned = raw.get("pinned") is True
            floating = raw.get("floating") is True
            special = wid is not None and wid < 0 and wid == monitor["special_workspace"]
            visible = (raw.get("mapped") is True and raw.get("hidden") is not True and
                       raw.get("visible") is not False and not monitor["disabled"] and
                       (pinned or (wid is not None and wid == monitor["active_workspace"]) or special))
            mode = integer(raw.get("fullscreen"), 0)
            if mode not in (0, 1, 2):
                mode = 0
            # Renderer passes from pinned Hyprland 0.56.2 source. Preserve
            # source array order as within-pass evidence; focus history is not z.
            render_pass = 3 if pinned and floating else 2 if special else 1 if floating else 0
            windows.append({"id": self.identities[key],
                            "rect": dict(zip(("x", "y", "w", "h"), pos + size)),
                            "workspace": wid, "monitor": mid, "visible": visible,
                            "fullscreen": mode == 2, "fullscreen_mode": mode,
                            "floating": floating, "pinned": pinned,
                            "source_order": source_order, "render_pass": render_pass,
                            "active_tile": not floating and address == active_address})
        self.identities = {key: value for key, value in self.identities.items() if key in live_keys}
        self.address_keys = {address: key for address, key in self.address_keys.items() if key in live_keys}
        if invalid:
            # An invalid record could be an occluder. Do not invent exposed
            # edges by merely dropping it while publishing remaining supports.
            return self.envelope(False, [], [], [], invalid)
        suppressed = sorted({w["monitor"] for w in windows if w["visible"] and w["fullscreen"]})
        # Fullscreen renderer exceptions (over-fullscreen permissions, fades)
        # aren't fully exposed through IPC. Fail closed for whole output.
        for mid in monitor_map:
            ranked = sorted((w for w in windows if w["monitor"] == mid),
                            key=lambda w: (w["render_pass"], w["active_tile"] if w["render_pass"] == 0 else False, w["source_order"]))
            for rank, w in enumerate(ranked):
                w["stacking_rank"] = rank
                w["effects_suppressed"] = mid in suppressed
        return self.envelope(True, monitors, windows, suppressed, invalid)


class EventFramer:
    def __init__(self):
        self.buffer = b""
        self.closed_addresses = []

    def feed(self, data):
        self.buffer += data
        if len(self.buffer) > MAX_EVENT_BUFFER:
            self.buffer = b""
            raise ValueError("event buffer limit exceeded")
        lines = self.buffer.split(b"\n")
        self.buffer = lines.pop()
        self.closed_addresses = []
        for line in lines:
            if line.startswith(b"closewindow>>"):
                value = line.partition(b">>")[2].strip()
                if len(value) <= 128:
                    try:
                        address = value.decode("ascii")
                        if not address.startswith("0x"):
                            address = "0x" + address
                        self.closed_addresses.append(address)
                    except UnicodeDecodeError:
                        pass
        # We only need invalidation; never log event payload (may contain titles).
        return any(b">>" in line for line in lines)


def request(path, command, timeout=0.35):
    deadline = time.monotonic() + timeout
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as sock:
        sock.settimeout(timeout)
        sock.connect(str(path))
        sock.sendall(command.encode("ascii"))
        chunks = []
        total = 0
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError("IPC reply deadline")
            sock.settimeout(remaining)
            chunk = sock.recv(65536)
            if not chunk:
                break
            total += len(chunk)
            if total > MAX_REPLY:
                raise ValueError("IPC reply limit exceeded")
            chunks.append(chunk)
    return json.loads(b"".join(chunks))


def log(event, **fields):
    print(json.dumps({"event": event, **fields}, separators=(",", ":")), file=sys.stderr, flush=True)


class Publisher:
    """Atomic latest file and single bounded latest stdout message."""
    def __init__(self, output_file=None, stdout=True):
        self.path = Path(output_file) if output_file else None
        self.stdout = stdout
        self.pending = b""
        self.offset = 0
        self.latest = None
        if stdout:
            os.set_blocking(sys.stdout.fileno(), False)

    def publish(self, snapshot):
        data = (json.dumps(snapshot, separators=(",", ":"), allow_nan=False) + "\n").encode()
        if self.path:
            fd, temp = tempfile.mkstemp(prefix=".geometry-", dir=self.path.parent)
            try:
                with os.fdopen(fd, "wb") as stream:
                    stream.write(data)
                os.replace(temp, self.path)
            finally:
                if os.path.exists(temp):
                    os.unlink(temp)
        if self.stdout:
            # A partially written line must finish to preserve framing, then
            # only the latest snapshot is kept under consumer backpressure.
            if self.offset:
                self.latest = data
            else:
                self.pending, self.offset = data, 0
            self.flush()

    def flush(self):
        if not self.stdout or not self.pending:
            return
        try:
            self.offset += os.write(sys.stdout.fileno(), self.pending[self.offset:])
        except BlockingIOError:
            return
        except BrokenPipeError:
            self.stdout = False
            self.pending, self.latest = b"", None
            return
        if self.offset == len(self.pending):
            self.pending, self.offset = self.latest or b"", 0
            self.latest = None

    def drain(self, timeout=0.2):
        deadline = time.monotonic() + timeout
        while self.pending and time.monotonic() < deadline:
            self.flush()
            if self.pending:
                time.sleep(0.005)
        return not self.pending


def run(args):
    signature = os.environ.get("HYPRLAND_INSTANCE_SIGNATURE", "")
    runtime = os.environ.get("XDG_RUNTIME_DIR", "")
    current_geometry = getattr(args, "current_geometry", False)
    normalizer = Normalizer(signature or "unavailable", current_geometry)
    publisher = Publisher(args.output_file, not args.no_stdout)
    if not signature or not runtime or "/" in signature or signature in (".", ".."):
        publisher.publish(normalizer.empty())
        log("hyprland_unavailable", reason="missing_or_invalid_session_environment")
        return 1
    directory = Path(runtime) / "hypr" / signature
    command_path, event_path = directory / ".socket.sock", directory / ".socket2.sock"
    selector = selectors.DefaultSelector()
    event_socket = None
    framer = EventFramer()
    started = time.monotonic()
    next_connect = next_query = started
    last_query = started - 1
    connected = False
    dirty = True
    sequence = 0
    interval = 1 / args.rate
    stop_requested = False
    previous_sigterm = signal.getsignal(signal.SIGTERM)

    def request_stop(_signum, _frame):
        nonlocal stop_requested
        stop_requested = True

    signal.signal(signal.SIGTERM, request_stop)
    log("provider_start", reconciliation_hz=args.rate,
        geometry_kind="animation_current" if current_geometry else "animation_target",
        exact_animated_geometry=current_geometry, fullscreen_policy="suppress_monitor_for_visible_mode_2")

    def disconnect(reason):
        nonlocal event_socket, connected, next_connect, framer
        if event_socket:
            selector.unregister(event_socket)
            event_socket.close()
            event_socket = None
        framer = EventFramer()
        connected = False
        publisher.publish(normalizer.empty())
        next_connect = time.monotonic() + 0.5
        log("hyprland_disconnected", reason=reason)

    try:
        while not stop_requested and (args.duration is None or time.monotonic() - started < args.duration):
            now = time.monotonic()
            if event_socket is None and now >= next_connect:
                try:
                    event_socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                    event_socket.settimeout(0.2)
                    event_socket.connect(str(event_path))
                    event_socket.setblocking(False)
                    selector.register(event_socket, selectors.EVENT_READ)
                    connected, dirty = True, True
                    log("hyprland_connected")
                except OSError:
                    if event_socket:
                        event_socket.close()
                        event_socket = None
                    disconnect("event_socket_unavailable")
                    if args.once:
                        return 1
            if connected and (now >= next_query or (dirty and now - last_query >= 1 / (60 if current_geometry else 30))):
                query_start = time.monotonic()
                last_query = query_start
                try:
                    # No subprocesses; short-lived command connections only.
                    clients = request(command_path, "j/clients")
                    monitors = request(command_path, "j/monitors")
                    active = request(command_path, "j/activewindow")
                    if current_geometry:
                        clients = merge_current_geometry(clients, request(command_path, "j/a-weather-app:geometry"))
                    snapshot = normalizer.normalize(clients, monitors, active)
                    sequence += 1
                    snapshot["sequence"] = sequence
                    publisher.publish(snapshot)
                    log("geometry_snapshot", sequence=sequence, windows=len(snapshot["windows"]),
                        visible=sum(w["visible"] for w in snapshot["windows"]), invalid=snapshot["invalid_records"],
                        reason="event" if dirty else "reconciliation",
                        query_ms=round((time.monotonic() - query_start) * 1000, 2))
                    dirty = False
                    if args.once:
                        if not publisher.drain():
                            log("stdout_backpressure", reason="diagnostic_drain_deadline")
                            return 1
                        return 0 if snapshot["connected"] else 1
                except (OSError, ValueError, TypeError):
                    disconnect("snapshot_failed")
                    if args.once:
                        return 1
                next_query = time.monotonic() + interval
            publisher.flush()
            timeout = min(0.02, max(0, next_query - time.monotonic())) if connected else 0.02
            for key, _ in selector.select(timeout):
                # Fixed read budget prevents continuous event floods starving
                # reconciliation or finite duration/shutdown.
                try:
                    for _ in range(4):
                        data = key.fileobj.recv(16384)
                        if not data:
                            disconnect("event_eof")
                            break
                        dirty = framer.feed(data) or dirty
                        for address in framer.closed_addresses:
                            normalizer.close_address(address)
                except BlockingIOError:
                    pass
                except (OSError, ValueError):
                    disconnect("event_read_failed")
    except KeyboardInterrupt:
        log("provider_interrupted")
    finally:
        signal.signal(signal.SIGTERM, previous_sigterm)
        if event_socket:
            selector.unregister(event_socket)
            event_socket.close()
        selector.close()
        # Once is a diagnostic capture. Continuous mode clears retained file
        # geometry on exit, including duration expiry and interrupts.
        if not args.once:
            publisher.publish(normalizer.empty())
        publisher.drain()
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--once", action="store_true", help="one snapshot then exit")
    parser.add_argument("--duration", type=float, help="finite runtime in seconds")
    parser.add_argument("--current-geometry", action="store_true", help="require compositor plugin current geometry")
    parser.add_argument("--rate", type=float, default=20, help="reconciliation Hz (1..30; current geometry 1..60)")
    parser.add_argument("--output-file", help="atomic latest JSON snapshot (parent must exist)")
    parser.add_argument("--no-stdout", action="store_true")
    args = parser.parse_args()
    max_rate = 60 if args.current_geometry else 30
    if not math.isfinite(args.rate) or not 1 <= args.rate <= max_rate:
        parser.error(f"rate must be finite and between 1 and {max_rate}")
    if args.duration is not None and (not math.isfinite(args.duration) or args.duration <= 0):
        parser.error("duration must be finite and positive")
    if args.no_stdout and not args.output_file:
        parser.error("--no-stdout requires --output-file")
    try:
        return run(args)
    except OSError:
        log("publisher_failed", reason="output_io_error")
        return 1


if __name__ == "__main__":
    sys.exit(main())
