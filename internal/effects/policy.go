package effects

// policyDecision mirrors the native host's fail-closed presentation contract.
func policyDecision(monitors, clients any, output string, lock object) string {
	metadata := policyMetadataDecision(monitors, clients, output)
	if metadata != "none" {
		return metadata
	}
	return lockDecision(lock)
}

// policyMetadataDecision evaluates compositor observations independently from
// lock state; both observations are refreshed on every active heartbeat.
func policyMetadataDecision(monitors, clients any, output string) string {
	ms, mok := monitors.([]any)
	cs, cok := clients.([]any)
	if !mok || !cok || len(ms) > 64 || len(cs) > 1024 {
		return "invalid_metadata"
	}
	var selected []object
	for _, v := range ms {
		m, ok := v.(map[string]any)
		if !ok {
			return "invalid_metadata"
		}
		if m["name"] == output {
			selected = append(selected, m)
		}
	}
	for _, v := range cs {
		if obj(v) == nil {
			return "invalid_metadata"
		}
	}
	if len(selected) == 0 {
		return "output_missing"
	}
	if len(selected) > 1 {
		return "output_ambiguous"
	}
	m := selected[0]
	id, ok := integer(m["id"])
	if !ok {
		return "invalid_metadata"
	}
	if m["disabled"] == true {
		return "output_disabled"
	}
	power, ok := m["dpmsStatus"].(bool)
	if m["disabled"] != false || !ok {
		return "power_unknown"
	}
	if !power {
		return "dpms_off"
	}
	ws, wok := signedInteger(obj(m["activeWorkspace"])["id"])
	if !wok {
		return "invalid_metadata"
	}
	special, sok := signedInteger(obj(m["specialWorkspace"])["id"])
	for _, v := range cs {
		c := obj(v)
		monitor, valid := signedInteger(c["monitor"])
		if !valid {
			return "invalid_metadata"
		}
		if monitor != id {
			continue
		}
		for _, k := range []string{"mapped", "hidden", "pinned"} {
			if _, ok := c[k].(bool); !ok {
				return "invalid_metadata"
			}
		}
		if c["mapped"] != true || c["hidden"] == true {
			continue
		}
		cws, valid := signedInteger(obj(c["workspace"])["id"])
		full, fok := integer(c["fullscreen"])
		if !valid || !fok || full > 2 {
			return "invalid_metadata"
		}
		if (c["pinned"] == true || cws == ws || (sok && special != 0 && cws == special)) && full == 2 {
			return "fullscreen"
		}
	}
	return "none"
}

func lockDecision(lock object) string {
	if lock["enabled"] == true {
		_, knownPresent := lock["lock_state_known"]
		_, lockedPresent := lock["session_locked"]
		if knownPresent || lockedPresent {
			locked, ok := lock["session_locked"].(bool)
			if lock["lock_state_known"] != true || !ok || lock["cleanup_failed"] == true {
				return "lock_unknown"
			}
			if locked {
				return "session_lock"
			}
			return "none"
		}
		if lock["suppression"] == "session_lock" {
			return "session_lock"
		}
		if lock["suppression"] == "none" && lock["cleanup_failed"] != true {
			return "none"
		}
	}
	return "lock_unknown"
}

func applyLockDecision(metadata string, lock object) string {
	if metadata != "none" {
		return metadata
	}
	return lockDecision(lock)
}
func signedInteger(v any) (int64, bool) {
	n, ok := number(v)
	return int64(n), ok && n == float64(int64(n)) && n >= -2147483648 && n <= 2147483647
}
func policyEnvelope(instance, output string, sequence int64, reason string, now int64) object {
	return object{"schema_version": 1, "session": instance, "output": output, "sequence": sequence, "generated_at_unix_ms": now, "stale_after_ms": 1500, "render_allowed": reason == "none", "reason": reason}
}
