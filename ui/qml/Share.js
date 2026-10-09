.pragma library
.import "Forecast.js" as Forecast

// Input is the bridge's validated snapshot. Retain only the short export model,
// never the full hourly dataset, alert text, coordinates or saved-place list.
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
                    date: d.date,
                    condition: Forecast.title(d.condition),
                    temperatures: "High " + temperature(d.high_c) + " · Low " + temperature(d.low_c),
                    precipitation: "Precipitation chance " + Forecast.percent(d.precipitation_probability)
                })),
        alerts: alertNote,
        source: f.source.name,
        attribution: f.source.attribution
    };
}
function utc(stamp) {
    return new Date(stamp).toISOString().slice(0, 16).replace("T", " ") + " UTC";
}
function text(v) {
    if (!v)
        return "";
    return [v.place, v.timezone, v.temperature + " · " + v.condition, v.feels + " · " + v.wind, v.valid, ...v.days.map(d => d.date + ": " + d.condition + ". " + d.temperatures + ". " + d.precipitation + "."), v.alerts, v.freshness + " · " + v.retrieved, v.source + " · " + v.attribution, "Shared from A Weather App"].join("\n");
}
