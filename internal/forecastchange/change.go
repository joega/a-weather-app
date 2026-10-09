// Package forecastchange compares narrow projections of forecasts retrieved
// from the same feed. Retrieval time is not a provider model issuance time.
package forecastchange

import (
	"errors"
	"math"
	"sort"
	"time"
)

const (
	MaxHours       = 72
	Horizon        = 48 * time.Hour
	MaxBaselineAge = 48 * time.Hour
	MaxCurrentAge  = 2 * time.Hour
	MaxChanges     = 3
)

// Value distinguishes an absent field from a known zero without pointer aliasing.
type Value struct {
	Number float64
	Known  bool
}

func Number(n float64) Value { return Value{n, true} }

type Hour struct {
	Time                                               time.Time
	TemperatureC, Probability, PrecipitationMM, GustMS Value
}
type Place struct {
	Latitude, Longitude float64
	Timezone            string
}
type Forecast struct {
	Place     Place
	Source    string
	Retrieved time.Time
	Hours     []Hour
}
type Change struct {
	Kind string
	// Temperature is sampled at Start..End; the other numeric changes describe
	// complete preceding-hour intervals from Start to End.
	Start, End    time.Time
	Before, After float64
	Samples       int
	// Used only by wet_hours; Start/End are the newer high-chance window.
	BeforeStart, BeforeEnd time.Time
	score                  float64
}
type Coverage struct {
	ExpectedPoints, ExpectedIntervals              int
	Temperature, Probability, Precipitation, Gusts int
}
type Result struct {
	Status                              string
	PreviousRetrieved, CurrentRetrieved time.Time
	Start, End                          time.Time
	Coverage                            Coverage
	Changes                             []Change
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func validValue(v Value, low, high float64) bool {
	return !v.Known && v.Number == 0 || v.Known && finite(v.Number) && v.Number >= low && v.Number <= high
}
func (f Forecast) Validate() error {
	if !finite(f.Place.Latitude) || f.Place.Latitude < -90 || f.Place.Latitude > 90 || !finite(f.Place.Longitude) || f.Place.Longitude < -180 || f.Place.Longitude > 180 || f.Place.Timezone == "" || len(f.Place.Timezone) > 100 || f.Source == "" || len(f.Source) > 80 || f.Retrieved.IsZero() || f.Retrieved.Year() < 1 || f.Retrieved.Year() > 9999 || len(f.Hours) > MaxHours {
		return errors.New("invalid forecast projection")
	}
	if _, err := time.LoadLocation(f.Place.Timezone); err != nil {
		return errors.New("invalid projection timezone")
	}
	var previous time.Time
	for _, h := range f.Hours {
		// Sparse hourly rows are allowed. A different cadence must not inherit
		// the provider contract that precipitation/gusts cover one preceding hour.
		if h.Time.IsZero() || h.Time.Before(f.Retrieved) || h.Time.After(f.Retrieved.Add(MaxHours*time.Hour)) || !previous.IsZero() && (!h.Time.After(previous) || h.Time.Sub(previous)%time.Hour != 0) || !validValue(h.TemperatureC, -150, 100) || !validValue(h.Probability, 0, 1) || !validValue(h.PrecipitationMM, 0, 10000) || !validValue(h.GustMS, 0, 1000) {
			return errors.New("invalid projected hour")
		}
		previous = h.Time
	}
	return nil
}

// Clone returns independent sample storage. Values themselves contain no pointers.
func (f Forecast) Clone() Forecast { f.Hours = append([]Hour(nil), f.Hours...); return f }

// Compare returns a bounded set of highlights over identical future valid times.
// It does not mutate forecasts, infer missing values, make provider requests, or
// write state. Timezone validation reads system zone data. Older data can be a
// useful baseline, but an expired current forecast cannot be a revision.
func Compare(previous *Forecast, current Forecast, now time.Time) (Result, error) {
	result := Result{Status: "no_previous", CurrentRetrieved: current.Retrieved, Start: now, End: now.Add(Horizon), Changes: []Change{}}
	if now.IsZero() || now.Year() < 1 || now.Add(Horizon).Year() > 9999 || current.Validate() != nil {
		return result, errors.New("invalid comparison input")
	}
	age := now.Sub(current.Retrieved)
	if age < 0 || age > MaxCurrentAge {
		result.Status = "current_unavailable"
		return result, nil
	}
	if previous == nil {
		return result, nil
	}
	if err := previous.Validate(); err != nil {
		return result, err
	}
	result.PreviousRetrieved = previous.Retrieved
	switch {
	case previous.Place != current.Place:
		result.Status = "place_changed"
	case previous.Source != current.Source:
		result.Status = "source_changed"
	case !current.Retrieved.After(previous.Retrieved):
		result.Status = "not_newer"
	case now.Sub(previous.Retrieved) > MaxBaselineAge:
		result.Status = "baseline_too_old"
	default:
		result.Status = "ready"
	}
	if result.Status != "ready" {
		return result, nil
	}
	if len(current.Hours) == 0 || len(previous.Hours) == 0 {
		result.Status = "no_overlap"
		return result, nil
	}
	// Expected coverage is the complete next-48-hour grid, even when either
	// input has gaps or ends early. The first interval may already be underway.
	first := current.Hours[0].Time
	for !first.After(now) {
		first = first.Add(time.Hour)
	}
	for first.Add(-time.Hour).After(now) {
		first = first.Add(-time.Hour)
	}
	result.Coverage.ExpectedPoints = int(result.End.Sub(first)/time.Hour) + 1
	result.Coverage.ExpectedIntervals = result.Coverage.ExpectedPoints
	if first.Add(-time.Hour).Before(now) {
		result.Coverage.ExpectedIntervals--
	}
	type pair struct{ old, next Hour }
	pairs := make([]pair, 0, MaxHours)
	for i, j := 0, 0; i < len(previous.Hours) && j < len(current.Hours); {
		old, next := previous.Hours[i], current.Hours[j]
		if old.Time.Before(next.Time) {
			i++
			continue
		}
		if next.Time.Before(old.Time) {
			j++
			continue
		}
		if next.Time.After(now) && !next.Time.After(result.End) {
			pairs = append(pairs, pair{old, next})
		}
		i++
		j++
	}
	if len(pairs) == 0 {
		result.Status = "no_overlap"
		return result, nil
	}
	candidates := make([]Change, 0, 5)
	for _, metric := range []struct {
		kind      string
		threshold float64
		point     bool
		values    func(Hour) Value
		count     *int
	}{
		{"temperature", 3, true, func(h Hour) Value { return h.TemperatureC }, &result.Coverage.Temperature},
		{"probability", .20, false, func(h Hour) Value { return h.Probability }, &result.Coverage.Probability},
		{"precipitation", 1, false, func(h Hour) Value { return h.PrecipitationMM }, &result.Coverage.Precipitation},
		{"gusts", 5, false, func(h Hour) Value { return h.GustMS }, &result.Coverage.Gusts},
	} {
		var run, best Change
		last := time.Time{}
		finish := func() {
			if run.Samples == 0 {
				return
			}
			run.Before /= float64(run.Samples)
			run.After /= float64(run.Samples)
			run.score = math.Abs(run.After-run.Before) / metric.threshold
			// Prefer magnitude, then sustained duration, then earliest valid time.
			if best.Samples == 0 || run.score > best.score+1e-9 || math.Abs(run.score-best.score) < 1e-9 && run.Samples > best.Samples {
				best = run
			}
			run = Change{}
		}
		for _, pair := range pairs {
			old, next := metric.values(pair.old), metric.values(pair.next)
			stamp := pair.next.Time
			eligible := metric.point || !stamp.Add(-time.Hour).Before(now)
			if !eligible || !old.Known || !next.Known {
				finish()
				last = time.Time{}
				continue
			}
			(*metric.count)++
			delta := next.Number - old.Number
			if math.Abs(delta)+1e-9 < metric.threshold {
				finish()
				last = time.Time{}
				continue
			}
			if run.Samples > 0 && (stamp.Sub(last) != time.Hour || (run.After-run.Before)*delta < 0) {
				finish()
			}
			if run.Samples == 0 {
				run = Change{Kind: metric.kind, Start: stamp}
				if !metric.point {
					run.Start = stamp.Add(-time.Hour)
				}
			}
			run.End = stamp
			run.Before += old.Number
			run.After += next.Number
			run.Samples++
			last = stamp
		}
		finish()
		if best.Samples > 0 {
			candidates = append(candidates, best)
		}
	}
	// Timing highlights require complete probability data for a whole local
	// calendar day. One interior wet block with dry hours on either side avoids
	// describing clipped windows or separate showers as a single shifted event.
	zone, _ := time.LoadLocation(current.Place.Timezone)
	local := now.In(zone)
	for day := 0; day < 3; day++ {
		start := time.Date(local.Year(), local.Month(), local.Day()+day, 0, 0, 0, 0, zone)
		end := start.AddDate(0, 0, 1)
		if start.Before(now) || end.After(result.End) {
			continue
		}
		oldRows, newRows := []Hour{}, []Hour{}
		for _, p := range pairs {
			if p.next.Time.After(start) && !p.next.Time.After(end) {
				oldRows = append(oldRows, p.old)
				newRows = append(newRows, p.next)
			}
		}
		oldStart, oldEnd, oldOK := wetWindow(oldRows, start, end)
		newStart, newEnd, newOK := wetWindow(newRows, start, end)
		shift, endShift := newStart.Sub(oldStart), newEnd.Sub(oldEnd)
		sameDirection := shift > 0 && endShift > 0 || shift < 0 && endShift < 0
		if oldOK && newOK && math.Abs(shift.Hours()) >= 2 && sameDirection && math.Abs(newEnd.Sub(newStart).Hours()-oldEnd.Sub(oldStart).Hours()) <= 1 {
			candidates = append(candidates, Change{Kind: "wet_hours", Start: newStart, End: newEnd, BeforeStart: oldStart, BeforeEnd: oldEnd, Samples: len(newRows), score: 2 + math.Min(4, math.Abs(shift.Hours())/2)})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].Start.Equal(candidates[j].Start) {
			return candidates[i].Start.Before(candidates[j].Start)
		}
		return candidates[i].Kind < candidates[j].Kind
	})
	for _, c := range candidates {
		duplicate := false
		for _, chosen := range result.Changes {
			if chosen.Kind == c.Kind {
				duplicate = true
			}
		}
		if !duplicate {
			result.Changes = append(result.Changes, c)
		}
		if len(result.Changes) == MaxChanges {
			break
		}
	}
	if result.Coverage.Temperature+result.Coverage.Probability+result.Coverage.Precipitation+result.Coverage.Gusts == 0 {
		result.Status = "insufficient_data"
	}
	return result, nil
}

func wetWindow(rows []Hour, start, end time.Time) (time.Time, time.Time, bool) {
	expected := int(end.Sub(start) / time.Hour)
	if len(rows) != expected {
		return time.Time{}, time.Time{}, false
	}
	first, last := -1, -1
	dryAfter := false
	for i, h := range rows {
		if !h.Time.Equal(start.Add(time.Duration(i+1)*time.Hour)) || !h.Probability.Known {
			return time.Time{}, time.Time{}, false
		}
		if h.Probability.Number >= .5 {
			if dryAfter {
				return time.Time{}, time.Time{}, false
			}
			if first < 0 {
				first = i
			}
			last = i
		} else if first >= 0 {
			dryAfter = true
		}
	}
	if first <= 0 || last < first+1 || last >= len(rows)-1 {
		return time.Time{}, time.Time{}, false
	}
	return rows[first].Time.Add(-time.Hour), rows[last].Time, true
}
