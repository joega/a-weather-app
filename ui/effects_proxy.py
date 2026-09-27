"""Bridge-facing independent effects owner; construction performs no I/O.

Each public action shares one 45s monotonic budget (RPC <=28s, EOF wait <=5s,
all fallback CLI calls honor remaining time). Bounded inventory and reap work
can add at most 8s. The shared guardian/bridge envelope also accounts for an
interrupted action and forecast-worker cleanup. Metadata reads are
descriptor/byte bounded. Neither recovery nor retry renews native session expiry.
"""
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import time
import re

from ui.effects import NativeBackend, child_environment, bounded_run
from ui.effects_worker import REQUEST_LIMIT, REPLY_LIMIT
from scripts.run_weather_app import EFFECTS_ACTION_BUDGET, cleanup_group
from weather.runtime import decode_json

ACTION_BUDGET = EFFECTS_ACTION_BUDGET
RPC_BUDGET = 28.0
REAP_RESERVE = 6.0


class RecoveryBackend(NativeBackend):
    """All nested inventory/native/unload calls share one recovery deadline."""
    def __init__(self, deadline):
        super().__init__()
        self.deadline = deadline
    def ctl(self, *args, json_output=True):
        self.identity()
        remaining = self.deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError("effects recovery absolute deadline")
        raw = bounded_run(["/usr/bin/hyprctl", "-i", self.instance,
                           *(["-j"] if json_output else []), *args],
                          timeout=min(3, remaining), stdout_limit=2 * 1024 * 1024, env=self.env)
        result = json.loads(raw) if json_output else raw.strip()
        if isinstance(result, dict) and result.get("error"):
            raise RuntimeError(str(result["error"]))
        return result


