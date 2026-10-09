package app

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/forecastchange"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func forecastChangesFixture(t *testing.T) (*App, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 9, 14, 30, 0, 0, time.UTC)
	a := briefingFixture(t, now, "America/New_York")
	a.options.Now = func() time.Time { return now }
	a.state = testState(t)
	a.mode, a.id = "place", "place-100"
	return a, &now
}
func forecastSeenQuery(a *App) M {
	return M{"location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": a.location["timezone"], "forecast_at": a.forecast["fetched_at"]}
}
func acknowledgeForecast(t *testing.T, a *App) M {
	t.Helper()
	reply, _ := a.Handle(context.Background(), request("forecast_presented", M{"forecast": forecastSeenQuery(a)}))
	if reply["ok"] != true || len(reply) != 4 || reply["snapshot"] != nil {
		t.Fatal(reply)
	}
	if raw, err := ipc.Encode(reply, ipc.ResponseLimit); err != nil || len(raw) > 6000 {
		t.Fatal("comparison exceeded 6 KiB reply target", len(raw), err)
	}
	return object(reply["forecast_changes"])
}
func reviseForecast(a *App, now *time.Time, temperature float64) {
	*now = now.Add(15 * time.Minute)
	a.forecast = safeio.Clone(a.forecast)
	a.forecast["fetched_at"] = changeStamp(*now)
	for _, raw := range a.forecast["hourly"].([]any) {
		object(raw)["temperature_c"] = temperature
	}
}

func TestForecastChangesPresentedHistoryAndRestart(t *testing.T) {
	a, now := forecastChangesFixture(t)
	a.Snapshot()
	if a.forecastHistory != nil {
		t.Fatal("snapshot loaded optional history")
	}
	first := acknowledgeForecast(t, a)
	if first["status"] != "no_previous" || first["save_status"] != "saved" || first["previous_retrieved"] != nil {
		t.Fatal(first)
	}
	baseline := a.forecast["fetched_at"]
	files := &savedFaultFiles{Directory: a.state}
	a.forecastHistory.files = files
	before := safeio.Clone(a.forecast)
	reviseForecast(a, now, 25)
	// A background refresh followed by snapshots cannot consume the baseline.
	a.Snapshot()
	if len(files.writes) != 0 || changeStamp(a.forecastHistory.entries[0].current.Retrieved) != baseline {
		t.Fatal("background work advanced history")
	}
	result := acknowledgeForecast(t, a)
	if result["status"] != "ready" || result["previous_retrieved"] != baseline || len(result["changes"].([]any)) != 1 {
		t.Fatal(result)
	}
	c := object(result["changes"].([]any)[0])
	if c["kind"] != "temperature" || c["before"] != 20.0 || c["after"] != 25.0 || c["range_label"] == "" {
		t.Fatal(c)
	}
	if before["fetched_at"] != baseline || len(files.writes) != 1 {
		t.Fatal("incorrect write count", files.writes)
	}
	// Alert-only replacement uses the same retrieval and must preserve evidence.
	a.forecast = safeio.Clone(a.forecast)
	a.forecast["alerts"] = weather.UnavailableAlerts()
	if got := acknowledgeForecast(t, a); !reflect.DeepEqual(got, result) || len(files.writes) != 1 {
		t.Fatal("same retrieval replaced baseline", got, files.writes)
	}
	reopened, _ := forecastChangesFixture(t)
	reopened.state, reopened.forecast = a.state, safeio.Clone(a.forecast)
	reopened.options.Now = a.options.Now
	if got := acknowledgeForecast(t, reopened); !reflect.DeepEqual(got, result) {
		t.Fatal("restart lost comparison", got)
	}
	// A later presentation advances exactly once from the preceding displayed one.
	previous := a.forecast["fetched_at"]
	reviseForecast(a, now, 30)
	if got := acknowledgeForecast(t, a); got["previous_retrieved"] != previous || object(got["changes"].([]any)[0])["before"] != 25.0 {
		t.Fatal(got)
	}
}

