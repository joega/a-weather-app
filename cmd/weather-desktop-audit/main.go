// weather-desktop-audit is an opt-in development acceptance harness, not part
// of desktop startup. It never unloads plugins or signals a bare/discovered PID.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/supervision"
)

type object = map[string]any

const auditBudget = 4 * time.Minute
const evidenceLimit = 32 * 1024 * 1024
const recordLimit = 3 * 1024 * 1024

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--guardian" {
		os.Exit(supervision.RunGuardian(os.Args[2:]))
	}
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "Desktop audit:", e)
		os.Exit(1)
	}
}

func run(args []string) (result error) {
	flags := flag.NewFlagSet("weather-desktop-audit", flag.ContinueOnError)
	rootFlag := flags.String("root", "", "absolute verified development checkout or installed package")
	outputFlag := flags.String("output", "", "selected enabled Hyprland output; required when more than one is connected")
	activate := flags.Bool("activate-native", false, "explicitly allow finite desktop-effects activation")
	crashWorker := flags.Bool("crash-worker", false, "instead audit one proven-owned worker crash and fresh-service restart")
	parent := flags.String("evidence-parent", os.TempDir(), "absolute parent for a fresh private evidence directory")
	if e := flags.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if flags.NArg() != 0 || !*activate || !filepath.IsAbs(*rootFlag) || !filepath.IsAbs(*parent) {
		return errors.New("require explicit --root /absolute/path --activate-native; no effects were activated")
	}
	root, e := filepath.EvalSymlinks(*rootFlag)
	if e != nil {
		return e
	}
	evidencePath, e := os.MkdirTemp(*parent, "weather-desktop-audit-")
	if e != nil {
		return e
	}
	fmt.Println("Private audit evidence:", evidencePath)
	directory, e := safeio.OpenDir(evidencePath, false)
	if e != nil {
		return e
	}
	defer directory.Close()
	a := &audit{root: root, output: *outputFlag, evidence: directory, state: filepath.Join(evidencePath, "state"), crashWorker: *crashWorker, processes: map[string]processIdentity{}, runtimeDirs: map[string]bool{}, retainedDirs: map[string]directoryEvidence{}}
	a.states = []string{a.state}
	defer func() {
		cleanup := a.cleanup()
		result = errors.Join(result, cleanup)
		liveSeconds := 35
		mode := "normal_lifecycle"
		if a.crashWorker {
			liveSeconds = 0
			mode = "fail_closed_recovery"
		}
		summary := object{"schema_version": 1, "finished_at": time.Now().UTC().Format(time.RFC3339Nano), "ok": result == nil, "cleanup_ok": cleanup == nil, "root": root, "state": a.state, "states": a.states, "evidence": evidencePath, "native_activation_requested": true, "live_seconds_required": liveSeconds, "mode": mode, "crash_tests_requested": a.crashWorker, "crash_tests_exercised": a.crashInjected, "fail_closed_recovery_verified": a.crashRecoveryVerified, "fresh_service_restart_verified": a.crashRestartVerified, "retained_diagnostics": a.retainedDirs, "rendering_performance_measured": false}
		summary["full_clean_stopped"] = !a.crashWorker && result == nil
		if result != nil {
			summary["error"] = result.Error()
		}
		result = errors.Join(result, directory.Write("result.json", summary, 65536))
	}()
	state, e := safeio.OpenDir(a.state, true)
	if e != nil {
		return e
	}
	state.Close()
	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signals, auditBudget)
	defer cancel()
	if e = a.preflight(ctx); e != nil {
		return e
	}
	if e = a.start(ctx); e != nil {
		return e
	}
	if a.crashWorker {
		return a.exerciseWorkerCrash(ctx)
	}
	return a.exercise(ctx)
}

func objectOf(v any) object  { result, _ := v.(map[string]any); return result }
func stringOf(v any) string  { result, _ := v.(string); return result }
func numberOf(v any) float64 { result, _ := v.(float64); return result }

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func environment() []string {
	result := []string{"PATH=/usr/bin:/bin", "GDK_BACKEND=wayland"}
	for _, name := range []string{"HOME", "LANG", "LC_ALL", "TZ", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "DBUS_SESSION_BUS_ADDRESS"} {
		if value, ok := os.LookupEnv(name); ok {
			result = append(result, name+"="+value)
		}
	}
	return result
}
