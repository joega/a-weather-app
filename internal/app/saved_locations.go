package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/joega/a-weather-app/internal/weather"
)

const (
	savedLocationsFile = "saved-locations.json"
	savedLocationLimit = 20
	savedMetadataBytes = 64 * 1024
	savedForecastLimit = 4
	savedForecastSlots = savedForecastLimit + 1
)

var (
	errSavedLocationsChanged     = errors.New("saved locations changed; reload required")
	errSavedLocationsUnconfirmed = errors.New("saved locations published but durability unconfirmed")
)

// The caller holds the existing service/state lock for the store's lifetime.
// Keeping this narrow also permits fault injection at atomic-write boundaries.
// Production uses safeio.Directory's owned, descriptor-relative file access.
type savedLocationFiles interface {
	Read(string, int) (M, error)
	Write(string, any, int) error
}

// savedLocations owns only small metadata, never full forecasts. Reads of the
// list do not read cache files. Four immutable cache references share five fixed
// disk slots: a new forecast uses the spare before the manifest is committed.
// The unreferenced slot is bounded staging space, reused after failures/restart.
// No goroutines, providers or timers belong to this store. Its caller serializes
// access. Documents and cached profiles returned to callers are independent.
type savedLocations struct {
	files savedLocationFiles
	doc   M
}

func savedToken() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validSavedToken(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

func savedFields(value M, keys ...string) bool {
	if len(value) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := value[key]; !ok {
			return false
		}
	}
	return true
}

func savedSlotName(slot int) string { return fmt.Sprintf("saved-forecast-%d.json", slot) }

func savedProfileIdentity(profile M) M {
	identity := M{}
	for key, value := range profile {
		if key != "forecast" {
			identity[key] = weather.Clone(value)
		}
	}
	identity["forecast"] = nil
	return identity
}

// IDs describe the selection, not a label or mutable current-location fix.
// ZIP and provider-place selections remain distinct even at equal coordinates.
func savedLocationID(profile M) (string, error) {
	location, err := validateProfileIdentity(profile)
	if err != nil || profile["schema_version"] != 2.0 {
		return "", errors.New("invalid saved location identity")
	}
	switch profile["mode"] {
	case "auto":
		return "current", nil
	case "default":
		return "default", nil
	case "zip":
		return "zip-" + stringOf(profile["zip_code"]), nil
	case "place":
		return "place-" + strconv.FormatFloat(object(profile["place"])["id"].(float64), 'f', 0, 64), nil
	case "custom":
		// Name and country metadata do not change a custom coordinate identity.
		coordinates := M{"latitude": location["latitude"], "longitude": location["longitude"], "timezone": location["timezone"]}
		for _, key := range []string{"latitude", "longitude"} {
			if coordinates[key] == float64(0) {
				coordinates[key] = float64(0)
			}
		}
		raw, err := json.Marshal(coordinates)
		if err != nil {
			return "", err
		}
		digest := sha256.Sum256(raw)
		return "custom-" + hex.EncodeToString(digest[:]), nil
	default:
		return "", errors.New("unsupported saved location mode")
	}
}

func savedLabel(label any) error {
	s, ok := label.(string)
	if !ok || !utf8.ValidString(s) || s != strings.TrimSpace(s) || len([]rune(s)) > 80 {
		return errors.New("invalid saved location label")
	}
	plain, err := weather.Plain(s, 80, false)
	if err != nil || plain != s {
		return errors.New("invalid saved location label")
	}
	return nil
}

func savedTime(value any) string {
	stamp, err := weather.Instant(value)
	if err != nil {
		return ""
	}
	return stamp.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
}

func savedSummary(profile M) M {
	forecast := object(profile["forecast"])
	if forecast == nil {
		return nil
	}
	current, _ := weather.WeatherRecord(object(forecast["current"]))
	summary := M{"fetched_at": savedTime(forecast["fetched_at"]), "valid_at": current["time"], "temperature_c": current["temperature_c"], "condition": current["condition"], "is_day": current["is_day"], "alert_expires": nil, "alert_fetched_at": nil, "alert_status": "unavailable"}
	if profile["country_code"] != "US" {
		if profile["country_code"] != nil {
			summary["alert_status"] = "not_supported_here"
		}
		return summary
	}
	alerts := object(forecast["alerts"])
	_, err := weather.Instant(alerts["fetched_at"])
	if alerts["status"] != "available" || err != nil {
		return summary
	}
	summary["alert_fetched_at"] = savedTime(alerts["fetched_at"])
	summary["alert_status"] = "current"
	if alerts["freshness"] == "stale" {
		summary["alert_status"] = "stale"
	}
	// A badge denotes at least one alert, not a count or an all-clear claim.
	// Keeping only the last expiry bounds metadata independently of feed size.
	rows, _ := alerts["items"].([]any)
	for _, row := range rows {
		expires, err := weather.Instant(object(row)["expires"])
		if err != nil {
			continue
		}
		previous, _ := weather.Instant(summary["alert_expires"])
		if summary["alert_expires"] == nil || expires.After(previous) {
			summary["alert_expires"] = savedTime(object(row)["expires"])
		}
	}
	return summary
}

