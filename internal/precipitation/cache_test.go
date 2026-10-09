package precipitation

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestCompactCacheRoundTripAndBound(t *testing.T) {
	d, err := Parse(payload(MaxHours), testLocation(), testTime().Add(987654321*time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	// Long float encodings exercise the explicit file budget rather than a
	// small all-zero fixture. Missing cells and known zeros remain distinct.
	for i := range d.Hours {
		h := &d.Hours[i]
		h.TotalMM, h.RainMM, h.ShowersMM, h.SnowCM = Number(9999.999999999998), Number(9999.999999999998), Number(9999.999999999998), Number(9999.999999999998)
		h.DepthM, h.FreezingM, h.Probability = Number(999.9999999999999), Number(29999.999999999996), Number(.9999999999999999)
	}
	d.Hours[0].RainMM, d.Hours[0].SnowCM = Number(0), Value{}
	record, err := CacheRecord(d)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) >= CacheBytes {
		t.Fatalf("cache budget: %d bytes, %v", len(raw), err)
	}
	t.Logf("maximum-row long-number cache fixture: %d bytes", len(raw))
	decoded, err := safeio.Object(raw, CacheBytes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadCache(decoded)
	if err != nil || !reflect.DeepEqual(d, got) {
		t.Fatalf("round trip changed units, precision or unknown cells: %v", err)
	}
	decoded["hours"].([]any)[0].([]any)[1] = 90.0
	if got.Hours[0].TotalMM != Number(9999.999999999998) {
		t.Fatal("cache decode retained mutable input")
	}
	got.Hours[0].Probability = Number(100)
	if _, err := CacheRecord(got); err == nil {
		t.Fatal("invalid data serialized")
	}
}

func TestCacheRejectsDamagedSchemaAndCells(t *testing.T) {
	d, err := Parse(payload(3), testLocation(), testTime())
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(weather.Object){
		"version":          func(p weather.Object) { p["version"] = 2.0 },
		"source":           func(p weather.Object) { p["source"] = "different/hour-contract" },
		"unknown key":      func(p weather.Object) { p["extra"] = true },
		"renamed key":      func(p weather.Object) { p["lat"] = p["latitude"]; delete(p, "latitude") },
		"location type":    func(p weather.Object) { p["latitude"] = "27" },
		"zone type":        func(p weather.Object) { p["timezone"] = nil },
		"bad zone":         func(p weather.Object) { p["timezone"] = "Local" },
		"bad date":         func(p weather.Object) { p["fetched_at"] = "invalid" },
		"empty hours":      func(p weather.Object) { p["hours"] = []any{} },
		"too many":         func(p weather.Object) { p["hours"] = make([]any, MaxHours+1) },
		"row width":        func(p weather.Object) { p["hours"].([]any)[0] = []any{1.0} },
		"cell type":        func(p weather.Object) { p["hours"].([]any)[0].([]any)[1] = "1.0" },
		"cell range":       func(p weather.Object) { p["hours"].([]any)[0].([]any)[7] = 101.0 },
		"non-finite":       func(p weather.Object) { p["hours"].([]any)[0].([]any)[2] = math.NaN() },
		"bad epoch":        func(p weather.Object) { p["hours"].([]any)[0].([]any)[0] = 1e100 },
		"fractional epoch": func(p weather.Object) { p["hours"].([]any)[0].([]any)[0] = float64(testTime().Unix()) + .5 },
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := CacheRecord(d)
			mutate(p)
			if _, err := ReadCache(p); err == nil {
				t.Fatal("damaged cache accepted")
			}
		})
	}
}
