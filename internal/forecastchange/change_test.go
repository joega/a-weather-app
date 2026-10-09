package forecastchange

import (
	"math"
	"reflect"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 9, 10, 0, 0, 0, time.UTC)

func forecasts(now time.Time, count int) (Forecast, Forecast) {
	current := Forecast{Place: Place{42, -71, "UTC"}, Source: "test-feed", Retrieved: now.Add(-15 * time.Minute)}
	for i := 0; i < count; i++ {
		current.Hours = append(current.Hours, Hour{Time: now.Add(time.Duration(i) * time.Hour), TemperatureC: Number(10), Probability: Number(.2), PrecipitationMM: Number(0), GustMS: Number(5)})
	}
	previous := current.Clone()
	previous.Retrieved = now.Add(-time.Hour)
	return previous, current
}

func compare(t *testing.T, old *Forecast, current Forecast, now time.Time) Result {
	t.Helper()
	r, err := Compare(old, current, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func change(t *testing.T, result Result, kind string) Change {
	t.Helper()
	for _, c := range result.Changes {
		if c.Kind == kind {
			return c
		}
	}
	t.Fatalf("missing %s in %+v", kind, result)
	return Change{}
}

func TestComparisonGuards(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		edit         func(*Forecast, *Forecast)
	}{
		{"unchanged", "ready", func(_, _ *Forecast) {}},
		{"coordinates", "place_changed", func(p, _ *Forecast) { p.Place.Latitude++ }},
		{"timezone", "place_changed", func(p, _ *Forecast) { p.Place.Timezone = "America/New_York" }},
		{"source", "source_changed", func(p, _ *Forecast) { p.Source = "other" }},
		{"same retrieval", "not_newer", func(p, c *Forecast) { p.Retrieved = c.Retrieved }},
		{"older current", "not_newer", func(p, c *Forecast) { p.Retrieved = c.Retrieved.Add(time.Minute) }},
		{"old baseline", "baseline_too_old", func(p, _ *Forecast) { p.Retrieved = testNow.Add(-MaxBaselineAge - time.Second) }},
		{"expired current", "current_unavailable", func(_, c *Forecast) { c.Retrieved = testNow.Add(-MaxCurrentAge - time.Second) }},
		{"future current", "current_unavailable", func(_, c *Forecast) { c.Retrieved = testNow.Add(time.Minute); c.Hours = nil }},
		{"empty", "no_overlap", func(p, _ *Forecast) { p.Hours = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old, current := forecasts(testNow, 2)
			tc.edit(&old, &current)
			r := compare(t, &old, current, testNow)
			if r.Status != tc.status || len(r.Changes) != 0 {
				t.Fatalf("got %+v, want %s without highlights", r, tc.status)
			}
		})
	}
	_, current := forecasts(testNow, 2)
	if got := compare(t, nil, current, testNow).Status; got != "no_previous" {
		t.Fatal(got)
	}
}

func TestCoverageAndIntervalAlignment(t *testing.T) {
	old, current := forecasts(testNow, 49)
	for i := range current.Hours {
		current.Hours[i].TemperatureC = Number(13)
		current.Hours[i].Probability = Number(.4)
	}
	for _, offset := range []time.Duration{0, 30 * time.Minute} {
		r := compare(t, &old, current, testNow.Add(offset))
		intervals := 48
		if offset != 0 {
			intervals = 47
		}
		want := Coverage{48, intervals, 48, intervals, intervals, intervals}
		if r.Coverage != want {
			t.Fatalf("offset %s: %+v, want %+v", offset, r.Coverage, want)
		}
		temperature, probability := change(t, r, "temperature"), change(t, r, "probability")
		if temperature.Samples != 48 || !temperature.Start.Equal(testNow.Add(time.Hour)) || probability.Samples != intervals || !probability.Start.Equal(testNow.Add(time.Duration(48-intervals)*time.Hour)) {
			t.Fatalf("incorrect valid period: %+v / %+v", temperature, probability)
		}
		if math.Abs(probability.After-probability.Before-.2) > 1e-9 {
			t.Fatal("probability change must be 20 percentage points")
		}
	}
}

func TestExactTimesMissingValuesAndKnownZero(t *testing.T) {
	old, current := forecasts(testNow, 5)
	// The current feed omits the first future sample; array indexes now differ.
	current.Hours = append(current.Hours[:1], current.Hours[2:]...)
	current.Hours[1].PrecipitationMM = Number(2)
	old.Hours[3].PrecipitationMM = Value{}
	current.Hours[2].PrecipitationMM = Number(20)
	r := compare(t, &old, current, testNow)
	c := change(t, r, "precipitation")
	if c.Before != 0 || c.After != 2 || c.Samples != 1 || !c.Start.Equal(testNow.Add(time.Hour)) || !c.End.Equal(testNow.Add(2*time.Hour)) || r.Coverage.Precipitation != 2 || r.Coverage.Temperature != 3 {
		t.Fatalf("unknown or unmatched data became a change: %+v", r)
	}
	for i := range current.Hours {
		current.Hours[i].Time = current.Hours[i].Time.Add(30 * time.Minute)
	}
	if got := compare(t, &old, current, testNow).Status; got != "no_overlap" {
		t.Fatal(got)
	}
	old, current = forecasts(testNow, 2)
	old.Hours[1] = Hour{Time: old.Hours[1].Time}
	if got := compare(t, &old, current, testNow).Status; got != "insufficient_data" {
		t.Fatal(got)
	}
}

func TestPartialHoursAndGapsDoNotBecomeSustainedChanges(t *testing.T) {
	old, current := forecasts(testNow, 6)
	current.Hours[1].Probability = Number(.9)
	current.Hours[1].PrecipitationMM = Number(20)
	current.Hours[1].GustMS = Number(40)
	if r := compare(t, &old, current, testNow.Add(30*time.Minute)); len(r.Changes) != 0 {
		t.Fatalf("partly elapsed interval produced a revision: %+v", r.Changes)
	}
	old, current = forecasts(testNow, 6)
	current.Hours[1].TemperatureC = Number(14)
	current.Hours[3].TemperatureC = Number(14)
	current.Hours[4].TemperatureC = Number(14)
	// Remove the gap from both feeds: matched samples still cannot bridge it.
	old.Hours = append(old.Hours[:2], old.Hours[3:]...)
	current.Hours = append(current.Hours[:2], current.Hours[3:]...)
	c := change(t, compare(t, &old, current, testNow), "temperature")
	if c.Samples != 2 || !c.Start.Equal(testNow.Add(3*time.Hour)) {
		t.Fatalf("gap joined separate runs: %+v", c)
	}
}

func TestContiguousRunsAndDeterministicCap(t *testing.T) {
	old, current := forecasts(testNow, 10)
	for i, delta := range []float64{0, 3, 3, 0, 4, 4, -4, -4, -4, 0} {
		current.Hours[i].TemperatureC = Number(10 + delta)
	}
	r := compare(t, &old, current, testNow)
	c := change(t, r, "temperature")
	if c.Before != 10 || c.After != 6 || c.Samples != 3 || !c.Start.Equal(testNow.Add(6*time.Hour)) || !c.End.Equal(testNow.Add(8*time.Hour)) {
		t.Fatalf("sign or quiet hour did not split runs: %+v", c)
	}
	// All four fields change; ranking stays bounded and leaves inputs untouched.
	for i := range current.Hours {
		current.Hours[i].PrecipitationMM = Number(10)
		current.Hours[i].Probability = Number(.8)
		current.Hours[i].GustMS = Number(30)
	}
	beforeOld, beforeCurrent := old.Clone(), current.Clone()
	want := compare(t, &old, current, testNow)
	if len(want.Changes) != MaxChanges || want.Changes[0].Kind != "precipitation" || want.Changes[1].Kind != "gusts" || want.Changes[2].Kind != "probability" {
		t.Fatalf("unexpected ranking: %+v", want.Changes)
	}
	for i := 0; i < 10; i++ {
		if got := compare(t, &old, current, testNow); !reflect.DeepEqual(got, want) {
			t.Fatal("comparison is not deterministic")
		}
	}
	if !reflect.DeepEqual(beforeOld, old) || !reflect.DeepEqual(beforeCurrent, current) {
		t.Fatal("comparison mutated source data")
	}
}

func TestWetWindowShiftAcrossLocalDays(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, date := range []struct {
		month      time.Month
		day, hours int
	}{{3, 8, 23}, {11, 1, 25}, {10, 10, 24}} {
		start := time.Date(2026, date.month, date.day, 0, 0, 0, 0, zone)
		now := start.Add(-12 * time.Hour)
		old, current := forecasts(now, 49)
		old.Place.Timezone, current.Place.Timezone = zone.String(), zone.String()
		for i, h := range old.Hours {
			if h.Time.After(start.Add(8*time.Hour)) && !h.Time.After(start.Add(11*time.Hour)) {
				old.Hours[i].Probability = Number(.8)
			}
			if h.Time.After(start.Add(10*time.Hour)) && !h.Time.After(start.Add(13*time.Hour)) {
				current.Hours[i].Probability = Number(.8)
			}
		}
		c := change(t, compare(t, &old, current, now), "wet_hours")
		if c.Samples != date.hours || !c.BeforeStart.Equal(start.Add(8*time.Hour)) || !c.BeforeEnd.Equal(start.Add(11*time.Hour)) || !c.Start.Equal(start.Add(10*time.Hour)) || !c.End.Equal(start.Add(13*time.Hour)) {
			t.Fatalf("wrong local-day shift for %s: %+v", start, c)
		}
		for i := range current.Hours {
			if current.Hours[i].Time.Equal(start.Add(time.Hour)) {
				current.Hours[i].Probability = Value{}
			}
		}
		for _, c := range compare(t, &old, current, now).Changes {
			if c.Kind == "wet_hours" {
				t.Fatal("incomplete probability day produced a timing claim")
			}
		}
	}
}

func TestWetWindowRejectsAmbiguousEvents(t *testing.T) {
	for _, tc := range []struct {
		name string
		wet  []int
	}{
		{"two events", []int{3, 4, 8, 9}},
		{"clipped start", []int{0, 1, 2}},
		{"clipped end", []int{21, 22, 23}},
		{"single hour", []int{8}},
		{"dry", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := make([]Hour, 24)
			for i := range rows {
				rows[i] = Hour{Time: testNow.Add(time.Duration(i+1) * time.Hour), Probability: Number(0)}
			}
			for _, i := range tc.wet {
				rows[i].Probability = Number(.5)
			}
			if _, _, ok := wetWindow(rows, testNow, testNow.Add(24*time.Hour)); ok {
				t.Fatal("ambiguous event accepted")
			}
		})
	}
}

