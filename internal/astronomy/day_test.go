package astronomy

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Independent US Naval Observatory API fixtures, retrieved October 9, 2026.
// Each checked-in fixture retains its exact public request URL and response.
// Their reference times are whole minutes; allow two minutes for atmosphere,
// rounding and our spherical topocentric approximation, not sub-second claims.
func TestUSNOReferences(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	if err != nil || len(files) != 7 {
		t.Fatal(files, err)
	}
	type phenomenon struct{ Phen, Time string }
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			var fixture struct {
				URL      string
				Response struct {
					Geometry   struct{ Coordinates []float64 }
					Properties struct {
						Data struct {
							Year, Month, Day  int
							TZ                float64
							Fracillum         string
							Sundata, Moondata []phenomenon
						}
					}
				}
			}
			raw, err := os.ReadFile(path)
			if err != nil || json.Unmarshal(raw, &fixture) != nil || !strings.HasPrefix(fixture.URL, "https://aa.usno.navy.mil/") {
				t.Fatal("bad reference", err)
			}
			r := fixture.Response.Properties.Data
			zone := time.FixedZone("reference", int(r.TZ*3600))
			date := fmt.Sprintf("%04d-%02d-%02d", r.Year, r.Month, r.Day)
			coords := fixture.Response.Geometry.Coordinates
			d, err := Calculate(date, zone, coords[1], coords[0])
			if err != nil {
				t.Fatal(err)
			}
			check := func(rows []phenomenon, b Body, civil []Event) {
				t.Helper()
				count := 0
				for _, row := range rows {
					events, kind := b.Events, ""
					switch row.Phen {
					case "Rise":
						kind = "rise"
						count++
					case "Set":
						kind = "set"
						count++
					case "Begin Civil Twilight":
						events, kind = civil, "rise"
					case "End Civil Twilight":
						events, kind = civil, "set"
					case "Object continuously above the Horizon":
						if b.State != "always_up" {
							t.Error("expected always up", b)
						}
					case "Object continuously below the Horizon":
						if b.State != "always_down" {
							t.Error("expected always down", b)
						}
					}
					if kind == "" {
						continue
					}
					want, err := time.ParseInLocation("2006-01-02 15:04", date+" "+row.Time, zone)
					if err != nil {
						t.Fatal(err)
					}
					best := 48 * time.Hour
					for _, e := range events {
						if e.Kind == kind && e.Time.Sub(want).Abs() < best {
							best = e.Time.Sub(want).Abs()
						}
					}
					if best > 2*time.Minute {
						t.Errorf("%s %s differs by %v: %+v", row.Phen, row.Time, best, events)
					}
					t.Log(row.Phen, row.Time, "error", best.Round(time.Second))
				}
				if count != len(b.Events) {
					t.Errorf("expected %d horizon events, got %+v", count, b.Events)
				}
			}
			check(r.Sundata, d.Sun, d.Civil)
			check(r.Moondata, d.Moon, nil)
			var percent float64
			if _, err := fmt.Sscanf(r.Fracillum, "%f%%", &percent); err != nil || math.Abs(d.Illumination*100-percent) > 1 {
				t.Fatal("illumination", d.Illumination, r.Fracillum, err)
			}
		})
	}
}

func TestLocalCalendarBoundariesAndPhases(t *testing.T) {
	for _, row := range []struct {
		date, zone string
		hours      float64
	}{
		{"2026-03-08", "America/New_York", 23}, {"2026-11-01", "America/New_York", 25},
		{"2026-04-05", "Australia/Lord_Howe", 24.5}, {"2026-10-04", "Australia/Lord_Howe", 23.5},
		{"2026-09-06", "America/Santiago", 23}, {"2026-06-21", "Asia/Kathmandu", 24},
		{"2011-12-31", "Pacific/Apia", 24}, {"2026-06-21", "Pacific/Kiritimati", 24},
		{"2000-01-01", "UTC", 24}, {"2100-12-31", "UTC", 24},
	} {
		t.Run(row.date+row.zone, func(t *testing.T) {
			zone, err := time.LoadLocation(row.zone)
			if err != nil {
				t.Fatal(err)
			}
			d, err := Calculate(row.date, zone, 40, -74)
			if err != nil || d.End.Sub(d.Start).Hours() != row.hours || d.Start.In(zone).Format(time.DateOnly) != row.date || d.End.Add(-time.Second).In(zone).Format(time.DateOnly) != row.date {
				t.Fatal(d, err)
			}
			if d.Start.Add(-time.Second).In(zone).Format(time.DateOnly) == row.date || d.End.In(zone).Format(time.DateOnly) == row.date || d.PhaseAt.In(zone).Hour() != 12 {
				t.Fatal("incorrect civil boundaries", d)
			}
			for _, b := range []Body{d.Sun, d.Moon} {
				if len(b.Events) > 4 || len(b.Above) > 3 {
					t.Fatal("unbounded events", b)
				}
				for i, e := range b.Events {
					if e.Time.Before(d.Start) || !e.Time.Before(d.End) || i > 0 && !e.Time.After(b.Events[i-1].Time) {
						t.Fatal("event outside calendar day or unordered", b)
					}
				}
			}
			if d.Phase < 0 || d.Phase >= 1 || d.Illumination < 0 || d.Illumination > 1 || d.Daylight < 0 || d.Daylight > d.End.Sub(d.Start) {
				t.Fatal("invalid proportions", d)
			}
		})
	}
	// USNO primary phases: new 2026-10-10 15:50 UTC; full 2026-12-24 01:28 UTC.
	for _, row := range []struct {
		at   string
		want float64
	}{{"2026-10-10T15:50:00Z", 0}, {"2026-12-24T01:28:00Z", .5}} {
		at, _ := time.Parse(time.RFC3339, row.at)
		phase, _ := illumination(days(at))
		delta := math.Abs(phase - row.want)
		if math.Min(delta, 1-delta) > .0002 {
			t.Fatal("phase convention mismatch", row, phase)
		}
	}
}

