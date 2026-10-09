package app

import (
	"errors"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/notifications"
)

func (a *App) tickWarnings() {
	if a.closed || a.warnings == nil {
		return
	}
	a.warningDesktop.setEnabled(a.warnings.DeliveryRequested())
	ready, revision := a.warningDesktop.deliveryState()
	if revision != a.warningNativeRevision {
		a.warningNativeRevision = revision
		a.warnings.SetDeliveryReady(false)
		if a.alerts != nil {
			a.alerts.invalidateMonitor(a.options.Now())
		}
	}
	a.warnings.SetDeliveryReady(ready)
	a.warnings.Tick(a.primary.location, stringOf(a.primary.country), !a.options.Offline && !a.primary.needsResolve && !a.primary.locationBusy && a.alerts != nil, a.options.Now())
}
func (a *App) configureWarnings(patch M) error {
	if a.warnings == nil {
		return errors.New("warnings unavailable")
	}
	capable, _, _ := a.warningDesktop.status()
	if patch["enabled"] == true && (!capable || a.alerts == nil) {
		return errors.New("native warning delivery unavailable")
	}
	return a.warnings.Configure(patch)
}
func (a *App) warningSnapshot() M {
	if a.warnings == nil {
		return M{"settings": notifications.WarningDefaults(), "state": "off", "reason": "", "paused_until": nil, "supported": false, "delivery": "none", "fetched_at": nil, "complete": false, "last": nil, "recent": []any{}, "actions": false, "ready": false}
	}
	v := a.warnings.Snapshot()
	capable, ready, actions := a.warningDesktop.status()
	v["supported"], v["ready"], v["actions"] = capable && a.alerts != nil, ready, actions
	return v
}
func (a *App) warningDetail(id float64, query M) M {
	reply := M{"version": 1.0, "request_id": id, "ok": false, "error": "invalid_request"}
	location, key := stringOf(query["location"]), stringOf(query["key"])
	if len(query) != 2 || !warningHex(location, 64) || !warningHex(key, 64) {
		return reply
	}
	reply["error"] = "warning_unavailable"
	if a.warnings == nil {
		return reply
	}
	notice, message, ok := a.warnings.Detail(location, key, a.options.Now())
	if !ok {
		return reply
	}
	zone, err := time.LoadLocation(notice.Timezone)
	if err != nil {
		zone = time.UTC
	}
	label := func(t time.Time) string { return t.In(zone).Format("Mon Jan 2, 3:04 PM MST") }
	result := M{"version": 1.0, "request_id": id, "ok": true, "warning": M{
		"location": location, "key": key, "place": notice.Place, "kind": notice.Kind,
		"timezone": zone.String(), "sent_label": label(message.Identity.Sent), "effective_label": label(message.Effective), "expires_label": label(message.Expires),
		"event": message.Event, "issuer": message.Issuer, "headline": message.Headline,
		"description": message.Description, "instruction": message.Instruction, "area": message.Area,
		"severity": message.Severity, "urgency": message.Urgency, "certainty": message.Certainty,
		"sent": message.Identity.Sent.UTC().Format(time.RFC3339), "effective": message.Effective.UTC().Format(time.RFC3339),
		"expires": message.Expires.UTC().Format(time.RFC3339)}}
	if _, err := ipc.Encode(result, ipc.ResponseLimit); err != nil {
		reply["error"] = "warning_detail_too_large"
		return reply // Preserve full instructions; never silently cut to fit IPC.
	}
	return result
}
