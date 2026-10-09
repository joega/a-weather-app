package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/notifications"
)

const warningRouteLimit = 16

type warningNativeAttempt struct {
	token  string
	notice notifications.WarningNotice
	done   chan error
}
type warningNativeRoute struct {
	token, location, key string
	created              time.Time
}

// warningDesktop connects the policy owner's one delivery worker to one native
// subscriber. It never calls back into App while locked. Peers only enqueue
// bounded messages; neither daemon acceptance nor clicks hold the app mutex.
type warningDesktop struct {
	mu                              sync.Mutex
	peers                           []*peer
	owner                           *peer
	enabled, closed, ready, actions bool
	generation                      string
	availabilityRevision            uint64
	pending                         *warningNativeAttempt
	routes                          []warningNativeRoute
	signal                          func()
}

func warningNonce() string {
	var value [16]byte
	_, _ = rand.Read(value[:]) // crypto/rand.Read is guaranteed to fill or fail fatally.
	return hex.EncodeToString(value[:])
}
func warningHex(s string, size int) bool {
	if len(s) != size {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func (b *warningDesktop) changed() {
	if b.signal != nil {
		b.signal()
	}
}
func (b *warningDesktop) sendLocked(p *peer, command M) error {
	raw, err := ipc.Encode(M{"version": 1.0, "event": "warning_desktop", "native": command}, ipc.RequestLimit)
	if err != nil {
		return err
	}
	_, err = p.enqueue(raw, false)
	return err
}
func (b *warningDesktop) finishLocked(err error) {
	if b.pending != nil {
		b.pending.done <- err
		b.pending = nil
	}
}
func (b *warningDesktop) resetLocked() {
	b.availabilityRevision++
	if b.owner != nil {
		_ = b.sendLocked(b.owner, M{"kind": "deactivate", "generation": b.generation})
	}
	b.finishLocked(context.Canceled)
	b.owner, b.generation, b.ready, b.actions, b.routes = nil, "", false, false, nil
}
func (b *warningDesktop) selectLocked() {
	if b.closed || !b.enabled || b.owner != nil {
		return
	}
	for _, p := range b.peers {
		select {
		case <-p.closed:
			continue
		default:
		}
		generation := warningNonce()
		if b.sendLocked(p, M{"kind": "activate", "generation": generation}) == nil {
			b.owner, b.generation = p, generation
			return
		}
	}
}

// add is called only after a successful, capability-bearing subscription's
// initial snapshot has been enqueued. Ordinary subscribers never receive controls.
func (b *warningDesktop) add(p *peer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, existing := range b.peers {
		if existing == p {
			return
		}
	}
	if len(b.peers) >= 16 {
		return
	}
	b.peers = append(b.peers, p)
	b.selectLocked()
	b.changed()
}
func (b *warningDesktop) remove(p *peer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, existing := range b.peers {
		if existing == p {
			b.peers = append(b.peers[:i], b.peers[i+1:]...)
			break
		}
	}
	if b.owner == p {
		b.resetLocked()
		b.selectLocked()
		b.changed()
	}
}
func (b *warningDesktop) setEnabled(enabled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.enabled == enabled {
		return
	}
	b.enabled = enabled
	if enabled {
		b.selectLocked()
	} else {
		b.resetLocked()
	}
	b.changed()
}
func (b *warningDesktop) status() (capable, ready, actions bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed && len(b.peers) > 0, !b.closed && b.ready, !b.closed && b.actions
}
func (b *warningDesktop) deliveryState() (bool, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.closed && b.ready, b.availabilityRevision
}
func (b *warningDesktop) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	b.resetLocked()
	b.peers = nil
}

// Send returns on daemon acceptance, not interaction. Cancellation removes its
// route and best-effort recalls native delivery, retaining an uncertain receipt.
func (b *warningDesktop) Send(ctx context.Context, notice notifications.WarningNotice) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	if b.closed || !b.enabled || !b.ready || b.owner == nil || b.pending != nil {
		b.mu.Unlock()
		return errors.New("native warning delivery unavailable")
	}
	p, generation := b.owner, b.generation
	attempt := &warningNativeAttempt{token: warningNonce(), notice: notice, done: make(chan error, 1)}
	err := b.sendLocked(p, M{"kind": "notify", "generation": generation, "token": attempt.token,
		"title": notice.Title, "body": notice.Body, "urgency": notice.Urgency})
	if err != nil {
		b.mu.Unlock()
		return err
	}
	b.pending = attempt
	b.mu.Unlock()
	select {
	case err = <-attempt.done:
	case <-ctx.Done():
		err = ctx.Err()
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		b.mu.Lock()
		if b.pending == attempt {
			b.pending = nil
		}
		b.removeRouteLocked(attempt.token)
		if b.owner == p && b.generation == generation {
			_ = b.sendLocked(p, M{"kind": "cancel", "generation": generation, "token": attempt.token})
		}
		b.mu.Unlock()
	}
	return err
}

