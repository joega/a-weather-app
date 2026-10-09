package notifications

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const (
	warningLedgerFile    = "warning-ledger.json"
	warningLedgerLimit   = 2 << 20
	warningNodeLimit     = 2048 // Includes referenced identities whose bodies are absent.
	warningLinkLimit     = 8192
	warningReceiptLimit  = 256
	warningLocationLimit = 20
	warningRetention     = 14 * 24 * time.Hour
	warningFreshness     = 5 * time.Minute
	warningCancelAge     = 24 * time.Hour
	// WarningBatchLimit caps an assembled reconciliation, not automatic paging.
	WarningBatchLimit = 4 * weather.AlertMessageLimit
)

var (
	ErrWarningLedgerClosed   = errors.New("warning ledger is closed")
	ErrWarningLedgerWrite    = errors.New("warning ledger persistence failed; reopen required")
	ErrWarningLedgerCapacity = errors.New("warning ledger capacity reached")
	ErrWarningClock          = errors.New("warning clock moved backwards")
	ErrWarningDecision       = errors.New("warning delivery decision is no longer eligible")
)

// WarningBatch is an assembled provider observation for one geographic point.
// Complete must be false until the caller has reconciled all required history
// pages and current observations. It is never an all-clear or cancellation.
// The caller must discard results from obsolete location/request generations.
// Incomplete observations are retained, but cannot authorize desktop delivery.
// No source bodies are persisted. Keep bounded current bodies in the caller if
// needed for notification presentation or detail navigation.
type WarningBatch struct {
	Location  string
	Messages  []weather.AlertMessage
	FetchedAt time.Time
	Complete  bool
}

// WarningDecision identifies one terminal revision. Kind is new, updated or
// canceled. It contains no text; the caller resolves the full current message.
type WarningDecision struct {
	Location string `json:"location"`
	Key      string `json:"key"`
	Kind     string `json:"kind"`
}

// WarningReceipt is an at-most-once attempt reservation, not proof a person saw
// a notification. A reserved receipt loaded after restart becomes uncertain;
// failed and uncertain attempts are never automatically retried.
type WarningReceipt struct {
	Decision   WarningDecision `json:"decision"`
	ReservedAt time.Time       `json:"reserved_at"`
	Status     string          `json:"status"`
}

type warningRecord struct {
	Location   string    `json:"location"`
	Key        string    `json:"key"`
	Type       string    `json:"type"`
	Digest     string    `json:"digest"`
	Material   string    `json:"material"`
	References []string  `json:"references"`
	Sent       time.Time `json:"sent"`
	Effective  time.Time `json:"effective"`
	Expires    time.Time `json:"expires"`
	Seen       time.Time `json:"seen"`
}

type warningCoverage struct {
	Location  string    `json:"location"`
	FetchedAt time.Time `json:"fetched_at"`
	Complete  bool      `json:"complete"`
}

type warningDocument struct {
	Schema    int               `json:"schema_version"`
	UpdatedAt time.Time         `json:"updated_at"`
	Records   []warningRecord   `json:"records"`
	Receipts  []WarningReceipt  `json:"receipts"`
	Coverage  []warningCoverage `json:"coverage"`
}

type warningFiles interface {
	Read(string, int) (map[string]any, error)
	Write(string, any, int) error
}

// WarningLedger owns an exclusive state lock and bounded metadata. Its owner
// serializes every method and closes it before closing the state directory.
// It has no timers, workers, network access, or desktop side effects. Open only
// when warning monitoring is enabled; the existing disabled path creates no
// ledger or lock. Any failed write permanently stops this instance, including
// failures after an atomic rename. Reopening re-reads the authoritative file.
type WarningLedger struct {
	files  warningFiles
	lock   io.Closer
	doc    warningDocument
	graph  map[string]*warningVertex
	closed bool
	fault  error
}

// OpenWarningLedger locks before reading authoritative state. Corrupt state is
// never replaced with empty history. The caller retains directory ownership.
func OpenWarningLedger(state *safeio.Directory) (*WarningLedger, error) {
	lock, err := state.Lock("warning-ledger.lock")
	if err != nil {
		return nil, err
	}
	ledger, err := openWarningLedger(state, lock)
	if err != nil {
		lock.Close()
	}
	return ledger, err
}

