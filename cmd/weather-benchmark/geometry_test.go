package main

import (
	"encoding/json"
	"os"
	"testing"
)

func suitableBenchmarkMonitor() map[string]any {
	return map[string]any{"id": 0, "name": "eDP-1", "disabled": false, "dpmsStatus": true, "mirrorOf": "none", "transform": 0, "x": 0, "y": 0, "width": 1920, "height": 1200, "scale": 1.25, "reserved": []int{0, 26, 0, 0}}
}

func TestBenchmarkGeometryFitsScaledUsableOutput(t *testing.T) {
	raw, err := json.Marshal([]any{suitableBenchmarkMonitor()})
	if err != nil {
		t.Fatal(err)
	}
	output, err := decodeBenchmarkOutput(raw)
	if err != nil || output.Scale != 1.25 || output.Width != 1920 || output.Height != 1200 || output.Reserved != [4]int{0, 26, 0, 0} {
		t.Fatal(output, err)
	}
	// The previous y=178 extent was 1088 logical pixels on a 960px output.
	if 178+benchmarkHeight <= int(float64(output.Height)/output.Scale) {
		t.Fatal("regression fixture does not exercise bottom-edge clipping")
	}
}

func TestBenchmarkGeometryRefusesUnsuitableOrUnknownOutput(t *testing.T) {
	cases := map[string]func(map[string]any){
		"disabled":                      func(m map[string]any) { m["disabled"] = true },
		"power off":                     func(m map[string]any) { m["dpmsStatus"] = false },
		"unknown power":                 func(m map[string]any) { delete(m, "dpmsStatus") },
		"mirrored":                      func(m map[string]any) { m["mirrorOf"] = "DP-1" },
		"rotated":                       func(m map[string]any) { m["transform"] = 1 },
		"missing transform":             func(m map[string]any) { delete(m, "transform") },
		"negative origin":               func(m map[string]any) { m["x"] = -1536 },
		"shifted origin":                func(m map[string]any) { m["y"] = 50 },
		"scale zero":                    func(m map[string]any) { m["scale"] = 0 },
		"scale wrong type":              func(m map[string]any) { m["scale"] = "1.25" },
		"fractional logical dimensions": func(m map[string]any) { m["scale"] = 1.3 },
		"too narrow":                    func(m map[string]any) { m["width"] = 800 },
		"too short":                     func(m map[string]any) { m["height"] = 1080 },
		"fractional pixels":             func(m map[string]any) { m["height"] = 1200.5 },
		"left reserve overlaps":         func(m map[string]any) { m["reserved"] = []int{16, 26, 0, 0} },
		"top reserve overlaps":          func(m map[string]any) { m["reserved"] = []int{0, 41, 0, 0} },
		"right reserve overlaps":        func(m map[string]any) { m["reserved"] = []int{0, 26, 773, 0} },
		"bottom reserve overlaps":       func(m map[string]any) { m["reserved"] = []int{0, 26, 0, 11} },
		"missing reserved":              func(m map[string]any) { delete(m, "reserved") },
		"short reserved":                func(m map[string]any) { m["reserved"] = []int{0, 26} },
		"negative reserved":             func(m map[string]any) { m["reserved"] = []int{0, -1, 0, 0} },
		"missing ID":                    func(m map[string]any) { delete(m, "id") },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			m := suitableBenchmarkMonitor()
			change(m)
			raw, err := json.Marshal([]any{m})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = decodeBenchmarkOutput(raw); err == nil {
				t.Fatal("accepted unsuitable output", string(raw))
			}
		})
	}
	for _, rows := range [][]any{nil, {}, {suitableBenchmarkMonitor(), suitableBenchmarkMonitor()}} {
		raw, _ := json.Marshal(rows)
		if _, err := decodeBenchmarkOutput(raw); err == nil {
			t.Fatal("accepted missing or multiple outputs")
		}
	}
}

func TestWindowStarttimeObservationAndUncapturedRefusal(t *testing.T) {
	first, err := windowStarttime(os.Getpid())
	if err != nil || first == "" {
		t.Fatal(first, err)
	}
	second, err := windowStarttime(os.Getpid())
	if err != nil || second != first {
		t.Fatal("read-only process identity drifted", first, second, err)
	}
	// Fail before invoking a compositor command when the PID was not captured.
	if err = checkWindowVisibility([]string{"/never-execute"}, os.Getpid(), false); err == nil {
		t.Fatal("accepted an uncaptured window PID")
	}
	windowGeometryBindings[os.Getpid()] = windowGeometryBinding{starttime: "different"}
	defer delete(windowGeometryBindings, os.Getpid())
	if err = checkWindowVisibility([]string{"/never-execute"}, os.Getpid(), true); err == nil {
		t.Fatal("accepted a replaced process identity")
	}
}
