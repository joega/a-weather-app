// Package weathermap fetches one bounded, explicit-model hourly lattice for a
// fixed ten-mile view. It never silently combines fields from different models.
package weathermap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const RadiusMiles = 10
const Samples = 5
const MaxResponseBytes = 192 * 1024

type Model struct {
	ID, Name, Endpoint string
	ResolutionKM       float64
}

var (
	errCoverage = errors.New("regional map coverage unavailable")
	nbm         = Model{"ncep_nbm_conus", "NOAA NBM CONUS", "https://api.open-meteo.com/v1/gfs", 2.5}
	d2          = Model{"dwd_icon_d2", "DWD ICON-D2", "https://api.open-meteo.com/v1/dwd-icon", 2}
	hrdps       = Model{"cmc_gem_hrdps", "ECCC GEM HRDPS", "https://api.open-meteo.com/v1/gem", 2.5}
	gfs         = Model{"ncep_gfs_global", "NOAA GFS global", "https://api.open-meteo.com/v1/gfs", 13}
)

type Cell struct {
	Latitude      float64   `json:"latitude"`
	Longitude     float64   `json:"longitude"`
	Temperature   []float64 `json:"temperature_c"`
	WindSpeed     []float64 `json:"wind_speed_m_s"`
	WindDirection []float64 `json:"wind_from_deg"`
	Precipitation []float64 `json:"precipitation_mm"`
}

type Data struct {
	Latitude     float64   `json:"latitude"`
	Longitude    float64   `json:"longitude"`
	RadiusMiles  int       `json:"radius_miles"`
	ModelID      string    `json:"model_id"`
	ModelName    string    `json:"model_name"`
	ResolutionKM float64   `json:"resolution_km"`
	FetchedAt    time.Time `json:"fetched_at"`
	Hours        []int64   `json:"hours"`
	Cells        []Cell    `json:"cells"`
	Attribution  string    `json:"attribution"`
}

type rawCell struct {
	Latitude    *float64          `json:"latitude"`
	Longitude   *float64          `json:"longitude"`
	HourlyUnits map[string]string `json:"hourly_units"`
	Hourly      struct {
		Time          []int64    `json:"time"`
		Temperature   []*float64 `json:"temperature_2m"`
		WindSpeed     []*float64 `json:"wind_speed_10m"`
		WindDirection []*float64 `json:"wind_direction_10m"`
		Precipitation []*float64 `json:"precipitation"`
	} `json:"hourly"`
}

// Candidate uses broad bounds only to avoid obvious out-of-domain requests.
// The complete live lattice is validated before a regional model is accepted.
func Candidate(lat, lon float64, country string) Model {
	if country == "CA" && lat >= 42 && lat <= 60 && lon >= -135 && lon <= -55 {
		return hrdps
	}
	if lat >= 25 && lat <= 49 && lon >= -124 && lon <= -67 {
		return nbm
	}
	if lat >= 46 && lat <= 55 && lon >= 6 && lon <= 16 {
		return d2
	}
	if lat >= 44 && lat <= 60 && lon >= -135 && lon <= -55 {
		return hrdps
	}
	return gfs
}

func Coordinates(lat, lon float64) ([]float64, []float64, error) {
	if !finite(lat) || !finite(lon) || lat < -84.8 || lat > 84.8 || lon < -180 || lon > 180 {
		return nil, nil, errors.New("invalid map center")
	}
	// 10 statute miles, sampled over a 5 by 5 lattice. The map displays the
	// returned model-cell coordinates, which can be snapped by the provider.
	stepLat := RadiusMiles * 1609.344 / 2 / 111320
	stepLon := stepLat / math.Cos(lat*math.Pi/180)
	if stepLon > 2 {
		stepLon = 2
	}
	lats, lons := make([]float64, 0, Samples*Samples), make([]float64, 0, Samples*Samples)
	for row := 0; row < Samples; row++ {
		for col := 0; col < Samples; col++ {
			la, lo := lat+float64(2-row)*stepLat, lon+float64(col-2)*stepLon
			for lo > 180 {
				lo -= 360
			}
			for lo < -180 {
				lo += 360
			}
			lats = append(lats, la)
			lons = append(lons, lo)
		}
	}
	return lats, lons, nil
}

