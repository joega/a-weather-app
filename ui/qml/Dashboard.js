.pragma library

const sections = ["hourly", "daily", "metrics", "maps", "air_quality"];
const metrics = ["wind", "humidity", "visibility", "solar", "uv", "pressure"];
const hourly = ["temperature_c", "apparent_temperature_c", "precipitation_probability", "wind_speed_m_s", "wind_gust_m_s", "humidity", "uv_index"];
function title(id) {
    return ({
            hourly: "Hourly forecast",
            daily: "10-day forecast",
            metrics: "Current metrics",
            maps: "Weather maps",
            air_quality: "Air quality",
            wind: "Wind",
            humidity: "Humidity",
            visibility: "Visibility",
            solar: "Sunrise & sunset",
            uv: "UV index",
            pressure: "Pressure",
            temperature_c: "Temperature",
            apparent_temperature_c: "Feels like",
            precipitation_probability: "Precipitation chance",
            wind_speed_m_s: "Wind",
            wind_gust_m_s: "Gusts",
            uv_index: "UV index"
        })[id] || "";
}
function defaults() {
    return {
        schema_version: 1,
        density: "spacious",
        sections: sections.map(id => ({
                    id: id,
                    enabled: true
                })),
        metrics: metrics.map(id => ({
                    id: id,
                    enabled: true
                })),
        hourly: ["temperature_c", "precipitation_probability"]
    };
}
function object(v, keys) {
    return v !== null && typeof v === "object" && !Array.isArray(v) && Object.keys(v).length === keys.length && keys.every(k => Object.prototype.hasOwnProperty.call(v, k));
}
function preferences(v) {
    if (!object(v, ["schema_version", "density", "sections", "metrics", "hourly"]) || v.schema_version !== 1 || ["compact", "spacious"].indexOf(v.density) < 0)
        throw Error("Invalid dashboard preferences");
    for (const key of ["sections", "metrics"]) {
        const ids = key === "sections" ? sections : metrics;
        if (!Array.isArray(v[key]) || v[key].length !== ids.length)
            throw Error("Invalid dashboard rows");
        let seen = [];
        for (const row of v[key]) {
            if (!object(row, ["id", "enabled"]) || ids.indexOf(row.id) < 0 || seen.indexOf(row.id) >= 0 || typeof row.enabled !== "boolean")
                throw Error("Invalid dashboard row");
            seen.push(row.id);
        }
        if (key === "metrics" && !v[key].some(row => row.enabled))
            throw Error("Dashboard requires a metric");
    }
    if (!Array.isArray(v.hourly) || v.hourly.length < 1 || v.hourly.length > 3 || v.hourly.some((id, i) => hourly.indexOf(id) < 0 || v.hourly.indexOf(id) !== i))
        throw Error("Invalid dashboard hourly values");
    return {
        schema_version: 1,
        density: v.density,
        sections: v.sections.map(row => ({
                    id: row.id,
                    enabled: row.enabled
                })),
        metrics: v.metrics.map(row => ({
                    id: row.id,
                    enabled: row.enabled
                })),
        hourly: v.hourly.slice()
    };
}
function state(v) {
    if (v === undefined)
        return {
            preferences: defaults(),
            revision: 1,
            error: null
        };
    if (!object(v, ["preferences", "revision", "error"]) || !Number.isSafeInteger(v.revision) || v.revision < 1 || [null, "state_unavailable", "save_unconfirmed"].indexOf(v.error) < 0)
        throw Error("Invalid dashboard state");
    return {
        preferences: preferences(v.preferences),
        revision: v.revision,
        error: v.error
    };
}
// Group adjacent daily/metric sections only. Rows and children retain the saved
// order, including the visual and keyboard order when the pair is reversed.
function rows(sections, paired) {
    const ids = sections.filter(row => row.enabled).map(row => row.id), result = [];
    for (let i = 0; i < ids.length; i++) {
        if (paired && i + 1 < ids.length && [ids[i], ids[i + 1]].sort().join(",") === "daily,metrics")
            result.push([ids[i], ids[++i]]);
        else
            result.push([ids[i]]);
    }
    return result;
}
