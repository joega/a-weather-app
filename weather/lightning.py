"""Opt-in deterministic, restrained cloud-light pulse; no I/O or bolt rendering."""

import math
from weather.solar import _finite


def lightning_flash(time_seconds, storm=False, enabled=False, reduced_motion=False, seed=0):
    """Return cloud illumination 0..0.28, gated by storm and accessibility.

    Each 24-second interval contains one 0.18-second primary pulse and optionally
    a weak 0.12-second secondary 0.8 seconds later. Thus pulses never approach
    three flashes per second. Seeded integer mixing is independent of Python's
    randomized hash and requires no mutable random state. Time must be nonnegative.
    """
    time_seconds = _finite(time_seconds, "time_seconds")
    if time_seconds < 0:
        raise ValueError("time_seconds must be nonnegative")
    if any(not isinstance(value, bool) for value in (storm, enabled, reduced_motion)):
        raise ValueError("storm, enabled and reduced_motion must be boolean")
    if isinstance(seed, bool) or not isinstance(seed, int):
        raise ValueError("seed must be an integer")
    if not storm or not enabled or reduced_motion:
        return 0.0
    cycle = int(time_seconds // 24)
    mixed = ((cycle + seed) * 1664525 + 1013904223) & 0xffffffff
    onset = 4 + (mixed % 14000) / 1000
    local = time_seconds % 24

    def pulse(start, duration, peak):
        phase = (local - start) / duration
        return peak * math.sin(math.pi * phase) ** 2 if 0 < phase < 1 else 0.0

    value = pulse(onset, 0.18, 0.28)
    if mixed & 1:
        value += pulse(onset + 0.8, 0.12, 0.09)
    return value
