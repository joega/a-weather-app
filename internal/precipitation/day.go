package precipitation

import (
	"errors"
	"time"
)

// Accumulation distinguishes a complete local-day total from the subtotal of
// known, fully contained hours. Covered is time with valid values, not merely
// time spanned by the provider's first and last timestamps.
type Accumulation struct {
	Total, Subtotal Value
	Covered         time.Duration
}

// Range describes available instantaneous samples, not a continuously observed
// daily extreme. A missing sample is never replaced with zero.
type Range struct {
	Min, Max Value
	Samples  int
}

type Day struct {
	Date                           string
	Start, End                     time.Time
	TotalMM, RainMM, SnowCM        Accumulation
	WetHours                       Accumulation
	DepthM, FreezingM, Probability Range
	Kind                           string
	Hours                          []Hour
	BoundaryHours                  int
}

// RainAmount combines modeled rain and convective showers. Both must be known
// to claim a combined amount. Snowfall stays in cm; no snow density is assumed.
func (h Hour) RainAmount() Value {
	if !h.RainMM.Known || !h.ShowersMM.Known {
		return Value{}
	}
	return Number(h.RainMM.Number + h.ShowersMM.Number)
}

// Kind reports amounts indicated by the supplied fields, not an exclusive
// precipitation classification. "rain_and_snow" does not imply sleet, freezing
// rain, or road icing. At day scope rain and snow may occur at different times.
func (h Hour) Kind() string {
	return kind(positive(h.RainMM) || positive(h.ShowersMM), positive(h.SnowCM), h.TotalMM)
}

func positive(v Value) bool { return v.Known && v.Number > 0 }

func kind(rain, snow bool, total Value) string {
	switch {
	case rain && snow:
		return "rain_and_snow"
	case rain:
		return "rain"
	case snow:
		return "snow"
	case positive(total):
		return "precipitation"
	case total.Known:
		return "none"
	default:
		return "unknown"
	}
}

// Summarize computes one local day on demand. Hourly amounts/probabilities
// describe (End-1h, End]; depth and freezing height describe the instant End.
// Boundary-straddling amounts are excluded rather than apportioned under an
// unsupported assumption of uniform precipitation. Their missing coverage is
// explicit, including across fractional-hour clock changes.
func Summarize(data Data, date string) (Day, error) {
	if err := data.Validate(); err != nil {
		return Day{}, err
	}
	zone, _ := time.LoadLocation(data.Timezone) // Validated above.
	start, end, err := dayBounds(date, zone)
	if err != nil {
		return Day{}, err
	}
	d := Day{Date: date, Start: start, End: end, Hours: []Hour{}}
	rain, snow := false, false
	for _, h := range data.Hours {
		// Instantaneous samples at next midnight belong to the next day,
		// while the accumulation ending there belongs to this day.
		if !h.End.Before(start) && h.End.Before(end) {
			d.DepthM.add(h.DepthM)
			d.FreezingM.add(h.FreezingM)
		}
		begin := h.End.Add(-time.Hour)
		if !h.End.After(start) || !begin.Before(end) {
			continue
		}
		if begin.Before(start) || h.End.After(end) {
			d.BoundaryHours++
			continue
		}
		d.Hours = append(d.Hours, h)
		d.TotalMM.add(h.TotalMM)
		d.RainMM.add(h.RainAmount())
		d.SnowCM.add(h.SnowCM)
		d.Probability.add(h.Probability)
		if h.TotalMM.Known {
			wet := 0.0
			if h.TotalMM.Number >= 0.1 {
				wet = 1
			}
			d.WetHours.add(Number(wet))
		}
		rain = rain || positive(h.RainMM) || positive(h.ShowersMM)
		snow = snow || positive(h.SnowCM)
	}
	for _, amount := range []*Accumulation{&d.TotalMM, &d.RainMM, &d.SnowCM, &d.WetHours} {
		if amount.Covered == end.Sub(start) {
			amount.Total = amount.Subtotal
		}
	}
	// Positive partial totals indicate precipitation; a partial zero cannot
	// establish a dry day. The UI must label WetHours as modeled hours with
	// at least 0.1 mm, never as exact minutes of rain or snow.
	total := d.TotalMM.Total
	if positive(d.TotalMM.Subtotal) {
		total = d.TotalMM.Subtotal
	}
	d.Kind = kind(rain, snow, total)
	return d, nil
}

func (a *Accumulation) add(v Value) {
	if v.Known {
		a.Subtotal = Number(a.Subtotal.Number + v.Number)
		a.Covered += time.Hour
	}
}

func (r *Range) add(v Value) {
	if !v.Known {
		return
	}
	if !r.Min.Known || v.Number < r.Min.Number {
		r.Min = v
	}
	if !r.Max.Known || v.Number > r.Max.Number {
		r.Max = v
	}
	r.Samples++
}

// Search calendar boundaries around local noon: adding 24h and constructing
// midnight are both wrong for some real timezones. Forecast dates are bounded
// to modern civil time, with skipped dates rejected instead of normalized.
func dayBounds(date string, zone *time.Location) (time.Time, time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, date)
	if err != nil || len(date) != 10 || parsed.Year() < 2000 || parsed.Year() > 2100 {
		return time.Time{}, time.Time{}, errors.New("invalid precipitation date")
	}
	noon := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 12, 0, 0, 0, zone)
	if noon.Format(time.DateOnly) != date {
		return time.Time{}, time.Time{}, errors.New("missing local date")
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
	start, end := boundary(noon.Add(-36*time.Hour), noon, false), boundary(noon, noon.Add(36*time.Hour), true)
	if end.Sub(start) < 22*time.Hour || end.Sub(start) > 26*time.Hour {
		return time.Time{}, time.Time{}, errors.New("unsupported local day duration")
	}
	return start, end, nil
}
