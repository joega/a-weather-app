package buildmeta

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeAuditFailureLogsAndExitStatus(t *testing.T) {
	raw, err := os.ReadFile("../../scripts/verify_go_runtime.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, tail, found := strings.Cut(string(raw), "cleanup_audit() {")
	if !found {
		t.Fatal("runtime cleanup handler missing")
	}
	body, _, found := strings.Cut(tail, "trap cleanup_audit EXIT")
	if !found {
		t.Fatal("runtime cleanup trap missing")
	}
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			dir, err := os.MkdirTemp(t.TempDir(), "runtime-audit-")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(dir, "state"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"runtime.log", "service.log", "exec.log", "service-exec.log", "state/guardian-last-error.log"} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte("retained-"+path+"\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			finish := "exit 0"
			if failure {
				finish = "exit 37"
			}
			script := "set -euo pipefail\naudit_root=$1\ncleanup_audit() {" + body + "trap cleanup_audit EXIT\n" + finish
			output, err := exec.Command("bash", "-c", script, "runtime-cleanup-test", dir).CombinedOutput()
			if failure {
				var status *exec.ExitError
				if !errors.As(err, &status) || status.ExitCode() != 37 {
					t.Fatalf("original exit status lost: %v\n%s", err, output)
				}
				for _, marker := range []string{"status 37", "retained-runtime.log", "retained-service.log", "retained-exec.log", "retained-service-exec.log", "retained-state/guardian-last-error.log"} {
					if !bytes.Contains(output, []byte(marker)) {
						t.Fatalf("failure log missing %q: %s", marker, output)
					}
				}
			} else if err != nil || len(output) != 0 {
				t.Fatalf("successful cleanup failed or leaked logs: %v\n%s", err, output)
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Fatalf("temporary audit directory survived cleanup: %v", err)
			}
		})
	}
}
