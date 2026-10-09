package precipitation

import (
	"reflect"
	"testing"
	"time"
)

func instant(raw string) time.Time {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		panic(err)
	}
	return t
}

func dayData(zone string, start time.Time) Data {
	d := Data{Timezone: zone, FetchedAt: start.Add(12 * time.Hour)}
	for i := -1; i <= 27; i++ {
		d.Hours = append(d.Hours, Hour{
			End: start.Add(time.Duration(i) * time.Hour), TotalMM: Number(1),
			RainMM: Number(.4), ShowersMM: Number(.1), SnowCM: Number(.2),
			DepthM: Number(1), FreezingM: Number(1500), Probability: Number(.25),
		})
	}
	return d
}

func TestActualLocalCalendarTotals(t *testing.T) {
	for _, tc := range []struct {
		zone, date, start, end string
		wholeHours             int
		boundaryHours          int
	}{
		{"UTC", "2026-10-09", "2026-10-09T00:00:00Z", "2026-10-10T00:00:00Z", 24, 0},
		{"Asia/Kathmandu", "2026-10-09", "2026-10-08T18:15:00Z", "2026-10-09T18:15:00Z", 24, 0},
		{"America/New_York", "2026-03-08", "2026-03-08T05:00:00Z", "2026-03-09T04:00:00Z", 23, 0},
		{"America/New_York", "2026-11-01", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", 25, 0},
		{"America/Santiago", "2026-09-06", "2026-09-06T04:00:00Z", "2026-09-07T03:00:00Z", 23, 0},
		{"America/Havana", "2026-11-01", "2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z", 25, 0},
		{"Australia/Lord_Howe", "2026-10-04", "2026-10-03T13:30:00Z", "2026-10-04T13:00:00Z", 23, 1},
		{"Australia/Lord_Howe", "2026-04-05", "2026-04-04T13:00:00Z", "2026-04-05T13:30:00Z", 24, 1},
	} {
		t.Run(tc.zone+tc.date, func(t *testing.T) {
			start, end := instant(tc.start), instant(tc.end)
			data := dayData(tc.zone, start)
			before := append([]Hour(nil), data.Hours...)
			d, err := Summarize(data, tc.date)
			if err != nil {
				t.Fatal(err)
			}
			if d.Start != start || d.End != end || d.Date != tc.date || len(d.Hours) != tc.wholeHours || d.BoundaryHours != tc.boundaryHours {
				t.Fatalf("wrong local bounds or included intervals: %+v", d)
			}
			if d.TotalMM.Subtotal != Number(float64(tc.wholeHours)) || d.TotalMM.Covered != time.Duration(tc.wholeHours)*time.Hour || d.RainMM.Subtotal != Number(float64(tc.wholeHours)/2) || d.WetHours.Subtotal != Number(float64(tc.wholeHours)) {
				t.Fatalf("wrong daily accumulation: %+v", d)
			}
			complete := tc.boundaryHours == 0
			if d.TotalMM.Total.Known != complete || d.RainMM.Total.Known != complete || d.SnowCM.Total.Known != complete || d.WetHours.Total.Known != complete {
				t.Fatal("partial boundary presented as complete day")
			}
			if !reflect.DeepEqual(before, data.Hours) {
				t.Fatal("summary mutated data")
			}
			d.Hours[0].TotalMM = Number(900)
			if !reflect.DeepEqual(before, data.Hours) {
				t.Fatal("summary aliases source hours")
			}
		})
	}
}

func TestMissingAmountsAndHourGaps(t *testing.T) {
	start := instant("2026-10-09T00:00:00Z")
	data := dayData("UTC", start)
	// Hour ending 05:00 is absent entirely; ending 07:00 has no total or
	// snowfall; 09:00 has rain but lacks the convective-showers component.
	data.Hours = append(data.Hours[:6:6], data.Hours[7:]...)
	for i := range data.Hours {
		h := &data.Hours[i]
		switch h.End.Sub(start) {
		case 7 * time.Hour:
			h.TotalMM, h.SnowCM = Value{}, Value{}
		case 9 * time.Hour:
			h.ShowersMM = Value{}
		}
	}
	d, err := Summarize(data, "2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hours) != 23 || d.TotalMM.Covered != 22*time.Hour || d.RainMM.Covered != 22*time.Hour || d.SnowCM.Covered != 22*time.Hour || d.WetHours.Covered != 22*time.Hour {
		t.Fatalf("gaps counted as coverage: %+v", d)
	}
	if d.TotalMM.Total.Known || d.TotalMM.Subtotal != Number(22) || d.RainMM.Total.Known || d.RainMM.Subtotal != Number(11) || d.WetHours.Total.Known || d.WetHours.Subtotal != Number(22) {
		t.Fatal("partial amounts became a full total")
	}
	if d.Kind != "rain_and_snow" {
		t.Fatal("positive known components lost when other fields are missing")
	}
}

func TestNoUniformRainAssumptionAtFractionalBoundaries(t *testing.T) {
	start := instant("2026-10-09T00:00:00Z")
	data := dayData("UTC", start.Add(30*time.Minute))
	for i := range data.Hours {
		if data.Hours[i].End.Equal(start.Add(30*time.Minute)) || data.Hours[i].End.Equal(start.Add(24*time.Hour+30*time.Minute)) {
			data.Hours[i].TotalMM = Number(100)
		}
	}
	d, err := Summarize(data, "2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if d.BoundaryHours != 2 || d.TotalMM.Covered != 23*time.Hour || d.TotalMM.Subtotal != Number(23) || d.TotalMM.Total.Known {
		t.Fatalf("straddling amounts were divided or assigned to wrong day: %+v", d.TotalMM)
	}
}

func TestAmountsAndInstantSamplesHaveDifferentMidnightOwnership(t *testing.T) {
	start := instant("2026-10-09T00:00:00Z")
	data := dayData("UTC", start)
	for i := range data.Hours {
		h := &data.Hours[i]
		h.TotalMM, h.RainMM, h.ShowersMM, h.SnowCM = Number(0), Number(0), Number(0), Number(0)
		if h.End.Equal(start) {
			h.TotalMM, h.DepthM, h.FreezingM, h.Probability = Number(99), Number(0), Number(0), Number(1)
		}
		if h.End.Equal(start.Add(24 * time.Hour)) {
			h.TotalMM, h.SnowCM, h.DepthM, h.FreezingM, h.Probability = Number(2), Number(7), Number(90), Number(9000), Number(.75)
		}
	}
	d, err := Summarize(data, "2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalMM.Total != Number(2) || d.SnowCM.Total != Number(7) || d.RainMM.Total != Number(0) || d.WetHours.Total != Number(1) || d.Kind != "snow" {
		t.Fatal("prior-hour amount associated with wrong day or wrong units")
	}
	if d.DepthM != (Range{Min: Number(0), Max: Number(1), Samples: 24}) || d.FreezingM != (Range{Min: Number(0), Max: Number(1500), Samples: 24}) || d.Probability != (Range{Min: Number(.25), Max: Number(.75), Samples: 24}) {
		t.Fatalf("instant ranges or interval probabilities include wrong midnight: depth=%+v freezing=%+v chance=%+v", d.DepthM, d.FreezingM, d.Probability)
	}
}

func TestDryUnknownAndModeledWetHours(t *testing.T) {
	data := dayData("UTC", instant("2026-10-09T00:00:00Z"))
	for i := range data.Hours {
		data.Hours[i] = Hour{End: data.Hours[i].End, TotalMM: Number(0)}
	}
	d, err := Summarize(data, "2026-10-09")
	if err != nil {
		t.Fatal(err)
	}
	if d.TotalMM.Total != Number(0) || d.Kind != "none" || d.WetHours.Total != Number(0) || d.SnowCM.Total.Known || d.DepthM.Min.Known || d.FreezingM.Max.Known || d.Probability.Max.Known {
		t.Fatal("known dry total and missing optional fields confused")
	}
	data.Hours[2].TotalMM = Value{}
	d, _ = Summarize(data, "2026-10-09")
	if d.Kind != "unknown" || d.TotalMM.Total.Known || d.TotalMM.Subtotal != Number(0) {
		t.Fatal("partial dry coverage claimed no precipitation all day")
	}
	data.Hours[3].TotalMM, data.Hours[4].TotalMM, data.Hours[5].TotalMM = Number(.099), Number(.1), Number(.2)
	d, _ = Summarize(data, "2026-10-09")
	if d.WetHours.Subtotal != Number(2) || d.WetHours.Total.Known || d.Kind != "precipitation" {
		t.Fatal("modeled wet-hour threshold or unknown type incorrect")
	}
	for i := range data.Hours {
		data.Hours[i].TotalMM = Value{}
	}
	d, _ = Summarize(data, "2026-10-09")
	if d.TotalMM.Subtotal.Known || d.WetHours.Subtotal.Known || d.TotalMM.Covered != 0 || d.Kind != "unknown" {
		t.Fatal("wholly missing amount became a zero subtotal")
	}
}

func TestIndicatedTypeUsesAmountsOnly(t *testing.T) {
	for _, tc := range []struct {
		hour Hour
		kind string
	}{
		{Hour{}, "unknown"},
		{Hour{TotalMM: Number(0)}, "none"},
		{Hour{TotalMM: Number(.1)}, "precipitation"},
		{Hour{RainMM: Number(.01)}, "rain"},
		{Hour{ShowersMM: Number(.1)}, "rain"},
		{Hour{SnowCM: Number(.1)}, "snow"},
		{Hour{RainMM: Number(.1), SnowCM: Number(.1)}, "rain_and_snow"},
		{Hour{DepthM: Number(1), FreezingM: Number(0)}, "unknown"},
	} {
		if got := tc.hour.Kind(); got != tc.kind {
			t.Errorf("%+v: got %s want %s", tc.hour, got, tc.kind)
		}
	}
}

func TestInvalidOrAbsentLocalDates(t *testing.T) {
	d := dayData("Pacific/Apia", instant("2011-12-29T10:00:00Z"))
	for _, date := range []string{"", "2026-02-29", "2026-1-01", "1999-12-31", "2101-01-01", "2011-12-30"} {
		if _, err := Summarize(d, date); err == nil {
			t.Errorf("accepted invalid or absent local date %s", date)
		}
	}
	d.Hours[1].End = d.Hours[0].End
	if _, err := Summarize(d, "2011-12-29"); err == nil {
		t.Fatal("summarized invalid data")
	}
}

func BenchmarkTenLocalDays(b *testing.B) {
	d, err := Parse(payload(MaxHours), testLocation(), testTime())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		for i := 0; i < 10; i++ {
			if _, err := Summarize(d, testTime().AddDate(0, 0, i).Format(time.DateOnly)); err != nil {
				b.Fatal(err)
			}
		}
	}
}
