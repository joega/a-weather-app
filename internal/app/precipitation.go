package app

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/joega/a-weather-app/internal/precipitation"
	"github.com/joega/a-weather-app/internal/weather"
)

const precipitationFile = "precipitation-detail.json"

// Only the socket server attaches a peer identity to its admitted commands.
// The one optional detail view belongs to that peer, not aggregate visibility.
type precipitationState struct {
	controller                   *precipitation.Controller
	owner                        *peer
	open                         bool
	query                        M
	revision, controllerRevision uint64
	dayKey                       string
	packetRevision               uint64
}

func (a *App) precipitationRequest(ctx context.Context, id float64, query M, closeView bool) M {
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
		if a.precipitation != nil && a.precipitation.owner == owner && a.precipitation.query["client_token"] == token {
			a.closePrecipitation()
		}
		return M{"version": 1.0, "request_id": id, "ok": true}
	}
	if !savedFields(query, "location_id", "latitude", "longitude", "timezone", "date", "client_token") {
		return reply
	}
	date, ok := query["date"].(string)
	if !ok || len(date) > 10 {
		return reply
	}
	reply["error"] = "precipitation_context_changed"
	if a.primaryOnly || a.forecastPoint == nil || a.mode == "default" || a.locationBusy || a.location == nil || a.id == "" || query["location_id"] != a.id || owner == nil && !a.presented {
		return reply
	}
	for _, key := range []string{"latitude", "longitude", "timezone"} {
		if query[key] != a.location[key] {
			return reply
		}
	}
	zone, err := time.LoadLocation(stringOf(a.location["timezone"]))
	if err != nil {
		return reply
	}
	now := a.options.Now()
	if now.IsZero() {
		return reply
	}
	today, last := now.In(zone).Format(time.DateOnly), now.In(zone).AddDate(0, 0, 9).Format(time.DateOnly)
	if date != "" {
		_, err = time.Parse(time.DateOnly, date)
		if err != nil || date < today || date > last {
			reply["error"] = "precipitation_date_unavailable"
			return reply
		}
	}
	if a.precipitation != nil && a.precipitation.open && a.precipitation.owner != owner {
		reply["error"] = "precipitation_in_use"
		return reply
	}
	if a.precipitation == nil {
		a.precipitation = &precipitationState{controller: precipitation.NewController(precipitation.Dependencies{
			Fetch: a.options.FetchPrecipitation, Offline: a.options.Offline, Notify: a.signalPrecipitation,
			Load: func() (*precipitation.Data, error) {
				if a.state == nil {
					return nil, errors.New("state unavailable")
				}
				record, err := a.state.Read(precipitationFile, precipitation.CacheBytes)
				if err != nil || record == nil {
					return nil, err
				}
				data, err := precipitation.ReadCache(record)
				return &data, err
			},
			Save: func(data precipitation.Data) error {
				if a.state == nil {
					return errors.New("state unavailable")
				}
				record, err := precipitation.CacheRecord(data)
				if err != nil {
					return err
				}
				return a.state.Write(precipitationFile, record, precipitation.CacheBytes)
			},
		})}
	}
	p := a.precipitation
	if err := p.controller.Open(a.location, now); err != nil {
		return reply
	}
	p.owner, p.open, p.query = owner, true, weather.Clone(query).(M)
	_, state := p.controller.Since(nil, now)
	a.signalPrecipitation()
	return M{"version": 1.0, "request_id": id, "ok": true, "precipitation": a.precipitationPresentation(state, now)}
}

func (a *App) closePrecipitation() {
	if p := a.precipitation; p != nil && p.open {
		p.controller.Close(a.options.Now())
		p.owner, p.open = nil, false
		a.signalPrecipitation()
	}
}

func (a *App) closePrecipitationFor(owner *peer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.precipitation != nil && a.precipitation.owner == owner {
		a.closePrecipitation()
	}
}

func (a *App) pollPrecipitation() {
	if a.precipitation != nil {
		a.precipitation.controller.Poll(a.options.Now())
	}
}

func (a *App) signalPrecipitation() {
	select {
	case a.PrecipitationChanged <- struct{}{}:
	default:
	}
}