func TestForecastChangesRejectObsoleteRequestsWithoutHistoryWork(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*App, M, *time.Time)
	}{
		{"identity", func(a *App, q M, _ *time.Time) { q["location_id"] = "place-other" }},
		{"coordinates", func(a *App, q M, _ *time.Time) { q["latitude"] = a.location["latitude"].(float64) + 1 }},
		{"timezone", func(_ *App, q M, _ *time.Time) { q["timezone"] = "UTC" }},
		{"retrieval", func(_ *App, q M, n *time.Time) { q["forecast_at"] = changeStamp(n.Add(-time.Minute)) }},
		{"location changing", func(a *App, _ M, _ *time.Time) { a.locationBusy = true }},
		{"primary only", func(a *App, _ M, _ *time.Time) { a.primaryOnly = true }},
		{"unselected default", func(a *App, _ M, _ *time.Time) { a.mode = "default" }},
		{"expired", func(_ *App, _ M, n *time.Time) { *n = n.Add(3 * time.Hour) }},
		{"future", func(_ *App, _ M, n *time.Time) { *n = n.Add(-time.Minute) }},
		{"extra fields", func(_ *App, q M, _ *time.Time) { q["extra"] = true }},
		{"malformed number", func(_ *App, q M, _ *time.Time) { q["latitude"] = math.NaN() }},
		{"invalid rows", func(a *App, _ M, _ *time.Time) { object(a.forecast["hourly"].([]any)[1])["temperature_c"] = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, now := forecastChangesFixture(t)
			q := forecastSeenQuery(a)
			tc.edit(a, q, now)
			r, _ := a.Handle(context.Background(), request("forecast_presented", M{"forecast": q}))
			if r["ok"] != false || a.forecastHistory != nil {
				t.Fatal("rejected request loaded history", r)
			}
			if doc, err := a.state.Read(forecastHistoryFile, forecastHistoryBytes); err != nil || doc != nil {
				t.Fatal("rejected request persisted history", doc, err)
			}
		})
	}
	a, _ := forecastChangesFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _ := a.Handle(ctx, request("forecast_presented", M{"forecast": forecastSeenQuery(a)}))
	if r["ok"] != false || a.forecastHistory != nil {
		t.Fatal("canceled request consumed presentation", r)
	}
}

func TestForecastChangesPersistenceFailureRemainsSessionEvidence(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprint(after), func(t *testing.T) {
			a, now := forecastChangesFixture(t)
			acknowledgeForecast(t, a)
			baseline := a.forecast["fetched_at"]
			files := &savedFaultFiles{Directory: a.state, fail: forecastHistoryFile, after: after}
			a.forecastHistory.files = files
			reviseForecast(a, now, 26)
			r := acknowledgeForecast(t, a)
			if r["status"] != "ready" || r["save_status"] != "unconfirmed" || r["previous_retrieved"] != baseline {
				t.Fatal(r)
			}
			if changeStamp(a.forecastHistory.entries[0].current.Retrieved) != a.forecast["fetched_at"] {
				t.Fatal("failed optional write prevented session adoption")
			}
			// A retry saves the same pair rather than losing the displayed baseline.
			files.fail = ""
			if r = acknowledgeForecast(t, a); r["save_status"] != "saved" || r["previous_retrieved"] != baseline {
				t.Fatal(r)
			}
			if len(files.writes) != 2 {
				t.Fatal(files.writes)
			}
			reopened := loadForecastHistory(a.state)
			if reopened.recovered || len(reopened.entries) != 1 || changeStamp(reopened.entries[0].previous.Retrieved) != baseline {
				t.Fatal("retry did not preserve comparison")
			}
		})
	}
}

func TestForecastHistoryBoundedRecencyAndIdentity(t *testing.T) {
	a, now := forecastChangesFixture(t)
	for i := 0; i < 5; i++ {
		a.id = fmt.Sprintf("place-%d", i)
		acknowledgeForecast(t, a)
	}
	h := a.forecastHistory
	if len(h.entries) != 4 || h.entries[0].id != "place-4" || h.entries[3].id != "place-1" {
		t.Fatal("history not bounded to recent places", h.entries)
	}
	a.id = "place-2"
	acknowledgeForecast(t, a)
	if h.entries[0].id != "place-2" || h.entries[3].id != "place-1" {
		t.Fatal("reopening did not update recency")
	}
	// Moving current location must not compare two physical places.
	a.location = safeio.Clone(a.location)
	a.location["latitude"] = a.location["latitude"].(float64) + 1
	reviseForecast(a, now, 30)
	if r := acknowledgeForecast(t, a); r["status"] != "no_previous" {
		t.Fatal(r)
	}
	current := h.entries[0].current.Clone()
	older := current.Clone()
	older.Retrieved = older.Retrieved.Add(-time.Minute)
	if r, _, err := h.advance(a.id, older, *now); err != nil || r.Status != "not_newer" || !h.entries[0].current.Retrieved.Equal(current.Retrieved) {
		t.Fatal("older retrieval rolled back history", r, err)
	}
	other := current.Clone()
	other.Source = "another-feed"
	if r, _, err := h.advance(a.id, other, *now); err != nil || r.Status != "no_previous" || h.entries[0].previous != nil {
		t.Fatal("cross-source comparison", r, err)
	}
	if restored := loadForecastHistory(a.state); restored.recovered || len(restored.entries) != 4 || restored.entries[0].id != a.id {
		t.Fatal("recency not persisted")
	}
}

