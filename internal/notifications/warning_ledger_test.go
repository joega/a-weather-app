package notifications

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

var warningTestNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
var warningTestLocation = warningHash("fixture point")

func warningFixture(id, kind string, sent time.Time, prior ...weather.AlertMessage) weather.AlertMessage {
	refs := []weather.AlertReference{}
	for _, m := range prior {
		refs = append(refs, m.Identity)
	}
	return weather.AlertMessage{
		Identity: weather.AlertReference{ID: id, Sender: "fixture@noaa.gov", Sent: sent},
		Type:     kind, References: refs, Issuer: "NWS Fixture Office", Event: "Flood Warning",
		Severity: "Severe", Urgency: "Immediate", Certainty: "Observed", Area: "Fixture County",
		Headline: "Flood Warning", Description: "Water is rising.", Instruction: "Move to higher ground.",
		Effective: sent, Expires: warningTestNow.Add(24 * time.Hour),
	}
}

func warningCancel(id string, sent time.Time, prior ...weather.AlertMessage) weather.AlertMessage {
	m := warningFixture(id, "Cancel", sent, prior...)
	m.Issuer, m.Event, m.Area, m.Headline, m.Description, m.Instruction = "", "", "", "", "", ""
	m.Severity, m.Urgency, m.Certainty = "Unknown", "Unknown", "Unknown"
	m.Expires = time.Time{}
	return m
}

type warningMemoryFiles struct {
	doc           M
	writes        int
	fail, publish bool
}