func (a *App) precipitationSince(last *uint64) (uint64, M, *peer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.precipitation
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
	zone, _ := time.LoadLocation(stringOf(p.query["timezone"]))
	dayKey := now.In(zone).Format(time.DateOnly)
	if p.dayKey != dayKey {
		p.revision++
		p.dayKey = dayKey
	}
	if last != nil && *last == p.revision {
		return p.revision, nil, nil
	}
	if state == nil {
		_, state = p.controller.Since(nil, now)
	}
	return p.revision, a.precipitationPresentation(state, now), p.owner
}

// The ordinary forecast snapshot stays unchanged. This narrow payload contains
// ten daily summaries and only the selected day's fully contained hourly rows.
func (a *App) precipitationPresentation(state *precipitation.Presentation, now time.Time) M {
	p := a.precipitation
	p.packetRevision++
	zone, _ := time.LoadLocation(stringOf(a.location["timezone"]))
	today := now.In(zone)
	date := stringOf(p.query["date"])
	if date == "" || date < today.Format(time.DateOnly) {
		date = today.Format(time.DateOnly)
	}
	stamp := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t.UTC().Format(time.RFC3339)
	}
	value := func(v precipitation.Value) any {
		if v.Known {
			return v.Number
		}
		return nil
	}
	view := M{"location_id": a.id, "latitude": a.location["latitude"], "longitude": a.location["longitude"], "timezone": zone.String(), "client_token": p.query["client_token"], "requested_date": p.query["date"], "date": date, "today": today.Format(time.DateOnly), "max_date": today.AddDate(0, 0, 9).Format(time.DateOnly), "status": state.Status, "refreshing": state.Refreshing, "error": state.Error, "save_failed": state.SaveFailed, "retry_at": stamp(state.RetryAt), "source": precipitation.Source, "fetched_at": nil, "fetched_label": "", "days": []any{}, "hours": []any{}}
	view["revision"] = p.packetRevision
	if state.Data == nil {
		return view
	}
	view["fetched_at"], view["fetched_label"] = stamp(state.Data.FetchedAt), state.Data.FetchedAt.In(zone).Format("Mon Jan 2, 3:04 PM MST")
	days, hours := []any{}, []any{}
	for i := 0; i < 10; i++ {
		key := today.AddDate(0, 0, i).Format(time.DateOnly)
		d, err := precipitation.Summarize(*state.Data, key)
		if err != nil {
			continue
		}
		row := M{"date": key, "label": d.Start.In(zone).Format("Mon Jan 2"), "duration_hours": d.End.Sub(d.Start).Hours(), "available_hours": len(d.Hours), "boundary_hours": d.BoundaryHours, "kind": d.Kind, "depth_min_m": value(d.DepthM.Min), "depth_max_m": value(d.DepthM.Max), "depth_samples": d.DepthM.Samples, "freezing_min_m": value(d.FreezingM.Min), "freezing_max_m": value(d.FreezingM.Max), "freezing_samples": d.FreezingM.Samples, "chance_max": value(d.Probability.Max), "chance_samples": d.Probability.Samples}
		row["start"], row["end"] = stamp(d.Start), stamp(d.End)
		for key, amount := range map[string]precipitation.Accumulation{"total_mm": d.TotalMM, "rain_mm": d.RainMM, "snow_cm": d.SnowCM, "wet_hours": d.WetHours} {
			row[key], row[key+"_subtotal"], row[key+"_coverage"] = value(amount.Total), value(amount.Subtotal), amount.Covered.Hours()
		}
		days = append(days, row)
		if key != date {
			continue
		}
		for _, h := range d.Hours {
			begin := h.End.Add(-time.Hour)
			hours = append(hours, M{"start": stamp(begin), "end": stamp(h.End), "label": begin.In(zone).Format("3:04 PM MST") + " – " + h.End.In(zone).Format("3:04 PM MST"), "total_mm": value(h.TotalMM), "rain_mm": value(h.RainAmount()), "snow_cm": value(h.SnowCM), "depth_m": value(h.DepthM), "freezing_m": value(h.FreezingM), "chance": value(h.Probability), "kind": h.Kind()})
		}
	}
	view["days"], view["hours"] = days, hours
	return view
}
