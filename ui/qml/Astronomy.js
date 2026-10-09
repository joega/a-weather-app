.pragma library
.import "Forecast.js" as Forecast

function fields(v, keys) {
    if (!Forecast.object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)))
        throw Error("Invalid astronomy fields");
}
function context(s) {
    if (!s || !s.saved_locations || s.location_settings.mode === "default" || s.location_settings.busy)
        return "";
    return JSON.stringify([s.saved_locations.viewed, s.latitude, s.longitude, s.timezone]);
}
function query(s, date) {
    return {
        location_id: s.saved_locations.viewed,
        latitude: s.latitude,
        longitude: s.longitude,
        timezone: s.timezone,
        date: date || ""
    };
}
function date(v) {
    Forecast.string(v, 10);
    if (!/^\d{4}-\d{2}-\d{2}$/.test(v) || !Number.isFinite(Date.parse(v)) || new Date(v).toISOString().slice(0, 10) !== v)
        throw Error("Invalid astronomy date");
    return v;
}
function result(v, q) {
    fields(v, ["location_id", "latitude", "longitude", "timezone", "date", "date_label", "today", "min_date", "max_date", "previous_date", "next_date", "day_start", "day_end", "calculated_at", "phase_at", "phase_label", "phase", "illumination", "daylight_seconds", "change_seconds", "sun_state", "moon_state", "sun_events", "moon_events", "civil_events", "nautical_events", "astronomical_events", "daylight", "golden"]);
    Forecast.string(v.location_id, 96);
    Forecast.string(v.timezone, 100);
    Forecast.number(v.latitude, -90, 90);
    Forecast.number(v.longitude, -180, 180);
    for (const k of ["date", "today", "min_date", "max_date", "previous_date", "next_date"])
        date(v[k]);
    for (const k of ["day_start", "day_end", "calculated_at", "phase_at"])
        Forecast.radarTime(v[k]);
    Forecast.string(v.date_label, 80);
    Forecast.string(v.phase_label, 80);
    if (q.location_id !== v.location_id || q.latitude !== v.latitude || q.longitude !== v.longitude || q.timezone !== v.timezone || (q.date && q.date !== v.date) || v.date < v.min_date || v.date > v.max_date || v.previous_date >= v.date || v.next_date <= v.date)
        throw Error("Astronomy context mismatch");
    const start = Date.parse(v.day_start), end = Date.parse(v.day_end);
    if (end - start < 22 * 3600000 || end - start > 26 * 3600000 || Date.parse(v.phase_at) < start || Date.parse(v.phase_at) >= end)
        throw Error("Invalid astronomy day");
    Forecast.number(v.phase, 0, 1);
    Forecast.number(v.illumination, 0, 1);
    Forecast.number(v.daylight_seconds, 0, (end - start) / 1000);
    Forecast.number(v.change_seconds, -26 * 3600, 26 * 3600);
    for (const key of ["sun_events", "moon_events", "civil_events", "nautical_events", "astronomical_events"]) {
        if (!Array.isArray(v[key]) || v[key].length > 4)
            throw Error("Invalid astronomy events");
        let previous = start - 1;
        for (const e of v[key]) {
            fields(e, ["time", "kind", "label"]);
            Forecast.radarTime(e.time);
            Forecast.string(e.label, 80);
            const at = Date.parse(e.time);
            if (["rise", "set"].indexOf(e.kind) < 0 || at <= previous || at >= end)
                throw Error("Invalid astronomy event");
            previous = at;
        }
    }
    for (const key of ["sun", "moon"]) {
        if (["normal", "always_up", "always_down"].indexOf(v[key + "_state"]) < 0 || (v[key + "_state"] === "normal") !== (v[key + "_events"].length > 0))
            throw Error("Invalid astronomy horizon state");
    }
    for (const key of ["daylight", "golden"]) {
        if (!Array.isArray(v[key]) || v[key].length > 4)
            throw Error("Invalid light windows");
        let previous = start;
        for (const s of v[key]) {
            fields(s, ["start", "end", "label"]);
            Forecast.radarTime(s.start);
            Forecast.radarTime(s.end);
            Forecast.string(s.label, 160);
            if (Date.parse(s.start) < previous || Date.parse(s.end) <= Date.parse(s.start) || Date.parse(s.end) > end)
                throw Error("Invalid light window");
            previous = Date.parse(s.end);
        }
    }
    return v;
}
function duration(seconds) {
    const minutes = Math.max(0, Math.round(seconds / 60));
    return minutes < 60 ? minutes + "m" : Math.floor(minutes / 60) + "h " + (minutes % 60) + "m";
}
function remaining(v, now) {
    return v.daylight.reduce((sum, s) => sum + Math.max(0, Date.parse(s.end) - Math.max(now, Date.parse(s.start))), 0) / 1000;
}
function phaseName(v) {
    if (v < 0.01 || v > 0.99)
        return "Near new moon";
    if (Math.abs(v - 0.25) < 0.01)
        return "Near first quarter";
    if (Math.abs(v - 0.5) < 0.01)
        return "Near full moon";
    if (Math.abs(v - 0.75) < 0.01)
        return "Near last quarter";
    return v < 0.25 ? "Waxing crescent" : v < 0.5 ? "Waxing gibbous" : v < 0.75 ? "Waning gibbous" : "Waning crescent";
}
function events(rows, rise, set) {
    return rows.length ? rows.map(e => (e.kind === "rise" ? rise : set) + " " + e.label).join(" · ") : "No crossings on this date";
}
function horizon(v, key) {
    const name = key === "sun" ? "Sun" : "Moon";
    return v[key + "_state"] === "always_up" ? name + " stays above the horizon all day" : v[key + "_state"] === "always_down" ? name + " stays below the horizon all day" : events(v[key + "_events"], "Rise", "Set");
}
function shiftedDate(date, days) {
    return new Date(Date.parse(date) + days * 86400000).toISOString().slice(0, 10);
}
