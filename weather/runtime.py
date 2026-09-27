"""Network/cache boundary. Neither providers nor renderers own this lifecycle."""
from copy import deepcopy
from datetime import datetime, timezone
import json
import math
import os
from pathlib import Path
import tempfile
from urllib.parse import urlencode, urlsplit
from urllib.request import Request, build_opener, HTTPRedirectHandler

UTC = timezone.utc
MAX_BYTES = 2 * 1024 * 1024
REFRESH_SECONDS = 900
STALE_SECONDS = 2700
EXPIRE_SECONDS = 7200
USER_AGENT = "a-weather-app/0.2 (A Weather App; Linux Hyprland desktop weather)"


def instant(value):
    if not isinstance(value, str):
        raise ValueError("timestamp must be ISO8601")
    result = datetime.fromisoformat(value.replace("Z", "+00:00"))
    if result.tzinfo is None:
        raise ValueError("timestamp needs a timezone")
    return result.astimezone(UTC)


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        raise ValueError("weather provider redirect refused")


def fetch_json(url):
    parsed = urlsplit(url)
    if (parsed.scheme != "https" or parsed.hostname not in
            {"api.open-meteo.com", "api.weather.gov", "geocoding-api.open-meteo.com"}
            or parsed.port not in (None, 443) or parsed.username or parsed.password):
        raise ValueError("unsupported weather endpoint")
    request = Request(url, headers={"User-Agent": USER_AGENT,
                                  "Accept": "application/geo+json, application/json"})
    with build_opener(NoRedirect()).open(request, timeout=10) as response:
        raw = response.read(MAX_BYTES + 1)
    if len(raw) > MAX_BYTES:
        raise ValueError("weather response exceeds size limit")
    data = json.loads(raw, parse_constant=lambda s: (_ for _ in ()).throw(ValueError(s)))
    if not isinstance(data, dict):
        raise ValueError("weather response must be an object")
    return data


def alerts_url(location):
    return "https://api.weather.gov/alerts/active?" + urlencode({
        "point": f"{location['latitude']},{location['longitude']}"})


def parse_alerts(payload, now):
    features = payload.get("features")
    if not isinstance(features, list) or len(features) > 256:
        raise ValueError("invalid alert collection")
    alerts = []
    for feature in features:
        p = feature.get("properties") if isinstance(feature, dict) else None
        if not isinstance(p, dict):
            raise ValueError("invalid alert feature")
        # Do not display tests, exercises, cancelled, future or expired alerts.
        if p.get("status") != "Actual" or p.get("messageType") == "Cancel":
            continue
        expires = instant(p["expires"])
        effective = instant(p.get("effective") or p["sent"])
        if expires <= now or effective > now:
            continue
        def text(key, limit):
            value = p.get(key)
            if value is None:
                return None
            if not isinstance(value, str) or len(value) > limit:
                raise ValueError(f"invalid alert {key}")
            return value
        alerts.append({"id": text("id", 2048) or str(feature.get("id", ""))[:2048],
                       "event": text("event", 512), "headline": text("headline", 2048),
                       "severity": text("severity", 64), "urgency": text("urgency", 64),
                       "description": text("description", 32000),
                       "instruction": text("instruction", 16000),
                       "effective": effective.isoformat(), "expires": expires.isoformat(),
                       "source": "National Weather Service"})
    return alerts


def fetch_snapshot(location, now=None, fetcher=fetch_json):
    from .provider import forecast_url, parse_forecast
    now = now or datetime.now(UTC)
    snapshot = parse_forecast(fetcher(forecast_url(location)), location, now)
    try:
        items = parse_alerts(fetcher(alerts_url(location)), now)
        snapshot["alerts"] = {"status": "available", "items": items,
                              "fetched_at": now.isoformat(), "source": "National Weather Service"}
    except Exception as error:
        # An unavailable alert feed is not equivalent to an all-clear.
        snapshot["alerts"] = {"status": "unavailable", "items": [],
                              "fetched_at": None, "error": type(error).__name__}
    return snapshot


def validate_snapshot(snapshot, location):
    if (not isinstance(snapshot, dict) or snapshot.get("schema_version") != 1
            or snapshot.get("location") != location):
        raise ValueError("cache schema or location mismatch")
    instant(snapshot["fetched_at"])
    instant(snapshot["current"]["time"])
    from .mapping import visual_targets
    visual_targets(snapshot["current"])
    for key in ("hourly", "daily"):
        if not isinstance(snapshot.get(key), list) or len(snapshot[key]) > 1000:
            raise ValueError("invalid cached forecast")
    # A cache is data, not trusted executable markup. UI must render strings as text.
    return snapshot


