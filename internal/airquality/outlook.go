package airquality

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

const OutlookHours = 48
const OutlookResponseBytes = 64 * 1024

// Indices are calculated by the provider using its pollutant averaging rules.
// The six concentrations are instantaneous micrograms per cubic metre. They
// cannot be substituted into an index formula as if they were rolling means.
// Source: https://open-meteo.com/en/docs/air-quality-api (2026-10-09).
type OutlookValue struct {
	Number float64
	Known  bool
}
type OutlookHour struct {
	Time   time.Time
	Values [8]OutlookValue
}
type Outlook struct {
	Latitude, Longitude, GridLatitude, GridLongitude float64
	Timezone                                         string
	FetchedAt                                        time.Time
	Hours                                            []OutlookHour
}
type OutlookMetric struct {
	Key, Unit string
	Maximum   float64
}

// Return a value array so callers cannot mutate shared schema definitions.
func OutlookMetrics() [8]OutlookMetric {
	return [8]OutlookMetric{
		{"us_aqi", "USAQI", 1000}, {"european_aqi", "EAQI", 1000},
		{"pm2_5", "μg/m³", 5000}, {"pm10", "μg/m³", 5000},
		{"nitrogen_dioxide", "μg/m³", 5000}, {"ozone", "μg/m³", 5000},
		{"sulphur_dioxide", "μg/m³", 5000}, {"carbon_monoxide", "μg/m³", 100000},
	}
}

func OutlookRequestURL(location Object) (string, error) {
	l, err := weather.ValidateLocation(location)
	if err != nil {
		return "", err
	}
	names := []string{}
	for _, f := range OutlookMetrics() {
		names = append(names, f.Key)
	}
	q := url.Values{"latitude": {fmt.Sprint(l["latitude"])}, "longitude": {fmt.Sprint(l["longitude"])},
		"hourly": {strings.Join(names, ",")}, "domains": {domain}, "timezone": {"GMT"},
		"timeformat": {"unixtime"}, "forecast_hours": {"48"}}
	return (&url.URL{Scheme: "https", Host: endpointHost, Path: endpointPath, RawQuery: q.Encode()}).String(), nil
}

func FetchOutlook(ctx context.Context, location Object, at time.Time) (Outlook, error) {
	return fetchOutlook(ctx, location, at, sharedTransport)
}
func fetchOutlook(ctx context.Context, location Object, at time.Time, transport *http.Transport) (result Outlook, err error) {
	endpoint, err := OutlookRequestURL(location)
	if err != nil || at.IsZero() {
		return Outlook{}, errors.New("invalid AQ outlook context")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			result, err = Outlook{}, ctx.Err()
		}
	}()
	payload, err := requestPayload(ctx, endpoint, OutlookResponseBytes, transport)
	if err != nil {
		return Outlook{}, err
	}
	return ParseOutlook(payload, location, at)
}

// UTC is requested deliberately; local labels are formed from the selected
// IANA zone in the service, including daylight-saving transitions.
func ParseOutlook(payload, location Object, at time.Time) (Outlook, error) {
	l, err := weather.ValidateLocation(location)
	if err != nil || at.IsZero() {
		return Outlook{}, errors.New("invalid AQ outlook context")
	}
	d := Outlook{Latitude: l["latitude"].(float64), Longitude: l["longitude"].(float64), Timezone: l["timezone"].(string), FetchedAt: at.UTC()}
	zone, _ := payload["timezone"].(string)
	_, claimedDomain := payload["domain"]
	_, claimedDomains := payload["domains"]
	if (payload["error"] != nil && payload["error"] != false) || claimedDomain || claimedDomains || (zone != "GMT" && zone != "UTC" && zone != "Etc/GMT" && zone != "Etc/UTC") || payload["utc_offset_seconds"] != float64(0) || !gridClose(payload, l) {
		return Outlook{}, errors.New("invalid AQ outlook source or grid")
	}
	d.GridLatitude, _ = number(payload["latitude"])
	d.GridLongitude, _ = number(payload["longitude"])
	hourly, units := object(payload["hourly"]), object(payload["hourly_units"])
	times, ok := hourly["time"].([]any)
	if !ok || len(times) == 0 || len(times) > OutlookHours || units["time"] != "unixtime" {
		return Outlook{}, errors.New("invalid AQ outlook hours")
	}
	var columns [8][]any
	metrics := OutlookMetrics()
	for k, f := range metrics {
		values, ok := hourly[f.Key].([]any)
		if ok && len(values) == len(times) && units[f.Key] == f.Unit {
			columns[k] = values
		}
	}
	d.Hours = make([]OutlookHour, len(times))
	for i, value := range times {
		t, err := epoch(value)
		if err != nil {
			return Outlook{}, err
		}
		d.Hours[i].Time = t
		for k, column := range columns {
			if column == nil {
				continue
			}
			if n, ok := bounded(column[i], metrics[k].Maximum); ok {
				d.Hours[i].Values[k] = OutlookValue{Number: n, Known: true}
			}
		}
	}
	return d, d.Validate()
}

func (d Outlook) Validate() error {
	bad := errors.New("invalid AQ outlook data")
	if _, err := weather.ValidateLocation(Object{"name": "AQ outlook", "latitude": d.Latitude, "longitude": d.Longitude, "timezone": d.Timezone}); err != nil {
		return bad
	}
	if !gridClose(Object{"latitude": d.GridLatitude, "longitude": d.GridLongitude}, Object{"latitude": d.Latitude, "longitude": d.Longitude}) || d.FetchedAt.IsZero() || d.FetchedAt.Year() < 2000 || d.FetchedAt.Year() > 2100 || len(d.Hours) == 0 || len(d.Hours) > OutlookHours {
		return bad
	}
	known := false
	metrics := OutlookMetrics()
	for i, h := range d.Hours {
		if h.Time.Nanosecond() != 0 || h.Time.Unix()%3600 != 0 || h.Time.Before(d.FetchedAt.Add(-2*time.Hour)) || h.Time.After(d.FetchedAt.Add(50*time.Hour)) || (i > 0 && !h.Time.After(d.Hours[i-1].Time)) {
			return bad
		}
		for k, v := range h.Values {
			if math.IsNaN(v.Number) || math.IsInf(v.Number, 0) || v.Number < 0 || v.Number > metrics[k].Maximum || (!v.Known && v.Number != 0) {
				return bad
			}
			known = known || v.Known
		}
	}
	if !known {
		return bad
	}
	return nil
}
