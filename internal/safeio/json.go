// Package safeio implements bounded JSON and descriptor-relative private state.
package safeio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// Decode accepts one UTF-8 JSON value within limit bytes and 16 nesting levels.
// Duplicate object keys, trailing values, and non-finite numbers are rejected.
// Numbers are decoded as float64, matching the persisted protocol representation.
func Decode(raw []byte, limit int) (any, error) {
	if len(raw) > limit || !utf8.Valid(raw) {
		return nil, errors.New("JSON size or UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	v, err := value(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}
func value(d *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("JSON nesting")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			s, ok := k.(string)
			if !ok {
				return nil, errors.New("JSON key")
			}
			if _, ok = m[s]; ok {
				return nil, fmt.Errorf("duplicate JSON field")
			}
			v, e := value(d, depth+1)
			if e != nil {
				return nil, e
			}
			m[s] = v
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, errors.New("JSON object")
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, e := value(d, depth+1)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, errors.New("JSON array")
		}
		return a, nil
	default:
		if _, ok := t.(json.Delim); ok {
			return nil, errors.New("JSON delimiter")
		}
		return t, nil
	}
}

// Object applies Decode's limits and additionally requires a JSON object.
func Object(raw []byte, limit int) (map[string]any, error) {
	v, e := Decode(raw, limit)
	if e != nil {
		return nil, e
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("JSON object required")
	}
	return m, nil
}

// Clone returns an independent JSON copy of a trusted internal object.
// It preserves nil and normalizes numbers to float64. It panics if m cannot
// satisfy Decode's JSON contract; use CloneChecked for fallible input boundaries.
func Clone(m map[string]any) map[string]any {
	v, err := CloneChecked(m)
	if err != nil {
		panic(err)
	}
	return v
}

// CloneChecked copies an object using the same JSON representation and nesting
// limits as Decode. It returns encoding/validation errors without modifying m.
func CloneChecked(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	raw, e := json.Marshal(m)
	if e != nil {
		return nil, e
	}
	return Object(raw, len(raw))
}