func openWarningLedger(files warningFiles, lock io.Closer) (*WarningLedger, error) {
	v, err := files.Read(warningLedgerFile, warningLedgerLimit)
	if err != nil {
		return nil, err
	}
	doc := warningDocument{Schema: 1, Records: []warningRecord{}, Receipts: []WarningReceipt{}, Coverage: []warningCoverage{}}
	if v != nil {
		doc, err = decodeWarningDocument(v)
		if err != nil {
			return nil, err
		}
	}
	for i := range doc.Receipts {
		if doc.Receipts[i].Status == "reserved" {
			doc.Receipts[i].Status = "uncertain"
		}
	}
	graph, err := buildWarningGraph(doc)
	if err != nil {
		return nil, err
	}
	return &WarningLedger{files: files, lock: lock, doc: doc, graph: graph}, nil
}

func (l *WarningLedger) Close() error {
	if l.closed {
		return nil
	}
	l.closed = true
	if l.lock != nil {
		return l.lock.Close()
	}
	return nil
}

func warningHash(v any) string {
	raw, _ := json.Marshal(v) // All callers use validated strings/times/finite coordinates.
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// WarningLocationKey identifies an NWS point without retaining coordinates,
// names or aliases in the ledger. The caller separately verifies US coverage.
func WarningLocationKey(location M) (string, error) {
	loc, err := weather.ValidateLocation(location)
	if err != nil {
		return "", err
	}
	lat, lon := loc["latitude"].(float64), loc["longitude"].(float64)
	if lat == 0 {
		lat = 0
	} // Canonicalize negative zero.
	if lon == 0 {
		lon = 0
	}
	return warningHash([]any{"NWS", lat, lon}), nil
}

func warningMaterial(m weather.AlertMessage) string {
	// Source whitespace, CAP identities and effective/issue timestamps alone
	// do not warrant another interruption. Changed expiry, onset/end, area,
	// hazard, severity, certainty, urgency or source text does. Include the
	// full headline: it may carry information absent from the description.
	fields := []string{m.Issuer, m.Event, m.Severity, m.Urgency, m.Certainty, m.Area, m.Headline, m.Description, m.Instruction}
	for i := range fields {
		fields[i] = strings.Join(strings.Fields(fields[i]), " ")
	}
	return warningHash(struct {
		Fields               []string
		Expires, Onset, Ends time.Time
	}{fields, m.Expires, m.Onset, m.Ends})
}

func warningNow(now time.Time) bool         { return !now.IsZero() && now.Year() >= 1 && now.Year() <= 9999 }
func warningID(location, key string) string { return location + ":" + key }

func (l *WarningLedger) ready(now time.Time) error {
	if l.closed {
		return ErrWarningLedgerClosed
	}
	if l.fault != nil {
		return l.fault
	}
	if !warningNow(now) || now.Before(l.doc.UpdatedAt) {
		return ErrWarningClock
	}
	return nil
}

func (l *WarningLedger) commit(doc warningDocument, now time.Time) error {
	doc.UpdatedAt = now.UTC()
	if err := validateWarningDocument(doc); err != nil {
		return err
	}
	graph, err := buildWarningGraph(doc)
	if err != nil {
		return err
	}
	if err := l.files.Write(warningLedgerFile, doc, warningLedgerLimit); err != nil {
		// No side effect is authorized after any uncertain persistence result.
		// Keep the instance faulted even if the write became visible on disk.
		l.fault = errors.Join(ErrWarningLedgerWrite, err)
		return l.fault
	}
	l.doc = doc
	l.graph = graph
	return nil
}

func cloneWarningDocument(d warningDocument) warningDocument {
	// Record reference slices are immutable after creation.
	d.Records = append([]warningRecord{}, d.Records...)
	d.Receipts = append([]WarningReceipt{}, d.Receipts...)
	d.Coverage = append([]warningCoverage{}, d.Coverage...)
	return d
}

// Reconcile validates and atomically stores a bounded observation. It collapses
// superseded revisions before any delivery is offered, regardless of input
// order. Missing entries are not canceled, and an expired record is not an
// explicit cancellation. Invalid/conflicting batches leave state unchanged.
func (l *WarningLedger) Reconcile(batch WarningBatch, now time.Time) error {
	if err := l.ready(now); err != nil {
		return err
	}
	if !fingerprint.MatchString(batch.Location) || !warningNow(batch.FetchedAt) || batch.FetchedAt.After(now) || now.Sub(batch.FetchedAt) > warningFreshness || len(batch.Messages) > WarningBatchLimit {
		return errors.New("invalid warning observation")
	}
	for _, c := range l.doc.Coverage {
		if c.Location == batch.Location && batch.FetchedAt.Before(c.FetchedAt) {
			return errors.New("obsolete warning observation")
		}
	}
	// Bound aggregate source bytes before building normalization/hash copies.
	bytes := 0
	for _, m := range batch.Messages {
		for _, s := range []string{m.Identity.ID, m.Identity.Sender, m.Issuer, m.Event, m.Area, m.Headline, m.Description, m.Instruction, m.Severity, m.Urgency, m.Certainty} {
			bytes += len(s)
		}
		if len(m.References) > weather.AlertReferenceLimit {
			return ErrWarningLedgerCapacity
		}
		for _, r := range m.References {
			bytes += len(r.ID) + len(r.Sender)
		}
		if bytes > 8<<20 {
			return ErrWarningLedgerCapacity
		}
	}
	doc := pruneWarningDocument(l.doc, now)
	positions := make(map[string]int, len(doc.Records))
	for i, r := range doc.Records {
		positions[warningID(r.Location, r.Key)] = i
	}
	for _, input := range batch.Messages {
		m, err := weather.NormalizeAlertMessage(input)
		if err != nil {
			return err
		}
		if m.Identity.Sent.After(batch.FetchedAt) {
			return errors.New("warning sent after observation")
		}
		if !m.Identity.Sent.Add(warningRetention).After(now) && !m.Expires.After(now) {
			continue
		}
		refs := make([]string, 0, len(m.References))
		for _, r := range m.References {
			refs = append(refs, r.Key())
		}
		sort.Strings(refs)
		r := warningRecord{Location: batch.Location, Key: m.Identity.Key(), Type: m.Type, Digest: warningHash(m), Material: warningMaterial(m), References: refs, Sent: m.Identity.Sent, Effective: m.Effective, Expires: m.Expires, Seen: batch.FetchedAt.UTC()}
		id := warningID(r.Location, r.Key)
		if i, ok := positions[id]; ok {
			if doc.Records[i].Digest != r.Digest {
				return errors.New("conflicting warning identity")
			}
			doc.Records[i].Seen = r.Seen
		} else {
			positions[id] = len(doc.Records)
			doc.Records = append(doc.Records, r)
			if len(doc.Records) > warningNodeLimit {
				return ErrWarningLedgerCapacity
			}
		}
	}
	coverage := warningCoverage{batch.Location, batch.FetchedAt.UTC(), batch.Complete}
	found := false
	for i := range doc.Coverage {
		if doc.Coverage[i].Location == batch.Location {
			doc.Coverage[i], found = coverage, true
			break
		}
	}
	if !found {
		doc.Coverage = append(doc.Coverage, coverage)
	}
	return l.commit(doc, now)
}

// Candidates returns eligible decisions in stable source-time/key order. The
// caller applies opt-in, severity, quiet hours and snooze before Reserve. Call
// only for the current target. Re-evaluation is required after any state change.
func (l *WarningLedger) Candidates(location string, now time.Time) []WarningDecision {
	if l.ready(now) != nil {
		return nil
	}
	var coverage warningCoverage
	for _, c := range l.doc.Coverage {
		if c.Location == location {
			coverage = c
			break
		}
	}
	if !coverage.Complete || coverage.FetchedAt.After(now) || now.Sub(coverage.FetchedAt) > warningFreshness {
		return nil
	}
	var nodes []*warningVertex
	for _, v := range l.graph {
		r := v.record
		if r == nil || r.Location != location || len(v.children) != 0 || v.receipt != nil || !r.Seen.Equal(coverage.FetchedAt) {
			continue
		}
		if r.Type == "Cancel" {
			if now.Sub(r.Sent) <= warningCancelAge {
				nodes = append(nodes, v)
			}
		} else if !r.Effective.After(now) && r.Expires.After(now) {
			nodes = append(nodes, v)
		}
	}
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i].record, nodes[j].record
		if !a.Sent.Equal(b.Sent) {
			return a.Sent.Before(b.Sent)
		}
		return a.Key < b.Key
	})
	out := []WarningDecision{}
	for _, v := range nodes {
		prior := warningPriorReceipts(v)
		kind := "new"
		if v.record.Type == "Cancel" {
			attemptedWarning := false
			for _, p := range prior {
				if p.record.Type != "Cancel" {
					attemptedWarning = true
					break
				}
			}
			if !attemptedWarning || warningHasLiveBranch(v, now) {
				continue
			}
			kind = "canceled"
		} else if len(prior) > 0 {
			changed := false
			for _, p := range prior {
				if p.record.Material != v.record.Material || p.record.Type == "Cancel" {
					changed = true
					break
				}
			}
			if !changed {
				continue
			}
			kind = "updated"
		}
		out = append(out, WarningDecision{location, v.record.Key, kind})
	}
	return out
}

