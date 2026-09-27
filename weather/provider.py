"""Pure Open-Meteo adapter; no network, compositor, or renderer dependencies.

Units and accumulation semantics: https://open-meteo.com/en/docs
Daily records describe the requested location's calendar days.
"""
from datetime import datetime, timezone
import math
from urllib.parse import urlencode
from zoneinfo import ZoneInfo

# Last fallback only: saved automatic locations and explicit overrides take priority.
DEFAULT_LOCATION = {"name": "New York, NY", "latitude": 40.7128,
                    "longitude": -74.0060, "timezone": "America/New_York"}
CURRENT = {"temperature_2m": "°C", "apparent_temperature": "°C",
           "relative_humidity_2m": "%", "cloud_cover": "%",
           "precipitation": "mm", "weather_code": "wmo code",
           "wind_speed_10m": "m/s", "wind_direction_10m": "°",
           "wind_gusts_10m": "m/s", "is_day": ""}
HOURLY = dict(CURRENT, precipitation_probability="%", visibility="m")
DAILY = {"temperature_2m_max": "°C", "temperature_2m_min": "°C",
         "sunrise": "unixtime", "sunset": "unixtime",
         "precipitation_probability_max": "%", "weather_code": "wmo code"}

class ForecastError(ValueError):
    """A response cannot safely be normalized."""

def _number(value, label, minimum=None, maximum=None, optional=False):
    if value is None and optional:
        return None
    if isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ForecastError(f"{label}: expected finite number")
    if minimum is not None and value < minimum or maximum is not None and value > maximum:
        raise ForecastError(f"{label}: out of range")
    return value

def _time(value):
    _number(value, "time")
    try:
        return datetime.fromtimestamp(value, timezone.utc).isoformat().replace("+00:00", "Z")
    except (ValueError, OverflowError, OSError) as error:
        raise ForecastError("time: out of range") from error

def _location(location):
    if not isinstance(location, dict) or not isinstance(location.get("name"), str) or not location["name"]:
        raise ForecastError("location: missing name")
    _number(location.get("latitude"), "latitude", -90, 90)
    _number(location.get("longitude"), "longitude", -180, 180)
    try:
        ZoneInfo(location["timezone"])
    except (KeyError, TypeError, ValueError) as error:
        raise ForecastError("location: invalid timezone") from error
    return {key: location[key] for key in DEFAULT_LOCATION}

def forecast_url(location=DEFAULT_LOCATION):
    location = _location(location)
    return "https://api.open-meteo.com/v1/forecast?" + urlencode({
        "latitude": location["latitude"], "longitude": location["longitude"],
        "current": ",".join(CURRENT), "hourly": ",".join(HOURLY), "daily": ",".join(DAILY),
        "temperature_unit": "celsius", "wind_speed_unit": "ms",
        "precipitation_unit": "mm", "timezone": location["timezone"], "timeformat": "unixtime",
        "forecast_days": 10})

def condition(code):
    _number(code, "weather_code", 0)
    if int(code) != code:
        raise ForecastError("weather_code: expected integer")
    if code == 0: return "clear"
    if code in (1, 2): return "partly_cloudy"
    if code == 3: return "cloudy"
    if code in (45, 48): return "fog"
    if code in (51, 53, 55): return "drizzle"
    if code in (56, 57, 66, 67): return "sleet"
    if code in (61, 63, 65, 80, 81, 82): return "rain"
    if code in (71, 73, 75, 77, 85, 86): return "snow"
    if code in (95, 96, 99): return "thunderstorm"
    return "unknown"

def _units(payload, section, expected):
    units = payload.get(section + "_units")
    if not isinstance(units, dict): raise ForecastError(f"{section}: missing units")
    for key, unit in dict(expected, time="unixtime").items():
        if units.get(key) != unit:
            raise ForecastError(f"{section}.{key}: unexpected unit")

def _records(payload, section, fields):
    data = payload.get(section)
    if not isinstance(data, dict) or not isinstance(data.get("time"), list):
        raise ForecastError(f"{section}: missing time array")
    count = len(data["time"])
    for key in fields:
        if not isinstance(data.get(key), list) or len(data[key]) != count:
            raise ForecastError(f"{section}.{key}: inconsistent array")
    times = data["time"]
    for index, value in enumerate(times):
        _time(value)
        if index and value <= times[index - 1]: raise ForecastError(f"{section}: unordered time")
    return [{key: data[key][index] for key in ("time", *fields)} for index in range(count)]

