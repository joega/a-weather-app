package supervision

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestGuardianHelper(t *testing.T) {
	if os.Getenv("GO_WEATHER_GUARDIAN_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(RunGuardian(os.Args[i+1:]))
		}
	}
	os.Exit(99)
}
func TestGuardianOwnerEOFAndFiniteDuration(t *testing.T) {
	for _, duration := range []string{"0", "0.1"} {
		t.Run(duration, func(t *testing.T) {
			reader, writer, e := os.Pipe()
			if e != nil {
				t.Fatal(e)
			}
			defer reader.Close()
			defer writer.Close()
			state := t.TempDir()
			os.Chmod(state, 0700)
			directory, e := os.Open(state)
			if e != nil {
				t.Fatal(e)
			}
			defer directory.Close()
			name := ".guardian-" + strings.Repeat("a", 32) + ".log"
			log, e := os.OpenFile(filepath.Join(state, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if e != nil {
				t.Fatal(e)
			}
			defer log.Close()
			command := exec.Command(os.Args[0], "-test.run=^TestGuardianHelper$", "--", "--owner-fd", "3", "--diagnostic-dir-fd", "4", "--diagnostic-name", name, "--duration", duration, "--", "/bin/sleep", "2")
			command.Env = append(os.Environ(), "GO_WEATHER_GUARDIAN_HELPER=1")
			command.ExtraFiles = []*os.File{reader, directory}
			command.Stderr = log
			command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if e = command.Start(); e != nil {
				t.Fatal(e)
			}
			reader.Close()
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			if duration == "0" {
				time.Sleep(100 * time.Millisecond)
				writer.Close()
			}
			select {
			case e = <-done:
				if e != nil {
					raw, _ := os.ReadFile(filepath.Join(state, "guardian-last-error.log"))
					t.Fatal(e, string(raw))
				}
			case <-time.After(5 * time.Second):
				command.Process.Kill()
				<-done
				t.Fatal("guardian did not clean owned command")
			}
			if _, e = os.Stat(filepath.Join(state, name)); !os.IsNotExist(e) {
				t.Fatal("successful diagnostic not removed", e)
			}
		})
	}
}
func TestGuardianRejectsInvalidSelectors(t *testing.T) {
	for _, args := range [][]string{{"--owner-fd", "2", "--", "/bin/true"}, {"--owner-fd", "3", "--duration", "NaN", "--", "/bin/true"}, {"--owner-fd", "3", "--duration", "+Inf", "--", "/bin/true"}, {"--owner-fd", "3", "--", "relative"}} {
		if code := RunGuardian(args); code != 2 {
			t.Fatal(args, code)
		}
	}
}

func TestGuardianRetainsOwnedServiceDiagnosticOnFailure(t *testing.T) {
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	defer writer.Close()
	state := t.TempDir()
	os.Chmod(state, 0700)
	directory, e := os.Open(state)
	if e != nil {
		t.Fatal(e)
	}
	defer directory.Close()
	name := ".guardian-" + strings.Repeat("b", 32) + ".log"
	log, e := os.OpenFile(filepath.Join(state, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestGuardianHelper$", "--", "--owner-fd", "3", "--diagnostic-dir-fd", "4", "--diagnostic-name", name, "--", "/bin/sh", "-c", "echo owned-service-diagnostic >&2; exit 9")
	command.Env = append(os.Environ(), "GO_WEATHER_GUARDIAN_HELPER=1")
	command.ExtraFiles = []*os.File{reader, directory}
	command.Stderr = log
	if e = command.Run(); e == nil {
		t.Fatal("service failure hidden")
	}
	raw, e := os.ReadFile(filepath.Join(state, "guardian-last-error.log"))
	if e != nil || !strings.Contains(string(raw), "owned-service-diagnostic") {
		t.Fatal("owned service diagnostic discarded", string(raw), e)
	}
	info, e := os.Stat(filepath.Join(state, "guardian-last-error.log"))
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("diagnostic is not private", e)
	}
}

func TestGuardianCapsAndDrainsFloodWithoutChangingChildExit(t *testing.T) {
	reader, writer, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	defer writer.Close()
	state := t.TempDir()
	os.Chmod(state, 0700)
	directory, e := os.Open(state)
	if e != nil {
		t.Fatal(e)
	}
	defer directory.Close()
	name := ".guardian-" + strings.Repeat("c", 32) + ".log"
	log, e := os.OpenFile(filepath.Join(state, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGuardianHelper$", "--", "--owner-fd", "3", "--diagnostic-dir-fd", "4", "--diagnostic-name", name, "--", "/bin/sh", "-c", "head -c 1048576 /dev/zero >&2; exit 9")
	command.Env = append(os.Environ(), "GO_WEATHER_GUARDIAN_HELPER=1")
	command.ExtraFiles = []*os.File{reader, directory}
	command.Stderr = log
	if e = command.Run(); e == nil || ctx.Err() != nil {
		t.Fatal("flood blocked or hid service failure", e, ctx.Err())
	}
	raw, e := os.ReadFile(filepath.Join(state, "guardian-last-error.log"))
	if e != nil || len(raw) < GuardianDiagnosticLimit || len(raw) > GuardianDiagnosticLimit+2048 || !strings.Contains(string(raw), "exit status 9") {
		t.Fatal("diagnostic cap changed original exit or lost prefix", len(raw), e)
	}
}
