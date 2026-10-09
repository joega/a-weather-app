.pragma library
.import "Forecast.js" as Forecast
.import "Astronomy.js" as Calendar

function context(s) {
    return Calendar.context(s);
}
function query(s, date, token) {
    const q = Calendar.query(s, date);
    q.client_token = token;
    return q;
}
function fields(v, keys) {
    if (!Forecast.object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)))
        throw Error("Invalid precipitation fields");
}
function amountValue(v, max) {
    if (v !== null)
        Forecast.number(v, 0, max);
}
function kind(v) {
    if (["unknown", "none", "precipitation", "rain", "snow", "rain_and_snow"].indexOf(v) < 0)
        throw Error("Invalid precipitation type");
}
function result(v, q) {
    fields(v, ["revision", "location_id", "latitude", "longitude", "timezone", "client_token", "requested_date", "date", "today", "max_date", "status", "refreshing", "error", "save_failed", "retry_at", "source", "fetched_at", "fetched_label", "days", "hours"]);
    Forecast.integer(v.revision, 1, 9007199254740991);
    for (const k of ["location_id", "latitude", "longitude", "timezone", "client_token"])
        if (q[k] !== v[k])
            throw Error("Precipitation context mismatch");
    if (q.date !== v.requested_date || (!q.date && v.date !== v.today))
        throw Error("Precipitation date mismatch");
    for (const k of ["date", "today", "max_date"])
        Calendar.date(v[k]);
    if (v.date < v.today || v.date > v.max_date || v.max_date !== Calendar.shiftedDate(v.today, 9) || (q.date && v.date !== (q.date < v.today ? v.today : q.date)))
        throw Error("Invalid precipitation dates");
    if (["fresh", "stale", "loading", "waiting", "unavailable"].indexOf(v.status) < 0 || typeof v.refreshing !== "boolean" || typeof v.save_failed !== "boolean" || v.source !== "Open-Meteo best match")
        throw Error("Invalid precipitation status");
    if (["", "offline", "provider_unavailable", "cache_unavailable", "fetch_failed"].indexOf(v.error) < 0)
        throw Error("Invalid precipitation error");
    Forecast.string(v.fetched_label, 80);
    for (const k of ["fetched_at", "retry_at"])
        if (v[k] !== null)
            Forecast.radarTime(v[k]);
    if (!Array.isArray(v.days) || v.days.length > 10 || !Array.isArray(v.hours) || v.hours.length > 26 || (v.fetched_at === null && (v.days.length || v.hours.length)) || ((v.status === "fresh" || v.status === "stale") !== (v.fetched_at !== null)))
        throw Error("Invalid precipitation horizon");
    let previous = "";
    for (const d of v.days) {
        fields(d, ["date", "start", "end", "label", "duration_hours", "available_hours", "boundary_hours", "kind", "depth_min_m", "depth_max_m", "depth_samples", "freezing_min_m", "freezing_max_m", "freezing_samples", "chance_max", "chance_samples", "total_mm", "total_mm_subtotal", "total_mm_coverage", "rain_mm", "rain_mm_subtotal", "rain_mm_coverage", "snow_cm", "snow_cm_subtotal", "snow_cm_coverage", "wet_hours", "wet_hours_subtotal", "wet_hours_coverage"]);
        Calendar.date(d.date);
        Forecast.radarTime(d.start);
        Forecast.radarTime(d.end);
        if (Date.parse(d.end) - Date.parse(d.start) !== d.duration_hours * 3600000)
            throw Error("Invalid precipitation day bounds");
        if (d.date <= previous || d.date < v.today || d.date > v.max_date)
            throw Error("Invalid precipitation calendar");
        previous = d.date;
        Forecast.string(d.label, 80);
        Forecast.number(d.duration_hours, 22, 26);
        Forecast.integer(d.available_hours, 0, 26);
        Forecast.integer(d.boundary_hours, 0, 2);
        kind(d.kind);
        for (const key of ["total_mm", "rain_mm", "snow_cm", "wet_hours"]) {
            const limit = key === "wet_hours" ? 26 : key === "rain_mm" ? 520000 : 260000;
            amountValue(d[key], limit);
            amountValue(d[key + "_subtotal"], limit);
            Forecast.number(d[key + "_coverage"], 0, d.duration_hours);
            if ((d[key + "_coverage"] === 0) !== (d[key + "_subtotal"] === null) || (d[key] !== null && (d[key + "_coverage"] !== d.duration_hours || d[key] !== d[key + "_subtotal"])))
                throw Error("Invalid precipitation coverage");
        }
        for (const key of ["depth", "freezing"]) {
            const limit = key === "depth" ? 1000 : 30000;
            amountValue(d[key + "_min_m"], limit);
            amountValue(d[key + "_max_m"], limit);
            Forecast.integer(d[key + "_samples"], 0, 26);
            if ((d[key + "_samples"] === 0) !== (d[key + "_min_m"] === null) || (d[key + "_min_m"] === null) !== (d[key + "_max_m"] === null) || d[key + "_min_m"] > d[key + "_max_m"])
                throw Error("Invalid precipitation sample range");
        }
        amountValue(d.chance_max, 1);
        Forecast.integer(d.chance_samples, 0, 26);
        if ((d.chance_samples === 0) !== (d.chance_max === null))
            throw Error("Invalid precipitation chance samples");
    }
    let last = -Infinity;
    for (const h of v.hours) {
        fields(h, ["start", "end", "label", "total_mm", "rain_mm", "snow_cm", "depth_m", "freezing_m", "chance", "kind"]);
        Forecast.radarTime(h.start);
        Forecast.radarTime(h.end);
        if (Date.parse(h.end) - Date.parse(h.start) !== 3600000 || Date.parse(h.start) < last)
            throw Error("Invalid precipitation intervals");
        last = Date.parse(h.end);
        Forecast.string(h.label, 120);
        amountValue(h.total_mm, 10000);
        amountValue(h.rain_mm, 20000);
        amountValue(h.snow_cm, 10000);
        amountValue(h.depth_m, 1000);
        amountValue(h.freezing_m, 30000);
        amountValue(h.chance, 1);
        kind(h.kind);
    }
    const selected = v.days.find(d => d.date === v.date);
    if (v.hours.length && (!selected || selected.available_hours !== v.hours.length || v.hours.some(h => Date.parse(h.start) < Date.parse(selected.start) || Date.parse(h.end) > Date.parse(selected.end))))
        throw Error("Invalid selected precipitation day");
    return v;
}
function indicated(v) {
    return ({
            rain: "Rain indicated",
            snow: "Snow indicated",
            rain_and_snow: "Rain and snow indicated",
            precipitation: "Precipitation indicated",
            none: "No accumulation modeled",
            unknown: "Precipitation type unavailable"
        })[v] || "";
}
function formatted(value, suffix, digits) {
    if (value === null || value === undefined)
        return "—";
    const step = Math.pow(10, -digits);
    if (value > 0 && value < step)
        return "<" + step.toFixed(digits) + " " + suffix;
    return value.toFixed(value === 0 ? 0 : digits) + " " + suffix;
}
function liquid(v, units) {
    return formatted(v === null ? null : units === "F" ? v / 25.4 : v, units === "F" ? "in" : "mm", units === "F" ? 2 : 1);
}
function snow(v, units) {
    return formatted(v === null ? null : units === "F" ? v / 2.54 : v, units === "F" ? "in" : "cm", units === "F" ? 2 : 1);
}
function height(v, units) {
    return formatted(v === null ? null : units === "F" ? v * 3.280839895 : v, units === "F" ? "ft" : "m", 0);
}
function accumulation(day, key, units) {
    if (!day)
        return "—";
    const n = day[key] !== null ? day[key] : day[key + "_subtotal"];
    return key === "snow_cm" ? snow(n, units) : liquid(n, units);
}
function coverage(day, key) {
    if (!day || day[key + "_subtotal"] === null)
        return "Unavailable";
    return (day[key] === null ? "Known subtotal · " : "Full day · ") + day[key + "_coverage"] + " of " + day.duration_hours + " hours";
}
function range(day, key, units) {
    if (!day || day[key + "_min_m"] === null)
        return "Unavailable";
    const format = n => key === "depth" ? snow(n * 100, units) : height(n, units);
    return format(day[key + "_min_m"]) + (day[key + "_min_m"] === day[key + "_max_m"] ? "" : " – " + format(day[key + "_max_m"]));
}
function chartHours(rows) {
    if (!rows.length)
        return [];
    const output = [], end = Date.parse(rows[rows.length - 1].end);
    for (let at = Date.parse(rows[0].start); at < end && output.length < 26; at += 3600000) {
        const h = rows.find(r => Date.parse(r.start) === at);
        output.push({
            time: new Date(at).toISOString(),
            local_hour: h ? h.label.split(" – ")[0] : "Gap",
            local_label: h ? h.label : new Date(at).toISOString(),
            precipitation_rate_mm_hr: h ? h.total_mm : null
        });
    }
    return output;
}
