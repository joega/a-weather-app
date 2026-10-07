package app

import (
	"context"
	"errors"
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

func TestUpdateLaunchTimeoutAllowsRetry(t *testing.T) {
	now := time.Now()
	starts := 0
	a := newTestApp(t, Options{Offline: true, Now: func() time.Time { return now }, StartUpdate: func() error { starts++; return nil }, UpdateStatus: func() M {
		return M{"state": "available", "installed": "0.51.5", "available": "0.51.9", "message": "Ready", "checked_at": 0.0}
	}})
	r, _ := a.Handle(context.Background(), request("install_update", nil))
	if r["ok"] != true {
		t.Fatal(r)
	}
	now = now.Add(61 * time.Second)
	a.Tick(context.Background())
	if u := object(a.Snapshot()["update"]); u["state"] != "failed" {
		t.Fatal("launch stayed pending forever", u)
	}
	r, _ = a.Handle(context.Background(), request("install_update", nil))
	if r["ok"] != true || starts != 2 {
		t.Fatal("retry unavailable", r, starts)
	}
}

func TestManualCheckReportsUnpersistedFailure(t *testing.T) {
	a := newTestApp(t, Options{Offline: true, CheckUpdates: func(context.Context, bool) error {
		return errors.New("status persistence failed")
	}, UpdateStatus: func() M { return M{"state": "current"} }})
	a.mu.Lock()
	a.options.Offline = false // Keep startup offline, then exercise a manual check.
	a.beginUpdateCheck(true)
	done := a.updates.done
	a.mu.Unlock()
	select {
	case result := <-done:
		done <- result // Restore the result for the application's normal poll path.
	case <-time.After(time.Second):
		t.Fatal("check did not complete")
	}
	snapshot := object(a.Snapshot()["update"])
	if snapshot["state"] != "failed" || snapshot["message"] != "Could not check for updates. Try again." {
		t.Fatalf("manual check failure was hidden: %v", snapshot)
	}
}