func validWarningReport(v M) bool {
	if !warningHex(stringOf(v["generation"]), 32) {
		return false
	}
	switch v["kind"] {
	case "ready":
		ready, ok := v["ready"].(bool)
		actions, okActions := v["actions"].(bool)
		return len(v) == 4 && ok && okActions && (ready || !actions)
	case "result":
		return len(v) == 4 && warningHex(stringOf(v["token"]), 32) &&
			(v["status"] == "accepted" || v["status"] == "failed" || v["status"] == "uncertain")
	case "action":
		token, ok := v["activation_token"].(string)
		if len(v) != 4 || !warningHex(stringOf(v["token"]), 32) || !ok || len(token) > 4096 || !utf8.ValidString(token) {
			return false
		}
		for _, c := range token {
			if c < 32 || c >= 127 && c <= 159 {
				return false
			}
		}
		return true
	}
	return false
}
func (b *warningDesktop) removeRouteLocked(token string) {
	for i, r := range b.routes {
		if r.token == token {
			b.routes = append(b.routes[:i], b.routes[i+1:]...)
			return
		}
	}
}
func (b *warningDesktop) pruneLocked(now time.Time) {
	kept := b.routes[:0]
	for _, route := range b.routes {
		if !now.Before(route.created) && now.Sub(route.created) < 24*time.Hour {
			kept = append(kept, route)
		}
	}
	b.routes = kept
}

// report handles only validated one-way adapter reports. Stale session/token
// values cannot complete newer deliveries or navigate to another warning.
func (b *warningDesktop) report(p *peer, v M) bool {
	if !validWarningReport(v) {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || p != b.owner || v["generation"] != b.generation {
		return true
	}
	switch v["kind"] {
	case "ready":
		if b.ready && v["ready"] == false {
			b.availabilityRevision++
		}
		b.ready, b.actions = v["ready"].(bool), v["actions"].(bool)
		if !b.ready {
			b.finishLocked(context.DeadlineExceeded)
			b.routes = nil
		}
		b.changed()
	case "result":
		if b.pending == nil || v["token"] != b.pending.token {
			return true
		}
		switch v["status"] {
		case "accepted":
			now := time.Now()
			b.pruneLocked(now)
			if len(b.routes) == warningRouteLimit {
				b.routes = b.routes[1:]
			}
			n := b.pending.notice
			b.routes = append(b.routes, warningNativeRoute{b.pending.token, n.Location, n.Key, now})
			b.finishLocked(nil)
		case "failed":
			b.finishLocked(errors.New("native warning not sent"))
		default:
			b.finishLocked(context.DeadlineExceeded)
		}
	case "action":
		if !b.ready || !b.actions {
			return true
		}
		b.pruneLocked(time.Now())
		for _, route := range b.routes {
			if route.token != v["token"] {
				continue
			}
			b.removeRouteLocked(route.token)
			raw, err := ipc.Encode(M{"version": 1.0, "event": "warning_open", "warning": M{"location": route.location, "key": route.key}, "activation_token": v["activation_token"]}, ipc.RequestLimit)
			if err == nil {
				_, _ = p.enqueue(raw, false)
			}
			break
		}
	}
	return true
}