def read_cache(path, location):
    try:
        with Path(path).open("rb") as stream:
            raw = stream.read(MAX_BYTES + 1)
        if len(raw) > MAX_BYTES:
            return None
        value = json.loads(raw, parse_constant=lambda s: (_ for _ in ()).throw(ValueError(s)))
        return validate_snapshot(value, location)
    except (OSError, ValueError, TypeError, KeyError):
        return None


def write_cache(path, snapshot):
    path = Path(path)
    encoded = json.dumps(snapshot, allow_nan=False, separators=(",", ":")).encode()
    if len(encoded) > MAX_BYTES:
        raise ValueError("snapshot exceeds cache size limit")
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".weather-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(encoded)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def select_weather(snapshot, *, now=None, mode="live", manual=None, strength="subtle", error=None,
                   reduced_motion=False, lightning_enabled=False):
    from .mapping import visual_targets
    from .solar import solar_position
    from .provider import DEFAULT_LOCATION
    now = now or datetime.now(UTC)
    if not isinstance(reduced_motion, bool) or not isinstance(lightning_enabled, bool):
        raise ValueError("accessibility controls must be boolean")
    if mode not in ("live", "manual"):
        raise ValueError("mode must be live or manual")
    result = {"schema_version": 1, "mode": mode, "selected_at": now.isoformat(),
              "forecast": deepcopy(snapshot), "error": error}
    location = snapshot["location"] if snapshot is not None else DEFAULT_LOCATION
    solar = solar_position(now, location["latitude"], location["longitude"])
    def atmosphere(effects, current, is_manual=False):
        elevation = solar["elevation_deg"]
        azimuth = solar["azimuth_deg"]
        if is_manual:
            elevation = current.get("sun_elevation_deg")
            if elevation is None:
                elevation = 35.0 if current.get("is_day", True) else -25.0
            if isinstance(elevation, bool) or not isinstance(elevation, (int,float)) or not math.isfinite(elevation) or not -90 <= elevation <= 90:
                raise ValueError("invalid manual solar elevation")
            azimuth = 180.0
        effects.update(sun_elevation=elevation, sun_azimuth=azimuth,
                       is_day=elevation >= 0, reduced_motion=reduced_motion,
                       lightning_enabled=lightning_enabled and not reduced_motion)
        return effects
    result["solar"] = solar
    # Forecast remains visible in Manual mode, but expired alerts must not.
    if result["forecast"] is not None:
        alerts = result["forecast"].get("alerts", {})
        try:
            alert_age = (now - instant(alerts["fetched_at"])).total_seconds()
            if not 0 <= alert_age <= STALE_SECONDS:
                raise ValueError("stale alerts")
            alerts["items"] = [a for a in alerts.get("items", []) if instant(a["expires"]) > now]
        except (KeyError, ValueError, TypeError):
            alerts.update(status="unavailable", items=[])
        result["forecast"]["alerts"] = alerts
    if mode == "manual":
        if not isinstance(manual, dict):
            raise ValueError("manual weather is required")
        result.update(current=deepcopy(manual), freshness="manual", age_seconds=None,
                      effects=atmosphere(visual_targets(manual, strength), manual, True))
        return result
    age = None
    if snapshot is not None:
        ages = [(now - instant(snapshot["fetched_at"])).total_seconds(),
                (now - instant(snapshot["current"]["time"])).total_seconds()]
        if min(ages) < -300:
            freshness = "invalid_future"
        else:
            age = max(0, *ages)
            freshness = "fresh" if age <= STALE_SECONDS else "stale" if age <= EXPIRE_SECONDS else "expired"
    else:
        freshness = "unavailable"
    current = deepcopy(snapshot["current"]) if snapshot is not None else {"condition": "unknown"}
    usable = freshness in ("fresh", "stale")
    result.update(current=current, freshness=freshness, age_seconds=age,
                  effects=atmosphere(visual_targets(current if usable else {"condition": "unknown"}, strength), current))
    return result


def load_live(cache_path, location, *, now=None, force=False, fetcher=fetch_json):
    now = now or datetime.now(UTC)
    snapshot = read_cache(cache_path, location)
    error = None
    age = (now - instant(snapshot["fetched_at"])).total_seconds() if snapshot else math.inf
    if force or age < 0 or age >= REFRESH_SECONDS:
        try:
            candidate = fetch_snapshot(location, now, fetcher)
            validate_snapshot(candidate, location)
            write_cache(cache_path, candidate)
            snapshot = candidate
        except Exception as problem:
            error = type(problem).__name__ + ": " + str(problem)[:300]
    return snapshot, error
