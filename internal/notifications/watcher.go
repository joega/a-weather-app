// Package notifications evaluates precipitation outlooks and official warnings
// and owns bounded, cancellable desktop delivery.
package notifications

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

// M is a notification JSON object with float64 persisted numbers.
type M = map[string]any

const limit = 32768
const retention = 14 * 86400

// Defaults returns fresh notification settings; delivery is disabled initially.
func Defaults() M {
	return M{"enabled": false, "quiet_enabled": true, "quiet_start": float64(22), "quiet_end": float64(7), "probability": float64(50)}
}

// Patch validates settings changes without mutating old.
// The old object must satisfy the trusted internal JSON contract.
func Patch(old, patch M) (M, error) {
	if len(patch) == 0 {
		return nil, errors.New("empty settings")
	}
	v := safeio.Clone(old)
	for k, x := range patch {
		if _, ok := v[k]; !ok {
			return nil, errors.New("unknown setting")
		}
		v[k] = x
	}
	for _, k := range []string{"enabled", "quiet_enabled"} {
		if _, ok := v[k].(bool); !ok {
			return nil, errors.New("boolean")
		}
	}
	for _, k := range []string{"quiet_start", "quiet_end"} {
		n, ok := v[k].(float64)
		if !ok || math.Trunc(n) != n || n < 0 || n > 23 {
			return nil, errors.New("hour")
		}
	}
	if v["quiet_start"] == v["quiet_end"] {
		return nil, errors.New("quiet range")
	}
	if v["probability"] != float64(50) && v["probability"] != float64(70) {
		return nil, errors.New("probability")
	}
	return v, nil
}

// DefaultDocument returns fresh settings, snooze state, and delivery history.
func DefaultDocument() M {
	return M{"schema_version": float64(1), "settings": Defaults(), "snoozed_until": nil, "events": []any{}}
}
func timestamp(v any) bool {
	n, ok := v.(float64)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 253402300799
}

var fingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Validate checks a bounded persisted notification document and its settings.
func Validate(v M) error {
	if len(v) != 4 || v["schema_version"] != float64(1) {
		return errors.New("notification schema")
	}
	s, ok := v["settings"].(M)
	if !ok || len(s) != 5 {
		return errors.New("settings")
	}
	if _, e := Patch(Defaults(), s); e != nil {
		return e
	}
	until, ok := v["snoozed_until"]
	if !ok || (until != nil && !timestamp(until)) {
		return errors.New("pause")
	}
	rows, ok := v["events"].([]any)
	if !ok || len(rows) > 64 {
		return errors.New("history")
	}
	for _, x := range rows {
		r, ok := x.(M)
		if !ok || len(r) != 5 {
			return errors.New("event")
		}
		key, ok := r["location"].(string)
		if !ok || !fingerprint.MatchString(key) {
			return errors.New("event location")
		}
		for _, k := range []string{"start", "end", "reserved", "expires"} {
			if !timestamp(r[k]) {
				return errors.New("event timestamp")
			}
		}
		start, end := r["start"].(float64), r["end"].(float64)
		if start >= end || end-start > 22*86400 || r["expires"].(float64) != r["reserved"].(float64)+retention {
			return errors.New("event interval")
		}
	}
	return nil
}

