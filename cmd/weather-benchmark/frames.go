package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const frameLimit = 10000
const frameLogLimit = 256 * 1024
const frameMarker = "Weather frame callbacks JSON: "

type boundedFrameWriter struct {
	sync.Mutex
	destination io.Writer
	written     int
	capped      bool
}

func (w *boundedFrameWriter) Write(b []byte) (int, error) {
	w.Lock()
	defer w.Unlock()
	n := len(b)
	remaining := frameLogLimit - w.written
	if len(b) > remaining {
		w.capped = true
		b = b[:remaining]
	}
	count, err := w.destination.Write(b)
	w.written += count
	return n, err
}
func frameCommand(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var buffer bytes.Buffer
	writer := &boundedFrameWriter{destination: &buffer}
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdout = writer
	command.Stderr = io.Discard
	command.WaitDelay = 500 * time.Millisecond
	if err := command.Run(); err != nil {
		return nil, err
	}
	if writer.capped {
		return nil, errors.New("frame IPC output exceeded bound")
	}
	return buffer.Bytes(), nil
}

type frameReport struct {
	Callbacks  []float64 `json:"callbacks"`
	Capped     bool      `json:"capped"`
	Resolution int       `json:"resolution_ms"`
}

func parseFrames(raw []byte, log bool) (frameReport, error) {
	var report frameReport
	if len(raw) > frameLogLimit {
		return report, errors.New("frame log exceeds bound")
	}
	if log {
		index := bytes.LastIndex(raw, []byte(frameMarker))
		if index < 0 {
			return report, errors.New("frame report missing")
		}
		raw = bytes.SplitN(raw[index+len(frameMarker):], []byte{'\n'}, 2)[0]
	}
	if err := json.Unmarshal(bytes.TrimSpace(raw), &report); err != nil {
		return report, err
	}
	if len(report.Callbacks) > frameLimit || report.Resolution != 1 {
		return report, errors.New("invalid frame report bounds")
	}
	var previous float64
	for _, v := range report.Callbacks {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v != math.Trunc(v) || v < previous {
			return report, errors.New("callback clock reversed or invalid")
		}
		previous = v
	}
	return report, nil
}

func frameStatistics(report frameReport, start, end int64) (map[string]any, error) {
	if end <= start || report.Capped {
		return nil, errors.New("invalid or capped frame measurement")
	}
	intervals := []float64{}
	var previous float64
	callbacks := 0
	for _, v := range report.Callbacks {
		if v < float64(start) || v > float64(end) {
			continue
		}
		callbacks++
		if previous > 0 {
			intervals = append(intervals, v-previous)
		}
		previous = v
	}
	if len(intervals) < 2 {
		return nil, errors.New("too few visible callbacks for p99")
	}
	sort.Float64s(intervals)
	index := (len(intervals)*99+99)/100 - 1
	return map[string]any{"callbacks": callbacks, "intervals": len(intervals), "p99_ms": intervals[index], "max_ms": intervals[len(intervals)-1], "resolution_ms": 1, "start_epoch_ms": start, "end_epoch_ms": end, "meaning": "queued frameSwapped callback intervals; not GPU time or hardware presentation latency"}, nil
}

const baselineConnections = `
    // Measurement-only observer in an isolated development copy.
    property var benchmarkCallbacks: []
    property bool benchmarkFramesCapped: false
    Connections {
        target: window
        function onFrameSwapped() {
            if(root.benchmarkCallbacks.length<10000)root.benchmarkCallbacks.push(Date.now());
            else root.benchmarkFramesCapped=true;
        }
    }
`
const baselineReport = `
        function frameReport():string { return JSON.stringify({callbacks:root.benchmarkCallbacks,capped:root.benchmarkFramesCapped,resolution_ms:1}) }
        function benchmarkCheckEffects():bool { return bridge.send("check_effects") }
        function benchmarkStartEffects():bool { return bridge.send("start_effects") }
        function benchmarkStopEffects():bool { return bridge.send("stop_effects") }
        function benchmarkEffectsSnapshot():string { let status=bridge.snapshot?bridge.snapshot.effect_status:null;if(typeof status==="string")status={state:status,persistent:root.liveDesktop};return JSON.stringify({setup:bridge.snapshot?bridge.snapshot.effects_setup:{},status:status||{state:"unavailable"}}) }
`

func instrumentShell(raw []byte) ([]byte, error) {
	source := string(raw)
	if strings.Count(source, "ShellRoot {") != 1 || strings.Count(source, "id:root") != 1 || strings.Count(source, `target:"a-weather-app-ui"`) != 1 || strings.Contains(source, "benchmarkCallbacks") {
		return nil, errors.New("unsupported baseline QML instrumentation shape")
	}
	source = strings.Replace(source, "id:root", "id:root"+baselineConnections, 1)
	source = strings.Replace(source, `target:"a-weather-app-ui"`, `target:"a-weather-app-ui"`+baselineReport, 1)
	return []byte(source), nil
}

// Copy only bounded regular files into a fresh private directory. Never follows
// source links or writes to the source checkout; Python remains reference-only.
func baselineCopy(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("baseline source must be a real directory")
	}
	relative, err := filepath.Rel(source, destination)
	if err != nil {
		return err
	}
	if relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("baseline instrumentation destination must be outside source")
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	files := 0
	var total int64
	err = filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") || entry.Name() == "__pycache__" || entry.Name() == "build" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nonregular baseline source: %s", relative)
		}
		files++
		total += info.Size()
		if files > 5000 || info.Size() > 32*1024*1024 || total > 256*1024*1024 {
			return errors.New("baseline copy exceeds bound")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		mode := os.FileMode(0600)
		if info.Mode()&0111 != 0 {
			mode = 0700
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = io.CopyN(output, input, info.Size())
		closeErr := output.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	path := filepath.Join(destination, "ui/qml/shell.qml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	patched, err := instrumentShell(raw)
	if err != nil {
		return err
	}
	return os.WriteFile(path, patched, 0600)
}
