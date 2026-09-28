package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFrameParserAndVisibleWindow(t *testing.T) {
	report, err := parseFrames([]byte(frameMarker+`{"callbacks":[900,1000,1016,1033,1049,1200],"capped":false,"resolution_ms":1}`+"\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := frameStatistics(report, 1000, 1050)
	if err != nil || stats["p99_ms"] != float64(17) || stats["intervals"] != 3 {
		t.Fatal(stats, err)
	}
	for _, raw := range []string{`{"callbacks":[2,1],"resolution_ms":1}`, `{"callbacks":[1.5],"resolution_ms":1}`, `{"callbacks":[1],"resolution_ms":0}`, `{}`} {
		if _, err := parseFrames([]byte(raw), false); err == nil {
			t.Fatal("accepted malformed report", raw)
		}
	}
	report.Capped = true
	if _, err := frameStatistics(report, 1000, 1050); err == nil {
		t.Fatal("accepted capped measurement")
	}
	if _, err := parseFrames(make([]byte, frameLogLimit+1), true); err == nil {
		t.Fatal("accepted oversized log")
	}
}

// Opt-in local-socket/offscreen integration: validates Quickshell exposes the
// actual window signal and that IPC returns parseable bounded callback data.
func TestQuickshellFrameObserver(t *testing.T) {
	if os.Getenv("WEATHER_BENCHMARK_QS_TEST") != "1" {
		t.Skip("set WEATHER_BENCHMARK_QS_TEST=1 for offscreen Quickshell integration")
	}
	directory := t.TempDir()
	raw := []byte(`import QtQuick
import QtQuick.Controls
import Quickshell
import Quickshell.Io
ShellRoot {id:root
 QtObject {id:bridge;property var snapshot:({effects_setup:{status:"ready"},effect_status:{state:"stopped"}});function send(op){return true}}
 IpcHandler {target:"a-weather-app-ui"}
 ApplicationWindow {id:window;visible:true;width:100;height:100
  Rectangle {anchors.fill:parent;color:"blue";SequentialAnimation on opacity {loops:Animation.Infinite;NumberAnimation {to:0.5;duration:200} NumberAnimation {to:1;duration:200}}}
 }
}`)
	patched, err := instrumentShell(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "shell.qml"), patched, 0600); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	writer := &boundedFrameWriter{destination: &log}
	process := exec.Command("/usr/bin/quickshell", "--path", directory, "--no-color")
	process.Stdout = writer
	process.Stderr = writer
	process.Env = append(os.Environ(), "QT_QPA_PLATFORM=offscreen", "QT_QPA_PLATFORMTHEME=generic", "QT_QUICK_CONTROLS_STYLE=Basic", "QT_QUICK_BACKEND=software", "QT_IM_MODULE=none")
	process.WaitDelay = 500 * time.Millisecond
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	defer func() {
		process.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			process.Process.Kill()
			<-done
		}
	}()
	time.Sleep(time.Second)
	data, err := frameCommand("/usr/bin/quickshell", "ipc", "--path", directory, "call", "a-weather-app-ui", "frameReport")
	if err != nil {
		writer.Lock()
		diagnostic := log.String()
		writer.Unlock()
		t.Fatalf("IPC: %v; server log: %s", err, diagnostic)
	}
	report, err := parseFrames(data, false)
	if err != nil {
		t.Fatalf("report %q: %v", data, err)
	}
	if len(report.Callbacks) < 3 {
		t.Fatalf("window frameSwapped unavailable: %d callbacks", len(report.Callbacks))
	}
}
func TestBaselineInstrumentationIsolated(t *testing.T) {
	source := t.TempDir()
	destination := filepath.Join(t.TempDir(), "copy")
	os.MkdirAll(filepath.Join(source, "ui/qml"), 0700)
	raw := []byte("ShellRoot { id:root\n IpcHandler { target:\"a-weather-app-ui\" }\nApplicationWindow {id:window}\n}")
	path := filepath.Join(source, "ui/qml/shell.qml")
	os.WriteFile(path, raw, 0600)
	if err := baselineCopy(source, destination); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	if !bytes.Equal(original, raw) {
		t.Fatal("modified original baseline")
	}
	patched, _ := os.ReadFile(filepath.Join(destination, "ui/qml/shell.qml"))
	if !strings.Contains(string(patched), "function onFrameSwapped()") || !strings.Contains(string(patched), "function frameReport():string") {
		t.Fatal("observer missing")
	}
	if _, err := instrumentShell(patched); err == nil {
		t.Fatal("instrumented twice")
	}
	if !strings.Contains(string(patched), "benchmarkStartEffects") || !strings.Contains(string(patched), "benchmarkEffectsSnapshot") {
		t.Fatal("native-effects test hooks missing")
	}
	if err := baselineCopy(source, filepath.Join(source, "must-not-write")); err == nil {
		t.Fatal("created instrumentation inside source")
	}
	if _, err := os.Stat(filepath.Join(source, "must-not-write")); !os.IsNotExist(err) {
		t.Fatal("wrote into source despite refusal")
	}
	if err := os.Symlink(path, filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := baselineCopy(source, filepath.Join(t.TempDir(), "refused")); err == nil {
		t.Fatal("followed source symlink")
	}
}
func TestBoundedFrameLog(t *testing.T) {
	var buffer bytes.Buffer
	writer := &boundedFrameWriter{destination: &buffer}
	n, err := writer.Write(make([]byte, frameLogLimit+100))
	if n != frameLogLimit+100 || err != nil || buffer.Len() != frameLogLimit || !writer.capped {
		t.Fatal("log cap", n, err, buffer.Len())
	}
}