func TestProjectionValidationAndClone(t *testing.T) {
	_, valid := forecasts(testNow, 3)
	for _, tc := range []struct {
		name string
		edit func(*Forecast)
	}{
		{"nan", func(f *Forecast) { f.Hours[0].TemperatureC = Number(math.NaN()) }},
		{"infinity", func(f *Forecast) { f.Hours[0].GustMS = Number(math.Inf(1)) }},
		{"unknown payload", func(f *Forecast) { f.Hours[0].Probability = Value{Number: .5} }},
		{"probability bound", func(f *Forecast) { f.Hours[0].Probability = Number(1.01) }},
		{"negative rain", func(f *Forecast) { f.Hours[0].PrecipitationMM = Number(-1) }},
		{"duplicate", func(f *Forecast) { f.Hours[1].Time = f.Hours[0].Time }},
		{"out of order", func(f *Forecast) { f.Hours[1], f.Hours[2] = f.Hours[2], f.Hours[1] }},
		{"non-hourly", func(f *Forecast) { f.Hours[1].Time = f.Hours[1].Time.Add(time.Minute) }},
		{"past row", func(f *Forecast) { f.Hours[0].Time = f.Retrieved.Add(-time.Second) }},
		{"distant row", func(f *Forecast) { f.Hours[2].Time = f.Retrieved.Add((MaxHours + 1) * time.Hour) }},
		{"too many", func(f *Forecast) { f.Hours = make([]Hour, MaxHours+1) }},
		{"location", func(f *Forecast) { f.Place.Latitude = math.NaN() }},
		{"timezone", func(f *Forecast) { f.Place.Timezone = "invalid/zone" }},
		{"source", func(f *Forecast) { f.Source = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := valid.Clone()
			tc.edit(&f)
			if f.Validate() == nil {
				t.Fatal("invalid projection accepted")
			}
			if _, err := Compare(nil, f, testNow); err == nil {
				t.Fatal("invalid current accepted")
			}
			if _, err := Compare(&f, valid, testNow); err == nil {
				t.Fatal("invalid baseline accepted")
			}
		})
	}
	f := valid.Clone()
	f.Hours[0].TemperatureC = Number(-150)
	f.Hours[1].TemperatureC = Number(100)
	f.Hours[0].GustMS = Number(1000)
	f.Hours[0].PrecipitationMM = Number(10000)
	if err := f.Validate(); err != nil {
		t.Fatal("normalized weather bounds rejected:", err)
	}
	if valid.Hours[0].TemperatureC.Number != 10 {
		t.Fatal("clone aliases source")
	}
	if _, err := Compare(nil, valid, time.Time{}); err == nil {
		t.Fatal("zero clock accepted")
	}
}

func BenchmarkCompare(b *testing.B) {
	old, current := forecasts(testNow, MaxHours)
	old.Place.Timezone, current.Place.Timezone = "America/New_York", "America/New_York"
	for i := range current.Hours {
		current.Hours[i].TemperatureC = Number(14)
		current.Hours[i].GustMS = Number(15)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compare(&old, current, testNow); err != nil {
			b.Fatal(err)
		}
	}
}