func (f *warningMemoryFiles) Read(_ string, _ int) (M, error) {
	if f.doc == nil {
		return nil, nil
	}
	return safeio.Clone(f.doc), nil
}
func (f *warningMemoryFiles) Write(_ string, value any, limit int) error {
	f.writes++
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	v, err := safeio.Object(raw, limit)
	if err != nil {
		return err
	}
	if !f.fail || f.publish {
		f.doc = v
	}
	if f.fail {
		return errors.New("fixture fsync failure")
	}
	return nil
}
func newWarningMemory(t testing.TB) (*WarningLedger, *warningMemoryFiles) {
	t.Helper()
	files := &warningMemoryFiles{}
	l, err := openWarningLedger(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return l, files
}
func warningObserve(t testing.TB, l *WarningLedger, now time.Time, complete bool, messages ...weather.AlertMessage) {
	t.Helper()
	if err := l.Reconcile(WarningBatch{warningTestLocation, messages, now, complete, warningCurrentKeys(messages, now)}, now); err != nil {
		t.Fatal(err)
	}
}
func warningDecisions(t testing.TB, l *WarningLedger, now time.Time, expected ...WarningDecision) {
	t.Helper()
	got := l.Candidates(warningTestLocation, now)
	if len(got) != len(expected) {
		t.Fatalf("decisions: got %+v, want %+v", got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Fatalf("decision %d: got %+v, want %+v", i, got[i], expected[i])
		}
	}
}
func warningDecision(m weather.AlertMessage, kind string) WarningDecision {
	return WarningDecision{warningTestLocation, m.Identity.Key(), kind}
}
func warningAttempt(t testing.TB, l *WarningLedger, m weather.AlertMessage, kind, status string, now time.Time) WarningReceipt {
	t.Helper()
	r, err := l.Reserve(warningDecision(m, kind), now)
	if err != nil {
		t.Fatal(err)
	}
	if status != "reserved" {
		if err := l.Finish(r, status, now); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestWarningLedgerReplayAndRestart(t *testing.T) {
	for _, status := range []string{"reserved", "sent", "failed", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			dir := testState(t)
			l, err := OpenWarningLedger(dir)
			if err != nil {
				t.Fatal(err)
			}
			a := warningFixture("initial", "Alert", warningTestNow)
			warningObserve(t, l, warningTestNow, true, a)
			warningDecisions(t, l, warningTestNow, warningDecision(a, "new"))
			r := warningAttempt(t, l, a, "new", status, warningTestNow)
			warningObserve(t, l, warningTestNow.Add(time.Minute), true, a, a)
			warningDecisions(t, l, warningTestNow.Add(time.Minute))
			if err := l.Close(); err != nil {
				t.Fatal(err)
			}
			l, err = OpenWarningLedger(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			warningObserve(t, l, warningTestNow.Add(2*time.Minute), true, a)
			warningDecisions(t, l, warningTestNow.Add(2*time.Minute))
			want := status
			if status == "reserved" {
				want = "uncertain"
			}
			if got := l.Receipts(); len(got) != 1 || got[0].Status != want {
				t.Fatalf("restart receipt = %+v", got)
			}
			if err := l.Finish(r, "sent", warningTestNow.Add(2*time.Minute)); !errors.Is(err, ErrWarningDecision) {
				t.Fatal("late previous-instance result accepted", err)
			}
			raw, err := os.ReadFile(dir.Path + "/" + warningLedgerFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{a.Identity.ID, a.Identity.Sender, a.Issuer, a.Area, a.Description, a.Instruction} {
				if strings.Contains(string(raw), secret) {
					t.Fatalf("source text leaked to state: %q", secret)
				}
			}
		})
	}
}

func TestWarningLedgerCollapsesUnorderedHistory(t *testing.T) {
	a := warningFixture("a", "Alert", warningTestNow.Add(-3*time.Minute))
	u := warningFixture("u", "Update", warningTestNow.Add(-2*time.Minute), a)
	u.Instruction = "Updated evacuation instructions."
	c := warningCancel("c", warningTestNow.Add(-time.Minute), u)
	for _, rows := range [][]weather.AlertMessage{{a, u, c}, {c, u, a}, {u, a, c}, {c, a, u}, {u, c, a}, {a, c, u}} {
		l, _ := newWarningMemory(t)
		warningObserve(t, l, warningTestNow, true, rows...)
		warningDecisions(t, l, warningTestNow)
		if s := l.Status(warningTestLocation, warningTestNow); s.Canceled != 1 || s.Active != 0 || s.Expired != 0 {
			t.Fatalf("collapsed lifecycle = %+v", s)
		}
		warningObserve(t, l, warningTestNow.Add(time.Minute), true, a, u)
		warningDecisions(t, l, warningTestNow.Add(time.Minute))
	}
	l, _ := newWarningMemory(t)
	warningObserve(t, l, warningTestNow, true, u, a)
	warningDecisions(t, l, warningTestNow, warningDecision(u, "new"))
}

func TestWarningLedgerLateReferencesAndIncompleteHistory(t *testing.T) {
	a := warningFixture("a", "Alert", warningTestNow.Add(-3*time.Minute))
	u := warningFixture("u", "Update", warningTestNow.Add(-2*time.Minute), a)
	c := warningCancel("c", warningTestNow.Add(-time.Minute), u)
	l, files := newWarningMemory(t)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	warningObserve(t, l, warningTestNow.Add(time.Minute), false, c)
	warningDecisions(t, l, warningTestNow.Add(time.Minute))
	if s := l.Status(warningTestLocation, warningTestNow.Add(time.Minute)); s.Complete || !s.Fresh {
		t.Fatalf("partial history mislabeled: %+v", s)
	}
	l, err := openWarningLedger(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	warningObserve(t, l, warningTestNow.Add(2*time.Minute), true, c, u, a)
	warningDecisions(t, l, warningTestNow.Add(2*time.Minute), warningDecision(c, "canceled"))
	warningAttempt(t, l, c, "canceled", "sent", warningTestNow.Add(2*time.Minute))
	warningObserve(t, l, warningTestNow.Add(3*time.Minute), true, a, u, c)
	warningDecisions(t, l, warningTestNow.Add(3*time.Minute))
	c2 := warningCancel("c2", warningTestNow.Add(4*time.Minute), c, u, a)
	warningObserve(t, l, warningTestNow.Add(4*time.Minute), true, c2)
	warningDecisions(t, l, warningTestNow.Add(4*time.Minute))
}

func TestWarningLedgerMaterialChangesAndReissue(t *testing.T) {
	a := warningFixture("a", "Alert", warningTestNow)
	for name, change := range map[string]func(*weather.AlertMessage){
		"headline":                        func(m *weather.AlertMessage) { m.Headline = "Flood Warning with increased danger" },
		"severity":                        func(m *weather.AlertMessage) { m.Severity = "Extreme" },
		"urgency":                         func(m *weather.AlertMessage) { m.Urgency = "Future" },
		"certainty":                       func(m *weather.AlertMessage) { m.Certainty = "Likely" },
		"area":                            func(m *weather.AlertMessage) { m.Area += " and another county" },
		"description":                     func(m *weather.AlertMessage) { m.Description += " Evacuate immediately." },
		"instruction beyond display crop": func(m *weather.AlertMessage) { m.Instruction += " Changed final sentence." },
		"expiry":                          func(m *weather.AlertMessage) { m.Expires = m.Expires.Add(time.Hour) },
		"onset":                           func(m *weather.AlertMessage) { m.Onset = warningTestNow.Add(time.Hour) },
		"ends":                            func(m *weather.AlertMessage) { m.Ends = warningTestNow.Add(2 * time.Hour) },
		"issuer":                          func(m *weather.AlertMessage) { m.Issuer = "Another NWS Office" },
		"event":                           func(m *weather.AlertMessage) { m.Event = "Flash Flood Warning" },
	} {
		t.Run(name, func(t *testing.T) {
			l, _ := newWarningMemory(t)
			initial := a
			initial.Instruction = strings.Repeat("Original instruction. ", 300)
			warningObserve(t, l, warningTestNow, true, initial)
			warningAttempt(t, l, initial, "new", "sent", warningTestNow)
			u := initial
			u.Identity.ID, u.Identity.Sent, u.Type = "u", warningTestNow.Add(time.Minute), "Update"
			u.References = []weather.AlertReference{initial.Identity}
			change(&u)
			warningObserve(t, l, warningTestNow.Add(time.Minute), true, u)
			warningDecisions(t, l, warningTestNow.Add(time.Minute), warningDecision(u, "updated"))
		})
	}
	l, _ := newWarningMemory(t)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	u := warningFixture("reissue", "Update", warningTestNow.Add(time.Minute), a)
	u.Description = "Water\n is   rising."
	warningObserve(t, l, warningTestNow.Add(time.Minute), true, u)
	warningDecisions(t, l, warningTestNow.Add(time.Minute))
	b := warningFixture("b", "Update", warningTestNow.Add(2*time.Minute), u, a)
	b.Instruction = "Use the designated evacuation route."
	warningObserve(t, l, warningTestNow.Add(2*time.Minute), true, b)
	warningAttempt(t, l, b, "updated", "sent", warningTestNow.Add(2*time.Minute))
	b2 := b
	b2.Identity.ID, b2.Identity.Sent = "b2", warningTestNow.Add(3*time.Minute)
	b2.References = []weather.AlertReference{a.Identity, b.Identity, u.Identity}
	warningObserve(t, l, warningTestNow.Add(3*time.Minute), true, b2)
	warningDecisions(t, l, warningTestNow.Add(3*time.Minute))
	back := warningFixture("back to a", "Update", warningTestNow.Add(4*time.Minute), a, u, b, b2)
	warningObserve(t, l, warningTestNow.Add(4*time.Minute), true, back)
	warningDecisions(t, l, warningTestNow.Add(4*time.Minute), warningDecision(back, "updated"))
}

func TestWarningLedgerAbsenceExpiryFreshnessAndFuture(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	a.Expires = warningTestNow.Add(10 * time.Minute)
	warningObserve(t, l, warningTestNow, true, a)
	warningDecisions(t, l, warningTestNow.Add(warningFreshness+time.Nanosecond))
	warningObserve(t, l, warningTestNow.Add(time.Minute), true)
	warningDecisions(t, l, warningTestNow.Add(time.Minute))
	if s := l.Status(warningTestLocation, warningTestNow.Add(time.Minute)); s.Active != 1 || s.Canceled != 0 {
		t.Fatalf("absence canceled a warning: %+v", s)
	}
	if s := l.Status(warningTestLocation, a.Expires); s.Active != 0 || s.Expired != 1 || s.Canceled != 0 {
		t.Fatalf("expiry lifecycle = %+v", s)
	}
	warningObserve(t, l, a.Expires, true, a)
	warningDecisions(t, l, a.Expires)
	future := warningFixture("future", "Alert", a.Expires)
	future.Effective = a.Expires.Add(time.Minute)
	warningObserve(t, l, a.Expires, true, future)
	warningDecisions(t, l, a.Expires)
	if s := l.Status(warningTestLocation, a.Expires); s.Future != 1 {
		t.Fatalf("future state: %+v", s)
	}
	warningDecisions(t, l, future.Effective, warningDecision(future, "new"))
}

func TestWarningLedgerBranchCancellationDoesNotClaimAllClear(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	u := warningFixture("u", "Update", warningTestNow.Add(time.Minute), a)
	u.Instruction += " Follow new directions."
	c := warningCancel("c", warningTestNow.Add(2*time.Minute), a)
	warningObserve(t, l, warningTestNow.Add(2*time.Minute), true, u, c)
	warningDecisions(t, l, warningTestNow.Add(2*time.Minute), warningDecision(u, "updated"))
	if s := l.Status(warningTestLocation, warningTestNow.Add(2*time.Minute)); s.Active != 1 || s.Canceled != 1 {
		t.Fatalf("branch state: %+v", s)
	}
}

func TestWarningLedgerRejectsConflictCyclesAndStaleDecisions(t *testing.T) {
	l, files := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a)
	before, writes := safeio.Clone(files.doc), files.writes
	conflict := a
	conflict.Instruction = "Conflicting identity content"
	if err := l.Reconcile(WarningBatch{warningTestLocation, []weather.AlertMessage{conflict}, warningTestNow, true, warningCurrentKeys([]weather.AlertMessage{conflict}, warningTestNow)}, warningTestNow); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	if files.writes != writes || !reflect.DeepEqual(before, files.doc) {
		t.Fatal("failed batch changed state")
	}
	b := warningFixture("b", "Update", warningTestNow, a)
	c := warningFixture("c", "Update", warningTestNow, b)
	b.References = []weather.AlertReference{c.Identity}
	if err := l.Reconcile(WarningBatch{warningTestLocation, []weather.AlertMessage{b, c}, warningTestNow, true, warningCurrentKeys([]weather.AlertMessage{b, c}, warningTestNow)}, warningTestNow); err == nil {
		t.Fatal("same-second reference cycle accepted")
	}
	if files.writes != writes || !reflect.DeepEqual(before, files.doc) {
		t.Fatal("cyclic batch changed state")
	}
	u := warningFixture("u", "Update", warningTestNow.Add(time.Minute), a)
	warningObserve(t, l, warningTestNow.Add(time.Minute), true, u)
	if _, err := l.Reserve(warningDecision(a, "new"), warningTestNow.Add(time.Minute)); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("superseded decision reserved", err)
	}
	if _, err := l.Reserve(warningDecision(u, "updated"), warningTestNow.Add(time.Minute)); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("forged decision kind reserved", err)
	}
	warningDecisions(t, l, warningTestNow.Add(time.Minute), warningDecision(u, "new"))
}

func TestWarningLedgerWriteFailureStopsEffectsAndReloads(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(fmt.Sprint(published), func(t *testing.T) {
			l, files := newWarningMemory(t)
			a := warningFixture("a", "Alert", warningTestNow)
			warningObserve(t, l, warningTestNow, true, a)
			files.fail, files.publish = true, published
			if _, err := l.Reserve(warningDecision(a, "new"), warningTestNow); !errors.Is(err, ErrWarningLedgerWrite) {
				t.Fatal("write error did not forbid side effect", err)
			}
			warningDecisions(t, l, warningTestNow)
			writes := files.writes
			if err := l.Reconcile(WarningBatch{warningTestLocation, nil, warningTestNow, true, warningCurrentKeys(nil, warningTestNow)}, warningTestNow); !errors.Is(err, ErrWarningLedgerWrite) {
				t.Fatal("faulted ledger resumed", err)
			}
			if files.writes != writes {
				t.Fatal("faulted ledger wrote again")
			}
			files.fail = false
			fresh, err := openWarningLedger(files, nil)
			if err != nil {
				t.Fatal(err)
			}
			if published {
				warningDecisions(t, fresh, warningTestNow)
				if got := fresh.Receipts(); len(got) != 1 || got[0].Status != "uncertain" {
					t.Fatalf("uncertain reservation lost: %+v", got)
				}
			} else {
				warningDecisions(t, fresh, warningTestNow, warningDecision(a, "new"))
			}
		})
	}
}

