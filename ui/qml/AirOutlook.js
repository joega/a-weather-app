.pragma library
.import "Forecast.js" as Forecast
.import "Astronomy.js" as Context
.import "AirQuality.js" as AirQuality

const metrics = [
    {
        key: "us_aqi",
        title: "US AQI",
        unit: "",
        max: 1000,
        scale: "us",
        description: "US index calculated by Open-Meteo. Its pollutant indices use different averaging periods; the overall index is the highest of them."
    },
    {
        key: "european_aqi",
        title: "European AQI",
        unit: "",
        max: 1000,
        scale: "eu",
        description: "European index calculated by Open-Meteo from hourly pollutant concentrations. Its numeric scale differs from US AQI."
    },
    {
        key: "pm2_5",
        title: "PM2.5",
        unit: "µg/m³",
        max: 5000,
        scale: "",
        description: "Fine airborne particles up to 2.5 µm in diameter. A concentration alone does not identify the pollution's source."
    },
    {
        key: "pm10",
        title: "PM10",
        unit: "µg/m³",
        max: 5000,
        scale: "",
        description: "Airborne particles up to 10 µm in diameter, including fine particles."
    },
    {
        key: "nitrogen_dioxide",
        title: "Nitrogen dioxide",
        unit: "µg/m³",
        max: 5000,
        scale: "",
        description: "Modeled near-surface nitrogen dioxide concentration."
    },
    {
        key: "ozone",
        title: "Ozone",
        unit: "µg/m³",
        max: 5000,
        scale: "",
        description: "Modeled near-surface ozone concentration."
    },
    {
        key: "sulphur_dioxide",
        title: "Sulphur dioxide",
        unit: "µg/m³",
        max: 5000,
        scale: "",
        description: "Modeled near-surface sulphur dioxide concentration."
    },
    {
        key: "carbon_monoxide",
        title: "Carbon monoxide",
        unit: "µg/m³",
        max: 100000,
        scale: "",
        description: "Modeled near-surface carbon monoxide concentration."
    }
];
function context(s) {
    return Context.context(s);
}
function query(s, token) {
    const q = Context.query(s, "");
    delete q.date;
    q.client_token = token;
    return q;
}
function fields(v, names) {
    if (!Forecast.object(v) || Object.keys(v).length !== names.length || names.some(k => !Object.prototype.hasOwnProperty.call(v, k)))
        throw Error("Invalid air outlook fields");
}
function result(v, q) {
    fields(v, ["revision", "location_id", "latitude", "longitude", "timezone", "client_token", "status", "refreshing", "error", "save_failed", "retry_at", "source", "attribution", "fetched_at", "fetched_label", "start", "end", "hours"]);
    Forecast.integer(v.revision, 1, 9007199254740991);
    for (const key of ["location_id", "latitude", "longitude", "timezone", "client_token"])
        if (v[key] !== q[key])
            throw Error("Air outlook context mismatch");
    if (["fresh", "stale", "loading", "waiting", "unavailable"].indexOf(v.status) < 0 || typeof v.refreshing !== "boolean" || typeof v.save_failed !== "boolean" || ["", "offline", "provider_unavailable", "cache_unavailable", "fetch_failed"].indexOf(v.error) < 0 || v.source !== "CAMS global model data" || v.attribution !== Forecast.airQuality().attribution)
        throw Error("Invalid air outlook provenance or status");
    Forecast.radarTime(v.start);
    Forecast.radarTime(v.end);
    const start = Date.parse(v.start);
    if (start % 3600000 !== 0 || Date.parse(v.end) - start !== 48 * 3600000)
        throw Error("Invalid air outlook window");
    for (const k of ["fetched_at", "retry_at"])
        if (v[k] !== null)
            Forecast.radarTime(v[k]);
    Forecast.string(v.fetched_label, 80);
    const ready = v.status === "fresh" || v.status === "stale";
    if (ready !== (v.fetched_at !== null) || !Array.isArray(v.hours) || v.hours.length !== (ready ? 48 : 0))
        throw Error("Invalid air outlook data availability");
    for (let i = 0; i < v.hours.length; i++) {
        const h = v.hours[i];
        fields(h, ["time", "label", "local_hour"].concat(metrics.map(m => m.key)));
        Forecast.radarTime(h.time);
        if (Date.parse(h.time) !== start + i * 3600000)
            throw Error("Invalid air outlook hour");
        Forecast.string(h.label, 100);
        Forecast.string(h.local_hour, 60);
        for (const m of metrics)
            if (h[m.key] !== null)
                Forecast.number(h[m.key], 0, m.max);
    }
    return v;
}
function info(key) {
    return metrics.find(m => m.key === key) || metrics[0];
}
function format(value, key, includeUnit) {
    if (value === null || value === undefined)
        return "—";
    const m = info(key);
    if (m.scale)
        return Math.round(value).toString();
    const number = value > 0 && value < .1 ? "<0.1" : value.toFixed(1);
    return number + (includeUnit === false ? "" : " " + m.unit);
}
function category(value, key) {
    return AirQuality.category(value, info(key).scale);
}
function scale(hours, key) {
    const known = hours.map(h => h[key]).filter(v => typeof v === "number" && Number.isFinite(v));
    if (!known.length)
        return null;
    const minimum = Math.min.apply(null, known), maximum = Math.max.apply(null, known);
    return {
        low: 0,
        high: Math.max(1, maximum),
        minimum: minimum,
        maximum: maximum,
        count: known.length
    };
}
function range(hours, key) {
    const r = scale(hours, key);
    return r ? "Available hourly range: " + format(r.minimum, key) + " – " + format(r.maximum, key) + ". " + r.count + " of " + hours.length + " samples available." : "No samples available for this measure in the next 48 hours.";
}
