package weather

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const AlertMessageLimit = 256
const AlertReferenceLimit = 128

// AlertReference is the CAP message identity triple. It is never shortened for
// display. References are data, not URLs to follow or commands to execute.
type AlertReference struct {
	ID     string    `json:"id"`
	Sender string    `json:"sender"`
	Sent   time.Time `json:"sent"`
}

// Key is a stable, collision-resistant identity independent of JSON key order
// and source timezone offsets. Invalid references must not enter the ledger.
func (r AlertReference) Key() string {
	raw, _ := json.Marshal([3]string{r.ID, r.Sender, r.Sent.UTC().Format(time.RFC3339Nano)})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// AlertMessage retains lifecycle information separately from shortened display
// alerts. Text is untrusted source content; presentation must sanitize it. A
// cancellation may omit its info fields, so it must be matched by References.
type AlertMessage struct {
	Identity    AlertReference   `json:"identity"`
	Type        string           `json:"type"`
	References  []AlertReference `json:"references"`
	Issuer      string           `json:"issuer"`
	Event       string           `json:"event"`
	Severity    string           `json:"severity"`
	Urgency     string           `json:"urgency"`
	Certainty   string           `json:"certainty"`
	Area        string           `json:"area"`
	Headline    string           `json:"headline"`
	Description string           `json:"description"`
	Instruction string           `json:"instruction"`
	Effective   time.Time        `json:"effective"`
	Expires     time.Time        `json:"expires"`
	Onset       time.Time        `json:"onset"`
	Ends        time.Time        `json:"ends"`
}

// AlertMessagePage describes a single bounded response. Complete only means no
// next page was advertised; it never means no warnings exist or proves that a
// previously active warning was canceled. The caller owns pagination budgets.
type AlertMessagePage struct {
	Messages   []AlertMessage
	FetchedAt  time.Time
	NextCursor string
	Complete   bool
}

// AlertMessageQuery selects active messages or history since a bounded time.
// CountryCode must be established by location resolution, not coordinates.
// A cursor is used only for history and does not override the original query.
type AlertMessageQuery struct {
	Location    Object
	CountryCode string
	Active      bool
	Since       time.Time
	Cursor      string
}

func alertOpaque(value any, limit int) (string, error) {
	s, ok := value.(string)
	if !ok || s == "" || len(s) > limit || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return "", errors.New("invalid alert identity")
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return "", errors.New("invalid alert identity")
		}
	}
	return s, nil
}

func alertReference(row Object, idField string) (AlertReference, error) {
	var r AlertReference
	var err error
	if r.ID, err = alertOpaque(row[idField], 2048); err != nil {
		return r, err
	}
	if r.Sender, err = alertOpaque(row["sender"], 256); err != nil {
		return r, err
	}
	r.Sent, err = Instant(row["sent"])
	if err != nil {
		return r, err
	}
	return r, nil
}

func alertText(value any, limit int, required bool) (string, error) {
	if value == nil && !required {
		return "", nil
	}
	s, ok := value.(string)
	if !ok || !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit || required && strings.TrimSpace(s) == "" {
		return "", errors.New("invalid alert text")
	}
	return s, nil
}

