package app

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

// Leave room for the protocol reply/event envelope below its 256 KiB limit.
const snapshotByteLimit = 250 * 1024

// encodedTextPrefix budgets JSON bytes, including escapes, rather than UTF-8
// bytes alone. Binary search keeps the cut on a complete Unicode rune.
func encodedTextPrefix(text string, extraBytes int) string {
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := low + (high-low+1)/2
		encoded, _ := json.Marshal(string(runes[:mid]))
		if len(encoded)-2 <= extraBytes {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low])
}

func capSnapshotAlertText(snapshot M) {
	// Without alert text there is nothing to truncate. Other snapshot fields
	// have their existing schema bounds and the final IPC envelope is checked.
	items, _ := object(snapshot["alerts"])["items"].([]any)
	if len(items) == 0 {
		return
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || len(encoded) <= snapshotByteLimit {
		return
	}
	type originalText struct {
		description, instruction any
		truncated                bool
	}
	originals := make([]originalText, len(items))
	for i, value := range items {
		row := object(value)
		originals[i] = originalText{row["description"], row["instruction"], row["text_truncated"] == true}
		for _, key := range []string{"description", "instruction"} {
			if _, ok := row[key].(string); ok {
				row[key] = ""
			}
		}
		row["text_truncated"] = true
	}
	base, _ := json.Marshal(snapshot)
	// A false flag is one byte longer than true; reserve that byte per alert
	// when a shorter original fits unchanged in its allowance.
	remaining := snapshotByteLimit - len(base) - len(items)
	if remaining < 0 {
		remaining = 0
	}
	perAlert := remaining / len(items)
	for i, value := range items {
		row := object(value)
		old := originals[i]
		truncated := old.truncated
		for _, field := range []struct {
			key    string
			value  any
			budget int
		}{
			{"description", old.description, perAlert * 2 / 3},
			{"instruction", old.instruction, perAlert - perAlert*2/3},
		} {
			if text, ok := field.value.(string); ok {
				prefix := encodedTextPrefix(text, field.budget)
				row[field.key] = prefix
				truncated = truncated || prefix != text
			}
		}
		row["text_truncated"] = truncated
	}
}

func clockLabel(t time.Time) string { return strings.TrimLeft(t.Format("03:04 PM"), "0") }
func (a *App) snapshot() M {
	now := a.options.Now()
	// Observations and the ambient forecast display use the default subtle
	// strength; the user's chosen strength belongs to the effects preview.
	live := weather.SelectView(a.forecast, now, "live", nil, "subtle", a.controls["reduced_motion"] == true, a.controls["lightning_enabled"] == true)
	if a.forecast == nil && a.location != nil {
		locationSelected := a.selected(true)
		live["solar"] = locationSelected["solar"]
		live["effects"] = locationSelected["effects"]
	}
	preview := a.selected(false)
	// Cached observations can carry ignored provider fields. Only normalized
	// weather fields belong in the display preview and its byte budget.
	if preview["freshness"] != "manual" {
		if record, err := weather.WeatherRecord(object(preview["current"])); err == nil {
			preview["current"] = record
		} else {
			preview["current"] = M{"condition": "unknown"}
		}
	}
	forecast := object(live["forecast"])
	location := a.location
	if location == nil {
		location = M{"name": "Current location", "timezone": "UTC", "latitude": nil, "longitude": nil}
	}
	zone, e := time.LoadLocation(stringOf(location["timezone"]))
	if e != nil {
		zone = time.UTC
	}
	hourly, daily := []any{}, []any{}
	var current any
	alerts := M{}
	if forecast != nil {
		current, _ = weather.WeatherRecord(object(forecast["current"]))
		alerts = object(forecast["alerts"])
		hourly, daily = a.forecastRows(forecast, zone, now)
	}
	if a.alerts != nil && a.forecastPoint.alerts != nil && forecast == nil {
		alerts = weather.SelectAlerts(a.forecastPoint.alerts, now)
	}
	if a.alerts != nil && a.country == "US" {
		entry := a.alerts.entries[pointAlertKey(a.forecastPoint)]
		pending := entry != nil && (entry.lastActive.IsZero() && entry.failures == 0 || a.alerts.job != nil && a.alerts.job.entry == entry && a.alerts.job.query.Active && a.alerts.job.ctx.Err() == nil)
		if pending && alerts["status"] != "available" {
			alerts = weather.UnavailableAlerts()
			alerts["freshness"], alerts["refreshing"] = "pending", true
		} else {
			alerts["refreshing"] = pending
		}
	}
	ranked := []M{}
	items, _ := alerts["items"].([]any)
	for _, v := range items {
		if row := object(v); row != nil {
			ranked = append(ranked, row)
		}
	}
	priority := func(v any, order map[string]int) int {
		if rank, ok := order[stringOf(v)]; ok {
			return rank
		}
		return 4
	}
	severity := map[string]int{"Extreme": 0, "Severe": 1, "Moderate": 2, "Minor": 3}
	urgency := map[string]int{"Immediate": 0, "Expected": 1, "Future": 2, "Past": 3}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		sa, sb := priority(a["severity"], severity), priority(b["severity"], severity)
		if sa != sb {
			return sa < sb
		}
		ua, ub := priority(a["urgency"], urgency), priority(b["urgency"], urgency)
		if ua != ub {
			return ua < ub
		}
		ta, _ := weather.Instant(a["expires"])
		tb, _ := weather.Instant(b["expires"])
		return ta.Before(tb)
	})
	display := []any{}
	for _, raw := range ranked {
		row, e := weather.AlertRecord(raw)
		if e != nil {
			continue
		}
		expiry, _ := weather.Instant(row["expires"])
		row["expires_label"] = expiry.In(zone).Format("Mon 03:04 PM MST")
		display = append(display, row)
		if len(display) == 8 {
			break
		}
	}
	status := alerts["status"]
	if status == nil {
		status = "unavailable"
	}
	var alertSource any
	coverage := "unknown"
	var alertFetched any
	if a.country == "US" {
		coverage, alertSource, alertFetched = "US", "National Weather Service", alerts["fetched_at"]
	} else {
		status, display = "unavailable", []any{}
		if a.country != nil {
			status, coverage = "not_supported_here", "unsupported"
		}
	}
	var fetched any
	if forecast != nil {
		fetched = forecast["fetched_at"]
	}
	notifications := M{"settings": M{"enabled": false, "quiet_enabled": true, "quiet_start": 22.0, "quiet_end": 7.0, "probability": 50.0}, "state": "off", "snoozed_until": nil, "delivery": "none", "supported": false}
	if a.notifications != nil {
		notifications = a.notifications.Snapshot()
	}
	result := M{"schema_version": 1.0, "location": safeio.Clone(location), "location_settings": M{"mode": a.mode, "zip_code": a.zip, "busy": a.locationBusy, "error": a.locationError}, "current": current, "hourly": hourly, "daily": daily, "alerts": M{"status": status, "items": display}, "source": M{"name": "Open-Meteo", "attribution": "Weather data by Open-Meteo.com (CC BY 4.0)", "fetched_at": fetched, "freshness": live["freshness"], "age_seconds": live["age_seconds"], "error": a.errorCode, "refreshing": a.fetchBusy}, "notifications": notifications, "controls": safeio.Clone(a.controls), "atmosphere": live["effects"], "preview": M{"current": preview["current"], "effects": preview["effects"], "freshness": preview["freshness"]}, "effect_status": a.effectsStatus(), "effects_setup": a.setupStatus(), "launcher_status": a.launcherStatus}
	settings := object(result["location_settings"])
	settings["country_code"], settings["place"] = a.country, nil
	if a.place != nil {
		settings["place"] = safeio.Clone(a.place)
	}
	result["place_search"] = a.searchSnapshot()
	result["saved_locations"] = a.savedLocationsSnapshot()
	result["warning_notifications"] = a.warningSnapshot()
	result["update"] = a.updateSnapshot()
	result["air_quality"] = a.airQualitySnapshot()
	result["dashboard"] = a.dashboardSnapshot()
	result["briefing"] = []any{}
	if forecast != nil && (live["freshness"] == "fresh" || live["freshness"] == "stale") {
		result["briefing"] = a.displayRows.briefing
	}
	result["alerts"] = M{"status": status, "items": display, "source": alertSource, "coverage": coverage, "fetched_at": alertFetched}
	envelope := object(result["alerts"])
	freshness := alerts["freshness"]
	if freshness == nil {
		if status == "available" {
			freshness = "current"
		} else {
			freshness = status
		}
	}
	if a.country != "US" {
		freshness = status
	}
	envelope["freshness"], envelope["refreshing"] = freshness, alerts["refreshing"] == true
	capSnapshotAlertText(result)
	return result
}