func validateSavedSummary(summary M, country any) error {
	if summary == nil {
		return nil
	}
	if !savedFields(summary, "fetched_at", "valid_at", "temperature_c", "condition", "is_day", "alert_expires", "alert_fetched_at", "alert_status") {
		return errors.New("invalid saved summary fields")
	}
	for _, key := range []string{"fetched_at", "valid_at"} {
		if savedTime(summary[key]) == "" || summary[key] != savedTime(summary[key]) {
			return errors.New("invalid saved summary time")
		}
		if _, err := weather.Instant(summary[key]); err != nil {
			return err
		}
	}
	if _, err := weather.WeatherRecord(M{"time": summary["valid_at"], "condition": summary["condition"], "temperature_c": summary["temperature_c"], "is_day": summary["is_day"]}); err != nil {
		return err
	}
	switch summary["alert_status"] {
	case "current", "stale":
		if country != "US" || summary["alert_fetched_at"] == nil {
			return errors.New("inconsistent saved alert source")
		}
	case "not_supported_here":
		if country == nil || country == "US" {
			return errors.New("inconsistent saved alert coverage")
		}
		fallthrough
	case "unavailable":
		if summary["alert_expires"] != nil || summary["alert_fetched_at"] != nil {
			return errors.New("inconsistent unavailable saved alerts")
		}
	default:
		return errors.New("invalid saved alert status")
	}
	for _, key := range []string{"alert_expires", "alert_fetched_at"} {
		if summary[key] != nil {
			if savedTime(summary[key]) == "" || summary[key] != savedTime(summary[key]) {
				return errors.New("invalid saved alert time")
			}
			if _, err := weather.Instant(summary[key]); err != nil {
				return err
			}
		}
	}
	return nil
}

func savedEntry(doc M, id string) M {
	rows, _ := doc["places"].([]any)
	for _, value := range rows {
		entry := object(value)
		if entry["id"] == id {
			return entry
		}
	}
	return nil
}

func validateSavedLocations(doc M) error {
	if len(doc) != 6 || doc["schema_version"] != 1.0 || !validSavedToken(doc["generation"]) {
		return errors.New("invalid saved locations document")
	}
	places, ok := doc["places"].([]any)
	if !ok || len(places) == 0 || len(places) > savedLocationLimit {
		return errors.New("invalid saved location count")
	}
	ids, slots, cached := map[string]bool{}, map[int]bool{}, map[string]bool{}
	for _, value := range places {
		entry := object(value)
		if !savedFields(entry, "id", "label", "profile", "cache_slot", "cache_token", "summary") || savedLabel(entry["label"]) != nil {
			return errors.New("invalid saved location entry")
		}
		profile := object(entry["profile"])
		id, err := savedLocationID(profile)
		if err != nil || entry["id"] != id || ids[id] || profile["forecast"] != nil {
			return errors.New("invalid or duplicate saved identity")
		}
		ids[id] = true
		if entry["summary"] != nil {
			if object(entry["summary"]) == nil || validateSavedSummary(object(entry["summary"]), profile["country_code"]) != nil {
				return errors.New("invalid saved summary")
			}
		}
		if entry["cache_slot"] == nil {
			if entry["cache_token"] != nil {
				return errors.New("unpaired cache reference")
			}
			continue
		}
		n, ok := entry["cache_slot"].(float64)
		if !ok || math.IsNaN(n) || n < 0 || n >= savedForecastSlots || math.Trunc(n) != n || slots[int(n)] || !validSavedToken(entry["cache_token"]) || entry["summary"] == nil {
			return errors.New("invalid saved cache reference")
		}
		slots[int(n)], cached[id] = true, true
	}
	if !ids[stringOf(doc["primary"])] || !ids[stringOf(doc["viewed"])] {
		return errors.New("missing primary or viewed saved place")
	}
	order, ok := doc["cache_order"].([]any)
	if !ok || len(order) != len(cached) || len(order) > savedForecastLimit {
		return errors.New("invalid saved cache order")
	}
	for _, value := range order {
		id, ok := value.(string)
		if !ok || !cached[id] {
			return errors.New("duplicate or missing saved cache entry")
		}
		delete(cached, id)
	}
	return nil
}

