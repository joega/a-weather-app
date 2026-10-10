package app

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const (
	savedSummaryRefresh = 30 * time.Minute
	savedSummarySpacing = 10 * time.Second
	savedSummaryRetry   = 5 * time.Minute
)

// App.mu owns admission and cancellation. A canceled worker keeps the single
// slot until its callback returns; completion cannot revive a hidden/removed city.
type savedSummaryWork struct {
	id       string
	identity M
	cancel   context.CancelFunc
	canceled bool
	summary  M
	err      error
}

func validateCurrentSummary(summary M) error {
	if !savedFields(summary, "fetched_at", "valid_at", "temperature_c", "condition", "is_day") {
		return errors.New("invalid current summary fields")
	}
	for _, key := range []string{"fetched_at", "valid_at"} {
		if savedTime(summary[key]) == "" || summary[key] != savedTime(summary[key]) {
			return errors.New("invalid current summary timestamp")
		}
		if _, err := weather.Instant(summary[key]); err != nil {
			return err
		}
	}
	_, err := weather.WeatherRecord(M{"time": summary["valid_at"], "temperature_c": summary["temperature_c"], "condition": summary["condition"], "is_day": summary["is_day"]})
	if summary["temperature_c"] == nil || summary["is_day"] == nil {
		return errors.New("missing current summary conditions")
	}
	return err
}

func validateCurrentSummaryTime(summary M, now time.Time) error {
	if err := validateCurrentSummary(summary); err != nil {
		return err
	}
	for _, key := range []string{"fetched_at", "valid_at"} {
		stamp, _ := weather.Instant(summary[key])
		if stamp.After(now.Add(5*time.Minute)) || stamp.Before(now.Add(-weather.StaleSeconds*time.Second)) {
			return errors.New("current summary timestamp out of range")
		}
	}
	return nil
}

func (a *App) savedSummariesEnabled() bool {
	return !a.closed && a.presented && !a.primaryOnly && !a.options.Offline && a.options.FetchCurrentSummary != nil && a.saved != nil
}

func (a *App) savedSummaryEligible(entry M) bool {
	id := stringOf(entry["id"])
	if a.forecastPoint != nil && id == a.forecastPoint.id {
		return false
	}
	if a.primary != nil && id == a.primary.id && a.primaryNeeded() {
		return false
	}
	// Current-location fixes belong to the normal resolution path.
	return object(entry["profile"])["mode"] != "auto"
}

func (a *App) savedSummaryDue(entry M, now time.Time) time.Time {
	due := now
	if summary := a.overlaySavedSummary(entry); summary != nil {
		fetched, _ := weather.Instant(summary["fetched_at"])
		valid, _ := weather.Instant(summary["valid_at"])
		if !fetched.After(now.Add(5*time.Minute)) && !valid.After(now.Add(5*time.Minute)) {
			due = fetched.Add(savedSummaryRefresh)
			if valid.Add(savedSummaryRefresh).Before(due) {
				due = valid.Add(savedSummaryRefresh)
			}
		}
	}
	if retry := a.summaryRetry[stringOf(entry["id"])]; retry.After(due) {
		due = retry
	}
	return due
}

func (a *App) cancelSavedSummary() {
	if work := a.summaryWork; work != nil && !work.canceled {
		work.canceled = true
		work.cancel()
	}
}

func (a *App) reconcileSavedSummary() {
	if a.saved == nil {
		a.cancelSavedSummary()
		return
	}
	if work := a.summaryWork; work != nil {
		entry := savedEntry(a.saved.doc, work.id)
		if !a.savedSummariesEnabled() || entry == nil || !a.savedSummaryEligible(entry) || !reflect.DeepEqual(entry["profile"], work.identity) {
			a.cancelSavedSummary()
		}
	}
	for id := range a.summaryRetry {
		if savedEntry(a.saved.doc, id) == nil {
			delete(a.summaryRetry, id)
		}
	}
}