class OwnedEffectsSupervisor:
    def __init__(self, *, command=None, process_factory=subprocess.Popen, backend_factory=None):
        self.command = command or ["/usr/bin/python3", "-I", "-B", "-S", str(Path(__file__).with_name("effects_worker.py"))]
        self.process_factory, self.backend_factory = process_factory, backend_factory
        self.process = self.pidfd = None
        self.context = None
        self.request_id = 0
        self.current = dict(state="stopped", error=None, session_generation=None, remaining_seconds=0)
        self.buffer = bytearray()
        self.known_stopped = False
        self.action_deadline = 0
        self.request_deadline = None

    def set_request_deadline(self, deadline):
        self.request_deadline = deadline

    def action_time_available(self):
        return self.request_deadline is None or time.monotonic() < self.request_deadline - REAP_RESERVE

    def _begin_action(self):
        if not self.action_time_available():
            raise TimeoutError("effects request deadline exhausted")
        self.action_deadline = time.monotonic() + ACTION_BUDGET
        if self.request_deadline is not None:
            self.action_deadline = min(self.action_deadline, self.request_deadline)

    def status(self):
        if self.process is not None and select.select([self.pidfd], [], [], 0)[0]:
            self._begin_action()
            self._failure("effects owner stopped")
        return dict(self.current)

    def _rpc(self, operation, **arguments):
        self.request_id += 1
        encoded = json.dumps(dict(id=self.request_id, op=operation, **arguments), allow_nan=False).encode() + b"\n"
        if len(encoded) > REQUEST_LIMIT:
            raise ValueError("effects request limit")
        deadline = min(self.action_deadline - REAP_RESERVE, time.monotonic() + RPC_BUDGET)
        view = memoryview(encoded)
        while view:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([], [self.process.stdin], [], remaining)[1]:
                raise TimeoutError("effects owner request deadline")
            count = os.write(self.process.stdin.fileno(), view)
            if count <= 0:
                raise OSError("effects request short write")
            view = view[count:]
        while b"\n" not in self.buffer:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not select.select([self.process.stdout], [], [], remaining)[0]:
                raise TimeoutError("effects owner response deadline")
            chunk = os.read(self.process.stdout.fileno(), min(4096, REPLY_LIMIT + 1 - len(self.buffer)))
            if not chunk:
                raise RuntimeError("effects owner lost its reply")
            self.buffer.extend(chunk)
            if len(self.buffer) > REPLY_LIMIT:
                raise ValueError("effects owner reply limit")
        line, _, remaining = self.buffer.partition(b"\n")
        self.buffer = bytearray(remaining)
        response = decode_json(line)
        if type(response.get("id")) is not int or response["id"] != self.request_id or type(response.get("ok")) is not bool:
            raise RuntimeError("effects owner reply identity")
        if not isinstance(response.get("status"), dict):
            raise RuntimeError("effects owner status missing")
        self.current = response["status"]
        self.known_stopped = self.current.get("state") == "stopped"
        if "ownership" in response:
            self.context = response["ownership"]
        if not response["ok"]:
            raise RuntimeError(response.get("error", "effects owner failed"))
        return dict(self.current)

    def start_live(self, instance, output, controls):
        return self.start(30, instance, output, controls, persistent=True)

    def start(self, duration, instance, output, controls, *, persistent=False):
        self._begin_action()
        if self.process is not None:
            try:
                status = self._rpc("status")
                if status.get("state") == "stopped":
                    self._retire_clean()
            except Exception as error:
                self._failure(str(error))
        if self.process is not None or self.current["state"] != "stopped":
            raise RuntimeError("effects already active or cleanup unresolved")
        try:
            self.context = None
            self.known_stopped = False
            self.buffer.clear()
            self.process = self.process_factory(self.command, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL, start_new_session=True, env=child_environment(), bufsize=0)
            self.pidfd = os.pidfd_open(self.process.pid)
            os.set_blocking(self.process.stdin.fileno(), False)
            os.set_blocking(self.process.stdout.fileno(), False)
            if persistent:
                return self._rpc("start_live", instance=instance, output=output, controls=controls)
            return self._rpc("start", duration=duration, instance=instance, output=output, controls=controls)
        except Exception as error:
            self._failure(str(error))
            raise RuntimeError(self.current["error"]) from error

    def tick(self, selected_weather, controls):
        if self.process is None:
            return self.status()
        self._begin_action()
        try:
            weather = {key: selected_weather[key] for key in ("schema_version", "selected_at", "effects")}
            # Preserve only thermal provenance, never the full forecast arrays,
            # location or alert text. The worker validates it again on each tick.
            weather.update({key: selected_weather.get(key) for key in ("mode", "freshness")})
            for key, fields in (("current", ("time", "temperature_c")), ("forecast", ("fetched_at",))):
                source = selected_weather.get(key)
                weather[key] = {field: source.get(field) for field in fields} if isinstance(source, dict) else None
            result = self._rpc("tick", weather=weather, controls=controls)
            if result.get("state") == "stopped":
                self._retire_clean()
            return result
        except Exception as error:
            return self._failure(str(error))

    def _fallback(self):
        if self.context is None:
            raise RuntimeError("effects owner died before ownership acknowledgement; native state uncertain")
        context = self.context
        if (not isinstance(context, dict) or type(context.get("generation")) is not int
                or context["generation"] <= 0 or type(context.get("pid")) is not int or context["pid"] <= 0
                or not isinstance(context.get("starttime"), str) or not isinstance(context.get("instance"), str)
                or not isinstance(context.get("plugin_inventory"), list) or len(context["plugin_inventory"]) != 1):
            raise RuntimeError("effects ownership acknowledgement invalid")
        if not re.fullmatch(r"[a-f0-9]+_[0-9]+_[0-9]+", context["instance"]) or not context["starttime"].isdigit():
            raise RuntimeError("effects compositor identity invalid")
        backend = self.backend_factory() if self.backend_factory is not None else RecoveryBackend(self.action_deadline - REAP_RESERVE)
        backend.instance, backend.pid, backend.starttime = context["instance"], context["pid"], context["starttime"]
        backend.plugin_inventory = context["plugin_inventory"]
        backend.identity()
        inventory = backend.inventory()
        if inventory == []:
            return  # Living owner already completed its acknowledged cleanup.
        if inventory != backend.plugin_inventory:
            raise RuntimeError("effects plugin ownership changed; refusing recovery")
        status = backend.native("status")
        if status.get("session_generation") != context["generation"] or type(status.get("enabled")) is not bool:
            raise RuntimeError("effects generation changed; refusing recovery")
        cleanup_failed = status.get("cleanup_failed") is True
        if status.get("enabled") is True:
            stopped = backend.native(f"guard {context['generation']} off")
            if stopped.get("session_generation") != context["generation"] or stopped.get("enabled") is not False:
                raise RuntimeError("native stop acknowledgement invalid; refusing unload")
            cleanup_failed |= stopped.get("cleanup_failed") is True
        backend.unload()
        if cleanup_failed:
            raise RuntimeError("native resource cleanup failed; runtime evidence retained")

    def _reap(self, grace=None):
        if self.process is None:
            return
        process = self.process
        try:
            # No poll/wait precedes group teardown: unreaped worker anchors PGID.
            remaining = max(0, self.action_deadline - time.monotonic() - REAP_RESERVE)
            cleanup_group(process, grace=remaining if grace is None else min(grace, remaining), force_grace=0)
        finally:
            process.stdin.close()
            process.stdout.close()
            if self.pidfd is not None:
                os.close(self.pidfd)
            self.process = self.pidfd = None

    def _retire_clean(self):
        self.process.stdin.close()
        remaining = max(0, min(5, self.action_deadline - time.monotonic() - REAP_RESERVE))
        if self.pidfd is None or not select.select([self.pidfd], [], [], remaining)[0]:
            raise TimeoutError("stopped effects owner did not exit")
        exited = os.waitid(os.P_PID, self.process.pid, os.WEXITED | os.WNOWAIT | os.WNOHANG)
        if exited is None or exited.si_code != os.CLD_EXITED or exited.si_status != 0:
            raise RuntimeError("stopped effects owner exit was not clean")
        self._reap()
        self.context = None

    def _failure(self, error):
        errors = [error]
        if self.current.get("state") == "cleanup_failed" and self.current.get("error"):
            errors.append(self.current["error"])
        if self.process is not None:
            interrupted = False
            try:
                self.process.stdin.close()  # EOF lets a living owner perform cleanup.
                if self.known_stopped:
                    self._retire_clean()
                    self.current = dict(state="stopped", error=error, session_generation=None, remaining_seconds=0)
                    return dict(self.current)
                remaining = max(0, min(5, self.action_deadline - time.monotonic() - REAP_RESERVE))
                if self.pidfd is not None and not select.select([self.pidfd], [], [], remaining)[0]:
                    errors.append("effects owner exceeded EOF cleanup grace")
                self._fallback()
            except Exception as failure:
                errors.append(str(failure))
            except BaseException:
                interrupted = True
                raise
            finally:
                try:
                    # Signal-driven bridge shutdown gets a fresh bounded stop
                    # after unwinding. Do not spend this interrupted action's
                    # remaining grace before that cleanup can even begin.
                    self._reap(grace=0 if interrupted else None)
                except Exception as failure:
                    errors.append(str(failure))
        self.current = dict(state="cleanup_failed", error="; ".join(errors)[:1024],
                            session_generation=None, remaining_seconds=0)
        return dict(self.current)

    def stop(self):
        if self.process is None:
            # A BridgeStopped signal can interrupt _failure's native fallback;
            # its finally still reaps the independent worker. Recover that
            # acknowledged ownership once, before reporting stop completion.
            # A previously completed failed action already reports
            # cleanup_failed and is never silently retried/adopted here.
            if self.context is not None and self.current.get("state") in ("running", "starting"):
                self._begin_action()
                try:
                    self._fallback()
                    self.context = None
                    self.current = dict(state="stopped", error=None, session_generation=None, remaining_seconds=0)
                except Exception as error:
                    self.current = dict(state="cleanup_failed", error=("interrupted effects cleanup could not be confirmed: " + str(error))[:1024],
                                        session_generation=None, remaining_seconds=0)
            return self.status()
        self._begin_action()
        try:
            result = self._rpc("stop")
            self._retire_clean()
            return result
        except Exception as error:
            return self._failure(str(error))
