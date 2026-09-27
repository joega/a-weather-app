"""Explicit location overrides; no settings or cache writes.

Postal-code search and country filtering are documented at
https://open-meteo.com/en/docs/geocoding-api . A result must contain the
exact requested ZIP in its postcodes; prefix matches are never accepted.
"""
import json
import math
import re
from urllib.parse import urlencode
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from .runtime import fetch_json, USER_AGENT

GEOCODING_URL = "https://geocoding-api.open-meteo.com/v1/search"
MAX_RESULTS = 10
MAX_POSTCODES = 2048
LOCAL_LOCATION_URL = ("https://ipwho.is/?fields=success,city,region,region_code,"
                      "country_code,latitude,longitude,timezone.id")
LOCAL_MAX_BYTES = 16 * 1024
_BOSTON = {"name": "Boston, MA", "latitude": 42.3601,
           "longitude": -71.0589, "timezone": "America/New_York"}
_STATES = dict(pair.split(":") for pair in (
    "Alabama:AL|Alaska:AK|Arizona:AZ|Arkansas:AR|California:CA|Colorado:CO|"
    "Connecticut:CT|Delaware:DE|District of Columbia:DC|Florida:FL|Georgia:GA|"
    "Hawaii:HI|Idaho:ID|Illinois:IL|Indiana:IN|Iowa:IA|Kansas:KS|Kentucky:KY|"
    "Louisiana:LA|Maine:ME|Maryland:MD|Massachusetts:MA|Michigan:MI|Minnesota:MN|"
    "Mississippi:MS|Missouri:MO|Montana:MT|Nebraska:NE|Nevada:NV|New Hampshire:NH|"
    "New Jersey:NJ|New Mexico:NM|New York:NY|North Carolina:NC|North Dakota:ND|"
    "Ohio:OH|Oklahoma:OK|Oregon:OR|Pennsylvania:PA|Rhode Island:RI|"
    "South Carolina:SC|South Dakota:SD|Tennessee:TN|Texas:TX|Utah:UT|Vermont:VT|"
    "Virginia:VA|Washington:WA|West Virginia:WV|Wisconsin:WI|Wyoming:WY|"
    "Puerto Rico:PR|Guam:GU|American Samoa:AS|Northern Mariana Islands:MP|"
    "U.S. Virgin Islands:VI").split("|"))


class LocationError(ValueError):
    """An explicit location could not be resolved safely."""
    def __init__(self, message, code="location_failed"):
        super().__init__(message)
        self.code = code


def validate_zip_code(value):
    """Preserve leading zeroes and accept exactly five ASCII digits."""
    if not isinstance(value, str) or re.fullmatch(r"[0-9]{5}", value) is None:
        raise LocationError("ZIP code must contain exactly five digits", "invalid_zip")
    return value


def _text(value, label, limit=120):
    if (not isinstance(value, str) or not value or len(value) > limit
            or value != value.strip() or any(not c.isprintable() for c in value)):
        raise LocationError(f"invalid location {label}")
    return value


def _coordinate(value, label, bound):
    if (isinstance(value, bool) or not isinstance(value, (int, float))
            or not -bound <= value <= bound or not math.isfinite(value)):
        raise LocationError(f"invalid location {label}")
    return value


def validate_location(value):
    """Validate the existing four-field location schema at a state boundary."""
    if not isinstance(value, dict) or set(value) != set(_BOSTON):
        raise LocationError("invalid location fields")
    timezone = _text(value.get("timezone"), "timezone", 100)
    try:
        ZoneInfo(timezone)
    except (ValueError, ZoneInfoNotFoundError) as error:
        raise LocationError("invalid location timezone") from error
    return {"name": _text(value.get("name"), "name", 244),
            "latitude": _coordinate(value.get("latitude"), "latitude", 90),
            "longitude": _coordinate(value.get("longitude"), "longitude", 180),
            "timezone": timezone}


def _candidate(result):
    if not isinstance(result, dict) or result.get("country_code") != "US":
        raise LocationError("geocoding result must be in the United States")
    name = _text(result.get("name"), "name")
    state = _text(result.get("admin1"), "state")
    state = _STATES.get(state, state)
    timezone = _text(result.get("timezone"), "timezone", 100)
    try:
        ZoneInfo(timezone)
    except (ValueError, ZoneInfoNotFoundError) as error:
        raise LocationError("invalid location timezone") from error
    return {"name": _text(f"{name}, {state}", "display name", 244),
            "latitude": _coordinate(result.get("latitude"), "latitude", 90),
            "longitude": _coordinate(result.get("longitude"), "longitude", 180),
            "timezone": timezone}


def resolve_zip_code(zip_code, *, fetcher=fetch_json):
    """Resolve one unambiguous US ZIP, using a fixed HTTPS origin.

    The shared fetcher rejects redirects and caps response bytes at 2 MiB.
    Custom fetchers are intended for tests and must return parsed JSON data.
    """
    zip_code = validate_zip_code(zip_code)
    url = GEOCODING_URL + "?" + urlencode({"name": zip_code,
        "countryCode": "US", "count": MAX_RESULTS, "language": "en", "format": "json"})
    try:
        payload = fetcher(url)
    except (OSError, ValueError) as error:
        raise LocationError("ZIP lookup failed; use --demo-location boston for offline demos",
                            "lookup_failed") from error
    if not isinstance(payload, dict) or payload.get("error"):
        raise LocationError("invalid geocoding response")
    results = payload.get("results", [])
    if not isinstance(results, list) or len(results) >= MAX_RESULTS:
        raise LocationError("invalid or truncated geocoding results")
    matches = []
    for result in results:
        location = _candidate(result)
        postcodes = result.get("postcodes")
        if (not isinstance(postcodes, list) or len(postcodes) > MAX_POSTCODES
                or any(not isinstance(p, str) or re.fullmatch(r"[0-9]{5}", p) is None
                       for p in postcodes)):
            raise LocationError("invalid geocoding postal codes")
        if zip_code in postcodes:
            matches.append(location)
    if not matches:
        raise LocationError(f"no exact US match for ZIP {zip_code}", "zip_not_found")
    if len(matches) != 1:
        raise LocationError(f"ZIP {zip_code} has multiple matches; choose --demo-location boston for Boston",
                            "zip_ambiguous")
    return matches[0]


