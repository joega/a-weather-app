"""Independent effects owner. Private stdin EOF means owner died.

Not a daemon or public IPC endpoint; started only by an explicit user action.
An acknowledged session exports exact ownership for guarded parent recovery.
Worker SIGKILL before that acknowledgement remains an explicit uncertainty:
the parent must not adopt foreign state; autonomous native/host expiry still bounds
already enabled effects to the current finite lease. Live leases renew only while
the private owner heartbeat remains healthy; previews keep their original expiry.
"""
import json
import os
from pathlib import Path
import select
import signal
import sys
import time

if __package__ in (None, ""):
    sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ui.effects import EffectsSupervisor

REQUEST_LIMIT = 8192
REPLY_LIMIT = 32768
OWNER_UPDATE_TIMEOUT = 8.0


def ownership(supervisor):
    backend = supervisor.backend
    return dict(instance=backend.instance, pid=backend.pid, starttime=backend.starttime,
                plugin_inventory=backend.plugin_inventory, generation=supervisor.generation)


def serve(supervisor_factory=EffectsSupervisor, input_fd=0, output_fd=1, *, owner_update_timeout=OWNER_UPDATE_TIMEOUT):
    supervisor = supervisor_factory()
    pending = bytearray()
    weather = controls = None
    next_tick = time.monotonic() + .5
    last_owner_update = time.monotonic()
    stopped = False
    def terminate(_signal, _frame):
        nonlocal stopped
        stopped = True
    def deadline(_signal, _frame):
        raise TimeoutError("effects worker operation deadline")
    previous = signal.signal(signal.SIGALRM, deadline)
    previous_termination = {number: signal.signal(number, terminate) for number in (signal.SIGTERM, signal.SIGINT)}
    def bounded_stop():
        signal.setitimer(signal.ITIMER_REAL, 25)
        try:
            return supervisor.stop()
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
    def reply(value):
        encoded = json.dumps(value, allow_nan=False).encode() + b"\n"
        if len(encoded) > REPLY_LIMIT:
            raise ValueError("effects worker reply limit")
        # Private pipe writer cannot block indefinitely if the bridge is hung.
        signal.setitimer(signal.ITIMER_REAL, 3)
        try:
            view = memoryview(encoded)
            while view:
                written = os.write(output_fd, view)
                if written <= 0:
                    raise OSError("effects reply short write")
                view = view[written:]
        finally:
            signal.setitimer(signal.ITIMER_REAL, 0)
    try:
        while not stopped:
            ready = select.select([input_fd], [], [], max(0, next_tick - time.monotonic()))[0]
            if time.monotonic() >= next_tick:
                if supervisor.status()["state"] == "running":
                    signal.setitimer(signal.ITIMER_REAL, 25)
                    try:
                        if time.monotonic() - last_owner_update >= owner_update_timeout:
                            supervisor.stop()
                        elif weather is not None:
                            supervisor.tick(weather, controls)
                        elif time.monotonic() >= supervisor.deadline or not supervisor.backend.children_alive():
                            supervisor.stop()
                    finally:
                        signal.setitimer(signal.ITIMER_REAL, 0)
                next_tick = time.monotonic() + .5
            if not ready:
                continue
            chunk = os.read(input_fd, min(4096, REQUEST_LIMIT + 1 - len(pending)))
            if not chunk:
                return 0 if bounded_stop()["state"] == "stopped" else 1
            pending.extend(chunk)
            if len(pending) > REQUEST_LIMIT:
                raise ValueError("effects request byte limit")
            while b"\n" in pending:
                line, _, remainder = pending.partition(b"\n")
                pending = bytearray(remainder)
                request = json.loads(line)
                if not isinstance(request, dict) or type(request.get("id")) is not int:
                    raise ValueError("effects request schema")
                operation = request.get("op")
                signal.setitimer(signal.ITIMER_REAL, 25)
                try:
                    if operation == "start_live" and set(request) == {"id", "op", "instance", "output", "controls"}:
                        controls = request["controls"]
                        status = supervisor.start_live(request["instance"], request["output"], controls)
                        last_owner_update = time.monotonic()
                        value = dict(id=request["id"], ok=True, status=status, ownership=ownership(supervisor))
                    elif operation == "start" and set(request) == {"id", "op", "duration", "instance", "output", "controls"}:
                        controls = request["controls"]
                        status = supervisor.start(request["duration"], request["instance"], request["output"], controls)
                        last_owner_update = time.monotonic()
                        value = dict(id=request["id"], ok=True, status=status, ownership=ownership(supervisor))
                    elif operation == "tick" and set(request) == {"id", "op", "weather", "controls"}:
                        weather, controls = request["weather"], request["controls"]
                        last_owner_update = time.monotonic()
                        value = dict(id=request["id"], ok=True, status=supervisor.tick(weather, controls))
                    elif operation in ("status", "stop") and set(request) == {"id", "op"}:
                        status = supervisor.stop() if operation == "stop" else supervisor.status()
                        value = dict(id=request["id"], ok=True, status=status)
                    else:
                        raise ValueError("effects operation schema")
                except Exception as error:
                    value = dict(id=request["id"], ok=False, error=str(error)[:512], status=supervisor.status())
                finally:
                    signal.setitimer(signal.ITIMER_REAL, 0)
                reply(value)
                if operation == "stop":
                    return 0 if value["status"]["state"] == "stopped" else 1
        return 0 if bounded_stop()["state"] == "stopped" else 1
    except Exception:
        bounded_stop()
        return 1
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)
        for number, handler in previous_termination.items():
            signal.signal(number, handler)


if __name__ == "__main__":
    raise SystemExit(serve())