func TestPolarAndGoldenWindows(t *testing.T) {
	for _, row := range []struct {
		lat         float64
		date, state string
	}{{90, "2026-06-21", "always_up"}, {90, "2026-12-21", "always_down"}, {-90, "2026-06-21", "always_down"}, {-90, "2026-12-21", "always_up"}} {
		d, err := Calculate(row.date, time.UTC, row.lat, 0)
		if err != nil || d.Sun.State != row.state || len(d.Sun.Events) != 0 {
			t.Fatal(row, d, err)
		}
	}
	zone, _ := time.LoadLocation("America/New_York")
	d, _ := Calculate("2026-10-09", zone, 40.7128, -74.006)
	if len(d.Golden) != 2 || d.Change >= 0 || d.Change < -4*time.Minute || len(d.Nautical) != 2 || len(d.Astronomical) != 2 {
		t.Fatal(d)
	}
	for _, s := range d.Golden {
		h := solarHeight(days(s.Start.Add(s.End.Sub(s.Start)/2)), 40.7128, -74.006)
		if h < -4 || h > 6 || s.Start.Before(d.Start) || s.End.After(d.End) {
			t.Fatal("bad golden window", s, h)
		}
	}
}

func TestGrazingCrossingsAndCalendarClipping(t *testing.T) {
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	for _, minute := range []float64{1, 4, 1438, 1439} {
		height := func(at time.Time) float64 { x := at.Sub(start).Minutes() - minute; return .25 - x*x }
		b := body(start, start.Add(24*time.Hour), height, 0)
		if len(b.Events) != 2 || len(b.Above) != 1 || math.Abs(b.Above[0].End.Sub(b.Above[0].Start).Seconds()-60) > 2 {
			t.Fatal("lost grazing pair", minute, b)
		}
	}
	a := []Span{{start, start.Add(4 * time.Hour)}}
	b := []Span{{start.Add(-time.Hour), start.Add(time.Hour)}, {start.Add(2 * time.Hour), start.Add(5 * time.Hour)}}
	r := subtract(a, b)
	if len(r) != 1 || r[0].Start != start.Add(time.Hour) || r[0].End != start.Add(2*time.Hour) {
		t.Fatal(r)
	}
}

func TestInvalidInputs(t *testing.T) {
	for _, date := range []string{"", "2026-02-30", "2026-1-01", "1999-12-31", "2101-01-01"} {
		if _, err := Calculate(date, time.UTC, 0, 0); err == nil {
			t.Fatal(date)
		}
	}
	for _, lat := range []float64{math.NaN(), math.Inf(1), 91} {
		if _, err := Calculate("2026-10-09", time.UTC, lat, 0); err == nil {
			t.Fatal(lat)
		}
	}
	if _, err := Calculate("2026-10-09", time.UTC, 0, 181); err == nil {
		t.Fatal("longitude")
	}
	if _, err := Calculate("2026-10-09", nil, 0, 0); err == nil {
		t.Fatal("timezone")
	}
	z, _ := time.LoadLocation("Pacific/Apia")
	if _, err := Calculate("2011-12-30", z, 0, 0); err == nil {
		t.Fatal("skipped date")
	}
}

func BenchmarkCalendarDay(b *testing.B) {
	zone, _ := time.LoadLocation("America/New_York")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Calculate("2026-11-01", zone, 40.7128, -74.006); err != nil {
			b.Fatal(err)
		}
	}
}
