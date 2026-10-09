.pragma library
.import "Forecast.js" as Forecast

function fields(v, keys) {
    if (!Forecast.object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)))
        throw Error("Invalid comparison fields");
}
function query(s) {
    return {
        location_id: s.saved_locations.viewed,
        latitude: s.latitude,
        longitude: s.longitude,
        timezone: s.timezone,
        forecast_at: s.forecast.fetched_at
    };
}
function context(s) {
    if (!s || !s.forecast || !s.saved_locations || s.location_settings.mode === "default" || s.location_settings.busy || ["fresh", "stale"].indexOf(s.source.freshness) < 0 || !s.forecast.hourly.length)
        return "";
    return JSON.stringify([query(s), s.source.freshness, s.forecast.hourly[0].time]);
}
function result(v, q) {
    fields(v, ["status", "location_id", "latitude", "longitude", "timezone", "source", "current_retrieved", "previous_retrieved", "current_retrieved_label", "previous_retrieved_label", "start", "end", "save_status", "history_recovered", "coverage", "changes"]);
    if (["ready", "no_previous", "current_unavailable", "place_changed", "source_changed", "not_newer", "baseline_too_old", "no_overlap", "insufficient_data"].indexOf(v.status) < 0 || ["saved", "unconfirmed"].indexOf(v.save_status) < 0 || typeof v.history_recovered !== "boolean" || v.source !== "Open-Meteo" || !Array.isArray(v.changes) || v.changes.length > 3 || (v.status !== "ready" && v.changes.length))
        throw Error("Invalid comparison state");
    Forecast.string(v.location_id, 96);
    Forecast.number(v.latitude, -90, 90);
    Forecast.number(v.longitude, -180, 180);
    Forecast.string(v.timezone, 100);
    for (const key of ["current_retrieved", "start", "end"])
        Forecast.radarTime(v[key]); // Same bounded UTC nanosecond representation.
    Forecast.string(v.current_retrieved_label, 100);
    if (v.previous_retrieved !== null) {
        Forecast.radarTime(v.previous_retrieved);
        Forecast.string(v.previous_retrieved_label, 100);
    } else if (v.previous_retrieved_label !== null)
        throw Error("Invalid previous retrieval label");
    if (q.location_id !== v.location_id || q.latitude !== v.latitude || q.longitude !== v.longitude || q.timezone !== v.timezone || Date.parse(q.forecast_at) !== Date.parse(v.current_retrieved) || Date.parse(v.end) - Date.parse(v.start) !== 48 * 3600000 || (v.status === "ready" && (v.previous_retrieved === null || Date.parse(v.previous_retrieved) >= Date.parse(v.current_retrieved))))
        throw Error("Comparison context mismatch");
    const c = v.coverage;
    fields(c, ["expected_points", "expected_intervals", "temperature", "probability", "precipitation", "gusts"]);
    for (const key of Object.keys(c))
        Forecast.integer(c[key], 0, 48);
    if (c.temperature > c.expected_points || ["probability", "precipitation", "gusts"].some(k => c[k] > c.expected_intervals))
        throw Error("Invalid comparison coverage");
    const seen = [];
    for (const r of v.changes) {
        fields(r, ["kind", "start", "end", "range_label", "samples", "before", "after", "previous_start", "previous_end", "previous_range_label"]);
        if (["temperature", "probability", "precipitation", "gusts", "wet_hours"].indexOf(r.kind) < 0 || seen.indexOf(r.kind) >= 0)
            throw Error("Invalid change kind");
        seen.push(r.kind);
        Forecast.radarTime(r.start);
        Forecast.radarTime(r.end);
        Forecast.string(r.range_label, 160);
        Forecast.integer(r.samples, 1, 48);
        const start = Date.parse(r.start), end = Date.parse(r.end);
        if (start < Date.parse(v.start) || end > Date.parse(v.end) || end < start)
            throw Error("Invalid change period");
        if (r.kind === "wet_hours") {
            Forecast.radarTime(r.previous_start);
            Forecast.radarTime(r.previous_end);
            Forecast.string(r.previous_range_label, 160);
            if (r.before !== null || r.after !== null || Date.parse(r.previous_start) < Date.parse(v.start) || Date.parse(r.previous_end) > Date.parse(v.end) || Date.parse(r.previous_start) >= Date.parse(r.previous_end) || start >= end)
                throw Error("Invalid wet window");
        } else {
            const bounds = {
                temperature: [-150, 100],
                probability: [0, 1],
                precipitation: [0, 10000],
                gusts: [0, 1000]
            }[r.kind];
            Forecast.number(r.before, bounds[0], bounds[1]);
            Forecast.number(r.after, bounds[0], bounds[1]);
            if (r.previous_start !== null || r.previous_end !== null || r.previous_range_label !== null || r.samples > c[r.kind] || end - start !== (r.samples - (r.kind === "temperature" ? 1 : 0)) * 3600000)
                throw Error("Invalid numeric change");
        }
    }
    return v;
}
function title(r, units) {
    const higher = r.after > r.before;
    if (r.kind === "temperature")
        return Math.round(Math.abs(r.after - r.before) * (units === "F" ? 1.8 : 1)) + "°" + units + (higher ? " warmer" : " cooler");
    if (r.kind === "probability")
        return "Precipitation chance " + Math.round(Math.abs(r.after - r.before) * 100) + " points " + (higher ? "higher" : "lower");
    if (r.kind === "precipitation")
        return higher ? "More precipitation expected" : "Less precipitation expected";
    if (r.kind === "gusts")
        return higher ? "Stronger gusts expected" : "Lighter gusts expected";
    return "High precipitation-chance hours shifted " + (Date.parse(r.start) > Date.parse(r.previous_start) ? "later" : "earlier");
}
function values(r, units, windUnits) {
    if (r.kind === "wet_hours")
        return "Previously: " + r.previous_range_label + "\nNow: " + r.range_label + "\nModeled hours with at least 50% precipitation chance.";
    const format = n => r.kind === "temperature" ? Forecast.temp(n, units) + units : r.kind === "probability" ? Forecast.percent(n) : r.kind === "gusts" ? Forecast.wind(n, units, windUnits) : units === "F" ? (n / 25.4).toFixed(3) + " in/hour" : n.toFixed(1) + " mm/hour";
    const label = {
        temperature: "Temperature",
        probability: "Precipitation chance",
        precipitation: "Liquid-equivalent precipitation",
        gusts: "Hourly maximum gust"
    }[r.kind];
    return label + " averages " + format(r.before) + " → " + format(r.after) + " across " + r.samples + (r.samples === 1 ? " hour." : " hours.");
}
function summary(v, state, units) {
    if (!v)
        return state === "unavailable" ? "Comparison unavailable for this forecast." : state === "waiting" ? "A comparison will appear when the latest forecast is shown." : "Checking forecast changes…";
    if (v.changes.length)
        return title(v.changes[0], units) + (v.changes.length > 1 ? " · " + (v.changes.length - 1) + " more" : "");
    if (v.status === "ready")
        return "No major changes in the compared hours.";
    return ({
            no_previous: "A comparison will be available after a newer forecast is shown.",
            baseline_too_old: "Your previous forecast is too old to compare.",
            no_overlap: "These forecasts have no matching future hours.",
            insufficient_data: "Too little shared hourly data to compare.",
            not_newer: "Waiting for a newer forecast to compare."
        })[v.status] || "Comparison unavailable for this forecast.";
}