func TestForecastHistoryCorruptionAndMaximumDocument(t *testing.T) {
	a, _ := forecastChangesFixture(t)
	if err := a.state.Write(forecastHistoryFile, M{"schema_version": 99.0}, forecastHistoryBytes); err != nil {
		t.Fatal(err)
	}
	r := acknowledgeForecast(t, a)
	if r["status"] != "no_previous" || r["history_recovered"] != true || r["save_status"] != "saved" {
		t.Fatal("corrupt history blocked current forecast", r)
	}
	if loadForecastHistory(a.state).recovered {
		t.Fatal("fresh baseline did not repair history")
	}
	f, err := a.forecastProjection()
	if err != nil {
		t.Fatal(err)
	}
	f.Hours = nil
	for i := 0; i < forecastchange.MaxHours; i++ {
		f.Hours = append(f.Hours, forecastchange.Hour{Time: f.Retrieved.Add(time.Duration(i) * time.Hour), TemperatureC: forecastchange.Number(-123.12345678901234), Probability: forecastchange.Number(.1234567890123456), PrecipitationMM: forecastchange.Number(1234.1234567890123), GustMS: forecastchange.Number(123.12345678901234)})
	}
	old := f.Clone()
	old.Retrieved = old.Retrieved.Add(-time.Minute)
	h := &forecastHistory{files: a.state, dirty: true}
	for i := 0; i < 4; i++ {
		h.entries = append(h.entries, forecastHistoryEntry{id: fmt.Sprintf("place-%d", i), current: f, previous: &old})
	}
	if h.save() != "saved" {
		t.Fatal("maximum history could not be saved")
	}
	doc, err := a.state.Read(forecastHistoryFile, forecastHistoryBytes)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ipc.Encode(doc, forecastHistoryBytes)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("maximum 4-place / 8-projection history: %d bytes", len(raw))
	if entries, err := readForecastHistory(doc); err != nil || len(entries) != 4 {
		t.Fatal("maximum history failed round trip", err)
	}
	for _, edit := range []func(M){
		func(d M) { d["extra"] = true },
		func(d M) { d["places"] = append(d["places"].([]any), d["places"].([]any)[0]) },
		func(d M) { object(d["places"].([]any)[1])["id"] = object(d["places"].([]any)[0])["id"] },
		func(d M) { row := object(d["places"].([]any)[0]); row["previous"] = row["current"] },
		func(d M) {
			p := object(object(d["places"].([]any)[0])["current"])
			p["hours"].([]any)[0].([]any)[2] = "missing"
		},
	} {
		damaged := safeio.Clone(doc)
		edit(damaged)
		if _, err := readForecastHistory(damaged); err == nil {
			t.Fatal("invalid persisted evidence accepted")
		}
	}
}

