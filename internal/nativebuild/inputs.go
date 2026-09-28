package nativebuild

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func probe(host string, accepted bool, args ...string) error {
	out, err := run(5*time.Second, 65536, host, args...)
	for _, marker := range []string{"AddressSanitizer", "LeakSanitizer", "UndefinedBehaviorSanitizer", "runtime error:"} {
		if bytes.Contains(out, []byte(marker)) {
			return fmt.Errorf("native sanitizer diagnostic: %.2048s", out)
		}
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signaled() {
			return fmt.Errorf("native probe failed: %w", err)
		}
	}
	if (err == nil) != accepted {
		return fmt.Errorf("unexpected native input acceptance for %s: %.1024s", args[0], out)
	}
	return nil
}
func encode(v any) ([]byte, error) { return json.Marshal(v) }
func clone(v map[string]any) map[string]any {
	raw, _ := json.Marshal(v)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return out
}
func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000Z") }

// Inputs exercises the exact allocation boundary and adversarial weather/policy files.
// It invokes only the host's display-free validation modes, never compositor effects.
func Inputs(host string) error {
	if err := executable(host); err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "weather-native-inputs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "weather.json")
	envelope := map[string]any{"schema_version": 1, "selected_at": iso(time.Now()), "effects": map[string]any{"sun_elevation": 35, "sun_azimuth": 180, "cloud_cover": 0.5, "fog_density": 0, "wind_x": 35, "lightning_enabled": false, "reduced_motion": false, "thunderstorm": false}}
	raw, _ := encode(envelope)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	check := func(target string, accepted bool) error { return probe(host, accepted, "--validate-weather", target) }
	if err = check(path, true); err != nil {
		return err
	}
	symlink := filepath.Join(directory, "symlink")
	if err = os.Symlink(path, symlink); err != nil {
		return err
	}
	if err = check(symlink, false); err != nil {
		return err
	}
	hardlink := filepath.Join(directory, "hardlink")
	if err = os.Link(path, hardlink); err != nil {
		return err
	}
	if err = check(path, false); err != nil {
		return err
	}
	if err = os.Remove(hardlink); err != nil {
		return err
	}
	if err = os.Chmod(path, 0666); err != nil {
		return err
	}
	if err = check(path, false); err != nil {
		return err
	}
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	fifo := filepath.Join(directory, "fifo")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		return err
	}
	if err = check(fifo, false); err != nil {
		return err
	}
	if err = check(path, true); err != nil {
		return err
	}
	weatherLimit := 2 * 1024 * 1024
	for _, size := range []int{weatherLimit, weatherLimit + 1} {
		payload := append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), size-len(raw))...)
		if err = os.WriteFile(path, payload, 0600); err != nil {
			return err
		}
		if err = check(path, size == weatherLimit); err != nil {
			return err
		}
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	if err = check(path, true); err != nil {
		return err
	}
	invalid := [][]byte{[]byte(""), []byte("[]"), []byte("{broken"), []byte("{\"x\":\"nul\x00byte\"}"), append([]byte("/* extension comment */"), raw...), append([]byte("{\"schema_version\":1,"), raw[1:]...), append([]byte("{\"schema_\\u0076ersion\":1,"), raw[1:]...), bytes.Replace(raw, []byte(`"cloud_cover":0.5`), []byte(`"cloud_cover":0.5,"cloud_\u0063over":0.5`), 1), []byte(`{"x":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + "}")}
	for _, patch := range []map[string]any{{"wind_x": 501}, {"wind_x": true}, {"lightning_enabled": 1}} {
		v := clone(envelope)
		for k, x := range patch {
			v["effects"].(map[string]any)[k] = x
		}
		data, _ := encode(v)
		invalid = append(invalid, data)
	}
	invalid = append(invalid, bytes.Replace(raw, []byte(`"cloud_cover":0.5`), []byte(`"cloud_cover":NaN`), 1))
	for _, patch := range []map[string]any{{"schema_version": true}, {"effects": []any{}}, {"selected_at": "2026-09-27T12:00:00"}, {"selected_at": iso(time.Now().Add(-time.Minute))}, {"selected_at": iso(time.Now().Add(10 * time.Minute))}} {
		v := clone(envelope)
		for k, x := range patch {
			v[k] = x
		}
		data, _ := encode(v)
		invalid = append(invalid, data)
	}
	for _, data := range invalid {
		if err = os.WriteFile(path, data, 0600); err != nil {
			return err
		}
		if err = check(path, false); err != nil {
			return err
		}
	}
	v := clone(envelope)
	v["unrelated"] = map[string]any{"schema_version": 1}
	raw, _ = encode(v)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	if err = check(path, true); err != nil {
		return err
	}
	envelope["selected_at"] = iso(time.Now())
	raw, _ = encode(envelope)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		return err
	}
	if err = check(path, true); err != nil {
		return err
	}
	policyPath := filepath.Join(directory, "policy.json")
	policy := map[string]any{"schema_version": 1, "session": "abcdef_123_456", "output": "eDP-1", "sequence": 1, "stale_after_ms": 1500, "render_allowed": true, "reason": "none"}
	checkPolicy := func(payload any, accepted bool) error {
		var data []byte
		switch value := payload.(type) {
		case []byte:
			data = value
		case map[string]any:
			v := clone(value)
			if _, ok := v["generated_at_unix_ms"]; !ok {
				v["generated_at_unix_ms"] = time.Now().UnixMilli()
			}
			data, _ = encode(v)
		default:
			return errors.New("invalid native policy test fixture")
		}
		if e := os.WriteFile(policyPath, data, 0600); e != nil {
			return e
		}
		return probe(host, accepted, "--validate-policy", policyPath, "--policy-session", "abcdef_123_456", "--output", "eDP-1")
	}
	if err = checkPolicy(policy, true); err != nil {
		return err
	}
	disabled := clone(policy)
	disabled["render_allowed"] = false
	disabled["reason"] = "fullscreen"
	if err = checkPolicy(disabled, true); err != nil {
		return err
	}
	v = clone(policy)
	v["generated_at_unix_ms"] = time.Now().UnixMilli()
	raw, _ = encode(v)
	for _, data := range [][]byte{append([]byte(`{"schema_version":1,`), raw[1:]...), append([]byte(`{"schema_\u0076ersion":1,`), raw[1:]...), append([]byte("/* extension comment */"), raw...)} {
		if err = checkPolicy(data, false); err != nil {
			return err
		}
	}
	for _, patch := range []map[string]any{{"schema_version": true}, {"session": "foreign_123_456"}, {"output": "HDMI-A-1"}, {"sequence": -1}, {"stale_after_ms": 99999}, {"render_allowed": 1}, {"render_allowed": true, "reason": "fullscreen"}, {"render_allowed": false, "reason": "none"}, {"generated_at_unix_ms": time.Now().Add(-10 * time.Second).UnixMilli()}, {"generated_at_unix_ms": time.Now().Add(10 * time.Second).UnixMilli()}} {
		v = clone(policy)
		for k, x := range patch {
			v[k] = x
		}
		if err = checkPolicy(v, false); err != nil {
			return err
		}
	}
	return nil
}
