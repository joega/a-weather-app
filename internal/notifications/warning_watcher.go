package notifications

import (
	"context"
	"errors"
	"math"
	"os"
	"sort"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const warningDeliverySpacing = 10 * time.Second
const warningDetailLimit = 16
const warningDetailBytes = 2 << 20
const warningDetailRetention = 24 * time.Hour

// WarningSender reports daemon acceptance independently of any later click.
// It must honor cancellation. The watcher retains its only slot until return,
// even after a timeout, and never retries an uncertain or failed reservation.
type WarningSender func(context.Context, WarningNotice) error

type warningDeliveryJob struct {
	receipt WarningReceipt
	message weather.AlertMessage
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan error
}

type warningDetailRecord struct {
	notice  WarningNotice
	message weather.AlertMessage
	created time.Time
	bytes   int
}

// WarningWatcher owns opt-in policy, one primary target, bounded source bodies,
// the durable ledger and one delivery worker. All methods are serialized by
// its owner. It does no provider polling and creates no idle goroutine/timer.
// Tick must run on target/policy changes and before delivering observations.
type WarningWatcher struct {
	state                        *safeio.Directory
	files                        warningFiles
	document                     M
	lock                         *os.File
	ledger                       *WarningLedger
	send                         WarningSender
	signal                       func()
	closed, dirty                bool
	closeErr                     error
	fault, mode, reason          string
	delivery                     string
	key, name, zoneName, country string
	latitude, longitude          float64
	zone                         *time.Location
	targetReady                  bool
	deliveryReady                bool
	bodies                       map[string]weather.AlertMessage
	current                      map[string]bool
	fetched                      time.Time
	complete                     bool
	observationError             string
	job                          *warningDeliveryJob
	nextDelivery, nextEvaluation time.Time
	lastTick                     time.Time
	lastNotice                   *WarningNotice
	details                      []warningDetailRecord
	detailBytes                  int
}

// NewWarningWatcher reads small private policy state. Disabled startup opens
// no ledger or lock. Nil sender explicitly means delivery is unsupported; the
// platform adapter is supplied by the owner, not discovered or launched here.
// The optional signal callback runs on workers and must be safe/nonblocking.
func NewWarningWatcher(state *safeio.Directory, sender WarningSender, signal func()) *WarningWatcher {
	w := &WarningWatcher{state: state, files: state, document: defaultWarningSettings(), send: sender, signal: signal, mode: "off", delivery: "none", dirty: true, deliveryReady: sender != nil}
	v, err := w.readSettings()
	if err == nil {
		w.document = v
		if w.Enabled() {
			err = w.acquire()
			if err == nil && w.Enabled() && sender != nil {
				w.ledger, err = OpenWarningLedger(state)
			}
		}
	}
	if err != nil {
		w.fail("state_unavailable")
	}
	w.releaseIdle()
	return w
}

func (w *WarningWatcher) readSettings() (M, error) {
	v, err := w.files.Read(warningSettingsFile, warningSettingsLimit)
	if err == nil && v == nil {
		return defaultWarningSettings(), nil
	}
	if err == nil {
		err = validateWarningSettings(v)
	}
	return v, err
}

func (w *WarningWatcher) acquire() error {
	if w.lock != nil {
		return nil
	}
	lock, err := w.state.Lock("warning-notifications.lock")
	if err != nil {
		return err
	}
	w.lock = lock
	v, err := w.readSettings()
	if err == nil {
		w.document = v
	}
	return err
}

func (w *WarningWatcher) fail(reason string) {
	w.fault, w.mode, w.reason, w.dirty = reason, "unavailable", reason, true
	w.cancelDelivery()
	w.clearObservation()
}

func (w *WarningWatcher) clearObservation() {
	w.bodies, w.current = nil, nil
	w.fetched, w.complete, w.observationError = time.Time{}, false, ""
	w.dirty = true
}

func (w *WarningWatcher) releaseIdle() {
	if w.Enabled() || w.job != nil {
		return
	}
	if w.ledger != nil {
		w.ledger.Close()
		w.ledger = nil
	}
	if w.lock != nil {
		w.lock.Close()
		w.lock = nil
	}
}

// Enabled is the saved opt-in. WantsFeed also accounts for delivery ownership,
// target coverage and availability; a failed/unsupported watcher does not poll.
func (w *WarningWatcher) Enabled() bool { return w.document["settings"].(M)["enabled"] == true }
func (w *WarningWatcher) DeliveryRequested() bool {
	return !w.closed && w.Enabled() && w.fault == "" && w.send != nil
}
func (w *WarningWatcher) WantsFeed() bool {
	return w.DeliveryRequested() && w.deliveryReady && w.ledger != nil && w.targetReady
}

// SetDeliveryReady tracks transient native availability separately from saved
// opt-in. Losing the daemon cancels work and requires a fresh observation when
// it returns; an unavailable adapter must not cause background history polling.
func (w *WarningWatcher) SetDeliveryReady(ready bool) {
	if w.deliveryReady == ready {
		return
	}
	w.deliveryReady = ready
	w.cancelDelivery()
	w.clearObservation()
}

func (w *WarningWatcher) cancelDelivery() {
	if w.job != nil {
		w.job.cancel()
	}
}

// Configure durably changes policy before admitting new delivery. Every policy
// change cancels an existing attempt. A write failure stops this instance; an
// explicit disable is still permitted when valid policy state remains readable.
func (w *WarningWatcher) Configure(patch M) error {
	disabling := len(patch) == 1 && patch["enabled"] == false
	if w.closed || w.fault != "" && !disabling {
		return errors.New("warning watcher unavailable")
	}
	if _, err := PatchWarnings(w.document["settings"].(M), patch); err != nil {
		return err
	}
	if err := w.acquire(); err != nil {
		w.fail("state_unavailable")
		w.releaseIdle()
		return err
	}
	v, err := w.readSettings()
	if err != nil {
		w.fail("state_unavailable")
		return err
	}
	settings, err := PatchWarnings(v["settings"].(M), patch)
	if err != nil {
		w.releaseIdle()
		return err
	}
	if settings["enabled"] == true && w.send == nil {
		w.releaseIdle()
		return errors.New("warning delivery unsupported")
	}
	v["settings"] = settings
	if settings["enabled"] == false {
		v["paused_until"] = nil
	}
	if err := w.files.Write(warningSettingsFile, v, warningSettingsLimit); err != nil {
		w.fail("save_unconfirmed")
		return err
	}
	w.document, w.dirty = v, true
	w.cancelDelivery()
	if !w.Enabled() {
		w.clearObservation()
		w.fault, w.mode, w.reason = "", "off", ""
		w.lastNotice, w.details, w.detailBytes = nil, nil, 0
		w.releaseIdle()
		return nil
	}
	if w.ledger == nil {
		w.ledger, err = OpenWarningLedger(w.state)
		if err != nil {
			w.fail("ledger_unavailable")
			return err
		}
	}
	return nil
}

// Pause stops delivery for one hour, including urgent interruptions. Feed
// monitoring continues so current conditions are ready when delivery resumes.
func (w *WarningWatcher) Pause(now time.Time, resume bool) error {
	if !warningNow(now) || w.closed || !w.Enabled() || w.fault != "" || w.lock == nil {
		return errors.New("warning watcher unavailable")
	}
	v := safeio.Clone(w.document)
	v["paused_until"] = nil
	if !resume {
		v["paused_until"] = float64(now.Unix() + 3600)
	}
	if err := validateWarningSettings(v); err != nil {
		return err
	}
	if err := w.files.Write(warningSettingsFile, v, warningSettingsLimit); err != nil {
		w.fail("save_unconfirmed")
		return err
	}
	w.document, w.dirty = v, true
	if !resume {
		w.cancelDelivery()
	}
	return nil
}

// Tick updates the primary target independently of any forecast. available is
// false while offline or resolving a new primary selection. Source/policy work
// is skipped until a deadline or relevant input changes. Disabled ticks are O(1).
func (w *WarningWatcher) Tick(location M, country string, available bool, now time.Time) {
	if w.closed {
		return
	}
	if !w.lastTick.IsZero() && now.Before(w.lastTick) {
		w.cancelDelivery()
		w.clearObservation()
		w.nextDelivery = now.Add(warningDeliverySpacing)
	}
	w.lastTick = now
	w.pruneDetails(now)
	if !w.Enabled() || w.fault != "" {
		w.cancelDelivery()
		w.collect(now)
		w.releaseIdle()
		if w.dirty || w.nextEvaluation.IsZero() || !now.Before(w.nextEvaluation) {
			w.evaluate(now)
		}
		return
	}
	lat, latOK := location["latitude"].(float64)
	lon, lonOK := location["longitude"].(float64)
	name, zoneName := str(location["name"]), str(location["timezone"])
	ready := available && country == "US" && latOK && lonOK
	if ready != w.targetReady || country != w.country || lat != w.latitude || lon != w.longitude || name != w.name || zoneName != w.zoneName {
		w.cancelDelivery()
		w.clearObservation()
		w.key = ""
		w.country, w.latitude, w.longitude, w.name, w.zoneName = country, lat, lon, name, zoneName
		w.targetReady = false
		if ready {
			key, err := WarningLocationKey(location)
			zone, zoneErr := time.LoadLocation(zoneName)
			if err == nil && zoneErr == nil {
				w.key, w.zone, w.targetReady = key, zone, true
			}
		}
	}
	w.collect(now)
	if w.job != nil && (!w.WantsFeed() || !w.deliveryAllowed(w.job.receipt.Decision, w.job.message, now)) {
		w.cancelDelivery()
	}
	if w.dirty || w.nextEvaluation.IsZero() || !now.Before(w.nextEvaluation) {
		w.evaluate(now)
	}
}

// Observe replaces bounded source bodies only after successful reconciliation.
// Wrong-target callbacks are discarded. Incomplete/error observations suppress
// delivery immediately; history absence never synthesizes a cancellation.
func (w *WarningWatcher) Observe(batch WarningBatch, reason string, now time.Time) {
	if !w.WantsFeed() || batch.Location != w.key {
		return
	}
	if err := w.ledger.Reconcile(batch, now); err != nil {
		w.cancelDelivery()
		w.clearObservation()
		w.observationError = "invalid_observation"
		if errors.Is(err, ErrWarningLedgerWrite) {
			w.fail("save_unconfirmed")
		} else if errors.Is(err, ErrWarningLedgerCapacity) {
			w.observationError = "capacity_reached"
		} else if errors.Is(err, ErrWarningClock) {
			w.observationError = "clock_changed"
		}
		w.evaluate(now)
		return
	}
	w.clearObservation()
	w.fetched, w.complete, w.observationError = batch.FetchedAt, batch.Complete, reason
	w.bodies = make(map[string]weather.AlertMessage, len(batch.Messages))
	for _, input := range batch.Messages {
		m, _ := weather.NormalizeAlertMessage(input) // Reconcile validated every input.
		w.bodies[m.Identity.Key()] = m
	}
	w.current = make(map[string]bool, len(batch.CurrentKeys))
	for _, key := range batch.CurrentKeys {
		w.current[key] = true
	}
	if w.job != nil && !w.attemptCurrent(w.job.receipt.Decision, now) {
		w.cancelDelivery()
	}
	w.evaluate(now)
}

func (w *WarningWatcher) fresh(now time.Time) bool {
	return w.complete && w.observationError == "" && !w.fetched.IsZero() && !w.fetched.After(now) && now.Sub(w.fetched) <= warningFreshness
}

func (w *WarningWatcher) paused(now time.Time) bool {
	until, ok := w.document["paused_until"].(float64)
	return ok && float64(now.Unix()) < until
}

func (w *WarningWatcher) deliveryAllowed(d WarningDecision, m weather.AlertMessage, now time.Time) bool {
	return w.WantsFeed() && w.fresh(now) && !w.paused(now) && warningAllowed(w.document["settings"].(M), d, m, now, w.zone) && w.attemptCurrent(d, now)
}

func (w *WarningWatcher) attemptCurrent(d WarningDecision, now time.Time) bool {
	if d.Location != w.key || !w.fresh(now) {
		return false
	}
	v := w.ledger.graph[warningID(d.Location, d.Key)]
	if v == nil || v.record == nil || len(v.children) != 0 || !v.record.Seen.Equal(w.fetched) {
		return false
	}
	if d.Kind == "canceled" {
		return v.record.Type == "Cancel" && now.Sub(v.record.Sent) <= warningCancelAge && !warningHasLiveBranch(v, now)
	}
	return w.current[d.Key] && !v.record.Effective.After(now) && v.record.Expires.After(now)
}

func (w *WarningWatcher) collect(now time.Time) {
	job := w.job
	if job == nil {
		return
	}
	select {
	case err := <-job.done:
		status := "sent"
		if err != nil {
			status = "failed"
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				status = "uncertain"
			}
		}
		job.cancel()
		w.job, w.delivery, w.dirty = nil, status, true
		if err := w.ledger.Finish(job.receipt, status, now); err != nil {
			w.delivery = "uncertain"
			reason := "save_unconfirmed"
			if errors.Is(err, ErrWarningClock) {
				reason = "clock_changed"
			}
			w.fail(reason)
		}
	default:
		if job.ctx.Err() != nil {
			w.delivery = "uncertain"
		}
	}
}

