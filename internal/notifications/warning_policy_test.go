package notifications

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestWarningPolicyValidationAndSeparation(t *testing.T) {
	defaults := WarningDefaults()
	for _, patch := range []M{nil, {}, {"enabled": 1.0}, {"minimum_severity": "Extreme"}, {"probability": 50.0}, {"quiet_start": math.NaN()}, {"quiet_end": math.Inf(1)}, {"quiet_start": -1.0}, {"quiet_start": 23.5}, {"quiet_end": 24.0}, {"quiet_start": 7.0}, {"urgent_override": "true"}} {
		if _, err := PatchWarnings(defaults, patch); err == nil {
			t.Errorf("accepted bad policy: %#v", patch)
		}
	}
	v, err := PatchWarnings(defaults, M{"enabled": true, "minimum_severity": "all"})
	if err != nil || v["enabled"] != true || defaults["enabled"] != false || !reflect.DeepEqual(defaults, WarningDefaults()) {
		t.Fatal("policy patch mutated defaults", err)
	}
	for _, mutate := range []func(M){
		func(v M) { v["schema_version"] = 2.0 },
		func(v M) { v["extra"] = true },
		func(v M) { delete(v["settings"].(M), "enabled") },
		func(v M) { delete(v, "paused_until") },
		func(v M) { v["paused_until"] = -1.0 },
	} {
		doc := defaultWarningSettings()
		mutate(doc)
		if validateWarningSettings(doc) == nil {
			t.Fatal("accepted bad policy document", doc)
		}
	}
}

func TestWarningSeverityQuietAndUrgentPolicy(t *testing.T) {
	zone, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	quiet := time.Date(2026, 10, 8, 23, 0, 0, 0, zone)
	m := warningFixture("policy", "Alert", quiet)
	d := WarningDecision{warningTestLocation, m.Identity.Key(), "new"}
	settings := WarningDefaults()
	if warningAllowed(settings, d, m, quiet, zone) {
		t.Fatal("default warning bypassed quiet hours")
	}
	settings["urgent_override"] = true
	if !warningAllowed(settings, d, m, quiet, zone) {
		t.Fatal("explicit urgent override did not apply")
	}
	for _, certainty := range []string{"Possible", "Unlikely", "Unknown"} {
		m.Certainty = certainty
		if warningAllowed(settings, d, m, quiet, zone) {
			t.Fatal("uncertain source bypassed quiet hours", certainty)
		}
	}
	m.Certainty, m.Urgency = "Observed", "Expected"
	if warningAllowed(settings, d, m, quiet, zone) {
		t.Fatal("non-immediate warning bypassed quiet hours")
	}
	settings["quiet_enabled"] = false
	for _, severity := range []string{"Minor", "Moderate", "Unknown"} {
		m.Severity = severity
		if warningAllowed(settings, d, m, quiet, zone) {
			t.Fatal("new low-severity message passed default threshold")
		}
		d.Kind = "updated"
		if !warningAllowed(settings, d, m, quiet, zone) {
			t.Fatal("downgraded prior warning was hidden")
		}
		d.Kind = "new"
	}
	settings["minimum_severity"] = "all"
	if !warningAllowed(settings, d, m, quiet, zone) {
		t.Fatal("all-source-severities policy excluded Unknown")
	}
	settings["quiet_enabled"] = true
	m = warningCancel("cancel", quiet, m)
	d.Kind = "canceled"
	if warningAllowed(settings, d, m, quiet, zone) {
		t.Fatal("cancellation bypassed quiet hours")
	}
}

func TestWarningNoticeRetainsOriginalSeparatelyAndBoundsText(t *testing.T) {
	m := warningFixture("notice", "Alert", warningTestNow)
	m.Event = "<b>Flood</b>\u202e Warning"
	m.Instruction = strings.Repeat("Leave <now>\n", 1000)
	original := m.Instruction
	n := warningNotice(WarningDecision{warningTestLocation, m.Identity.Key(), "new"}, m, "<Town>\u2066", time.UTC, WarningDefaults())
	if strings.ContainsAny(n.Title+n.Body+n.Place, "<>&\u202e\u2066") || len([]rune(n.Title)) > 120 || len([]rune(n.Body)) > 800 || n.Urgency != "normal" || m.Instruction != original {
		t.Fatal("notice bounds/sanitization changed original or failed", n)
	}
	if !strings.Contains(n.Body, "Severe") || !strings.Contains(n.Body, "NWS Fixture Office") || !strings.Contains(n.Body, "valid until") {
		t.Fatal("notice omitted source/severity/validity", n)
	}
	settings := WarningDefaults()
	settings["urgent_override"] = true
	n = warningNotice(WarningDecision{warningTestLocation, m.Identity.Key(), "updated"}, m, "Town", time.UTC, settings)
	if n.Urgency != "critical" || !strings.HasPrefix(n.Title, "Updated:") {
		t.Fatal("urgent revision presentation", n)
	}
}

func TestWarningNoticeEmptySanitizedLabelsUseReadableFallbacks(t *testing.T) {
	m := warningFixture("empty sanitized label", "Alert", warningTestNow)
	m.Event, m.Issuer = "<>&\u2066", "\x00\u2066"
	n := warningNotice(WarningDecision{warningTestLocation, m.Identity.Key(), "new"}, m, "Town", time.UTC, WarningDefaults())
	if n.Title != "Official weather warning" || !strings.Contains(n.Body, "National Weather Service") || n.Timezone != "UTC" {
		t.Fatal("unreadable source labels crossed native boundary", n)
	}
}
