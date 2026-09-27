"""Pure geometric sun position for visual lighting (no atmospheric refraction).

Uses NOAA's fractional-year Fourier approximation:
https://gml.noaa.gov/grad/solcalc/solareqns.PDF
This is an approximate visual model, not the higher precision Meeus calculator,
an astronomical ephemeris, or a moon position model. No accuracy bound is claimed.
Azimuth is clockwise from north; elevation is above the geometric horizon.
"""

import calendar
from datetime import datetime, timezone
import math


def _finite(value, name):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise ValueError(f"{name} must be finite")
    try:
        value = float(value)
    except OverflowError as error:
        raise ValueError(f"{name} must be finite") from error
    if not math.isfinite(value):
        raise ValueError(f"{name} must be finite")
    return value


def solar_position(instant, latitude, longitude):
    """Return elevation_deg, azimuth_deg, is_day, twilight and daylight (0..1).

    An aware datetime is required; equivalent instants in any timezone give
    identical results. Coordinates use degrees, with east longitude positive.
    Twilight is day/civil/nautical/astronomical/night. Daylight is a smooth visual
    ramp from civil twilight (-6 degrees) to 6 degrees above the horizon.
    Azimuth at the exact zenith/nadir is conventionally zero (undefined in nature).
    """
    if not isinstance(instant, datetime) or instant.utcoffset() is None:
        raise ValueError("instant must be a timezone-aware datetime")
    lat = _finite(latitude, "latitude")
    lon = _finite(longitude, "longitude")
    if not -90 <= lat <= 90 or not -180 <= lon <= 180:
        raise ValueError("coordinates out of range")
    utc = instant.astimezone(timezone.utc)
    hour = utc.hour + utc.minute / 60 + (utc.second + utc.microsecond / 1e6) / 3600
    gamma = 2 * math.pi / (366 if calendar.isleap(utc.year) else 365) * (utc.timetuple().tm_yday - 1 + (hour - 12) / 24)
    eqtime = 229.18 * (0.000075 + 0.001868 * math.cos(gamma) - 0.032077 * math.sin(gamma)
                       - 0.014615 * math.cos(2 * gamma) - 0.040849 * math.sin(2 * gamma))
    decl = (0.006918 - 0.399912 * math.cos(gamma) + 0.070257 * math.sin(gamma)
            - 0.006758 * math.cos(2 * gamma) + 0.000907 * math.sin(2 * gamma)
            - 0.002697 * math.cos(3 * gamma) + 0.00148 * math.sin(3 * gamma))
    ha = math.radians(((hour * 60 + eqtime + 4 * lon) % 1440) / 4 - 180)
    lat = math.radians(lat)
    up = math.sin(lat) * math.sin(decl) + math.cos(lat) * math.cos(decl) * math.cos(ha)
    elevation = math.degrees(math.asin(max(-1, min(1, up))))
    east = -math.cos(decl) * math.sin(ha)
    north = math.cos(lat) * math.sin(decl) - math.sin(lat) * math.cos(decl) * math.cos(ha)
    azimuth = math.degrees(math.atan2(east, north)) % 360 if math.hypot(east, north) > 1e-12 else 0.0
    twilight = next((label for threshold, label in ((0, "day"), (-6, "civil"), (-12, "nautical"), (-18, "astronomical"))
                     if elevation >= threshold), "night")
    ramp = max(0.0, min(1.0, (elevation + 6) / 12))
    return {"elevation_deg": elevation, "azimuth_deg": azimuth, "is_day": elevation >= 0,
            "twilight": twilight, "daylight": ramp * ramp * (3 - 2 * ramp)}