func URL(model Model, lat, lon float64) (string, error) {
	lats, lons, e := Coordinates(lat, lon)
	if e != nil {
		return "", e
	}
	a, b := make([]string, len(lats)), make([]string, len(lons))
	for i := range lats {
		a[i] = strconv.FormatFloat(lats[i], 'f', 5, 64)
		b[i] = strconv.FormatFloat(lons[i], 'f', 5, 64)
	}
	v := url.Values{"latitude": {strings.Join(a, ",")}, "longitude": {strings.Join(b, ",")}, "models": {model.ID}, "hourly": {"temperature_2m,wind_speed_10m,wind_direction_10m,precipitation"}, "forecast_hours": {"24"}, "timeformat": {"unixtime"}, "wind_speed_unit": {"ms"}, "precipitation_unit": {"mm"}, "timezone": {"UTC"}}
	return model.Endpoint + "?" + v.Encode(), nil
}

func Fetch(ctx context.Context, client *http.Client, lat, lon float64, country string, now time.Time) (Data, error) {
	model := Candidate(lat, lon, country)
	data, e := fetchModel(ctx, client, model, lat, lon, now)
	if e == nil {
		return data, nil
	}
	if model.ID == gfs.ID || ctx.Err() != nil || !errors.Is(e, errCoverage) {
		return Data{}, e
	}
	// A regional grid can extend beyond the documented footprint. Keep all
	// fields and hours on one model by restarting the whole lattice on GFS.
	return fetchModel(ctx, client, gfs, lat, lon, now)
}