func parseAlertMessage(p Object) (AlertMessage, error) {
	var m AlertMessage
	var err error
	m.Type, _ = p["messageType"].(string)
	if m.Type != "Alert" && m.Type != "Update" && m.Type != "Cancel" {
		return m, errors.New("invalid alert message type")
	}
	if m.Identity, err = alertReference(p, "id"); err != nil {
		return m, err
	}
	rows, ok := p["references"].([]any)
	if !ok || len(rows) > AlertReferenceLimit || m.Type != "Alert" && len(rows) == 0 {
		return m, errors.New("invalid alert references")
	}
	m.References = make([]AlertReference, 0, len(rows))
	seen := map[string]bool{}
	for _, value := range rows {
		r, err := alertReference(obj(value), "identifier")
		if err != nil || r.Sent.After(m.Identity.Sent) || r.ID == m.Identity.ID && r.Sender == m.Identity.Sender {
			return m, errors.New("invalid prior alert reference")
		}
		if !seen[r.Key()] {
			seen[r.Key()] = true
			m.References = append(m.References, r)
		}
	}
	sort.Slice(m.References, func(i, j int) bool {
		a, b := m.References[i], m.References[j]
		if a.Sender != b.Sender {
			return a.Sender < b.Sender
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Sent.Before(b.Sent)
	})
	for _, field := range []struct {
		key      string
		limit    int
		required bool
		out      *string
	}{
		{"senderName", 256, m.Type != "Cancel", &m.Issuer},
		{"event", 512, m.Type != "Cancel", &m.Event},
		{"areaDesc", 8192, false, &m.Area},
		{"headline", 2048, false, &m.Headline},
		{"description", 32000, m.Type != "Cancel", &m.Description},
		{"instruction", 16000, false, &m.Instruction},
	} {
		if *field.out, err = alertText(p[field.key], field.limit, field.required); err != nil {
			return m, fmt.Errorf("%s: %w", field.key, err)
		}
	}
	for _, field := range []struct {
		key, allowed string
		out          *string
	}{
		{"severity", "|Extreme|Severe|Moderate|Minor|Unknown|", &m.Severity},
		{"urgency", "|Immediate|Expected|Future|Past|Unknown|", &m.Urgency},
		{"certainty", "|Observed|Likely|Possible|Unlikely|Unknown|", &m.Certainty},
	} {
		s, ok := p[field.key].(string)
		if p[field.key] == nil && m.Type == "Cancel" {
			s, ok = "Unknown", true
		}
		if !ok || s == "" || strings.Contains(s, "|") || !strings.Contains(field.allowed, "|"+s+"|") {
			return m, fmt.Errorf("invalid alert %s", field.key)
		}
		*field.out = s
	}
	m.Effective = m.Identity.Sent
	for _, field := range []struct {
		key      string
		required bool
		out      *time.Time
	}{
		{"effective", false, &m.Effective},
		{"expires", m.Type != "Cancel", &m.Expires},
		{"onset", false, &m.Onset},
		{"ends", false, &m.Ends},
	} {
		if p[field.key] == nil && !field.required {
			continue
		}
		if *field.out, err = Instant(p[field.key]); err != nil {
			return m, err
		}
	}
	if m.Type != "Cancel" && !m.Expires.After(m.Effective) {
		return m, errors.New("invalid alert validity interval")
	}
	return m, nil
}

// ParseAlertMessages retains expired/future messages and cancellations for a
// later lifecycle reconciler. It excludes non-Actual, non-Public, Ack and Error
// messages. Malformed actual data fails the page instead of implying all clear.
func ParseAlertMessages(payload Object) ([]AlertMessage, error) {
	features, ok := payload["features"].([]any)
	if !ok || len(features) > AlertMessageLimit {
		return nil, errors.New("invalid alert message collection")
	}
	messages := make([]AlertMessage, 0, len(features))
	seen := map[string]AlertMessage{}
	for _, value := range features {
		p := obj(obj(value)["properties"])
		if p == nil {
			return nil, errors.New("invalid alert message feature")
		}
		switch p["status"] {
		case "Exercise", "System", "Test", "Draft":
			continue
		case "Actual":
		default:
			return nil, errors.New("invalid alert message status")
		}
		switch p["scope"] {
		case "Private", "Restricted":
			continue
		case "Public":
		default:
			return nil, errors.New("invalid alert message scope")
		}
		if p["messageType"] == "Ack" || p["messageType"] == "Error" {
			continue
		}
		m, err := parseAlertMessage(p)
		if err != nil {
			return nil, err
		}
		key := m.Identity.Key()
		if prior, exists := seen[key]; exists {
			if !reflect.DeepEqual(prior, m) {
				return nil, errors.New("conflicting alert message identity")
			}
			continue
		}
		seen[key] = m
		messages = append(messages, m)
	}
	return messages, nil
}

func alertMessageURL(query AlertMessageQuery, now time.Time) (string, error) {
	location, err := ValidateLocation(query.Location)
	if err != nil || query.CountryCode != "US" || now.IsZero() {
		return "", errors.New("invalid US alert query")
	}
	q := url.Values{"point": {fmt.Sprint(location["latitude"]) + "," + fmt.Sprint(location["longitude"])}, "status": {"actual"}}
	path := "/alerts/active"
	if query.Active {
		if !query.Since.IsZero() || query.Cursor != "" {
			return "", errors.New("active alert query has history fields")
		}
	} else {
		if query.Since.IsZero() || query.Since.After(now) || query.Since.Before(now.Add(-7*24*time.Hour)) {
			return "", errors.New("invalid alert history interval")
		}
		path = "/alerts"
		q.Set("start", stamp(query.Since))
		q.Set("limit", fmt.Sprint(AlertMessageLimit))
		if query.Cursor != "" {
			if _, err := alertOpaque(query.Cursor, 2048); err != nil {
				return "", err
			}
			q.Set("cursor", query.Cursor)
		}
	}
	return "https://api.weather.gov" + path + "?" + q.Encode(), nil
}

// Only the opaque cursor is reused. All request filters are reconstructed from
// the caller's validated query, including when the API rewrites array filters.
func alertNextCursor(payload Object) (string, error) {
	value, present := payload["pagination"]
	if !present {
		return "", nil
	}
	p := obj(value)
	if len(p) != 1 {
		return "", errors.New("invalid alert pagination")
	}
	s, err := alertOpaque(p["next"], 8192)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Host != "api.weather.gov" || u.Path != "/alerts" || u.RawPath != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return "", errors.New("invalid alert pagination endpoint")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["cursor"]) != 1 {
		return "", errors.New("invalid alert pagination cursor")
	}
	return alertOpaque(q.Get("cursor"), 2048)
}

// FetchAlertMessages performs one bounded HTTPS request. It creates no worker,
// timer or automatic pagination. The caller owns polling/admission and must not
// present history completeness as an all-clear or cancellation signal.
func FetchAlertMessages(ctx context.Context, query AlertMessageQuery, now time.Time) (AlertMessagePage, error) {
	return defaultJSONProvider().fetchAlertMessages(ctx, query, now)
}

func (provider jsonProvider) fetchAlertMessages(ctx context.Context, query AlertMessageQuery, now time.Time) (AlertMessagePage, error) {
	var page AlertMessagePage
	u, err := alertMessageURL(query, now)
	if err != nil {
		return page, err
	}
	payload, err := provider.fetchJSON(ctx, u, false)
	if err != nil {
		return page, err
	}
	messages, err := ParseAlertMessages(payload)
	if err != nil {
		return page, err
	}
	cursor, err := alertNextCursor(payload)
	if err != nil || cursor != "" && (query.Active || cursor == query.Cursor) {
		return page, errors.New("invalid or repeated alert pagination")
	}
	return AlertMessagePage{Messages: messages, FetchedAt: now.UTC(), NextCursor: cursor, Complete: cursor == ""}, nil
}