func (w *WarningWatcher) evaluate(now time.Time) {
	w.dirty = false
	w.nextEvaluation = now.Add(time.Hour)
	if len(w.details) > 0 {
		w.nextEvaluation = earlier(w.nextEvaluation, w.details[0].created.Add(warningDetailRetention))
	}
	if w.closed {
		w.mode, w.reason = "off", ""
		return
	}
	if w.fault != "" {
		w.mode, w.reason = "unavailable", w.fault
		return
	}
	if !w.Enabled() {
		w.mode, w.reason = "off", ""
		return
	}
	if w.send == nil {
		w.mode, w.reason = "unavailable", "delivery_unsupported"
		return
	}
	if !w.deliveryReady {
		w.mode, w.reason = "unavailable", "delivery_unavailable"
		return
	}
	if !w.targetReady {
		w.mode, w.reason = "unavailable", "target_unavailable"
		if w.country != "US" {
			w.reason = "not_supported_here"
		}
		return
	}
	settings := w.document["settings"].(M)
	w.nextEvaluation = earlier(w.nextEvaluation, nextQuietTransition(settings, now, w.zone))
	if !w.fetched.IsZero() && !now.After(w.fetched.Add(warningFreshness)) {
		w.nextEvaluation = earlier(w.nextEvaluation, w.fetched.Add(warningFreshness+time.Nanosecond))
	}
	for _, m := range w.bodies {
		if m.Effective.After(now) {
			w.nextEvaluation = earlier(w.nextEvaluation, m.Effective)
		}
		if m.Expires.After(now) {
			w.nextEvaluation = earlier(w.nextEvaluation, m.Expires)
		}
	}
	if w.paused(now) {
		w.mode, w.reason = "paused", ""
		w.nextEvaluation = earlier(w.nextEvaluation, time.Unix(int64(math.Ceil(w.document["paused_until"].(float64))), 0))
		return
	}
	if !w.fresh(now) {
		w.mode, w.reason = "waiting", w.observationError
		if !w.fetched.IsZero() || w.observationError != "" {
			w.mode = "unavailable"
			if w.reason == "" {
				w.reason = "incomplete_history"
				if now.Sub(w.fetched) > warningFreshness || now.Before(w.fetched) {
					w.reason = "stale_feed"
				}
			}
		}
		return
	}
	w.mode, w.reason = "watching", ""
	if quietAt(settings, now, w.zone) {
		w.mode = "quiet"
	}
	if w.job != nil {
		return
	}
	if now.Before(w.nextDelivery) {
		w.nextEvaluation = earlier(w.nextEvaluation, w.nextDelivery)
		return
	}
	decisions := w.ledger.Candidates(w.key, now)
	// The most serious current warning gets the first bounded delivery slot.
	// Stable source order breaks ties; there is no retained unbounded queue.
	sort.SliceStable(decisions, func(i, j int) bool {
		return warningSeverity(w.bodies[decisions[i].Key].Severity) > warningSeverity(w.bodies[decisions[j].Key].Severity)
	})
	for _, d := range decisions {
		m, found := w.bodies[d.Key]
		if !found || !w.deliveryAllowed(d, m, now) {
			continue
		}
		r, err := w.ledger.Reserve(d, now)
		if err != nil {
			reason := "reservation_failed"
			if errors.Is(err, ErrWarningLedgerCapacity) {
				reason = "capacity_reached"
			}
			w.fail(reason)
			return
		}
		notice := warningNotice(d, m, w.name, w.zone, settings)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		job := &warningDeliveryJob{receipt: r, message: m, ctx: ctx, cancel: cancel, done: make(chan error, 1)}
		w.job, w.delivery, w.lastNotice = job, "pending", &notice
		w.rememberDetail(notice, m, now)
		w.nextDelivery = now.Add(warningDeliverySpacing)
		send, signal := w.send, w.signal
		go func() {
			err := send(ctx, notice)
			if contextErr := ctx.Err(); contextErr != nil {
				err = contextErr
			}
			job.done <- err
			cancel()
			if signal != nil {
				signal()
			}
		}()
		return
	}
}