def _check_local_tree(value):
    nodes = 0
    def check(item, depth):
        nonlocal nodes
        nodes += 1
        if nodes > 64 or depth > 4:
            raise ValueError("local location response structure")
        if isinstance(item, dict):
            if len(item) > 16:
                raise ValueError("local location response object")
            for key, child in item.items():
                if not isinstance(key, str) or len(key) > 64:
                    raise ValueError("local location response key")
                check(child, depth + 1)
        elif isinstance(item, list):
            raise ValueError("local location response array")
        elif isinstance(item, str):
            if len(item) > 256:
                raise ValueError("local location response text")
        elif item is not None and not isinstance(item, bool):
            if (not isinstance(item, (int, float)) or not -1e9 <= item <= 1e9
                    or not math.isfinite(item)):
                raise ValueError("local location response number")
    check(value, 0)


def _decode_local_json(raw):
    if len(raw) > LOCAL_MAX_BYTES:
        raise ValueError("local location response size")
    # Bound nesting before the decoder constructs objects, including ignored fields.
    depth, quoted, escaped = 0, False, False
    for byte in raw:
        if quoted:
            if escaped:
                escaped = False
            elif byte == 92:
                escaped = True
            elif byte == 34:
                quoted = False
        elif byte == 34:
            quoted = True
        elif byte in (91, 123):
            depth += 1
            if depth > 4:
                raise ValueError("local location response depth")
        elif byte in (93, 125):
            depth -= 1
    def pairs(items):
        if len(items) > 16:
            raise ValueError("local location response object")
        result = {}
        for key, value in items:
            if key in result:
                raise ValueError("local location duplicate field")
            result[key] = value
        return result
    value = json.loads(raw, object_pairs_hook=pairs,
        parse_constant=lambda _: (_ for _ in ()).throw(ValueError("local location number")))
    _check_local_tree(value)
    return value


def fetch_local_json(url):
    """Fetch only the selected IP geolocation fields from one fixed endpoint.

    This is called only by an explicit user action. The caller supplies the
    whole-worker deadline; this boundary supplies byte and socket bounds.
    """
    if url != LOCAL_LOCATION_URL:
        raise LocationError("unsupported local location endpoint", "lookup_failed")
    from urllib.request import Request, ProxyHandler, build_opener
    from .network import NoRedirect
    request = Request(url, headers={"User-Agent": USER_AGENT, "Accept": "application/json"})
    try:
        with build_opener(ProxyHandler({}), NoRedirect()).open(request, timeout=10) as response:
            if response.status != 200:
                raise ValueError("local location HTTP status")
            raw = response.read(LOCAL_MAX_BYTES + 1)
        return _decode_local_json(raw)
    except (OSError, ValueError, TypeError, OverflowError) as error:
        raise LocationError("Local location lookup failed", "lookup_failed") from error


def resolve_local_location(*, fetcher=None):
    """Approximate requester location, explicitly requested by the user.

    ipwho.is receives the public IP of this HTTPS request. VPNs and network
    routing affect the result. No IP is requested, retained, stored or logged.
    """
    try:
        payload = (fetcher or fetch_local_json)(LOCAL_LOCATION_URL)
        _check_local_tree(payload)
        allowed = {"success", "city", "region", "region_code", "country_code",
                   "latitude", "longitude", "timezone"}
        if (not isinstance(payload, dict) or set(payload) != allowed
                or payload.get("success") is not True):
            raise ValueError("local location lookup unsuccessful")
        city = _text(payload["city"], "city")
        region = _text(payload["region"], "region")
        country = payload["country_code"]
        if not isinstance(country, str) or re.fullmatch(r"[A-Z]{2}", country) is None:
            raise ValueError("local location country")
        region_code = payload["region_code"]
        if region_code is not None:
            if not isinstance(region_code, str) or len(region_code) > 16:
                raise ValueError("local location region code")
        if country == "US":
            code = _STATES.get(region, region_code)
            if code not in _STATES.values() or (region_code and region_code != code):
                raise ValueError("local location state")
            name = f"{city}, {code}"
        else:
            name = f"{city}, {region}, {country}"
        zone = payload["timezone"]
        if not isinstance(zone, dict) or set(zone) != {"id"}:
            raise ValueError("local location timezone")
        return validate_location({"name": name, "latitude": payload["latitude"],
                                  "longitude": payload["longitude"], "timezone": zone["id"]})
    except (OSError, ValueError, TypeError, OverflowError) as error:
        raise LocationError("Local location lookup failed", "lookup_failed") from error


def demo_location(name):
    """Return a fresh offline preset without changing the primary location."""
    if name != "boston":
        raise LocationError("unknown demo location; available: boston")
    return dict(_BOSTON)


def select_location(*, zip_code=None, demo_location=None, fetcher=fetch_json):
    """Return an override or None; CLI options must be mutually exclusive."""
    if zip_code is not None and demo_location is not None:
        raise LocationError("--zip-code and --demo-location are mutually exclusive")
    if demo_location is not None:
        if demo_location != "boston":
            raise LocationError("unknown demo location; available: boston")
        return dict(_BOSTON)
    if zip_code is not None:
        return resolve_zip_code(zip_code, fetcher=fetcher)
    return None
