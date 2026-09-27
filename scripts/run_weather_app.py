#!/usr/bin/python3
"""A Weather App: live forecasts for Hyprland; desktop effects are explicitly opt-in."""
import argparse
import math
import os
from pathlib import Path
import re
import select
import signal
import stat
import subprocess
import sys
import time
import secrets
import tempfile

ROOT = Path(__file__).resolve().parents[1]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

# Shared shutdown envelope. Keep the independent guardian alive until the
# bridge has finished native recovery, then give Qt its own termination grace.
# Group teardown may spend one full bounded inventory beyond each deadline and
# two bounded leader waits. A signal interrupting an in-flight proxy action can
# also unwind that action's group teardown before Bridge.close begins.
PROCESS_INVENTORY_BUDGET = 1.0
PROCESS_REAP_TIMEOUT = 3.0
GROUP_CLEANUP_OVERHEAD = 2 * PROCESS_INVENTORY_BUDGET + 2 * PROCESS_REAP_TIMEOUT
EFFECTS_ACTION_BUDGET = 45.0
EFFECTS_STOP_MAX = EFFECTS_ACTION_BUDGET + GROUP_CLEANUP_OVERHEAD
FORECAST_TERM_GRACE = .2
FORECAST_KILL_GRACE = 3.0
FORECAST_STOP_MAX = FORECAST_TERM_GRACE + FORECAST_KILL_GRACE
BRIDGE_CLEANUP_MAX = GROUP_CLEANUP_OVERHEAD + 2 * FORECAST_STOP_MAX + EFFECTS_STOP_MAX
BRIDGE_CLEANUP_GRACE = math.ceil(BRIDGE_CLEANUP_MAX + 2)
# A request can arrive immediately after an idle heartbeat starts. Its wait
# must include that one bounded heartbeat, then one shared effects transaction.
BRIDGE_BACKGROUND_MAX = 2 * FORECAST_STOP_MAX + EFFECTS_STOP_MAX
BRIDGE_REQUEST_GRACE = math.ceil(BRIDGE_BACKGROUND_MAX + EFFECTS_STOP_MAX + 4)
UI_TERM_GRACE = 3.0
GUARDIAN_FINALIZE_MARGIN = 2.0


def guardian_wait_budget(cleanup_grace, force_grace):
    # The descendant phase's final bounded inventory can straddle its deadline;
    # group teardown has its own two inventories accounted for separately.
    return (cleanup_grace + PROCESS_INVENTORY_BUDGET + UI_TERM_GRACE + force_grace
            + GROUP_CLEANUP_OVERHEAD + GUARDIAN_FINALIZE_MARGIN)


def require_shader():
    from ui.release import ARTIFACTS, verify_artifacts
    verify_artifacts(ARTIFACTS[:1])
    try:
        info = (ROOT / "ui/shaders/atmosphere.frag.qsb").lstat()
        ready = stat.S_ISREG(info.st_mode) and 0 < info.st_size <= 512 * 1024
    except FileNotFoundError:
        ready = False
    if not ready:
        raise RuntimeError("the forecast shader is missing; from the app directory run: python3 -I -B packaging/build_shaders.py")


def prepare_location_state(args, state_home):
    """Explicit overrides get separate caches; never relabel another city's data."""
    from weather.location import select_location
    from ui.bridge import StateDirectory, validate_forecast, read_profile
    location = select_location(zip_code=args.zip_code, demo_location=args.demo_location)
    if args.state_dir is None:
        path = Path(state_home) / "a-weather-app"
        if args.demo_location is not None:
            path = path / "demos" / args.demo_location
        elif args.zip_code is not None:
            path = path / "locations" / args.zip_code
        args.state_dir = str(path)
    if location is None:
        return
    directory = private_state_directory(args.state_dir)
    state = StateDirectory(directory)
    try:
        profile = read_profile(state)
        if profile is not None:
            if profile["location"] != location:
                raise ValueError("state directory belongs to another location; choose a fresh --state-dir")
            return
        prior = state.read("location.json", 4096)
        if prior is not None and prior != location:
            raise ValueError("state directory belongs to another location; choose a fresh --state-dir")
        cached = state.read("forecast.json", 2 * 1024 * 1024)
        if cached is not None:
            validate_forecast(cached, location)
        state.write("location.json", location, 4096)
    finally:
        state.close()


