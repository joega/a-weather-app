.pragma library

// The numeric indices are supplied by Open-Meteo, not reconstructed from the
// displayed instantaneous pollutant concentrations. Classify the same rounded
// index displayed by the card so a boundary label cannot contradict its value.
// Sources checked 2026-10-09: open-meteo.com/en/docs/air-quality-api,
// airnow.gov/aqi/aqi-basics, airindex.eea.europa.eu/AQI/index.html.
function category(value, scale) {
    if (typeof value !== "number" || !Number.isFinite(value) || value < 0 || value > 1000)
        return "Unavailable";
    const index = Math.round(value);
    if (scale === "us") {
        if (index <= 50)
            return "Good";
        if (index <= 100)
            return "Moderate";
        if (index <= 150)
            return "Unhealthy for sensitive groups";
        if (index <= 200)
            return "Unhealthy";
        if (index <= 300)
            return "Very unhealthy";
        return "Hazardous";
    }
    if (scale === "eu") {
        if (index <= 20)
            return "Good";
        if (index <= 40)
            return "Fair";
        if (index <= 60)
            return "Moderate";
        if (index <= 80)
            return "Poor";
        if (index <= 100)
            return "Very poor";
        return "Extremely poor";
    }
    return "Unavailable";
}
