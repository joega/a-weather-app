package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

var messageNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func messageFixture(id, kind string, sent time.Time, prior ...Object) Object {
	references := []any{}
	for _, p := range prior {
		references = append(references, Object{"identifier": p["id"], "sender": p["sender"], "sent": p["sent"], "@id": "https://untrusted.invalid/do-not-follow"})
	}
	return Object{"id": id, "sender": "w-nws.webmaster@noaa.gov", "senderName": "NWS Test Office", "status": "Actual", "scope": "Public", "messageType": kind,
		"sent": stamp(sent), "effective": stamp(sent), "expires": stamp(sent.Add(time.Hour)), "onset": nil, "ends": nil, "references": references,
		"event": "Flood Warning", "severity": "Severe", "urgency": "Immediate", "certainty": "Observed", "areaDesc": "Fixture County", "headline": "Flood Warning", "description": "Water is rising.\nMove to higher ground.", "instruction": "Do not drive through floodwater."}
}

func messagePayload(rows ...Object) Object {
	features := []any{}
	for _, p := range rows {
		features = append(features, Object{"type": "Feature", "properties": p})
	}
	return Object{"type": "FeatureCollection", "features": features}
}

func TestAlertMessagesPreserveLifecycleAndIdentity(t *testing.T) {
	// Long IDs must remain distinct even when their display prefixes match.
	first := messageFixture(strings.Repeat("x", 512)+"a", "Alert", messageNow.Add(-3*time.Hour))
	update := messageFixture(strings.Repeat("x", 512)+"b", "Update", messageNow.Add(-time.Minute), first)
	update["instruction"] = strings.Repeat("Original instructions.\n", 400)
	cancel := messageFixture("cancel", "Cancel", messageNow, update, first)
	for _, key := range []string{"effective", "expires", "senderName", "event", "description", "instruction", "severity", "urgency", "certainty"} {
		delete(cancel, key)
	}
	test := Object{"status": "Test"}
	private := Object{"status": "Actual", "scope": "Private"}
	ack := Object{"status": "Actual", "scope": "Public", "messageType": "Ack"}
	rows, err := ParseAlertMessages(messagePayload(first, update, cancel, test, private, ack))
	if err != nil || len(rows) != 3 {
		t.Fatal("lifecycle messages lost", rows, err)
	}
	if !rows[0].Expires.Before(messageNow) || rows[0].Identity.ID != first["id"] || rows[1].Identity.Key() == rows[0].Identity.Key() || rows[1].Instruction != update["instruction"] {
		t.Fatal("expired history, full identity or instructions discarded")
	}
	if rows[2].Type != "Cancel" || len(rows[2].References) != 2 || !rows[2].Expires.IsZero() || !rows[2].Effective.Equal(messageNow) {
		t.Fatal("cancellation without info was lost", rows[2])
	}
	reference := rows[1].References[0]
	if reference.Key() != rows[0].Identity.Key() {
		t.Fatal("reference cannot find initial message")
	}
	firstOffset := safeio.Clone(first)
	firstOffset["sent"] = messageNow.Add(-3 * time.Hour).In(time.FixedZone("fixture", -4*3600)).Format(time.RFC3339)
	other, err := ParseAlertMessages(messagePayload(firstOffset))
	if err != nil || other[0].Identity.Key() != rows[0].Identity.Key() {
		t.Fatal("timezone offset changed CAP identity", err)
	}
}

func TestAlertMessagesDeduplicateButRejectConflicts(t *testing.T) {
	a := messageFixture("a", "Alert", messageNow.Add(-time.Minute))
	b := messageFixture("b", "Alert", messageNow.Add(-time.Minute))
	u := messageFixture("u", "Update", messageNow, a, b, a)
	permuted := messageFixture("u", "Update", messageNow, b, a)
	rows, err := ParseAlertMessages(messagePayload(a, a, u, permuted))
	if err != nil || len(rows) != 2 || len(rows[1].References) != 2 {
		t.Fatal("identical replay or reordered references were not deduplicated", rows, err)
	}
	conflict := safeio.Clone(a)
	conflict["instruction"] = "Conflicting content under the same identity"
	if _, err := ParseAlertMessages(messagePayload(a, conflict)); err == nil {
		t.Fatal("conflicting content was silently chosen")
	}
	// CAP timestamps have second precision; a distinct message can reference
	// another message sent within the same second.
	if _, err := ParseAlertMessages(messagePayload(messageFixture("same-second", "Update", messageNow.Add(-time.Minute), a))); err != nil {
		t.Fatal("valid same-second reference rejected", err)
	}
}

