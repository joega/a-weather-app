package effects

import (
	"context"
	"testing"
)

func TestDefaultOutputFollowsWindowHintAndExplicitOverride(t *testing.T) {
	m := New("", "", "", "")
	outputs := []string{"eDP-1", "DP-1"}
	if got := m.preferredOutput(outputs, "DP-1"); got != "DP-1" {
		t.Fatal("app connector was not preferred", got)
	}
	if got := m.preferredOutput(outputs, "eDP-1"); got != "eDP-1" {
		t.Fatal("moving the app did not change the default", got)
	}
	if got := m.preferredOutput(outputs, ""); got != "" {
		t.Fatal("ambiguous monitors were guessed", got)
	}
	if got := m.preferredOutput([]string{"DP-1"}, ""); got != "DP-1" {
		t.Fatal("single-monitor fallback missing", got)
	}
	m.setup["outputs"] = []any{object{"name": "DP-1", "enabled": true}, object{"name": "eDP-1", "enabled": true}, object{"name": "DP-2", "enabled": false}}
	if _, err := m.SelectOutput(context.Background(), "DP-1"); err != nil {
		t.Fatal(err)
	}
	if got := m.preferredOutput(outputs, "eDP-1"); got != "DP-1" || m.SetupSnapshot()["output_selection"] != "explicit" {
		t.Fatal("window hint replaced a manual override", got)
	}
	if _, err := m.SelectOutput(context.Background(), "DP-2"); err == nil || m.output != "DP-1" {
		t.Fatal("disabled connector changed the selection", err)
	}
	if _, err := m.SelectOutput(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got := m.preferredOutput(outputs, "eDP-1"); got != "eDP-1" || m.SetupSnapshot()["output_selection"] != "automatic" {
		t.Fatal("return to Automatic failed", got)
	}
	cli := New("", "", "", "DP-1")
	if got := cli.preferredOutput(outputs, "eDP-1"); got != "DP-1" {
		t.Fatal("explicit command-line connector replaced", got)
	}
}

func TestUnknownWindowOutputDoesNotSelectAnotherMonitor(t *testing.T) {
	m := New("", "", "", "")
	selected := m.preferredOutput([]string{"DP-1", "eDP-1"}, "DP-9")
	if selected != "DP-9" {
		t.Fatal("unknown hint silently selected another output", selected)
	}
	if _, err := selectedOutput([]any{object{"name": "DP-1", "disabled": false}}, selected); err == nil {
		t.Fatal("unknown output passed validation")
	}
}

func TestFailedAutomaticCheckClearsPreviousConnector(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	m := New("", "", "", "")
	m.output = "DP-1"
	m.setup["status"] = "ready"
	m.setup["selected_output"] = "DP-1"
	setup := m.CheckOutput(context.Background(), "eDP-1")
	if setup["status"] != "unavailable" || setup["selected_output"] != nil || m.output != "" {
		t.Fatal("failed automatic check retained the previous connector", setup, m.output)
	}
	if err := m.Start(context.Background(), 1, false, nil); err == nil {
		t.Fatal("a failed check admitted a start on the previous monitor")
	}
}