// Reserve durably records an attempt before the caller starts desktop delivery.
// Any error prohibits that side effect. Quiet/snoozed/disabled decisions should
// not be reserved. No failed or uncertain reservation is automatically retried.
func (l *WarningLedger) Reserve(decision WarningDecision, now time.Time) (WarningReceipt, error) {
	if err := l.ready(now); err != nil {
		return WarningReceipt{}, err
	}
	eligible := false
	for _, d := range l.Candidates(decision.Location, now) {
		if d == decision {
			eligible = true
			break
		}
	}
	if !eligible {
		return WarningReceipt{}, ErrWarningDecision
	}
	if len(l.doc.Receipts) >= warningReceiptLimit {
		return WarningReceipt{}, ErrWarningLedgerCapacity
	}
	doc := cloneWarningDocument(l.doc)
	r := WarningReceipt{decision, now.UTC(), "reserved"}
	doc.Receipts = append(doc.Receipts, r)
	if err := l.commit(doc, now); err != nil {
		return WarningReceipt{}, err
	}
	return r, nil
}

// Finish records a worker result only for this instance's exact reservation.
// A late result cannot complete another revision, place or restarted attempt.
// sent means the desktop sender returned successfully; it is not read receipt.
func (l *WarningLedger) Finish(receipt WarningReceipt, status string, now time.Time) error {
	if err := l.ready(now); err != nil {
		return err
	}
	if status != "sent" && status != "failed" && status != "uncertain" {
		return errors.New("invalid warning delivery status")
	}
	doc := cloneWarningDocument(l.doc)
	for i, r := range doc.Receipts {
		if r.Decision == receipt.Decision && r.ReservedAt.Equal(receipt.ReservedAt) && r.Status == "reserved" && receipt.Status == "reserved" {
			doc.Receipts[i].Status = status
			return l.commit(doc, now)
		}
	}
	return ErrWarningDecision
}

