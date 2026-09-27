"""Read-only DRM fdinfo busy-time sampling, not power or frame-time measurement.

See https://www.kernel.org/doc/html/latest/gpu/drm-usage-stats.html.
Shared descriptors are deduplicated by device/client. Busy time is not frequency-
adjusted utilization; aggregated engine activity can exceed one engine's 100%.
"""
from pathlib import Path
import re


def parse_fdinfo(text):
    fields = dict(line.split(":", 1) for line in text.splitlines() if ":" in line)
    fields = {key: value.strip() for key, value in fields.items()}
    client, driver = fields.get("drm-client-id"), fields.get("drm-driver")
    if not client or not client.isdecimal() or not driver:
        return {}
    device = fields.get("drm-pdev", "global-client-ids")
    result = {}
    for key, value in fields.items():
        if key.startswith("drm-engine-"):
            match = re.fullmatch(r"([0-9]+) ns", value)
            if match:
                result[(driver, device, client, key.removeprefix("drm-engine-"))] = int(match[1])
    return result


def sample_gpu(pids):
    counters = {}
    inaccessible = 0
    for pid in pids:
        try:
            paths = list((Path("/proc") / str(pid) / "fdinfo").iterdir())
        except OSError:
            inaccessible += 1
            continue
        for path in paths:
            try:
                with path.open() as stream:
                    text = stream.read(65537)
                if len(text) > 65536:
                    inaccessible += 1
                    continue
                for key, value in parse_fdinfo(text).items():
                    counters[key] = max(counters.get(key, 0), value)
            except OSError:
                inaccessible += 1
    return counters, inaccessible


def accumulate_busy(current, highwater, totals):
    for key, value in current.items():
        if key in highwater:
            delta = max(0, value - highwater[key])
            engine = "/".join((key[0], key[1], key[3]))
            totals[engine] = totals.get(engine, 0) + delta
            highwater[key] = max(highwater[key], value)
        else:
            # First observation establishes a baseline, never counts lifetime
            # work as activity during this measurement interval.
            highwater[key] = value
