// weather-qt-diagnose runs bounded, private, offline Qt teardown experiments.
// It is development tooling and is not included in the installed runtime.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var experimentContext = context.Background()

type result struct {
	Command     []string          `json:"command"`
	CWD         string            `json:"cwd"`
	Timeout     int               `json:"timeout_seconds"`
	Environment map[string]string `json:"environment"`
	ReturnCode  int               `json:"returncode"`
	TimedOut    bool              `json:"timed_out"`
	Interrupted bool              `json:"interrupted"`
	Seconds     float64           `json:"seconds"`
	Cleaned     []int             `json:"cleaned_child_pids"`
	Mode        string            `json:"mode,omitempty"`
	Inferior    map[string]any    `json:"inferior,omitempty"`
	Passed      bool              `json:"passed"`
}

func save(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}

func hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func output(cwd string, args ...string) (string, error) {
	c := exec.Command(args[0], args[1:]...)
	c.Dir = cwd
	raw, err := c.Output()
	return strings.TrimSpace(string(raw)), err
}

func privateEnv(base string) (map[string]string, error) {
	env := map[string]string{}
	for _, value := range os.Environ() {
		key, val, _ := strings.Cut(value, "=")
		if strings.HasPrefix(key, "WEATHER_") || strings.HasPrefix(key, "QT_") || strings.HasPrefix(key, "QML_") || strings.HasPrefix(key, "QSG_") {
			continue
		}
		switch key {
		case "HYPRLAND_INSTANCE_SIGNATURE", "WAYLAND_DISPLAY", "DISPLAY", "DBUS_SESSION_BUS_ADDRESS", "LD_PRELOAD", "LD_LIBRARY_PATH", "MALLOC_PERTURB_", "GLIBC_TUNABLES":
			continue
		}
		env[key] = val
	}
	for key, folder := range map[string]string{"HOME": "home", "XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data", "XDG_STATE_HOME": "state", "XDG_RUNTIME_DIR": "runtime", "TMPDIR": "tmp"} {
		path := filepath.Join(base, folder)
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		env[key] = path
	}
	for key, val := range map[string]string{"QT_QPA_PLATFORM": "offscreen", "QT_QPA_PLATFORMTHEME": "basic", "QT_QUICK_BACKEND": "software", "WEATHER_QT_TEARDOWN_TRACE": "1", "DEBUGINFOD_URLS": "", "ASAN_OPTIONS": "detect_leaks=0:abort_on_error=1", "UBSAN_OPTIONS": "halt_on_error=1:print_stacktrace=1"} {
		env[key] = val
	}
	return env, nil
}