// Receipts returns an independent, bounded copy, with no source text.
func (l *WarningLedger) Receipts() []WarningReceipt {
	return append([]WarningReceipt{}, l.doc.Receipts...)
}

// WarningLedgerStatus distinguishes observed lifecycle state from history
// freshness. Active counts tracked unexpired terminal messages, even when the
// feed is stale or omits them. Canceled counts explicit terminal Cancel records.
// Neither an empty response nor these counts asserts all clear.
type WarningLedgerStatus struct {
	FetchedAt                         time.Time
	Complete, Fresh                   bool
	Active, Future, Expired, Canceled int
}

func (l *WarningLedger) Status(location string, now time.Time) WarningLedgerStatus {
	var out WarningLedgerStatus
	for _, c := range l.doc.Coverage {
		if c.Location == location {
			out.FetchedAt, out.Complete = c.FetchedAt, c.Complete
			out.Fresh = l.ready(now) == nil && !c.FetchedAt.After(now) && now.Sub(c.FetchedAt) <= warningFreshness
		}
	}
	for _, v := range l.graph {
		r := v.record
		if r == nil || r.Location != location || len(v.children) != 0 {
			continue
		}
		switch {
		case r.Type == "Cancel":
			out.Canceled++
		case !r.Expires.After(now):
			out.Expired++
		case r.Effective.After(now):
			out.Future++
		default:
			out.Active++
		}
	}
	return out
}

