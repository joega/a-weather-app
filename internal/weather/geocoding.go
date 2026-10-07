package weather

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const geocodingBase = "https://geocoding-api.open-meteo.com"
const geocodingMaxBytes = 64 * 1024

var countryCodePattern = regexp.MustCompile(`^[A-Z]{2}$`)
var zonePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+\-]*(/[A-Za-z0-9_+\-]+)+$`)

// Only one provider search runs at a time. A canceled request releases its slot
// when the HTTP call ends; a newer caller can wait with its own cancellable context.
var searchSlot = make(chan struct{}, 1)

func placeInteger(v any, allowZero bool) (int, bool) {
	n, ok := num(v)
	if !ok || n != math.Trunc(n) || n > math.MaxInt32 || n < 0 || (!allowZero && n == 0) {
		return 0, false
	}
	return int(n), true
}

func safePlaceText(v any, limit int, optional bool) (string, error) {
	if v == nil && optional {
		return "", nil
	}
	s, ok := v.(string)
	if !ok || len([]rune(s)) > limit {
		return "", errors.New("invalid place text")
	}
	for _, r := range s {
		if r == '<' || r == '>' || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || !unicode.IsPrint(r) {
			return "", errors.New("invalid place text")
		}
	}
	p, e := Plain(s, limit, false)
	if e != nil {
		return "", e
	}
	s = strings.TrimSpace(p.(string))
	if s == "" && !optional {
		return "", errors.New("empty place text")
	}
	return s, nil
}

// admin1 is the existing five-field search-row display field. Include admin2
// when it gives a distinct region, so people can distinguish same-named places.
func placeRegion(row Object) (string, error) {
	admin1, e := safePlaceText(row["admin1"], 120, true)
	if e != nil {
		return "", e
	}
	admin2, e := safePlaceText(row["admin2"], 120, true)
	if e != nil {
		return "", e
	}
	if admin2 == "" || strings.EqualFold(admin1, admin2) {
		return admin1, nil
	}
	if admin1 == "" {
		return admin2, nil
	}
	region := admin1 + ", " + admin2
	if len([]rune(region)) > 244 {
		return "", errors.New("invalid place region")
	}
	return region, nil
}

func placeDisplayLabel(name, region, country string) string {
	if region != "" {
		return name + ", " + region + ", " + country
	}
	return name + ", " + country
}

func ValidatePlaceSearch(v Object) (Object, error) {
	if len(v) != 2 && len(v) != 3 {
		return nil, errors.New("invalid place search")
	}
	token := float64(0)
	if raw, present := v["client_token"]; present {
		value, ok := placeInteger(raw, true)
		if !ok {
			return nil, errors.New("invalid search client token")
		}
		token = float64(value)
	} else if len(v) == 3 {
		return nil, errors.New("invalid place search field")
	}
	query, ok := v["query"].(string)
	if !ok {
		return nil, errors.New("invalid place query")
	}
	for _, r := range query {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || !unicode.IsPrint(r) {
			return nil, errors.New("invalid place query character")
		}
	}
	query = strings.TrimSpace(query)
	if n := len([]rune(query)); n < 2 || n > 120 {
		return nil, errors.New("invalid place query length")
	}
	country, ok := v["country_code"].(string)
	if !ok || (country != "" && !countryCodePattern.MatchString(country)) {
		return nil, errors.New("invalid country code")
	}
	return Object{"query": query, "country_code": country, "client_token": token}, nil
}

func geocodingTree(v any) error {
	nodes := 0
	var visit func(any, int) error
	visit = func(v any, depth int) error {
		nodes++
		if nodes > 2048 || depth > 5 {
			return errors.New("geocoding structure limit")
		}
		switch x := v.(type) {
		case Object:
			if len(x) > 32 {
				return errors.New("geocoding object limit")
			}
			for k, item := range x {
				if len(k) > 64 {
					return errors.New("geocoding key limit")
				}
				if e := visit(item, depth+1); e != nil {
					return e
				}
			}
		case []any:
			if len(x) > 512 {
				return errors.New("geocoding array limit")
			}
			for _, item := range x {
				if e := visit(item, depth+1); e != nil {
					return e
				}
			}
		case string:
			if len([]rune(x)) > 256 {
				return errors.New("geocoding string limit")
			}
		}
		return nil
	}
	return visit(v, 0)
}

func (provider jsonProvider) geocodingJSON(ctx context.Context, path string, q url.Values) (Object, error) {
	if path != "/v1/search" && path != "/v1/get" {
		return nil, errors.New("invalid geocoding path")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	p, e := provider.fetchJSONBound(ctx, geocodingBase+path+"?"+q.Encode(), false, geocodingMaxBytes)
	if e != nil {
		return nil, e
	}
	if e = geocodingTree(p); e != nil {
		return nil, e
	}
	return p, nil
}

func SearchPlaces(ctx context.Context, request Object) ([]any, error) {
	return defaultJSONProvider().searchPlaces(ctx, request)
}

func (provider jsonProvider) searchPlaces(ctx context.Context, request Object) ([]any, error) {
	v, e := ValidatePlaceSearch(request)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	select {
	case searchSlot <- struct{}{}:
		defer func() { <-searchSlot }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	q := url.Values{"name": {v["query"].(string)}, "count": {"10"}, "language": {"en"}, "format": {"json"}}
	if country := v["country_code"].(string); country != "" {
		q.Set("countryCode", country)
	}
	p, e := provider.geocodingJSON(ctx, "/v1/search", q)
	if e != nil {
		return nil, e
	}
	if p["error"] != nil && p["error"] != false {
		return nil, errors.New("geocoding provider error")
	}
	if p["results"] == nil {
		return []any{}, nil
	}
	rows, ok := p["results"].([]any)
	if !ok || len(rows) > 10 {
		return nil, errors.New("invalid place results")
	}
	results := make([]Object, 0, len(rows))
	seen := map[int]bool{}
	for _, raw := range rows {
		r := obj(raw)
		id, ok := placeInteger(r["id"], false)
		if !ok || seen[id] {
			return nil, errors.New("invalid or duplicate place id")
		}
		seen[id] = true
		name, e := safePlaceText(r["name"], 120, false)
		if e != nil {
			return nil, e
		}
		admin, e := placeRegion(r)
		if e != nil {
			return nil, e
		}
		country, e := safePlaceText(r["country"], 120, false)
		if e != nil {
			return nil, e
		}
		code, ok := r["country_code"].(string)
		if !ok || !countryCodePattern.MatchString(code) || v["country_code"] != "" && code != v["country_code"] {
			return nil, errors.New("invalid place country")
		}
		results = append(results, Object{"id": float64(id), "name": name, "admin1": admin, "country": country, "country_code": code})
	}
	// Do not choose one member of an ambiguous group. After validating every
	// provider row, omit all members and preserve the remaining result order.
	ambiguous := make([]bool, len(results))
	for i := range results {
		for j := i + 1; j < len(results); j++ {
			left := placeDisplayLabel(results[i]["name"].(string), results[i]["admin1"].(string), results[i]["country"].(string))
			right := placeDisplayLabel(results[j]["name"].(string), results[j]["admin1"].(string), results[j]["country"].(string))
			if strings.EqualFold(left, right) {
				ambiguous[i], ambiguous[j] = true, true
			}
		}
	}
	filtered := make([]any, 0, len(results))
	for i, result := range results {
		if !ambiguous[i] {
			filtered = append(filtered, result)
		}
	}
	return filtered, nil
}

func (provider jsonProvider) resolvePlace(ctx context.Context, rawID any) (Object, error) {
	id, ok := placeInteger(rawID, false)
	if !ok {
		return nil, locError("stale_selection")
	}
	p, e := provider.geocodingJSON(ctx, "/v1/get", url.Values{"id": {fmt.Sprint(id)}})
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, locError("lookup_failed")
	}
	if p["error"] != nil && p["error"] != false {
		return nil, locError("place_not_found")
	}
	returnedID, ok := placeInteger(p["id"], false)
	if !ok || returnedID != id {
		return nil, locError("place_not_found")
	}
	name, e := safePlaceText(p["name"], 120, false)
	if e != nil {
		return nil, locError("lookup_failed")
	}
	admin, e := placeRegion(p)
	if e != nil {
		return nil, locError("lookup_failed")
	}
	country, e := safePlaceText(p["country"], 120, false)
	if e != nil {
		return nil, locError("lookup_failed")
	}
	code, ok := p["country_code"].(string)
	if !ok || !countryCodePattern.MatchString(code) {
		return nil, locError("lookup_failed")
	}
	zone, ok := p["timezone"].(string)
	if !ok || zone != "UTC" && !zonePattern.MatchString(zone) {
		return nil, locError("lookup_failed")
	}
	l, e := ValidateLocation(Object{"name": placeDisplayLabel(name, admin, country), "latitude": p["latitude"], "longitude": p["longitude"], "timezone": zone})
	if e != nil {
		return nil, locError("lookup_failed")
	}
	return Object{"location": l, "country_code": code, "place": Object{"provider": "open-meteo", "id": float64(id)}}, nil
}
