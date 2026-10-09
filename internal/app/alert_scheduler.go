package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const (
	alertRequestSpacing = 30 * time.Second
	alertMonitorCadence = 2 * time.Minute
	alertCycleBudget    = 5 * time.Minute
	alertHistoryPages   = 3
)

type alertPageFetcher func(context.Context, weather.AlertMessageQuery, time.Time) (weather.AlertMessagePage, error)
type alertDemand struct {
	key      string
	location M
	monitor  bool
}
type alertCycle struct {
	started, since time.Time
	phase          string
	cursor         string
	cursors        map[string]bool
	pages          int
	messages       []weather.AlertMessage
	identities     map[string]weather.AlertMessage
	incomplete     string
}
type alertEntry struct {
	demand                      alertDemand
	generation                  uint64
	next, lastActive, watermark time.Time
	cycle                       *alertCycle
	failures                    int
	seeded                      bool
	completed                   uint64
}
type alertJob struct {
	entry           *alertEntry
	generation      uint64
	query           weather.AlertMessageQuery
	started         time.Time
	ctx             context.Context
	cancel          context.CancelFunc
	timeoutReported bool
}
type alertJobResult struct {
	job      *alertJob
	page     weather.AlertMessagePage
	err      error
	finished time.Time
}

// alertObservation transfers one current feed and/or assembled warning batch.
// err describes monitoring failure, not an empty current feed. A display feed
// can remain valid while history is incomplete. No UI or delivery lives here.
type alertObservation struct {
	key          string
	active       M
	batch        *notifications.WarningBatch
	err          string
	activeFailed bool
}

// alertScheduler is serialized by its owner. It shares at most two geographic
// demands, one of which may monitor warnings. There is exactly one provider
// callback in flight, retained even after cancellation, and one result slot.
// Every request start is spaced by at least 30 seconds across both points and
// all pages. Idle/off schedulers own no worker or timer. Current-feed requests
// never depend on forecast/map workers. The owner supplies a completion wakeup.
type alertScheduler struct {
	fetch  alertPageFetcher
	signal func()
	// Production supplies its clock; nil keeps deterministic fixture responses
	// at their request timestamp. Completion is recorded before queueing.
	finishedNow           func() time.Time
	entries               map[string]*alertEntry
	job                   *alertJob
	done                  chan alertJobResult
	nextRequest, lastTick time.Time
	closed                bool
}

func newAlertScheduler(fetch alertPageFetcher, signal func()) *alertScheduler {
	return &alertScheduler{fetch: fetch, signal: signal, entries: map[string]*alertEntry{}, done: make(chan alertJobResult, 1)}
}

func (s *alertScheduler) setDemands(demands []alertDemand, now time.Time) error {
	if s.closed {
		return errors.New("alert scheduler closed")
	}
	if len(demands) > 2 {
		return errors.New("too many alert demands")
	}
	unchanged := len(demands) == len(s.entries) && (len(demands) != 2 || demands[0].key != demands[1].key)
	if unchanged {
		for _, d := range demands {
			e := s.entries[d.key]
			if e == nil || e.demand.monitor != d.monitor || !sameAlertLocation(d.location, e.demand.location) {
				unchanged = false
				break
			}
		}
		if unchanged {
			return nil
		}
	}
	validated := make(map[string]alertDemand, len(demands))
	for _, d := range demands {
		if known := s.entries[d.key]; known != nil && sameAlertLocation(d.location, known.demand.location) {
			d.location = known.demand.location
			if prior, ok := validated[d.key]; ok {
				d.monitor = d.monitor || prior.monitor
			}
			validated[d.key] = d
			continue
		}
		loc, err := weather.ValidateLocation(d.location)
		if err != nil {
			return err
		}
		key, err := notifications.WarningLocationKey(loc)
		if err != nil || key != d.key {
			return errors.New("invalid alert demand key")
		}
		d.location = loc
		if prior, ok := validated[key]; ok {
			d.monitor = d.monitor || prior.monitor
		}
		validated[key] = d
	}
	monitors := 0
	for _, d := range validated {
		if d.monitor {
			monitors++
		}
	}
	if monitors > 1 {
		return errors.New("multiple warning targets")
	}
	for key, e := range s.entries {
		d, keep := validated[key]
		if !keep {
			if s.job != nil && s.job.entry == e {
				s.job.cancel()
			}
			delete(s.entries, key)
			continue
		}
		if e.demand.monitor != d.monitor {
			e.generation++
			e.cycle = nil
			e.next = now
			if s.job != nil && s.job.entry == e {
				s.job.cancel()
			}
		}
		e.demand = d
	}
	for key, d := range validated {
		if s.entries[key] == nil {
			s.entries[key] = &alertEntry{demand: d, next: now}
		}
	}
	return nil
}

