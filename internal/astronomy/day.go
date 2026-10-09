// Package astronomy calculates one bounded local calendar day on demand.
// It performs no I/O, keeps no cache, and starts no timers or goroutines.
package astronomy

import (
	"errors"
	"math"
	"slices"
	"time"
)

const MinYear, MaxYear = 2000, 2100

type Event struct {
	Time time.Time
	Kind string
}
type Span struct{ Start, End time.Time }
type Body struct {
	// normal, always_up or always_down within this local calendar day.
	State  string
	Events []Event
	Above  []Span
}
type Day struct {
	Date                          string
	Start, End, PhaseAt           time.Time
	Sun, Moon                     Body
	Civil, Nautical, Astronomical []Event
	Golden                        []Span
	Phase, Illumination           float64
	Daylight, Change              time.Duration
}

// Calculate uses a level sea horizon and standard refraction. Times are
// astronomical estimates, not promises of visible sun/moon or clear skies.
// Phase and illumination refer to local noon on the selected calendar date.
func Calculate(date string, zone *time.Location, lat, lon float64) (Day, error) {
	if zone == nil || !finite(lat) || !finite(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return Day{}, errors.New("invalid astronomy location")
	}
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil || parsed.Year() < MinYear || parsed.Year() > MaxYear {
		return Day{}, errors.New("invalid astronomy date")
	}
	start, end, noon, err := bounds(date, zone)
	if err != nil {
		return Day{}, err
	}
	d := Day{Date: date, Start: start, End: end, PhaseAt: noon}
	sunHeight := func(t time.Time) float64 { return solarHeight(days(t), lat, lon) }
	d.Sun = body(start, end, sunHeight, -.833)
	d.Moon = body(start, end, func(t time.Time) float64 { return lunarHeight(days(t), lat, lon) }, 0)
	d.Civil = body(start, end, sunHeight, -6).Events
	d.Nautical = body(start, end, sunHeight, -12).Events
	d.Astronomical = body(start, end, sunHeight, -18).Events
	// Define the photographic window explicitly: geometric solar center
	// between -4 and +6 degrees, clipped to the local calendar day.
	d.Golden = subtract(body(start, end, sunHeight, -4).Above, body(start, end, sunHeight, 6).Above)
	d.Daylight = duration(d.Sun.Above)
	previous := start.Add(-time.Second).In(zone).Format(time.DateOnly)
	pStart, pEnd, _, pErr := bounds(previous, zone)
	if pErr != nil {
		return Day{}, pErr
	}
	d.Change = d.Daylight - duration(body(pStart, pEnd, sunHeight, -.833).Above)
	d.Phase, d.Illumination = illumination(days(noon))
	return d, nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func days(t time.Time) float64 {
	return float64(t.Unix())/86400 + float64(t.Nanosecond())/86400e9 - 10957.5
}

// Search actual civil boundaries rather than adding 24h or assuming midnight
// exists. This includes DST transitions at midnight and fractional-hour zones.
func bounds(date string, zone *time.Location) (time.Time, time.Time, time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil || len(date) != 10 || parsed.Year() < MinYear-1 || parsed.Year() > MaxYear {
		return time.Time{}, time.Time{}, time.Time{}, errors.New("invalid astronomy date")
	}
	noon := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 12, 0, 0, 0, zone)
	if noon.Format(time.DateOnly) != date {
		return time.Time{}, time.Time{}, time.Time{}, errors.New("missing local date")
	}
	boundary := func(lo, hi time.Time, after bool) time.Time {
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2).Truncate(time.Second)
			key := mid.In(zone).Format(time.DateOnly)
			if key < date || after && key == date {
				lo = mid
			} else {
				hi = mid
			}
		}
		return hi.UTC()
	}
	start := boundary(noon.Add(-36*time.Hour), noon, false)
	end := boundary(noon, noon.Add(36*time.Hour), true)
	if end.Sub(start) < 22*time.Hour || end.Sub(start) > 26*time.Hour {
		return time.Time{}, time.Time{}, time.Time{}, errors.New("unsupported local day duration")
	}
	return start, end, noon.UTC(), nil
}

// A ten-minute scan brackets the smooth daily altitude curve. Refining each
// turning point before finding roots preserves short grazing rise/set pairs
// even when both endpoints of a sample interval are on the same side.
func body(start, end time.Time, height func(time.Time) float64, threshold float64) Body {
	f := func(t time.Time) float64 { return height(t) - threshold }
	const step = 10 * time.Minute
	points := []time.Time{start, end}
	a, b := start.Add(-step), start
	fa, fb := f(a), f(b)
	for c := start.Add(step); !c.After(end.Add(step)); c = c.Add(step) {
		fc := f(c)
		if (fb > fa && fb > fc) || (fb < fa && fb < fc) {
			lo, hi := a, c
			maximum := fb > fa
			for range 24 {
				x, y := lo.Add(hi.Sub(lo)/3), hi.Add(-hi.Sub(lo)/3)
				if (f(x) < f(y)) == maximum {
					lo = x
				} else {
					hi = y
				}
			}
			extreme := lo.Add(hi.Sub(lo) / 2)
			if extreme.After(start) && extreme.Before(end) {
				points = append(points, extreme)
			}
		}
		if c.Before(end) {
			points = append(points, c)
		}
		a, b, fa, fb = b, c, fb, fc
	}
	slices.SortFunc(points, func(a, b time.Time) int { return a.Compare(b) })
	above := f(start) >= 0
	r := Body{State: "normal", Events: []Event{}, Above: []Span{}}
	opened := start
	for i := 1; i < len(points); i++ {
		lo, hi := points[i-1], points[i]
		left, right := f(lo) >= 0, f(hi) >= 0
		if left == right {
			continue
		}
		for hi.Sub(lo) > time.Second {
			mid := lo.Add(hi.Sub(lo) / 2)
			if (f(mid) >= 0) == left {
				lo = mid
			} else {
				hi = mid
			}
		}
		cross := lo.Add(hi.Sub(lo) / 2)
		kind := "rise"
		if left {
			kind = "set"
			r.Above = append(r.Above, Span{opened, cross})
		} else {
			opened = cross
		}
		r.Events = append(r.Events, Event{cross, kind})
		above = right
	}
	if above {
		r.Above = append(r.Above, Span{opened, end})
	}
	if len(r.Events) == 0 {
		r.State = "always_down"
		if above {
			r.State = "always_up"
		}
	}
	return r
}

func duration(spans []Span) time.Duration {
	var total time.Duration
	for _, s := range spans {
		total += s.End.Sub(s.Start)
	}
	return total
}

func subtract(outer, inner []Span) []Span {
	result := []Span{}
	for _, a := range outer {
		start := a.Start
		for _, b := range inner {
			if !b.End.After(start) || !b.Start.Before(a.End) {
				continue
			}
			if b.Start.After(start) {
				result = append(result, Span{start, b.Start})
			}
			if b.End.After(start) {
				start = b.End
			}
		}
		if start.Before(a.End) {
			result = append(result, Span{start, a.End})
		}
	}
	return result
}
