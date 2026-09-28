package main

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/supervision"
)

// The test executable supplies only the guardian dispatch needed by
// GuardWithStarted. Its guarded command below is sleep, never the application
// service, compositor, effects worker, or a native renderer.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--guardian" {
		os.Exit(supervision.RunGuardian(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func TestRealGuardianStartupIdentity(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		t.Run(fmt.Sprintf("%02d", iteration), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			command := []string{"/usr/bin/sleep", "0.2"}
			a := &audit{serviceArgs: append([]string(nil), command...)}
			type launched struct {
				pid  int
				args []string
			}
			started := make(chan launched, 1)
			done := make(chan error, 1)
			finished := false
			defer func() {
				cancel() // Close the private owner pipe even on assertion failure.
				if !finished {
					select {
					case err := <-done:
						if err != nil {
							t.Errorf("owned startup guardian cleanup: %v", err)
						}
					case <-time.After(3 * time.Second):
						t.Error("owned startup guardian did not finish within cleanup bound")
					}
				}
			}()
			go func() {
				// Avoid race-runtime exit sleep multiplying this bounded startup
				// regression when the parent suite runs with -race.
				env := []string{"PATH=/usr/bin:/bin", "GORACE=atexit_sleep_ms=0"}
				done <- supervision.GuardWithStarted(ctx, command, env, "", time.Second, func(pid int, args []string) {
					started <- launched{pid, append([]string(nil), args...)}
				})
			}()

			var child launched
			select {
			case child = <-started:
			case err := <-done:
				finished = true
				t.Fatalf("guardian exited before reporting its launch: %v", err)
			case <-ctx.Done():
				t.Fatalf("guardian launch notification: %v", ctx.Err())
			}
			if err := a.bindGuardian(child.pid, child.args); err != nil {
				observed, inspectErr := inspectProcess(child.pid)
				t.Fatalf("bindGuardian: %v; expected parent=%d uid=%d arguments=%q; re-observed identity=%+v inspection_error=%v", err, os.Getpid(), os.Geteuid(), child.args, observed, inspectErr)
			}
			if a.guardianPID != child.pid || a.guardianStart == "" || !reflect.DeepEqual(a.guardianArgs, child.args) {
				t.Fatalf("guardian binding incomplete: pid=%d/%d starttime=%q arguments=%q/%q", a.guardianPID, child.pid, a.guardianStart, a.guardianArgs, child.args)
			}
			// Keep the owner pipe open until the real sleep child exits. This
			// exercises natural guardian completion, not its pre-spawn EOF path.
			select {
			case err := <-done:
				finished = true
				if err != nil {
					t.Fatalf("owned startup guardian: %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("owned sleep/guardian completion: %v", ctx.Err())
			}
		})
		if t.Failed() {
			return // One detailed failure is sufficient; do not spawn more children.
		}
	}
}