func TestAlertMessagesRejectMalformedActualData(t *testing.T) {
	base := messageFixture("id", "Alert", messageNow)
	for name, patch := range map[string]Object{
		"missing id": {"id": nil}, "oversized id": {"id": strings.Repeat("a", 2049)},
		"invalid utf8": {"id": string([]byte{255})}, "control identity": {"sender": "sender\n"},
		"bidi identity": {"id": "id\u202e"}, "invalid sent": {"sent": "tomorrow"},
		"unknown type": {"messageType": "Changed"}, "unknown status": {"status": "real"},
		"missing scope": {"scope": nil}, "unknown severity": {"severity": "Critical"},
		"missing event": {"event": nil}, "invalid text": {"instruction": 3.0},
		"oversized instruction": {"instruction": strings.Repeat("a", 16001)},
		"bad expiry":            {"expires": stamp(messageNow)}, "empty references on update": {"messageType": "Update"},
		"invalid references": {"references": true}, "self reference": {"references": []any{Object{"identifier": "id", "sender": base["sender"], "sent": base["sent"]}}},
		"future reference":    {"references": []any{Object{"identifier": "prior", "sender": base["sender"], "sent": stamp(messageNow.Add(time.Second))}}},
		"too many references": {"references": make([]any, AlertReferenceLimit+1)},
	} {
		t.Run(name, func(t *testing.T) {
			row := safeio.Clone(base)
			for key, value := range patch {
				row[key] = value
			}
			if _, err := ParseAlertMessages(messagePayload(row)); err == nil {
				t.Fatal("malformed lifecycle data accepted")
			}
		})
	}
	for _, payload := range []Object{{}, {"features": []any{nil}}, {"features": make([]any, AlertMessageLimit+1)}} {
		if _, err := ParseAlertMessages(payload); err == nil {
			t.Fatal("invalid collection accepted")
		}
	}
}

func TestAlertPaginationRejectsRedirectsAndMalformedCursors(t *testing.T) {
	for _, next := range []any{nil, "", "https://evil.invalid/alerts?cursor=x", "http://api.weather.gov/alerts?cursor=x", "https://api.weather.gov:444/alerts?cursor=x", "https://user@api.weather.gov/alerts?cursor=x", "https://api.weather.gov/alerts?cursor=x#fragment", "https://api.weather.gov/other?cursor=x", "https://api.weather.gov/alerts", "https://api.weather.gov/alerts?cursor=a&cursor=b", "https://api.weather.gov/alerts?cursor=%0A", "https://api.weather.gov/alerts?cursor=" + strings.Repeat("a", 2049)} {
		if _, err := alertNextCursor(Object{"pagination": Object{"next": next}}); err == nil {
			t.Fatalf("invalid pagination accepted: %v", next)
		}
	}
	for _, value := range []any{nil, Object{}, Object{"next": "https://api.weather.gov/alerts?cursor=x", "extra": true}} {
		if _, err := alertNextCursor(Object{"pagination": value}); err == nil {
			t.Fatal("malformed pagination accepted", value)
		}
	}
	cursor, err := alertNextCursor(Object{"pagination": Object{"next": "https://api.weather.gov/alerts?status%5B0%5D=actual&point=0,0&cursor=a%2Bb%3D"}})
	if err != nil || cursor != "a+b=" {
		t.Fatal("valid opaque pagination cursor rejected", cursor, err)
	}
	query := AlertMessageQuery{Location: DefaultLocation(), CountryCode: "US", Since: messageNow.Add(-time.Hour), Cursor: cursor}
	raw, err := alertMessageURL(query, messageNow)
	u, _ := url.Parse(raw)
	if err != nil || u.Query().Get("point") == "0,0" || u.Query().Get("cursor") != cursor || u.Query().Get("status") != "actual" || u.Query().Get("limit") != "256" {
		t.Fatal("pagination changed caller-owned filters", raw, err)
	}
}

