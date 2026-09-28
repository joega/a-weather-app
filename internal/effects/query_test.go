package effects

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

func queryListener(t *testing.T) (string, *net.UnixListener) {
	t.Helper()
	directory, err := os.MkdirTemp("", "weather-query-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(directory, ".socket.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return directory, listener
}

func TestCompositorReadRequestAllowlist(t *testing.T) {
	for _, args := range [][]string{{"clients"}, {"monitors", "all"}, {"plugin", "list"}, {"a-weather-app:rain", "status"}} {
		if got, ok := compositorReadRequest(args); !ok || got != "j/"+strings.Join(args, " ") {
			t.Fatal(args, got, ok)
		}
	}
	for _, args := range [][]string{nil, {"clients", "extra"}, {"plugin", "load"}, {"plugin", "unload"}, {"a-weather-app:rain", "off"}, {"a-weather-app:rain", "guard 7 off"}, {"a-weather-app:rain", "status\nplugin unload"}} {
		if request, ok := compositorReadRequest(args); ok {
			t.Fatal("non-observational request entered direct path", args, request)
		}
	}
}

func TestCompositorQueryAuthenticatesBeforeWriting(t *testing.T) {
	for _, failure := range []string{"", "peer", "identity"} {
		t.Run(failure, func(t *testing.T) {
			directory, listener := queryListener(t)
			received := make(chan string, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					received <- "accept failed"
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				raw := make([]byte, len("j/clients"))
				n, _ := io.ReadFull(conn, raw)
				received <- string(raw[:n])
				if n > 0 {
					conn.Write([]byte(`[{"id":1}]`))
				}
			}()
			pid := os.Getpid()
			if failure == "peer" {
				pid++
			}
			verified := false
			raw, err := compositorQuery(context.Background(), directory, pid, "j/clients", func() error {
				verified = true
				if failure == "identity" {
					return errors.New("identity changed")
				}
				return nil
			})
			request := <-received
			if failure == "" {
				if err != nil || !verified || request != "j/clients" || string(raw) != `[{"id":1}]` {
					t.Fatal(string(raw), request, verified, err)
				}
			} else if err == nil || request != "" {
				t.Fatal("request sent before authentication", request, err)
			}
		})
	}
}

func TestCompositorQueryLimitsAndCancellation(t *testing.T) {
	for _, kind := range []string{"oversize", "deadline", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			directory, listener := queryListener(t)
			timeout := 100 * time.Millisecond
			if kind == "oversize" {
				timeout = 3 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.AcceptUnix()
				if err != nil {
					return
				}
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(time.Second))
				request := make([]byte, len("j/clients"))
				if _, err = io.ReadFull(conn, request); err != nil {
					return
				}
				if kind == "oversize" {
					io.WriteString(conn, strings.Repeat(" ", compositorReplyLimit+1))
				} else {
					if kind == "cancel" {
						cancel()
					}
					io.Copy(io.Discard, conn)
				}
			}()
			start := time.Now()
			_, err := compositorQuery(ctx, directory, os.Getpid(), "j/clients", func() error { return nil })
			if err == nil || time.Since(start) > time.Second {
				t.Fatal("unbounded compositor query", err, time.Since(start))
			}
			if kind == "oversize" && !strings.Contains(err.Error(), "byte limit") {
				t.Fatal("oversized reply was not rejected at the byte bound", err)
			}
			<-done
		})
	}
}

func TestCompositorQueryRejectsSymlink(t *testing.T) {
	directory := t.TempDir()
	if err := os.Symlink("/dev/null", filepath.Join(directory, ".socket.sock")); err != nil {
		t.Fatal(err)
	}
	if _, err := compositorQuery(context.Background(), directory, os.Getpid(), "j/clients", func() error { t.Fatal("symlink passed authentication"); return nil }); err == nil {
		t.Fatal("followed socket symlink")
	}
}

// Opt-in read-only comparison with the real compositor. It never loads a
// plugin, changes a window, or writes user configuration.
func TestCompositorQueryLiveParity(t *testing.T) {
	if os.Getenv("WEATHER_COMPOSITOR_QUERY_TEST") != "1" {
		t.Skip("opt-in compositor query parity")
	}
	instance, pid, _, err := discover("")
	if err != nil {
		t.Fatal(err)
	}
	b := newBackend("")
	b.instance, b.pid = instance, pid
	b.starttime, err = metadata(pid)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	direct, err := b.ctl(ctx, true, "plugin", "list")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := exec.CommandContext(ctx, "/usr/bin/hyprctl", "-i", instance, "-j", "plugin", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	cli, err := safeio.Decode(raw, compositorReplyLimit)
	if err != nil || !reflect.DeepEqual(direct, cli) {
		t.Fatal("direct IPC differs from hyprctl", err)
	}
	if _, err = b.ctl(ctx, true, "monitors", "all"); err != nil {
		t.Fatal(err)
	}
	if _, err = b.ctl(ctx, true, "clients"); err != nil {
		t.Fatal(err)
	}
}
