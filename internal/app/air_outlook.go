package app

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/airquality"
	"github.com/joega/a-weather-app/internal/weather"
)

const airOutlookFile = "air-quality-outlook.json"

// Only the socket server attaches a peer identity to its admitted commands.
// The one optional detail view belongs to that peer, not aggregate visibility.
type airOutlookState struct {
	controller                   *airquality.OutlookController
	owner                        *peer
	open                         bool
	query                        M
	revision, controllerRevision uint64
	hourKey                      string
	packetRevision               uint64
}

func (a *App) airOutlookRequest(ctx context.Context, id float64, query M, closeView bool) M {
	reply := M{"version": 1.0, "request_id": id, "ok": false, "error": "invalid_request"}
	owner, _ := ctx.Value(presentationPeerKey{}).(*peer)
	token, valid := query["client_token"].(float64)
	if !valid || math.IsNaN(token) || math.IsInf(token, 0) || token < 0 || token > 2147483647 || math.Trunc(token) != token {
		return reply
	}
	if closeView {
		if !savedFields(query, "client_token") {
			return reply
		}
		if a.airOutlook != nil && a.airOutlook.owner == owner && a.airOutlook.query["client_token"] == token {
			a.closeAirOutlook()
		}
		return M{"version": 1.0, "request_id": id, "ok": true}
	}
	if !savedFields(query, "location_id", "latitude", "longitude", "timezone", "client_token") {
		return reply
	}
	reply["error"] = "air_outlook_context_changed"
	if a.primaryOnly || a.forecastPoint == nil || a.mode == "default" || a.locationBusy || a.location == nil || a.id == "" || query["location_id"] != a.id || owner == nil && !a.presented {
		return reply
	}
	for _, key := range []string{"latitude", "longitude", "timezone"} {
		if query[key] != a.location[key] {
			return reply
		}
	}
	if _, err := time.LoadLocation(stringOf(a.location["timezone"])); err != nil {
		return reply
	}
	now := a.options.Now()
	if now.IsZero() {
		return reply
	}
	if a.airOutlook != nil && a.airOutlook.open && a.airOutlook.owner != owner {
		reply["error"] = "air_outlook_in_use"
		return reply
	}
	if a.airOutlook == nil {
		a.airOutlook = &airOutlookState{controller: airquality.NewOutlookController(airquality.OutlookDependencies{
			Fetch: a.options.FetchAirOutlook, Offline: a.options.Offline, Notify: a.signalAirOutlook,
			Load: func() (*airquality.Outlook, error) {
				if a.state == nil {
					return nil, errors.New("state unavailable")
				}
				record, err := a.state.Read(airOutlookFile, airquality.OutlookCacheBytes)
				if err != nil || record == nil {
					return nil, err
				}
				data, err := airquality.ReadOutlookCache(record)
				return &data, err
			},
			Save: func(data airquality.Outlook) error {
				if a.state == nil {
					return errors.New("state unavailable")
				}
				record, err := airquality.OutlookCacheRecord(data)
				if err != nil {
					return err
				}
				return a.state.Write(airOutlookFile, record, airquality.OutlookCacheBytes)
			},
		})}
	}
	p := a.airOutlook
	if err := p.controller.Open(a.location, now); err != nil {
		return reply
	}
	p.owner, p.open, p.query = owner, true, weather.Clone(query).(M)
	_, state := p.controller.Since(nil, now)
	a.signalAirOutlook()
	return M{"version": 1.0, "request_id": id, "ok": true, "air_outlook": a.airOutlookPresentation(state, now)}
}

func (a *App) closeAirOutlook() {
	if p := a.airOutlook; p != nil && p.open {
		p.controller.Close(a.options.Now())
		p.owner, p.open = nil, false
		a.signalAirOutlook()
	}
}

func (a *App) closeAirOutlookFor(owner *peer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.airOutlook != nil && a.airOutlook.owner == owner {
		a.closeAirOutlook()
	}
}

func (a *App) pollAirOutlook() {
	if a.airOutlook != nil {
		a.airOutlook.controller.Poll(a.options.Now())
	}
}

func (a *App) signalAirOutlook() {
	select {
	case a.AirOutlookChanged <- struct{}{}:
	default:
	}
}

func (a *App) airOutlookSince(last *uint64) (uint64, M, *peer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.airOutlook
	if p == nil {
		return 0, nil, nil
	}
	now := a.options.Now()
	revision, state := p.controller.Since(&p.controllerRevision, now)
	if revision != p.controllerRevision {
		p.revision++
		p.controllerRevision = revision
	}
	if !p.open {
		return p.revision, nil, nil
	}
	hourKey := now.UTC().Truncate(time.Hour).Format(time.RFC3339)
	if p.hourKey != hourKey {
		p.revision++
		p.hourKey = hourKey
	}
	if last != nil && *last == p.revision {
		return p.revision, nil, nil
	}
	if state == nil {
		_, state = p.controller.Since(nil, now)
	}
	return p.revision, a.airOutlookPresentation(state, now), p.owner
}

// The ordinary snapshot stays small. This view presents 48 upcoming UTC hours
// with local IANA labels; missing samples remain null and visibly break charts.
func (a *App) airOutlookPresentation(state *airquality.OutlookPresentation, now time.Time) M {
	p := a.airOutlook
	p.packetRevision++
	zone, _ := time.LoadLocation(stringOf(a.location["timezone"]))
	start := now.UTC().Truncate(time.Hour)
	stamp := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	view := M{"revision": p.packetRevision, "location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": zone.String(), "client_token": p.query["client_token"], "status": state.Status, "refreshing": state.Refreshing, "error": state.Error, "save_failed": state.SaveFailed, "retry_at": stamp(state.RetryAt), "source": "CAMS global model data", "attribution": airquality.Attribution, "fetched_at": nil, "fetched_label": "", "start": stamp(start), "end": stamp(start.Add(48 * time.Hour)), "hours": []any{}}
	if state.Outlook == nil {
		return view
	}
	data := state.Outlook
	view["fetched_at"], view["fetched_label"] = stamp(data.FetchedAt), data.FetchedAt.In(zone).Format("Mon Jan 2, 3:04 PM MST")
	samples := make(map[int64]airquality.OutlookHour, len(data.Hours))
	for _, h := range data.Hours {
		samples[h.Time.Unix()] = h
	}
	hours := make([]any, airquality.OutlookHours)
	for i := range hours {
		at := start.Add(time.Duration(i) * time.Hour)
		h := samples[at.Unix()]
		row := M{"time": stamp(at), "label": at.In(zone).Format("Mon Jan 2, 3:04 PM MST (-07:00)"), "local_hour": at.In(zone).Format("Mon 3 PM MST")}
		for k, field := range airquality.OutlookMetrics() {
			row[field.Key] = nil
			if h.Values[k].Known {
				row[field.Key] = h.Values[k].Number
			}
		}
		hours[i] = row
	}
	view["hours"] = hours
	return view
}