func TestAlertMessageProviderOnePageAndCancellation(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	provider := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.URL.Path != "/alerts" || q.Get("point") != "40.7128,-74.006" || q.Get("status") != "actual" || q.Get("limit") != "256" || q.Get("start") == "" {
			t.Error("unexpected provider query", r.URL)
		}
		if q.Get("cursor") == "wait" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		payload := messagePayload(messageFixture("alert", "Alert", messageNow))
		if q.Get("cursor") == "" {
			payload["pagination"] = Object{"next": "https://api.weather.gov/alerts?status%5B0%5D=actual&cursor=second"}
		}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Error(err)
		}
	})
	query := AlertMessageQuery{Location: DefaultLocation(), CountryCode: "US", Since: messageNow.Add(-time.Hour)}
	first, err := provider.fetchAlertMessages(context.Background(), query, messageNow)
	if err != nil || first.Complete || first.NextCursor != "second" || len(first.Messages) != 1 || !first.FetchedAt.Equal(messageNow) || calls.Load() != 1 {
		t.Fatal("single-page contract or lifecycle data lost", first, calls.Load(), err)
	}
	query.Cursor = first.NextCursor
	second, err := provider.fetchAlertMessages(context.Background(), query, messageNow)
	if err != nil || !second.Complete || second.NextCursor != "" || !reflect.DeepEqual(second.Messages, first.Messages) || calls.Load() != 2 {
		t.Fatal("explicit pagination failed", second, err)
	}
	query.Cursor = "wait"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := provider.fetchAlertMessages(ctx, query, messageNow); done <- err }()
	<-entered
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled alert request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled alert request did not finish")
	}
}

func TestAlertMessageProviderFailuresDoNotLookComplete(t *testing.T) {
	for _, mode := range []string{"redirect", "status", "large", "malformed", "duplicate", "cursor", "repeated", "active"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			provider := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch mode {
				case "redirect":
					http.Redirect(w, r, "https://evil.invalid/", http.StatusFound)
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "large":
					_, _ = w.Write([]byte(strings.Repeat(" ", MaxBytes+1)))
				case "malformed":
					_, _ = w.Write([]byte(`{"features":[{}]}`))
				case "duplicate":
					_, _ = w.Write([]byte(`{"features":[],"features":[]}`))
				default:
					next := "https://evil.invalid/alerts?cursor=next"
					if mode != "cursor" {
						next = "https://api.weather.gov/alerts?cursor=repeated"
					}
					_ = json.NewEncoder(w).Encode(Object{"features": []any{}, "pagination": Object{"next": next}})
				}
			})
			query := AlertMessageQuery{Location: DefaultLocation(), CountryCode: "US", Since: messageNow.Add(-time.Hour), Cursor: "repeated"}
			if mode == "active" {
				query.Active, query.Since, query.Cursor = true, time.Time{}, ""
			}
			page, err := provider.fetchAlertMessages(context.Background(), query, messageNow)
			if err == nil || page.Complete || len(page.Messages) != 0 || !page.FetchedAt.IsZero() || calls.Load() != 1 {
				t.Fatal("failed query looked complete or followed a URL", page, calls.Load(), err)
			}
		})
	}
}

func TestAlertActiveQueryRetainsCurrentMessages(t *testing.T) {
	provider := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/alerts/active" || len(r.URL.Query()) != 2 || r.URL.Query().Get("status") != "actual" {
			t.Error("active query contains history fields", r.URL)
		}
		_ = json.NewEncoder(w).Encode(messagePayload(messageFixture("active", "Alert", messageNow)))
	})
	page, err := provider.fetchAlertMessages(context.Background(), AlertMessageQuery{Location: DefaultLocation(), CountryCode: "US", Active: true}, messageNow)
	if err != nil || !page.Complete || len(page.Messages) != 1 {
		t.Fatal("active response lost", page, err)
	}
}