func loadSavedLocations(files savedLocationFiles) (*savedLocations, error) {
	doc, err := files.Read(savedLocationsFile, savedMetadataBytes)
	if err != nil || doc == nil {
		return nil, err
	}
	if err := validateSavedLocations(doc); err != nil {
		return nil, err
	}
	return &savedLocations{files: files, doc: doc}, nil
}

// createSavedLocations preserves all legacy files, providing a rollback path.
// The migration marker is the final manifest rename, after the initial cache
// has been written. A failed first attempt leaves the old profile authoritative.
func createSavedLocations(files savedLocationFiles, legacy M) (*savedLocations, error) {
	if existing, err := loadSavedLocations(files); existing != nil || err != nil {
		return existing, err
	}
	if _, err := validateProfileIdentity(legacy); err != nil {
		return nil, err
	}
	if legacy["forecast"] != nil {
		if err := ValidateProfile(legacy); err != nil {
			return nil, err
		}
	}
	profile := weather.Clone(legacy).(M)
	country, place := profileIdentity(profile, stringOf(profile["mode"]))
	profile["schema_version"], profile["country_code"], profile["place"] = 2.0, country, nil
	if place != nil {
		profile["place"] = place
	}
	store := &savedLocations{files: files, doc: M{"schema_version": 1.0, "generation": nil, "primary": "", "viewed": "", "places": []any{}, "cache_order": []any{}}}
	if err := store.put(profile, true, true); err != nil {
		if errors.Is(err, errSavedLocationsUnconfirmed) {
			return store, err
		}
		return nil, err
	}
	return store, nil
}

func (s *savedLocations) document() M { return weather.Clone(s.doc).(M) }

// unchanged requires the caller to hold the state lock around the check and write.
func (s *savedLocations) unchanged() error {
	current, err := s.files.Read(savedLocationsFile, savedMetadataBytes)
	if err != nil {
		return err
	}
	if current != nil {
		if err := validateSavedLocations(current); err != nil {
			return err
		}
	}
	if current["generation"] != s.doc["generation"] {
		return errSavedLocationsChanged
	}
	return nil
}

// commit publishes metadata only. On a post-rename fsync failure, adopt the
// visible document but explicitly report its unconfirmed durability, matching
// the existing profile migration contract. A stale store cannot overwrite a
// newer document; the caller's state lock is still required around this check.
func (s *savedLocations) commit(candidate M) error {
	if err := s.unchanged(); err != nil {
		return err
	}
	token, err := savedToken()
	if err != nil {
		return err
	}
	candidate["generation"] = token
	if err := validateSavedLocations(candidate); err != nil {
		return err
	}
	if err := s.files.Write(savedLocationsFile, candidate, savedMetadataBytes); err != nil {
		published, readErr := s.files.Read(savedLocationsFile, savedMetadataBytes)
		if readErr == nil && reflect.DeepEqual(published, candidate) {
			s.doc = candidate
			return errors.Join(errSavedLocationsUnconfirmed, err)
		}
		return err
	}
	s.doc = candidate
	return nil
}

func savedTouch(doc M, id string, include bool) {
	order := []any{}
	if include {
		order = append(order, id)
	}
	for _, old := range doc["cache_order"].([]any) {
		if old != id {
			order = append(order, old)
		}
	}
	doc["cache_order"] = order
}

// put records resolved identity and weather together. Merely storing or viewing
// a place never changes primary unless the caller explicitly requests it.
// Nil forecast is permitted for an identity with no cached data.
func (s *savedLocations) put(profile M, view, primary bool) error {
	id, err := savedLocationID(profile)
	if err != nil {
		return err
	}
	// Check before touching a slot: a stale store's spare may now be live.
	if err := s.unchanged(); err != nil {
		return err
	}
	if profile["forecast"] != nil {
		if err := ValidateProfile(profile); err != nil {
			return err
		}
	}
	candidate := s.document()
	entry := savedEntry(candidate, id)
	if entry == nil {
		if len(candidate["places"].([]any)) >= savedLocationLimit {
			return errors.New("saved location limit reached")
		}
		entry = M{"id": id, "label": "", "profile": nil, "cache_slot": nil, "cache_token": nil, "summary": nil}
		candidate["places"] = append(candidate["places"].([]any), entry)
	}
	entry["profile"], entry["summary"] = savedProfileIdentity(profile), nil
	if summary := savedSummary(profile); summary != nil {
		entry["summary"] = summary
	}
	entry["cache_slot"], entry["cache_token"] = nil, nil
	savedTouch(candidate, id, false)
	if view {
		candidate["viewed"] = id
	}
	if primary {
		candidate["primary"] = id
	}
	if profile["forecast"] != nil {
		order := candidate["cache_order"].([]any)
		if len(order) == savedForecastLimit {
			for i := len(order) - 1; i >= 0; i-- {
				victim := order[i].(string)
				if victim != candidate["primary"] && victim != candidate["viewed"] {
					old := savedEntry(candidate, victim)
					old["cache_slot"], old["cache_token"] = nil, nil
					savedTouch(candidate, victim, false)
					break
				}
			}
		}
		used := map[int]bool{}
		for _, value := range s.doc["places"].([]any) {
			if slot, ok := object(value)["cache_slot"].(float64); ok {
				used[int(slot)] = true
			}
		}
		slot := 0
		for used[slot] {
			slot++
		}
		if slot >= savedForecastSlots {
			return errors.New("no free saved forecast slot")
		}
		token, err := savedToken()
		if err != nil {
			return err
		}
		cache := M{"schema_version": 1.0, "token": token, "profile": profile}
		if err := s.files.Write(savedSlotName(slot), cache, weather.MaxBytes); err != nil {
			return err
		}
		entry["cache_slot"], entry["cache_token"] = float64(slot), token
		savedTouch(candidate, id, true)
	}
	return s.commit(candidate)
}