// Forecast rows are immutable after publication. Keep their normalized display
// form until a row expires, the local day changes, or the forecast is replaced.
type displayRows struct {
	source           M
	zone             string
	date             string
	builtAt, expires time.Time
	hourly, daily    []any
	briefing         []any
}

func (a *App) forecastRows(forecast M, zone *time.Location, now time.Time) ([]any, []any) {
	c := &a.displayRows
	date := now.In(zone).Format("2006-01-02")
	if c.source != nil && reflect.ValueOf(c.source).UnsafePointer() == reflect.ValueOf(a.forecast).UnsafePointer() && c.zone == zone.String() && c.date == date && !now.Before(c.builtAt) && now.Before(c.expires) {
		return c.hourly, c.daily
	}
	hourly, daily := []any{}, []any{}
	rows, _ := forecast["hourly"].([]any)
	for _, v := range rows {
		raw := object(v)
		stamp, e := weather.Instant(raw["time"])
		if e != nil || stamp.Before(now) {
			continue
		}
		row, e := weather.WeatherRecord(raw)
		if e != nil {
			continue
		}
		local := stamp.In(zone)
		start := stamp.Add(-time.Hour).In(zone)
		row["local_hour"] = strings.TrimLeft(local.Format("03 PM"), "0")
		row["local_date"] = local.Format("2006-01-02")
		row["local_label"] = local.Format("Mon Jan 02 · 03 PM MST")
		row["period_label"] = clockLabel(start) + " " + start.Format("MST") + " – " + clockLabel(local) + " " + local.Format("MST")
		hourly = append(hourly, row)
		if len(hourly) == 240 {
			break
		}
	}
	rows, _ = forecast["daily"].([]any)
	for _, v := range rows {
		row, e := weather.DailyRecord(object(v))
		if e != nil {
			continue
		}
		date, _ := time.Parse("2006-01-02", stringOf(row["date"]))
		row["day_label"] = date.Format("Mon")
		if stringOf(row["date"]) == now.In(zone).Format("2006-01-02") {
			row["day_label"] = "Today"
		}
		for _, k := range []string{"sunrise", "sunset"} {
			row[k+"_label"] = nil
			if row[k] != nil {
				stamp, e := weather.Instant(row[k])
				if e == nil {
					row[k+"_label"] = clockLabel(stamp.In(zone))
				}
			}
		}
		daily = append(daily, row)
		if len(daily) == 10 {
			break
		}
	}
	expires := now.Add(24 * time.Hour)
	local := now.In(zone)
	evening := time.Date(local.Year(), local.Month(), local.Day(), 18, 0, 0, 0, zone)
	if evening.After(now) && evening.Before(expires) {
		expires = evening
	}
	for _, value := range hourly {
		stamp, err := weather.Instant(object(value)["time"])
		if err == nil && stamp.Add(time.Nanosecond).Before(expires) {
			expires = stamp.Add(time.Nanosecond)
		}
	}
	*c = displayRows{source: a.forecast, zone: zone.String(), date: date, builtAt: now, expires: expires, hourly: hourly, daily: daily, briefing: forecastBriefing(hourly, zone, now)}
	return hourly, daily
}

// snapshotEncoded serializes the private immutable display tree directly while
// holding its ownership lock. Public Snapshot still supplies structural copies.
// The comparison excludes the monotonic revision; full adds it without a second
// traversal. Both byte slices are immutable after return.
func (a *App) snapshotEncoded() (comparison, full []byte, err error) {
	var value M
	if a.mu.TryLock() {
		defer a.mu.Unlock()
		a.poll()
		value = a.snapshotPrivateLocked()
	} else {
		a.cacheMu.RLock()
		defer a.cacheMu.RUnlock()
		value = a.cached
	}
	if value == nil {
		return nil, []byte("null"), nil
	}
	fields := make(M, len(value))
	for k, v := range value {
		if k != "snapshot_revision" {
			fields[k] = v
		}
	}
	comparison, err = json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	full = make([]byte, 0, len(comparison)+48)
	full = append(full, comparison[:len(comparison)-1]...)
	full = append(full, `,"snapshot_revision":`...)
	full = strconv.AppendFloat(full, value["snapshot_revision"].(float64), 'f', -1, 64)
	full = append(full, '}')
	return comparison, full, nil
}
