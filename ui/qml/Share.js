.pragma library
.import "Forecast.js" as Forecast

// Input is the bridge's validated snapshot. Retain only the bounded export model
// and derived day periods, never raw hours, alert text, coordinates or saved places.
function dateLabel(value) {
    if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}$/.test(value))
        return "—";
    const date = new Date(value + "T00:00:00Z");
    if (!isFinite(date.getTime()) || date.toISOString().slice(0, 10) !== value)
        return "—";
    return ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][date.getUTCDay()] + ", " + date.getUTCDate() + " " + monthName(date.getUTCMonth()) + " " + date.getUTCFullYear();
}
function monthName(month) {
    return ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][month];
}
function clockHour(label) {
    const parts = typeof label === "string" ? label.match(/^(\d{1,2}) (AM|PM)$/) : null;
    if (!parts || Number(parts[1]) < 1 || Number(parts[1]) > 12)
        return -1;
    return Number(parts[1]) % 12 + (parts[2] === "PM" ? 12 : 0);
}
function nextDate(value) {
    if (dateLabel(value) === "—")
        return "";
    const date = new Date(value + "T00:00:00Z");
    date.setUTCDate(date.getUTCDate() + 1);
    return date.toISOString().slice(0, 10);
}
function periods(hours, date, temperature) {
    const groups = [
        {
            name: "Morning",
            range: "6 AM–noon",
            start: 6,
            end: 12
        },
        {
            name: "Afternoon",
            range: "Noon–6 PM",
            start: 12,
            end: 18
        },
        {
            name: "Evening",
            range: "6 PM–midnight",
            start: 18,
            end: 24
        }
    ];
    const followingDate = nextDate(date);
    return groups.map(group => {
        const temperatureRows = (hours || []).filter(hour => {
            const clock = clockHour(hour.local_hour);
            return hour.local_date === date && clock >= group.start && clock < group.end;
        });
        // Temperatures are point samples; precipitation describes the preceding
        // hour. Midnight's interval belongs to the previous day's evening.
        const probabilityRows = (hours || []).filter(hour => {
            const clock = clockHour(hour.local_hour);
            return hour.local_date === date && clock > group.start && clock <= group.end || group.end === 24 && followingDate !== "" && hour.local_date === followingDate && clock === 0;
        });
        const uniqueTemperatureHours = {};
        const uniqueProbabilityHours = {};
        const temperatures = [];
        const probabilities = [];
        let missing = false;
        temperatureRows.forEach(hour => {
            uniqueTemperatureHours[clockHour(hour.local_hour)] = true;
            if (hour.temperature_c === null || hour.temperature_c === undefined)
                missing = true;
            else
                temperatures.push(hour.temperature_c);
        });
        probabilityRows.forEach(hour => {
            uniqueProbabilityHours[clockHour(hour.local_hour)] = true;
            if (hour.precipitation_probability === null || hour.precipitation_probability === undefined)
                missing = true;
            else
                probabilities.push(hour.precipitation_probability);
        });
        const low = temperatures.length ? temperature(Math.min(...temperatures)) : "—";
        const high = temperatures.length ? temperature(Math.max(...temperatures)) : "—";
        return {
            name: group.name,
            range: group.range,
            temperatures: low === high ? low : low + "–" + high,
            precipitation: "Peak chance " + Forecast.percent(probabilities.length ? Math.max(...probabilities) : null),
            coverage: !temperatureRows.length && !probabilityRows.length ? "No hourly data" : Object.keys(uniqueTemperatureHours).length < 6 || Object.keys(uniqueProbabilityHours).length < 6 || missing ? "Partial" : ""
        };
    });
}
function summary(snapshot, includePlace) {
    const f = snapshot ? snapshot.forecast : null;
    if (!f || snapshot.location_settings.busy || ["fresh", "stale"].indexOf(snapshot.source.freshness) < 0)
        return null;
    const units = snapshot.controls.units;
    const temperature = v => Forecast.temp(v, units) + (v === null || v === undefined ? "" : units);
    const current = f.current;
    const alerts = snapshot.alerts;
    const alertNote = alerts.items.length ? (alerts.freshness === "current" ? "Official weather alerts are active. Check the issuing agency for current instructions." : "Cached weather alerts may be out of date. Check the issuing agency for current alerts.") : alerts.freshness === "current" ? "No active official alerts in the latest feed." : "Current official alert status unavailable.";
    return {
        place: includePlace ? snapshot.location : "Weather forecast",
        timezone: snapshot.timezone,
        temperature: temperature(current.temperature_c),
        condition: Forecast.title(current.condition),
        conditionKey: current.condition,
        isDay: current.is_day,
        feels: "Feels like " + temperature(current.apparent_temperature_c),
        wind: "Wind " + Forecast.wind(current.wind_speed_m_s, units, snapshot.controls.wind_units),
        valid: "Current conditions valid " + utc(current.time),
        retrieved: "Retrieved " + utc(f.fetched_at),
        freshness: snapshot.source.freshness === "stale" ? "Stale forecast · check for updates" : "Fresh when previewed",
        days: f.daily.slice(0, 3).map(d => ({
                    date: dateLabel(d.date),
                    condition: Forecast.title(d.condition),
                    temperatures: "High " + temperature(d.high_c) + " · Low " + temperature(d.low_c),
                    precipitation: "Precipitation chance " + Forecast.percent(d.precipitation_probability),
                    periods: periods(f.hourly, d.date, temperature)
                })),
        alerts: alertNote,
        source: f.source.name,
        attribution: f.source.attribution
    };
}
function utc(stamp) {
    const date = new Date(stamp);
    if (!isFinite(date.getTime()))
        return "—";
    return date.getUTCDate() + " " + monthName(date.getUTCMonth()) + " " + date.getUTCFullYear() + ", " + String(date.getUTCHours()).padStart(2, "0") + ":" + String(date.getUTCMinutes()).padStart(2, "0") + " UTC";
}
function text(v) {
    if (!v)
        return "";
    return [v.place, v.timezone, v.temperature + " · " + v.condition, v.feels + " · " + v.wind, v.valid, ...v.days.map(d => d.date + ": " + d.condition + ". " + d.temperatures + ". " + d.precipitation + ".\n" + d.periods.map(p => p.name + " (" + p.range + "): " + p.temperatures + ". " + p.precipitation + (p.coverage ? " · " + p.coverage : "") + ".").join("\n")), v.alerts, v.freshness + " · " + v.retrieved, v.source + " · " + v.attribution, "Shared from A Weather App"].join("\n");
}
