package notifications

import (
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"time"
)

// These copied values, rather than references to forecast maps or rows, make
// cache invalidation safe even when a caller revises a forecast in place.
type candidateRow struct {
	stamp  string
	chance float64
	valid  bool
}

func rowValue(v any) candidateRow {
	r, ok := v.(M)
	if !ok {
		return candidateRow{}
	}
	p, ok := r["precipitation_probability"].(float64)
	if !ok || math.IsNaN(p) || p < 0 || p > 1 {
		return candidateRow{}
	}
	return candidateRow{str(r["time"]), p, true}
}

type candidateCache struct {
	rows                 []candidateRow
	ready                bool
	probability          float64
	groups               []interval
	groupNow, groupUntil time.Time
	event                M
	eventReady           bool
	eventNow, eventUntil time.Time
	name, zone           string
	latitude, longitude  float64
}

func earlier(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func (c *candidateCache) candidate(forecast, location M, now time.Time, probability float64, zone *time.Location) (M, time.Time) {
	rows, ok := forecast["hourly"].([]any)
	lat, a := location["latitude"].(float64)
	lon, b := location["longitude"].(float64)
	if !ok || len(rows) > 240 || !a || !b || math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		c.ready = false
		return nil, time.Time{}
	}
	changed := !c.ready || len(rows) != len(c.rows) || probability != c.probability
	if !changed {
		for i, row := range rows {
			if rowValue(row) != c.rows[i] {
				changed = true
				break
			}
		}
	}
	if changed {
		c.rows = make([]candidateRow, len(rows))
		for i, row := range rows {
			c.rows[i] = rowValue(row)
		}
		c.probability, c.ready = probability, true
	}
	if changed || now.Before(c.groupNow) || (!c.groupUntil.IsZero() && !now.Before(c.groupUntil)) {
		wet := make([][2]float64, 0, len(c.rows))
		c.groupNow, c.groupUntil = now, time.Time{}
		for _, row := range c.rows {
			if !row.valid || row.chance < probability/100 {
				continue
			}
			t := stamp(row.stamp)
			if t.IsZero() {
				continue
			}
			enters, leaves := t.Add(-11*24*time.Hour), t.Add(11*24*time.Hour+time.Nanosecond)
			if now.Before(enters) {
				c.groupUntil = earlier(c.groupUntil, enters)
				continue
			}
			if !now.Before(leaves) {
				continue
			}
			c.groupUntil = earlier(c.groupUntil, leaves)
			wet = append(wet, [2]float64{float64(t.Unix()) - 3600, row.chance})
		}
		sort.Slice(wet, func(i, j int) bool { return wet[i][0] < wet[j][0] })
		c.groups = nil
		for _, h := range wet {
			n := len(c.groups)
			if n > 0 && h[0] <= c.groups[n-1].end+10800 {
				c.groups[n-1].end = math.Max(c.groups[n-1].end, h[0]+3600)
				c.groups[n-1].hours = append(c.groups[n-1].hours, h)
			} else {
				c.groups = append(c.groups, interval{h[0], h[0] + 3600, [][2]float64{h}})
			}
		}
		c.eventReady = false
	}
	name, zoneName := str(location["name"]), zone.String()
	if c.name != name || c.zone != zoneName || c.latitude != lat || c.longitude != lon {
		c.name, c.zone, c.latitude, c.longitude = name, zoneName, lat, lon
		c.eventReady = false
	}
	if c.eventReady && !now.Before(c.eventNow) && (c.eventUntil.IsZero() || now.Before(c.eventUntil)) {
		return c.event, c.eventUntil
	}
	c.event, c.eventNow, c.eventUntil, c.eventReady = nil, now, c.groupUntil, true
	instant := float64(now.Unix())
	for _, g := range c.groups {
		for _, h := range g.hours {
			enters, leaves := time.Unix(int64(h[0]-10800), 0), time.Unix(int64(h[0]+3600), 0)
			if now.Before(enters) {
				c.eventUntil = earlier(c.eventUntil, enters)
			}
			if now.Before(leaves) {
				c.eventUntil = earlier(c.eventUntil, leaves)
			}
			if c.event != nil || h[0]+3600 <= instant || h[0] > instant+10800 {
				continue
			}
			start, end := time.Unix(int64(h[0]), 0).In(zone), leaves.In(zone)
			window := start.Format("Mon 3 PM MST") + "–" + end.Format("3 PM MST")
			key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%.4f,%.4f", lat, lon))))
			c.event = M{"location": key, "start": g.start, "end": g.end, "title": "Hourly precipitation outlook", "body": Text(fmt.Sprintf("%s: %.0f%% chance of precipitation for %s. Open-Meteo hourly forecast; timing may change.", Text(name, 96), math.RoundToEven(h[1]*100), window), 320)}
		}
	}
	return c.event, c.eventUntil
}

func quietAt(settings M, now time.Time, zone *time.Location) bool {
	if settings["quiet_enabled"] != true || zone == nil {
		return false
	}
	hour := float64(now.In(zone).Hour())
	start, end := settings["quiet_start"].(float64), settings["quiet_end"].(float64)
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

// Advance through actual local-hour and offset boundaries. Date arithmetic
// alone can choose the wrong occurrence of a repeated hour or normalize a
// nonexistent hour backwards, including zones with half-hour DST changes.
func nextQuietTransition(settings M, now time.Time, zone *time.Location) time.Time {
	if settings["quiet_enabled"] != true || zone == nil {
		return time.Time{}
	}
	quiet := quietAt(settings, now, zone)
	at := now
	for range 72 {
		local := at.In(zone)
		next := at.Add(time.Hour - time.Duration(local.Minute())*time.Minute - time.Duration(local.Second())*time.Second - time.Duration(local.Nanosecond()))
		_, zoneEnd := local.ZoneBounds()
		if !zoneEnd.IsZero() && zoneEnd.After(at) {
			next = earlier(next, zoneEnd)
		}
		if quietAt(settings, next, zone) != quiet {
			return next
		}
		at = next
	}
	return at // Bound pathological timezone histories while still making progress.
}
