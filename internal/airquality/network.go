package airquality

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const responseMaxBytes = 16 * 1024
const endpointHost = "air-quality-api.open-meteo.com"
const endpointPath = "/v1/air-quality"
const userAgent = "a-weather-app/0.2 (A Weather App; Linux Hyprland desktop weather)"

func RequestURL(location Object) (string, error) {
	l, e := weather.ValidateLocation(location)
	if e != nil {
		return "", e
	}
	q := url.Values{
		"latitude":      {fmt.Sprint(l["latitude"])},
		"longitude":     {fmt.Sprint(l["longitude"])},
		"current":       {"us_aqi,european_aqi,pm2_5"},
		"domains":       {domain},
		"timezone":      {"GMT"},
		"timeformat":    {"unixtime"},
		"forecast_days": {"1"},
	}
	u := url.URL{Scheme: "https", Host: endpointHost, Path: endpointPath, RawQuery: q.Encode()}
	return u.String(), nil
}

func Fetch(ctx context.Context, location Object, fetchedAt time.Time) (Object, error) {
	rawURL, e := RequestURL(location)
	if e != nil {
		return nil, e
	}
	if fetchedAt.IsZero() {
		fetchedAt = time.Now()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("air quality redirects refused") }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("air quality HTTP status %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, responseMaxBytes+1))
	if e != nil {
		return nil, e
	}
	payload, e := safeio.Object(raw, responseMaxBytes)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	record, e := Parse(payload, location, fetchedAt)
	if e != nil {
		return nil, e
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return record, nil
}
