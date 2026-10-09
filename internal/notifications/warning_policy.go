package notifications

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
)

const warningSettingsFile = "warning-notifications.json"
const warningSettingsLimit = 4096

// WarningDefaults keeps official warning delivery separate from precipitation
// outlooks. Urgent interruptions require an explicit additional opt-in.
func WarningDefaults() M {
	return M{"enabled": false, "minimum_severity": "severe", "quiet_enabled": true, "quiet_start": 22.0, "quiet_end": 7.0, "urgent_override": false}
}

// PatchWarnings returns independent, validated warning policy settings.
func PatchWarnings(old, patch M) (M, error) {
	if len(patch) == 0 {
		return nil, errors.New("empty warning settings")
	}
	v := safeio.Clone(old)
	for k, value := range patch {
		if _, known := v[k]; !known {
			return nil, errors.New("unknown warning setting")
		}
		v[k] = value
	}
	for _, k := range []string{"enabled", "quiet_enabled", "urgent_override"} {
		if _, ok := v[k].(bool); !ok {
			return nil, errors.New("warning boolean")
		}
	}
	for _, k := range []string{"quiet_start", "quiet_end"} {
		n, ok := v[k].(float64)
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < 0 || n > 23 {
			return nil, errors.New("warning quiet hour")
		}
	}
	if v["quiet_start"] == v["quiet_end"] {
		return nil, errors.New("warning quiet range")
	}
	if v["minimum_severity"] != "severe" && v["minimum_severity"] != "moderate" && v["minimum_severity"] != "all" {
		return nil, errors.New("warning severity")
	}
	return v, nil
}

func defaultWarningSettings() M {
	return M{"schema_version": 1.0, "settings": WarningDefaults(), "paused_until": nil}
}

func validateWarningSettings(v M) error {
	if len(v) != 3 || v["schema_version"] != 1.0 {
		return errors.New("warning settings schema")
	}
	settings, ok := v["settings"].(M)
	if !ok || len(settings) != len(WarningDefaults()) {
		return errors.New("warning settings")
	}
	if _, err := PatchWarnings(WarningDefaults(), settings); err != nil {
		return err
	}
	until, present := v["paused_until"]
	if !present || until != nil && !timestamp(until) {
		return errors.New("warning pause")
	}
	return nil
}

func warningSeverity(severity string) int {
	switch severity {
	case "Extreme":
		return 4
	case "Severe":
		return 3
	case "Moderate":
		return 2
	case "Minor":
		return 1
	default:
		return 0
	}
}

func warningUrgent(m weather.AlertMessage) bool {
	return m.Type != "Cancel" && warningSeverity(m.Severity) >= 3 && m.Urgency == "Immediate" && (m.Certainty == "Observed" || m.Certainty == "Likely")
}

func warningAllowed(settings M, decision WarningDecision, message weather.AlertMessage, now time.Time, zone *time.Location) bool {
	// Revisions/cancellations of an already attempted warning remain useful
	// even when its severity falls below the threshold used for new warnings.
	if decision.Kind == "new" {
		threshold := 0
		switch settings["minimum_severity"] {
		case "severe":
			threshold = 3
		case "moderate":
			threshold = 2
		}
		if warningSeverity(message.Severity) < threshold {
			return false
		}
	}
	return !quietAt(settings, now, zone) || settings["urgent_override"] == true && warningUrgent(message)
}

// WarningNotice is a bounded presentation request. Key/Location are opaque
// hashes for routing to original details, not provider URLs or executable text.
// A successful sender result means acceptance, never proof the user read it.
type WarningNotice struct {
	Key, Location, Kind, Place string
	Title, Body, Urgency       string
}

func warningNotice(decision WarningDecision, m weather.AlertMessage, place string, zone *time.Location, settings M) WarningNotice {
	event := m.Event
	if strings.TrimSpace(event) == "" {
		event = "Official weather warning"
	}
	prefix := ""
	if decision.Kind == "updated" {
		prefix = "Updated: "
	} else if decision.Kind == "canceled" {
		prefix = "Canceled: "
	}
	issuer := m.Issuer
	if strings.TrimSpace(issuer) == "" {
		issuer = "National Weather Service"
	}
	parts := []string{Text(place, 100), Text(issuer, 100)}
	if decision.Kind != "canceled" {
		parts = append(parts, m.Severity+" · valid until "+m.Expires.In(zone).Format("Mon Jan 2, 3:04 PM MST"))
	}
	if instruction := Text(strings.Join(strings.Fields(m.Instruction), " "), 500); instruction != "" {
		parts = append(parts, instruction)
	} else if headline := Text(m.Headline, 200); headline != "" {
		parts = append(parts, headline)
	}
	urgency := "normal"
	if settings["urgent_override"] == true && warningUrgent(m) {
		urgency = "critical"
	}
	return WarningNotice{Key: decision.Key, Location: decision.Location, Kind: decision.Kind, Place: Text(place, 100), Title: Text(prefix+event, 120), Body: strings.Join(parts, "\n"), Urgency: urgency}
}
