#!/usr/bin/env python3
"""Bounded, private, offline Qt teardown experiments; no system configuration changes.

Build once, then run small batches, for example:
  python3 scripts/diagnose_qt_teardown.py build /tmp/qt-teardown-evidence
  python3 scripts/diagnose_qt_teardown.py run /tmp/qt-teardown-evidence --repeat 3
Each invocation retains commands, environment overrides, raw logs and identities.
GDB requires permission to trace its own child. No debuginfod or core storage is used.
"""
import argparse
import ctypes
import hashlib
import json
import os
from pathlib import Path
import resource
import shutil
import signal
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[1]


def save(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def private_env(base):
    env = os.environ.copy()
    for name in list(env):
        if name.startswith(("WEATHER_", "QT_", "QML_", "QSG_")) or name in (
            "HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY", "DISPLAY",
            "DBUS_SESSION_BUS_ADDRESS", "LD_PRELOAD", "LD_LIBRARY_PATH",
        ):
            env.pop(name, None)
    for key, folder in (("HOME", "home"), ("XDG_CONFIG_HOME", "config"),
                        ("XDG_CACHE_HOME", "cache"), ("XDG_DATA_HOME", "data"), ("XDG_STATE_HOME", "state"),
                        ("XDG_RUNTIME_DIR", "runtime"), ("TMPDIR", "tmp")):
        path = base / folder
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
        env[key] = str(path)
    env.update(QT_QPA_PLATFORM="offscreen", QT_QPA_PLATFORMTHEME="basic",
               QT_QUICK_BACKEND="software", WEATHER_QT_TEARDOWN_TRACE="1",
               DEBUGINFOD_URLS="", ASAN_OPTIONS="detect_leaks=0:abort_on_error=1",
               UBSAN_OPTIONS="halt_on_error=1:print_stacktrace=1")
    return env


def children():
    # This process is a child subreaper: descendants of a crashed inferior are
    # adopted here, so cleanup never relies on matching global process names.
    path = Path(f"/proc/self/task/{os.getpid()}/children")
    return [int(pid) for pid in path.read_text().split()]


def cleanup_children():
    killed = []
    deadline = time.monotonic() + 5
    while time.monotonic() < deadline:
        pids = children()
        if not pids:
            return killed
        for pid in pids:
            try:
                os.kill(pid, signal.SIGKILL)
                killed.append(pid)
            except ProcessLookupError:
                pass
        while True:
            try:
                pid, _ = os.waitpid(-1, os.WNOHANG)
            except ChildProcessError:
                break
            if not pid:
                break
        time.sleep(.05)
    raise RuntimeError("Owned child cleanup did not finish")


def execute(command, cwd, env, log, timeout):
    started = time.monotonic()
    record = dict(command=command, cwd=str(cwd), timeout_seconds=timeout,
                  environment={k: v for k, v in env.items()
                               if k.startswith(("QT_", "QML_", "QSG_", "XDG_", "WEATHER_"))
                               or k in ("HOME", "TMPDIR", "GO_APP", "ASAN_OPTIONS",
                                        "UBSAN_OPTIONS", "DEBUGINFOD_URLS", "GOCACHE",
                                        "GOMODCACHE", "MALLOC_PERTURB_", "GLIBC_TUNABLES")})
    save(log.with_suffix(".command.json"), record)
    with log.open("w") as output:
        process = subprocess.Popen(command, cwd=cwd, env=env, stdout=output,
                                   stderr=subprocess.STDOUT, start_new_session=True)
        try:
            code = process.wait(timeout=timeout)
            timed_out = False
        except subprocess.TimeoutExpired:
            # SIGINT lets GDB stop the inferior and execute the stack commands.
            process.send_signal(signal.SIGINT)
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            code, timed_out = process.returncode, True
    record.update(returncode=code, timed_out=timed_out,
                  seconds=round(time.monotonic()-started, 3),
                  cleaned_child_pids=cleanup_children())
    save(log.with_suffix(".result.json"), record)
    return record


def build(out):
    source = out / "source"
    source.mkdir()  # Refuse to overwrite an existing evidence snapshot.
    shutil.copy2(__file__, out / "harness.py")
    files = subprocess.check_output(["git", "ls-files", "-z"], cwd=ROOT).split(b"\0")
    for raw in files:
        if not raw:
            continue
        rel = os.fsdecode(raw)
        target = source / rel
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(ROOT / rel, target)
    (out / "worktree.patch").write_bytes(subprocess.check_output(["git", "diff", "HEAD"], cwd=ROOT))
    save(out / "source-identity.json", {
        "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
        "files": {str(p.relative_to(source)): sha(p) for p in source.rglob("*") if p.is_file()},
        "harness_sha256": sha(Path(__file__)),
    })
    env = private_env(out / "build-env")
    env.update(GOCACHE=str(out / "go-cache"),
               GOMODCACHE=subprocess.check_output(["go", "env", "GOMODCACHE"], text=True).strip())
    (source / "build").mkdir(exist_ok=True)
    commands = [(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-X main.buildMode=development", "-o", str(source / "build/a-weather-app"),
                  "./cmd/a-weather-app"], source, "go-build", 180)]
    for variant in ("optimized", "sanitized"):
        folder = out / variant
        folder.mkdir()
        qt = source / "native/qt"
        project = folder / "diagnostic.pro"
        project.write_text("\n".join([
            "QT += testlib quick network", "CONFIG += console c++17 testcase release",
            "CONFIG -= debug", "TARGET = service-frontend-test", f"INCLUDEPATH += {qt}",
            "SOURCES += " + " ".join(str(qt / s) for s in (
                "tests/service_frontend_test.cpp", "transport.cpp", "protocol.cpp", "maptiles.cpp")),
            "HEADERS += " + " ".join(str(qt / s) for s in ("transport.h", "protocol.h", "maptiles.h")),
            f"RESOURCES += {qt}/resources.qrc", "QMAKE_CXXFLAGS += -g -fno-omit-frame-pointer",
            "QMAKE_CXXFLAGS_RELEASE = -O1" if variant == "sanitized" else "QMAKE_CXXFLAGS_RELEASE = -O2",
            "QMAKE_CXXFLAGS += -fsanitize=address,undefined" if variant == "sanitized" else "",
            "QMAKE_LFLAGS += -fsanitize=address,undefined" if variant == "sanitized" else "",
        ]) + "\n")
        commands.extend([(["qmake6", str(project)], folder, variant+"-qmake", 30),
                         (["make", "-j2"], folder, variant+"-build", 240)])
    for command, cwd, name, timeout in commands:
        result = execute(command, cwd, env, out / (name+".log"), timeout)
        if result["returncode"] or result["timed_out"]:
            raise RuntimeError(f"Build failed: {name}; inspect retained log")
    for variant in ("optimized", "sanitized"):
        binary = out / variant / "service-frontend-test"
        execute(["ldd", str(binary)], out, env, out / (variant+"-libraries.log"), 10)
        execute(["readelf", "-n", str(binary)], out, env, out / (variant+"-elf.log"), 10)
    execute(["qmake6", "-query"], out, env, out / "qt-version.log", 10)
    execute(["go", "version", "-m", str(source / "build/a-weather-app")], out, env, out / "go-identity.log", 10)
    save(out / "binary-hashes.json", {str(p.relative_to(out)): sha(p) for p in (
        source / "build/a-weather-app", out / "optimized/service-frontend-test", out / "sanitized/service-frontend-test")})


