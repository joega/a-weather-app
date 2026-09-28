package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
)

// The fixture is a compiled development executable with isolated XDG paths.
// Headless, offline operation never loads Qt, native assets or desktop config.
type cliFixture struct {
	root, exe string
	sequence  int
}
type cliCase struct {
	fixture                      *cliFixture
	root, state, runtime, socket string
	env                          []string
}
type privateOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *privateOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	keep := min(len(p), 65536-b.data.Len())
	if keep > 0 {
		b.data.Write(p[:keep])
	}
	return len(p), nil
}
func (b *privateOutput) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.data.String() }

type cliProcess struct {
	cmd            *exec.Cmd
	stdout, stderr *privateOutput
	done           chan struct{}
	err            error
	owner          *ownedPID
	children       []*ownedPID
}
type ownedPID struct {
	pid, fd int
	start   string
}

func compileCLI(t *testing.T) *cliFixture {
	t.Helper()
	root, e := os.MkdirTemp("/tmp", "weather-cli-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if e = os.Mkdir(filepath.Join(root, "build"), 0700); e != nil {
		t.Fatal(e)
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	source := filepath.Clean(filepath.Join(cwd, "../.."))
	fixture := &cliFixture{root: root, exe: filepath.Join(root, "build/a-weather-app")}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-o", fixture.exe, "./cmd/a-weather-app")
	build.Dir = source
	var log privateOutput
	build.Stdout = &log
	build.Stderr = &log
	if e = build.Run(); e != nil {
		t.Fatalf("compiled CLI fixture failed: %v\n%s", e, log.String())
	}
	return fixture
}
func (f *cliFixture) newCase(t *testing.T) *cliCase {
	t.Helper()
	f.sequence++
	root := filepath.Join(f.root, fmt.Sprintf("c%d", f.sequence))
	for _, dir := range []string{root, filepath.Join(root, "xdg"), filepath.Join(root, "home")} {
		if e := os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	state := filepath.Join(root, "state")
	hash := sha256.Sum256([]byte(state))
	runtimeDir := filepath.Join(root, "xdg", fmt.Sprintf("a-weather-app-%d-%s", os.Geteuid(), hex.EncodeToString(hash[:])[:16]))
	return &cliCase{fixture: f, root: root, state: state, runtime: runtimeDir, socket: filepath.Join(runtimeDir, "service.sock"), env: []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "TZ=UTC", "HOME=" + filepath.Join(root, "home"), "XDG_RUNTIME_DIR=" + filepath.Join(root, "xdg"), "XDG_STATE_HOME=" + filepath.Join(root, "default-state"), "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache")}}
}
func (c *cliCase) arguments(args ...string) []string {
	return append([]string{"--state-dir", c.state}, args...)
}
func (c *cliCase) run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.fixture.exe, c.arguments(args...)...)
	cmd.Env = c.env
	var stdout, stderr privateOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	e := cmd.Run()
	return stdout.String(), stderr.String(), e
}

