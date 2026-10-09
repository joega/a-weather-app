// Package outdoor finds hourly forecast windows that fit personal preferences.
// It performs no I/O, schedules no work, and makes no safety or accuracy score.
package outdoor

import (
	"errors"
	"math"
	"sort"
	"time"
)

const (
	MaxHours   = 240
	MaxDays    = 10
	Horizon    = 48 * time.Hour
	MaxResults = 3
)

type Preferences struct {
	Hours                    int     `json:"hours"`
	MinTemperatureC          float64 `json:"min_temperature_c"`
	MaxTemperatureC          float64 `json:"max_temperature_c"`
	MaxProbability           float64 `json:"max_probability"`
	MaxHourlyPrecipitationMM float64 `json:"max_hourly_precipitation_mm"`
	MaxWindMS                float64 `json:"max_wind_m_s"`
	MaxGustMS                float64 `json:"max_gust_m_s"`
	DaylightOnly             bool    `json:"daylight_only"`
}

func DefaultPreferences() Preferences {
	return Preferences{Hours: 1, MinTemperatureC: 10, MaxTemperatureC: 27, MaxProbability: .2, MaxHourlyPrecipitationMM: .1, MaxWindMS: 6, MaxGustMS: 10, DaylightOnly: true}
}
func finite(n float64) bool          { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func between(n, lo, hi float64) bool { return finite(n) && n >= lo && n <= hi }
func (p Preferences) Valid() bool {
	return p.Hours >= 1 && p.Hours <= 4 && between(p.MinTemperatureC, -80, 60) && between(p.MaxTemperatureC, p.MinTemperatureC, 60) && between(p.MaxProbability, 0, 1) && between(p.MaxHourlyPrecipitationMM, 0, 100) && between(p.MaxWindMS, 0, 75) && between(p.MaxGustMS, p.MaxWindMS, 100)
}

// Value keeps absent readings separate from zero without per-sample allocation.
type Value struct {
	Number float64
	Known  bool
}

func Number(n float64) Value           { return Value{n, finite(n)} }
func (v Value) in(lo, hi float64) bool { return v.Known && between(v.Number, lo, hi) }

type Hour struct {
	Time time.Time
	// Temperature and wind are point samples. Precipitation probability, amount
	// and maximum gust describe the hour ENDING at Time, matching our provider.
	TemperatureC, WindMS                 Value
	Probability, PrecipitationMM, GustMS Value
}
type Daylight struct {
	Date            string
	Sunrise, Sunset time.Time
}
type Range struct {
	Low, High float64
	Known     bool
}

func (r *Range) add(v Value, lo, hi float64) bool {
	if !v.in(lo, hi) {
		return false
	}
	if !r.Known {
		r.Low, r.High, r.Known = v.Number, v.Number, true
	} else {
		r.Low = math.Min(r.Low, v.Number)
		r.High = math.Max(r.High, v.Number)
	}
	return true
}

type Window struct {
	Start, End time.Time
	Fits       bool
	// These stable keys describe both missing inputs and exceeded preferences.
	// Known partial statistics may be shown, but a partial window never Fits.
	Missing, Exceeds                                                 []string
	TemperatureC                                                     Range
	PeakProbability, PeakHourlyPrecipitationMM, TotalPrecipitationMM Value
	PeakWindMS, PeakGustMS                                           Value
	Daylight                                                         string // yes, no, unknown, or not_requested
}
type Result struct {
	Windows               []Window
	Evaluated, Incomplete int
	// Candidates interrupted by a missing hour are excluded, not interpolated.
	Gaps int
}
type candidate struct {
	Window
	penalty float64
}

// Rank examines at most 240 rows, a 48-hour horizon and four-hour windows.
// Results do not overlap, remain in preference order, and use actual elapsed
// time across DST. Missing values always sort after complete-data windows.
// The private ranking penalty is a preference distance, never a probability.
func Rank(hours []Hour, days []Daylight, zone *time.Location, now time.Time, p Preferences) (Result, error) {
	result := Result{Windows: []Window{}}
	if !p.Valid() || zone == nil || now.IsZero() || len(hours) > MaxHours || len(days) > MaxDays {
		return result, errors.New("invalid outdoor planning input")
	}
	for i, h := range hours {
		if h.Time.IsZero() || (i > 0 && !h.Time.After(hours[i-1].Time)) {
			return result, errors.New("unordered outdoor forecast")
		}
	}
	daylight := make(map[string]Daylight, len(days))
	for _, d := range days {
		if _, err := time.Parse("2006-01-02", d.Date); err != nil {
			return result, errors.New("invalid daylight date")
		}
		if _, ok := daylight[d.Date]; ok {
			return result, errors.New("duplicate daylight date")
		}
		daylight[d.Date] = d
	}
	candidates := make([]candidate, 0, 48)
	endLimit := now.Add(Horizon)
	for i, h := range hours {
		if h.Time.Before(now) {
			continue
		}
		if i+p.Hours >= len(hours) || hours[i+p.Hours].Time.After(endLimit) {
			break
		}
		continuous := true
		for j := 1; j <= p.Hours; j++ {
			if hours[i+j].Time.Sub(hours[i+j-1].Time) != time.Hour {
				continuous = false
				break
			}
		}
		if !continuous {
			result.Gaps++
			continue
		}
		c := evaluate(hours[i:i+p.Hours+1], daylight, zone, p)
		candidates = append(candidates, c)
		result.Evaluated++
		if len(c.Missing) > 0 {
			result.Incomplete++
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if len(a.Missing) != len(b.Missing) {
			return len(a.Missing) < len(b.Missing)
		}
		if a.penalty != b.penalty {
			return a.penalty < b.penalty
		}
		return a.Start.Before(b.Start)
	})
	for _, c := range candidates {
		overlaps := false
		for _, picked := range result.Windows {
			if c.Start.Before(picked.End) && picked.Start.Before(c.End) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			result.Windows = append(result.Windows, c.Window)
			if len(result.Windows) == MaxResults {
				break
			}
		}
	}
	return result, nil
}
func evaluate(hours []Hour, days map[string]Daylight, zone *time.Location, p Preferences) candidate {
	c := candidate{Window: Window{Start: hours[0].Time, End: hours[len(hours)-1].Time, Missing: []string{}, Exceeds: []string{}, Daylight: "not_requested"}}
	temperature, wind, probability, precipitation, gust := true, true, true, true, true
	var rain, amount, windRange, gustRange Range
	total := 0.0
	for i, h := range hours {
		temperature = c.TemperatureC.add(h.TemperatureC, -150, 100) && temperature
		wind = windRange.add(h.WindMS, 0, 1000) && wind
		if i == 0 {
			continue
		} // The starting row's preceding hour is outside this window.
		probability = rain.add(h.Probability, 0, 1) && probability
		precipitation = amount.add(h.PrecipitationMM, 0, 10000) && precipitation
		gust = gustRange.add(h.GustMS, 0, 1000) && gust
		if h.PrecipitationMM.in(0, 10000) {
			total += h.PrecipitationMM.Number
		}
	}
	c.PeakProbability = Value{rain.High, rain.Known}
	c.PeakHourlyPrecipitationMM = Value{amount.High, amount.Known}
	c.TotalPrecipitationMM = Value{total, precipitation}
	c.PeakWindMS = Value{windRange.High, windRange.Known}
	c.PeakGustMS = Value{gustRange.High, gustRange.Known}
	check := func(key string, complete bool, distance float64) {
		if !complete {
			c.Missing = append(c.Missing, key)
		}
		if distance > 0 {
			c.Exceeds = append(c.Exceeds, key)
			c.penalty += distance
		}
	}
	tempDistance := 0.0
	if c.TemperatureC.Known {
		tempDistance = (math.Max(0, p.MinTemperatureC-c.TemperatureC.Low) + math.Max(0, c.TemperatureC.High-p.MaxTemperatureC)) / 5
	}
	check("temperature", temperature, tempDistance)
	check("rain_chance", probability, excess(c.PeakProbability, p.MaxProbability, .2))
	check("rain_amount", precipitation, excess(c.PeakHourlyPrecipitationMM, p.MaxHourlyPrecipitationMM, .5))
	check("wind", wind, excess(c.PeakWindMS, p.MaxWindMS, 3))
	check("gusts", gust, excess(c.PeakGustMS, p.MaxGustMS, 5))
	if p.DaylightOnly {
		c.Daylight = daylightFor(c.Start, c.End, days, zone)
		if c.Daylight == "unknown" {
			c.Missing = append(c.Missing, "daylight")
		} else if c.Daylight == "no" {
			c.Exceeds = append(c.Exceeds, "daylight")
			c.penalty++
		}
	}
	c.Fits = len(c.Missing) == 0 && len(c.Exceeds) == 0
	return c
}
func excess(v Value, limit, scale float64) float64 {
	if !v.Known {
		return 0
	}
	return math.Max(0, v.Number-limit) / scale
}
func daylightFor(start, end time.Time, days map[string]Daylight, zone *time.Location) string {
	// Missing polar events are unknown; hourly is_day samples cannot prove that
	// an entire interval stays in daylight near a brief polar sunrise/sunset.
	first, last := start.In(zone), end.Add(-time.Nanosecond).In(zone)
	date := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, zone)
	through := last.Format("2006-01-02")
	covered := time.Duration(0)
	for n := 0; n < 3; n++ {
		key := date.Format("2006-01-02")
		d, ok := days[key]
		if !ok || d.Sunrise.IsZero() || !d.Sunset.After(d.Sunrise) || d.Sunset.Sub(d.Sunrise) > 24*time.Hour || d.Sunrise.In(zone).Format("2006-01-02") != key {
			return "unknown"
		}
		dayEnd := date.AddDate(0, 0, 1)
		if d.Sunset.After(dayEnd) {
			return "unknown"
		}
		from, to := d.Sunrise, d.Sunset
		if from.Before(start) {
			from = start
		}
		if to.After(end) {
			to = end
		}
		if to.After(from) {
			covered += to.Sub(from)
		}
		if key == through {
			if covered >= end.Sub(start) {
				return "yes"
			}
			return "no"
		}
		date = date.AddDate(0, 0, 1)
	}
	return "unknown"
}
