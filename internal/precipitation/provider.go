// Package precipitation owns bounded, on-demand rain and snow detail data.
// Amounts describe preceding-hour intervals; depth and freezing level are
// instantaneous values at the interval's end. No amounts are inferred from
// temperature or weather codes.
// Provider semantics: https://open-meteo.com/en/docs (hourly variables).
// The provider uses a fixed timezone offset for its timeline; grouping here
// uses returned Unix timestamps and the selected place's IANA timezone instead.
package precipitation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/providerhttp"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const (
	MaxHours         = 290 // 26 past + 264 forecast, enough for ten complete local days.
	MaxResponseBytes = 128 * 1024
	Source           = "Open-Meteo best match"
)

type Value struct {
	Number float64
	Known  bool
}

func Number(n float64) Value { return Value{Number: n, Known: true} }

type Hour struct {
	End                                time.Time
	TotalMM, RainMM, ShowersMM, SnowCM Value
	DepthM, FreezingM, Probability     Value
}
type Data struct {
	Latitude, Longitude, GridLatitude, GridLongitude float64
	Timezone                                         string
	FetchedAt                                        time.Time
	Hours                                            []Hour
}

var fields = []struct {
	name, unit string
	maximum    float64
}{
	{"precipitation", "mm", 10000}, {"rain", "mm", 10000}, {"showers", "mm", 10000},
	{"snowfall", "cm", 10000}, {"snow_depth", "m", 1000}, {"freezing_level_height", "m", 30000},
	{"precipitation_probability", "%", 100},
}

func RequestURL(location weather.Object) (string, error) {
	l, err := weather.ValidateLocation(location)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, f.name)
	}
	q := url.Values{"latitude": {fmt.Sprint(l["latitude"])}, "longitude": {fmt.Sprint(l["longitude"])}, "timezone": {l["timezone"].(string)}, "timeformat": {"unixtime"}, "precipitation_unit": {"mm"}, "hourly": {strings.Join(names, ",")}, "past_hours": {"26"}, "forecast_hours": {"264"}}
	return "https://api.open-meteo.com/v1/forecast?" + q.Encode(), nil
}

var sharedTransport = providerhttp.New(http.DefaultTransport.(*http.Transport), false)

func CloseIdleConnections() { sharedTransport.CloseIdleConnections() }

func Fetch(ctx context.Context, location weather.Object, at time.Time) (Data, error) {
	return fetch(ctx, location, at, sharedTransport)
}

// Inject only a private transport in tests; endpoint, redirect, timeout,
// decoding, body-size and cancellation policies remain the production ones.
func fetch(ctx context.Context, location weather.Object, at time.Time, transport *http.Transport) (result Data, err error) {
	endpoint, err := RequestURL(location)
	if err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	defer func() {
		if e := ctx.Err(); e != nil {
			result, err = Data{}, e
		}
	}()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("precipitation redirects refused") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "a-weather-app/0.2 (A Weather App; Linux desktop weather)")
	resp, err := client.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("precipitation HTTP status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return result, err
	}
	payload, err := safeio.Object(raw, MaxResponseBytes)
	if err != nil {
		return result, err
	}
	return Parse(payload, location, at)
}

func Parse(payload, location weather.Object, at time.Time) (Data, error) {
	l, err := weather.ValidateLocation(location)
	if err != nil || at.IsZero() {
		return Data{}, errors.New("invalid precipitation context")
	}
	d := Data{Latitude: l["latitude"].(float64), Longitude: l["longitude"].(float64), Timezone: l["timezone"].(string), FetchedAt: at.UTC()}
	zone, ok := payload["timezone"].(string)
	utc := func(v string) bool { return v == "UTC" || v == "GMT" || v == "Etc/UTC" || v == "Etc/GMT" }
	if !ok || zone != d.Timezone && !(utc(zone) && utc(d.Timezone)) {
		return Data{}, errors.New("unexpected precipitation timezone")
	}
	var lonOK, latOK bool
	d.GridLatitude, latOK = payload["latitude"].(float64)
	d.GridLongitude, lonOK = payload["longitude"].(float64)
	if !latOK || !lonOK {
		return Data{}, errors.New("missing precipitation grid")
	}
	hourly, _ := payload["hourly"].(map[string]any)
	units, _ := payload["hourly_units"].(map[string]any)
	times, ok := hourly["time"].([]any)
	if !ok || len(times) == 0 || len(times) > MaxHours || units["time"] != "unixtime" {
		return Data{}, errors.New("invalid precipitation hours")
	}
	series := make([][]any, len(fields))
	for i, f := range fields {
		v, ok := hourly[f.name].([]any)
		if ok && len(v) == len(times) && units[f.name] == f.unit {
			series[i] = v
		}
	}
	d.Hours = make([]Hour, len(times))
	for i, raw := range times {
		n, ok := raw.(float64)
		if !ok || !finite(n) || math.Trunc(n) != n || n < float64(at.Add(-28*time.Hour).Unix()) || n > float64(at.Add(266*time.Hour).Unix()) {
			return Data{}, errors.New("invalid precipitation time")
		}
		h := &d.Hours[i]
		h.End = time.Unix(int64(n), 0).UTC()
		values := []*Value{&h.TotalMM, &h.RainMM, &h.ShowersMM, &h.SnowCM, &h.DepthM, &h.FreezingM, &h.Probability}
		for k, field := range fields {
			if series[k] == nil {
				continue
			}
			n, ok := series[k][i].(float64)
			if ok && finite(n) && n >= 0 && n <= field.maximum {
				if field.name == "precipitation_probability" {
					n /= 100
				}
				*values[k] = Number(n)
			}
		}
	}
	return d, d.Validate()
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func (d Data) Validate() error {
	bad := errors.New("invalid precipitation data")
	if !finite(d.Latitude) || !finite(d.Longitude) || !finite(d.GridLatitude) || !finite(d.GridLongitude) || math.Abs(d.Latitude) > 90 || math.Abs(d.Longitude) > 180 || math.Abs(d.GridLatitude) > 90 || math.Abs(d.GridLongitude) > 180 {
		return bad
	}
	// Best-match grids may be displaced from the requested coordinate. Refuse
	// an unrelated grid; wrap the longitude difference at the date line.
	if math.Abs(d.GridLatitude-d.Latitude) > 1.5 || math.Abs(math.Remainder(d.GridLongitude-d.Longitude, 360)) > 1.5 {
		return bad
	}
	if len(d.Timezone) > 100 || d.Timezone == "" || d.Timezone == "Local" {
		return bad
	}
	if _, err := time.LoadLocation(d.Timezone); err != nil {
		return bad
	}
	if d.FetchedAt.IsZero() || len(d.Hours) == 0 || len(d.Hours) > MaxHours {
		return bad
	}
	for i, h := range d.Hours {
		if h.End.Nanosecond() != 0 || h.End.Before(d.FetchedAt.Add(-28*time.Hour)) || h.End.After(d.FetchedAt.Add(266*time.Hour)) {
			return bad
		}
		if i > 0 {
			delta := h.End.Sub(d.Hours[i-1].End)
			if delta < time.Hour || delta%time.Hour != 0 {
				return bad
			}
		}
		for k, v := range []Value{h.TotalMM, h.RainMM, h.ShowersMM, h.SnowCM, h.DepthM, h.FreezingM, h.Probability} {
			limit := fields[k].maximum
			if k == 6 {
				limit = 1
			}
			if !finite(v.Number) || v.Number < 0 || v.Number > limit || !v.Known && v.Number != 0 {
				return bad
			}
		}
	}
	return nil
}