def _weather(row, optional=False):
    def n(key, lo=None, hi=None):
        return _number(row.get(key), key, lo, hi, optional)
    def fraction(key):
        value = n(key, 0, 100)
        return None if value is None else value / 100
    code = n("weather_code", 0)
    day = n("is_day", 0, 1)
    if day not in (None, 0, 1): raise ForecastError("is_day: expected 0 or 1")
    return {"time": _time(row["time"]), "temperature_c": n("temperature_2m", -150, 100),
            "apparent_temperature_c": n("apparent_temperature", -200, 150),
            "humidity": fraction("relative_humidity_2m"), "cloud_cover": fraction("cloud_cover"),
            "precipitation_rate_mm_hr": n("precipitation", 0),
            "weather_code": None if code is None else int(code),
            "condition": "unknown" if code is None else condition(code),
            "wind_speed_m_s": n("wind_speed_10m", 0),
            "wind_direction_deg": n("wind_direction_10m", 0, 360),
            "wind_gust_m_s": n("wind_gusts_10m", 0),
            "is_day": None if day is None else bool(day), "sun_elevation_deg": None}

def parse_forecast(payload, location=DEFAULT_LOCATION, fetched_at=None):
    """Normalize a decoded response; reject incompatible units and malformed data.

    Unknown optional forecast values remain None. Current core values are required.
    Hourly precipitation is the preceding-hour sum, numerically mm/hour. Current
    precipitation is divided by its documented backward-looking interval.
    """
    location = _location(location)
    if not isinstance(payload, dict): raise ForecastError("payload: expected object")
    if "timezone" in payload and payload["timezone"] != location["timezone"]:
        raise ForecastError("unexpected response timezone")
    for section, fields in (("current", CURRENT), ("hourly", HOURLY), ("daily", DAILY)):
        _units(payload, section, fields)
    if payload["current_units"].get("interval") != "seconds":
        raise ForecastError("current.interval: unexpected unit")
    row = payload.get("current")
    if not isinstance(row, dict): raise ForecastError("current: expected object")
    current = _weather(row)
    interval = _number(row.get("interval"), "interval", 1)
    current["precipitation_rate_mm_hr"] *= 3600 / interval
    current.update(precipitation_probability=None, visibility_m=None)
    hourly = []
    for item in _records(payload, "hourly", HOURLY):
        record = _weather(item, optional=True)
        probability = _number(item["precipitation_probability"], "probability", 0, 100, True)
        record["precipitation_probability"] = None if probability is None else probability / 100
        record["visibility_m"] = _number(item["visibility"], "visibility", 0, optional=True)
        # Probability describes (timestamp - 1h, timestamp]; visibility is instant.
        if item["time"] - 3600 < row["time"] <= item["time"]:
            current["precipitation_probability"] = record["precipitation_probability"]
        if item["time"] <= row["time"] < item["time"] + 3600:
            current["visibility_m"] = record["visibility_m"]
        hourly.append(record)
    daily = []
    for item in _records(payload, "daily", DAILY):
        code = _number(item["weather_code"], "weather_code", 0, optional=True)
        probability = _number(item["precipitation_probability_max"], "probability", 0, 100, True)
        high = _number(item["temperature_2m_max"], "high", -150, 100, True)
        low = _number(item["temperature_2m_min"], "low", -150, 100, True)
        if high is not None and low is not None and low > high: raise ForecastError("daily: low exceeds high")
        date = datetime.fromtimestamp(item["time"], ZoneInfo(location["timezone"])).date().isoformat()
        daily.append({"date": date, "high_c": high, "low_c": low,
                      "sunrise": None if item["sunrise"] is None else _time(item["sunrise"]),
                      "sunset": None if item["sunset"] is None else _time(item["sunset"]),
                      "precipitation_probability": None if probability is None else probability / 100,
                      "condition": "unknown" if code is None else condition(code)})
    fetched_at = datetime.now(timezone.utc) if fetched_at is None else fetched_at
    if not isinstance(fetched_at, datetime) or fetched_at.tzinfo is None or fetched_at.utcoffset() is None:
        raise ForecastError("fetched_at: expected timezone-aware datetime")
    return {"schema_version": 1, "location": location,
            "source": {"name": "Open-Meteo", "url": "https://open-meteo.com/",
                       "attribution": "Weather data by Open-Meteo.com (CC BY 4.0)"},
            "fetched_at": fetched_at.astimezone(timezone.utc).isoformat().replace("+00:00", "Z"),
            "current": current, "hourly": hourly, "daily": daily}