func TestWarningLedgerLocationScopeAndLock(t *testing.T) {
	d := testState(t)
	l, err := OpenWarningLedger(d)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := OpenWarningLedger(d); err == nil {
		other.Close()
		t.Fatal("second warning owner acquired lock")
	}
	a := warningFixture("a", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	second := warningHash("second point")
	if err := l.Reconcile(WarningBatch{second, []weather.AlertMessage{a}, warningTestNow, true, warningCurrentKeys([]weather.AlertMessage{a}, warningTestNow)}, warningTestNow); err != nil {
		t.Fatal(err)
	}
	want := WarningDecision{second, a.Identity.Key(), "new"}
	if got := l.Candidates(second, warningTestNow); len(got) != 1 || got[0] != want {
		t.Fatalf("location deliveries merged: %+v", got)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Reconcile(WarningBatch{}, warningTestNow); !errors.Is(err, ErrWarningLedgerClosed) {
		t.Fatal("closed ledger accepted mutation", err)
	}
	fresh, err := OpenWarningLedger(d)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	fresh.Close()
	loc := weather.DefaultLocation()
	key, err := WarningLocationKey(loc)
	if err != nil {
		t.Fatal(err)
	}
	loc["name"], loc["timezone"] = "Different display name", "Etc/UTC"
	other, err := WarningLocationKey(loc)
	if err != nil || key != other {
		t.Fatal("presentation changed geographic identity", err)
	}
	loc["latitude"], loc["longitude"] = float64(0), float64(0)
	positive, _ := WarningLocationKey(loc)
	loc["latitude"] = math.Copysign(0, -1)
	negative, _ := WarningLocationKey(loc)
	if positive != negative {
		t.Fatal("negative zero changed geographic identity")
	}
	loc["latitude"] = math.Inf(1)
	if _, err := WarningLocationKey(loc); err == nil {
		t.Fatal("invalid point accepted")
	}
}

func TestWarningLedgerRejectsMalformedObservations(t *testing.T) {
	a := warningFixture("a", "Alert", warningTestNow)
	for name, change := range map[string]func(*WarningBatch){
		"plaintext location":   func(b *WarningBatch) { b.Location = "New York" },
		"missing fetched time": func(b *WarningBatch) { b.FetchedAt = time.Time{} },
		"future fetch":         func(b *WarningBatch) { b.FetchedAt = warningTestNow.Add(time.Nanosecond) },
		"stale fetch":          func(b *WarningBatch) { b.FetchedAt = warningTestNow.Add(-warningFreshness - time.Nanosecond) },
		"future message":       func(b *WarningBatch) { b.Messages[0].Identity.Sent = warningTestNow.Add(time.Second) },
		"missing identity":     func(b *WarningBatch) { b.Messages[0].Identity.ID = "" },
		"invalid instruction":  func(b *WarningBatch) { b.Messages[0].Instruction = string([]byte{255}) },
		"too many messages":    func(b *WarningBatch) { b.Messages = make([]weather.AlertMessage, WarningBatchLimit+1) },
		"too many references": func(b *WarningBatch) {
			b.Messages[0].References = make([]weather.AlertReference, weather.AlertReferenceLimit+1)
		},
		"too many source bytes": func(b *WarningBatch) { b.Messages[0].Description = strings.Repeat("x", 8<<20) },
	} {
		t.Run(name, func(t *testing.T) {
			l, files := newWarningMemory(t)
			b := WarningBatch{warningTestLocation, []weather.AlertMessage{a}, warningTestNow, true, warningCurrentKeys([]weather.AlertMessage{a}, warningTestNow)}
			change(&b)
			if err := l.Reconcile(b, warningTestNow); err == nil {
				t.Fatal("malformed observation accepted")
			}
			if files.writes != 0 || len(l.doc.Records) != 0 {
				t.Fatal("invalid observation mutated state")
			}
		})
	}
	l, files := newWarningMemory(t)
	warningObserve(t, l, warningTestNow, true, a)
	writes := files.writes
	if err := l.Reconcile(WarningBatch{warningTestLocation, []weather.AlertMessage{a}, warningTestNow.Add(-time.Second), true, warningCurrentKeys([]weather.AlertMessage{a}, warningTestNow.Add(-time.Second))}, warningTestNow); err == nil {
		t.Fatal("obsolete response replaced newer observation")
	}
	if err := l.Reconcile(WarningBatch{warningTestLocation, nil, warningTestNow, true, warningCurrentKeys(nil, warningTestNow)}, warningTestNow.Add(-time.Second)); !errors.Is(err, ErrWarningClock) {
		t.Fatal("clock reversal accepted", err)
	}
	warningDecisions(t, l, warningTestNow.Add(-time.Second))
	if l.Status(warningTestLocation, warningTestNow.Add(-time.Second)).Fresh {
		t.Fatal("reversed clock reported fresh history")
	}
	if files.writes != writes {
		t.Fatal("rejected chronology wrote state")
	}
}

func TestWarningLedgerStrictStateAndCorruption(t *testing.T) {
	l, files := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	original := files.doc
	for name, corrupt := range map[string]func(M){
		"unknown schema":          func(d M) { d["schema_version"] = 999.0 },
		"unknown field":           func(d M) { d["ignored"] = true },
		"missing records":         func(d M) { delete(d, "records") },
		"null receipts":           func(d M) { d["receipts"] = nil },
		"missing complete flag":   func(d M) { delete(d["coverage"].([]any)[0].(M), "complete") },
		"missing nested field":    func(d M) { delete(d["records"].([]any)[0].(M), "references") },
		"extra nested field":      func(d M) { d["records"].([]any)[0].(M)["source"] = "NWS" },
		"plaintext key":           func(d M) { d["records"].([]any)[0].(M)["key"] = "source-identifier" },
		"wrong type":              func(d M) { d["records"].([]any)[0].(M)["type"] = "AllClear" },
		"duplicate record":        func(d M) { d["records"] = append(d["records"].([]any), d["records"].([]any)[0]) },
		"duplicate receipt":       func(d M) { d["receipts"] = append(d["receipts"].([]any), d["receipts"].([]any)[0]) },
		"unmatched receipt":       func(d M) { d["receipts"].([]any)[0].(M)["decision"].(M)["key"] = warningHash("missing") },
		"wrong receipt kind":      func(d M) { d["receipts"].([]any)[0].(M)["decision"].(M)["kind"] = "canceled" },
		"unknown delivery status": func(d M) { d["receipts"].([]any)[0].(M)["status"] = "seen" },
		"future reservation": func(d M) {
			d["receipts"].([]any)[0].(M)["reserved_at"] = warningTestNow.Add(time.Minute).Format(time.RFC3339)
		},
		"future observation":   func(d M) { d["records"].([]any)[0].(M)["seen"] = warningTestNow.Add(time.Minute).Format(time.RFC3339) },
		"invalid expiry":       func(d M) { d["records"].([]any)[0].(M)["expires"] = warningTestNow.Format(time.RFC3339) },
		"unknown location":     func(d M) { d["coverage"] = []any{} },
		"duplicate location":   func(d M) { d["coverage"] = append(d["coverage"].([]any), d["coverage"].([]any)[0]) },
		"self reference":       func(d M) { d["records"].([]any)[0].(M)["references"] = []any{a.Identity.Key()} },
		"duplicate references": func(d M) { d["records"].([]any)[0].(M)["references"] = []any{warningHash("x"), warningHash("x")} },
	} {
		t.Run(name, func(t *testing.T) {
			v := safeio.Clone(original)
			corrupt(v)
			bad := &warningMemoryFiles{doc: v}
			if ledger, err := openWarningLedger(bad, nil); err == nil {
				ledger.Close()
				t.Fatal("corrupt state accepted")
			}
			if bad.writes != 0 || !reflect.DeepEqual(v, bad.doc) {
				t.Fatal("corrupt state overwritten")
			}
		})
	}
	// A real open failure releases the exclusive lock and leaves the evidence.
	dir := testState(t)
	if err := dir.Write(warningLedgerFile, M{"schema_version": 999.0}, warningLedgerLimit); err != nil {
		t.Fatal(err)
	}
	if got, err := OpenWarningLedger(dir); err == nil {
		got.Close()
		t.Fatal("invalid disk document accepted")
	}
	lock, err := dir.Lock("warning-ledger.lock")
	if err != nil {
		t.Fatal("failed open leaked lock", err)
	}
	lock.Close()
}

func TestWarningLedgerRetentionPreservesLiveChains(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("long lived", "Alert", warningTestNow)
	a.Expires = warningTestNow.Add(40 * 24 * time.Hour)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	later := warningTestNow.Add(20 * 24 * time.Hour)
	warningObserve(t, l, later, true, a)
	warningDecisions(t, l, later)
	if len(l.doc.Receipts) != 1 {
		t.Fatal("still-live warning lost receipt after retention")
	}
	u := warningFixture("long update", "Update", later.Add(time.Minute), a)
	u.Expires = a.Expires.Add(time.Hour)
	warningObserve(t, l, later.Add(time.Minute), true, u)
	warningDecisions(t, l, later.Add(time.Minute), warningDecision(u, "updated"))
	// Retain the entire connected graph, including old references/receipts.
	warningObserve(t, l, later.Add(warningRetention), true, u)
	if len(l.doc.Records) != 2 || len(l.doc.Receipts) != 1 {
		t.Fatal("part of a live revision chain was evicted")
	}
	// After every component member is expired and unseen for the retention
	// period, the entire component and its receipts can be removed together.
	end := warningTestNow.Add(60 * 24 * time.Hour)
	warningObserve(t, l, end, true)
	if len(l.doc.Records) != 0 || len(l.doc.Receipts) != 0 {
		t.Fatal("expired history was retained indefinitely")
	}
	warningObserve(t, l, end, true, a, u)
	warningDecisions(t, l, end)
	if len(l.doc.Records) != 0 {
		t.Fatal("ancient replay recreated expired history")
	}
}

func TestWarningLedgerBoundsFailWithoutEviction(t *testing.T) {
	t.Run("reference identities", func(t *testing.T) {
		l, files := newWarningMemory(t)
		rows := []weather.AlertMessage{}
		for i := range 16 {
			m := warningFixture(fmt.Sprint("update", i), "Update", warningTestNow)
			for j := range weather.AlertReferenceLimit {
				m.References = append(m.References, weather.AlertReference{ID: fmt.Sprint(i, "/", j), Sender: "fixture@noaa.gov", Sent: warningTestNow.Add(-time.Minute)})
			}
			rows = append(rows, m)
		}
		if err := l.Reconcile(WarningBatch{warningTestLocation, rows, warningTestNow, true, warningCurrentKeys(rows, warningTestNow)}, warningTestNow); !errors.Is(err, ErrWarningLedgerCapacity) {
			t.Fatal("placeholder identity budget not enforced", err)
		}
		if files.writes != 0 || len(l.doc.Records) != 0 {
			t.Fatal("overflow partially committed")
		}
	})
	t.Run("total links", func(t *testing.T) {
		l, files := newWarningMemory(t)
		rows := []weather.AlertMessage{}
		for i := range 65 {
			m := warningFixture(fmt.Sprint("u", i), "Update", warningTestNow)
			for j := range weather.AlertReferenceLimit {
				m.References = append(m.References, weather.AlertReference{ID: fmt.Sprint("shared", j), Sender: "fixture@noaa.gov", Sent: warningTestNow.Add(-time.Minute)})
			}
			rows = append(rows, m)
		}
		if err := l.Reconcile(WarningBatch{warningTestLocation, rows, warningTestNow, true, warningCurrentKeys(rows, warningTestNow)}, warningTestNow); !errors.Is(err, ErrWarningLedgerCapacity) {
			t.Fatal("edge budget not enforced", err)
		}
		if files.writes != 0 {
			t.Fatal("oversized graph reached disk")
		}
	})
	t.Run("locations", func(t *testing.T) {
		l, files := newWarningMemory(t)
		for i := range warningLocationLimit {
			if err := l.Reconcile(WarningBatch{warningHash(i), nil, warningTestNow, true, warningCurrentKeys(nil, warningTestNow)}, warningTestNow); err != nil {
				t.Fatal(err)
			}
		}
		before, writes := safeio.Clone(files.doc), files.writes
		if err := l.Reconcile(WarningBatch{warningTestLocation, nil, warningTestNow, true, warningCurrentKeys(nil, warningTestNow)}, warningTestNow); !errors.Is(err, ErrWarningLedgerCapacity) {
			t.Fatal("location budget not enforced", err)
		}
		if writes != files.writes || !reflect.DeepEqual(before, files.doc) {
			t.Fatal("location overflow evicted existing history")
		}
	})
	t.Run("receipts", func(t *testing.T) {
		l, files := newWarningMemory(t)
		rows := make([]weather.AlertMessage, warningReceiptLimit+1)
		for i := range rows {
			rows[i] = warningFixture(fmt.Sprint("a", i), "Alert", warningTestNow)
		}
		warningObserve(t, l, warningTestNow, true, rows...)
		for _, m := range rows[:warningReceiptLimit] {
			warningAttempt(t, l, m, "new", "reserved", warningTestNow)
		}
		before, writes := safeio.Clone(files.doc), files.writes
		if _, err := l.Reserve(warningDecision(rows[warningReceiptLimit], "new"), warningTestNow); !errors.Is(err, ErrWarningLedgerCapacity) {
			t.Fatal("receipt budget not enforced", err)
		}
		if files.writes != writes || !reflect.DeepEqual(before, files.doc) {
			t.Fatal("receipt overflow evicted reservations")
		}
	})
}

func TestWarningLedgerFinishRequiresExactReservation(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a)
	r := warningAttempt(t, l, a, "new", "reserved", warningTestNow)
	for _, change := range []func(*WarningReceipt){
		func(r *WarningReceipt) { r.Decision.Location = warningHash("wrong place") },
		func(r *WarningReceipt) { r.Decision.Key = warningHash("wrong revision") },
		func(r *WarningReceipt) { r.ReservedAt = r.ReservedAt.Add(time.Nanosecond) },
		func(r *WarningReceipt) { r.Status = "sent" },
	} {
		other := r
		change(&other)
		if err := l.Finish(other, "sent", warningTestNow); !errors.Is(err, ErrWarningDecision) {
			t.Fatal("mismatched worker completion accepted", err)
		}
	}
	if err := l.Finish(r, "seen", warningTestNow); err == nil {
		t.Fatal("unknown completion status accepted")
	}
	if err := l.Finish(r, "failed", warningTestNow); err != nil {
		t.Fatal(err)
	}
	if err := l.Finish(r, "sent", warningTestNow); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("completed attempt overwritten", err)
	}
	copy := l.Receipts()
	copy[0].Status = "reserved"
	if l.Receipts()[0].Status != "failed" {
		t.Fatal("receipt slice exposed mutable ledger")
	}
}