func sameAlertLocation(a, b M) bool {
	lat, latOK := a["latitude"].(float64)
	lon, lonOK := a["longitude"].(float64)
	return len(a) == 4 && latOK && lonOK && lat == b["latitude"] && lon == b["longitude"] && stringOf(a["name"]) == b["name"] && stringOf(a["timezone"]) == b["timezone"]
}

func (s *alertScheduler) close() {
	if s.closed {
		return
	}
	s.closed = true
	if s.job != nil {
		s.job.cancel()
	}
	s.entries = map[string]*alertEntry{}
}

func (s *alertScheduler) resetCycle(e *alertEntry, now time.Time) {
	e.generation++
	e.cycle = nil
	e.next = now
	if s.job != nil && s.job.entry == e {
		s.job.cancel()
	}
}

// tick drains completed work before admitting another call. Forward clock gaps
// discard incomplete assemblies; backward jumps rebase the request throttle
// and invalidate pending replies rather than making future-dated data fresh.
func (s *alertScheduler) tick(now time.Time) []alertObservation {
	if s.closed || s.fetch == nil {
		return nil
	}
	if !s.lastTick.IsZero() && now.Before(s.lastTick) {
		s.nextRequest = now.Add(alertRequestSpacing)
		for _, e := range s.entries {
			s.resetCycle(e, now)
		}
	} else if !s.lastTick.IsZero() && now.Sub(s.lastTick) > alertCycleBudget {
		for _, e := range s.entries {
			s.resetCycle(e, now)
		}
	}
	s.lastTick = now
	for _, e := range s.entries {
		if e.cycle != nil && now.Sub(e.cycle.started) > alertCycleBudget {
			s.resetCycle(e, now)
		}
	}
	var out []alertObservation
	select {
	case result := <-s.done:
		if result.job == s.job {
			s.job = nil
		}
		e := result.job.entry
		if s.entries[e.demand.key] == e && e.generation == result.job.generation {
			out = s.accept(result, now)
		}
		result.job.cancel()
	default:
	}
	if job := s.job; job != nil && job.ctx.Err() == context.DeadlineExceeded && !job.timeoutReported {
		job.timeoutReported = true
		e := job.entry
		if s.entries[e.demand.key] == e && e.generation == job.generation {
			e.generation++
			out = append(out, s.fail(e, now, "fetch_timeout")...)
		}
	}
	if s.job != nil || now.Before(s.nextRequest) {
		return out
	}
	var due []*alertEntry
	for _, e := range s.entries {
		if !now.Before(e.next) {
			due = append(due, e)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		// The oldest pending demand wins, so a viewed city cannot starve behind
		// repeated primary history requests, or vice versa.
		if !due[i].next.Equal(due[j].next) {
			return due[i].next.Before(due[j].next)
		}
		if due[i].demand.monitor != due[j].demand.monitor {
			return due[i].demand.monitor
		}
		return due[i].demand.key < due[j].demand.key
	})
	if len(due) == 0 {
		return out
	}
	e := due[0]
	q := weather.AlertMessageQuery{Location: safeio.Clone(e.demand.location), CountryCode: "US", Active: true}
	if e.demand.monitor {
		if e.cycle == nil {
			// Leave room for spaced pages inside NWS's seven-day query bound.
			// The watermark is cycle start, never its later completion time.
			since := e.watermark.Add(-2 * time.Minute)
			floor := now.Add(-7*24*time.Hour + alertCycleBudget)
			if e.watermark.IsZero() || since.Before(floor) || since.After(now) {
				since = floor
			}
			phase := "history"
			if e.lastActive.IsZero() || now.Sub(e.lastActive) > alertCycleBudget || e.lastActive.After(now) {
				phase = "initial"
			}
			e.cycle = &alertCycle{started: now, since: since, phase: phase, cursors: map[string]bool{}, messages: []weather.AlertMessage{}, identities: map[string]weather.AlertMessage{}}
		}
		if e.cycle.phase == "history" {
			q.Active = false
			q.Since = e.cycle.since
			q.Cursor = e.cycle.cursor
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	job := &alertJob{entry: e, generation: e.generation, query: q, started: now, ctx: ctx, cancel: cancel}
	s.job = job
	s.nextRequest = now.Add(alertRequestSpacing)
	go func() {
		page, err := s.fetch(ctx, q, now)
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		// A canceled callback still owns this slot until it returns. The
		// buffered result is safe even if the owner has already closed.
		finished := now
		if s.finishedNow != nil {
			finished = s.finishedNow()
		}
		s.done <- alertJobResult{job: job, page: page, err: err, finished: finished}
		cancel()
		if s.signal != nil {
			s.signal()
		}
	}()
	return out
}

func (s *alertScheduler) fail(e *alertEntry, now time.Time, reason string) []alertObservation {
	activeFailed := e.cycle == nil || e.cycle.phase != "history"
	e.failures++
	delay := time.Minute
	for i := 1; i < e.failures && delay < 15*time.Minute; i++ {
		delay *= 2
	}
	if delay > 15*time.Minute || !e.demand.monitor {
		delay = 15 * time.Minute
	}
	e.next = now.Add(delay)
	e.cycle = nil
	v := alertObservation{key: e.demand.key, err: reason, activeFailed: activeFailed}
	if e.demand.monitor {
		v.batch = &notifications.WarningBatch{Location: e.demand.key, FetchedAt: now, Complete: false}
	}
	return []alertObservation{v}
}

func validAlertCursor(cursor string) bool {
	if len(cursor) > 2048 || !utf8.ValidString(cursor) || strings.TrimSpace(cursor) != cursor {
		return false
	}
	for _, r := range cursor {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}

func normalizeAlertPage(page weather.AlertMessagePage, job *alertJob, now time.Time) (weather.AlertMessagePage, error) {
	if !page.FetchedAt.Equal(job.started) || page.FetchedAt.After(now) || now.Sub(page.FetchedAt) > alertCycleBudget || len(page.Messages) > weather.AlertMessageLimit || page.Complete != (page.NextCursor == "") || job.query.Active && !page.Complete || !validAlertCursor(page.NextCursor) || page.NextCursor != "" && page.NextCursor == job.query.Cursor {
		return weather.AlertMessagePage{}, errors.New("invalid alert page")
	}
	messages := make([]weather.AlertMessage, 0, len(page.Messages))
	seen := map[string]weather.AlertMessage{}
	bytes := 0
	for _, input := range page.Messages {
		// Bound retained source text before any normalized/hash/JSON copy.
		for _, v := range []string{input.Identity.ID, input.Identity.Sender, input.Issuer, input.Event, input.Area, input.Headline, input.Description, input.Instruction, input.Severity, input.Urgency, input.Certainty} {
			bytes += len(v)
		}
		if len(input.References) > weather.AlertReferenceLimit {
			return weather.AlertMessagePage{}, errors.New("alert reference limit")
		}
		for _, r := range input.References {
			bytes += len(r.ID) + len(r.Sender)
		}
		if bytes > 2<<20 {
			return weather.AlertMessagePage{}, errors.New("alert page size")
		}
		m, err := weather.NormalizeAlertMessage(input)
		if err != nil || m.Identity.Sent.After(now) {
			return weather.AlertMessagePage{}, errors.New("invalid alert message")
		}
		key := m.Identity.Key()
		if prior, ok := seen[key]; ok {
			if !reflect.DeepEqual(prior, m) {
				return weather.AlertMessagePage{}, errors.New("conflicting alert page")
			}
			continue
		}
		seen[key] = m
		messages = append(messages, m)
	}
	raw, err := json.Marshal(messages)
	if err != nil || len(raw) > 2<<20 {
		return weather.AlertMessagePage{}, errors.New("alert page size")
	}
	page.Messages = messages
	return page, nil
}

func alertPageView(page weather.AlertMessagePage, now time.Time) M {
	superseded := map[string]bool{}
	for _, m := range page.Messages {
		if m.Type == "Update" || m.Type == "Cancel" {
			for _, r := range m.References {
				superseded[r.Key()] = true
			}
		}
	}
	items := []any{}
	for _, m := range page.Messages {
		if m.Type == "Cancel" || superseded[m.Identity.Key()] || m.Effective.After(now) || !m.Expires.After(now) {
			continue
		}
		items = append(items, M{"id": m.Identity.ID, "source": "National Weather Service", "event": m.Event, "headline": m.Headline, "severity": m.Severity, "urgency": m.Urgency, "description": m.Description, "instruction": m.Instruction, "effective": m.Effective.UTC().Format(time.RFC3339Nano), "expires": m.Expires.UTC().Format(time.RFC3339Nano)})
	}
	return M{"status": "available", "items": items, "fetched_at": page.FetchedAt.UTC().Format(time.RFC3339Nano), "source": "National Weather Service", "coverage": "US", "freshness": "current", "refreshing": false}
}

func (s *alertScheduler) accept(result alertJobResult, now time.Time) []alertObservation {
	e := result.job.entry
	e.completed++
	if result.err != nil {
		return s.fail(e, now, "fetch_failed")
	}
	if result.finished.Before(result.job.started) || result.finished.After(now) || now.Sub(result.finished) > alertCycleBudget {
		return s.fail(e, now, "stale_response")
	}
	page, err := normalizeAlertPage(result.page, result.job, result.finished)
	if err != nil {
		return s.fail(e, now, "invalid_feed")
	}
	page.FetchedAt = result.finished.UTC()
	if !e.demand.monitor {
		e.failures = 0
		e.lastActive = page.FetchedAt
		e.next = now.Add(weather.RefreshSeconds * time.Second)
		return []alertObservation{{key: e.demand.key, active: alertPageView(page, now)}}
	}
	cycle := e.cycle
	if cycle == nil {
		return s.fail(e, now, "incomplete_history")
	}
	// Yield to an equally old demand even when a fixture/coarse clock reports
	// request start and completion in the same instant.
	e.next = now.Add(time.Nanosecond)
	if cycle.phase == "initial" {
		e.lastActive = page.FetchedAt
		cycle.phase = "history"
		return []alertObservation{{key: e.demand.key, active: alertPageView(page, now)}}
	}
	for _, m := range page.Messages {
		key := m.Identity.Key()
		if prior, ok := cycle.identities[key]; ok {
			if !reflect.DeepEqual(prior, m) {
				return s.fail(e, now, "conflicting_history")
			}
			continue
		}
		cycle.identities[key] = m
		cycle.messages = append(cycle.messages, m)
	}
	if cycle.phase == "history" {
		cycle.pages++
		if !page.Complete {
			if cycle.cursors[page.NextCursor] {
				return s.fail(e, now, "repeated_cursor")
			}
			cycle.cursors[page.NextCursor] = true
			cycle.cursor = page.NextCursor
			if cycle.pages < alertHistoryPages {
				return nil
			}
			cycle.incomplete = "history_limit"
		}
		cycle.phase = "final"
		return nil
	}
	current := []string{}
	for _, m := range page.Messages {
		if m.Type != "Cancel" {
			current = append(current, m.Identity.Key())
		}
	}
	batch := &notifications.WarningBatch{Location: e.demand.key, Messages: cycle.messages, FetchedAt: page.FetchedAt, Complete: cycle.incomplete == "", CurrentKeys: current}
	v := alertObservation{key: e.demand.key, active: alertPageView(page, now), batch: batch, err: cycle.incomplete}
	e.lastActive = page.FetchedAt
	if cycle.incomplete == "" {
		e.watermark = cycle.started
		e.failures = 0
	}
	e.cycle = nil
	e.next = now.Add(alertMonitorCadence)
	return []alertObservation{v}
}

func (s *alertScheduler) interval(now time.Time) time.Duration {
	if s == nil || s.closed || len(s.entries) == 0 {
		return time.Hour
	}
	if !s.lastTick.IsZero() && now.Before(s.lastTick) {
		return time.Nanosecond
	}
	if s.job != nil {
		if s.job.ctx.Err() != nil {
			return time.Minute
		}
		return time.Second
	}
	next := now.Add(time.Hour)
	for _, e := range s.entries {
		if e.next.Before(next) {
			next = e.next
		}
	}
	if next.Before(s.nextRequest) {
		next = s.nextRequest
	}
	if !next.After(now) {
		return time.Nanosecond
	}
	return next.Sub(now)
}
