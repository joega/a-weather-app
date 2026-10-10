package weather

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

const currentSummaryMaxBytes = 16 * 1024

// CurrentSummaryURL asks for only the values displayed on a saved-location card.
// It never requests hourly, daily or alert data.
func CurrentSummaryURL(location Object) (string, error) {
	l, err := ValidateLocation(location)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"latitude":         {fmt.Sprint(l["latitude"])},
		"longitude":        {fmt.Sprint(l["longitude"])},
		"current":          {"temperature_2m,weather_code,is_day"},
		"temperature_unit": {"celsius"},
		"timeformat":       {"unixtime"},
		"timezone":         {l["timezone"].(string)},
	}
	return "https://api.open-meteo.com/v1/forecast?" + q.Encode(), nil
}

// ParseCurrentSummary validates a current-only reply before it can replace a
// saved card's earlier weather. Invalid or old replies cannot look current.
func ParseCurrentSummary(payload, location Object, now time.Time) (Object, error) {
	l, err := ValidateLocation(location)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if zone, present := payload["timezone"]; present && zone != l["timezone"] {
		return nil, errors.New("unexpected current summary timezone")
	}
	if err := units(payload, "current", Object{"temperature_2m": "°C", "weather_code": "wmo code", "is_day": ""}); err != nil {
		return nil, err
	}
	row := obj(payload["current"])
	validAt, err := epoch(row["time"])
	if err != nil {
		return nil, err
	}
	validTime, err := Instant(validAt)
	if err != nil {
		return nil, err
	}
	if validTime.After(now.Add(5*time.Minute)) || validTime.Before(now.Add(-45*time.Minute)) {
		return nil, errors.New("current summary time outside freshness window")
	}
	temperature, err := bounded(row["temperature_2m"], -150, 100, false)
	if err != nil {
		return nil, err
	}
	condition, err := Condition(row["weather_code"])
	if err != nil {
		return nil, err
	}
	day, err := bounded(row["is_day"], 0, 1, false)
	if err != nil || (day != float64(0) && day != float64(1)) {
		return nil, errors.New("invalid current summary daylight")
	}
	record, err := WeatherRecord(Object{"time": validAt, "temperature_c": temperature, "condition": condition, "is_day": day == float64(1)})
	if err != nil {
		return nil, err
	}
	return Object{
		"fetched_at":    stamp(now),
		"valid_at":      record["time"],
		"temperature_c": record["temperature_c"],
		"condition":     record["condition"],
		"is_day":        record["is_day"],
	}, nil
}

// FetchCurrentSummary shares forecast transport safety and cancellation while
// bounding this smaller reply independently of a full forecast.
func FetchCurrentSummary(ctx context.Context, location Object, now time.Time) (Object, error) {
	return defaultJSONProvider().fetchCurrentSummary(ctx, location, now)
}

func (provider jsonProvider) fetchCurrentSummary(ctx context.Context, location Object, now time.Time) (Object, error) {
	rawURL, err := CurrentSummaryURL(location)
	if err != nil {
		return nil, err
	}
	payload, err := provider.fetchJSONBound(ctx, rawURL, false, currentSummaryMaxBytes)
	if err != nil {
		return nil, err
	}
	return ParseCurrentSummary(payload, location, now)
}