func TestForecastPresentedRequiresThisVisibleSubscriber(t *testing.T) {
	f := serveFixture(t)
	f.a.mu.Lock()
	f.a.mode, f.a.id = "place", "place-100"
	f.a.forecast = appFixture(f.a.options.Now())
	f.a.forecast["hourly"] = []any{M{"time": changeStamp(f.a.options.Now().Add(time.Hour)), "condition": "clear", "temperature_c": 20.0}}
	q := forecastSeenQuery(f.a)
	f.a.mu.Unlock()
	c := connect(t, f.path)
	r := bufio.NewReader(c)
	send := func(op string, patch M) M {
		t.Helper()
		if err := ipc.Send(c, request(op, patch), ipc.RequestLimit); err != nil {
			t.Fatal(err)
		}
		return readReply(t, r)
	}
	if got := send("forecast_presented", M{"forecast": q}); got["error"] != "forecast_not_presented" {
		t.Fatal("unsubscribed peer consumed history", got)
	}
	if send("subscribe", nil)["ok"] != true {
		t.Fatal("subscribe")
	}
	if send("set_presentation", M{"active": false})["ok"] != true {
		t.Fatal("hide")
	}
	// Another visible subscriber must not authorize the hidden one.
	other := connect(t, f.path)
	otherReader := bufio.NewReader(other)
	if err := ipc.Send(other, request("subscribe", nil), ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	if readReply(t, otherReader)["ok"] != true {
		t.Fatal("other subscribe")
	}
	if got := send("forecast_presented", M{"forecast": q}); got["error"] != "forecast_not_presented" {
		t.Fatal("hidden peer consumed history", got)
	}
	f.a.mu.Lock()
	loaded := f.a.forecastHistory != nil
	f.a.mu.Unlock()
	if loaded {
		t.Fatal("unseen forecast loaded history")
	}
	if send("set_presentation", M{"active": true})["ok"] != true {
		t.Fatal("restore")
	}
	if got := send("forecast_presented", M{"forecast": q}); got["ok"] != true || got["snapshot"] != nil {
		t.Fatal("visible subscriber could not acknowledge forecast", got)
	}
}

func TestForecastHistoryRemovalAndNilStorage(t *testing.T) {
	a, _ := forecastChangesFixture(t)
	acknowledgeForecast(t, a)
	if !a.forgetForecastHistory(a.id) || len(loadForecastHistory(a.state).entries) != 0 {
		t.Fatal("removed place retains comparison evidence")
	}
	acknowledgeForecast(t, a)
	files := &savedFaultFiles{Directory: a.state, fail: forecastHistoryFile}
	a.forecastHistory.files = files
	if a.forgetForecastHistory(a.id) || len(a.forecastHistory.entries) != 0 || !a.forecastHistory.dirty {
		t.Fatal("failed history removal not reported and retained for retry")
	}
	files.fail = ""
	if !a.forgetForecastHistory(a.id) || len(loadForecastHistory(a.state).entries) != 0 {
		t.Fatal("history removal retry failed")
	}
	a.state, a.forecastHistory = nil, nil
	if got := acknowledgeForecast(t, a); got["save_status"] != "unconfirmed" {
		t.Fatal("missing optional storage blocked session history", got)
	}
}

func TestForecastHistoryActualCompletionAndRemovalLifecycle(t *testing.T) {
	now := savedRuntimeNow
	a, state := runtimeLocations(t, Options{Offline: true, Now: func() time.Time { return now }}, 0)
	object(a.forecast["hourly"].([]any)[0])["time"] = changeStamp(now.Add(2 * time.Hour))
	initial := acknowledgeForecast(t, a)
	baseline := initial["current_retrieved"]
	files := &savedFaultFiles{Directory: state}
	a.forecastHistory.files = files
	a.setPresented(false)
	now = now.Add(15 * time.Minute)
	next := safeio.Clone(a.forecast)
	next["fetched_at"] = changeStamp(now)
	object(next["hourly"].([]any)[0])["precipitation_probability"] = .5
	c := completion{point: a.forecastPoint, generation: a.generation, location: a.location, forecast: next}
	a.applyForecastCompletion(c)
	if a.forecast["fetched_at"] != next["fetched_at"] || changeStamp(a.forecastHistory.entries[0].current.Retrieved) != baseline || len(files.writes) != 0 {
		t.Fatal("background completion consumed presentation")
	}
	stale := c
	stale.generation++
	stale.forecast = safeio.Clone(next)
	stale.forecast["fetched_at"] = changeStamp(now.Add(time.Minute))
	a.applyForecastCompletion(stale)
	if a.forecast["fetched_at"] != next["fetched_at"] || len(files.writes) != 0 {
		t.Fatal("late callback affected history")
	}
	a.setPresented(true)
	result := acknowledgeForecast(t, a)
	if result["previous_retrieved"] != baseline || len(result["changes"].([]any)) != 1 || len(files.writes) != 1 {
		t.Fatal("presentation did not compare to last shown forecast", result)
	}
	a.applyForecastCompletion(completion{point: a.forecastPoint, generation: a.generation, location: a.location, alerts: weather.UnavailableAlerts()})
	if got := acknowledgeForecast(t, a); !reflect.DeepEqual(got, result) || len(files.writes) != 1 {
		t.Fatal("alert completion reset comparison", got)
	}
	locationAction(t, a, M{"action": "remove", "id": "place-100", "replacement": "place-101"})
	if len(loadForecastHistory(state).entries) != 0 || savedEntry(a.saved.doc, "place-100") != nil {
		t.Fatal("removal retained comparison history")
	}
}

func TestForecastProjectionBoundsAndSnapshotIndependence(t *testing.T) {
	a, now := forecastChangesFixture(t)
	hours := make([]any, 0, 240)
	start := now.Truncate(time.Hour)
	for i := 0; i < 240; i++ {
		hours = append(hours, M{"time": changeStamp(start.Add(time.Duration(i) * time.Hour)), "temperature_c": 20.0, "condition": "clear"})
	}
	a.forecast["hourly"] = hours
	before := safeio.Clone(a.forecast)
	f, err := a.forecastProjection()
	if err != nil || len(f.Hours) != forecastchange.MaxHours || f.Hours[0].Time.Before(*now) {
		t.Fatal("projection bound", len(f.Hours), err)
	}
	if f.Hours[0].Probability.Known || !f.Hours[0].TemperatureC.Known {
		t.Fatal("missing fields became zeros")
	}
	// Requests return narrow replies and leave ordinary snapshots unchanged.
	first := a.Snapshot()
	delete(first, "snapshot_revision")
	acknowledgeForecast(t, a)
	second := a.Snapshot()
	delete(second, "snapshot_revision")
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(before, a.forecast) {
		t.Fatal("comparison changed forecast or snapshot payload")
	}
}

func BenchmarkForecastHistory(b *testing.B) {
	for _, phase := range []string{"repeat_presentation", "four_place_save", "four_place_load"} {
		b.Run(phase, func(b *testing.B) {
			a := benchmarkIdleApp(b)
			a.id, a.mode = "place-100", "place"
			query := forecastSeenQuery(a)
			if r := a.forecastPresented(1, query); r["ok"] != true {
				b.Fatal(r)
			}
			f := a.forecastHistory.entries[0].current
			old := f.Clone()
			old.Retrieved = old.Retrieved.Add(-time.Minute)
			old.Hours = old.Hours[:len(old.Hours)-1]
			a.forecastHistory.entries[0].previous = &old
			h := &forecastHistory{files: a.state, dirty: true}
			for i := 0; i < 4; i++ {
				h.entries = append(h.entries, forecastHistoryEntry{id: fmt.Sprintf("place-%d", i), current: f, previous: &old})
			}
			if h.save() != "saved" {
				b.Fatal("initial history save")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				switch phase {
				case "repeat_presentation":
					if r := a.forecastPresented(1, query); r["ok"] != true {
						b.Fatal(r)
					}
				case "four_place_save":
					h.dirty = true
					if h.save() != "saved" {
						b.Fatal("save failed")
					}
				case "four_place_load":
					if got := loadForecastHistory(a.state); got.recovered || len(got.entries) != 4 {
						b.Fatal("load failed")
					}
				}
			}
		})
	}
}

func TestForecastChangesThreeHighlightsAndLocalTimingReply(t *testing.T) {
	a, now := forecastChangesFixture(t)
	zone, _ := time.LoadLocation("America/New_York")
	day := time.Date(2026, 10, 10, 0, 0, 0, 0, zone)
	setWet := func(start, end time.Time) {
		for _, raw := range a.forecast["hourly"].([]any) {
			r := object(raw)
			stamp, _ := weather.Instant(r["time"])
			r["precipitation_probability"] = .1
			if stamp.After(start) && !stamp.After(end) {
				r["precipitation_probability"] = .8
			}
		}
	}
	setWet(day.Add(8*time.Hour), day.Add(11*time.Hour))
	acknowledgeForecast(t, a)
	reviseForecast(a, now, 25)
	setWet(day.Add(10*time.Hour), day.Add(13*time.Hour))
	for _, raw := range a.forecast["hourly"].([]any) {
		object(raw)["wind_gust_m_s"] = 20.0
	}
	r := acknowledgeForecast(t, a)
	if len(r["changes"].([]any)) != 3 {
		t.Fatal("missing bounded highlights", r)
	}
	for _, raw := range r["changes"].([]any) {
		c := object(raw)
		if c["kind"] != "wet_hours" {
			continue
		}
		if c["before"] != nil || c["after"] != nil || c["previous_range_label"] != "Sat Oct 10, 8:00 AM EDT – Sat Oct 10, 11:00 AM EDT" || c["range_label"] != "Sat Oct 10, 10:00 AM EDT – Sat Oct 10, 1:00 PM EDT" {
			t.Fatal("timing evidence mislabeled", c)
		}
		return
	}
	t.Fatal("timing highlight missing", r)
}
