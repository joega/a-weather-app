package notifications

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"math"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

type M = map[string]any

const limit = 32768
const retention = 14 * 86400

func Defaults() M {
	return M{"enabled": false, "quiet_enabled": true, "quiet_start": float64(22), "quiet_end": float64(7), "probability": float64(50)}
}
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
func DefaultDocument() M {
	return M{"schema_version": float64(1), "settings": Defaults(), "snoozed_until": nil, "events": []any{}}
}
func timestamp(v any) bool {
	n, ok := v.(float64)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n <= 253402300799
}

var fingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

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

func Candidate(forecast, location M, now time.Time, probability float64) M {
	if forecast == nil || !reflect.DeepEqual(forecast["location"], location) {
		return nil
	}
	age := now.Sub(stamp(forecast["fetched_at"]))
	if age < 0 || age > 2700*time.Second {
		return nil
	}
	rows, ok := forecast["hourly"].([]any)
	if !ok || len(rows) > 240 {
		return nil
	}
	wet := [][2]float64{}
	for _, x := range rows {
		r, ok := x.(M)
		if !ok {
			continue
		}
		chance, ok := r["precipitation_probability"].(float64)
		if !ok || math.IsNaN(chance) || chance < probability/100 || chance > 1 {
			continue
		}
		t := stamp(r["time"])
		if t.IsZero() || math.Abs(t.Sub(now).Seconds()) > 11*86400 {
			continue
		}
		wet = append(wet, [2]float64{float64(t.Unix()) - 3600, chance})
	}
	sort.Slice(wet, func(i, j int) bool { return wet[i][0] < wet[j][0] })
	groups := []interval{}
	for _, h := range wet {
		n := len(groups)
		if n > 0 && h[0] <= groups[n-1].end+10800 {
			groups[n-1].end = math.Max(groups[n-1].end, h[0]+3600)
			groups[n-1].hours = append(groups[n-1].hours, h)
		} else {
			groups = append(groups, interval{h[0], h[0] + 3600, [][2]float64{h}})
		}
	}
	zone, e := time.LoadLocation(str(location["timezone"]))
	if e != nil {
		return nil
	}
	lat, a := location["latitude"].(float64)
	lon, b := location["longitude"].(float64)
	if !a || !b || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return nil
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%.4f,%.4f", lat, lon))))
	instant := float64(now.Unix())
	for _, g := range groups {
		for _, h := range g.hours {
			if h[0]+3600 > instant && h[0] <= instant+10800 {
				start, end := time.Unix(int64(h[0]), 0).In(zone), time.Unix(int64(h[0]+3600), 0).In(zone)
				window := start.Format("Mon 3 PM MST") + "–" + end.Format("3 PM MST")
				return M{"location": key, "start": g.start, "end": g.end, "title": "Hourly precipitation outlook", "body": Text(fmt.Sprintf("%s: %.0f%% chance of precipitation for %s. Open-Meteo hourly forecast; timing may change.", Text(str(location["name"]), 96), math.RoundToEven(h[1]*100), window), 320)}
			}
		}
	}
	return nil
}

// Sender completes asynchronously and must respect cancellation.
type Sender func(context.Context, string, string) error

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
}

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
func (w *Watcher) Tick(forecast, location M, now time.Time, dataOK bool) {
	settings := w.document["settings"].(M)
	zone, e := time.LoadLocation(str(location["timezone"]))
	locationOK := e == nil && location != nil
	age := now.Sub(stamp(forecast["fetched_at"]))
	dataOK = dataOK && forecast != nil && reflect.DeepEqual(forecast["location"], location) && age >= 0 && age <= 2700*time.Second && locationOK
	quiet := false
	if w.Enabled() && settings["quiet_enabled"] == true && locationOK {
		hour := float64(now.In(zone).Hour())
		start, end := settings["quiet_start"].(float64), settings["quiet_end"].(float64)
		if start < end {
			quiet = hour >= start && hour < end
		} else {
			quiet = hour >= start || hour < end
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
	event := Candidate(forecast, location, now, settings["probability"].(float64))
	if event == nil {
		return
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
	for _, x := range rows {
		r := x.(M)
		if r["location"] == event["location"] && ((event["start"].(float64) <= r["end"].(float64)+10800 && event["end"].(float64) >= r["start"].(float64)-10800) || float64(now.Unix())-r["reserved"].(float64) < 21600) {
			start, end := math.Min(r["start"].(float64), event["start"].(float64)), math.Max(r["end"].(float64), event["end"].(float64))
			if start != r["start"] || end != r["end"] {
				r["start"], r["end"] = start, end
				if e = w.save(v); e != nil {
					w.mode = "unavailable"
					w.delivery = "failed"
				}
			}
			return
		}
	}
	if len(rows) >= 64 {
		w.mode = "unavailable"
		return
	}
	v["events"] = append(rows, M{"location": event["location"], "start": event["start"], "end": event["end"], "reserved": float64(now.Unix()), "expires": float64(now.Unix()) + retention})
	if e = w.save(v); e != nil {
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
func (w *Watcher) Snapshot() M {
	return M{"settings": safeio.Clone(w.document["settings"].(M)), "state": w.mode, "snoozed_until": w.document["snoozed_until"], "delivery": w.delivery, "supported": w.supported}
}
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
