// Development-only finite whole-app measurement. Never edits real saved data.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/notifications"
	"github.com/joega/a-weather-app/internal/safeio"
)

type row struct{ identity, cpu, rss, pss, switches int64 }
type sample struct {
	Seconds float64 `json:"seconds"`
	RSS     int64   `json:"rss_bytes"`
	PSS     int64   `json:"pss_bytes"`
}

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func readTree(root int) (map[int]row, error) {
	pending := []int{root}
	result := map[int]row{}
	for len(pending) > 0 {
		pid := pending[0]
		pending = pending[1:]
		if _, ok := result[pid]; ok {
			continue
		}
		base := fmt.Sprintf("/proc/%d", pid)
		raw, e := os.ReadFile(base + "/stat")
		if os.IsNotExist(e) || errors.Is(e, syscall.ESRCH) {
			continue
		}
		if e != nil {
			return nil, e
		}
		i := strings.LastIndex(string(raw), ")")
		if i < 0 {
			return nil, errors.New("invalid process metadata")
		}
		fields := strings.Fields(string(raw[i+1:]))
		if len(fields) < 22 {
			return nil, errors.New("short process metadata")
		}
		number := func(index int) int64 { n, _ := strconv.ParseInt(fields[index], 10, 64); return n }
		r := row{identity: number(19), cpu: number(11) + number(12), rss: number(21) * int64(os.Getpagesize())}
		tasks, e := os.ReadDir(base + "/task")
		if os.IsNotExist(e) || errors.Is(e, syscall.ESRCH) {
			continue
		}
		if e != nil {
			return nil, e
		}
		for _, task := range tasks {
			dir := base + "/task/" + task.Name()
			children, _ := os.ReadFile(dir + "/children")
			for _, s := range strings.Fields(string(children)) {
				id, e := strconv.Atoi(s)
				if e == nil {
					pending = append(pending, id)
				}
			}
			status, _ := os.ReadFile(dir + "/status")
			for _, line := range strings.Split(string(status), "\n") {
				if strings.HasPrefix(line, "voluntary_ctxt_switches:") || strings.HasPrefix(line, "nonvoluntary_ctxt_switches:") {
					n, _ := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
					r.switches += n
				}
			}
		}
		mem, e := os.ReadFile(base + "/smaps_rollup")
		if os.IsNotExist(e) || errors.Is(e, syscall.ESRCH) {
			continue
		}
		if e != nil {
			return nil, e
		}
		for _, line := range strings.Split(string(mem), "\n") {
			if strings.HasPrefix(line, "Pss:") {
				n, _ := strconv.ParseInt(strings.Fields(line)[1], 10, 64)
				r.pss = n * 1024
			}
		}
		result[pid] = r
	}
	return result, nil
}
func totals(rows map[int]row) (rss, pss int64) {
	for _, r := range rows {
		rss += r.rss
		pss += r.pss
	}
	return
}
func command(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, args[0], args[1:]...).Output()
}

func environmentWithStateDir(environment []string, stateDir string) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "A_WEATHER_APP_STATE_DIR=") {
			result = append(result, entry)
		}
	}
	return append(result, "A_WEATHER_APP_STATE_DIR="+stateDir)
}
func write(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.Write(append(b, '\n'))
	return e
}

type client struct {
	PID      int   `json:"pid"`
	Mapped   bool  `json:"mapped"`
	Visible  bool  `json:"visible"`
	Hidden   bool  `json:"hidden"`
	Floating bool  `json:"floating"`
	Size     []int `json:"size"`
	At       []int `json:"at"`
	Monitor  *int  `json:"monitor"`
}

func clients(hypr []string) ([]client, error) {
	b, e := command(append(hypr, "clients", "-j")...)
	if e != nil {
		return nil, e
	}
	var cs []client
	e = json.Unmarshal(b, &cs)
	return cs, e
}

const benchmarkWidth = 749
const benchmarkHeight = 910
const benchmarkX = 15
const benchmarkY = 40

type benchmarkOutput struct {
	Name              string
	ID, Width, Height int
	Scale             float64
	Reserved          [4]int // Hyprland logical left, top, right, bottom.
}

