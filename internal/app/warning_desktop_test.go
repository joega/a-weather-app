package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
)

func warningPeer() *peer {
	return &peer{out: make(chan outbound, peerQueueLimit), closed: make(chan struct{})}
}
func warningEvent(t *testing.T, p *peer) M {
	t.Helper()
	select {
	case out := <-p.out:
		v, err := safeio.Object(out.raw, 8192)
		if err != nil {
			t.Fatal(err)
		}
		return v
	case <-time.After(time.Second):
		t.Fatal("no warning event")
	}
	return nil
}
func warningCommand(t *testing.T, p *peer, kind string) M {
	t.Helper()
	v := warningEvent(t, p)
	command := object(v["native"])
	if v["event"] != "warning_desktop" || command["kind"] != kind {
		t.Fatal("wrong native event", v)
	}
	return command
}
func nativeReady(t *testing.T, b *warningDesktop, p *peer, generation string, ready bool) {
	t.Helper()
	if !b.report(p, M{"kind": "ready", "generation": generation, "ready": ready, "actions": ready}) {
		t.Fatal("ready rejected")
	}
}
func nativeResult(t *testing.T, b *warningDesktop, p *peer, command M, status string) {
	t.Helper()
	if !b.report(p, M{"kind": "result", "generation": command["generation"], "token": command["token"], "status": status}) {
		t.Fatal("result rejected")
	}
}
func nativeSend(b *warningDesktop, ctx context.Context) <-chan error {
	done := make(chan error, 1)
	go func() {
		done <- b.Send(ctx, notifications.WarningNotice{Title: "Flood Warning", Body: "Move to higher ground", Urgency: "normal", Location: strings.Repeat("a", 64), Key: strings.Repeat("b", 64)})
	}()
	return done
}
func nativeDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Second):
		t.Fatal("delivery blocked")
		return nil
	}
}

func TestWarningDesktopDisabledSingleOwnerAndExactAction(t *testing.T) {
	b := &warningDesktop{}
	p, spare := warningPeer(), warningPeer()
	b.add(p)
	b.add(spare)
	b.add(p)
	if len(p.out) != 0 || len(spare.out) != 0 {
		t.Fatal("disabled adapter activated")
	}
	if capable, ready, _ := b.status(); !capable || ready {
		t.Fatal("availability", capable, ready)
	}
	b.setEnabled(true)
	generation := stringOf(warningCommand(t, p, "activate")["generation"])
	if len(spare.out) != 0 {
		t.Fatal("multiple native owners")
	}
	nativeReady(t, b, spare, generation, true)
	if _, ready, _ := b.status(); ready {
		t.Fatal("other peer made owner ready")
	}
	nativeReady(t, b, p, generation, true)
	done := nativeSend(b, context.Background())
	command := warningCommand(t, p, "notify")
	if err := b.Send(context.Background(), notifications.WarningNotice{}); err == nil {
		t.Fatal("second attempt admitted")
	}
	nativeResult(t, b, spare, command, "accepted")
	select {
	case <-done:
		t.Fatal("other peer completed delivery")
	default:
	}
	nativeResult(t, b, p, command, "accepted")
	if err := nativeDone(t, done); err != nil {
		t.Fatal(err)
	}
	if len(p.out) != 0 {
		t.Fatal("acceptance navigated without interaction")
	}
	action := M{"kind": "action", "generation": generation, "token": command["token"], "activation_token": "fixture-token"}
	if !b.report(p, action) {
		t.Fatal("action rejected")
	}
	opened := warningEvent(t, p)
	if opened["event"] != "warning_open" || object(opened["warning"])["key"] != strings.Repeat("b", 64) || object(opened["warning"])["location"] != strings.Repeat("a", 64) || opened["activation_token"] != "fixture-token" {
		t.Fatal("wrong detail route", opened)
	}
	b.report(p, action)
	if len(p.out) != 0 {
		t.Fatal("repeated click navigated again")
	}
	b.close()
}

