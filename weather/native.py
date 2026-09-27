"""Low-frequency control boundary; never loads a plugin or enables effects."""
import json
import math
import re
import subprocess


def request(instance, argument):
    if not re.fullmatch(r"[a-f0-9]+_[0-9]+_[0-9]+", instance):
        raise ValueError("explicit Hyprland instance required")
    value = json.loads(subprocess.check_output(
        ["hyprctl", "-i", instance, "a-weather-app:rain", argument], text=True, timeout=3))
    if not isinstance(value, dict) or value.get("error"):
        raise RuntimeError(f"native weather control failed: {value}")
    return value


def apply_targets(instance, effects, caller=request):
    updates = {key: effects[key] for key in ("rain_intensity", "wind_x")}
    if "snow_intensity" in effects:
        updates["snow_intensity"] = effects["snow_intensity"]
    for key, value in updates.items():
        low, high = (-500, 500) if key == "wind_x" else (0, 1)
        if isinstance(value, bool) or not isinstance(value, (float, int)) or not math.isfinite(value) or not low <= value <= high:
            raise ValueError(f"invalid native {key}")
    status = caller(instance, "status")
    if status.get("enabled") is not True:
        raise RuntimeError("native rain must already be enabled explicitly")
    saved = dict(status["target_parameters"])
    snow_supported = isinstance(status.get("snow_target_parameters"), dict)
    if snow_supported:
        saved["snow_intensity"] = status["snow_target_parameters"]["intensity"]
    elif updates.get("snow_intensity", 0) > 0:
        raise RuntimeError("native plugin does not support snow")
    else:
        updates.pop("snow_intensity", None)
    if not all(key in saved for key in updates):
        raise RuntimeError("native parameter snapshot missing")
    changed = []
    def command(key, value):
        return f"snow intensity {value:.9g}" if key == "snow_intensity" else f"set {key} {value:.9g}"
    try:
        for key, value in updates.items():
            if abs(saved[key] - value) > 1e-6:
                changed.append(key)  # Also restore a successful write with a lost reply.
                caller(instance, command(key, value))
        after = caller(instance, "status")
        actual = dict(after["target_parameters"])
        if snow_supported:
            actual["snow_intensity"] = after["snow_target_parameters"]["intensity"]
        # Native diagnostic JSON currently prints six significant digits.
        if not after.get("enabled") or any(not math.isclose(actual[k], v, rel_tol=1e-5, abs_tol=1e-6) for k, v in updates.items()):
            raise RuntimeError("native target verification failed")
    except Exception as error:
        cleanup_errors = []
        for key in changed:
            try:
                caller(instance, command(key, saved[key]))
            except Exception as failure:
                cleanup_errors.append(str(failure))
        raise RuntimeError(f"native apply failed: {error}; restore errors: {cleanup_errors}") from error
    return {"applied": updates, "changed": changed, "enabled_by_weather": False,
            "native_capabilities": ["rain", "wind"] + (["snow"] if snow_supported else []), "status": after}