func decodeWarningDocument(v M) (warningDocument, error) {
	var d warningDocument
	raw, err := json.Marshal(v)
	if err != nil || len(raw) > warningLedgerLimit {
		return d, errors.New("invalid warning ledger size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, err
	}
	if err := validateWarningDocument(d); err != nil {
		return d, err
	}
	// Reject missing fields as well as unknown fields. No permissive zero-value
	// defaults may erase receipt or coverage information in a saved document.
	encoded, err := json.Marshal(d)
	if err != nil {
		return d, err
	}
	canonical, err := safeio.Object(encoded, warningLedgerLimit)
	if err != nil || !reflect.DeepEqual(v, canonical) {
		return d, errors.New("noncanonical warning ledger")
	}
	return d, nil
}

func validateWarningDocument(d warningDocument) error {
	if d.Schema != 1 || !warningNow(d.UpdatedAt) || d.Records == nil || d.Receipts == nil || d.Coverage == nil {
		return errors.New("warning ledger schema")
	}
	if len(d.Records) > warningNodeLimit || len(d.Receipts) > warningReceiptLimit || len(d.Coverage) > warningLocationLimit {
		return ErrWarningLedgerCapacity
	}
	locations := map[string]bool{}
	for _, c := range d.Coverage {
		if !fingerprint.MatchString(c.Location) || locations[c.Location] || !warningNow(c.FetchedAt) || c.FetchedAt.After(d.UpdatedAt) {
			return errors.New("warning coverage")
		}
		locations[c.Location] = true
	}
	for _, r := range d.Records {
		if !locations[r.Location] || !fingerprint.MatchString(r.Key) || !fingerprint.MatchString(r.Digest) || !fingerprint.MatchString(r.Material) || r.References == nil || len(r.References) > weather.AlertReferenceLimit || !warningNow(r.Sent) || !warningNow(r.Seen) || r.Sent.After(r.Seen) || r.Seen.After(d.UpdatedAt) || !warningNow(r.Effective) {
			return errors.New("warning record")
		}
		if r.Type != "Alert" && r.Type != "Update" && r.Type != "Cancel" || r.Type != "Alert" && len(r.References) == 0 {
			return errors.New("warning record type")
		}
		if r.Type != "Cancel" && (!warningNow(r.Expires) || !r.Expires.After(r.Effective)) || !r.Expires.IsZero() && !warningNow(r.Expires) {
			return errors.New("warning validity")
		}
		last := ""
		for _, key := range r.References {
			if !fingerprint.MatchString(key) || key <= last || key == r.Key {
				return errors.New("warning reference")
			}
			last = key
		}
	}
	graph, err := buildWarningGraph(d)
	if err != nil {
		return err
	}
	for _, receipt := range d.Receipts {
		v := graph[warningID(receipt.Decision.Location, receipt.Decision.Key)]
		if v == nil || v.record == nil || !warningNow(receipt.ReservedAt) || receipt.ReservedAt.After(d.UpdatedAt) || receipt.ReservedAt.Before(v.record.Sent) {
			return errors.New("warning receipt identity")
		}
		switch receipt.Status {
		case "reserved", "sent", "failed", "uncertain":
		default:
			return errors.New("warning receipt status")
		}
		kind := receipt.Decision.Kind
		if v.record.Type == "Cancel" && kind != "canceled" || v.record.Type != "Cancel" && kind != "new" && kind != "updated" {
			return errors.New("warning receipt kind")
		}
	}
	return nil
}

func warningError(detail string) error { return fmt.Errorf("invalid warning graph: %s", detail) }
