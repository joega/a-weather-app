package app

import (
	"encoding/json"
	"fmt"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func appFixture(now time.Time) M {
	return M{"schema_version": 1.0, "location": weather.DefaultLocation(), "fetched_at": now.UTC().Format(time.RFC3339), "current": M{"time": now.UTC().Format(time.RFC3339), "condition": "clear", "temperature_c": 15.0, "is_day": true}, "hourly": []any{}, "daily": []any{}, "alerts": M{"status": "available", "fetched_at": now.UTC().Format(time.RFC3339), "items": []any{}}}
}

func TestSnapshotEscapedUnicodeFitsWireBudget(t *testing.T) {
	for _, alphabet := range []string{"🌧", "\u2028", "🌧\u2028\"\\\n"} {
		t.Run(fmt.Sprintf("runes-%x", []rune(alphabet)), func(t *testing.T) {
			now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
			f := appFixture(now)
			hours := []any{}
			for i := 0; i < 240; i++ {
				row := M{"time": now.Add(time.Duration(i) * time.Hour).Format(time.RFC3339), "condition": "thunderstorm", "is_day": true}
				for key := range weather.WeatherBounds {
					row[key] = 0.12345678901234567
				}
				for key, bounds := range weather.OptionalWeatherBounds {
					row[key] = bounds[0] + 0.12345678901234567
				}
				hours = append(hours, row)
			}
			f["hourly"] = hours
			for key, bounds := range weather.OptionalWeatherBounds {
				object(f["current"])[key] = bounds[0] + 0.12345678901234567
			}
			days := []any{}
			for i := 0; i < 10; i++ {
				days = append(days, M{"date": now.AddDate(0, 0, i).Format("2006-01-02"), "condition": "thunderstorm", "high_c": 22.123456789012345, "low_c": 10.123456789012345, "precipitation_probability": .9876543210987654, "sunrise": now.AddDate(0, 0, i).Format(time.RFC3339), "sunset": now.AddDate(0, 0, i).Add(12 * time.Hour).Format(time.RFC3339)})
			}
			f["daily"] = days
			text := func(n int) string { runes := []rune(strings.Repeat(alphabet, n)); return string(runes[:n]) }
			description, instruction := text(4096), text(2048)
			alerts := []any{}
			for i := 0; i < 8; i++ {
				alerts = append(alerts, M{"id": fmt.Sprintf("alert-%d", i), "event": "Thunderstorm warning", "severity": "Extreme", "urgency": "Immediate", "headline": "Thunderstorm", "effective": now.Add(-time.Hour).Format(time.RFC3339), "expires": now.Add(time.Hour).Format(time.RFC3339), "description": description, "instruction": instruction})
			}
			object(f["alerts"])["items"] = alerts
			if e := weather.ValidateSnapshot(f, weather.DefaultLocation()); e != nil {
				t.Fatal(e)
			}
			before := safeio.Clone(f)
			a := &App{options: Options{Now: func() time.Time { return now }}, location: weather.DefaultLocation(), forecast: f, controls: DefaultControls(), mode: "default", country: "US", launcherStatus: "ready"}
			a.search.init()
			a.search.status = "ready"
			for i := 0; i < 10; i++ {
				a.search.rows = append(a.search.rows, M{"id": float64(i + 1), "name": strings.Repeat("🌧", 120), "admin1": strings.Repeat("🌧", 244), "country": strings.Repeat("🌧", 120), "country_code": "DE"})
			}
			snapshot := a.snapshot()
			raw, e := json.Marshal(snapshot)
			if e != nil || len(raw) > snapshotByteLimit {
				t.Fatalf("snapshot exceeds byte budget: %d, %v", len(raw), e)
			}
			wire, e := json.Marshal(M{"version": 1.0, "request_id": 2147483647.0, "ok": true, "snapshot": snapshot})
			if e != nil || len(wire) > ipc.ResponseLimit {
				t.Fatalf("reply exceeds wire budget: %d, %v", len(wire), e)
			}
			event, e := json.Marshal(M{"version": 1.0, "event": "snapshot", "snapshot": snapshot})
			if e != nil || len(event) > ipc.ResponseLimit {
				t.Fatalf("event exceeds wire budget: %d, %v", len(event), e)
			}
			// The Qt bridge also limits JSON complexity before decoding a
			// snapshot. Count the actual encoded reply, including its envelope.
			var tree any
			if e := json.Unmarshal(wire, &tree); e != nil {
				t.Fatal(e)
			}
			nodes := 0
			var checkTree func(any, int)
			checkTree = func(value any, depth int) {
				nodes++
				if nodes > 8192 || depth > 16 {
					t.Fatal("reply exceeds Qt JSON complexity budget", nodes, depth)
				}
				switch value := value.(type) {
				case map[string]any:
					if len(value) > 64 {
						t.Fatal("reply exceeds Qt object cardinality budget")
					}
					for _, child := range value {
						checkTree(child, depth+1)
					}
				case []any:
					if len(value) > 240 {
						t.Fatal("reply exceeds Qt array cardinality budget")
					}
					for _, child := range value {
						checkTree(child, depth+1)
					}
				}
			}
			checkTree(tree, 0)
			if len(snapshot["hourly"].([]any)) != 240 || len(snapshot["daily"].([]any)) != 10 {
				t.Fatal("forecast records discarded to reduce message size")
			}
			for key := range weather.OptionalWeatherBounds {
				if object(snapshot["current"])[key] == nil || object(snapshot["hourly"].([]any)[239])[key] == nil {
					t.Fatal("optional metrics lost at maximum forecast size", key)
				}
			}
			if len(object(snapshot["place_search"])["results"].([]any)) != 10 {
				t.Fatal("place results lost at maximum forecast size")
			}
			display := object(snapshot["alerts"])["items"].([]any)
			if len(display) != 8 {
				t.Fatal("alert metadata discarded")
			}
			for i, value := range display {
				row := object(value)
				if row["id"] != fmt.Sprintf("alert-%d", i) || row["event"] != "Thunderstorm warning" || row["severity"] != "Extreme" || row["urgency"] != "Immediate" || row["text_truncated"] != true {
					t.Fatal("metadata/truncation flag lost", row)
				}
				for key, original := range map[string]string{"description": description, "instruction": instruction} {
					trimmed := stringOf(row[key])
					if trimmed == "" || !utf8.ValidString(trimmed) || !strings.HasPrefix(original, trimmed) || trimmed == original {
						t.Fatal("alert not trimmed on rune boundaries", key)
					}
				}
			}
			if !reflect.DeepEqual(f, before) {
				t.Fatal("byte cap mutated saved alert text")
			}
			repeat, _ := json.Marshal(a.snapshot())
			if string(repeat) != string(raw) {
				t.Fatal("byte cap is nondeterministic")
			}
			t.Logf("snapshot=%d bytes, reply=%d bytes, event=%d bytes, tree=%d nodes", len(raw), len(wire), len(event), nodes)
		})
	}
}

func TestSnapshotIgnoresUnrecognizedPreviewFields(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	f := appFixture(now)
	object(f["current"])["ignored_provider_field"] = strings.Repeat("🌧", ipc.ResponseLimit)
	a := &App{options: Options{Now: func() time.Time { return now }}, location: weather.DefaultLocation(), forecast: f, controls: DefaultControls(), mode: "default", country: "US", launcherStatus: "ready"}
	snapshot := a.snapshot()
	if _, exists := object(object(snapshot["preview"])["current"])["ignored_provider_field"]; exists {
		t.Fatal("ignored provider data escaped through display preview")
	}
	raw, e := json.Marshal(snapshot)
	if e != nil || len(raw) > snapshotByteLimit {
		t.Fatal("ignored data enlarged snapshot", len(raw), e)
	}
}
func testState(t *testing.T) *safeio.Directory {
	t.Helper()
	path := t.TempDir()
	if e := os.Chmod(path, 0700); e != nil {
		t.Fatal(e)
	}
	d, e := safeio.OpenDir(path, false)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
func TestSnapshotDSTLabelsAndLivePreview(t *testing.T) {
	now := time.Date(2026, 11, 1, 4, 30, 0, 0, time.UTC)
	f := appFixture(now)
	f["hourly"] = []any{M{"time": "2026-11-01T04:00:00Z", "condition": "clear"}, M{"time": "2026-11-01T05:00:00Z", "condition": "clear"}, M{"time": "2026-11-01T06:00:00Z", "condition": "rain"}}
	f["daily"] = []any{M{"date": "2026-11-01", "condition": "clear", "high_c": 20.0, "low_c": 10.0, "sunrise": "2026-11-01T11:25:00Z", "sunset": nil}, M{"date": "2026-11-02", "condition": "rain"}}
	a := &App{options: Options{Now: func() time.Time { return now }}, location: weather.DefaultLocation(), forecast: f, controls: DefaultControls(), mode: "default", country: "US", launcherStatus: "ready"}
	a.controls["mode"] = "manual"
	a.controls["manual"] = M{"condition": "snow"}
	before := safeio.Clone(f)
	s := a.snapshot()
	rows := s["hourly"].([]any)
	if len(rows) != 2 {
		t.Fatal("past hour not filtered")
	}
	if object(rows[0])["local_hour"] != "1 AM" || object(rows[1])["local_hour"] != "1 AM" {
		t.Fatal("repeated DST hour lost")
	}
	if object(rows[1])["period_label"] != "1:00 AM EDT – 1:00 AM EST" {
		t.Fatal(object(rows[1])["period_label"])
	}
	daily := s["daily"].([]any)
	if object(daily[0])["day_label"] != "Today" || object(daily[0])["sunrise_label"] != "6:25 AM" || object(daily[1])["day_label"] != "Mon" {
		t.Fatal(daily)
	}
	if object(s["current"])["condition"] != "clear" || object(s["preview"])["freshness"] != "manual" {
		t.Fatal("manual preview replaced observations")
	}
	if object(s["source"])["freshness"] != "fresh" || object(s["source"])["refreshing"] != false {
		t.Fatal(s["source"])
	}
	if !reflect.DeepEqual(f, before) {
		t.Fatal("snapshot changed saved forecast")
	}
}
func TestSnapshotRanksBeforeCapAndSanitizes(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	f := appFixture(now)
	items := []any{}
	for i := 0; i < 10; i++ {
		items = append(items, M{"event": "Low", "severity": "Minor", "urgency": "Future", "effective": "2026-09-27T11:00:00Z", "expires": "2026-09-27T14:00:00Z"})
	}
	items = append(items, M{"event": "<Warning>", "severity": "Extreme", "urgency": "Immediate", "effective": "2026-09-27T11:00:00Z", "expires": "2026-09-27T13:00:00Z"})
	object(f["alerts"])["items"] = items
	a := &App{options: Options{Now: func() time.Time { return now }}, location: weather.DefaultLocation(), forecast: f, controls: DefaultControls(), mode: "default", country: "US", launcherStatus: "ready"}
	rows := object(a.snapshot()["alerts"])["items"].([]any)
	if len(rows) != 8 || object(rows[0])["event"] != "Warning" {
		t.Fatal("severity warning hidden by display cap", rows)
	}
	if object(rows[0])["expires_label"] != "Sun 09:00 AM EDT" {
		t.Fatal(object(rows[0])["expires_label"])
	}
	s := a.snapshot()
	a.forecast = nil
	empty := a.snapshot()
	if empty["current"] != nil || len(empty["hourly"].([]any)) != 0 || object(empty["alerts"])["status"] != "unavailable" {
		t.Fatal("missing forecast schema")
	}
	if object(s["source"])["name"] != "Open-Meteo" || object(s["source"])["attribution"] != "Weather data by Open-Meteo.com (CC BY 4.0)" {
		t.Fatal("attribution missing")
	}
}
