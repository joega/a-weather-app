"""Offline native input-file checks; invoke with the freshly built sky host."""
from copy import deepcopy
from datetime import datetime, timedelta, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


def checked_run(arguments):
    result = subprocess.run(arguments, capture_output=True, timeout=5)
    diagnostics = result.stdout + result.stderr
    if any(marker in diagnostics for marker in
           (b"AddressSanitizer", b"LeakSanitizer", b"UndefinedBehaviorSanitizer", b"runtime error:")):
        raise AssertionError("native sanitizer diagnostic: " + diagnostics.decode(errors="replace")[:2048])
    return result


def run(host):
    with tempfile.TemporaryDirectory() as folder:
        path = Path(folder) / "weather.json"
        envelope = dict(schema_version=1, selected_at=datetime.now(timezone.utc).isoformat(),
            effects=dict(sun_elevation=35, sun_azimuth=180, cloud_cover=.5,
                fog_density=0, wind_x=35, lightning_enabled=False,
                reduced_motion=False, thunderstorm=False))
        path.write_text(json.dumps(envelope))
        path.chmod(0o600)

        def check(target, accepted):
            result = checked_run([host, "--validate-weather", str(target)])
            if (result.returncode == 0) != accepted:
                raise AssertionError(f"unexpected native file acceptance: {target.name}")

        check(path, True)
        symlink = Path(folder) / "symlink"
        symlink.symlink_to(path)
        check(symlink, False)
        hardlink = Path(folder) / "hardlink"
        os.link(path, hardlink)
        check(path, False)
        hardlink.unlink()
        path.chmod(0o666)
        check(path, False)
        path.chmod(0o600)
        fifo = Path(folder) / "fifo"
        os.mkfifo(fifo, 0o600)
        check(fifo, False)
        check(path, True)
        # Exercise exact allocation/limit boundaries with valid JSON plus
        # whitespace, not merely a malformed large payload.
        weather_limit = 2 * 1024 * 1024
        encoded = json.dumps(envelope).encode()
        path.write_bytes(encoded + b" " * (weather_limit - len(encoded)))
        check(path, True)
        path.write_bytes(encoded + b" " * (weather_limit + 1 - len(encoded)))
        check(path, False)
        path.write_bytes(encoded)
        check(path, True)
        invalid = [b"", b"[]", b"{broken", b'{"x":"nul\x00byte"}',
            b'/* extension comment */' + encoded,
            b'{"schema_version":1,' + encoded[1:],
            b'{"schema_\\u0076ersion":1,' + encoded[1:],
            encoded.replace(b'"cloud_cover": 0.5', b'"cloud_cover":0.5,"cloud_\\u0063over":0.5'),
            b'{"x":' + b'[' * 65 + b'0' + b']' * 65 + b'}']
        for field, value in (("wind_x", 501), ("wind_x", True),
                             ("cloud_cover", float("nan")),
                             ("lightning_enabled", 1)):
            candidate = deepcopy(envelope)
            candidate["effects"][field] = value
            invalid.append(json.dumps(candidate).encode())
        for updates in (dict(schema_version=True), dict(effects=[]),
                dict(selected_at="2026-09-27T12:00:00"),
                dict(selected_at=(datetime.now(timezone.utc) - timedelta(minutes=1)).isoformat()),
                dict(selected_at=(datetime.now(timezone.utc) + timedelta(minutes=10)).isoformat())):
            candidate = dict(envelope, **updates)
            invalid.append(json.dumps(candidate).encode())
        for payload in invalid:
            path.write_bytes(payload)
            check(path, False)
        # Repeated names in different objects are not duplicates.
        candidate = dict(envelope, unrelated=dict(schema_version=1))
        path.write_text(json.dumps(candidate))
        check(path, True)
        envelope["selected_at"] = datetime.now(timezone.utc).isoformat()
        path.write_text(json.dumps(envelope))
        check(path, True)
        policy_path = Path(folder) / "policy.json"
        policy = dict(schema_version=1, session="abcdef_123_456", output="eDP-1",
            sequence=1, generated_at_unix_ms=int(time.time() * 1000),
            stale_after_ms=1500, render_allowed=True, reason="none")

        def check_policy(value, accepted):
            if isinstance(value, bytes):
                policy_path.write_bytes(value)
            else:
                value = dict(value)
                value.setdefault("generated_at_unix_ms", int(time.time() * 1000))
                policy_path.write_text(json.dumps(value))
            policy_path.chmod(0o600)
            result = checked_run([host, "--validate-policy", str(policy_path),
                "--policy-session", policy["session"], "--output", policy["output"]])
            if (result.returncode == 0) != accepted:
                raise AssertionError("unexpected native policy acceptance")

        del policy["generated_at_unix_ms"]  # Each valid probe gets a fresh lease.
        check_policy(policy, True)
        check_policy(dict(policy, render_allowed=False, reason="fullscreen"), True)
        raw = json.dumps(dict(policy, generated_at_unix_ms=int(time.time() * 1000))).encode()
        check_policy(b'{"schema_version":1,' + raw[1:], False)
        check_policy(b'{"schema_\\u0076ersion":1,' + raw[1:], False)
        check_policy(b'/* extension comment */' + raw, False)
        for updates in (dict(schema_version=True), dict(session="foreign_123_456"),
                dict(output="HDMI-A-1"), dict(sequence=-1), dict(stale_after_ms=99999),
                dict(render_allowed=1), dict(render_allowed=True, reason="fullscreen"),
                dict(render_allowed=False, reason="none"),
                dict(generated_at_unix_ms=int(time.time() * 1000) - 10000),
                dict(generated_at_unix_ms=int(time.time() * 1000) + 10000)):
            check_policy(dict(policy, **updates), False)
    print("PASS: native weather/policy accepted; unsafe files and malformed/stale/mismatched inputs rejected")


if __name__ == "__main__":
    run(str(Path(sys.argv[1]).resolve()))