func BenchmarkWarningLedgerCandidates(b *testing.B) {
	for _, count := range []int{1, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			l, _ := newWarningMemory(b)
			rows := make([]weather.AlertMessage, count)
			for i := range rows {
				rows[i] = warningFixture(fmt.Sprint("a", i), "Alert", warningTestNow)
			}
			warningObserve(b, l, warningTestNow, true, rows...)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if len(l.Candidates(warningTestLocation, warningTestNow)) != count {
					b.Fatal("lost candidates")
				}
			}
		})
	}
}

func TestWarningLedgerOnlyExplicitLifecycleReferencesSupersede(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	// A reference on an initial Alert is not an Update/Cancel instruction.
	b := warningFixture("b", "Alert", warningTestNow.Add(time.Minute), a)
	warningObserve(t, l, warningTestNow.Add(time.Minute), true, b, a)
	warningDecisions(t, l, warningTestNow.Add(time.Minute), warningDecision(a, "new"), warningDecision(b, "new"))
	warningAttempt(t, l, a, "new", "sent", warningTestNow.Add(time.Minute))
	warningAttempt(t, l, b, "new", "sent", warningTestNow.Add(time.Minute))
	// An explicit update can merge earlier independent warning threads.
	u := warningFixture("merge", "Update", warningTestNow.Add(2*time.Minute), a, b)
	u.Instruction = "Changed directions for both warning areas."
	warningObserve(t, l, warningTestNow.Add(2*time.Minute), true, u)
	warningDecisions(t, l, warningTestNow.Add(2*time.Minute), warningDecision(u, "updated"))
}