// Text removes control/markup characters and truncates to limit runes.
func Text(s string, limit int) string {
	out := []rune{}
	for _, r := range s {
		if r == '<' || r == '>' || r == '&' || r < 32 || (r >= 127 && r <= 159) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return string(out)
}
func stamp(v any) time.Time { s, _ := v.(string); t, _ := time.Parse(time.RFC3339Nano, s); return t }
func str(v any) string      { s, _ := v.(string); return s }

type interval struct {
	start, end float64
	hours      [][2]float64
}

// Candidate selects an upcoming precipitation interval using the location timezone.
// Invalid or unsupported forecasts produce nil; it does not deliver notifications.
func Candidate(forecast, location M, now time.Time, probability float64) M {
	if forecast == nil || !reflect.DeepEqual(forecast["location"], location) {
		return nil
	}
	age := now.Sub(stamp(forecast["fetched_at"]))
	if age < 0 || age > 2700*time.Second {
		return nil
	}
	zone, err := time.LoadLocation(str(location["timezone"]))
	if err != nil {
		return nil
	}
	var cache candidateCache
	event, _ := cache.candidate(forecast, location, now, probability, zone)
	return event
}

// Sender is invoked on a delivery worker and must return when its context is canceled.
// Its result determines delivery status; the Watcher serializes worker admission.
type Sender func(context.Context, string, string) error

// Send invokes notify-send with sanitized text and bounded subprocess cleanup.
// The command inherits only the environment needed for desktop delivery.
func Send(ctx context.Context, title, body string) error {
	cmd := exec.CommandContext(ctx, "/usr/bin/notify-send", "--app-name=A Weather App", "--urgency=low", "--expire-time=8000", "--transient", "--hint=boolean:suppress-sound:true", "--", Text(title, 80), Text(body, 320))
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	for _, k := range []string{"DBUS_SESSION_BUS_ADDRESS", "XDG_RUNTIME_DIR", "DISPLAY", "WAYLAND_DISPLAY", "LANG"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	return cmd.Run()
}

// Watcher owns notification history, its delivery lock, and at most one worker.
// Its owner must serialize all methods and close it before closing state.
type Watcher struct {
	state          *safeio.Directory
	document       M
	lock           *os.File
	problem        bool
	mode, delivery string
	closing        bool
	send           Sender
	supported      bool
	cancel         context.CancelFunc
	done           chan error
	zoneName       string
	zone           *time.Location
	zoneReady      bool
	fetchedText    string
	fetchedAt      time.Time
	candidates     candidateCache
	lastTick       time.Time
	nextEvaluation time.Time
}

// New restores private notification state and acquires delivery ownership when
// enabled. Nil sender uses Send; invalid state is reflected in Snapshot.
func New(state *safeio.Directory, sender Sender) *Watcher {
	supported := true
	if sender == nil {
		sender = Send
		supported = false
		if info, e := os.Stat("/usr/bin/notify-send"); e == nil && info.Mode()&0111 != 0 {
			supported = true
		}
	}
	w := &Watcher{state: state, document: DefaultDocument(), mode: "off", delivery: "none", send: sender, supported: supported}
	v, e := state.Read("notifications.json", limit)
	if e == nil && v != nil {
		e = Validate(v)
		if e == nil {
			w.document = v
		}
	}
	if e == nil && w.Enabled() {
		e = w.acquire()
		if e == nil {
			v, e = state.Read("notifications.json", limit)
			if e == nil {
				e = Validate(v)
			}
			if e == nil {
				w.document = v
				if !w.Enabled() {
					w.release()
				}
			}
		}
	}
	if e != nil {
		w.problem = true
		w.release()
	}
	return w
}

// Enabled reports whether saved settings enable notification delivery.
func (w *Watcher) Enabled() bool { return w.document["settings"].(M)["enabled"] == true }
func (w *Watcher) acquire() error {
	if w.lock != nil {
		return nil
	}
	f, e := w.state.Lock("notifications.lock")
	if e == nil {
		w.lock = f
	}
	return e
}
func (w *Watcher) release() {
	if w.lock != nil {
		syscall.Flock(int(w.lock.Fd()), syscall.LOCK_UN)
		w.lock.Close()
		w.lock = nil
	}
}
func (w *Watcher) save(v M) error {
	if e := Validate(v); e != nil {
		return e
	}
	if e := w.state.Write("notifications.json", v, limit); e != nil {
		return e
	}
	w.document = v
	return nil
}
func (w *Watcher) cancelDelivery() {
	if w.cancel != nil {
		w.cancel()
	}
}

// Configure validates and persists settings before changing delivery state.
func (w *Watcher) Configure(patch M) error {
	if w.closing || w.problem {
		return errors.New("watcher unavailable")
	}
	if _, e := Patch(w.document["settings"].(M), patch); e != nil {
		return e
	}
	prior := w.Enabled()
	if e := w.acquire(); e != nil {
		return e
	}
	success := false
	defer func() {
		if !success && !prior {
			w.release()
		}
	}()
	v := safeio.Clone(w.document)
	saved, e := w.state.Read("notifications.json", limit)
	if e != nil {
		return e
	}
	if saved != nil {
		if e = Validate(saved); e != nil {
			return e
		}
		v = saved
	}
	settings, e := Patch(v["settings"].(M), patch)
	if e != nil {
		return e
	}
	quietChanged := false
	for _, k := range []string{"quiet_enabled", "quiet_start", "quiet_end"} {
		if settings[k] != v["settings"].(M)[k] {
			quietChanged = true
		}
	}
	v["settings"] = settings
	if e = w.save(v); e != nil {
		return e
	}
	success = true
	if !w.Enabled() || quietChanged {
		w.cancelDelivery()
	}
	w.mode = "waiting"
	if !w.Enabled() {
		w.mode = "off"
		w.release()
	}
	return nil
}

// Snooze pauses delivery for one hour, or resumes it immediately when resume is true.
func (w *Watcher) Snooze(now time.Time, resume bool) error {
	if !w.Enabled() || w.closing || w.problem || w.lock == nil {
		return errors.New("watcher unavailable")
	}
	v := safeio.Clone(w.document)
	v["snoozed_until"] = nil
	if !resume {
		v["snoozed_until"] = float64(now.Unix()) + 3600
	}
	if e := w.save(v); e != nil {
		return e
	}
	if !resume {
		w.cancelDelivery()
	}
	return nil
}

// Tick and Interval are serialized by the owning app, like the other Watcher
// methods. Settings, forecast and location changes must call Tick immediately.
func (w *Watcher) Tick(forecast, location M, now time.Time, dataOK bool) {
	settings := w.document["settings"].(M)
	w.lastTick, w.nextEvaluation = now, time.Time{}
	zoneName := str(location["timezone"])
	if !w.zoneReady || zoneName != w.zoneName {
		w.zoneName, w.zoneReady = zoneName, true
		w.zone, _ = time.LoadLocation(zoneName)
	}
	zone := w.zone
	locationOK := zone != nil && location != nil
	fetchedText := str(forecast["fetched_at"])
	if fetchedText != w.fetchedText {
		w.fetchedText, w.fetchedAt = fetchedText, stamp(fetchedText)
	}
	age := now.Sub(w.fetchedAt)
	dataOK = dataOK && forecast != nil && reflect.DeepEqual(forecast["location"], location) && age >= 0 && age <= 2700*time.Second && locationOK
	quiet := w.Enabled() && quietAt(settings, now, zone)
	if w.Enabled() && locationOK {
		w.nextEvaluation = nextQuietTransition(settings, now, zone)
		if now.Before(w.fetchedAt) {
			w.nextEvaluation = earlier(w.nextEvaluation, w.fetchedAt)
		} else if expiry := w.fetchedAt.Add(2700*time.Second + time.Nanosecond); now.Before(expiry) {
			w.nextEvaluation = earlier(w.nextEvaluation, expiry)
		}
		if until, ok := w.document["snoozed_until"].(float64); ok && float64(now.Unix()) < until {
			// Snooze comparisons use whole Unix seconds, including a persisted
			// fractional timestamp: resume at the first matching whole second.
			w.nextEvaluation = earlier(w.nextEvaluation, time.Unix(int64(math.Ceil(until)), 0))
		}
	}

	if !dataOK || quiet {
		w.cancelDelivery()
	}
	if w.done != nil {
		select {
		case err := <-w.done:
			w.delivery = "sent"
			if err != nil {
				w.delivery = "failed"
			}
			w.done = nil
			w.cancel()
			w.cancel = nil
		default:
		}
	}
	if w.closing || !w.Enabled() {
		w.mode = "off"
		return
	}
	if w.problem || w.lock == nil || !w.supported {
		w.mode = "unavailable"
		return
	}
	if !locationOK {
		w.mode = "waiting"
		return
	}
	if until, ok := w.document["snoozed_until"].(float64); ok && float64(now.Unix()) < until {
		w.mode = "paused"
		return
	}
	if quiet {
		w.mode = "quiet"
		return
	}
	w.mode = "waiting"
	if !dataOK {
		return
	}
	w.mode = "watching"
	if w.done != nil {
		return
	}
	event, next := w.candidates.candidate(forecast, location, now, settings["probability"].(float64), zone)
	w.nextEvaluation = earlier(w.nextEvaluation, next)
	if event == nil {
		return
	}
	// Inspect reservations without cloning the entire document on every tick.
	// Clone only when extending or creating a durable reservation.
	for index, x := range w.document["events"].([]any) {
		r := x.(M)
		if expires := r["expires"].(float64); expires > float64(now.Unix()) {
			w.nextEvaluation = earlier(w.nextEvaluation, time.Unix(int64(math.Ceil(expires)), 0))
		} else {
			continue
		}
		if r["location"] != event["location"] {
			continue
		}
		if until := r["reserved"].(float64) + 21600; float64(now.Unix()) < until {
			w.nextEvaluation = earlier(w.nextEvaluation, time.Unix(int64(math.Ceil(until)), 0))
		}
		if (event["start"].(float64) <= r["end"].(float64)+10800 && event["end"].(float64) >= r["start"].(float64)-10800) || float64(now.Unix())-r["reserved"].(float64) < 21600 {
			start, end := math.Min(r["start"].(float64), event["start"].(float64)), math.Max(r["end"].(float64), event["end"].(float64))
			if start != r["start"] || end != r["end"] {
				v := safeio.Clone(w.document)
				copy := v["events"].([]any)[index].(M)
				copy["start"], copy["end"] = start, end
				rows := []any{}
				for _, x := range v["events"].([]any) {
					if x.(M)["expires"].(float64) > float64(now.Unix()) {
						rows = append(rows, x)
					}
				}
				v["events"] = rows
				if e := w.save(v); e != nil {
					w.mode, w.delivery = "unavailable", "failed"
				}
			}
			return
		}
	}
	v := safeio.Clone(w.document)
	rows := []any{}
	for _, x := range v["events"].([]any) {
		r := x.(M)
		if r["expires"].(float64) > float64(now.Unix()) {
			rows = append(rows, r)
		}
	}
	v["events"] = rows
	if len(rows) >= 64 {
		w.mode = "unavailable"
		return
	}
	v["events"] = append(rows, M{"location": event["location"], "start": event["start"], "end": event["end"], "reserved": float64(now.Unix()), "expires": float64(now.Unix()) + retention})
	if e := w.save(v); e != nil {
		w.mode = "unavailable"
		w.delivery = "failed"
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	w.cancel = cancel
	done := make(chan error, 1)
	w.done = done
	w.delivery = "pending"
	go func() {
		err := w.send(ctx, str(event["title"]), str(event["body"]))
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		done <- err
	}()
}

// Interval schedules the next time-dependent evaluation. Input or policy
// changes are handled immediately by Tick; pending asynchronous delivery keeps
// the existing one-second completion/cancellation polling budget.
func (w *Watcher) Interval(now time.Time) time.Duration {
	if w.lastTick.IsZero() {
		return time.Second
	}
	if now.Before(w.lastTick) {
		return time.Nanosecond
	}
	d := time.Hour
	if !w.nextEvaluation.IsZero() {
		d = w.nextEvaluation.Sub(now)
	}
	if d <= 0 {
		return time.Nanosecond
	}
	if w.done != nil && d > time.Second {
		d = time.Second
	}
	return d
}

// Snapshot returns caller-owned settings and current delivery state.
func (w *Watcher) Snapshot() M {
	return M{"settings": safeio.Clone(w.document["settings"].(M)), "state": w.mode, "snoozed_until": w.document["snoozed_until"], "delivery": w.delivery, "supported": w.supported}
}

// Close cancels delivery and waits up to four seconds before releasing the lock.
// A timeout reports unconfirmed cleanup rather than claiming delivery stopped.
func (w *Watcher) Close() error {
	w.closing = true
	w.cancelDelivery()
	defer w.release()
	if w.done != nil {
		select {
		case <-w.done:
			w.done = nil
		case <-time.After(4 * time.Second):
			return errors.New("notification cleanup unconfirmed")
		}
	}
	return nil
}

// Plain preserves multiline alert content while excluding control and markup.
func Plain(v any, limit int, multiline bool) any {
	if v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	r := []rune(s)
	if len(r) > limit {
		r = r[:limit]
	}
	var b strings.Builder
	for _, c := range r {
		if c == '\n' && multiline {
			b.WriteRune(c)
		} else if c >= 32 && !(c >= 127 && c <= 159) && !(c >= 0x202a && c <= 0x202e) && !(c >= 0x2066 && c <= 0x2069) && !strings.ContainsRune("<>&", c) {
			b.WriteRune(c)
		}
	}
	return b.String()
}