func procStart(pid int) (string, int, error) {
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", 0, e
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return "", 0, errors.New("invalid owned process record")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return "", 0, errors.New("short owned process record")
	}
	parent, e := strconv.Atoi(fields[1])
	return fields[19], parent, e
}
func pinPID(pid, parent int, exe, role string) (*ownedPID, error) {
	start, actualParent, e := procStart(pid)
	if e != nil {
		return nil, e
	}
	if parent > 0 && actualParent != parent {
		return nil, errors.New("owned process parent changed")
	}
	info, e := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if e != nil {
		return nil, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("owned process UID mismatch")
	}
	actualExe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if e != nil || actualExe != exe {
		return nil, errors.New("owned process executable mismatch")
	}
	raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil {
		return nil, e
	}
	if role != "" {
		args := strings.Split(string(raw), "\x00")
		if len(args) < 2 || args[1] != role {
			return nil, errors.New("owned process role mismatch")
		}
	}
	fd, _, errno := syscall.Syscall(434, uintptr(pid), 0, 0)
	if errno != 0 {
		return nil, errno
	}
	p := &ownedPID{pid, int(fd), start}
	now, _, e := procStart(pid)
	if e != nil || now != start {
		syscall.Close(p.fd)
		return nil, errors.New("owned process identity changed")
	}
	return p, nil
}
func (p *ownedPID) alive() bool {
	poll := struct {
		fd              int32
		events, revents int16
	}{int32(p.fd), 1, 0}
	_, _, e := syscall.Syscall(syscall.SYS_POLL, uintptr(unsafe.Pointer(&poll)), 1, 0)
	return e == 0 && poll.revents == 0
}
func (p *ownedPID) signal(sig syscall.Signal) error {
	if !p.alive() {
		return nil
	}
	_, _, e := syscall.Syscall6(424, uintptr(p.fd), uintptr(sig), 0, 0, 0, 0)
	if e != 0 && e != syscall.ESRCH {
		return e
	}
	return nil
}
func (p *ownedPID) close() { syscall.Close(p.fd) }
func directChildren(pid int) ([]int, error) {
	tasks, e := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if e != nil {
		return nil, e
	}
	result := []int{}
	seen := map[int]bool{}
	// Go may launch a child from any runtime thread; children is per-task.
	for _, task := range tasks {
		raw, e := os.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task.Name()))
		if os.IsNotExist(e) {
			continue
		} // A thread exited during enumeration.
		if e != nil {
			return nil, e
		}
		for _, field := range strings.Fields(string(raw)) {
			n, e := strconv.Atoi(field)
			if e != nil {
				return nil, e
			}
			if !seen[n] {
				result = append(result, n)
				seen[n] = true
			}
		}
	}
	return result, nil
}

