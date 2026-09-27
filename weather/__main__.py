"""python -m weather: live/manual normalized state; native writes are opt-in."""
import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import sys

from .mapping import STRENGTHS
from .provider import DEFAULT_LOCATION
from .runtime import MAX_BYTES, load_live, read_cache, select_weather


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=("live", "manual"), default="live")
    parser.add_argument("--manual", type=Path, help="JSON current weather object (manual mode only)")
    parser.add_argument("--cache", type=Path, default=Path.home() / ".cache/a-weather-app/weather.json")
    parser.add_argument("--refresh", action="store_true", help="force a provider request")
    parser.add_argument("--offline", action="store_true", help="never use the network")
    parser.add_argument("--strength", choices=tuple(STRENGTHS), default="subtle")
    parser.add_argument("--apply-native", metavar="INSTANCE", help="apply rain/wind targets to an already-enabled plugin")
    args = parser.parse_args()
    if (args.mode == "manual") != bool(args.manual):
        parser.error("manual mode requires --manual; live mode does not accept it")
    if args.offline and args.refresh:
        parser.error("--offline and --refresh are mutually exclusive")
    now = datetime.now(timezone.utc)
    error = None
    if args.offline or args.mode == "manual":
        snapshot = read_cache(args.cache, DEFAULT_LOCATION)
    else:
        snapshot, error = load_live(args.cache, DEFAULT_LOCATION, now=now, force=args.refresh)
    manual = None
    if args.manual:
        with args.manual.open("rb") as stream:
            raw = stream.read(MAX_BYTES + 1)
        if len(raw) > MAX_BYTES:
            raise ValueError("manual input exceeds size limit")
        manual = json.loads(raw)
    value = select_weather(snapshot, now=now, mode=args.mode, manual=manual, strength=args.strength, error=error)
    value["location"] = dict(DEFAULT_LOCATION)
    if args.apply_native:
        from .native import apply_targets
        value["native"] = apply_targets(args.apply_native, value["effects"])
    json.dump(value, sys.stdout, allow_nan=False, indent=2)
    print()


if __name__ == "__main__":
    main()