def run(out, args):
    batch = out / (args.variant + "-" + args.render_loop + "-" + str(time.time_ns()))
    batch.mkdir()
    shutil.copy2(__file__, batch / "harness.py")
    save(batch / "identity.json", {
        "harness_sha256": sha(Path(__file__)),
        "binary_sha256": sha(out / args.variant / "service-frontend-test"),
        "service_sha256": sha(out / "source/build/a-weather-app"),
    })
    results = []
    for number in range(args.repeat):
        for mode in args.modes:
            folder = batch / f"{number+1}-{mode}"
            folder.mkdir()
            # Short paths also satisfy Unix-domain socket path length limits.
            fixture_dir = tempfile.TemporaryDirectory(prefix="wqt-", dir="/tmp")
            env = private_env(Path(fixture_dir.name))
            env["GO_APP"] = str(out / "source/build/a-weather-app")
            if args.render_loop != "default":
                env["QSG_RENDER_LOOP"] = args.render_loop
            if args.perturb:
                env["MALLOC_PERTURB_"] = str(args.perturb)
                env["GLIBC_TUNABLES"] = "glibc.malloc.tcache_count=0"
            status = folder / "inferior.json"
            # A successful GDB exit is not evidence that the inferior passed.
            # Record actual exit events; signal stops capture every thread before
            # killing only the owned inferior. GDB does not pass the fatal signal
            # onward, avoiding the system's piped coredump handler entirely.
            script = folder / "capture.gdb"
            trace = []
            if args.trace_transport:
                trace = ["break WeatherTransport::fail", "commands", "silent", "bt 16",
                         "print this->failed", "print this->buffer.d"]
                if args.variant == "sanitized":
                    trace += ["print (int)__asan_address_is_poisoned(this->buffer.d.d)",
                              "call (void)__asan_describe_address(this->buffer.d.d)"]
                trace += ["continue", "end", "break MapTiles::request", "commands", "silent",
                          'printf "MAP REQUEST REACHED\\n"', "bt 4", "continue", "end"]
            script.write_text("\n".join([
                "set pagination off", "set confirm off", "set disable-randomization off",
                "set startup-with-shell off", "set print thread-events off",
                "handle SIGPIPE nostop noprint pass", "python", "import gdb, json",
                "result = {}",
                "def exited(event): result.update(exit_code=getattr(event, 'exit_code', None))",
                "gdb.events.exited.connect(exited)", "end", *trace, "run", "python",
                "if gdb.selected_thread() is not None:",
                "    result['stopped'] = True",
                "    gdb.execute('info program')",
                "    gdb.execute('thread apply all bt full')",
                "    gdb.execute('info sharedlibrary')",
                "    gdb.execute('kill')",
                f"with open({str(status)!r}, 'w') as f: json.dump(result, f)",
                "end", "quit",
            ]) + "\n")
            command = ["gdb", "-q", "-nx", "-batch", "-iex", "set debuginfod enabled off",
                       "-x", str(script), "--args", str(out / args.variant / "service-frontend-test"),
                       "hiddenPresentationSuppressesPeriodicSnapshots:"+mode, "-nocrashhandler"]
            result = execute(command, out, env, folder / "raw.log", args.timeout)
            fixture_dir.cleanup()
            inferior = json.loads(status.read_text()) if status.exists() else {}
            result.update(mode=mode, inferior=inferior,
                          passed=(not result["returncode"] and not result["timed_out"]
                                  and not inferior.get("stopped") and inferior.get("exit_code") == 0))
            results.append(result)
            save(batch / "summary.json", {"runs": results, "passed": sum(r["passed"] for r in results),
                                          "failed": sum(not r["passed"] for r in results)})
            print(f"{args.variant}/{mode}/{number+1}: {'PASS' if result['passed'] else 'FAIL'}", flush=True)
            if not result["passed"]:
                return 1  # Change the experiment after a failure; do not plow on.
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("build", "run"))
    parser.add_argument("output", type=Path)
    parser.add_argument("--variant", choices=("optimized", "sanitized"), default="optimized")
    parser.add_argument("--repeat", type=int, choices=range(1, 11), default=3)
    parser.add_argument("--timeout", type=int, choices=range(15, 301), default=45)
    parser.add_argument("--render-loop", choices=("default", "basic", "threaded"), default="default")
    parser.add_argument("--perturb", type=int, choices=range(1, 256))
    parser.add_argument("--trace-transport", action="store_true")
    parser.add_argument("--modes", nargs="+", default=["fixture-terminate", "orderly", "kill-disconnect"],
                        choices=("fixture-terminate", "orderly", "kill-disconnect"))
    args = parser.parse_args()
    os.umask(0o077)
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):  # PR_SET_CHILD_SUBREAPER
        raise OSError(ctypes.get_errno(), "Cannot enable owned child cleanup")
    out = args.output.resolve()
    out.mkdir(mode=0o700, parents=True, exist_ok=True)
    try:
        return build(out) if args.action == "build" else run(out, args)
    finally:
        cleanup_children()


if __name__ == "__main__":
    raise SystemExit(main())