func (c *cliCase) start(t *testing.T, args ...string) *cliProcess {
	t.Helper()
	p := &cliProcess{cmd: exec.Command(c.fixture.exe, c.arguments(args...)...), stdout: &privateOutput{}, stderr: &privateOutput{}, done: make(chan struct{})}
	p.cmd.Env = c.env
	p.cmd.Stdout = p.stdout
	p.cmd.Stderr = p.stderr
	if e := p.cmd.Start(); e != nil {
		t.Fatal(e)
	}
	owner, e := pinPID(p.cmd.Process.Pid, os.Getpid(), c.fixture.exe, "")
	if e != nil {
		p.cmd.Process.Kill()
		p.cmd.Wait()
		t.Fatal(e)
	}
	p.owner = owner
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		p.owner.signal(syscall.SIGTERM)
		select {
		case <-p.done:
		case <-time.After(3 * time.Second):
			p.owner.signal(syscall.SIGKILL)
			select {
			case <-p.done:
			case <-time.After(2 * time.Second):
				t.Error("owned launcher did not exit")
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			allDead := true
			for _, child := range p.children {
				if child.alive() {
					allDead = false
				}
			}
			if allDead {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		// Failure cleanup only targets identities pinned from this exact process tree.
		for i := len(p.children) - 1; i >= 0; i-- {
			child := p.children[i]
			if child.alive() {
				child.signal(syscall.SIGKILL)
				t.Error("owned descendant needed fixture cleanup")
			}
			child.close()
		}
		p.owner.close()
	})
	return p
}
func waitUntil(t *testing.T, duration time.Duration, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(description)
}
func (c *cliCase) ready(t *testing.T, p *cliProcess) {
	t.Helper()
	waitUntil(t, 5*time.Second, "owned service did not publish a valid offline snapshot", func() bool {
		select {
		case <-p.done:
			return false
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		reply, e := ipc.Call(ctx, c.socket, M{"op": "snapshot"})
		if e != nil || reply["ok"] != true {
			return false
		}
		snapshot, ok := reply["snapshot"].(map[string]any)
		return ok && snapshot["schema_version"] == 1.0 && snapshot["current"] == nil
	})
	for _, name := range []string{c.state, c.runtime} {
		info, e := os.Stat(name)
		if e != nil || info.Mode().Perm() != 0700 {
			t.Fatal("private fixture directory permissions changed")
		}
	}
	info, e := os.Lstat(c.socket)
	if e != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("owned socket permissions changed")
	}
}
func (c *cliCase) tree(t *testing.T, p *cliProcess) (guardian, service *ownedPID) {
	t.Helper()
	waitUntil(t, 3*time.Second, "owned guardian/service tree unavailable", func() bool {
		children, e := directChildren(p.owner.pid)
		if e != nil {
			return false
		}
		for _, pid := range children {
			guard, e := pinPID(pid, p.owner.pid, c.fixture.exe, "--guardian")
			if e != nil {
				continue
			}
			services, e := directChildren(pid)
			if e != nil {
				guard.close()
				continue
			}
			for _, servicePID := range services {
				worker, e := pinPID(servicePID, pid, c.fixture.exe, "--service")
				if e == nil {
					guardian, service = guard, worker
					p.children = append(p.children, guard, worker)
					return true
				}
			}
			guard.close()
		}
		return false
	})
	return guardian, service
}
func waitProcess(t *testing.T, p *cliProcess, success bool) {
	t.Helper()
	select {
	case <-p.done:
		if (p.err == nil) != success {
			t.Fatalf("unexpected owned launcher result: %v; stderr=%q", p.err, p.stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("owned launcher exceeded bounded completion")
	}
}
func assertPrivateOutput(t *testing.T, c *cliCase, stdout, stderr string) {
	t.Helper()
	if strings.Contains(stdout, c.root) || strings.Contains(stderr, c.root) || strings.Contains(stdout, c.fixture.root) || strings.Contains(stderr, c.fixture.root) {
		t.Fatal("CLI exposed private fixture paths")
	}
	if len(stdout) > 65536 || len(stderr) > 65536 {
		t.Fatal("CLI diagnostics exceeded fixture bound")
	}
}
func (c *cliCase) clean(t *testing.T, guardian, service *ownedPID) {
	t.Helper()
	waitUntil(t, 8*time.Second, "owned service/guardian survived cleanup", func() bool { return !guardian.alive() && !service.alive() })
	if _, e := os.Lstat(c.socket); !os.IsNotExist(e) {
		t.Fatal("clean service exit retained socket")
	}
}

func TestCLIEndToEnd(t *testing.T) {
	fixture := compileCLI(t)
	t.Run("bar_does_not_create_state", func(t *testing.T) {
		c := fixture.newCase(t)
		stdout, stderr, e := c.run(t, "--bar")
		if e != nil {
			t.Fatal("cached bar command failed", e)
		}
		value, e := safeio.Object([]byte(stdout), 4096)
		if e != nil || value["label"] != "--° · Unavailable" || value["freshness"] != "unavailable" {
			t.Fatal("invalid empty cached bar reply")
		}
		assertPrivateOutput(t, c, stdout, stderr)
		if stderr != "" {
			t.Fatal("bar emitted diagnostics")
		}
		for _, name := range []string{c.state, c.runtime} {
			if _, e := os.Lstat(name); !os.IsNotExist(e) {
				t.Fatal("bar created state or runtime directory")
			}
		}
	})
	t.Run("fresh_finite_start_and_cleanup", func(t *testing.T) {
		c := fixture.newCase(t)
		p := c.start(t, "--headless", "--offline", "--duration", "1")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		waitProcess(t, p, true)
		c.clean(t, guardian, service)
		assertPrivateOutput(t, c, p.stdout.String(), p.stderr.String())
		if p.stdout.String() != "" || p.stderr.String() != "" {
			t.Fatal("successful forecast-only CLI emitted diagnostics")
		}
	})
	t.Run("reuse_existing_and_quit", func(t *testing.T) {
		c := fixture.newCase(t)
		p := c.start(t, "--headless", "--offline")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		before, _ := os.Lstat(c.socket)
		stdout, stderr, e := c.run(t, "--toggle-window", "--headless", "--offline")
		if e != nil {
			t.Fatal("second launcher did not reuse service", e)
		}
		after, _ := os.Lstat(c.socket)
		if !os.SameFile(before, after) || !service.alive() {
			t.Fatal("second launcher replaced owned service")
		}
		children, e := directChildren(p.owner.pid)
		if e != nil || len(children) != 1 || children[0] != guardian.pid {
			t.Fatal("second launcher duplicated guardian")
		}
		assertPrivateOutput(t, c, stdout, stderr)
		stdout, stderr, e = c.run(t, "--quit")
		if e != nil {
			t.Fatal("owned quit request failed", e)
		}
		assertPrivateOutput(t, c, stdout, stderr)
		waitProcess(t, p, true)
		c.clean(t, guardian, service)
	})
	t.Run("toggle_restarts_after_clean_quit", func(t *testing.T) {
		c := fixture.newCase(t)
		p := c.start(t, "--toggle-window", "--headless", "--offline")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		if _, _, e := c.run(t, "--quit"); e != nil {
			t.Fatal("owned clean quit failed", e)
		}
		waitProcess(t, p, true)
		c.clean(t, guardian, service)
		if _, e := os.Stat(c.runtime); e != nil {
			t.Fatal("expected retained private runtime directory", e)
		}
		if _, e := os.Lstat(c.socket); !os.IsNotExist(e) {
			t.Fatal("clean quit did not remove its socket", e)
		}
		replacement := c.start(t, "--toggle-window", "--headless", "--offline", "--duration", "1")
		c.ready(t, replacement)
		newGuardian, newService := c.tree(t, replacement)
		waitProcess(t, replacement, true)
		c.clean(t, newGuardian, newService)
		assertPrivateOutput(t, c, replacement.stdout.String(), replacement.stderr.String())
	})
	t.Run("recover_trusted_stale_socket", func(t *testing.T) {
		c := fixture.newCase(t)
		if e := os.MkdirAll(c.runtime, 0700); e != nil {
			t.Fatal(e)
		}
		l, e := net.ListenUnix("unix", &net.UnixAddr{Name: c.socket, Net: "unix"})
		if e != nil {
			t.Fatal(e)
		}
		l.SetUnlinkOnClose(false)
		os.Chmod(c.socket, 0600)
		l.Close()
		stale, _ := os.Lstat(c.socket)
		p := c.start(t, "--toggle-window", "--headless", "--offline", "--duration", "1")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		current, _ := os.Lstat(c.socket)
		if os.SameFile(stale, current) {
			t.Fatal("trusted stale socket not replaced under service lock")
		}
		waitProcess(t, p, true)
		c.clean(t, guardian, service)
		assertPrivateOutput(t, c, p.stdout.String(), p.stderr.String())
	})
	t.Run("guardian_cleans_after_launcher_sigkill", func(t *testing.T) {
		c := fixture.newCase(t)
		p := c.start(t, "--headless", "--offline")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		if e := p.owner.signal(syscall.SIGKILL); e != nil {
			t.Fatal(e)
		}
		waitProcess(t, p, false)
		c.clean(t, guardian, service)
		assertPrivateOutput(t, c, p.stdout.String(), p.stderr.String())
	})
	t.Run("service_crash_then_bar_and_toggle_recovery", func(t *testing.T) {
		c := fixture.newCase(t)
		p := c.start(t, "--headless", "--offline")
		c.ready(t, p)
		guardian, service := c.tree(t, p)
		before, _ := os.Lstat(c.socket)
		if e := service.signal(syscall.SIGKILL); e != nil {
			t.Fatal(e)
		}
		waitProcess(t, p, false)
		waitUntil(t, 8*time.Second, "crashed owned process group survived cleanup", func() bool { return !guardian.alive() && !service.alive() })
		stale, e := os.Lstat(c.socket)
		if e != nil || !os.SameFile(before, stale) {
			t.Fatal("service crash did not preserve expected stale socket")
		}
		stdout, stderr, e := c.run(t, "--bar")
		if e != nil {
			t.Fatal("cached bar unavailable after crash", e)
		}
		value, e := safeio.Object([]byte(stdout), 4096)
		if e != nil || value["freshness"] != "unavailable" {
			t.Fatal("invalid cached bar after crash")
		}
		assertPrivateOutput(t, c, stdout, stderr)
		afterBar, _ := os.Lstat(c.socket)
		if !os.SameFile(stale, afterBar) {
			t.Fatal("bar mutated stale runtime socket")
		}
		replacement := c.start(t, "--toggle-window", "--headless", "--offline", "--duration", "1")
		c.ready(t, replacement)
		newGuardian, newService := c.tree(t, replacement)
		if newService.pid == service.pid && newService.start == service.start {
			t.Fatal("replacement adopted crashed process identity")
		}
		waitProcess(t, replacement, true)
		c.clean(t, newGuardian, newService)
		assertPrivateOutput(t, c, p.stdout.String(), p.stderr.String())
		assertPrivateOutput(t, c, replacement.stdout.String(), replacement.stderr.String())
	})
}
