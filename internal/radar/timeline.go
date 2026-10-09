// Package radar provides bounded, on-demand NOAA MRMS radar imagery. It does
// not start workers, poll, or cache frames; the viewing service owns demand.
package radar

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	MaxMetadataBytes = 128 * 1024
	MaxFrames        = 90
	History          = 2 * time.Hour
	StaleAfter       = 15 * time.Minute
	ExpireAfter      = 30 * time.Minute
	namespace        = "http://www.opengis.net/wms"
	layerName        = "conus_bref_qcd"
	styleName        = "radar_reflectivity"
)

// Coverage is the service footprint, not a promise of radar coverage at every
// point. A transparent pixel must never be presented as proof of dry weather.
type Coverage struct{ West, South, East, North float64 }

func (b Coverage) valid() bool {
	return finite(b.West) && finite(b.East) && finite(b.South) && finite(b.North) && b.West >= -180 && b.East <= 180 && b.South >= -85 && b.North <= 85 && b.West < b.East && b.South < b.North
}
func (b Coverage) Contains(lat, lon float64) bool {
	return finite(lat) && finite(lon) && lat >= b.South && lat <= b.North && lon >= b.West && lon <= b.East && b.valid()
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

type Timeline struct {
	fetchedAt time.Time
	times     []time.Time
	coverage  Coverage
}

func (t Timeline) FetchedAt() time.Time { return t.fetchedAt }
func (t Timeline) Coverage() Coverage   { return t.coverage }
func (t Timeline) Times() []time.Time   { return slices.Clone(t.times) }
func (t Timeline) Latest() time.Time {
	if len(t.times) == 0 {
		return time.Time{}
	}
	return t.times[len(t.times)-1]
}

// Freshness reflects the newest composite, not the age of a deliberately
// selected historical frame. Failed refreshes are tracked by the service.
func (t Timeline) Freshness(now time.Time) string {
	latest := t.Latest()
	if now.IsZero() || latest.IsZero() || now.Before(latest.Add(-time.Minute)) || now.Before(t.fetchedAt.Add(-time.Minute)) || now.Sub(latest) > ExpireAfter {
		return "unavailable"
	}
	if now.Sub(latest) > StaleAfter || now.Sub(t.fetchedAt) > 5*time.Minute {
		return "stale"
	}
	return "current"
}

type xmlLayer struct {
	Name   string
	CRS    []string
	Bounds struct {
		West  *float64 `xml:"westBoundLongitude"`
		East  *float64 `xml:"eastBoundLongitude"`
		South *float64 `xml:"southBoundLatitude"`
		North *float64 `xml:"northBoundLatitude"`
	} `xml:"EX_GeographicBoundingBox"`
	Dimensions []struct {
		Name   string `xml:"name,attr"`
		Units  string `xml:"units,attr"`
		Values string `xml:",chardata"`
	} `xml:"Dimension"`
	Styles []struct{ Name string } `xml:"Style"`
	Layers []xmlLayer              `xml:"Layer"`
}

func ParseTimeline(body []byte, now time.Time) (Timeline, error) {
	invalid := errors.New("invalid radar capabilities")
	if now.IsZero() || len(body) == 0 || len(body) > MaxMetadataBytes {
		return Timeline{}, invalid
	}
	// Bound nesting before decoding recursive layers. No DTD/entity expansion or
	// foreign-namespace elements are part of this narrowly supported document.
	decoder := xml.NewDecoder(bytes.NewReader(body))
	depth, nodes, roots := 0, 0, 0
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Timeline{}, invalid
		}
		switch tok := tok.(type) {
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
			nodes++
			if depth > 16 || nodes > 4096 || roots > 1 || tok.Name.Space != namespace {
				return Timeline{}, invalid
			}
		case xml.EndElement:
			depth--
		case xml.Directive:
			return Timeline{}, invalid
		case xml.CharData:
			if depth == 0 && strings.TrimSpace(string(tok)) != "" {
				return Timeline{}, invalid
			}
		}
	}
	var doc struct {
		XMLName    xml.Name `xml:"http://www.opengis.net/wms WMS_Capabilities"`
		Version    string   `xml:"version,attr"`
		Capability struct {
			Request struct {
				GetMap struct {
					Formats []string `xml:"Format"`
				}
			}
			Layer xmlLayer
		}
	}
	if depth != 0 || roots != 1 || xml.Unmarshal(body, &doc) != nil || doc.Version != "1.3.0" || !slices.Contains(doc.Capability.Request.GetMap.Formats, "image/png") {
		return Timeline{}, invalid
	}
	var found *xmlLayer
	var mercator bool
	var walk func(*xmlLayer, bool)
	matches := 0
	walk = func(l *xmlLayer, inherited bool) {
		inherited = inherited || slices.Contains(l.CRS, "EPSG:3857")
		if l.Name == layerName {
			found = l
			mercator = inherited
			matches++
		}
		for i := range l.Layers {
			walk(&l.Layers[i], inherited)
		}
	}
	walk(&doc.Capability.Layer, false)
	if matches != 1 || !mercator || found.Bounds.West == nil || found.Bounds.East == nil || found.Bounds.South == nil || found.Bounds.North == nil {
		return Timeline{}, invalid
	}
	bounds := Coverage{*found.Bounds.West, *found.Bounds.South, *found.Bounds.East, *found.Bounds.North}
	style := false
	for _, s := range found.Styles {
		if s.Name == styleName {
			style = true
		}
	}
	if !bounds.valid() || !style {
		return Timeline{}, invalid
	}
	value := ""
	dimensions := 0
	for _, d := range found.Dimensions {
		if d.Name == "time" {
			if d.Units != "ISO8601" {
				return Timeline{}, invalid
			}
			value = d.Values
			dimensions++
		}
	}
	values := strings.Split(strings.TrimSpace(value), ",")
	if dimensions != 1 || len(values) > MaxFrames || value == "" {
		return Timeline{}, invalid
	}
	result := Timeline{fetchedAt: now.UTC(), coverage: bounds}
	for _, raw := range values {
		t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
		if err != nil || t.After(now.Add(time.Minute)) {
			return Timeline{}, invalid
		}
		// Old frames can remain in an outage's capabilities; discard them rather
		// than minting current timestamps or erasing a still useful recent frame.
		if !t.Before(now.Add(-History)) {
			result.times = append(result.times, t.UTC())
		}
	}
	slices.SortFunc(result.times, func(a, b time.Time) int { return a.Compare(b) })
	result.times = slices.CompactFunc(result.times, func(a, b time.Time) bool { return a.Equal(b) })
	if len(result.times) == 0 {
		return Timeline{}, ErrUnavailable
	}
	return result, nil
}