// profile reads only the requested forecast. A missing/corrupt/reused slot
// never changes the location list or returns another place's weather.
func (s *savedLocations) profile(id string) (M, error) {
	entry := savedEntry(s.doc, id)
	if entry == nil {
		return nil, errors.New("saved location not found")
	}
	identity := weather.Clone(entry["profile"]).(M)
	if entry["cache_slot"] == nil {
		return identity, nil
	}
	cache, err := s.files.Read(savedSlotName(int(entry["cache_slot"].(float64))), weather.MaxBytes)
	if err != nil {
		return identity, err
	}
	profile := object(cache["profile"])
	if !savedFields(cache, "schema_version", "token", "profile") || cache["schema_version"] != 1.0 || cache["token"] != entry["cache_token"] || ValidateProfile(profile) != nil || !reflect.DeepEqual(savedProfileIdentity(profile), identity) || !reflect.DeepEqual(savedSummary(profile), entry["summary"]) {
		return identity, errors.New("saved forecast cache mismatch")
	}
	return profile, nil
}

func (s *savedLocations) view(id string) error {
	entry := savedEntry(s.doc, id)
	if entry == nil {
		return errors.New("saved location not found")
	}
	candidate := s.document()
	candidate["viewed"] = id
	if entry["cache_slot"] != nil {
		savedTouch(candidate, id, true)
	}
	return s.commit(candidate)
}

func (s *savedLocations) makePrimary(id string) error {
	if savedEntry(s.doc, id) == nil {
		return errors.New("saved location not found")
	}
	candidate := s.document()
	candidate["primary"] = id
	return s.commit(candidate)
}

func (s *savedLocations) rename(id, label string) error {
	if err := savedLabel(label); err != nil {
		return err
	}
	candidate := s.document()
	entry := savedEntry(candidate, id)
	if entry == nil {
		return errors.New("saved location not found")
	}
	entry["label"] = label
	return s.commit(candidate)
}

func (s *savedLocations) move(id string, index int) error {
	candidate := s.document()
	rows := candidate["places"].([]any)
	if index < 0 || index >= len(rows) || savedEntry(candidate, id) == nil {
		return errors.New("invalid saved location order")
	}
	ordered := []any{}
	var target any
	for _, row := range rows {
		if object(row)["id"] == id {
			target = row
		} else {
			ordered = append(ordered, row)
		}
	}
	ordered = append(ordered, nil)
	copy(ordered[index+1:], ordered[index:])
	ordered[index] = target
	candidate["places"] = ordered
	return s.commit(candidate)
}

// Removing primary requires an explicit replacement in the same commit.
// Removing the viewed place returns to primary; other deletions preserve view.
func (s *savedLocations) remove(id, replacement string) error {
	candidate := s.document()
	rows := candidate["places"].([]any)
	if len(rows) <= 1 || savedEntry(candidate, id) == nil {
		return errors.New("cannot remove saved location")
	}
	if candidate["primary"] == id {
		if replacement == id || savedEntry(candidate, replacement) == nil {
			return errors.New("choose a replacement primary location")
		}
		candidate["primary"] = replacement
	} else if replacement != "" {
		return errors.New("unexpected replacement primary location")
	}
	if candidate["viewed"] == id {
		candidate["viewed"] = candidate["primary"]
	}
	remaining := []any{}
	for _, row := range rows {
		if object(row)["id"] != id {
			remaining = append(remaining, row)
		}
	}
	candidate["places"] = remaining
	savedTouch(candidate, id, false)
	return s.commit(candidate)
}
