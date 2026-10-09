package outdoor

import (
	"math"
	"reflect"
	"slices"
	"testing"
	"time"
)

func fixture(start time.Time, count int) []Hour {
	rows := make([]Hour, count)
	for i := range rows {
		rows[i] = Hour{Time: start.Add(time.Duration(i) * time.Hour), TemperatureC: Number(20), WindMS: Number(2), Probability: Number(.1), PrecipitationMM: Number(0), GustMS: Number(4)}
	}
	return rows
}
func options() Preferences { p := DefaultPreferences(); p.DaylightOnly = false; return p }
func plan(t *testing.T, rows []Hour, days []Daylight, zone *time.Location, now time.Time, p Preferences) Result {
	t.Helper()
	r, e := Rank(rows, days, zone, now, p)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestUsesEndingHourAmountsAndBothPointSamples(t *testing.T) {
	start := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	rows := fixture(start, 3)
	rows[0].Probability, rows[0].PrecipitationMM, rows[0].GustMS = Number(1), Number(30), Number(40)
	rows[1].PrecipitationMM = Number(.1)
	rows[2].TemperatureC = Number(40)
	before := slices.Clone(rows)
	r := plan(t, rows, nil, time.UTC, start, options())
	if len(r.Windows) != 2 || !r.Windows[0].Fits || !r.Windows[0].Start.Equal(start) || r.Windows[0].TotalPrecipitationMM.Number != .1 || r.Windows[0].PeakGustMS.Number != 4 {
		t.Fatal(r)
	}
	if r.Windows[1].Fits || !slices.Contains(r.Windows[1].Exceeds, "temperature") {
		t.Fatal("endpoint temperature was ignored", r)
	}
	if !reflect.DeepEqual(rows, before) {
		t.Fatal("mutated forecast")
	}
	rows[0].TemperatureC = Number(0)
	r = plan(t, rows[:2], nil, time.UTC, start, options())
	if r.Windows[0].Fits {
		t.Fatal("start temperature ignored")
	}
}
func TestPreferenceRankingReturnsDistinctAlternatives(t *testing.T) {
	now := time.Date(2026, 10, 9, 9, 30, 0, 0, time.UTC)
	rows := fixture(now.Truncate(time.Hour), 10)
	rows[2].Probability = Number(.8) // 10-11, not 11-12.
	rows[3].GustMS = Number(20)
	p := options()
	p.Hours = 2
	r := plan(t, rows, nil, time.UTC, now, p)
	if len(r.Windows) != 3 || r.Evaluated != 7 || !r.Windows[0].Start.Equal(rows[3].Time) {
		t.Fatal(r)
	}
	for i, w := range r.Windows {
		if w.Start.Before(now) || w.End.Sub(w.Start) != 2*time.Hour {
			t.Fatal(w)
		}
		for j := 0; j < i; j++ {
			other := r.Windows[j]
			if w.Start.Before(other.End) && other.Start.Before(w.End) {
				t.Fatal("overlapping alternatives")
			}
		}
	}
	// Narrowing temperature, rain and wind limits must explain every mismatch.
	p.MinTemperatureC = 21
	p.MaxProbability = 0
	p.MaxWindMS = 1
	p.MaxGustMS = 2
	p.MaxHourlyPrecipitationMM = 0
	rows[1].PrecipitationMM = Number(.3)
	r = plan(t, rows[:3], nil, time.UTC, rows[0].Time, p)
	w := r.Windows[0]
	for _, key := range []string{"temperature", "rain_chance", "rain_amount", "wind", "gusts"} {
		if !slices.Contains(w.Exceeds, key) {
			t.Fatal("missing reason", key, w)
		}
	}
	if w.Fits {
		t.Fatal("unsuitable window called a match")
	}
}
func TestMissingIsNotDryCalmOrComfortable(t *testing.T) {
	start := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	for _, key := range []string{"temperature", "rain_chance", "rain_amount", "wind", "gusts"} {
		t.Run(key, func(t *testing.T) {
			rows := fixture(start, 4)
			switch key {
			case "temperature":
				rows[0].TemperatureC = Value{}
			case "wind":
				rows[0].WindMS = Value{}
			case "rain_chance":
				rows[1].Probability = Value{}
			case "rain_amount":
				rows[1].PrecipitationMM = Value{}
			case "gusts":
				rows[1].GustMS = Value{}
			}
			r := plan(t, rows, nil, time.UTC, start, options())
			if r.Incomplete != 1 || !r.Windows[0].Fits || !r.Windows[0].Start.Equal(rows[1].Time) {
				t.Fatal("missing data outranked a known match", r)
			}
			w := r.Windows[len(r.Windows)-1]
			if w.Fits || !slices.Contains(w.Missing, key) {
				t.Fatal("missing value called a match", w)
			}
			if key == "rain_amount" && w.TotalPrecipitationMM.Known {
				t.Fatal("partial amount described as a total")
			}
		})
	}
	rows := fixture(start, 2)
	rows[1].Probability = Number(0)
	rows[1].GustMS = Number(0)
	if !plan(t, rows, nil, time.UTC, start, options()).Windows[0].Fits {
		t.Fatal("known zero rejected")
	}
	for _, invalid := range []float64{-1, math.NaN(), math.Inf(1), 1.1} {
		rows[1].Probability = Value{invalid, true}
		w := plan(t, rows, nil, time.UTC, start, options()).Windows[0]
		if w.Fits || !slices.Contains(w.Missing, "rain_chance") {
			t.Fatal("invalid value reassured", invalid, w)
		}
	}
}
func TestGapHorizonAndInsufficientRows(t *testing.T) {
	start := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	rows := fixture(start, 240)
	r := plan(t, rows, nil, time.UTC, start, options())
	if r.Evaluated != 48 {
		t.Fatal("horizon not bounded", r.Evaluated)
	}
	rows = append(rows[:1], rows[2:4]...)
	r = plan(t, rows, nil, time.UTC, start, options())
	if r.Gaps != 1 || r.Evaluated != 1 || !r.Windows[0].Start.Equal(start.Add(2*time.Hour)) {
		t.Fatal("gap interpolated", r)
	}
	for _, rows := range [][]Hour{nil, fixture(start, 1), fixture(start.Add(-24*time.Hour), 4)} {
		if len(plan(t, rows, nil, time.UTC, start, options()).Windows) != 0 {
			t.Fatal("invented a future interval")
		}
	}
}
func TestDaylightUnknownPolarAndCrossMidnight(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 9, 6, 0, 0, 0, zone)
	rows := fixture(start, 18)
	days := []Daylight{{Date: "2026-10-09", Sunrise: start.Add(time.Hour), Sunset: start.Add(12 * time.Hour)}}
	p := DefaultPreferences()
	p.Hours = 2
	r := plan(t, rows, days, zone, start, p)
	if !r.Windows[0].Fits || r.Windows[0].Daylight != "yes" || !r.Windows[0].Start.Equal(start.Add(time.Hour)) {
		t.Fatal(r)
	}
	r = plan(t, rows[:3], days, zone, start, p)
	if r.Windows[0].Fits || r.Windows[0].Daylight != "no" || !slices.Contains(r.Windows[0].Exceeds, "daylight") {
		t.Fatal("sunrise crossing called daylight", r)
	}
	days[0].Sunrise = time.Time{}
	days[0].Sunset = time.Time{}
	r = plan(t, rows, days, zone, start, p)
	if r.Incomplete != r.Evaluated || r.Windows[0].Daylight != "unknown" || r.Windows[0].Fits {
		t.Fatal("missing polar events reassured", r)
	}
	p.DaylightOnly = false
	if !plan(t, rows, nil, zone, start, p).Windows[0].Fits {
		t.Fatal("unrequested daylight prevents a match")
	}
	p.DaylightOnly = true
	late := time.Date(2026, 10, 9, 23, 0, 0, 0, zone)
	rows = fixture(late, 3)
	days = []Daylight{{"2026-10-09", late.Add(-16 * time.Hour), late.Add(-5 * time.Hour)}, {"2026-10-10", late.Add(8 * time.Hour), late.Add(19 * time.Hour)}}
	if plan(t, rows, days, zone, late, p).Windows[0].Daylight != "no" {
		t.Fatal("known night not recognized")
	}
	days = days[:1]
	if plan(t, rows, days, zone, late, p).Windows[0].Daylight != "unknown" {
		t.Fatal("next-day gap not exposed")
	}
	// Overlapping malformed daily intervals cannot add up to apparent daylight.
	days = []Daylight{{"2026-10-09", late.Add(-16 * time.Hour), late.Add(3 * time.Hour)}, {"2026-10-10", late.Add(time.Hour), late.Add(6 * time.Hour)}}
	if plan(t, rows, days, zone, late, p).Windows[0].Daylight != "unknown" {
		t.Fatal("invalid solar interval accepted")
	}
}
func TestDSTAndQuarterHourZoneUseElapsedHours(t *testing.T) {
	for _, test := range []struct{ zone, stamp string }{{"America/New_York", "2026-11-01T05:00:00Z"}, {"America/New_York", "2026-03-08T06:00:00Z"}, {"Asia/Kathmandu", "2026-10-09T05:00:00Z"}} {
		zone, err := time.LoadLocation(test.zone)
		if err != nil {
			t.Fatal(err)
		}
		start, _ := time.Parse(time.RFC3339, test.stamp)
		r := plan(t, fixture(start, 5), nil, zone, start, options())
		if len(r.Windows) != 3 || r.Windows[0].End.Sub(r.Windows[0].Start) != time.Hour || !r.Windows[0].Fits {
			t.Fatal(r)
		}
	}
}
func TestInvalidInputAndPreferences(t *testing.T) {
	now := time.Now()
	rows := fixture(now, 3)
	p := options()
	for _, change := range []func(*Preferences){func(p *Preferences) { p.Hours = 0 }, func(p *Preferences) { p.Hours = 5 }, func(p *Preferences) { p.MaxProbability = math.NaN() }, func(p *Preferences) { p.MinTemperatureC = 30; p.MaxTemperatureC = 10 }, func(p *Preferences) { p.MaxGustMS = 1 }} {
		bad := p
		change(&bad)
		if _, err := Rank(rows, nil, time.UTC, now, bad); err == nil {
			t.Fatal("bad preferences accepted", bad)
		}
	}
	for _, bad := range [][]Hour{fixture(now, 241), {rows[0], rows[0]}, {rows[1], rows[0]}, {{}}} {
		if _, err := Rank(bad, nil, time.UTC, now, p); err == nil {
			t.Fatal("bad row order/count accepted")
		}
	}
	for _, days := range [][]Daylight{make([]Daylight, 11), {{Date: "bad"}}, {{Date: "2026-10-09"}, {Date: "2026-10-09"}}} {
		if _, err := Rank(rows, days, time.UTC, now, p); err == nil {
			t.Fatal("bad daylight accepted")
		}
	}
	if _, err := Rank(rows, nil, nil, now, p); err == nil {
		t.Fatal("nil timezone accepted")
	}
	if _, err := Rank(rows, nil, time.UTC, time.Time{}, p); err == nil {
		t.Fatal("missing current time accepted")
	}
}
func BenchmarkRankFullForecast(b *testing.B) {
	now := time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)
	hours := fixture(now, 240)
	days := make([]Daylight, 10)
	for i := range days {
		sunrise := now.AddDate(0, 0, i).Add(-4 * time.Hour)
		days[i] = Daylight{Date: sunrise.Format("2006-01-02"), Sunrise: sunrise, Sunset: sunrise.Add(12 * time.Hour)}
	}
	for _, daylight := range []bool{false, true} {
		name := "any_time"
		if daylight {
			name = "daylight"
		}
		b.Run(name, func(b *testing.B) {
			p := DefaultPreferences()
			p.Hours, p.DaylightOnly = 4, daylight
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := Rank(hours, days, time.UTC, now, p); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
