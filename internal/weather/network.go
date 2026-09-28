package weather

import (
	"context"
	"errors"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const LocalURL = "https://ipwho.is/?fields=success,city,region,region_code,country_code,latitude,longitude,timezone.id"
const userAgent = "a-weather-app/0.2 (A Weather App; Linux Hyprland desktop weather)"

type LocationError struct {
	Code    string
	Message string
}

func (e *LocationError) Error() string { return e.Message }
func locError(code string) error       { return &LocationError{code, code} }
func ValidateSelection(v Object) (Object, error) {
	if v["mode"] == "zip" && len(v) == 2 {
		z, ok := v["zip_code"].(string)
		if !ok || !regexp.MustCompile(`^[0-9]{5}$`).MatchString(z) {
			return nil, locError("invalid_zip")
		}
		return Object{"mode": "zip", "zip_code": z}, nil
	}
	if v["mode"] == "auto" && len(v) == 1 {
		return Object{"mode": "auto", "zip_code": nil}, nil
	}
	if v["mode"] == "place" && len(v) == 3 {
		id, ok := placeInteger(v["place_id"], false)
		generation, validGeneration := placeInteger(v["search_generation"], true)
		if ok && validGeneration {
			return Object{"mode": "place", "place_id": float64(id), "search_generation": float64(generation)}, nil
		}
	}
	return nil, errors.New("invalid location selection")
}
func FetchJSON(ctx context.Context, rawURL string) (Object, error) {
	return fetchJSON(ctx, rawURL, false)
}
func fetchJSON(ctx context.Context, rawURL string, local bool) (Object, error) {
	limit := MaxBytes
	if local {
		limit = 16 * 1024
	}
	return fetchJSONBound(ctx, rawURL, local, limit)
}
func fetchJSONBound(ctx context.Context, rawURL string, local bool, limit int) (Object, error) {
	u, e := url.Parse(rawURL)
	if e != nil {
		return nil, e
	}
	allowed := u.Hostname() == "api.open-meteo.com" || u.Hostname() == "api.weather.gov" || u.Hostname() == "geocoding-api.open-meteo.com"
	if local {
		allowed = rawURL == LocalURL
	}
	if !allowed || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") || (u.Hostname() == "geocoding-api.open-meteo.com" && u.Path != "/v1/search" && u.Path != "/v1/get") {
		return nil, errors.New("unsupported weather endpoint")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if local {
		transport.Proxy = nil
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("weather redirects refused") }}
	req, e := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/geo+json, application/json")
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("weather HTTP status %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, int64(limit+1)))
	if e != nil {
		return nil, e
	}
	value, e := safeio.Object(raw, limit)
	if e != nil {
		return nil, e
	}
	if local {
		if e = localTree(value); e != nil {
			return nil, e
		}
	}
	return value, nil
}
func Fetch(ctx context.Context, location Object, now time.Time) (Object, error) {
	return FetchForCountry(ctx, location, now, "")
}
func FetchForCountry(ctx context.Context, location Object, now time.Time, countryCode string) (Object, error) {
	if countryCode != "" && !countryCodePattern.MatchString(countryCode) {
		return nil, errors.New("invalid country code")
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if now.IsZero() {
		now = time.Now()
	}
	u, e := ForecastURL(location)
	if e != nil {
		return nil, e
	}
	p, e := FetchJSON(ctx, u)
	if e != nil {
		return nil, e
	}
	s, e := ParseForecast(p, location, now)
	if e != nil {
		return nil, e
	}
	if countryCode != "US" {
		status, coverage := "not_supported_here", "unsupported"
		if countryCode == "" {
			status, coverage = "unavailable", "unknown"
		}
		s["alerts"] = Object{"status": status, "items": []any{}, "fetched_at": nil, "source": nil, "coverage": coverage}
		return s, ValidateSnapshot(s, obj(s["location"]))
	}
	q := url.Values{"point": {fmt.Sprint(location["latitude"]) + "," + fmt.Sprint(location["longitude"])}}
	p, e = FetchJSON(ctx, "https://api.weather.gov/alerts/active?"+q.Encode())
	var items []any
	if e == nil {
		items, e = ParseAlerts(p, now)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if e != nil {
		s["alerts"] = Object{"status": "unavailable", "items": []any{}, "fetched_at": nil, "source": "National Weather Service", "coverage": "US", "error": "alert_feed_failed"}
	} else {
		s["alerts"] = Object{"status": "available", "items": items, "fetched_at": stamp(now), "source": "National Weather Service", "coverage": "US"}
	}
	if e = ValidateSnapshot(s, obj(s["location"])); e != nil {
		return nil, e
	}
	return s, nil
}
func ParseAlerts(payload Object, now time.Time) ([]any, error) {
	features, ok := payload["features"].([]any)
	if !ok || len(features) > 256 {
		return nil, errors.New("invalid alert collection")
	}
	r := []any{}
	for _, v := range features {
		feature := obj(v)
		p := obj(feature["properties"])
		if p == nil {
			return nil, errors.New("invalid alert feature")
		}
		if p["status"] != "Actual" || p["messageType"] == "Cancel" {
			continue
		}
		expires, e := Instant(p["expires"])
		if e != nil {
			return nil, e
		}
		effective := p["effective"]
		if effective == nil || effective == "" {
			effective = p["sent"]
		}
		eff, e := Instant(effective)
		if e != nil {
			return nil, e
		}
		if !expires.After(now) || eff.After(now) {
			continue
		}
		a := Object{"effective": stamp(eff), "expires": stamp(expires), "source": "National Weather Service"}
		for k, limit := range map[string]int{"id": 2048, "event": 512, "headline": 2048, "severity": 64, "urgency": 64, "description": 32000, "instruction": 16000} {
			value := p[k]
			if value != nil {
				s, ok := value.(string)
				if !ok || len([]rune(s)) > limit {
					return nil, errors.New("invalid alert text")
				}
			}
			a[k] = value
		}
		if a["id"] == nil || a["id"] == "" {
			s, _ := feature["id"].(string)
			runes := []rune(s)
			if len(runes) > 2048 {
				runes = runes[:2048]
			}
			a["id"] = string(runes)
		}
		r = append(r, a)
	}
	return r, nil
}
func Plain(v any, limit int, multiline bool) (any, error) {
	if v == nil {
		return nil, nil
	}
	s, ok := v.(string)
	if !ok {
		return nil, errors.New("invalid text")
	}
	rs := []rune(s)
	if len(rs) > limit {
		rs = rs[:limit]
	}
	var b strings.Builder
	for _, r := range rs {
		if r == '<' || r == '>' || r == '&' || (r < 32 && !(multiline && r == '\n')) || (r >= 127 && r <= 159) || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), nil
}
func AlertRecord(row Object) (Object, error) {
	if row == nil {
		return nil, errors.New("invalid alert")
	}
	r := Object{"source": "National Weather Service", "text_truncated": false}
	for k, l := range map[string]int{"id": 256, "event": 128, "headline": 512, "severity": 32, "urgency": 32, "description": 4096, "instruction": 2048} {
		multiline := k == "description" || k == "instruction"
		v, e := Plain(row[k], l, multiline)
		if e != nil {
			return nil, e
		}
		r[k] = v
		if s, ok := row[k].(string); ok && multiline && len([]rune(s)) > l {
			r["text_truncated"] = true
		}
	}
	for _, k := range []string{"effective", "expires"} {
		t, e := Instant(row[k])
		if e != nil {
			return nil, e
		}
		r[k] = stamp(t)
	}
	return r, nil
}

var states = func() map[string]string {
	r := map[string]string{}
	for _, s := range strings.Split("Alabama:AL|Alaska:AK|Arizona:AZ|Arkansas:AR|California:CA|Colorado:CO|Connecticut:CT|Delaware:DE|District of Columbia:DC|Florida:FL|Georgia:GA|Hawaii:HI|Idaho:ID|Illinois:IL|Indiana:IN|Iowa:IA|Kansas:KS|Kentucky:KY|Louisiana:LA|Maine:ME|Maryland:MD|Massachusetts:MA|Michigan:MI|Minnesota:MN|Mississippi:MS|Missouri:MO|Montana:MT|Nebraska:NE|Nevada:NV|New Hampshire:NH|New Jersey:NJ|New Mexico:NM|New York:NY|North Carolina:NC|North Dakota:ND|Ohio:OH|Oklahoma:OK|Oregon:OR|Pennsylvania:PA|Rhode Island:RI|South Carolina:SC|South Dakota:SD|Tennessee:TN|Texas:TX|Utah:UT|Vermont:VT|Virginia:VA|Washington:WA|West Virginia:WV|Wisconsin:WI|Wyoming:WY|Puerto Rico:PR|Guam:GU|American Samoa:AS|Northern Mariana Islands:MP|U.S. Virgin Islands:VI", "|") {
		p := strings.Split(s, ":")
		r[p[0]] = p[1]
	}
	return r
}()

func Resolve(ctx context.Context, selection Object) (Object, error) {
	r, e := ResolveSelection(ctx, selection)
	if e != nil {
		return nil, e
	}
	return obj(r["location"]), nil
}
func ResolveSelection(ctx context.Context, selection Object) (Object, error) {
	s, e := ValidateSelection(selection)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	if s["mode"] == "place" {
		return resolvePlace(ctx, s["place_id"])
	}
	if s["mode"] == "auto" {
		p, e := fetchJSON(ctx, LocalURL, true)
		if e != nil {
			return nil, locError("lookup_failed")
		}
		l, e := ParseLocalLocation(p)
		if e != nil {
			return nil, e
		}
		return Object{"location": l, "country_code": p["country_code"], "place": nil}, nil
	}
	z := s["zip_code"].(string)
	q := url.Values{"name": {z}, "countryCode": {"US"}, "count": {"10"}, "language": {"en"}, "format": {"json"}}
	p, e := FetchJSON(ctx, "https://geocoding-api.open-meteo.com/v1/search?"+q.Encode())
	if e != nil {
		return nil, locError("lookup_failed")
	}
	l, e := ParseZIP(p, z)
	if e != nil {
		return nil, e
	}
	return Object{"location": l, "country_code": "US", "place": nil}, nil
}
func ParseZIP(p Object, zip string) (Object, error) {
	if _, e := ValidateSelection(Object{"mode": "zip", "zip_code": zip}); e != nil {
		return nil, e
	}
	if p["error"] != nil && p["error"] != false {
		return nil, locError("location_failed")
	}
	rows, ok := p["results"].([]any)
	if p["results"] == nil {
		rows = []any{}
		ok = true
	}
	if !ok || len(rows) >= 10 {
		return nil, locError("location_failed")
	}
	matches := []Object{}
	for _, v := range rows {
		r := obj(v)
		if r["country_code"] != "US" {
			return nil, locError("location_failed")
		}
		name, e := text(r["name"], 120)
		if e != nil {
			return nil, e
		}
		state, e := text(r["admin1"], 120)
		if e != nil {
			return nil, e
		}
		if code, ok := states[state]; ok {
			state = code
		}
		l, e := ValidateLocation(Object{"name": name + ", " + state, "latitude": r["latitude"], "longitude": r["longitude"], "timezone": r["timezone"]})
		if e != nil {
			return nil, e
		}
		codes, ok := r["postcodes"].([]any)
		if !ok || len(codes) > 2048 {
			return nil, locError("location_failed")
		}
		match := false
		for _, v := range codes {
			s, ok := v.(string)
			if !ok || !regexp.MustCompile(`^[0-9]{5}$`).MatchString(s) {
				return nil, locError("location_failed")
			}
			if s == zip {
				match = true
			}
		}
		if match {
			matches = append(matches, l)
		}
	}
	if len(matches) == 0 {
		return nil, locError("zip_not_found")
	}
	if len(matches) != 1 {
		return nil, locError("zip_ambiguous")
	}
	return matches[0], nil
}
func localTree(v any) error {
	nodes := 0
	var check func(any, int) error
	check = func(v any, d int) error {
		nodes++
		if nodes > 64 || d > 4 {
			return errors.New("local structure limit")
		}
		switch x := v.(type) {
		case Object:
			if len(x) > 16 {
				return errors.New("local object limit")
			}
			for k, v := range x {
				if len([]rune(k)) > 64 {
					return errors.New("local key limit")
				}
				if e := check(v, d+1); e != nil {
					return e
				}
			}
		case []any:
			return errors.New("local arrays refused")
		case string:
			if len([]rune(x)) > 256 {
				return errors.New("local string limit")
			}
		case nil, bool:
		default:
			if _, e := bounded(v, -1e9, 1e9, false); e != nil {
				return e
			}
		}
		return nil
	}
	return check(v, 0)
}
func ParseLocalLocation(p Object) (Object, error) {
	fail := func() (Object, error) { return nil, locError("lookup_failed") }
	if e := localTree(p); e != nil {
		return fail()
	}
	keys := []string{"success", "city", "region", "region_code", "country_code", "latitude", "longitude", "timezone"}
	if len(p) != len(keys) || p["success"] != true {
		return fail()
	}
	for _, k := range keys {
		if _, ok := p[k]; !ok {
			return fail()
		}
	}
	city, e := text(p["city"], 120)
	if e != nil {
		return fail()
	}
	region, e := text(p["region"], 120)
	if e != nil {
		return fail()
	}
	country, ok := p["country_code"].(string)
	if !ok || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(country) {
		return fail()
	}
	code := ""
	if p["region_code"] != nil {
		code, ok = p["region_code"].(string)
		if !ok || len([]rune(code)) > 16 {
			return fail()
		}
	}
	name := city + ", " + region + ", " + country
	if country == "US" {
		expected, ok := states[region]
		if !ok {
			expected = code
		}
		valid := false
		for _, v := range states {
			if v == expected {
				valid = true
			}
		}
		if !valid || (code != "" && code != expected) {
			return fail()
		}
		name = city + ", " + expected
	}
	z := obj(p["timezone"])
	if len(z) != 1 || z["id"] == nil {
		return fail()
	}
	l, e := ValidateLocation(Object{"name": name, "latitude": p["latitude"], "longitude": p["longitude"], "timezone": z["id"]})
	if e != nil {
		return fail()
	}
	return l, nil
}