func fetchModel(ctx context.Context, client *http.Client, model Model, lat, lon float64, now time.Time) (Data, error) {
	endpoint, e := URL(model, lat, lon)
	if e != nil {
		return Data{}, e
	}
	request, e := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if e != nil {
		return Data{}, e
	}
	request.Header.Set("User-Agent", "a-weather-app/0.3 (Linux desktop weather map)")
	request.Header.Set("Accept", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("map redirect refused") }}
	}
	response, e := client.Do(request)
	if e != nil {
		return Data{}, e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == http.StatusBadRequest && model.ID != gfs.ID {
			// The API returns this specific 400 when an explicit regional model
			// has no coverage. Other 400s (and overloads) must not spend a
			// second 25-location request on the global fallback.
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 513))
			if readErr == nil && len(body) <= 512 {
				var failure struct {
					Error  bool   `json:"error"`
					Reason string `json:"reason"`
				}
				if json.Unmarshal(body, &failure) == nil && failure.Error && failure.Reason == "No data is available for this location" {
					return Data{}, errCoverage
				}
			}
		}
		return Data{}, fmt.Errorf("map HTTP status %d", response.StatusCode)
	}
	body, e := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if e != nil {
		return Data{}, e
	}
	if len(body) > MaxResponseBytes {
		return Data{}, errors.New("map response too large")
	}
	var raw []rawCell
	if e = json.Unmarshal(body, &raw); e != nil {
		return Data{}, e
	}
	if len(raw) != Samples*Samples {
		return Data{}, errors.New("incomplete map lattice")
	}
	requestedLat, requestedLon, _ := Coordinates(lat, lon)
	data := Data{Latitude: lat, Longitude: lon, RadiusMiles: RadiusMiles, ModelID: model.ID, ModelName: model.Name, ResolutionKM: model.ResolutionKM, FetchedAt: now.UTC().Truncate(time.Second), Attribution: "Model forecast via Open-Meteo (CC BY 4.0)"}
	count := 24
	for i, cell := range raw {
		if cell.Latitude == nil || cell.Longitude == nil {
			if cell.Latitude == nil && cell.Longitude == nil && len(cell.HourlyUnits) == 0 && len(cell.Hourly.Time) == 0 {
				return Data{}, errCoverage
			}
			return Data{}, errors.New("invalid map grid coordinate")
		}
		if cell.HourlyUnits["time"] != "unixtime" || cell.HourlyUnits["temperature_2m"] != "°C" || cell.HourlyUnits["wind_speed_10m"] != "m/s" || cell.HourlyUnits["wind_direction_10m"] != "°" || cell.HourlyUnits["precipitation"] != "mm" {
			return Data{}, errors.New("unexpected map units")
		}
		if !finite(*cell.Latitude) || !finite(*cell.Longitude) || *cell.Latitude < -85 || *cell.Latitude > 85 || *cell.Longitude < -180 || *cell.Longitude > 180 || distanceKM(requestedLat[i], requestedLon[i], *cell.Latitude, *cell.Longitude) > model.ResolutionKM*1.5+2 {
			return Data{}, errors.New("invalid map grid coordinate")
		}
		h := cell.Hourly
		length := len(h.Time)
		if length == 0 || length > 24 || len(h.Temperature) != length || len(h.WindSpeed) != length || len(h.WindDirection) != length || len(h.Precipitation) != length {
			return Data{}, errors.New("invalid map hours")
		}
		if i == 0 {
			data.Hours = append([]int64(nil), h.Time...)
		} else if len(data.Hours) != length {
			return Data{}, errors.New("inconsistent map hours")
		}
		for j, t := range h.Time {
			if t != data.Hours[j] || (j > 0 && t-h.Time[j-1] != 3600) || t < now.Add(-2*time.Hour).Unix() || t > now.Add(26*time.Hour).Unix() {
				return Data{}, errors.New("invalid map time")
			}
		}
		row := Cell{Latitude: *cell.Latitude, Longitude: *cell.Longitude}
		for j := 0; j < length; j++ {
			values := []*float64{h.Temperature[j], h.WindSpeed[j], h.WindDirection[j], h.Precipitation[j]}
			if values[0] == nil || values[1] == nil || values[2] == nil || values[3] == nil {
				count = min(count, j)
				break
			}
			for k, v := range values {
				bounds := [][2]float64{{-100, 70}, {0, 150}, {0, 360}, {0, 500}}[k]
				if !finite(*v) || *v < bounds[0] || *v > bounds[1] {
					return Data{}, errors.New("invalid map value")
				}
			}
			row.Temperature = append(row.Temperature, *values[0])
			row.WindSpeed = append(row.WindSpeed, *values[1])
			row.WindDirection = append(row.WindDirection, *values[2])
			row.Precipitation = append(row.Precipitation, *values[3])
		}
		data.Cells = append(data.Cells, row)
	}
	if count < 1 {
		return Data{}, errCoverage
	}
	data.Hours = data.Hours[:count]
	for i := range data.Cells {
		c := &data.Cells[i]
		if len(c.Temperature) < count || len(c.WindSpeed) < count || len(c.WindDirection) < count || len(c.Precipitation) < count {
			return Data{}, errors.New("noncontiguous map availability")
		}
		c.Temperature = c.Temperature[:count]
		c.WindSpeed = c.WindSpeed[:count]
		c.WindDirection = c.WindDirection[:count]
		c.Precipitation = c.Precipitation[:count]
	}
	if e := Validate(data); e != nil {
		return Data{}, e
	}
	return data, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func Validate(d Data) error {
	model := gfs
	switch d.ModelID {
	case nbm.ID:
		model = nbm
	case d2.ID:
		model = d2
	case hrdps.ID:
		model = hrdps
	case gfs.ID:
		model = gfs
	default:
		return errors.New("invalid map model")
	}
	if d.ModelName != model.Name || d.ResolutionKM != model.ResolutionKM || d.RadiusMiles != RadiusMiles || d.Attribution != "Model forecast via Open-Meteo (CC BY 4.0)" || len(d.Hours) < 1 || len(d.Hours) > 24 || len(d.Cells) != Samples*Samples || d.FetchedAt.IsZero() {
		return errors.New("invalid map metadata")
	}
	lats, lons, e := Coordinates(d.Latitude, d.Longitude)
	if e != nil {
		return e
	}
	for i, h := range d.Hours {
		if h < d.FetchedAt.Add(-2*time.Hour).Unix() || h > d.FetchedAt.Add(26*time.Hour).Unix() || (i > 0 && h-d.Hours[i-1] != 3600) {
			return errors.New("invalid map timeline")
		}
	}
	for i, c := range d.Cells {
		if !finite(c.Latitude) || !finite(c.Longitude) || c.Latitude < -85 || c.Latitude > 85 || c.Longitude < -180 || c.Longitude > 180 || distanceKM(lats[i], lons[i], c.Latitude, c.Longitude) > model.ResolutionKM*1.5+2 || len(c.Temperature) != len(d.Hours) || len(c.WindSpeed) != len(d.Hours) || len(c.WindDirection) != len(d.Hours) || len(c.Precipitation) != len(d.Hours) {
			return errors.New("invalid map cell")
		}
		for j := range d.Hours {
			if !bound(c.Temperature[j], -100, 70) || !bound(c.WindSpeed[j], 0, 150) || !bound(c.WindDirection[j], 0, 360) || !bound(c.Precipitation[j], 0, 500) {
				return errors.New("invalid map value")
			}
		}
	}
	return nil
}
func bound(v, lo, hi float64) bool { return finite(v) && v >= lo && v <= hi }
func distanceKM(lat1, lon1, lat2, lon2 float64) float64 {
	a, b := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	h := math.Pow(math.Sin(a/2), 2) + math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*math.Pow(math.Sin(b/2), 2)
	return 12742 * math.Asin(math.Min(1, math.Sqrt(h)))
}