type windowGeometryBinding struct {
	starttime string
	output    benchmarkOutput
}

// The finite benchmark is serial; each invocation owns its own binding map.
var windowGeometryBindings = map[int]windowGeometryBinding{}

func benchmarkInteger(value any, low, high int) (int, bool) {
	n, ok := value.(float64)
	if !ok || !(n >= float64(low) && n <= float64(high)) || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}

func decodeBenchmarkOutput(raw []byte) (benchmarkOutput, error) {
	var output benchmarkOutput
	value, err := safeio.Decode(raw, 2*1024*1024)
	if err != nil {
		return output, err
	}
	rows, ok := value.([]any)
	if !ok || len(rows) != 1 {
		return output, errors.New("benchmark requires exactly one output")
	}
	m, ok := rows[0].(map[string]any)
	if !ok || m["disabled"] != false || m["dpmsStatus"] != true || m["mirrorOf"] != "none" || m["transform"] != float64(0) || m["x"] != float64(0) || m["y"] != float64(0) {
		return output, errors.New("benchmark requires one powered, unmirrored, untransformed output at origin 0,0")
	}
	output.Name, ok = m["name"].(string)
	if !ok || output.Name == "" || len(output.Name) > 128 {
		return output, errors.New("benchmark output name unavailable")
	}
	var idOK, widthOK, heightOK, scaleOK bool
	output.ID, idOK = benchmarkInteger(m["id"], 0, 2147483647)
	output.Width, widthOK = benchmarkInteger(m["width"], 1, 32768)
	output.Height, heightOK = benchmarkInteger(m["height"], 1, 32768)
	output.Scale, scaleOK = m["scale"].(float64)
	if !idOK || !widthOK || !heightOK || !scaleOK || !(output.Scale >= .25 && output.Scale <= 8) {
		return output, errors.New("benchmark output dimensions or scale invalid")
	}
	width, height := float64(output.Width)/output.Scale, float64(output.Height)/output.Scale
	if width != float64(int(width)) || height != float64(int(height)) {
		return output, errors.New("benchmark requires integral logical output dimensions")
	}
	reserved, ok := m["reserved"].([]any)
	if !ok || len(reserved) != 4 {
		return output, errors.New("benchmark output reserved edges unavailable")
	}
	for i, value := range reserved {
		var valid bool
		output.Reserved[i], valid = benchmarkInteger(value, 0, 32768)
		if !valid {
			return output, errors.New("benchmark output reserved edges invalid")
		}
	}
	if benchmarkX < output.Reserved[0] || benchmarkY < output.Reserved[1] || benchmarkX+benchmarkWidth > int(width)-output.Reserved[2] || benchmarkY+benchmarkHeight > int(height)-output.Reserved[3] {
		return output, errors.New("fixed benchmark window does not fit wholly within the usable logical output")
	}
	return output, nil
}

func currentBenchmarkOutput(hypr []string) (benchmarkOutput, error) {
	raw, err := command(append(hypr, "monitors", "all", "-j")...)
	if err != nil {
		return benchmarkOutput{}, err
	}
	return decodeBenchmarkOutput(raw)
}

func windowStarttime(pid int) (string, error) {
	if pid <= 1 || pid > 2147483647 {
		return "", errors.New("invalid owned window PID")
	}
	raw, err := safeio.ReadFile(fmt.Sprintf("/proc/%d/stat", pid), 4096)
	if err != nil {
		return "", err
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return "", errors.New("invalid owned window process metadata")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" {
		return "", errors.New("owned window process exited")
	}
	if _, err = strconv.ParseUint(fields[19], 10, 64); err != nil {
		return "", err
	}
	return fields[19], nil
}

