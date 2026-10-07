package app

import (
	"context"
	"testing"
	"time"
)

func TestUpdateCheckDoesNotBlockWeatherControls(t *testing.T) {
	started := make(chan bool, 2)
	finish := make(chan struct{})
	a := newTestApp(t, Options{CheckUpdates: func(ctx context.Context, force bool) error {
		started <- force
		select {
		case <-finish:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}, UpdateStatus: func() M {
		return M{"state": "available", "installed": "0.51.5", "available": "0.51.9", "message": "Ready", "checked_at": 0.0}
	}})
	select {
	case force := <-started:
		if force {
			t.Fatal("startup forced check")
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not check")
	}
	reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"units": "C"}}))
	if reply["ok"] != true {
		t.Fatal("update blocked controls", reply)
	}
	if object(object(reply["snapshot"])["update"])["state"] != "checking" {
		t.Fatal("missing progress", reply)
	}
	close(finish)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if object(a.Snapshot()["update"])["state"] == "available" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if object(a.Snapshot()["update"])["state"] != "available" {
		t.Fatal("completion missing")
	}
	reply, _ = a.Handle(context.Background(), request("check_updates", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	select {
	case force := <-started:
		if !force {
			t.Fatal("manual check not forced")
		}
	case <-time.After(time.Second):
		t.Fatal("manual check missing")
	}
}

func TestOfflineAppDoesNotAutomaticallyCheck(t *testing.T) {
	a := newTestApp(t, Options{Offline: true, CheckUpdates: func(context.Context, bool) error { t.Error("offline automatic check"); return nil }})
	a.Tick(context.Background())
}