// Interval supplies a deadline to the app's existing timer. A callback that
// ignores cancellation retains its slot without creating a tight polling loop.
func (w *WarningWatcher) Interval(now time.Time) time.Duration {
	if w.closed {
		return time.Hour
	}
	if w.dirty || !w.lastTick.IsZero() && now.Before(w.lastTick) {
		return time.Nanosecond
	}
	d := time.Hour
	if !w.nextEvaluation.IsZero() {
		d = w.nextEvaluation.Sub(now)
		if d <= 0 {
			return time.Nanosecond
		}
	}
	if w.job != nil {
		pending := time.Second
		if w.job.ctx.Err() != nil {
			pending = time.Minute
		}
		if d > pending {
			d = pending
		}
	}
	return d
}

// Snapshot contains only small status/policy metadata. Original source bodies
// remain private and are requested separately for detail presentation.
func (w *WarningWatcher) Snapshot() M {
	var fetched any
	if !w.fetched.IsZero() {
		fetched = w.fetched.UTC().Format(time.RFC3339Nano)
	}
	var last any
	if w.lastNotice != nil {
		last = M{"key": w.lastNotice.Key, "location": w.lastNotice.Location, "place": w.lastNotice.Place, "kind": w.lastNotice.Kind, "title": w.lastNotice.Title}
	}
	return M{"settings": safeio.Clone(w.document["settings"].(M)), "state": w.mode, "reason": w.reason, "paused_until": w.document["paused_until"], "supported": w.send != nil, "delivery": w.delivery, "fetched_at": fetched, "complete": w.complete, "last": last}
}

