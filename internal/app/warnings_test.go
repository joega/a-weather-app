package app

import (
	"bufio"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

func TestWarningsRuntimePrimaryDemandAndExactDetails(t *testing.T) {
	now := savedRuntimeNow
	calls := make(chan alertProbeCall, 4)
	var forecasts atomic.Int32
	a, _ := runtimeLocations(t, Options{Now: func() time.Time { return now }, FetchAlertMessages: alertProbeFetcher(calls), FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
		forecasts.Add(1)
		return nil, errors.New("unexpected forecast fetch")
	}}, 1)
	a.setPresented(false)
	p := warningPeer()
	a.warningDesktop.add(p)
	reply, _ := a.Handle(context.Background(), request("set_warning_notifications", M{"notifications": M{"enabled": true, "quiet_enabled": false}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	generation := stringOf(warningCommand(t, p, "activate")["generation"])
	if len(calls) != 0 || a.warnings.WantsFeed() {
		t.Fatal("feed started before native ready")
	}
	nativeReady(t, a.warningDesktop, p, generation, true)
	a.Tick(context.Background())
	call := alertAwaitCall(t, calls)
	if call.query.Active || call.query.Location["latitude"] != a.primary.location["latitude"] {
		t.Fatal("wrong primary demand", call.query)
	}
	m := alertTestMessage("native warning", call.now)
	m.Instruction = "Original full instructions.\nKeep this line intact."
	alertAppComplete(t, a, call, m)
	now = now.Add(30 * time.Second)
	a.Tick(context.Background())
	call = alertAwaitCall(t, calls)
	if !call.query.Active {
		t.Fatal("missing final active check")
	}
	alertAppComplete(t, a, call, m)
	command := warningCommand(t, p, "notify")
	if !strings.Contains(stringOf(command["body"]), "City 0") {
		t.Fatal("delivery followed viewed city", command)
	}
	nativeResult(t, a.warningDesktop, p, command, "accepted")
	deadline := time.Now().Add(time.Second)
	for object(a.Snapshot()["warning_notifications"])["delivery"] != "sent" {
		if time.Now().After(deadline) {
			t.Fatal("native acceptance not collected")
		}
		time.Sleep(time.Millisecond)
	}
	key := m.Identity.Key()
	query := M{"location": pointAlertKey(a.primary), "key": key}
	reply, _ = a.Handle(context.Background(), request("warning_detail", M{"warning": query}))
	if reply["ok"] != true || object(reply["warning"])["instruction"] != m.Instruction || object(reply["warning"])["place"] != "City 0" {
		t.Fatal("original detail not preserved", reply)
	}
	query["location"] = strings.Repeat("f", 64)
	reply, _ = a.Handle(context.Background(), request("warning_detail", M{"warning": query}))
	if reply["error"] != "warning_unavailable" {
		t.Fatal("wrong place resolved", reply)
	}
	// Finish another cycle's final request before losing the daemon, but leave
	// its response queued. That pre-restart result must not authorize delivery
	// even if the daemon returns before the app's next tick.
	now = now.Add(2 * time.Minute)
	a.Tick(context.Background())
	call = alertAwaitCall(t, calls)
	if call.query.Active {
		t.Fatal("expected new history cycle")
	}
	alertAppComplete(t, a, call, m)
	now = now.Add(30 * time.Second)
	a.Tick(context.Background())
	call = alertAwaitCall(t, calls)
	if !call.query.Active {
		t.Fatal("expected final active request")
	}
	call.reply <- alertProbeReply{page: weather.AlertMessagePage{Messages: []weather.AlertMessage{m}, FetchedAt: call.now, Complete: true}}
	select {
	case completed := <-a.alerts.done:
		a.alerts.done <- completed
	case <-time.After(time.Second):
		t.Fatal("final request did not complete")
	}
	nativeReady(t, a.warningDesktop, p, generation, false)
	nativeReady(t, a.warningDesktop, p, generation, true)
	a.Tick(context.Background())
	if a.warnings.Snapshot()["state"] != "waiting" {
		t.Fatal("daemon restart reused old observation", a.warnings.Snapshot())
	}
	reply, _ = a.Handle(context.Background(), request("set_warning_notifications", M{"notifications": M{"enabled": false}}))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	warningCommand(t, p, "deactivate")
	if len(a.alerts.entries) != 0 || forecasts.Load() != 0 {
		t.Fatal("disabled hidden monitor retained demand", forecasts.Load())
	}
	query["location"] = pointAlertKey(a.primary)
	reply, _ = a.Handle(context.Background(), request("warning_detail", M{"warning": query}))
	if reply["error"] != "warning_unavailable" {
		t.Fatal("disabled source retained", reply)
	}
}

func TestWarningsDefaultAndHeadlessHaveNoDeliveryOwnership(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	a.Tick(context.Background())
	if a.warnings.Enabled() || a.warnings.WantsFeed() || object(a.Snapshot()["warning_notifications"])["ready"] != false {
		t.Fatal("default monitoring enabled")
	}
	for _, name := range []string{"warning-notifications.json", "warning-notifications.lock", "warning-ledger.json"} {
		v, err := a.state.Read(name, 2<<20)
		if err != nil || v != nil {
			t.Fatal("disabled startup created warning state", name, err)
		}
	}
	reply, _ := a.Handle(context.Background(), request("set_warning_notifications", M{"notifications": M{"enabled": true}}))
	if reply["ok"] != false || a.warnings.Enabled() {
		t.Fatal("enabled without native subscriber")
	}
	headless, err := newApp(testState(t), Options{Offline: true}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer headless.Close(context.Background())
	if headless.warnings != nil || headless.warningDesktop != nil || headless.warningObserver != nil {
		t.Fatal("bar refresh owns native warnings")
	}
}

func warningWire(t *testing.T, reader *bufio.Reader, event string) M {
	t.Helper()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		v, err := safeio.Object(line, ipc.ResponseLimit)
		if err != nil {
			t.Fatal(err)
		}
		if v["event"] == event {
			return v
		}
		if v["event"] != "snapshot" {
			t.Fatal("unexpected wire message", v)
		}
	}
}

func TestWarningSocketCapabilityAcceptanceAndAction(t *testing.T) {
	f := serveFixture(t)
	c := connect(t, f.path)
	reader := bufio.NewReader(c)
	if err := ipc.Send(c, request("subscribe", M{"native_notifications": true}), ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	if initial := readReply(t, reader); initial["ok"] != true {
		t.Fatal(initial)
	}
	if err := ipc.Send(c, request("set_warning_notifications", M{"notifications": M{"enabled": true}}), ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	activation := object(warningWire(t, reader, "warning_desktop")["native"])
	if activation["kind"] != "activate" {
		t.Fatal(activation)
	}
	if reply := readReply(t, reader); reply["ok"] != true {
		t.Fatal(reply)
	}
	report := func(v M) {
		t.Helper()
		if err := ipc.Send(c, M{"version": 1.0, "op": "warning_native", "native": v}, ipc.RequestLimit); err != nil {
			t.Fatal(err)
		}
	}
	report(M{"kind": "ready", "generation": activation["generation"], "ready": true, "actions": true})
	deadline := time.Now().Add(time.Second)
	for {
		_, ready, _ := f.a.warningDesktop.status()
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("one-way ready report not handled")
		}
		time.Sleep(time.Millisecond)
	}
	done := nativeSend(f.a.warningDesktop, context.Background())
	command := object(warningWire(t, reader, "warning_desktop")["native"])
	if command["kind"] != "notify" {
		t.Fatal(command)
	}
	report(M{"kind": "result", "generation": command["generation"], "token": command["token"], "status": "accepted"})
	if err := nativeDone(t, done); err != nil {
		t.Fatal(err)
	}
	report(M{"kind": "action", "generation": command["generation"], "token": command["token"], "activation_token": "token"})
	opened := warningWire(t, reader, "warning_open")
	if object(opened["warning"])["location"] != strings.Repeat("a", 64) || opened["activation_token"] != "token" {
		t.Fatal(opened)
	}
	if err := ipc.Send(c, request("snapshot", nil), ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	if reply := readReply(t, reader); reply["request_id"] != 7.0 || reply["ok"] != true {
		t.Fatal("native reply polluted ordinary request stream", reply)
	}
}

func TestWarningSocketRejectsMalformedCapabilityAndUnsubscribedReports(t *testing.T) {
	f := serveFixture(t)
	c := connect(t, f.path)
	reader := bufio.NewReader(c)
	for _, value := range []any{"true", 1.0, nil} {
		if err := ipc.Send(c, request("subscribe", M{"native_notifications": value}), ipc.RequestLimit); err != nil {
			t.Fatal(err)
		}
		if reply := readReply(t, reader); reply["ok"] != false {
			t.Fatal("malformed capability accepted", reply)
		}
	}
	if capable, _, _ := f.a.warningDesktop.status(); capable {
		t.Fatal("failed subscribe registered native peer")
	}
	if err := ipc.Send(c, M{"version": 1.0, "op": "warning_native", "native": M{"kind": "ready", "generation": strings.Repeat("a", 32), "ready": true, "actions": true}}, ipc.RequestLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("unsubscribed report accepted")
	}
}