func TestWarningLedgerCancellationAgeAndReservationGates(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	a.Expires = warningTestNow.Add(48 * time.Hour)
	warningObserve(t, l, warningTestNow, true, a)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	c := warningCancel("c", warningTestNow.Add(time.Minute), a)
	now := c.Identity.Sent.Add(warningCancelAge + time.Second)
	warningObserve(t, l, now, true, c)
	warningDecisions(t, l, now)
	if s := l.Status(warningTestLocation, now); s.Canceled != 1 || s.Active != 0 {
		t.Fatalf("old cancellation was lost: %+v", s)
	}
	fresh := warningFixture("fresh", "Alert", now)
	fresh.Expires = now.Add(time.Hour)
	warningObserve(t, l, now, true, fresh)
	d := warningDecision(fresh, "new")
	warningDecisions(t, l, now, d)
	warningObserve(t, l, now, false, fresh)
	if _, err := l.Reserve(d, now); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("partial history authorized delivery", err)
	}
	warningObserve(t, l, now, true, fresh)
	if _, err := l.Reserve(d, now.Add(warningFreshness+time.Nanosecond)); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("stale history authorized delivery", err)
	}
	warningObserve(t, l, fresh.Expires, true, fresh)
	if _, err := l.Reserve(d, fresh.Expires); !errors.Is(err, ErrWarningDecision) {
		t.Fatal("expired warning authorized delivery", err)
	}
}

