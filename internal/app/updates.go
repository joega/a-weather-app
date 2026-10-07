package app

import (
	"context"
	"time"
)

type updateState struct {
	checking bool
	next     time.Time
	done     chan struct{}
}

func (a *App) beginUpdateCheck(force bool) {
	if a.options.CheckUpdates == nil || a.options.Offline || a.updates.checking || (!force && a.options.Now().Before(a.updates.next)) {
		return
	}
	a.updates.checking = true
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
	if a.updates.checking {
		v = safeUpdateCopy(v)
		v["state"] = "checking"
		v["message"] = "Checking for updates…"
	}
	return v
}
func safeUpdateCopy(v M) M {
	r := M{}
	for k, x := range v {
		r[k] = x
	}
	return r
}