func TestWarningDesktopCancelAndLateAcceptanceCannotNavigate(t *testing.T) {
	b := &warningDesktop{}
	p := warningPeer()
	b.add(p)
	b.setEnabled(true)
	generation := stringOf(warningCommand(t, p, "activate")["generation"])
	nativeReady(t, b, p, generation, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := nativeSend(b, ctx)
	command := warningCommand(t, p, "notify")
	cancel()
	if err := nativeDone(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	recall := warningCommand(t, p, "cancel")
	if recall["token"] != command["token"] {
		t.Fatal("wrong attempt canceled")
	}
	nativeResult(t, b, p, command, "accepted")
	b.report(p, M{"kind": "action", "generation": generation, "token": command["token"], "activation_token": ""})
	if len(p.out) != 0 || len(b.routes) != 0 {
		t.Fatal("late acceptance created route")
	}
	// An old token cannot complete a subsequent delivery in the same generation.
	done = nativeSend(b, context.Background())
	next := warningCommand(t, p, "notify")
	nativeResult(t, b, p, command, "accepted")
	select {
	case <-done:
		t.Fatal("old token completed newer delivery")
	default:
	}
	nativeResult(t, b, p, next, "uncertain")
	if err := nativeDone(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	warningCommand(t, p, "cancel")
	b.close()
}

func TestWarningDesktopFailoverDaemonRestartAndDisable(t *testing.T) {
	b := &warningDesktop{}
	p, spare := warningPeer(), warningPeer()
	b.add(p)
	b.add(spare)
	b.setEnabled(true)
	generation := stringOf(warningCommand(t, p, "activate")["generation"])
	nativeReady(t, b, p, generation, true)
	done := nativeSend(b, context.Background())
	command := warningCommand(t, p, "notify")
	b.remove(p)
	warningCommand(t, p, "deactivate")
	if err := nativeDone(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	nextGeneration := stringOf(warningCommand(t, spare, "activate")["generation"])
	if nextGeneration == generation {
		t.Fatal("session reused across peers")
	}
	nativeReady(t, b, spare, nextGeneration, true)
	done = nativeSend(b, context.Background())
	next := warningCommand(t, spare, "notify")
	nativeResult(t, b, p, command, "accepted")
	select {
	case <-done:
		t.Fatal("departed owner completed delivery")
	default:
	}
	nativeReady(t, b, spare, nextGeneration, false)
	if err := nativeDone(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	warningCommand(t, spare, "cancel")
	nativeResult(t, b, spare, next, "accepted")
	if len(b.routes) != 0 {
		t.Fatal("old daemon result restored route")
	}
	nativeReady(t, b, spare, nextGeneration, true)
	done = nativeSend(b, context.Background())
	warningCommand(t, spare, "notify")
	b.setEnabled(false)
	warningCommand(t, spare, "deactivate")
	if err := nativeDone(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, ready, _ := b.status(); ready {
		t.Fatal("disabled still ready")
	}
	b.setEnabled(true)
	finalGeneration := stringOf(warningCommand(t, spare, "activate")["generation"])
	if finalGeneration == nextGeneration {
		t.Fatal("reenable reused session")
	}
	nativeReady(t, b, spare, nextGeneration, true)
	if _, ready, _ := b.status(); ready {
		t.Fatal("old ready changed new session")
	}
	b.close()
}

func TestWarningDesktopRouteBoundsAndReportValidation(t *testing.T) {
	b := &warningDesktop{}
	p := warningPeer()
	b.add(p)
	b.setEnabled(true)
	generation := stringOf(warningCommand(t, p, "activate")["generation"])
	nativeReady(t, b, p, generation, true)
	var first M
	for i := 0; i < 20; i++ {
		done := nativeSend(b, context.Background())
		command := warningCommand(t, p, "notify")
		if i == 0 {
			first = command
		}
		nativeResult(t, b, p, command, "accepted")
		if err := nativeDone(t, done); err != nil {
			t.Fatal(err)
		}
		if len(b.routes) > warningRouteLimit {
			t.Fatal("unbounded routes")
		}
	}
	b.report(p, M{"kind": "action", "generation": generation, "token": first["token"], "activation_token": ""})
	if len(p.out) != 0 {
		t.Fatal("evicted action opened")
	}
	for _, v := range []M{
		{}, {"kind": "ready", "generation": generation, "ready": false, "actions": true},
		{"kind": "ready", "generation": generation, "ready": true, "actions": false, "extra": true},
		{"kind": "result", "generation": generation, "token": first["token"], "status": "sent"},
		{"kind": "action", "generation": generation, "token": first["token"], "activation_token": strings.Repeat("a", 4097)},
		{"kind": "action", "generation": generation, "token": first["token"], "activation_token": "bad\ncontrol"},
	} {
		if b.report(p, v) {
			t.Fatal("malformed report accepted", v)
		}
	}
	for i := range b.routes {
		b.routes[i].created = time.Now().Add(-25 * time.Hour)
	}
	b.pruneLocked(time.Now())
	if len(b.routes) != 0 {
		t.Fatal("expired routes retained")
	}
	b.close()
}