func TestWarningLedgerMaximumDocumentFitsDiskBudget(t *testing.T) {
	// Exercise the independent identity and edge budgets together. Graph
	// placeholders count toward the identity limit; there are no hidden arrays.
	l, files := newWarningMemory(t)
	rows := make([]weather.AlertMessage, 1024)
	for i := range rows {
		rows[i] = warningFixture(fmt.Sprint("root", i), "Alert", warningTestNow)
	}
	warningObserve(t, l, warningTestNow, true, rows...)
	updates := make([]weather.AlertMessage, 1024)
	for i := range updates {
		updates[i] = warningFixture(fmt.Sprint("update", i), "Update", warningTestNow.Add(time.Minute))
		for j := range 8 {
			updates[i].References = append(updates[i].References, rows[(i+j)%len(rows)].Identity)
		}
	}
	warningObserve(t, l, warningTestNow.Add(time.Minute), true, updates...)
	if len(l.doc.Records) != warningNodeLimit {
		t.Fatal("maximum identity fixture did not reach bound")
	}
	raw, err := json.Marshal(files.doc)
	if err != nil || len(raw) > warningLedgerLimit {
		t.Fatalf("maximum graph exceeds disk bound: %d %v", len(raw), err)
	}
	more := warningFixture("overflow", "Alert", warningTestNow.Add(time.Minute))
	writes := files.writes
	if err := l.Reconcile(WarningBatch{warningTestLocation, []weather.AlertMessage{more}, warningTestNow.Add(time.Minute), true, warningCurrentKeys([]weather.AlertMessage{more}, warningTestNow.Add(time.Minute))}, warningTestNow.Add(time.Minute)); !errors.Is(err, ErrWarningLedgerCapacity) {
		t.Fatal("real identity budget not enforced", err)
	}
	if files.writes != writes {
		t.Fatal("overfull graph wrote state")
	}
}

