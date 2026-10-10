.pragma library
.import "Dashboard.js" as Dashboard

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
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$/.test(v) || !isFinite(Date.parse(v)))
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
function warningReference(v) {
    if (!object(v) || Object.keys(v).length !== 2 || typeof v.location !== "string" || typeof v.key !== "string" || !/^[a-f0-9]{64}$/.test(v.location) || !/^[a-f0-9]{64}$/.test(v.key))
        throw Error("Invalid warning reference");
    return {
        location: v.location,
        key: v.key
    };
}
function warningSummary(v, recent) {
    if (!object(v) || Object.keys(v).length !== (recent ? 7 : 5) || ["new", "updated", "canceled"].indexOf(v.kind) < 0)
        throw Error("Invalid warning summary");
    let result = warningReference({
        location: v.location,
        key: v.key
    });
    result.place = string(v.place, 100);
    result.title = string(v.title, 120);
    result.kind = v.kind;
    if (recent) {
        if (["pending", "sent", "failed", "uncertain"].indexOf(v.delivery) < 0)
            throw Error("Invalid warning delivery");
        result.created_at = time(v.created_at);
        result.delivery = v.delivery;
    }
    return result;
}
function warningNotifications(v) {
    if (v === undefined)
        return {
            settings: {
                enabled: false,
                minimum_severity: "severe",
                quiet_enabled: true,
                quiet_start: 22,
                quiet_end: 7,
                urgent_override: false
            },
            state: "off",
            reason: "",
            paused_until: null,
            supported: false,
            ready: false,
            actions: false,
            delivery: "none",
            fetched_at: null,
            complete: false,
            last: null,
            recent: []
        };
    const keys = ["settings", "state", "reason", "paused_until", "supported", "ready", "actions", "delivery", "fetched_at", "complete", "last", "recent"];
    if (!object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)) || !object(v.settings) || Object.keys(v.settings).length !== 6 || ["off", "waiting", "watching", "quiet", "paused", "unavailable"].indexOf(v.state) < 0 || ["none", "pending", "sent", "failed", "uncertain"].indexOf(v.delivery) < 0 || !Array.isArray(v.recent) || v.recent.length > 16)
        throw Error("Invalid official warning state");
    for (const key of ["supported", "ready", "actions", "complete"])
        if (typeof v[key] !== "boolean")
            throw Error("Invalid official warning capability");
    const s = v.settings;
    for (const key of ["enabled", "quiet_enabled", "urgent_override"])
        if (typeof s[key] !== "boolean")
            throw Error("Invalid official warning setting");
    if (["severe", "moderate", "all"].indexOf(s.minimum_severity) < 0 || s.quiet_start === s.quiet_end)
        throw Error("Invalid warning policy");
    integer(s.quiet_start, 0, 23);
    integer(s.quiet_end, 0, 23);
    const reason = string(v.reason, 64);
    if (!/^[a-z_]*$/.test(reason) || v.actions && !v.ready)
        throw Error("Invalid warning status");
    const until = v.paused_until === null ? null : number(v.paused_until, 0, 253402300799);
    if (v.state === "paused" && until === null)
        throw Error("Missing warning pause expiry");
    return {
        settings: {
            enabled: s.enabled,
            minimum_severity: s.minimum_severity,
            quiet_enabled: s.quiet_enabled,
            quiet_start: s.quiet_start,
            quiet_end: s.quiet_end,
            urgent_override: s.urgent_override
        },
        state: v.state,
        reason: reason,
        paused_until: until,
        supported: v.supported,
        ready: v.ready,
        actions: v.actions,
        delivery: v.delivery,
        fetched_at: v.fetched_at === null ? null : time(v.fetched_at),
        complete: v.complete,
        last: v.last === null ? null : warningSummary(v.last, false),
        recent: v.recent.map(row => warningSummary(row, true))
    };
}
// Original CAP text is bounded and rendered as plain text. Unlike UI labels,
// it may contain source whitespace/formatting; never turn it into rich text.
function warningSourceText(v, limit) {
    if (typeof v !== "string" || codepoints(v) > limit)
        throw Error("Invalid warning source text");
    return v;
}
function warningDetail(v) {
    const keys = ["location", "key", "place", "kind", "timezone", "sent_label", "effective_label", "expires_label", "event", "issuer", "headline", "description", "instruction", "area", "severity", "urgency", "certainty", "sent", "effective", "expires"];
    if (!object(v) || Object.keys(v).length !== keys.length || keys.some(k => !Object.prototype.hasOwnProperty.call(v, k)) || ["new", "updated", "canceled"].indexOf(v.kind) < 0 || ["Extreme", "Severe", "Moderate", "Minor", "Unknown"].indexOf(v.severity) < 0 || ["Immediate", "Expected", "Future", "Past", "Unknown"].indexOf(v.urgency) < 0 || ["Observed", "Likely", "Possible", "Unlikely", "Unknown"].indexOf(v.certainty) < 0)
        throw Error("Invalid warning detail");
    let result = warningReference({
        location: v.location,
        key: v.key
    });
    for (const [key, limit] of [["place", 100], ["timezone", 80], ["sent_label", 120], ["effective_label", 120], ["expires_label", 120]])
        result[key] = string(v[key], limit);
    for (const [key, limit] of [["event", 512], ["issuer", 256], ["headline", 2048], ["description", 32000], ["instruction", 16000], ["area", 8192]])
        result[key] = warningSourceText(v[key], limit);
    for (const key of ["kind", "severity", "urgency", "certainty"])
        result[key] = v[key];
    for (const key of ["sent", "effective", "expires"])
        result[key] = time(v[key]);
    return result;
}
function warningStatus(v, available) {
    if (!available)
        return "Monitoring unavailable · weather service disconnected";
    if (v.state === "unavailable")
        return ({
                delivery_unavailable: "Monitoring unavailable · desktop notification service is not ready",
                delivery_unsupported: "Monitoring unavailable · native delivery is not supported",
                target_unavailable: "Monitoring unavailable · connect and resolve the primary place",
                not_supported_here: "Official warning monitoring is available for US places",
                stale_feed: "Monitoring unavailable · warning data is stale",
                incomplete_history: "Monitoring unavailable · warning history is incomplete",
                capacity_reached: "Monitoring unavailable · warning history capacity reached",
                save_unconfirmed: "Monitoring unavailable · saving could not be confirmed",
                state_unavailable: "Monitoring unavailable · saved settings could not be read",
                ledger_unavailable: "Monitoring unavailable · warning history could not be opened",
                clock_changed: "Monitoring unavailable · the clock changed; waiting for fresh data"
            })[v.reason] || "Monitoring unavailable · a complete warning check could not be confirmed";
    return ({
            off: "Official warning monitoring is off",
            waiting: "Waiting for a complete, fresh warning check",
            watching: "Monitoring official NWS warnings",
            quiet: v.settings.urgent_override ? "Quiet hours · urgent overrides are enabled" : "Quiet hours · warning delivery is paused",
            paused: "Warning delivery paused · monitoring continues"
        })[v.state];
}
function warningDeliveryLabel(status) {
    return ({
            pending: "Sending…",
            sent: "Accepted by desktop",
            failed: "Not sent",
            uncertain: "Delivery unconfirmed"
        })[status] || "";
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
// The detail view shares one selected chart across these existing hourly fields.
const metricChoices = [
    {
        label: "Temperature",
        value: "temperature_c",
        format: "temperature",
        zero: false,
        explanation: "Forecast air temperature at the selected time."
    },
    {
        label: "Feels like",
        value: "apparent_temperature_c",
        format: "temperature",
        zero: false,
        explanation: "Modeled apparent temperature accounts for conditions such as wind and humidity."
    },
    {
        label: "Precipitation chance",
        value: "precipitation_probability",
        format: "percent",
        zero: true,
        explanation: "Chance of precipitation during the hour ending at each time. Probability is different from the amount that may fall."
    },
    {
        label: "Precipitation amount",
        value: "precipitation_rate_mm_hr",
        format: "amount",
        zero: true,
        explanation: "Precipitation total for the hour ending at each time. Missing values do not mean dry weather."
    },
    {
        label: "Wind",
        value: "wind_speed_m_s",
        format: "wind",
        zero: true,
        explanation: "Forecast sustained wind speed at the selected time."
    },
    {
        label: "Gusts",
        value: "wind_gust_m_s",
        format: "wind",
        zero: true,
        explanation: "Forecast gust speed. Gusts represent brief peaks rather than sustained wind."
    },
    {
        label: "Humidity",
        value: "humidity",
        format: "percent",
        zero: true,
        explanation: "Relative humidity describes how close the air is to saturation at its temperature."
    },
    {
        label: "Dew point",
        value: "dew_point_c",
        format: "temperature",
        zero: false,
        explanation: "The temperature at which the air would become saturated if cooled."
    },
    {
        label: "Visibility",
        value: "visibility_m",
        format: "distance",
        zero: true,
        explanation: "Modeled horizontal visibility. Local conditions can vary from the model."
    },
    {
        label: "UV index",
        value: "uv_index",
        format: "uv",
        zero: true,
        explanation: "Hourly ultraviolet index from the forecast model. The index has no units."
    },
    {
        label: "Pressure",
        value: "pressure_msl_hpa",
        format: "pressure",
        zero: false,
        explanation: "Atmospheric pressure adjusted to mean sea level, allowing comparison across elevations."
    }
];
function metricInfo(metric) {
    return metricChoices.find(row => row.value === metric) || metricChoices[0];
}
function metricValue(value, metric, units, windUnits) {
    const format = metricInfo(metric).format;
    if (format === "temperature")
        return temp(value, units);
    if (format === "percent")
        return percent(value);
    if (format === "amount")
        return amount(value, units);
    if (format === "wind")
        return wind(value, units, windUnits);
    if (format === "distance")
        return distance(value, units);
    if (format === "pressure")
        return pressure(value, units);
    return uv(value);
}
function metricScale(hours, metric) {
    const known = hours.map(row => row[metric]).filter(value => typeof value === "number" && Number.isFinite(value));
    if (!known.length)
        return null;
    const info = metricInfo(metric), minimum = Math.min.apply(null, known), maximum = Math.max.apply(null, known);
    const low = info.zero ? 0 : minimum - 1;
    const high = info.format === "percent" ? 1 : Math.max(low + 1, maximum);
    return {
        low: low,
        high: high,
        minimum: minimum,
        maximum: maximum,
        count: known.length
    };
}
function metricRange(hours, metric, units, windUnits) {
    const range = metricScale(hours, metric);
    if (!range)
        return "No values available in this period.";
    return "Available hourly range: " + metricValue(range.minimum, metric, units, windUnits) + " – " + metricValue(range.maximum, metric, units, windUnits) + (range.count < hours.length ? ". Some hours are unavailable." : ".");
}
function briefings(value) {
    if (value === undefined)
        return [];
    if (!Array.isArray(value) || value.length > 3)
        throw Error("Invalid briefing");
    let seen = [], previousEnd = 0;
    return value.map(row => {
        if (!object(row) || ["today", "tonight", "tomorrow"].indexOf(row.period) < 0 || seen.indexOf(row.period) >= 0)
            throw Error("Invalid briefing period");
        seen.push(row.period);
        const start = time(row.start), end = time(row.end);
        if (Date.parse(start) < previousEnd || Date.parse(end) <= Date.parse(start) || Date.parse(end) - Date.parse(start) > 86400000)
            throw Error("Invalid briefing interval");
        previousEnd = Date.parse(end);
        const low = optional(row.low_c, -150, 100), high = optional(row.high_c, -150, 100);
        const probability = optional(row.peak_probability, 0, 1), gust = optional(row.gust_m_s, 0, 250);
        if ((low === null) !== (high === null) || (low !== null && low > high) || (probability === null) !== (row.peak_label === null))
            throw Error("Invalid briefing values");
        for (const key of ["temperature_complete", "precipitation_complete", "wind_complete"])
            if (typeof row[key] !== "boolean")
                throw Error("Invalid briefing coverage");
        if ((row.temperature_complete && low === null) || (row.precipitation_complete && probability === null) || (row.wind_complete && gust === null))
            throw Error("Invalid complete briefing");
        return {
            period: row.period,
            start: start,
            end: end,
            range_label: string(row.range_label, 96),
            low_c: low,
            high_c: high,
            peak_probability: probability,
            peak_label: row.peak_label === null ? null : string(row.peak_label, 64),
            gust_m_s: gust,
            temperature_complete: row.temperature_complete,
            precipitation_complete: row.precipitation_complete,
            wind_complete: row.wind_complete
        };
    });
}
function briefingText(row, units, windUnits) {
    if (!row || (row.low_c === null && row.peak_probability === null && row.gust_m_s === null))
        return "Hourly outlook unavailable.";
    let parts = [];
    if (row.low_c !== null)
        parts.push("Hourly temperatures " + temp(row.low_c, units) + "–" + temp(row.high_c, units) + ".");
    if (row.peak_probability !== null) {
        if (row.precipitation_complete && row.peak_probability < 0.2)
            parts.push("Low precipitation chances.");
        else
            parts.push((row.precipitation_complete ? "Precipitation chance peaks at " : "Available precipitation chances reach ") + percent(row.peak_probability) + " (" + row.peak_label + ").");
    }
    if (row.gust_m_s !== null && row.gust_m_s >= 8)
        parts.push("Gusts up to " + wind(row.gust_m_s, units, windUnits) + ".");
    if (!row.temperature_complete || !row.precipitation_complete || !row.wind_complete)
        parts.push("Partial hourly forecast.");
    return parts.join(" ");
}
function savedLocations(v) {
    if (v === undefined || v === null)
        return null;
    const exact = (o, keys) => object(o) && Object.keys(o).length === keys.length && keys.every(k => Object.prototype.hasOwnProperty.call(o, k));
    const validId = id => typeof id === "string" && /^(current|default|zip-[0-9]{5}|place-[1-9][0-9]{0,9}|custom-[a-f0-9]{64})$/.test(id);
    if (!exact(v, ["schema_version", "primary", "viewed", "items", "primary_forecast_available"]) || v.schema_version !== 1 || !validId(v.primary) || !validId(v.viewed) || typeof v.primary_forecast_available !== "boolean" || !Array.isArray(v.items) || v.items.length < 1 || v.items.length > 20)
        throw Error("Invalid saved locations");
    let ids = [];
    const items = v.items.map(row => {
        if (!exact(row, ["id", "name", "label", "mode", "country_code", "timezone", "summary"]) || !validId(row.id) || ids.indexOf(row.id) >= 0 || ["default", "auto", "zip", "place", "custom"].indexOf(row.mode) < 0)
            throw Error("Invalid saved place");
        ids.push(row.id);
        if ((row.mode === "auto" && row.id !== "current") || (row.mode === "default" && row.id !== "default") || (["zip", "place", "custom"].indexOf(row.mode) >= 0 && row.id.indexOf(row.mode + "-") !== 0))
            throw Error("Invalid saved identity");
        const country = countryCode(row.country_code);
        if ((row.mode === "zip" && country !== "US") || (row.mode === "place" && country === null))
            throw Error("Invalid saved country");
        let summary = null;
        if (row.summary !== null) {
            const data = row.summary;
            const summaryFields = ["temperature_c", "condition", "is_day", "fetched_at", "valid_at", "freshness", "alert_status"];
            const counted = Object.prototype.hasOwnProperty.call(data, "alert_count");
            if (!exact(data, counted ? summaryFields.concat(["alert_count"]) : summaryFields) || (data.is_day !== null && typeof data.is_day !== "boolean") || ["fresh", "stale", "expired", "invalid_future"].indexOf(data.freshness) < 0 || ["active", "cached", "none", "unavailable", "not_supported_here"].indexOf(data.alert_status) < 0)
                throw Error("Invalid saved summary");
            if ((["active", "cached", "none"].indexOf(data.alert_status) >= 0 && country !== "US") || (data.alert_status === "not_supported_here" && (country === null || country === "US")))
                throw Error("Invalid saved alert coverage");
            const alertCount = counted && data.alert_count !== null ? integer(data.alert_count, 0, 256) : null;
            const hasAlerts = data.alert_status === "active" || data.alert_status === "cached";
            if (hasAlerts && alertCount === 0 || !hasAlerts && alertCount !== null && alertCount !== 0)
                throw Error("Invalid saved alert count");
            summary = {
                temperature_c: optional(data.temperature_c, -150, 100),
                condition: condition(data.condition),
                is_day: data.is_day,
                fetched_at: time(data.fetched_at),
                valid_at: time(data.valid_at),
                freshness: data.freshness,
                alert_status: data.alert_status,
                alert_count: alertCount
            };
        }
        const zone = string(row.timezone, 100);
        if (!/^[A-Za-z0-9_+\-/]+$/.test(zone))
            throw Error("Invalid saved timezone");
        const label = string(row.label, 80);
        if (label !== label.trim())
            throw Error("Invalid saved label");
        return {
            id: row.id,
            name: string(row.name, 244),
            label: label,
            mode: row.mode,
            country_code: country,
            timezone: zone,
            summary: summary
        };
    });
    if (ids.indexOf(v.primary) < 0 || ids.indexOf(v.viewed) < 0)
        throw Error("Missing saved selection");
    return {
        primary: v.primary,
        viewed: v.viewed,
        items: items,
        primary_forecast_available: v.primary_forecast_available
    };
}
// Provider names include counties for search disambiguation. Keep those names
// in storage, but use familiar regional codes in the saved-location UI.
var savedRegionCodes = {
    US: "Alabama:AL|Alaska:AK|Arizona:AZ|Arkansas:AR|California:CA|Colorado:CO|Connecticut:CT|Delaware:DE|District of Columbia:DC|Florida:FL|Georgia:GA|Hawaii:HI|Idaho:ID|Illinois:IL|Indiana:IN|Iowa:IA|Kansas:KS|Kentucky:KY|Louisiana:LA|Maine:ME|Maryland:MD|Massachusetts:MA|Michigan:MI|Minnesota:MN|Mississippi:MS|Missouri:MO|Montana:MT|Nebraska:NE|Nevada:NV|New Hampshire:NH|New Jersey:NJ|New Mexico:NM|New York:NY|North Carolina:NC|North Dakota:ND|Ohio:OH|Oklahoma:OK|Oregon:OR|Pennsylvania:PA|Rhode Island:RI|South Carolina:SC|South Dakota:SD|Tennessee:TN|Texas:TX|Utah:UT|Vermont:VT|Virginia:VA|Washington:WA|West Virginia:WV|Wisconsin:WI|Wyoming:WY|Puerto Rico:PR|Guam:GU|American Samoa:AS|Northern Mariana Islands:MP|U.S. Virgin Islands:VI",
    CA: "Alberta:AB|British Columbia:BC|Manitoba:MB|New Brunswick:NB|Newfoundland and Labrador:NL|Nova Scotia:NS|Ontario:ON|Prince Edward Island:PE|Quebec:QC|Québec:QC|Saskatchewan:SK|Northwest Territories:NT|Nunavut:NU|Yukon:YT",
    AU: "Australian Capital Territory:ACT|New South Wales:NSW|Northern Territory:NT|Queensland:QLD|South Australia:SA|Tasmania:TAS|Victoria:VIC|Western Australia:WA"
};
function savedPlaceName(row) {
    if (!row)
        return "Location unavailable";
    const name = row.name;
    if (row.mode === "custom")
        return name;
    const parts = name.split(", ");
    if (parts.length < 2)
        return name;
    const regions = savedRegionCodes[row.country_code];
    if (typeof regions === "string") {
        const region = parts[1].trim();
        for (const entry of regions.split("|")) {
            const pair = entry.split(":");
            if (region.toLowerCase() === pair[0].toLowerCase() || region.toUpperCase() === pair[1])
                return parts[0] + ", " + pair[1];
        }
        // Without a recognized state/province, this may be a county or a
        // place name containing a comma. Do not guess which part to remove.
        return name;
    }
    return row.mode === "place" && parts.length > 3 ? [parts[0], parts[1], parts[parts.length - 1]].join(", ") : name;
}
function savedName(row) {
    return row ? row.label || savedPlaceName(row) : "Location unavailable";
}
function savedUnits(row, controls) {
    return controls.units_mode === "auto" ? (row.country_code === null || row.country_code === "US" ? "F" : "C") : controls.units;
}
function savedAlertText(row) {
    return row && row.summary ? ({
            active: "NWS alert in saved feed",
            cached: "Cached NWS alert",
            none: "No active alerts in saved NWS feed",
            unavailable: "Alert status unavailable",
            not_supported_here: "Official alerts not supported here"
        })[row.summary.alert_status] : "Alert status unavailable";
}
function locationErrorText(error) {
    return ({
            lookup_failed: "Location lookup failed. Try again.",
            zip_not_found: "ZIP code not found. Check the code and try again.",
            zip_ambiguous: "This ZIP code matches more than one location. Try another code.",
            place_not_found: "Selected city is no longer available. Search again.",
            stale_selection: "Search result expired. Search again.",
            timeout: "Location lookup timed out. Try again.",
            state_io_failed: "Location could not be saved. Try again.",
            save_unconfirmed: "Location changed, but saving could not be confirmed. Try again."
        })[error] || "";
}
function appearance(v) {
    if (v === undefined)
        return {
            schema_version: 1,
            text_scale: 1,
            high_contrast: false,
            error: null
        };
    if (!object(v) || Object.keys(v).length !== 4 || v.schema_version !== 1 || [1, 1.25, 1.5].indexOf(v.text_scale) < 0 || typeof v.high_contrast !== "boolean" || [null, "state_unavailable", "save_unconfirmed"].indexOf(v.error) < 0)
        throw Error("Invalid appearance preferences");
    return {
        schema_version: 1,
        text_scale: v.text_scale,
        high_contrast: v.high_contrast,
        error: v.error
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
        dashboard: Dashboard.state(v.dashboard),
        appearance: appearance(v.appearance),
        update: updateStatus(v.update),
        launcher_status: v.launcher_status || "ready",
        forecast: f,
        location: string(v.location.name, 244),
        latitude: optional(v.location.latitude, -90, 90),
        longitude: optional(v.location.longitude, -180, 180),
        location_settings: settings,
        place_search: placeSearch(v.place_search),
        saved_locations: savedLocations(v.saved_locations),
        air_quality: airQuality(v.air_quality),
        briefing: briefings(v.briefing),
        effects_setup: effectsSetup(v.effects_setup),
        notifications: notifications(v.notifications),
        warning_notifications: warningNotifications(v.warning_notifications),
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

function emptyRadar(status) {
    return {
        status: status || "closed",
        refreshing: false,
        error: "",
        frames: [],
        legend: "",
        view: null,
        latest: "",
        latest_label: "",
        client_token: 0
    };
}
function radarTime(value) {
    string(value, 40);
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?Z$/.test(value) || !isFinite(Date.parse(value)))
        throw Error("Invalid radar time");
    return value;
}
function radarState(v) {
    if (!object(v) || Object.keys(v).length !== 9 || ["closed", "loading", "current", "stale", "unavailable", "unsupported", "offline"].indexOf(v.status) < 0 || typeof v.refreshing !== "boolean" || ["", "fetch_failed", "invalid_image", "history_limited", "unavailable"].indexOf(v.error) < 0 || !Array.isArray(v.frames) || v.frames.length > 24 || !object(v.view) || Object.keys(v.view).length !== 4)
        throw Error("Invalid radar metadata");
    integer(v.client_token, 0, 2147483647);
    string(v.latest_label, 80);
    radarTime(v.latest);
    for (const key of ["west", "south", "east", "north"])
        number(v.view[key], -20037509, 20037509);
    if (v.view.east <= v.view.west || v.view.north <= v.view.south)
        throw Error("Invalid radar viewport");
    let previous = -Infinity, ids = [];
    for (const f of v.frames) {
        if (!object(f) || Object.keys(f).length !== 4 || ["ready", "pending", "failed", "limited"].indexOf(f.state) < 0 || typeof f.id !== "string" || (f.state === "ready" ? !/^[a-f0-9]{64}$/.test(f.id) : f.id !== ""))
            throw Error("Invalid radar frame");
        radarTime(f.time);
        string(f.label, 80);
        const stamp = Date.parse(f.time);
        if (stamp <= previous || f.id && ids.indexOf(f.id) >= 0)
            throw Error("Invalid radar sequence");
        previous = stamp;
        if (f.id)
            ids.push(f.id);
    }
    if (typeof v.legend !== "string" || v.legend !== "" && !/^[a-f0-9]{64}$/.test(v.legend))
        throw Error("Invalid radar legend");
    if (["closed", "offline", "unsupported", "unavailable"].indexOf(v.status) >= 0 && (v.frames.length || v.legend !== ""))
        throw Error("Invalid unavailable radar payload");
    return v;
}