func (w *WarningWatcher) rememberDetail(notice WarningNotice, m weather.AlertMessage, now time.Time) {
	bytes := 0
	for _, v := range []string{notice.Key, notice.Location, notice.Kind, notice.Place, notice.Title, notice.Body, notice.Urgency, m.Identity.ID, m.Identity.Sender, m.Type, m.Issuer, m.Event, m.Area, m.Headline, m.Description, m.Instruction, m.Severity, m.Urgency, m.Certainty} {
		bytes += len(v)
	}
	for _, ref := range m.References {
		bytes += len(ref.ID) + len(ref.Sender)
	}
	if bytes > warningDetailBytes {
		return // Never cut original instructions to pretend a full detail exists.
	}
	kept := make([]warningDetailRecord, 0, warningDetailLimit)
	total := 0
	for _, d := range w.details {
		if !now.Before(d.created) && now.Sub(d.created) < warningDetailRetention {
			kept = append(kept, d)
			total += d.bytes
		}
	}
	for len(kept) > 0 && (len(kept) >= warningDetailLimit || total+bytes > warningDetailBytes) {
		total -= kept[0].bytes
		kept[0] = warningDetailRecord{}
		kept = kept[1:]
	}
	w.details = append(kept, warningDetailRecord{notice, m, now, bytes})
	w.detailBytes = total + bytes
}