// This measures pure candidate selection and whole reconciliation including
// JSON serialization into a bounded in-memory file fixture. It excludes disk
// fsync, provider I/O and application/UI costs; those belong to integration QA.
func BenchmarkWarningLedgerReconcile(b *testing.B) {
	for _, count := range []int{1, 256} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			l, _ := newWarningMemory(b)
			rows := make([]weather.AlertMessage, count)
			for i := range rows {
				rows[i] = warningFixture(fmt.Sprint("a", i), "Alert", warningTestNow)
			}
			batch := WarningBatch{warningTestLocation, rows, warningTestNow, true, warningCurrentKeys(rows, warningTestNow)}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if err := l.Reconcile(batch, warningTestNow); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkWarningLedgerStatus(b *testing.B) {
	l, _ := newWarningMemory(b)
	rows := make([]weather.AlertMessage, 256)
	for i := range rows {
		rows[i] = warningFixture(fmt.Sprint("a", i), "Alert", warningTestNow)
	}
	warningObserve(b, l, warningTestNow, true, rows...)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if l.Status(warningTestLocation, warningTestNow).Active != len(rows) {
			b.Fatal("lost active warning status")
		}
	}
}

func warningCurrentKeys(messages []weather.AlertMessage, now time.Time) []string {
	keys := []string{}
	seen := map[string]bool{}
	for _, m := range messages {
		key := m.Identity.Key()
		if m.Type != "Cancel" && m.Expires.After(now) && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}

func TestWarningHistoryWithoutCurrentObservationCannotNotify(t *testing.T) {
	l, _ := newWarningMemory(t)
	a := warningFixture("historical", "Alert", warningTestNow)
	b := WarningBatch{Location: warningTestLocation, Messages: []weather.AlertMessage{a}, FetchedAt: warningTestNow, Complete: true}
	if err := l.Reconcile(b, warningTestNow); err != nil {
		t.Fatal(err)
	}
	warningDecisions(t, l, warningTestNow)
	if s := l.Status(warningTestLocation, warningTestNow); s.Active != 1 || s.Canceled != 0 {
		t.Fatalf("history absence changed lifecycle: %+v", s)
	}
	b.CurrentKeys = []string{a.Identity.Key()}
	if err := l.Reconcile(b, warningTestNow); err != nil {
		t.Fatal(err)
	}
	warningDecisions(t, l, warningTestNow, warningDecision(a, "new"))
}

func TestWarningLedgerMigrationRequiresFreshCurrentObservation(t *testing.T) {
	l, files := newWarningMemory(t)
	a := warningFixture("a", "Alert", warningTestNow)
	b := warningFixture("b", "Alert", warningTestNow)
	warningObserve(t, l, warningTestNow, true, a, b)
	warningAttempt(t, l, a, "new", "sent", warningTestNow)
	files.doc["schema_version"] = 1.0
	for _, v := range files.doc["coverage"].([]any) {
		delete(v.(M), "current_keys")
	}
	migrated, err := openWarningLedger(files, nil)
	if err != nil {
		t.Fatal(err)
	}
	warningDecisions(t, migrated, warningTestNow)
	if len(migrated.Receipts()) != 1 || migrated.Receipts()[0].Status != "sent" {
		t.Fatal("migration lost receipts")
	}
	warningObserve(t, migrated, warningTestNow, true, a, b)
	warningDecisions(t, migrated, warningTestNow, warningDecision(b, "new"))
	if files.doc["schema_version"] != 2.0 {
		t.Fatal("new schema not persisted after observation")
	}
}
