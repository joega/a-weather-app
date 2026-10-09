package app

import (
	"errors"
	"math"
	"reflect"

	"github.com/joega/a-weather-app/internal/safeio"
)

const dashboardFile = "dashboard.json"
const dashboardBytes = 4096

var dashboardSections = []string{"hourly", "daily", "metrics", "maps", "air_quality"}
var dashboardMetrics = []string{"wind", "humidity", "visibility", "solar", "uv", "pressure"}
var dashboardHourly = []string{"temperature_c", "apparent_temperature_c", "precipitation_probability", "wind_speed_m_s", "wind_gust_m_s", "humidity", "uv_index"}

type dashboardFiles interface {
	Read(string, int) (M, error)
	Write(string, any, int) error
}
type dashboardState struct {
	preferences M
	revision    float64
	errorCode   string
	files       dashboardFiles
}

// All supported IDs occur exactly once, including hidden rows, so toggling a
// card preserves its position. Current conditions and official alerts are not
// optional dashboard sections and cannot be hidden by this document.
func defaultDashboard() M {
	rows := func(ids []string) []any {
		result := make([]any, 0, len(ids))
		for _, id := range ids {
			result = append(result, M{"id": id, "enabled": true})
		}
		return result
	}
	return M{"schema_version": 1.0, "density": "spacious", "sections": rows(dashboardSections), "metrics": rows(dashboardMetrics), "hourly": []any{"temperature_c", "precipitation_probability"}}
}
func validateDashboard(v M) error {
	if len(v) != 5 || v["schema_version"] != 1.0 || (v["density"] != "compact" && v["density"] != "spacious") {
		return errors.New("dashboard preferences")
	}
	for key, ids := range map[string][]string{"sections": dashboardSections, "metrics": dashboardMetrics} {
		rows, ok := v[key].([]any)
		if !ok || len(rows) != len(ids) {
			return errors.New("dashboard rows")
		}
		remaining := make(map[string]bool, len(ids))
		for _, id := range ids {
			remaining[id] = true
		}
		enabled := 0
		for _, raw := range rows {
			row := object(raw)
			id := stringOf(row["id"])
			on, ok := row["enabled"].(bool)
			if len(row) != 2 || !remaining[id] || !ok {
				return errors.New("dashboard row")
			}
			delete(remaining, id)
			if on {
				enabled++
			}
		}
		if key == "metrics" && enabled == 0 {
			return errors.New("dashboard requires one saved metric")
		}
	}
	hours, ok := v["hourly"].([]any)
	if !ok || len(hours) < 1 || len(hours) > 3 {
		return errors.New("dashboard hourly count")
	}
	allowed := make(map[string]bool, len(dashboardHourly))
	for _, key := range dashboardHourly {
		allowed[key] = true
	}
	for _, raw := range hours {
		key := stringOf(raw)
		if !allowed[key] {
			return errors.New("dashboard hourly value")
		}
		delete(allowed, key)
	}
	return nil
}
func (a *App) initDashboard() {
	a.dashboard = &dashboardState{preferences: defaultDashboard(), revision: 1, files: a.state}
	doc, err := a.state.Read(dashboardFile, dashboardBytes)
	if err != nil || doc != nil && validateDashboard(doc) != nil {
		a.dashboard.errorCode = "state_unavailable"
		return // A damaged optional layout must not prevent weather from opening.
	}
	if doc != nil {
		a.dashboard.preferences = doc
	}
}
func (a *App) dashboardSnapshot() M {
	var p M
	revision, code := 1.0, ""
	if a.dashboard != nil {
		p, revision, code = a.dashboard.preferences, a.dashboard.revision, a.dashboard.errorCode
	} else {
		p = defaultDashboard()
	}
	var stateError any
	if code != "" {
		stateError = code
	}
	return M{"preferences": safeio.Clone(p), "revision": revision, "error": stateError}
}
func (a *App) dashboardVisible(id string) bool {
	if a.dashboard == nil {
		return true
	}
	for _, raw := range a.dashboard.preferences["sections"].([]any) {
		row := object(raw)
		if row["id"] == id {
			return row["enabled"] == true
		}
	}
	return false
}
func (a *App) setDashboard(query M) string {
	if len(query) != 2 || validateDashboard(object(query["preferences"])) != nil {
		return "invalid_request"
	}
	revision, ok := query["revision"].(float64)
	if !ok || math.IsNaN(revision) || math.IsInf(revision, 0) || revision < 1 || revision > 9007199254740991 || math.Trunc(revision) != revision {
		return "invalid_request"
	}
	if a.dashboard == nil || a.dashboard.files == nil {
		return "state_io_failed"
	}
	d := a.dashboard
	if revision != d.revision || revision >= 9007199254740991 {
		return "dashboard_changed"
	}
	next := safeio.Clone(object(query["preferences"]))
	if reflect.DeepEqual(next, d.preferences) && d.errorCode == "" {
		return "" // Reapplying unchanged confirmed settings needs no disk write.
	}
	confirmed := d.files.Write(dashboardFile, next, dashboardBytes) == nil
	if !confirmed {
		// A directory fsync error may follow a successful atomic rename. Only
		// publish the new layout if a bounded read confirms the visible bytes,
		// and still report durability as unconfirmed until a later save succeeds.
		actual, err := d.files.Read(dashboardFile, dashboardBytes)
		if err != nil || !reflect.DeepEqual(actual, next) {
			return "state_io_failed"
		}
	}
	d.preferences = next
	d.revision++
	d.errorCode = ""
	if !confirmed {
		d.errorCode = "save_unconfirmed"
	}
	if !a.dashboardVisible("maps") {
		a.closeMap()
		a.closeRadar()
	}
	if !a.dashboardVisible("air_quality") {
		a.cancelAirQuality()
	}
	if !confirmed {
		return "save_unconfirmed"
	}
	return ""
}