func (w *WarningWatcher) pruneDetails(now time.Time) {
	for len(w.details) > 0 && !now.Before(w.details[0].created.Add(warningDetailRetention)) {
		w.detailBytes -= w.details[0].bytes
		w.details[0] = warningDetailRecord{}
		w.details = w.details[1:]
		w.dirty = true
	}
	if len(w.details) == 0 {
		w.details = nil
	}
}

// Detail resolves an exact warning/place pair to a full original message.
// At most 16 recently attempted details and two MiB of text are retained for
// 24 hours in memory. Eviction, restart and disable return unavailable instead
// of opening another warning or silently substituting today's primary city.
func (w *WarningWatcher) Detail(location, key string, now time.Time) (WarningNotice, weather.AlertMessage, bool) {
	for _, d := range w.details {
		if d.notice.Location == location && d.notice.Key == key && !now.Before(d.created) && now.Sub(d.created) < warningDetailRetention {
			m := d.message
			m.References = append([]weather.AlertReference{}, m.References...)
			return d.notice, m, true
		}
	}
	return WarningNotice{}, weather.AlertMessage{}, false
}

// Close cancels delivery and bounds cleanup. A timed-out worker remains an
// uncertain durable reservation; reopening never retries it automatically.
func (w *WarningWatcher) Close() error {
	if w.closed {
		return w.closeErr
	}
	w.closed = true
	w.mode, w.reason = "off", ""
	w.cancelDelivery()
	var err error
	if w.job != nil {
		w.delivery = "uncertain"
		select {
		case <-w.job.done:
		case <-time.After(4 * time.Second):
			err = errors.New("warning delivery cleanup unconfirmed")
		}
		w.job = nil
	}
	if w.ledger != nil {
		err = errors.Join(err, w.ledger.Close())
		w.ledger = nil
	}
	if w.lock != nil {
		err = errors.Join(err, w.lock.Close())
		w.lock = nil
	}
	w.clearObservation()
	w.lastNotice, w.details, w.detailBytes = nil, nil, 0
	w.closeErr = err
	if err != nil {
		w.reason = "cleanup_unconfirmed"
	}
	return err
}
