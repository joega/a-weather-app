package main

import "testing"

func TestDecodeEffectSnapshotAndStatus(t *testing.T) {
	valid, err := decodeEffectSnapshot([]byte(`{"setup":{"status":"ready"},"status":{"state":"running"}}`))
	if err != nil || effectsState(valid) != "running" {
		t.Fatal("rejected valid status", valid, err)
	}
	for _, raw := range [][]byte{
		[]byte(`{"setup":{},"status":[]}`),
		make([]byte, 8193),
	} {
		if _, err := decodeEffectSnapshot(raw); err == nil {
			t.Fatal("accepted invalid effects snapshot")
		}
	}
	if got := effectsState(map[string]any{"status": map[string]any{"state": "running"}}); got != "running" {
		t.Fatalf("state=%q", got)
	}
	if got := effectsState(map[string]any{"effect_status": map[string]any{"state": "stopped"}}); got != "stopped" {
		t.Fatalf("state=%q", got)
	}
}
