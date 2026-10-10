package app

import (
	"errors"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

const savedCurrentSummariesFile = "saved-current-summaries.json"

// Compact current conditions have an independent cache. The forecast manifest's
// summary remains bound to its immutable full-forecast slot for integrity checks.
type savedCurrentSummary struct {
	identity M
	summary  M
}

func (a *App) initSavedSummaries() {
	a.savedSummaries = make(map[string]savedCurrentSummary)
	if a.saved == nil {
		return
	}
	doc, err := a.saved.files.Read(savedCurrentSummariesFile, savedMetadataBytes)
	if err != nil || !savedFields(doc, "schema_version", "items") || doc["schema_version"] != 1.0 {
		return
	}
	items, ok := doc["items"].([]any)
	if !ok || len(items) > savedLocationLimit {
		return
	}
	loaded := make(map[string]savedCurrentSummary)
	seen := make(map[string]bool)
	for _, value := range items {
		row := object(value)
		id, ok := row["id"].(string)
		identity, summary := object(row["identity"]), object(row["summary"])
		identityID, identityErr := savedLocationID(identity)
		if !savedFields(row, "id", "identity", "summary") || !ok || seen[id] || identityErr != nil || identityID != id || !reflect.DeepEqual(identity, savedProfileIdentity(identity)) || validateCurrentSummary(summary) != nil {
			return
		}
		seen[id] = true
		entry := savedEntry(a.saved.doc, id)
		if entry == nil || !reflect.DeepEqual(identity, savedProfileIdentity(object(entry["profile"]))) {
			continue
		}
		loaded[id] = savedCurrentSummary{identity: weather.Clone(identity).(M), summary: weather.Clone(summary).(M)}
	}
	a.savedSummaries = loaded
}

// Called under App.mu. Rows are serialized in authoritative saved-list order;
// removed or relocated identities cannot survive a subsequent publication.
func (a *App) persistSavedSummaries() error {
	if a.saved == nil {
		return errors.New("saved locations unavailable")
	}
	for id, record := range a.savedSummaries {
		entry := savedEntry(a.saved.doc, id)
		if entry == nil || !reflect.DeepEqual(record.identity, savedProfileIdentity(object(entry["profile"]))) {
			delete(a.savedSummaries, id)
		}
	}
	items := []any{}
	for _, value := range a.saved.doc["places"].([]any) {
		entry := object(value)
		id := stringOf(entry["id"])
		record, ok := a.savedSummaries[id]
		if !ok {
			continue
		}
		if err := validateCurrentSummary(record.summary); err != nil {
			return err
		}
		items = append(items, M{"id": id, "identity": record.identity, "summary": record.summary})
	}
	return a.saved.files.Write(savedCurrentSummariesFile, M{"schema_version": 1.0, "items": items}, savedMetadataBytes)
}

func summaryAtLeastAsRecent(current, previous M) bool {
	if previous == nil {
		return true
	}
	newer := false
	for _, field := range []string{"fetched_at", "valid_at"} {
		currentTime, currentErr := weather.Instant(current[field])
		previousTime, previousErr := weather.Instant(previous[field])
		if currentErr != nil || previousErr != nil || currentTime.Before(previousTime) {
			return false
		}
		newer = newer || currentTime.After(previousTime)
	}
	return newer
}

func savedSummaryFromFuture(summary M, now time.Time) bool {
	for _, field := range []string{"fetched_at", "valid_at"} {
		stamp, err := weather.Instant(summary[field])
		if err == nil && stamp.After(now.Add(5*time.Minute)) {
			return true
		}
	}
	return false
}

// This only combines small immutable metadata; it never opens a forecast slot
// or changes the independent age of official-alert information.
func (a *App) overlaySavedSummary(entry M) M {
	previous := object(entry["summary"])
	record, ok := a.savedSummaries[stringOf(entry["id"])]
	if !ok {
		return previous
	}
	// Manifest profiles already contain identity only, with forecast == nil.
	identity := object(entry["profile"])
	now := a.options.Now()
	if !reflect.DeepEqual(record.identity, identity) || savedSummaryFromFuture(record.summary, now) || (!savedSummaryFromFuture(previous, now) && !summaryAtLeastAsRecent(record.summary, previous)) {
		return previous
	}
	summary := weather.Clone(record.summary).(M)
	summary["alert_expires"], summary["alert_fetched_at"], summary["alert_status"] = nil, nil, "unavailable"
	if country := identity["country_code"]; country != nil && country != "US" {
		summary["alert_status"] = "not_supported_here"
	}
	if previous != nil {
		for _, field := range []string{"alert_expires", "alert_fetched_at", "alert_status"} {
			summary[field] = previous[field]
		}
	}
	return summary
}
