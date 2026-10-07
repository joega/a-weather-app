package main

import (
	"github.com/joega/a-weather-app/internal/safeio"
	"net"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestBootstrapRequiresPackagedWorker(t *testing.T) {
	for _, args := range [][]string{{"--bootstrap-update"}, {"--bootstrap-update", "--update-worker"}} {
		if err := launch(args); err == nil {
			t.Fatal("development or non-worker bootstrap accepted", args)
		}
	}
}

func TestServiceWaitsForBarRefreshLock(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := safeio.OpenDir(root, false)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	marker, err := dir.Lock("bar-refresh.lock")
	if err != nil {
		t.Fatal(err)
	}
	service, err := dir.Lock("service.lock")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		lock, err := serviceLock(dir)
		if lock != nil {
			lock.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("service did not wait for bar refresh: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	service.Close()
	marker.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not acquire lock after bar refresh")
	}
}

func TestInvalidCLIInputsDoNotCreateState(t *testing.T) {
	for _, args := range [][]string{
		{"--duration", "0"}, {"--duration", "-1"}, {"--duration", "3601"},
		{"--zip-code", "../../escape"}, {"--zip-code", "１２３４５"},
		{"--zip-code", "02115", "--offline"}, {"--demo-location", "invalid"},
		{"--demo-location", "boston", "--zip-code", "02115"},
		{"--instance", "bad\nsignature"}, {"--output", "--evil"},
		{"--state-dir", "/tmp/../tmp/bad"}, {"unexpected"},
	} {
		t.Run(args[0]+args[len(args)-1], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("XDG_STATE_HOME", filepath.Join(root, "absent"))
			if e := launch(args); e == nil {
				t.Fatal("accepted invalid arguments")
			}
			if _, e := os.Stat(filepath.Join(root, "absent")); !os.IsNotExist(e) {
				t.Fatal("created state before validation", e)
			}
		})
	}
}

func TestDemoPreservesExistingLocation(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	d, e := safeio.OpenDir(root, false)
	if e != nil {
		t.Fatal(e)
	}
	defer d.Close()
	if e = prepareLocation(d, "", "boston"); e != nil {
		t.Fatal(e)
	}
	first, e := os.ReadFile(filepath.Join(root, "location.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = prepareLocation(d, "", "boston"); e != nil {
		t.Fatal(e)
	}
	second, _ := os.ReadFile(filepath.Join(root, "location.json"))
	if string(first) != string(second) {
		t.Fatal("repeat changed state")
	}
	other := M{"name": "Other", "latitude": 1.0, "longitude": 2.0, "timezone": "UTC"}
	if e = d.Write("location.json", other, 8192); e != nil {
		t.Fatal(e)
	}
	if e = prepareLocation(d, "", "boston"); e == nil {
		t.Fatal("relabeled another location")
	}
	retained, _ := d.Read("location.json", 8192)
	if retained["name"] != "Other" {
		t.Fatal("changed saved location")
	}
}

func TestChildEnvironmentRejectsInjection(t *testing.T) {
	for _, name := range []string{"LD_PRELOAD", "PYTHONPATH", "QT_PLUGIN_PATH", "QML_IMPORT_PATH", "A_WEATHER_APP_INSTANCE"} {
		t.Setenv(name, "bad")
	}
	for _, entry := range childEnvironment() {
		if entry == "LD_PRELOAD=bad" || entry == "PYTHONPATH=bad" || entry == "QT_PLUGIN_PATH=bad" || entry == "QML_IMPORT_PATH=bad" || entry == "A_WEATHER_APP_INSTANCE=bad" {
			t.Fatal(entry)
		}
	}
}

func TestWeatherServiceRetainsNetworkConfiguration(t *testing.T) {
	for _, name := range []string{"HTTPS_PROXY", "http_proxy", "NO_PROXY", "SSL_CERT_FILE"} {
		t.Setenv(name, "explicit-value")
		if !slices.Contains(serviceEnvironment(), name+"=explicit-value") {
			t.Fatal("service lost", name)
		}
		if slices.Contains(childEnvironment(), name+"=explicit-value") {
			t.Fatal("unrelated native child inherited", name)
		}
	}
}

func TestFrontendRetainsDesktopInputMethod(t *testing.T) {
	for _, module := range []string{"fcitx", "ibus", "none"} {
		t.Setenv("QT_IM_MODULE", module)
		if !slices.Contains(childEnvironment(), "QT_IM_MODULE="+module) {
			t.Fatal("frontend lost configured input method", module)
		}
	}
}

func TestIPCRecoveryRefusesUntrustedOrAmbiguousFailure(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	path := filepath.Join(root, "service.sock")
	if !mayStartAfterIPCError(path, os.ErrNotExist) {
		t.Fatal("missing socket should start")
	}
	// A clean service shutdown leaves its private directory but removes the
	// socket. Dial reports this through net.OpError, not a bare PathError.
	_, dialErr := net.Dial("unix", path)
	if dialErr == nil || !mayStartAfterIPCError(path, dialErr) {
		t.Fatal("wrapped missing socket should start", dialErr)
	}
	if mayStartAfterIPCError(path, syscall.ETIMEDOUT) {
		t.Fatal("ambiguous timeout must not start")
	}
	for _, failure := range []error{syscall.ETIMEDOUT, syscall.EACCES, syscall.EPERM, syscall.ELOOP} {
		wrapped := &net.OpError{Op: "dial", Net: "unix", Err: &os.SyscallError{Syscall: "connect", Err: failure}}
		if mayStartAfterIPCError(path, wrapped) {
			t.Fatal("wrapped ambiguous or unsafe failure must not start", wrapped)
		}
	}
	os.WriteFile(path, []byte("not a socket"), 0600)
	if mayStartAfterIPCError(path, syscall.ECONNREFUSED) {
		t.Fatal("non-socket must not start")
	}
}

func TestInstalledDirectoryNamedBuildIsNotDevelopmentRoot(t *testing.T) {
	path := "/tmp/owned/build/a-weather-app"
	if got := executableRoot(path, "package"); got != "/tmp/owned/build" {
		t.Fatal(got)
	}
	if got := executableRoot(path, "development"); got != "/tmp/owned" {
		t.Fatal(got)
	}
	if got := executableRoot("/tmp/runtime/a-weather-app", "package"); got != "/tmp/runtime" {
		t.Fatal(got)
	}
}