func childPIDs() ([]int, error) {
	// Go may launch children from any runtime thread. Read every owned thread.
	tasks, err := os.ReadDir("/proc/self/task")
	if err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	pids := []int{}
	for _, task := range tasks {
		raw, err := os.ReadFile(filepath.Join("/proc/self/task", task.Name(), "children"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, field := range strings.Fields(string(raw)) {
			pid, err := strconv.Atoi(field)
			if err != nil {
				return nil, err
			}
			if !seen[pid] {
				seen[pid] = true
				pids = append(pids, pid)
			}
		}
	}
	return pids, nil
}

func cleanupChildren() ([]int, error) {
	killed := []int{}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pids, err := childPIDs()
		if err != nil {
			return killed, err
		}
		if len(pids) == 0 {
			return killed, nil
		}
		for _, pid := range pids {
			if err := syscall.Kill(pid, syscall.SIGKILL); err == nil {
				killed = append(killed, pid)
			} else if err != syscall.ESRCH {
				return killed, err
			}
		}
		for {
			var status syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
			if err == syscall.ECHILD || pid == 0 {
				break
			}
			if err != nil {
				return killed, err
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return killed, errors.New("owned child cleanup did not finish")
}

func execute(command []string, cwd string, env map[string]string, log string, timeout int) (result, error) {
	r := result{Command: command, CWD: cwd, Timeout: timeout, Environment: env, Cleaned: []int{}}
	stem := strings.TrimSuffix(log, filepath.Ext(log))
	// Record only experiment settings, never inherited credentials.
	r.Environment = map[string]string{}
	for key, val := range env {
		if strings.HasPrefix(key, "QT_") || strings.HasPrefix(key, "QML_") || strings.HasPrefix(key, "QSG_") || strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "WEATHER_") {
			r.Environment[key] = val
			continue
		}
		switch key {
		case "HOME", "TMPDIR", "GO_APP", "ASAN_OPTIONS", "UBSAN_OPTIONS", "DEBUGINFOD_URLS", "GOCACHE", "GOMODCACHE", "MALLOC_PERTURB_", "GLIBC_TUNABLES":
			r.Environment[key] = val
		}
	}
	if err := save(stem+".command.json", r); err != nil {
		return r, err
	}
	f, err := os.OpenFile(log, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return r, err
	}
	defer f.Close()
	c := exec.Command(command[0], command[1:]...)
	c.Dir = cwd
	c.Stdout = f
	c.Stderr = f
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	for key, val := range env {
		c.Env = append(c.Env, key+"="+val)
	}
	started := time.Now()
	if err = c.Start(); err != nil {
		return r, err
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	stopProcess := func() error {
		_ = c.Process.Signal(os.Interrupt)
		grace := time.NewTimer(10 * time.Second)
		defer grace.Stop()
		select {
		case waitErr := <-done:
			return waitErr
		case <-grace.C:
			_ = c.Process.Kill()
			return <-done
		}
	}
	select {
	case err = <-done:
	case <-timer.C:
		r.TimedOut = true
		err = stopProcess()
	case <-experimentContext.Done():
		r.Interrupted = true
		err = stopProcess()
	}
	r.ReturnCode = c.ProcessState.ExitCode()
	r.Seconds = time.Since(started).Seconds()
	var cleanupErr error
	r.Cleaned, cleanupErr = cleanupChildren()
	if saveErr := save(stem+".result.json", r); saveErr != nil {
		return r, saveErr
	}
	if cleanupErr != nil {
		return r, cleanupErr
	}
	if r.Interrupted {
		return r, experimentContext.Err()
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return r, err
	}
	return r, nil
}

func copyFile(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("nonregular source: %s", source)
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	return errors.Join(copyErr, closeErr)
}

func build(root, out string) error {
	source := filepath.Join(out, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return err
	}
	filesCmd := exec.Command("git", "ls-files", "-z")
	filesCmd.Dir = root
	raw, err := filesCmd.Output()
	if err != nil {
		return err
	}
	hashes := map[string]string{}
	for _, rel := range strings.Split(string(raw), "\x00") {
		if rel == "" {
			continue
		}
		if err = copyFile(filepath.Join(root, rel), filepath.Join(source, rel)); err != nil {
			return err
		}
		hashes[rel], err = hash(filepath.Join(source, rel))
		if err != nil {
			return err
		}
	}
	diff := exec.Command("git", "diff", "HEAD")
	diff.Dir = root
	raw, err = diff.Output()
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, "worktree.patch"), raw, 0600); err != nil {
		return err
	}
	head, err := output(root, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if err = save(filepath.Join(out, "source-identity.json"), map[string]any{"head": head, "files": hashes}); err != nil {
		return err
	}
	env, err := privateEnv(filepath.Join(out, "build-env"))
	if err != nil {
		return err
	}
	env["GOCACHE"] = filepath.Join(out, "go-cache")
	env["GOMODCACHE"], err = output(root, "go", "env", "GOMODCACHE")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(source, "build"), 0700); err != nil {
		return err
	}
	service := filepath.Join(source, "build/a-weather-app")
	r, err := execute([]string{"go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-X main.buildMode=development", "-o", service, "./cmd/a-weather-app"}, source, env, filepath.Join(out, "go-build.log"), 180)
	if err != nil {
		return err
	}
	if r.ReturnCode != 0 || r.TimedOut {
		return errors.New("service build failed; inspect retained log")
	}
	for _, variant := range []string{"optimized", "sanitized"} {
		folder := filepath.Join(out, variant)
		if err = os.Mkdir(folder, 0700); err != nil {
			return err
		}
		qt := filepath.Join(source, "native/qt")
		lines := []string{"QT += testlib quick network", "CONFIG += console c++17 testcase release", "CONFIG -= debug", "TARGET = service-frontend-test", "INCLUDEPATH += " + qt,
			"SOURCES += " + qt + "/tests/service_frontend_test.cpp " + qt + "/transport.cpp " + qt + "/protocol.cpp " + qt + "/maptiles.cpp",
			"HEADERS += " + qt + "/transport.h " + qt + "/protocol.h " + qt + "/maptiles.h", "RESOURCES += " + qt + "/resources.qrc", "QMAKE_CXXFLAGS += -g -fno-omit-frame-pointer", "QMAKE_CXXFLAGS_RELEASE = -O2"}
		if variant == "sanitized" {
			lines[len(lines)-1] = "QMAKE_CXXFLAGS_RELEASE = -O1"
			lines = append(lines, "QMAKE_CXXFLAGS += -fsanitize=address,undefined", "QMAKE_LFLAGS += -fsanitize=address,undefined")
		}
		project := filepath.Join(folder, "diagnostic.pro")
		if err = os.WriteFile(project, []byte(strings.Join(lines, "\n")+"\n"), 0600); err != nil {
			return err
		}
		for index, command := range [][]string{{"qmake6", project}, {"make", "-j2"}} {
			step := "qmake"
			timeout := 30
			if index == 1 {
				step = "build"
				timeout = 240
			}
			r, err = execute(command, folder, env, filepath.Join(out, variant+"-"+step+".log"), timeout)
			if err != nil {
				return err
			}
			if r.ReturnCode != 0 || r.TimedOut {
				return fmt.Errorf("%s failed; inspect retained log", step)
			}
		}
		binary := filepath.Join(folder, "service-frontend-test")
		for name, command := range map[string][]string{"libraries": {"ldd", binary}, "elf": {"readelf", "-n", binary}} {
			if _, err = execute(command, out, env, filepath.Join(out, variant+"-"+name+".log"), 10); err != nil {
				return err
			}
		}
	}
	for name, command := range map[string][]string{"qt-version": {"qmake6", "-query"}, "go-identity": {"go", "version", "-m", service}} {
		if _, err = execute(command, out, env, filepath.Join(out, name+".log"), 10); err != nil {
			return err
		}
	}
	binaries := map[string]string{}
	for _, rel := range []string{"source/build/a-weather-app", "optimized/service-frontend-test", "sanitized/service-frontend-test"} {
		binaries[rel], err = hash(filepath.Join(out, rel))
		if err != nil {
			return err
		}
	}
	return save(filepath.Join(out, "binary-hashes.json"), binaries)
}

func gdbScript(status string, trace bool, sanitized bool) string {
	lines := []string{"set pagination off", "set confirm off", "set disable-randomization off", "set startup-with-shell off", "set print thread-events off", "handle SIGPIPE nostop noprint pass", "python", "import gdb, json", "result = {}", "def exited(event): result.update(exit_code=getattr(event, 'exit_code', None))", "gdb.events.exited.connect(exited)", "end"}
	if trace {
		lines = append(lines, "break WeatherTransport::fail", "commands", "silent", "bt 16", "print this->failed", "print this->buffer.d")
		if sanitized {
			lines = append(lines, "print (int)__asan_address_is_poisoned(this->buffer.d.d)", "call (void)__asan_describe_address(this->buffer.d.d)")
		}
		lines = append(lines, "continue", "end", "break MapTiles::request", "commands", "silent", `printf "MAP REQUEST REACHED\n"`, "bt 4", "continue", "end")
	}
	// The embedded Python belongs to GDB's existing interpreter, not the runtime.
	lines = append(lines, "run", "python", "if gdb.selected_thread() is not None:", "    result['stopped'] = True", "    gdb.execute('info program')", "    gdb.execute('thread apply all bt full')", "    gdb.execute('info sharedlibrary')", "    gdb.execute('kill')", fmt.Sprintf("with open(%s, 'w') as f: json.dump(result, f)", strconv.Quote(status)), "end", "quit")
	return strings.Join(lines, "\n") + "\n"
}

func run(out, variant, loop string, repeat, timeout, perturb int, modes []string, trace bool) error {
	batch := filepath.Join(out, variant+"-"+loop+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	if err := os.Mkdir(batch, 0700); err != nil {
		return err
	}
	binary := filepath.Join(out, variant, "service-frontend-test")
	service := filepath.Join(out, "source/build/a-weather-app")
	identity := map[string]string{}
	for key, path := range map[string]string{"binary_sha256": binary, "service_sha256": service} {
		val, err := hash(path)
		if err != nil {
			return err
		}
		identity[key] = val
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	identity["harness_sha256"], err = hash(self)
	if err != nil {
		return err
	}
	if err = copyFile(self, filepath.Join(batch, "weather-qt-diagnose")); err != nil {
		return err
	}
	if err = save(filepath.Join(batch, "identity.json"), identity); err != nil {
		return err
	}
	results := []result{}
	passed := 0
	for number := 1; number <= repeat; number++ {
		for _, mode := range modes {
			folder := filepath.Join(batch, fmt.Sprintf("%d-%s", number, mode))
			if err = os.Mkdir(folder, 0700); err != nil {
				return err
			}
			fixture, err := os.MkdirTemp("/tmp", "wqt-")
			if err != nil {
				return err
			}
			// Fixture removal follows owned-child cleanup, even when the run fails.
			r, err := func() (result, error) {
				defer os.RemoveAll(fixture)
				env, err := privateEnv(fixture)
				if err != nil {
					return result{}, err
				}
				env["GO_APP"] = service
				if loop != "default" {
					env["QSG_RENDER_LOOP"] = loop
				}
				if perturb != 0 {
					env["MALLOC_PERTURB_"] = strconv.Itoa(perturb)
					env["GLIBC_TUNABLES"] = "glibc.malloc.tcache_count=0"
				}
				status := filepath.Join(folder, "inferior.json")
				script := filepath.Join(folder, "capture.gdb")
				if err = os.WriteFile(script, []byte(gdbScript(status, trace, variant == "sanitized")), 0600); err != nil {
					return result{}, err
				}
				command := []string{"gdb", "-q", "-nx", "-batch", "-iex", "set debuginfod enabled off", "-x", script, "--args", binary, "hiddenPresentationSuppressesPeriodicSnapshots:" + mode, "-nocrashhandler"}
				r, err := execute(command, out, env, filepath.Join(folder, "raw.log"), timeout)
				if err != nil {
					return r, err
				}
				r.Inferior = map[string]any{}
				if raw, readErr := os.ReadFile(status); readErr == nil {
					if err = json.Unmarshal(raw, &r.Inferior); err != nil {
						return r, err
					}
				}
				code, hasCode := r.Inferior["exit_code"].(float64)
				r.Mode = mode
				r.Passed = r.ReturnCode == 0 && !r.TimedOut && r.Inferior["stopped"] != true && hasCode && code == 0
				return r, nil
			}()
			if err != nil {
				return err
			}
			results = append(results, r)
			if r.Passed {
				passed++
			}
			if err = save(filepath.Join(batch, "summary.json"), map[string]any{"runs": results, "passed": passed, "failed": len(results) - passed}); err != nil {
				return err
			}
			fmt.Printf("%s/%s/%d: %s\n", variant, mode, number, map[bool]string{true: "PASS", false: "FAIL"}[r.Passed])
			if !r.Passed {
				return errors.New("test failed; batch stopped, inspect retained evidence")
			}
		}
	}
	return nil
}

func mainRun() error {
	if len(os.Args) < 3 {
		return errors.New("usage: weather-qt-diagnose build|run OUTPUT [--repeat 3 --variant optimized --modes fixture-terminate,orderly,kill-disconnect]")
	}
	action := os.Args[1]
	if action != "build" && action != "run" {
		return errors.New("action must be build or run")
	}
	flags := flag.NewFlagSet("weather-qt-diagnose", flag.ContinueOnError)
	variant := flags.String("variant", "optimized", "optimized or sanitized")
	repeat := flags.Int("repeat", 3, "repetitions (1..10)")
	timeout := flags.Int("timeout", 45, "deadline per process in seconds (15..300)")
	loop := flags.String("render-loop", "default", "default, basic or threaded")
	perturb := flags.Int("perturb", 0, "allocator perturbation (1..255; zero disables)")
	trace := flags.Bool("trace-transport", false, "capture transport stacks and allocation evidence")
	modeFlag := flags.String("modes", "fixture-terminate,orderly,kill-disconnect", "comma-separated teardown data rows")
	if err := flags.Parse(os.Args[3:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*variant != "optimized" && *variant != "sanitized") || *repeat < 1 || *repeat > 10 || *timeout < 15 || *timeout > 300 || *perturb < 0 || *perturb > 255 || (*loop != "default" && *loop != "basic" && *loop != "threaded") {
		return errors.New("invalid experiment options")
	}
	modes := strings.Split(*modeFlag, ",")
	for _, mode := range modes {
		if mode != "fixture-terminate" && mode != "orderly" && mode != "kill-disconnect" {
			return errors.New("invalid teardown mode")
		}
	}
	root, err := output("", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(os.Args[2])
	if err != nil {
		return err
	}
	if strings.ContainsAny(root+out, " \t\n\r\"\\") {
		return errors.New("qmake paths must not contain whitespace, quotes or backslashes")
	}
	if action == "build" && (out == root || !strings.HasPrefix(filepath.Dir(out), "/")) {
		return errors.New("invalid evidence output directory")
	}
	syscall.Umask(0077)
	if err = syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
	if errno != 0 {
		return errno
	} // PR_SET_CHILD_SUBREAPER
	defer cleanupChildren()
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	if action == "build" {
		return build(root, out)
	}
	return run(out, *variant, *loop, *repeat, *timeout, *perturb, modes, *trace)
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	experimentContext = ctx
	if err := mainRun(); err != nil {
		fmt.Fprintln(os.Stderr, "weather-qt-diagnose:", err)
		os.Exit(1)
	}
}
