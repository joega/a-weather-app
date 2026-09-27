"""Provider-independent weather to bounded visual targets; no I/O or activation.

Amounts and cloud cover/probability use mm/hour and fractions (0..1).
Wind direction is meteorological FROM: east wind travels left on screen.
Missing observations produce zero effects; missing daylight defaults to day.
Thunderstorm is descriptive only: callers must separately opt into lightning.
"""

import math

CONDITIONS = frozenset({"clear", "partly_cloudy", "cloudy", "fog", "drizzle",
                        "rain", "snow", "sleet", "thunderstorm", "unknown"})
# Precipitation scale (mm/hr), effect ceiling, wind scale (m/s).
STRENGTHS = {"subtle": (6.0, 0.65, 30.0),
             "normal": (4.0, 0.85, 22.0),
             "immersive": (2.5, 1.0, 15.0)}


def _number(current, key, default=0.0):
    value = current.get(key)
    if value is None:
        return default
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{key} must be a finite number or null")
    try:
        result = float(value)
    except OverflowError as error:
        raise ValueError(f"{key} must be finite") from error
    if not math.isfinite(result):
        raise ValueError(f"{key} must be finite")
    return result


def _clamp(value, low=0.0, high=1.0):
    return max(low, min(high, value))


def visual_targets(current: dict, strength="subtle") -> dict:
    """Return native rain/wind and atmosphere targets without mutating input.

    Invalid types, non-finite numbers, condition names and strengths raise
    ValueError. Finite out-of-range observations are clamped. Probability never
    creates precipitation. Positive measured precipitation with unknown condition
    is conservatively left unrendered; sleet splits rain and snow equally.
    """
    if not isinstance(current, dict):
        raise ValueError("current must be a dictionary")
    if not isinstance(strength, str) or strength not in STRENGTHS:
        raise ValueError("unknown strength")
    condition = current.get("condition", "unknown")
    if not isinstance(condition, str) or condition not in CONDITIONS:
        raise ValueError("unknown condition")
    daylight = current.get("is_day", True)
    if daylight is None:
        daylight = True
    if not isinstance(daylight, bool):
        raise ValueError("is_day must be boolean or null")
    rate = max(0.0, _number(current, "precipitation_rate_mm_hr"))
    # Validate optional observations even when they do not affect this effect.
    _number(current, "precipitation_probability")
    _number(current, "probability")
    _number(current, "temperature_c")
    _number(current, "wind_gust_m_s")
    speed = _clamp(_number(current, "wind_speed_m_s"), 0.0, 100.0)
    direction = _number(current, "wind_direction_deg", None)
    clouds = _clamp(_number(current, "cloud_cover"))
    visibility = _number(current, "visibility_m", None)
    rain_scale, ceiling, wind_scale = STRENGTHS[strength]
    precipitation = ceiling * (-math.expm1(-min(rate, 1000.0) / rain_scale))
    rain = precipitation if condition in {"rain", "drizzle", "thunderstorm"} else 0.0
    snow = precipitation if condition == "snow" else 0.0
    if condition == "sleet":
        rain = snow = precipitation * 0.5
    # -sin converts FROM bearing to east-positive movement. Missing direction
    # cannot establish horizontal motion. Native bounds are -500..500.
    horizontal = 0.0 if direction is None else -math.sin(math.radians(direction % 360)) * speed
    wind = 500.0 * math.tanh(horizontal / wind_scale)
    if abs(wind) < 1e-10:
        wind = 0.0
    fog = 0.0 if visibility is None else _clamp((2000.0 - max(0.0, visibility)) / 2000.0)
    return {"rain_intensity": _clamp(rain), "wind_x": _clamp(wind, -500.0, 500.0),
            "snow_intensity": _clamp(snow), "cloud_cover": clouds,
            "fog_density": fog * ceiling, "is_day": daylight,
            "thunderstorm": condition == "thunderstorm"}
