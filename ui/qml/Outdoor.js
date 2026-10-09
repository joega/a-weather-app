.pragma library
.import "Forecast.js" as Forecast

function fields(v, keys) {
    if (!Forecast.object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)))
        throw Error("Invalid outdoor fields");
}
function preferences(v) {
    fields(v, ["schema_version", "hours", "min_temperature_c", "max_temperature_c", "max_probability", "max_hourly_precipitation_mm", "max_wind_m_s", "max_gust_m_s", "daylight_only"]);
    Forecast.integer(v.schema_version, 1, 1);
    Forecast.integer(v.hours, 1, 4);
    Forecast.number(v.min_temperature_c, -80, 60);
    Forecast.number(v.max_temperature_c, v.min_temperature_c, 60);
    Forecast.number(v.max_probability, 0, 1);
    Forecast.number(v.max_hourly_precipitation_mm, 0, 100);
    Forecast.number(v.max_wind_m_s, 0, 75);
    Forecast.number(v.max_gust_m_s, v.max_wind_m_s, 100);
    if (typeof v.daylight_only !== "boolean")
        throw Error("Invalid outdoor daylight preference");
    return JSON.parse(JSON.stringify(v));
}
function context(snapshot) {
    return snapshot ? JSON.stringify([snapshot.latitude, snapshot.longitude, snapshot.timezone, (snapshot.forecast ? snapshot.forecast.fetched_at : null), snapshot.source.freshness, snapshot.location_settings.busy, snapshot.forecast && snapshot.forecast.hourly.length ? snapshot.forecast.hourly[0].time : null]) : "";
}
function query(snapshot, prefs) {
    return {
        latitude: snapshot.latitude,
        longitude: snapshot.longitude,
        timezone: snapshot.timezone,
        forecast_at: (snapshot.forecast ? snapshot.forecast.fetched_at : null),
        preferences: prefs === null ? null : preferences(prefs)
    };
}
function result(v) {
    fields(v, ["preferences", "save_status", "freshness", "forecast_at", "generated_at", "place", "timezone", "windows", "evaluated", "gaps"]);
    preferences(v.preferences);
    Forecast.string(v.place, 244);
    Forecast.string(v.timezone, 80);
    Forecast.time(v.forecast_at);
    Forecast.time(v.generated_at);
    Forecast.integer(v.evaluated, 0, 48);
    Forecast.integer(v.gaps, 0, 240);
    if (["defaults", "saved", "unavailable", "unconfirmed"].indexOf(v.save_status) < 0 || ["fresh", "stale", "expired", "invalid_future", "unavailable"].indexOf(v.freshness) < 0 || !Array.isArray(v.windows) || v.windows.length > 3 || v.windows.length > v.evaluated || (["fresh", "stale"].indexOf(v.freshness) < 0 && v.windows.length))
        throw Error("Invalid outdoor status");
    const reasons = ["temperature", "rain_chance", "rain_amount", "wind", "gusts", "daylight"];
    const seen = [];
    for (const w of v.windows) {
        fields(w, ["start", "end", "range_label", "fits", "missing", "exceeds", "low_c", "high_c", "peak_probability", "peak_hourly_mm", "total_mm", "wind_m_s", "gust_m_s", "daylight"]);
        Forecast.time(w.start);
        Forecast.time(w.end);
        const start = Date.parse(w.start), end = Date.parse(w.end), now = Date.parse(v.generated_at);
        if (end - start !== v.preferences.hours * 3600000 || start < now || end > now + 48 * 3600000 || seen.some(other => start < other.end && other.start < end))
            throw Error("Invalid outdoor interval");
        seen.push({
            start: start,
            end: end
        });
        Forecast.string(w.range_label, 180);
        for (const key of ["missing", "exceeds"])
            if (!Array.isArray(w[key]) || w[key].length > reasons.length || w[key].some((r, i) => reasons.indexOf(r) < 0 || w[key].indexOf(r) !== i))
                throw Error("Invalid outdoor reason");
        if (typeof w.fits !== "boolean" || w.fits !== (w.missing.length === 0 && w.exceeds.length === 0) || ["yes", "no", "unknown", "not_requested"].indexOf(w.daylight) < 0)
            throw Error("Invalid outdoor match");
        const metrics = [["low_c", -150, 100, "temperature"], ["high_c", -150, 100, "temperature"], ["peak_probability", 0, 1, "rain_chance"], ["peak_hourly_mm", 0, 10000, "rain_amount"], ["total_mm", 0, 40000, "rain_amount"], ["wind_m_s", 0, 1000, "wind"], ["gust_m_s", 0, 1000, "gusts"]];
        for (const m of metrics) {
            Forecast.optional(w[m[0]], m[1], m[2]);
            if ((w[m[0]] === null) !== (w.missing.indexOf(m[3]) >= 0))
                throw Error("Invalid outdoor coverage");
        }
        if (w.low_c !== null && w.low_c > w.high_c || v.preferences.daylight_only && (w.daylight === "not_requested" || (w.daylight === "unknown") !== (w.missing.indexOf("daylight") >= 0) || (w.daylight === "no") !== (w.exceeds.indexOf("daylight") >= 0)) || !v.preferences.daylight_only && (w.daylight !== "not_requested" || w.missing.indexOf("daylight") >= 0 || w.exceeds.indexOf("daylight") >= 0))
            throw Error("Invalid outdoor daylight coverage");
    }
    return v;
}
function reasonNames(keys) {
    const names = {
        temperature: "temperature",
        rain_chance: "precipitation chance",
        rain_amount: "precipitation amount",
        wind: "wind",
        gusts: "gusts",
        daylight: "daylight"
    };
    return keys.map(k => names[k]).join(", ");
}
function explanation(w) {
    if (w.fits)
        return "Matches all your preferences";
    const parts = [];
    if (w.exceeds.length)
        parts.push("Outside your preferences: " + reasonNames(w.exceeds));
    if (w.missing.length)
        parts.push("Incomplete data: " + reasonNames(w.missing));
    return parts.join(". ");
}
function metrics(w, units, windUnits) {
    return Forecast.temp(w.low_c, units) + " to " + Forecast.temp(w.high_c, units) + " · Peak hourly precipitation chance " + Forecast.percent(w.peak_probability) + "\n" + Forecast.amount(w.total_mm, units) + " total precipitation · Wind up to " + Forecast.wind(w.wind_m_s, units, windUnits) + " · Gusts " + Forecast.wind(w.gust_m_s, units, windUnits);
}