func geometryCommand(operation string, pid int) (string, error) {
	if pid <= 1 || pid > 2147483647 {
		return "", errors.New("invalid owned window PID")
	}
	window := fmt.Sprintf("pid:%d", pid)
	switch operation {
	case "float":
		return fmt.Sprintf("hl.dsp.window.float({window=%q, action='set'})", window), nil
	case "resize":
		return fmt.Sprintf("hl.dsp.window.resize({window=%q, x=%d, y=%d})", window, benchmarkWidth, benchmarkHeight), nil
	case "move":
		// Verified against installed Hyprland efb509 Lua stubs/binary and
		// https://wiki.hypr.land/configuring/core/dispatchers/: relative=false
		// means absolute logical coordinates, with an explicit window selector.
		return fmt.Sprintf("hl.dsp.window.move({window=%q, x=%d, y=%d, relative=false})", window, benchmarkX, benchmarkY), nil
	default:
		return "", errors.New("unknown benchmark window operation")
	}
}

func ownWindow(rows []client, pid int) (client, int) {
	var found client
	count := 0
	for _, candidate := range rows {
		if candidate.PID == pid {
			found = candidate
			count++
		}
	}
	return found, count
}

// Position and size only the process-owned benchmark window. The floating state
// disappears with that window; no compositor config or user window is touched.
func fixWindowGeometry(hypr []string, pid int) (client, error) {
	output, err := currentBenchmarkOutput(hypr)
	if err != nil {
		return client{}, err
	}
	identity, err := windowStarttime(pid)
	if err != nil {
		return client{}, err
	}
	initial, err := clients(hypr)
	if err != nil {
		return client{}, err
	}
	window, matches := ownWindow(initial, pid)
	if matches != 1 || !window.Mapped || !window.Visible || window.Hidden || window.Monitor == nil || *window.Monitor != output.ID {
		return client{}, errors.New("cannot resize an unmapped benchmark window")
	}
	windowGeometryBindings[pid] = windowGeometryBinding{identity, output}
	for _, operation := range []string{"float", "resize", "move"} {
		current, err := windowStarttime(pid)
		if err != nil || current != identity {
			return client{}, errors.New("owned window process identity changed before dispatch")
		}
		rows, err := clients(hypr)
		if err != nil {
			return client{}, err
		}
		candidate, count := ownWindow(rows, pid)
		if count != 1 || !candidate.Mapped || !candidate.Visible || candidate.Hidden || candidate.Monitor == nil || *candidate.Monitor != output.ID {
			return client{}, errors.New("owned window changed before geometry dispatch")
		}
		dispatch, err := geometryCommand(operation, pid)
		if err != nil {
			return client{}, err
		}
		current, err = windowStarttime(pid)
		if err != nil || current != identity {
			return client{}, errors.New("owned window process identity changed during geometry lookup")
		}
		if _, err = command(append(hypr, "dispatch", dispatch)...); err != nil {
			return client{}, fmt.Errorf("owned window %s: %w", operation, err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		rows, err := clients(hypr)
		if err != nil {
			return client{}, err
		}
		window, matches = ownWindow(rows, pid)
		if windowVisibilityMatches(window, matches, pid, false) {
			if err = checkWindowVisibility(hypr, pid, false); err != nil {
				return client{}, err
			}
			return window, nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return client{}, fmt.Errorf("owned window did not reach floating %dx%d at %d,%d", benchmarkWidth, benchmarkHeight, benchmarkX, benchmarkY)
}

func windowVisibilityMatches(window client, matches int, pid int, hidden bool) bool {
	if hidden {
		return matches == 0 || matches == 1 && window.PID == pid && (window.Hidden || !window.Mapped || !window.Visible)
	}
	return matches == 1 && window.PID == pid && window.Mapped && window.Visible && !window.Hidden && window.Floating && len(window.Size) == 2 && window.Size[0] == benchmarkWidth && window.Size[1] == benchmarkHeight && len(window.At) == 2 && window.At[0] == benchmarkX && window.At[1] == benchmarkY
}

func checkWindowVisibility(hypr []string, pid int, hidden bool) error {
	binding, captured := windowGeometryBindings[pid]
	identity, err := windowStarttime(pid)
	if !captured || err != nil || identity != binding.starttime {
		return errors.New("benchmark window PID identity changed or was not captured")
	}
	if !hidden {
		output, err := currentBenchmarkOutput(hypr)
		if err != nil {
			return err
		}
		if output != binding.output {
			return errors.New("benchmark output geometry, scale or reserved edges changed")
		}
	}
	rows, err := clients(hypr)
	if err != nil {
		return err
	}
	window, matches := ownWindow(rows, pid)
	if !hidden && (window.Monitor == nil || *window.Monitor != binding.output.ID) {
		return errors.New("benchmark window moved to a different output")
	}
	if !windowVisibilityMatches(window, matches, pid, hidden) {
		if hidden {
			return errors.New("owned window remained visible during hidden measurement")
		}
		return errors.New("owned window visibility or geometry changed during measurement")
	}
	return nil
}

func baselineCall(qmlRoot, method string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	args := []string{"ipc", "--path", qmlRoot, "call", "a-weather-app-ui", method}
	raw, err := exec.CommandContext(ctx, "/usr/bin/quickshell", args...).CombinedOutput()
	if err != nil {
		if len(raw) > 2048 {
			raw = raw[:2048]
		}
		return nil, fmt.Errorf("baseline Quickshell IPC %s: %w: %s", method, err, strings.TrimSpace(string(raw)))
	}
	if len(raw) > 8192 {
		return nil, errors.New("baseline Quickshell IPC response exceeded bound")
	}
	return raw, nil
}

func decodeEffectSnapshot(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > 8192 {
		return nil, errors.New("invalid effects status response size")
	}
	value, err := safeio.Object(raw, 8192)
	if err != nil {
		return nil, err
	}
	setup, _ := value["setup"].(map[string]any)
	status, _ := value["status"].(map[string]any)
	if setup == nil || status == nil {
		return nil, fmt.Errorf("incomplete effects status response: %s", strings.TrimSpace(string(raw)))
	}
	return value, nil
}

func effectsState(snapshot map[string]any) string {
	status, _ := snapshot["effect_status"].(map[string]any)
	if status == nil {
		status, _ = snapshot["status"].(map[string]any)
	}
	state, _ := status["state"].(string)
	return state
}

func migrationCall(socket, op string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	request := map[string]any{"op": op}
	if op == "start_effects" {
		request["duration"] = 300
	}
	return ipc.Call(ctx, socket, request)
}

func effectsRequest(kind, qmlRoot, socket, op string) error {
	if kind == "baseline" {
		method := map[string]string{"check_effects": "benchmarkCheckEffects", "start_effects": "benchmarkStartEffects", "stop_effects": "benchmarkStopEffects"}[op]
		if method == "" {
			return errors.New("unsupported baseline effects operation")
		}
		raw, err := baselineCall(qmlRoot, method)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != "true" {
			return errors.New("baseline effects action was not queued")
		}
		return nil
	}
	reply, err := migrationCall(socket, op)
	if err != nil {
		return err
	}
	if reply["ok"] != true {
		return fmt.Errorf("migration effects request %s failed: %v", op, reply["error"])
	}
	return nil
}

func effectsSnapshot(kind, qmlRoot, socket string) (map[string]any, error) {
	if kind == "baseline" {
		raw, err := baselineCall(qmlRoot, "benchmarkEffectsSnapshot")
		if err != nil {
			return nil, err
		}
		return decodeEffectSnapshot(raw)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := ipc.Call(ctx, socket, map[string]any{"op": "snapshot"})
	if err != nil {
		return nil, err
	}
	snapshot, _ := reply["snapshot"].(map[string]any)
	if reply["ok"] != true || snapshot == nil {
		return nil, errors.New("migration effects snapshot unavailable")
	}
	return snapshot, nil
}

func waitEffectsState(kind, qmlRoot, socket, want string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snapshot, err := effectsSnapshot(kind, qmlRoot, socket)
		if err == nil && effectsState(snapshot) == want {
			return snapshot, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("effects session did not reach %s", want)
}
func stop(p *os.Process, done <-chan error) error {
	_ = p.Signal(syscall.SIGTERM)
	select {
	case e := <-done:
		return e
	case <-time.After(100 * time.Second):
		return errors.New("owned app cleanup exceeded deadline; guardian left to finish")
	}
}
func run() error {
	root := flag.String("root", "", "source checkout or runtime package")
	kind := flag.String("kind", "migration", "baseline or migration")
	saved := flag.String("saved-state", "", "read-only saved state source")
	reduced := flag.Bool("reduced-motion", false, "use reduced motion in isolated state")
	hidden := flag.Bool("hidden", false, "hide window with notifications enabled and snoozed")
	seconds := flag.Int("seconds", 30, "1..600 second sampling interval")
	runs := flag.Int("runs", 3, "1..3 runs")
	capture := flag.Bool("capture", false, "capture only the owned weather window")
	frames := flag.Bool("frames", false, "separate matched visible frame-callback measurement (not GPU time)")
	effects := flag.Bool("effects", false, "measure a finite owned native rain preview with the window visible")
	breakdown := flag.Bool("process-breakdown", false, "include bounded owned-process CPU/RSS diagnostics")
	flag.Parse()
	if !filepath.IsAbs(*root) || !filepath.IsAbs(*saved) || (*kind != "baseline" && *kind != "migration") || *seconds < 1 || *seconds > 600 || *runs < 1 || *runs > 3 {
		return errors.New("invalid benchmark arguments")
	}
	if *frames && (*hidden || *seconds > 120) {
		return errors.New("frame mode requires a visible window and at most120seconds (10000callback bound)")
	}
	if *effects && (*hidden || *frames) {
		return errors.New("native effects mode requires a visible window and whole-app measurement")
	}
	state, e := safeio.OpenDir(*saved, false)
	if e != nil {
		return e
	}
	defer state.Close()
	profile, e := state.Read("location-profile.json", 2*1024*1024)
	if e != nil {
		return e
	}
	if profile == nil {
		return errors.New("saved location profile required")
	}
	controls, e := state.Read("controls.json", 8192)
	if e != nil {
		return e
	}
	if controls == nil {
		controls = map[string]any{}
	}
	controls["mode"] = "live"
	controls["reduced_motion"] = *reduced
	if *effects {
		controls["mode"] = "manual"
		controls["manual"] = map[string]any{"condition": "rain"}
	}
	location, ok := profile["location"].(map[string]any)
	if !ok {
		return errors.New("location missing")
	}
	forecast, ok := profile["forecast"].(map[string]any)
	if !ok {
		return errors.New("forecast missing")
	}
	b, e := command("/usr/bin/hyprctl", "instances", "-j")
	if e != nil {
		return e
	}
	var instances []struct {
		Instance string `json:"instance"`
		Socket   string `json:"wl_socket"`
	}
	if e = json.Unmarshal(b, &instances); e != nil {
		return e
	}
	instance := ""
	for _, in := range instances {
		if in.Socket == os.Getenv("WAYLAND_DISPLAY") {
			if instance != "" {
				return errors.New("ambiguous display")
			}
			instance = in.Instance
		}
	}
	if instance == "" {
		return errors.New("display unavailable")
	}
	hypr := []string{"/usr/bin/hyprctl", "-i", instance}
	hzRaw, e := command("/usr/bin/getconf", "CLK_TCK")
	if e != nil {
		return e
	}
	hz, e := strconv.ParseFloat(strings.TrimSpace(string(hzRaw)), 64)
	if e != nil || hz <= 0 {
		return errors.New("clock tick resolution unavailable")
	}
	output, e := os.MkdirTemp("/tmp", "weather-migration-benchmark.")
	if e != nil {
		return e
	}
	fmt.Println("Private evidence directory:", output)
	runRoot := *root
	if (*frames || *effects) && *kind == "baseline" {
		runRoot = filepath.Join(output, "baseline-instrumented")
		if e = baselineCopy(*root, runRoot); e != nil {
			return e
		}
	}
	results := []map[string]any{}
	for run := 1; run <= *runs; run++ {
		result, e := func() (map[string]any, error) {
			dir := filepath.Join(output, fmt.Sprintf("state-%d", run))
			if e = os.Mkdir(dir, 0700); e != nil {
				return nil, e
			}
			preparedForecast, prepareErr := freshBenchmarkForecast(forecast, time.Now())
			if prepareErr != nil {
				return nil, prepareErr
			}
			for name, value := range map[string]any{"location.json": location, "forecast.json": preparedForecast, "controls.json": controls} {
				if e = write(filepath.Join(dir, name), value); e != nil {
					return nil, e
				}
			}
			if *hidden {
				n := notifications.DefaultDocument()
				n["settings"].(map[string]any)["enabled"] = true
				n["snoozed_until"] = time.Now().Add(24 * time.Hour).Unix()
				if e = write(filepath.Join(dir, "notifications.json"), n); e != nil {
					return nil, e
				}
			}
			executable := filepath.Join(runRoot, "a-weather-app")
			if *kind == "migration" {
				if _, e := os.Stat(filepath.Join(*root, "build/a-weather-app")); e == nil {
					executable = filepath.Join(*root, "build/a-weather-app")
				}
			}
			extraLifetime := 15
			if *effects {
				extraLifetime = 90 // Compatibility, activation and bounded cleanup precede sampling.
			}
			args := []string{"--state-dir", dir, "--duration", strconv.Itoa(*seconds + extraLifetime)}
			if *kind == "migration" {
				args = append(args, "--offline")
				if *frames {
					args = append(args, "--service", "--measure-frames")
				}
			}
			log, e := os.OpenFile(filepath.Join(output, fmt.Sprintf("run-%d.log", run)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				return nil, e
			}
			defer log.Close()
			proc := exec.Command(executable, args...)
			if *kind == "baseline" {
				// The original QML bridge gets its state root from this environment
				// variable. Keep legacy writes inside the disposable fixture too.
				proc.Env = environmentWithStateDir(os.Environ(), dir)
			}
			proc.Stdout = log
			proc.Stderr = log
			frameLog := &boundedFrameWriter{destination: log}
			if *frames {
				proc.Stdout = frameLog
				proc.Stderr = frameLog
			}
			began := time.Now()
			if e = proc.Start(); e != nil {
				return nil, e
			}
			done := make(chan error, 1)
			go func() { done <- proc.Wait() }()
			stopped := false
			defer func() {
				if !stopped {
					_ = stop(proc.Process, done)
				}
			}()
			var c *client
			for time.Since(began) < 8*time.Second {
				rows, e := readTree(proc.Process.Pid)
				if e != nil {
					return nil, e
				}
				cs, e := clients(hypr)
				if e != nil {
					return nil, e
				}
				for _, candidate := range cs {
					if _, ok := rows[candidate.PID]; ok && candidate.Mapped {
						copy := candidate
						c = &copy
						break
					}
				}
				if c != nil {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			if c == nil {
				return nil, errors.New("owned window did not map; inspect private log")
			}
			startup := float64(time.Since(began).Microseconds()) / 1000
			fixedWindow, geometryErr := fixWindowGeometry(hypr, c.PID)
			if geometryErr != nil {
				return nil, geometryErr
			}
			c = &fixedWindow
			time.Sleep(3 * time.Second)
			socket := ""
			if *kind == "migration" {
				socketRaw, socketErr := command(executable, "--state-dir", dir, "--print-socket")
				if socketErr != nil {
					return nil, socketErr
				}
				socket = strings.TrimSpace(string(socketRaw))
				if socket == "" || len(socket) > 4096 {
					return nil, errors.New("migration service socket unavailable")
				}
			}
			if *effects {
				qmlRoot := filepath.Join(runRoot, "ui/qml")
				if e = effectsRequest(*kind, qmlRoot, socket, "check_effects"); e != nil {
					return nil, fmt.Errorf("compatibility check request: %w", e)
				}
				deadline := time.Now().Add(30 * time.Second)
				ready := false
				lastReason := "no effects snapshot"
				for time.Now().Before(deadline) {
					setupSnapshot, statusErr := effectsSnapshot(*kind, qmlRoot, socket)
					if statusErr == nil {
						setup, _ := setupSnapshot["effects_setup"].(map[string]any)
						if setup == nil {
							setup, _ = setupSnapshot["setup"].(map[string]any)
						}
						if setup != nil {
							lastReason, _ = setup["reason"].(string)
							if lastReason == "" {
								lastReason, _ = setup["status"].(string)
							}
							if setup["status"] == "ready" {
								ready = true
								break
							}
							if reason, _ := setup["reason"].(string); reason != "" && reason != "not_checked" {
								return nil, fmt.Errorf("native effects preflight refused activation: %s", reason)
							}
						}
					} else {
						lastReason = statusErr.Error()
					}
					time.Sleep(200 * time.Millisecond)
				}
				if !ready {
					return nil, fmt.Errorf("native effects preflight did not become ready (%s)", lastReason)
				}
				if e = effectsRequest(*kind, qmlRoot, socket, "start_effects"); e != nil {
					return nil, fmt.Errorf("finite effects start: %w", e)
				}
				activeSnapshot, statusErr := waitEffectsState(*kind, qmlRoot, socket, "running", 60*time.Second)
				if statusErr != nil {
					return nil, statusErr
				}
				status, _ := activeSnapshot["effect_status"].(map[string]any)
				if status == nil {
					status, _ = activeSnapshot["status"].(map[string]any)
				}
				if status != nil && status["persistent"] == true {
					return nil, errors.New("finite benchmark unexpectedly entered persistent effects mode")
				}
				time.Sleep(2 * time.Second)
			}
			if *capture && len(c.At) == 2 && len(c.Size) == 2 {
				_, e = command("/usr/bin/grim", "-g", fmt.Sprintf("%d,%d %dx%d", c.At[0], c.At[1], c.Size[0], c.Size[1]), filepath.Join(output, fmt.Sprintf("window-%d.png", run)))
				if e != nil {
					return nil, e
				}
			}
			if *hidden {
				if *kind == "baseline" {
					_, e = command("/usr/bin/quickshell", "ipc", "--path", filepath.Join(*root, "ui/qml"), "call", "a-weather-app-ui", "toggleWindow")
				} else {
					socket, e2 := command(executable, "--state-dir", dir, "--print-socket")
					if e2 != nil {
						return nil, e2
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					_, e = ipc.Call(ctx, strings.TrimSpace(string(socket)), map[string]any{"op": "toggle_window"})
					cancel()
				}
				if e != nil {
					return nil, e
				}
				hiddenDeadline := time.Now().Add(3 * time.Second)
				for {
					e = checkWindowVisibility(hypr, c.PID, true)
					if e == nil {
						break
					}
					if time.Now().After(hiddenDeadline) {
						return nil, e
					}
					time.Sleep(40 * time.Millisecond)
				}
			} else if e = checkWindowVisibility(hypr, c.PID, false); e != nil {
				return nil, e
			}
			previous, e := readTree(proc.Process.Pid)
			if e != nil {
				return nil, e
			}
			initialRSS, _ := totals(previous)
			var peakRSS, peakPSS, ticks, switches int64
			maximum := 0
			processes := map[int]*processMeasurement{}
			samples := []sample{}
			started := time.Now()
			frameStart := started.UnixMilli()
			for time.Since(started) < time.Duration(*seconds)*time.Second {
				time.Sleep(min(500*time.Millisecond, time.Until(started.Add(time.Duration(*seconds)*time.Second))))
				if e = checkWindowVisibility(hypr, c.PID, *hidden); e != nil {
					return nil, e
				}
				current, e := readTree(proc.Process.Pid)
				if e != nil {
					return nil, e
				}
				if _, ok := current[proc.Process.Pid]; !ok {
					return nil, errors.New("root exited before measurement finished")
				}
				for pid, r := range current {
					var diagnostic *processMeasurement
					if *breakdown {
						diagnostic = processes[pid]
						if diagnostic == nil || diagnostic.Identity != r.identity {
							diagnostic = &processMeasurement{PID: pid, Identity: r.identity, Role: processRole(pid)}
							processes[pid] = diagnostic
						}
						diagnostic.PeakRSS = max(diagnostic.PeakRSS, r.rss)
						diagnostic.PeakPSS = max(diagnostic.PeakPSS, r.pss)
					}
					if old, ok := previous[pid]; ok && old.identity == r.identity {
						ticks += max(0, r.cpu-old.cpu)
						if diagnostic != nil {
							diagnostic.Ticks += max(0, r.cpu-old.cpu)
						}
						switches += max(0, r.switches-old.switches)
					}
				}
				rss, pss := totals(current)
				samples = append(samples, sample{time.Since(started).Seconds(), rss, pss})
				peakRSS = max(peakRSS, rss)
				peakPSS = max(peakPSS, pss)
				maximum = max(maximum, len(current))
				previous = current
			}
			elapsed := time.Since(started).Seconds()
			frameEnd := time.Now().UnixMilli()
			var frameData []byte
			if *frames && *kind == "baseline" {
				frameData, e = frameCommand("/usr/bin/quickshell", "ipc", "--path", filepath.Join(runRoot, "ui/qml"), "call", "a-weather-app-ui", "frameReport")
				if e != nil {
					return nil, e
				}
			}
			result := map[string]any{"kind": *kind, "run": run, "reduced_motion": *reduced, "hidden": *hidden, "native_effects": *effects, "seconds": elapsed, "startup_mapped_ms": startup, "geometry": c.Size, "cpu_percent_one_core": 100 * float64(ticks) / hz / elapsed, "scheduler_switches_per_second": float64(switches) / elapsed, "peak_summed_rss_mib": float64(peakRSS) / 1048576, "peak_summed_pss_mib": float64(peakPSS) / 1048576, "rss_growth_mib": float64(samples[len(samples)-1].RSS-initialRSS) / 1048576, "maximum_processes": maximum}
			if *breakdown {
				result["processes"] = processStatistics(processes, hz, elapsed)
			}
			result["samples"] = samples
			if *effects {
				if e = effectsRequest(*kind, filepath.Join(runRoot, "ui/qml"), socket, "stop_effects"); e != nil {
					return nil, fmt.Errorf("finite effects cleanup request: %w", e)
				}
				if _, e = waitEffectsState(*kind, filepath.Join(runRoot, "ui/qml"), socket, "stopped", 60*time.Second); e != nil {
					return nil, e
				}
			}
			e = stop(proc.Process, done)
			stopped = true
			if e != nil {
				return nil, fmt.Errorf("cleanup failed: %w", e)
			}
			if *frames {
				if frameLog.capped {
					return nil, errors.New("private frame log exceeded bound")
				}
				if *kind == "migration" {
					if e = log.Sync(); e != nil {
						return nil, e
					}
					frameData, e = os.ReadFile(log.Name())
					if e != nil {
						return nil, e
					}
				}
				report, err := parseFrames(frameData, *kind == "migration")
				if err != nil {
					return nil, err
				}
				if err = write(filepath.Join(output, fmt.Sprintf("frame-report-%d.json", run)), report); err != nil {
					return nil, err
				}
				stats, err := frameStatistics(report, frameStart, frameEnd)
				if err != nil {
					return nil, err
				}
				result = map[string]any{"kind": *kind, "run": run, "mode": "frame_callbacks_only", "geometry": c.Size, "seconds": elapsed, "warmup_seconds": 3, "reduced_motion": *reduced, "frames": stats, "process_scope": "frame-only service excludes migration launcher/guardian; whole-app CPU/RSS mode unchanged"}
			}
			result["window_position"] = c.At
			console := map[string]any{}
			result["fixture_timestamp_policy"] = "private fetched_at and current.time refreshed together; saved weather values unchanged"
			for key, value := range result {
				if key != "samples" {
					console[key] = value
				}
			}
			printed, _ := json.Marshal(console)
			fmt.Println(string(printed))
			return result, nil
		}()
		if e != nil {
			return e
		}
		results = append(results, result)
		if e = write(filepath.Join(output, fmt.Sprintf("result-%d.json", run)), result); e != nil {
			return e
		}
	}
	if e = write(filepath.Join(output, "results.json"), results); e != nil {
		return e
	}
	fmt.Println("Evidence:", filepath.Join(output, "results.json"))
	return nil
}
