package effects

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"time"
)

type object = map[string]any

var instancePattern = regexp.MustCompile(`^[a-f0-9]+_[0-9]+_[0-9]+$`)
var outputPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func number(v any) (float64, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case int:
		n = float64(x)
	case int64:
		n = float64(x)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
func integer(v any) (int64, bool) {
	n, ok := number(v)
	return int64(n), ok && math.Trunc(n) == n && n >= 0 && n <= 9007199254740991
}
func num(v any) float64 { n, _ := number(v); return n }
func obj(v any) object  { m, _ := v.(map[string]any); return m }
func text(v any) string { s, _ := v.(string); return s }
func controls(value object) (object, error) {
	result := object{"reduced_motion": false, "window_physics": true, "accumulation": true, "lightning_enabled": false, "fps": 30}
	if value == nil {
		return nil, errors.New("invalid effects controls")
	}
	for k, d := range result {
		v, ok := value[k]
		if !ok {
			v = d
		}
		if k == "fps" {
			n, ok := integer(v)
			if !ok || (n != 15 && n != 30 && n != 60) {
				return nil, errors.New("effects fps must be 15, 30 or 60")
			}
			v = int(n)
		} else {
			if _, ok := v.(bool); !ok {
				return nil, errors.New("effects flags must be boolean")
			}
		}
		result[k] = v
	}
	return result, nil
}
func validateOutput(m object) error {
	n, ok := integer(m["transform"])
	if !ok || n != 0 || m["mirrorOf"] != "none" {
		return errors.New("unsupported output")
	}
	colors := map[string]bool{"srgb": true, "wide": true, "edid": true, "dcip3": true, "dp3": true, "adobe": true}
	if !colors[text(m["colorManagementPreset"])] {
		return errors.New("unsupported output color")
	}
	for _, k := range []string{"width", "height", "scale", "x", "y"} {
		if _, ok := number(m[k]); !ok {
			return errors.New("unsupported output geometry")
		}
	}
	w, h, s, x, y := num(m["width"]), num(m["height"]), num(m["scale"]), num(m["x"]), num(m["y"])
	if s <= 0 || s > 8 || w/s < .5 || w/s >= 16384.5 || h/s < .5 || h/s >= 16384.5 || math.Abs(x) > 32768 || math.Abs(y) > 32768 {
		return errors.New("unsupported output geometry")
	}
	return nil
}
func monitorRecords(value any) ([]any, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) < 1 || len(rows) > 32 {
		return nil, errors.New("output_unavailable")
	}
	result := []any{}
	names := map[string]bool{}
	for _, r := range rows {
		m := obj(r)
		name := text(m["name"])
		disabled, ok := m["disabled"].(bool)
		if !ok || !outputPattern.MatchString(name) || names[name] {
			return nil, errors.New("output_unavailable")
		}
		names[name] = true
		w, wok := number(m["width"])
		h, hok := number(m["height"])
		s, sok := number(m["scale"])
		if !wok || !hok || !sok || w < 0 || w > 32768 || h < 0 || h > 32768 || s < .25 || s > 8 || (!disabled && (w == 0 || h == 0)) {
			return nil, errors.New("output_unavailable")
		}
		result = append(result, object{"name": name, "width": w, "height": h, "scale": s, "enabled": !disabled})
	}
	return result, nil
}
func temperatureLease(selected object, now time.Time) (float64, int64, bool) {
	v, ok := number(obj(selected["current"])["temperature_c"])
	schema, valid := integer(selected["schema_version"])
	if !ok || !valid || schema != 1 || selected["mode"] != "live" || selected["freshness"] != "fresh" || v < -100 || v > 65 {
		return 0, 0, false
	}
	until := now.Add(8 * time.Second)
	for _, record := range []struct {
		v        any
		duration time.Duration
	}{{selected["selected_at"], 8 * time.Second}, {obj(selected["forecast"])["fetched_at"], 2700 * time.Second}, {obj(selected["current"])["time"], 2700 * time.Second}} {
		s := text(record.v)
		if len(s) > 64 {
			return 0, 0, false
		}
		stamp, e := time.Parse(time.RFC3339Nano, s)
		if e != nil || now.Before(stamp) || now.Sub(stamp) > record.duration {
			return 0, 0, false
		}
		expiry := stamp.Add(record.duration)
		if expiry.Before(until) {
			until = expiry
		}
	}
	return v, until.UnixMilli(), until.UnixMilli() > now.UnixMilli()
}
func formatted(v any) string {
	if b, ok := v.(bool); ok {
		return strconv.FormatBool(b)
	}
	return strconv.FormatFloat(num(v), 'g', 9, 64)
}
