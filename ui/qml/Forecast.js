.pragma library

const conditions = ["clear", "partly_cloudy", "cloudy", "fog", "drizzle", "rain", "snow", "sleet", "thunderstorm", "unknown"];
function object(v) {
    return v !== null && typeof v === "object" && !Array.isArray(v);
}
function codepoints(v) {
    let count = 0;
    for (let i = 0; i < v.length; i++) {
        let c = v.charCodeAt(i);
        if (c >= 0xd800 && c <= 0xdbff) {
            if (i + 1 >= v.length || v.charCodeAt(i + 1) < 0xdc00 || v.charCodeAt(i + 1) > 0xdfff)
                throw Error("Invalid surrogate");
            i++;
        } else if (c >= 0xdc00 && c <= 0xdfff)
            throw Error("Invalid surrogate");
        count++;
    }
    return count;
}
function string(v, n, multiline) {
    if (typeof v !== "string" || codepoints(v) > n || (multiline ? /[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/ : /[\u0000-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/).test(v))
        throw Error("Invalid text");
    return v;
}
function number(v, lo, hi) {
    if (typeof v !== "number" || !isFinite(v) || v < lo || v > hi)
        throw Error("Invalid number");
    return v;
}
function optional(v, lo, hi) {
    return v === null || v === undefined ? null : number(v, lo, hi);
}
function integer(v, lo, hi) {
    number(v, lo, hi);
    if (!Number.isSafeInteger(v))
        throw Error("Invalid integer");
    return v;
}
function countryCode(v) {
    if (v === null || v === undefined)
        return null;
    if (typeof v !== "string" || !/^[A-Z]{2}$/.test(v))
        throw Error("Invalid country code");
    return v;
}
function placeSearch(v) {
    if (v === undefined)
        return {
            generation: 0,
            client_token: 0,
            status: "idle",
            results: [],
            error: null
        };
    if (!object(v) || Object.keys(v).length < 4 || Object.keys(v).length > 5 || Object.keys(v).some(k => ["generation", "client_token", "status", "results", "error"].indexOf(k) < 0) || ["generation", "status", "results", "error"].some(k => !Object.prototype.hasOwnProperty.call(v, k)) || ["idle", "loading", "ready", "error"].indexOf(v.status) < 0 || !Array.isArray(v.results) || v.results.length > 10)
        throw Error("Invalid place search");
    let token = v.client_token === undefined ? 0 : integer(v.client_token, 0, 2147483647);
    let generation = integer(v.generation, 0, 2147483647), ids = [], results = [];
    for (let row of v.results) {
        if (!object(row) || Object.keys(row).length !== 5)
            throw Error("Invalid place result");
        let id = integer(row.id, 1, 2147483647);
        if (ids.indexOf(id) >= 0)
            throw Error("Duplicate place result");
        ids.push(id);
        let name = string(row.name, 244), admin1 = string(row.admin1, 244), country = string(row.country, 244), code = countryCode(row.country_code);
        if (!name || !country || code === null)
            throw Error("Incomplete place result");
        results.push({
            id: id,
            name: name,
            admin1: admin1,
            country: country,
            country_code: code
        });
    }
    let error = v.error;
    if (error !== null && ["lookup_failed", "timeout", "offline"].indexOf(error) < 0)
        throw Error("Invalid place search error");
    if ((v.status === "error") !== (error !== null) || (v.status !== "ready" && results.length > 0))
        throw Error("Invalid place search state");
    if (v.status === "idle" && token !== 0)
        throw Error("Invalid idle search token");
    return {
        generation: generation,
        client_token: token,
        status: v.status,
        results: results,
        error: error
    };
}
function time(v) {
    string(v, 40);
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,6})?(Z|[+-]\d{2}:\d{2})$/.test(v) || !isFinite(Date.parse(v)))
        throw Error("Invalid time");
    return v;
}
function boundedTree(value) {
    let stack = [
        {
            value: value,
            depth: 0
        }
    ], count = 0;
    while (stack.length) {
        let entry = stack.pop();
        if (++count > 8192 || entry.depth > 16)
            throw Error("JSON complexity exceeded");
        let v = entry.value;
        if (v === null || typeof v !== "object")
            continue;
        let keys = Object.keys(v);
        if (keys.length > (Array.isArray(v) ? 240 : 64))
            throw Error("JSON cardinality exceeded");
        for (let key of keys) {
            if (key === "__proto__" || key === "constructor" || key === "prototype")
                throw Error("Invalid key");
            stack.push({
                value: v[key],
                depth: entry.depth + 1
            });
        }
    }
}
function condition(v) {
    if (conditions.indexOf(v) < 0)
        throw Error("Invalid condition");
    return v;
}
function atmosphere(v) {
    if (!object(v))
        throw Error("Invalid atmosphere");
    for (let key of ["reduced_motion", "lightning_enabled", "thunderstorm"])
        if (typeof v[key] !== "boolean")
            throw Error("Invalid atmosphere flag");
    return {
        rain_intensity: number(v.rain_intensity, 0, 1),
        snow_intensity: number(v.snow_intensity, 0, 1),
        cloud_cover: number(v.cloud_cover, 0, 1),
        fog_density: number(v.fog_density, 0, 1),
        sun_elevation: number(v.sun_elevation, -90, 90),
        sun_azimuth: number(v.sun_azimuth, 0, 360),
        wind_x: number(v.wind_x, -500, 500),
        reduced_motion: v.reduced_motion,
        lightning_enabled: v.lightning_enabled,
        thunderstorm: v.thunderstorm
    };
}
function weather(v) {
    if (!object(v))
        throw Error("Invalid weather");
    if (v.is_day !== null && typeof v.is_day !== "boolean")
        throw Error("Invalid daylight");
    return {
        time: time(v.time),
        condition: condition(v.condition),
        temperature_c: optional(v.temperature_c, -150, 100),
        apparent_temperature_c: optional(v.apparent_temperature_c, -200, 150),
        humidity: optional(v.humidity, 0, 1),
        cloud_cover: optional(v.cloud_cover, 0, 1),
        precipitation_rate_mm_hr: optional(v.precipitation_rate_mm_hr, 0, 10000),
        precipitation_probability: optional(v.precipitation_probability, 0, 1),
        visibility_m: optional(v.visibility_m, 0, 1000000),
        wind_speed_m_s: optional(v.wind_speed_m_s, 0, 200),
        wind_gust_m_s: optional(v.wind_gust_m_s, 0, 250),
        wind_direction_deg: optional(v.wind_direction_deg, 0, 360),
        uv_index: optional(v.uv_index, 0, 50),
        pressure_msl_hpa: optional(v.pressure_msl_hpa, 800, 1100),
        dew_point_c: optional(v.dew_point_c, -100, 60),
        is_day: typeof v.is_day === "boolean" ? v.is_day : true
    };
}
function forecast(v) {
    if (v === null || v === undefined)
        return null;
    if (!object(v) || !object(v.location) || !object(v.source) || !Array.isArray(v.hourly) || v.hourly.length > 240 || !Array.isArray(v.daily) || v.daily.length > 10)
        throw Error("Invalid forecast");
    let tz = string(v.location.timezone, 80);
    if (!/^[A-Za-z0-9_+\-/]+$/.test(tz))
        throw Error("Invalid timezone");
    let result = {
        location: {
            name: string(v.location.name, 244),
            timezone: tz
        },
        source: {
            name: string(v.source.name, 80),
            attribution: string(v.source.attribution, 240)
        },
        fetched_at: time(v.fetched_at),
        current: weather(v.current),
        hourly: [],
        daily: []
    };
    for (let i = 0; i < v.hourly.length; i++) {
        let row = v.hourly[i], h = weather(row);
        h.local_hour = string(row.local_hour, 32);
        h.local_date = row.local_date === undefined ? "" : string(row.local_date, 10);
        if (h.local_date !== "" && !/^\d{4}-\d{2}-\d{2}$/.test(h.local_date))
            throw Error("Invalid local date");
        h.local_label = row.local_label === undefined ? h.local_hour : string(row.local_label, 64);
        h.period_label = row.period_label === undefined ? "" : string(row.period_label, 64);
        result.hourly.push(h);
    }
    for (let i = 0; i < v.daily.length; i++) {
        let d = v.daily[i];
        if (!object(d) || !/^\d{4}-\d{2}-\d{2}$/.test(d.date))
            throw Error("Invalid day");
        let lo = optional(d.low_c, -150, 100), hi = optional(d.high_c, -150, 100);
        if (lo !== null && hi !== null && lo > hi)
            throw Error("Invalid range");
        result.daily.push({
            date: d.date,
            day_label: string(d.day_label, 32),
            sunrise_label: d.sunrise_label === null ? null : string(d.sunrise_label, 32),
            sunset_label: d.sunset_label === null ? null : string(d.sunset_label, 32),
            low_c: lo,
            high_c: hi,
            condition: condition(d.condition),
            sunrise: d.sunrise === null ? null : time(d.sunrise),
            sunset: d.sunset === null ? null : time(d.sunset),
            precipitation_probability: optional(d.precipitation_probability, 0, 1)
        });
    }
    return result;
}
function effectsSetup(v) {
    if (v === undefined)
        return {
            status: "unchecked",
            reason: "not_checked",
            outputs: [],
            selected_output: null
        };
    const reasons = ["not_checked", "ready", "wayland_required", "session_unavailable", "output_unavailable", "output_selection_required", "unsupported_output", "plugin_conflict", "native_missing", "native_incompatible", "activation_failed"];
    if (!object(v) || Object.keys(v).length !== 4 || ["unchecked", "ready", "unavailable"].indexOf(v.status) < 0 || reasons.indexOf(v.reason) < 0 || !Array.isArray(v.outputs) || v.outputs.length > 32)
        throw Error("Invalid effects setup");
    let outputs = [], names = [];
    for (let row of v.outputs) {
        if (!object(row) || Object.keys(row).length !== 5 || typeof row.name !== "string" || !/^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(row.name) || names.indexOf(row.name) >= 0 || typeof row.enabled !== "boolean")
            throw Error("Invalid output");
        let width = number(row.width, 0, 32768), height = number(row.height, 0, 32768);
        if (row.enabled && (width <= 0 || height <= 0))
            throw Error("Invalid enabled output");
        outputs.push({
            name: row.name,
            width: width,
            height: height,
            scale: number(row.scale, .25, 8),
            enabled: row.enabled
        });
        names.push(row.name);
    }
    if (v.selected_output !== null && !outputs.some(row => row.name === v.selected_output && row.enabled))
        throw Error("Invalid output selection");
    if ((v.status === "ready") !== (v.reason === "ready") || (v.status === "unchecked") !== (v.reason === "not_checked") || (v.status === "ready" && v.selected_output === null))
        throw Error("Invalid compatibility state");
    return {
        status: v.status,
        reason: v.reason,
        outputs: outputs,
        selected_output: v.selected_output
    };
}
function notifications(v) {
    if (v === undefined)
        return {
            settings: {
                enabled: false,
                quiet_enabled: true,
                quiet_start: 22,
                quiet_end: 7,
                probability: 50
            },
            state: "off",
            snoozed_until: null,
            delivery: "none",
            supported: false
        };
    if (!object(v) || Object.keys(v).length !== 5 || !object(v.settings) || Object.keys(v.settings).length !== 5 || typeof v.supported !== "boolean" || ["off", "waiting", "watching", "quiet", "paused", "unavailable"].indexOf(v.state) < 0 || ["none", "pending", "sent", "failed"].indexOf(v.delivery) < 0)
        throw Error("Invalid notification state");
    let s = v.settings;
    if (typeof s.enabled !== "boolean" || typeof s.quiet_enabled !== "boolean" || [50, 70].indexOf(s.probability) < 0)
        throw Error("Invalid notification settings");
    for (let name of ["quiet_start", "quiet_end"])
        if (typeof s[name] !== "number" || Math.floor(s[name]) !== s[name] || s[name] < 0 || s[name] > 23)
            throw Error("Invalid quiet hour");
    if (s.quiet_start === s.quiet_end)
        throw Error("Invalid quiet period");
    let until = v.snoozed_until === null ? null : number(v.snoozed_until, 0, 253402300799);
    if (v.state === "paused" && until === null)
        throw Error("Missing pause expiry");
    return {
        settings: {
            enabled: s.enabled,
            quiet_enabled: s.quiet_enabled,
            quiet_start: s.quiet_start,
            quiet_end: s.quiet_end,
            probability: s.probability
        },
        state: v.state,
        snoozed_until: until,
        delivery: v.delivery,
        supported: v.supported
    };
}
const aqAttribution = "CAMS global model data via Open-Meteo (CC BY 4.0)";
function airQuality(v) {
    if (v === undefined)
        return {
            freshness: "unavailable",
            refreshing: false,
            offline: false,
            error: null,
            domain: "cams_global",
            source: "CAMS global model data",
            attribution: aqAttribution,
            valid_at: null,
            fetched_at: null,
            valid_label: null,
            fetched_label: null,
            age_seconds: null,
            us_aqi: null,
            european_aqi: null,
            pm2_5_ug_m3: null
        };
    const keys = ["freshness", "refreshing", "offline", "error", "domain", "source", "attribution", "valid_at", "fetched_at", "valid_label", "fetched_label", "age_seconds", "us_aqi", "european_aqi", "pm2_5_ug_m3"];
    if (!object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)) || ["unavailable", "fresh", "stale", "expired", "invalid_future"].indexOf(v.freshness) < 0 || typeof v.refreshing !== "boolean" || typeof v.offline !== "boolean" || (v.error !== null && ["fetch_failed", "timeout", "cache_invalid", "save_failed"].indexOf(v.error) < 0) || v.domain !== "cams_global" || v.source !== "CAMS global model data" || v.attribution !== aqAttribution)
        throw Error("Invalid air quality state");
    let valid = v.valid_at === null ? null : time(v.valid_at), fetched = v.fetched_at === null ? null : time(v.fetched_at);
    let validLabel = v.valid_label === null ? null : string(v.valid_label, 80), fetchedLabel = v.fetched_label === null ? null : string(v.fetched_label, 80);
    let age = optional(v.age_seconds, 0, 315360000);
    let us = optional(v.us_aqi, 0, 1000), eu = optional(v.european_aqi, 0, 1000), pm = optional(v.pm2_5_ug_m3, 0, 5000);
    if (v.freshness === "unavailable") {
        if (valid !== null || fetched !== null || validLabel !== null || fetchedLabel !== null || age !== null || us !== null || eu !== null || pm !== null)
            throw Error("Invalid unavailable air quality");
    } else {
        if (valid === null || fetched === null || !validLabel || !fetchedLabel)
            throw Error("Missing air quality time or label");
        let validMs = Date.parse(valid), fetchedMs = Date.parse(fetched);
        if (validMs < fetchedMs - 86400000 || validMs > fetchedMs + 300000)
            throw Error("Invalid air quality time range");
        if (v.freshness === "invalid_future") {
            if (age !== null)
                throw Error("Invalid future air quality age");
        } else if (age === null)
            throw Error("Missing air quality age");
        if ((v.freshness === "fresh" && age > 7200) || (v.freshness === "stale" && (age <= 7200 || age > 21600)) || (v.freshness === "expired" && age <= 21600))
            throw Error("Invalid air quality freshness age");
        if ((v.freshness === "expired" || v.freshness === "invalid_future") && (us !== null || eu !== null || pm !== null))
            throw Error("Expired air quality values");
        if ((v.freshness === "fresh" || v.freshness === "stale") && us === null && eu === null && pm === null)
            throw Error("Missing air quality values");
    }
    return {
        freshness: v.freshness,
        refreshing: v.refreshing,
        offline: v.offline,
        error: v.error,
        domain: v.domain,
        source: v.source,
        attribution: v.attribution,
        valid_at: valid,
        fetched_at: fetched,
        valid_label: validLabel,
        fetched_label: fetchedLabel,
        age_seconds: age,
        us_aqi: us,
        european_aqi: eu,
        pm2_5_ug_m3: pm
    };
}
function weatherMap(v) {
    if (!object(v) || ["closed", "loading", "unavailable", "fresh", "stale"].indexOf(v.status) < 0 || typeof v.offline !== "boolean" || (v.error !== "" && v.error !== "fetch_failed" && v.error !== "save_failed"))
        throw Error("Invalid map status");
    if (!Array.isArray(v.hour_labels) || v.hour_labels.length > 24)
        throw Error("Invalid map labels");
    if (v.data === null) {
        if (v.hour_labels.length !== 0 || v.fetched_label !== null)
            throw Error("Invalid empty map labels");
        return {
            status: v.status,
            offline: v.offline,
            error: v.error,
            data: null,
            hour_labels: [],
            fetched_label: null
        };
    }
    let d = v.data;
    if (!object(d) || d.radius_miles !== 10 || !Array.isArray(d.hours) || d.hours.length < 1 || d.hours.length > 24 || !Array.isArray(d.cells) || d.cells.length !== 25 || ["ncep_nbm_conus", "dwd_icon_d2", "cmc_gem_hrdps", "ncep_gfs_global"].indexOf(d.model_id) < 0 || d.attribution !== "Model forecast via Open-Meteo (CC BY 4.0)")
        throw Error("Invalid map data");
    number(d.latitude, -85, 85);
    number(d.longitude, -180, 180);
    number(d.resolution_km, 1, 25);
    time(d.fetched_at);
    string(d.model_name, 80);
    if (v.hour_labels.length !== d.hours.length || typeof v.fetched_label !== "string")
        throw Error("Invalid map time labels");
    string(v.fetched_label, 80);
    for (let label of v.hour_labels)
        string(label, 80);
    for (let i = 0; i < d.hours.length; i++) {
        integer(d.hours[i], 0, 253402300799);
        if (i && d.hours[i] - d.hours[i - 1] !== 3600)
            throw Error("Invalid map hours");
    }
    for (let cell of d.cells) {
        if (!object(cell))
            throw Error("Invalid map cell");
        number(cell.latitude, -85, 85);
        number(cell.longitude, -180, 180);
        for (let [key, lo, hi] of [["temperature_c", -100, 70], ["wind_speed_m_s", 0, 150], ["wind_from_deg", 0, 360], ["precipitation_mm", 0, 500]]) {
            if (!Array.isArray(cell[key]) || cell[key].length !== d.hours.length)
                throw Error("Invalid map series");
            for (let x of cell[key])
                number(x, lo, hi);
        }
    }
    return {
        status: v.status,
        offline: v.offline,
        error: v.error,
        data: d,
        hour_labels: v.hour_labels,
        fetched_label: v.fetched_label
    };
}
function snapshot(v) {
    if (!object(v) || v.schema_version !== 1 || !object(v.controls) || !object(v.alerts) || !object(v.source) || !object(v.location))
        throw Error("Invalid snapshot");
    if (!Array.isArray(v.alerts.items) || v.alerts.items.length > 8 || ["available", "unavailable", "not_supported_here"].indexOf(v.alerts.status) < 0)
        throw Error("Invalid alerts");
    let alertSource = v.alerts.source === undefined || v.alerts.source === null ? null : string(v.alerts.source, 80);
    let coverage = v.alerts.coverage === undefined ? "unknown" : v.alerts.coverage;
    if (["US", "unknown", "unsupported"].indexOf(coverage) < 0 || (alertSource !== null && alertSource !== "National Weather Service") || (alertSource !== null && coverage !== "US") || (v.alerts.status === "not_supported_here" && coverage !== "unsupported") || (coverage === "unsupported" && v.alerts.status !== "not_supported_here"))
        throw Error("Invalid alert coverage");
    let alertsFetchedAt = v.alerts.fetched_at === undefined || v.alerts.fetched_at === null ? null : time(v.alerts.fetched_at);
    let controls = {};
    for (let k of ["reduced_motion", "lightning_enabled", "window_physics", "accumulation", "pause_fullscreen"]) {
        if (typeof v.controls[k] !== "boolean")
            throw Error("Invalid control");
        controls[k] = v.controls[k];
    }
    if ([15, 30, 60].indexOf(v.controls.fps) < 0 || ["live", "manual"].indexOf(v.controls.mode) < 0 || ["subtle", "normal", "immersive"].indexOf(v.controls.strength) < 0 || !object(v.controls.manual))
        throw Error("Invalid controls");
    if (["F", "C"].indexOf(v.controls.units) < 0)
        throw Error("Invalid units");
    let unitsMode = v.controls.units_mode === undefined ? "manual" : v.controls.units_mode;
    if (["auto", "manual"].indexOf(unitsMode) < 0)
        throw Error("Invalid units mode");
    controls.units_mode = unitsMode;
    let windUnits = v.controls.wind_units === undefined ? "auto" : v.controls.wind_units;
    if (["auto", "mph", "km/h", "m/s", "kn"].indexOf(windUnits) < 0)
        throw Error("Invalid wind units");
    controls.wind_units = windUnits;
    const quality = v.controls.visual_quality === undefined ? "auto" : v.controls.visual_quality;
    if (["auto", "full", "economical", "static"].indexOf(quality) < 0)
        throw Error("Invalid visual quality");
    controls.visual_quality = quality;
    controls.units = v.controls.units;
    controls.fps = v.controls.fps;
    controls.mode = v.controls.mode;
    controls.strength = v.controls.strength;
    controls.manual = {
        condition: condition(v.controls.manual.condition)
    };
    let fresh = v.source.freshness;
    if (["fresh", "stale", "expired", "invalid_future", "unavailable"].indexOf(fresh) < 0)
        throw Error("Invalid freshness");
    let f = null;
    if (v.current !== null)
        f = forecast({
            location: v.location,
            source: v.source,
            fetched_at: v.source.fetched_at,
            current: v.current,
            hourly: v.hourly,
            daily: v.daily
        });
    else if (!Array.isArray(v.hourly) || v.hourly.length !== 0 || !Array.isArray(v.daily) || v.daily.length !== 0)
        throw Error("Invalid empty forecast");
    const alertFreshness = v.alerts.freshness === undefined ? (v.alerts.status === "available" ? "current" : v.alerts.status) : v.alerts.freshness;
    if (["current", "stale", "pending", "unavailable", "not_supported_here"].indexOf(alertFreshness) < 0 || (v.alerts.refreshing !== undefined && typeof v.alerts.refreshing !== "boolean"))
        throw Error("Invalid alert freshness");
    if (v.alerts.freshness !== undefined && (((alertFreshness === "current" || alertFreshness === "stale") && (v.alerts.status !== "available" || alertsFetchedAt === null)) || (alertFreshness === "pending" && (v.alerts.status !== "unavailable" || v.alerts.refreshing !== true || alertsFetchedAt !== null)) || (alertFreshness === "unavailable" && v.alerts.status !== "unavailable")))
        throw Error("Inconsistent alert freshness");
    let alerts = [];
    for (let a of v.alerts.items) {
        if (!object(a))
            throw Error("Invalid alert");
        if (a.text_truncated !== undefined && typeof a.text_truncated !== "boolean")
            throw Error("Invalid alert truncation flag");
        const text = (value, limit, multiline) => value === null || value === undefined ? "" : string(value, limit, multiline);
        alerts.push({
            id: text(a.id, 256),
            event: text(a.event, 160) || "Weather alert",
            headline: text(a.headline, 512),
            description: text(a.description, 4096, true),
            instruction: text(a.instruction, 2048, true),
            severity: text(a.severity, 40) || "Unknown",
            urgency: text(a.urgency, 40) || "Unknown",
            expires: a.expires === undefined ? null : time(a.expires),
            expires_label: text(a.expires_label, 64),
            text_truncated: a.text_truncated === true
        });
    }
    if (!object(v.effect_status) || ["stopped", "starting", "running", "cleanup_failed"].indexOf(v.effect_status.state) < 0)
        throw Error("Invalid effects state");
    const remaining = v.effect_status.remaining_seconds === undefined ? 0 : number(v.effect_status.remaining_seconds, 0, 300);
    if (v.effect_status.persistent !== undefined && typeof v.effect_status.persistent !== "boolean")
        throw Error("Invalid persistent effects state");
    if (typeof v.source.refreshing !== "boolean")
        throw Error("Invalid refresh status");
    let settings = {
        mode: "default",
        zip_code: null,
        busy: false,
        error: null,
        country_code: null,
        place: null
    };
    if (v.location_settings !== undefined) {
        let s = v.location_settings;
        let settingKeys = Object.keys(s);
        if (!object(s) || settingKeys.length < 4 || settingKeys.length > 6 || settingKeys.some(k => ["mode", "zip_code", "busy", "error", "country_code", "place"].indexOf(k) < 0) || ["mode", "zip_code", "busy", "error"].some(k => !Object.prototype.hasOwnProperty.call(s, k)) || ["default", "custom", "zip", "auto", "place"].indexOf(s.mode) < 0 || typeof s.busy !== "boolean" || (s.zip_code !== null && (typeof s.zip_code !== "string" || !/^[0-9]{5}$/.test(s.zip_code))) || (s.mode === "zip") !== (s.zip_code !== null) || (s.error !== null && ["lookup_failed", "zip_not_found", "zip_ambiguous", "timeout", "state_io_failed", "save_unconfirmed", "place_not_found", "stale_selection"].indexOf(s.error) < 0))
            throw Error("Invalid location settings");
        let place = null;
        if (s.place !== undefined && s.place !== null) {
            if (!object(s.place) || Object.keys(s.place).length !== 2 || s.place.provider !== "open-meteo")
                throw Error("Invalid place identity");
            place = {
                provider: s.place.provider,
                id: integer(s.place.id, 1, 2147483647)
            };
        }
        if ((s.mode === "place") !== (place !== null))
            throw Error("Invalid place mode");
        settings = {
            mode: s.mode,
            zip_code: s.zip_code,
            busy: s.busy,
            error: s.error,
            country_code: countryCode(s.country_code),
            place: place
        };
    }
    if (v.launcher_status !== undefined && ["ready", "installed", "conflict", "failed", "unsupported_path"].indexOf(v.launcher_status) < 0)
        throw Error("Invalid launcher status");
    return {
        update: updateStatus(v.update),
        launcher_status: v.launcher_status || "ready",
        forecast: f,
        location: string(v.location.name, 244),
        location_settings: settings,
        place_search: placeSearch(v.place_search),
        air_quality: airQuality(v.air_quality),
        effects_setup: effectsSetup(v.effects_setup),
        notifications: notifications(v.notifications),
        timezone: string(v.location.timezone, 80),
        controls: controls,
        atmosphere: atmosphere(v.atmosphere),
        alerts: {
            status: v.alerts.status,
            freshness: alertFreshness,
            refreshing: v.alerts.refreshing === true,
            items: alerts,
            source: alertSource,
            coverage: coverage,
            fetched_at: alertsFetchedAt
        },
        source: {
            name: string(v.source.name, 80),
            attribution: string(v.source.attribution, 240),
            freshness: fresh,
            age_seconds: optional(v.source.age_seconds, 0, 315360000),
            refreshing: v.source.refreshing,
            error: v.source.error === null ? null : string(v.source.error, 80)
        },
        effect_status: v.effect_status.state,
        effect_remaining_seconds: remaining,
        effect_persistent: v.effect_status.persistent === true
    };
}
function temp(v, units) {
    return v === null || v === undefined ? "—" : Math.round(units === "F" ? v * 9 / 5 + 32 : v) + "°";
}
function uv(v) {
    return v === null || v === undefined ? "—" : v.toFixed(1);
}
function pressure(v, units) {
    return v === null || v === undefined ? "—" : units === "F" ? (v / 33.8638866667).toFixed(2) + " inHg" : Math.round(v) + " hPa";
}
function title(v) {
    return ({
            clear: "Clear",
            partly_cloudy: "Partly cloudy",
            cloudy: "Cloudy",
            fog: "Fog",
            drizzle: "Drizzle",
            rain: "Rain",
            snow: "Snow",
            sleet: "Sleet",
            thunderstorm: "Thunderstorm",
            unknown: "Unavailable"
        })[v] || "Unavailable";
}
function percent(v) {
    return v === null || v === undefined ? "—" : Math.round(v * 100) + "%";
}
function localTime(v, tz, kind) {
    if (!v)
        return "—";
    try {
        return new Date(v).toLocaleString("en-US", {
            timeZone: tz,
            hour: "numeric",
            minute: kind === "hour" ? undefined : "2-digit",
            hour12: true
        });
    } catch (e) {
        return "Time unavailable";
    }
}
function windUnit(units, preference) {
    return !preference || preference === "auto" ? (units === "F" ? "mph" : "km/h") : preference;
}
function wind(v, units, preference) {
    if (v === null || v === undefined)
        return "—";
    const unit = windUnit(units, preference);
    const value = v * ({
            mph: 3600 / 1609.344,
            "km/h": 3.6,
            "m/s": 1,
            kn: 3600 / 1852
        })[unit];
    return (unit === "m/s" ? value.toFixed(1) : Math.round(value)) + " " + unit;
}
function distance(v, units) {
    return v === null || v === undefined ? "—" : (v / (units === "F" ? 1609.344 : 1000)).toFixed(1) + (units === "F" ? " mi" : " km");
}
function direction(v) {
    return v === null || v === undefined ? "" : ["N", "NE", "E", "SE", "S", "SW", "W", "NW"][Math.round(v / 45) % 8];
}
function amount(v, units) {
    return v === null || v === undefined ? "—" : (units === "F" ? (v / 25.4).toFixed(2) + " in" : v.toFixed(1) + " mm");
}
function outlook(hours, units, windUnits) {
    // Describe the available hourly model, never invent minute-level onset or
    // turn missing probability into a dry-weather promise.
    const next = hours.slice(0, 6);
    if (!next.length)
        return "Hourly outlook unavailable.";
    const known = next.filter(h => h.precipitation_probability !== null && h.precipitation_probability !== undefined);
    let parts = [];
    if (known.length === next.length) {
        const peak = known.reduce((a, b) => a.precipitation_probability >= b.precipitation_probability ? a : b);
        parts.push(peak.precipitation_probability < 0.2 ? "Low precipitation chances in the next " + next.length + " hourly forecasts." : "Precipitation chance peaks at " + percent(peak.precipitation_probability) + " around " + peak.local_hour + ".");
    } else
        parts.push("Precipitation outlook is incomplete.");
    const gusts = next.filter(h => h.wind_gust_m_s !== null && h.wind_gust_m_s !== undefined);
    if (gusts.length === next.length) {
        const peak = Math.max.apply(null, gusts.map(h => h.wind_gust_m_s));
        if (peak >= 8)
            parts.push("Gusts up to " + wind(peak, units, windUnits) + ".");
    }
    return parts.join(" ");
}

function updateStatus(v) {
    if (v === undefined || v === null)
        return {
            state: "unsupported",
            installed: "",
            available: "",
            message: "Updates are unavailable for this installation",
            checked_at: 0
        };
    if (!object(v) || Object.keys(v).length !== 5 || ["idle", "current", "available", "publishing", "failed", "checking", "downloading", "verifying", "restarting", "updated", "rolled_back", "development", "unsupported"].indexOf(v.state) < 0)
        throw Error("Invalid update status");
    return {
        state: v.state,
        installed: string(v.installed, 32),
        available: string(v.available, 32),
        message: string(v.message, 512),
        checked_at: integer(v.checked_at, 0, 253402300799)
    };
}