func TestParseNWSAlertCapture(t *testing.T) {
	path := os.Getenv("A_WEATHER_APP_ALERT_CAPTURE")
	if path == "" {
		t.Skip("optional public NWS capture replay")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := safeio.Object(raw, MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := ParseAlertMessages(payload)
	if err != nil || len(messages) == 0 {
		t.Fatal("public actual-message capture did not parse", len(messages), err)
	}
	t.Log("parsed public lifecycle messages:", len(messages))
}

func TestAlertQueriesRejectInvalidScopeBeforeNetwork(t *testing.T) {
	base := AlertMessageQuery{Location: DefaultLocation(), CountryCode: "US", Since: messageNow.Add(-time.Hour)}
	for name, change := range map[string]func(*AlertMessageQuery){
		"unknown country":  func(q *AlertMessageQuery) { q.CountryCode = "" },
		"foreign country":  func(q *AlertMessageQuery) { q.CountryCode = "GB" },
		"invalid location": func(q *AlertMessageQuery) { q.Location = nil },
		"missing since":    func(q *AlertMessageQuery) { q.Since = time.Time{} },
		"old since":        func(q *AlertMessageQuery) { q.Since = messageNow.Add(-7*24*time.Hour - time.Second) },
		"future since":     func(q *AlertMessageQuery) { q.Since = messageNow.Add(time.Second) },
		"active history":   func(q *AlertMessageQuery) { q.Active = true },
		"active cursor":    func(q *AlertMessageQuery) { q.Active, q.Since, q.Cursor = true, time.Time{}, "cursor" },
		"invalid cursor":   func(q *AlertMessageQuery) { q.Cursor = "x\u202e" },
	} {
		t.Run(name, func(t *testing.T) {
			query := base
			change(&query)
			// A nil transport makes accidental network admission fail loudly.
			if _, err := (jsonProvider{}).fetchAlertMessages(context.Background(), query, messageNow); err == nil {
				t.Fatal("invalid query admitted")
			}
		})
	}
}

func BenchmarkAlertMessages(b *testing.B) {
	for _, count := range []int{1, AlertMessageLimit} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			rows := make([]Object, 0, count)
			for i := 0; i < count; i++ {
				row := messageFixture("id-"+strconv.Itoa(i), "Alert", messageNow)
				if count == AlertMessageLimit {
					row["description"] = strings.Repeat("a", 4096)
					row["instruction"] = strings.Repeat("b", 2048)
				}
				rows = append(rows, row)
			}
			payload := messagePayload(rows...)
			raw, err := json.Marshal(payload)
			if err != nil || len(raw) > MaxBytes {
				b.Fatal("benchmark exceeds provider envelope", len(raw), err)
			}
			b.SetBytes(int64(len(raw)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := ParseAlertMessages(payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNormalizeTypedAlertMessage(t *testing.T) {
	a := messageFixture("a", "Alert", messageNow.Add(-time.Minute))
	b := messageFixture("b", "Update", messageNow, a)
	parsed, err := ParseAlertMessages(messagePayload(a, b))
	if err != nil {
		t.Fatal(err)
	}
	m := parsed[1]
	m.Identity.Sent = m.Identity.Sent.In(time.FixedZone("fixture", -4*3600))
	m.References = append(m.References, m.References[0])
	normalized, err := NormalizeAlertMessage(m)
	if err != nil || !reflect.DeepEqual(normalized, parsed[1]) {
		t.Fatal("typed normalization differs from provider parsing", normalized, err)
	}
	normalized.References[0].ID = "mutated"
	if m.References[0].ID != "a" {
		t.Fatal("normalization aliases caller references")
	}
	m.Identity.Sent = time.Time{}
	if _, err := NormalizeAlertMessage(m); err == nil {
		t.Fatal("missing typed identity time accepted")
	}
}

func TestAlertMessageOptionalSourceTextAndInitialReferences(t *testing.T) {
	p := messageFixture("headline-only", "Alert", messageNow)
	for _, key := range []string{"senderName", "description", "instruction", "references"} {
		delete(p, key)
	}
	messages, err := ParseAlertMessages(messagePayload(p))
	if err != nil || len(messages) != 1 || messages[0].Description != "" || messages[0].Issuer != "" || len(messages[0].References) != 0 || messages[0].Event != "Flood Warning" {
		t.Fatal("optional CAP fields rejected", messages, err)
	}
	if _, err := NormalizeAlertMessage(messages[0]); err != nil {
		t.Fatal("typed normalization rejected optional fields", err)
	}
	p["messageType"] = "Update"
	if _, err := ParseAlertMessages(messagePayload(p)); err == nil {
		t.Fatal("update without required lifecycle references accepted")
	}
}