def private_state_directory(path, *, return_fd=False):
    """Open each component once; never follow a symlink or repair an existing dir."""
    path = Path(path)
    if not path.is_absolute() or ".." in path.parts or path == Path("/"):
        raise ValueError("state directory must be an absolute, non-root path")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
    descriptor = os.open("/", flags)
    # User namespaces may map the filesystem root owner to an overflow UID.
    # Anchor trusted system ancestors to the actual root descriptor, not a
    # hard-coded numeric UID that differs inside the development sandbox.
    system_owner = os.fstat(descriptor).st_uid
    try:
        for index, component in enumerate(path.parts[1:]):
            final = index == len(path.parts) - 2
            try:
                child = os.open(component, flags, dir_fd=descriptor)
            except FileNotFoundError:
                os.mkdir(component, 0o700, dir_fd=descriptor)
                child = os.open(component, flags, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = child
            info = os.fstat(descriptor)
            if not stat.S_ISDIR(info.st_mode) or info.st_uid not in (system_owner, os.getuid()):
                raise ValueError("untrusted state directory owner or type")
            # /tmp is useful for finite development runs. Its sticky root-owned
            # parent is allowed; the actual state directory must still be 0700.
            trusted_sticky_parent = (not final and info.st_uid == system_owner
                                     and bool(info.st_mode & stat.S_ISVTX))
            if info.st_mode & 0o022 and not trusted_sticky_parent:
                raise ValueError("writable state directory ancestor refused")
            if final and (info.st_uid != os.getuid() or info.st_mode & 0o077):
                raise ValueError("state directory must be owned by this user and mode 0700")
        return os.dup(descriptor) if return_fd else str(path)
    finally:
        os.close(descriptor)


def command_environment(args):
    state = private_state_directory(args.state_dir)
    env = dict(os.environ, A_WEATHER_APP_STATE_DIR=state)
    # Do not silently reuse a signature inherited from before a compositor crash.
    env.pop("A_WEATHER_APP_INSTANCE", None)
    env.pop("A_WEATHER_APP_OUTPUT", None)
    if args.instance:
        if not re.fullmatch(r"[a-f0-9]+_[0-9]+_[0-9]+", args.instance):
            raise ValueError("invalid explicit compositor instance")
        env["A_WEATHER_APP_INSTANCE"] = args.instance
    if args.output:
        if not re.fullmatch(r"[A-Za-z0-9_.:-]{1,128}", args.output):
            raise ValueError("invalid explicit output connector")
        env["A_WEATHER_APP_OUTPUT"] = args.output
    return ["/usr/bin/quickshell", "--no-duplicate", "--path", str(ROOT / "ui/qml")], env


def live_group_members(group, *, include_zombies=False):
    """Bounded metadata-only check; the unreaped leader anchors the group ID."""
    members = []
    deadline = time.monotonic() + PROCESS_INVENTORY_BUDGET
    with os.scandir("/proc") as entries:
        for index, entry in enumerate(entries):
            if index > 65536 or time.monotonic() > deadline:
                raise RuntimeError("process inventory exceeds cleanup bound")
            if not entry.name.isdecimal():
                continue
            try:
                if entry.stat(follow_symlinks=False).st_uid != os.getuid():
                    continue
                descriptor = os.open(entry.path + "/stat", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
                try:
                    raw = os.read(descriptor, 4097)
                finally:
                    os.close(descriptor)
                if len(raw) > 4096:
                    raise RuntimeError("process identity exceeds cleanup bound")
                fields = raw.rsplit(b")", 1)[1].split()
                if len(fields) < 4:
                    raise RuntimeError("malformed process cleanup identity")
                if int(fields[2]) == group and int(fields[3]) == group and (include_zombies or fields[0] != b"Z"):
                    members.append(int(entry.name))
            except (FileNotFoundError, ProcessLookupError):
                continue
    return members


def cleanup_group(child, grace=BRIDGE_CLEANUP_GRACE, force_grace=3):
    """Observation failure is unknown, never 'gone'; still KILL and reap."""
    errors = []
    remaining = [child.pid]
    result = None
    try:
        try:
            os.killpg(child.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        except OSError as error:
            errors.append(f"group TERM failed: {error}")
        deadline = time.monotonic() + grace
        while not errors:
            try:
                remaining = live_group_members(child.pid)
            except Exception as error:
                errors.append(f"group observation failed: {error}")
                break
            if not remaining or time.monotonic() >= deadline:
                break
            time.sleep(min(.1, max(0, deadline - time.monotonic())))
        if remaining or errors:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            except OSError as error:
                errors.append(f"group KILL failed: {error}")
            deadline = time.monotonic() + force_grace
            while True:
                try:
                    remaining = live_group_members(child.pid)
                except Exception as error:
                    errors.append(f"post-KILL observation failed: {error}")
                    break
                if not remaining or time.monotonic() >= deadline:
                    break
                time.sleep(min(.1, max(0, deadline - time.monotonic())))
            errors.append("weather window group required forced termination; inspect effect cleanup")
    finally:
        # Even an unexpected scanner/signal failure cannot bypass leader reap.
        try:
            result = child.wait(timeout=PROCESS_REAP_TIMEOUT)
        except subprocess.TimeoutExpired:
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            finally:
                try:
                    result = child.wait(timeout=PROCESS_REAP_TIMEOUT)
                except subprocess.TimeoutExpired as error:
                    raise RuntimeError("owned weather child could not be reaped after KILL") from error
            errors.append("owned weather leader exceeded reap deadline")
    if errors:
        raise RuntimeError("; ".join(errors))
    return result


def descendant_first_term(child, deadline):
    """Let the bridge finish cleanup before Qt destroys its child Process.

    The unreaped UI leader anchors PGID/session identity throughout this phase.
    Each descendant signal uses a pidfd and descriptor-bound metadata recheck;
    no PID from a stale inventory is signaled by numeric PID alone.
    """
    # Qt must reap its helper and receive the reserved cleanup-failure exit code
    # before guardian TERM can close the UI. A zombie is therefore still pending
    # in this phase; ordinary group teardown continues to ignore zombies.
    leader = os.pidfd_open(child.pid)
    try:
        _descendant_first_term(child, deadline, leader)
    finally:
        os.close(leader)


def _descendant_first_term(child, deadline, leader):
    signaled = set()
    while True:
        # A dead UI cannot acknowledge zombies, but its still-living bridge
        # retains the full native cleanup phase before group escalation.
        leader_dead = bool(select.select([leader], [], [], 0)[0])
        members = [pid for pid in live_group_members(child.pid, include_zombies=not leader_dead) if pid != child.pid]
        if not members:
            return
        if time.monotonic() >= deadline:
            raise TimeoutError("descendant cleanup acknowledgement exceeded its deadline")
        for pid in members:
            if time.monotonic() >= deadline:
                break
            descriptor = None
            identity_fd = None
            try:
                descriptor = os.pidfd_open(pid)
                identity_fd = os.open(f"/proc/{pid}/stat", os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
                info = os.fstat(identity_fd)
                raw = os.read(identity_fd, 4097)
                if info.st_uid != os.getuid() or not stat.S_ISREG(info.st_mode) or len(raw) > 4096:
                    raise RuntimeError("untrusted descendant cleanup identity")
                fields = raw.rsplit(b")", 1)[1].split()
                if len(fields) < 20:
                    raise RuntimeError("malformed descendant cleanup identity")
                if int(fields[2]) != child.pid or int(fields[3]) != child.pid:
                    continue  # A process left the owned group; never adopt it.
                if fields[0] == b"Z":
                    continue  # Wait for the living UI to consume its child's status.
                identity = (pid, fields[19])
                if identity not in signaled:
                    signal.pidfd_send_signal(descriptor, signal.SIGTERM)
                    signaled.add(identity)
            except (ProcessLookupError, FileNotFoundError):
                continue
            finally:
                if identity_fd is not None:
                    os.close(identity_fd)
                if descriptor is not None:
                    os.close(descriptor)
        time.sleep(min(.1, max(0, deadline - time.monotonic())))


def supervise(command, env, duration=None, *, cleanup_grace=BRIDGE_CLEANUP_GRACE, force_grace=3, owner_fd=None, quiet_child=False):
    stdio = {"stdout": subprocess.DEVNULL, "stderr": subprocess.DEVNULL} if quiet_child else {}
    child = subprocess.Popen(command, env=env, start_new_session=True, **stdio)
    # Do not poll/wait/reap the leader until group cleanup is complete. Its
    # zombie reserves the PID/PGID, preventing a later killpg from targeting a
    # reused group after a quick UI exit. Linux pidfd readiness observes exit
    # without reaping it.
    descriptor = None
    stopped = False
    prior = {}

    def stop(_number, _frame):
        nonlocal stopped
        stopped = True

    deadline = time.monotonic() + duration if duration else float("inf")
    try:
        descriptor = os.pidfd_open(child.pid)
        for number in (signal.SIGINT, signal.SIGTERM):
            prior[number] = signal.signal(number, stop)
        while not stopped and time.monotonic() < deadline:
            watched = [descriptor] + ([owner_fd] if owner_fd is not None else [])
            ready = select.select(watched, [], [], .2)[0]
            if owner_fd is not None and owner_fd in ready:
                if os.read(owner_fd, 1):
                    raise RuntimeError("unexpected launcher liveness data")
                break
            if descriptor in ready:
                break
    finally:
        # The owned session includes the bridge. Both receive SIGTERM so the
        # bridge can stop effects even if the UI event loop cannot process close.
        try:
            cleanup_error = None
            grace = cleanup_grace
            if owner_fd is not None:
                cleanup_deadline = time.monotonic() + cleanup_grace
                try:
                    descendant_first_term(child, cleanup_deadline)
                    # Qt must not lose its own grace merely because the bridge
                    # used its legitimate native recovery budget. This second
                    # phase cannot extend the already-finished bridge phase.
                    grace = UI_TERM_GRACE
                except Exception as error:
                    cleanup_error = error
                    grace = 0  # Unknown metadata never bypasses group teardown.
            result = cleanup_group(child, grace, force_grace)
            if cleanup_error is not None:
                raise RuntimeError(f"descendant-first cleanup failed: {cleanup_error}") from cleanup_error
        finally:
            try:
                if descriptor is not None:
                    os.close(descriptor)
            finally:
                for number, previous in prior.items():
                    signal.signal(number, previous)
    return 0 if result == -signal.SIGTERM else result


def guarded_supervise(command, env, duration=None, *, cleanup_grace=BRIDGE_CLEANUP_GRACE, force_grace=3):
    """The independent guardian owns UI cleanup even if this launcher is SIGKILLed.

    Qt Process destruction can remove its immediate child without running Python
    finally blocks. A new-session guardian instead retains the UI's unreaped PID
    anchor and watches EOF on a pipe whose sole writer is this launcher. The
    writer is CLOEXEC and never inherited by any child. This is an intentional
    ownership split, not a daemon: owner death starts the bounded existing group
    cleanup, and explicit duration is independently enforced by the guardian.
    Descendants receive TERM first so the bridge can unload effects before Qt
    tears down its Process object. The bridge has one derived grace budget and
    Qt then gets a separate fixed termination grace. The launcher wait includes
    both phases, bounded inventory/reap work and diagnostic finalization.
    Guardian diagnostics are capped at 8KiB in a random private state file,
    deleted on success or atomically promoted to guardian-last-error.log on
    failure. SIGKILL of the guardian itself can leave its bounded active file;
    arbitrary guardian death cannot perform cleanup. UI stderr is not inherited
    into this file, and a caller without a state directory gets anonymous logs.
    """
    reader, writer = os.pipe2(os.O_CLOEXEC | os.O_NONBLOCK)
    guardian = None
    diagnostic_directory = diagnostic_file = anonymous_log = None
    diagnostic_name = None
    diagnostic_hint = "no persistent diagnostic state directory configured"
    prior = {}
    stopped = False
    def stop(_number, _frame):
        nonlocal stopped
        stopped = True
    try:
        state = env.get("A_WEATHER_APP_STATE_DIR")
        if state:
            diagnostic_directory = private_state_directory(state, return_fd=True)
            diagnostic_name = f".guardian-{secrets.token_hex(16)}.log"
            diagnostic_file = os.open(diagnostic_name, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW | os.O_CLOEXEC,
                                      0o600, dir_fd=diagnostic_directory)
            diagnostic_hint = str(Path(state) / "guardian-last-error.log")
        else:
            anonymous_log = tempfile.TemporaryFile()
            diagnostic_file = anonymous_log.fileno()
        guardian_command = ["/usr/bin/python3", "-I", "-B", str(ROOT / "scripts/weather_app_guardian.py"),
                            "--owner-fd", str(reader), "--cleanup-grace", str(cleanup_grace),
                            "--force-grace", str(force_grace)]
        if diagnostic_directory is not None:
            guardian_command += ["--diagnostic-dir-fd", str(diagnostic_directory), "--diagnostic-name", diagnostic_name]
        if duration is not None:
            guardian_command += ["--duration", str(duration)]
        guardian_command += ["--", *command]
        guardian = subprocess.Popen(guardian_command, env=env, start_new_session=True,
                                    pass_fds=(reader,) + ((diagnostic_directory,) if diagnostic_directory is not None else ()), stdin=subprocess.DEVNULL,
                                    stdout=subprocess.DEVNULL, stderr=diagnostic_file)
        os.close(reader)
        reader = None
        for number in (signal.SIGINT, signal.SIGTERM):
            prior[number] = signal.signal(number, stop)
        while not stopped:
            try:
                # No killpg follows a successful wait: guardian exit means its
                # owned UI group cleanup finished (or failed visibly).
                result = guardian.wait(timeout=.2)
                if result:
                    raise RuntimeError(f"weather guardian exited with failure ({result}); diagnostics: {diagnostic_hint}")
                return result
            except subprocess.TimeoutExpired:
                pass
    finally:
        os.close(writer)  # Also executes on startup errors and ordinary cancellation.
        if reader is not None:
            os.close(reader)
        try:
            if guardian is not None and guardian.returncode is None:
                try:
                    result = guardian.wait(timeout=guardian_wait_budget(cleanup_grace, force_grace))
                except subprocess.TimeoutExpired as error:
                    # Killing the cleanup owner could orphan its UI. Leave its
                    # already-triggered cleanup running and report failure.
                    raise RuntimeError("weather guardian exceeded cleanup deadline") from error
                if result:
                    raise RuntimeError(f"weather guardian cleanup failed ({result}); diagnostics: {diagnostic_hint}")
        finally:
            try:
                if anonymous_log is not None:
                    anonymous_log.close()
                elif diagnostic_file is not None:
                    os.close(diagnostic_file)
                if diagnostic_directory is not None:
                    try:
                        if guardian is None and diagnostic_name is not None:
                            os.unlink(diagnostic_name, dir_fd=diagnostic_directory)
                    finally:
                        os.close(diagnostic_directory)
            finally:
                for number, previous in prior.items():
                    signal.signal(number, previous)
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(prog="a-weather-app", description=__doc__)
    parser.add_argument("--version", action="version", version="A Weather App 1.0.0-rc.1")
    parser.add_argument("--toggle-window", action="store_true", help="toggle the running forecast window")
    state_home = os.environ.get("XDG_STATE_HOME") or str(Path.home() / ".local/state")
    parser.add_argument("--state-dir", help="private state directory (overrides use separate location caches by default)")
    location = parser.add_mutually_exclusive_group()
    location.add_argument("--zip-code", help="use an exact five-digit US ZIP code")
    location.add_argument("--demo-location", choices=["boston"], help="use Boston for demo captures without changing your normal location")
    parser.add_argument("--instance", help="explicit live Hyprland instance for effect activation")
    parser.add_argument("--output", help="explicit monitor connector for effect activation")
    parser.add_argument("--duration", type=int, help="finite UI development run, 1..3600 seconds")
    args = parser.parse_args(argv)
    if args.toggle_window:
        # ROOT resolves installed symlinks, matching command_environment's path.
        try:
            return subprocess.run(["/usr/bin/quickshell", "ipc", "--path",
                                   str(ROOT / "ui/qml"), "call",
                                   "a-weather-app-ui", "toggleWindow"],
                                  timeout=2, check=False).returncode
        except (OSError, subprocess.TimeoutExpired):
            return 1
    if args.duration is not None and not 1 <= args.duration <= 3600:
        parser.error("duration must be between 1 and 3600 seconds")
    try:
        require_shader()
        prepare_location_state(args, state_home)
        command, env = command_environment(args)
        return guarded_supervise(command, env, args.duration)
    except (OSError, ValueError, RuntimeError) as error:
        print(f"A Weather App launch failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