func (a *App) tickSavedSummaries() {
	a.reconcileSavedSummary()
	if !a.savedSummariesEnabled() || a.summaryWork != nil {
		return
	}
	now := a.options.Now()
	if now.Before(a.summaryNext) {
		return
	}
	// Foreground forecast admission always happens first. Do not initiate
	// speculative traffic while a location is resolving or weather is loading.
	if len(a.forecastJobs) != 0 || a.forecastPoint.pending != nil || a.primary.pending != nil {
		return
	}
	for _, value := range a.saved.doc["places"].([]any) {
		entry := object(value)
		if !a.savedSummaryEligible(entry) || now.Before(a.savedSummaryDue(entry, now)) {
			continue
		}
		if a.summaryDone == nil {
			a.summaryDone = make(chan *savedSummaryWork, 1)
		}
		if a.summaryRetry == nil {
			a.summaryRetry = make(map[string]time.Time)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		work := &savedSummaryWork{id: stringOf(entry["id"]), identity: safeio.Clone(object(entry["profile"])), cancel: cancel}
		a.summaryWork = work
		a.summaryNext = now.Add(savedSummarySpacing)
		a.summaryRetry[work.id] = now.Add(savedSummaryRetry)
		fetch, done, location := a.options.FetchCurrentSummary, a.summaryDone, safeio.Clone(object(work.identity["location"]))
		go func() {
			defer cancel()
			summary, err := fetch(ctx, location, now)
			if err == nil {
				err = validateCurrentSummaryTime(summary, now)
			}
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			if err == nil {
				summary = safeio.Clone(summary)
			}
			work.summary, work.err = summary, err
			done <- work
			a.signal()
		}()
		return
	}
}

func (a *App) pollSavedSummaries() {
	select {
	case work := <-a.summaryDone:
		if a.summaryWork != work {
			return
		}
		a.summaryWork = nil
		entry := savedEntry(a.saved.doc, work.id)
		if !work.canceled && work.err == nil && a.savedSummariesEnabled() && entry != nil && a.savedSummaryEligible(entry) && reflect.DeepEqual(entry["profile"], work.identity) && validateCurrentSummaryTime(work.summary, a.options.Now()) == nil {
			// A full forecast may have arrived during this request. Never replace
			// newer weather or refresh independent alert metadata.
			old := a.overlaySavedSummary(entry)
			newValid, _ := weather.Instant(work.summary["valid_at"])
			oldValid, _ := weather.Instant(old["valid_at"])
			newFetched, _ := weather.Instant(work.summary["fetched_at"])
			oldFetched, _ := weather.Instant(old["fetched_at"])
			futureLimit := a.options.Now().Add(5 * time.Minute)
			if old == nil || oldValid.After(futureLimit) || oldFetched.After(futureLimit) || (!newValid.Before(oldValid) && !newFetched.Before(oldFetched)) {
				a.savedSummaries[work.id] = savedCurrentSummary{identity: work.identity, summary: work.summary}
				a.savedList.items = nil
				// Failed durability leaves a usable in-memory card, never marks a
				// full forecast as saved or drops last-good data.
				_ = a.persistSavedSummaries()
			}
			a.summaryRetry[work.id] = a.options.Now().Add(savedSummaryRefresh)
		}
	default:
	}
}

func (a *App) savedSummaryInterval(now time.Time) time.Duration {
	if !a.savedSummariesEnabled() || a.summaryWork != nil || len(a.forecastJobs) != 0 {
		return time.Minute
	}
	delay := time.Minute
	for _, value := range a.saved.doc["places"].([]any) {
		entry := object(value)
		if !a.savedSummaryEligible(entry) {
			continue
		}
		due := a.savedSummaryDue(entry, now)
		if a.summaryNext.After(due) {
			due = a.summaryNext
		}
		delay = min(delay, max(time.Second, due.Sub(now)))
	}
	return delay
}
