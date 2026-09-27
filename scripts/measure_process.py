#!/usr/bin/python3
"""Read-only Linux process-tree CPU/RSS sampling for finite audit runs.

CPU percentages use one logical core as 100%. RSS sums may count shared pages
more than once. Short-lived children between samples can be missed. GPU
busy time is optional; compositor frame times and power are not measured.
"""
import argparse
import json
import os
from pathlib import Path
import time


def sample(root):
    pending, result = [root], {}
    while pending:
        pid = pending.pop()
        if pid in result:
            continue
        try:
            base = Path("/proc") / str(pid)
            fields = (base / "stat").read_text().rsplit(")", 1)[1].split()
            result[pid] = (int(fields[19]), int(fields[11]) + int(fields[12]),
                           int(fields[21]) * os.sysconf("SC_PAGE_SIZE"))
            # A worker may be forked by any thread, not only the process leader.
            for task in (base / "task").iterdir():
                try:
                    pending.extend(int(child) for child in
                        (task / "children").read_text().split())
                except FileNotFoundError:
                    pass
        except (FileNotFoundError, ProcessLookupError):
            continue
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("pid", type=int)
    parser.add_argument("--seconds", type=int, default=30)
    parser.add_argument("--gpu", action="store_true", help="also sample optional DRM engine busy-time counters")
    args = parser.parse_args()
    if args.pid < 1 or not 1 <= args.seconds <= 60:
        parser.error("positive PID and duration 1..60 required")
    previous = sample(args.pid)
    if args.pid not in previous:
        parser.error("process not running")
    identity = previous[args.pid][0]
    ticks, peak, maximum_processes = 0, 0, 0
    gpu_totals, gpu_highwater, gpu_inaccessible = {}, {}, 0
    if args.gpu:
        from gpu_usage import sample_gpu, accumulate_busy
        gpu_highwater, gpu_inaccessible = sample_gpu(previous)
    began = time.monotonic()
    deadline = began + args.seconds
    while time.monotonic() < deadline:
        time.sleep(min(.5, max(0, deadline - time.monotonic())))
        current = sample(args.pid)
        if args.pid not in current or current[args.pid][0] != identity:
            raise SystemExit("root process exited or changed identity; measurement incomplete")
        if args.gpu:
            gpu_current, inaccessible = sample_gpu(current)
            gpu_inaccessible += inaccessible
            accumulate_busy(gpu_current, gpu_highwater, gpu_totals)
        for pid, (start, cpu, _) in current.items():
            if pid in previous and previous[pid][0] == start:
                ticks += max(0, cpu - previous[pid][1])
        peak = max(peak, sum(row[2] for row in current.values()))
        maximum_processes = max(maximum_processes, len(current))
        previous = current
    elapsed = time.monotonic() - began
    result = dict(elapsed_seconds=round(elapsed, 3),
        sampled_cpu_percent_one_core=round(100 * ticks / os.sysconf("SC_CLK_TCK") / elapsed, 2),
        peak_summed_rss_mib=round(peak / 1048576, 2), maximum_processes=maximum_processes)
    if args.gpu:
        result.update(sampled_drm_engine_busy_percent={engine: round(100 * busy / (elapsed * 1e9), 3)
                          for engine, busy in gpu_totals.items()} if gpu_totals else None,
                      drm_counters_observed=bool(gpu_highwater),
                      drm_inaccessible_fdinfo_reads=gpu_inaccessible)
    print(json.dumps(result))


if __name__ == "__main__":
    main()
