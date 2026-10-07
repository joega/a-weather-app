package app

import (
	"context"
	"reflect"
	"time"
)

type updateState struct {
	checking       bool
	next           time.Time
	done           chan struct{}
	installing     bool
	installStarted time.Time
	installError   string
	lastStatus     M
}

func (a *App) beginUpdateCheck(force bool) {
	if a.options.CheckUpdates == nil || a.options.Offline || a.updates.checking || (!force && a.options.Now().Before(a.updates.next)) {
		return
	}
	if updateInProgress(a.updateSnapshot()) {
		return
	}
	a.updates.checking = true
	if force {
		a.updates.installError = ""
	}
	a.updates.next = a.options.Now().Add(time.Minute)
	if a.updates.done == nil {
		a.updates.done = make(chan struct{}, 1)
	}
	done := a.updates.done
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		_ = a.options.CheckUpdates(ctx, force)
		done <- struct{}{}
		a.signal()
	}()
}

func (a *App) pollUpdates() {
	if a.options.UpdateStatus != nil {
		status := a.options.UpdateStatus()
		if !reflect.DeepEqual(status, a.updates.lastStatus) {
			a.updates.lastStatus = status
			a.signal()
		}
	}
	if a.updates.installing && a.options.UpdateStatus != nil {
		state := stringOf(a.options.UpdateStatus()["state"])
		if state == "failed" || state == "rolled_back" || state == "updated" || updateInProgress(a.options.UpdateStatus()) {
			a.updates.installing = false
		} else if a.options.Now().Sub(a.updates.installStarted) >= time.Minute {
			a.updates.installing = false
			a.updates.installError = "The updater did not start. Check for updates and try again."
		}
	}
	if a.updates.done == nil {
		return
	}
	select {
	case <-a.updates.done:
		a.updates.checking = false
	default:
	}
}

func (a *App) updateSnapshot() M {
	v := M{"state": "unsupported", "installed": "", "available": "", "message": "Updates are unavailable for this installation", "checked_at": 0.0}
	if a.options.UpdateStatus != nil {
		v = a.options.UpdateStatus()
	}
	if a.updates.installError != "" {
		v = safeUpdateCopy(v)
		v["state"] = "failed"
		v["message"] = a.updates.installError
	}
	if a.updates.checking {
		v = safeUpdateCopy(v)
		v["state"] = "checking"
		v["message"] = "Checking for updates…"
	}
	if a.updates.installing && !updateInProgress(v) && v["state"] != "failed" {
		v = safeUpdateCopy(v)
		v["state"] = "downloading"
		v["message"] = "Preparing the update…"
	}
	return v
}

func updateInProgress(v M) bool {
	switch stringOf(v["state"]) {
	case "downloading", "verifying", "restarting":
		return true
	}
	return false
}
func safeUpdateCopy(v M) M {
	r := M{}
	for k, x := range v {
		r[k] = x
	}
	return r
}
