package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSendAndScanBounds(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	finished := make(chan error, 1)
	go func() { finished <- Send(a, M{"value": 1.0}, 100) }()
	scan := Scanner(b, 100)
	if !scan.Scan() || string(scan.Bytes()) != `{"value":1}` {
		t.Fatal("JSON line format", scan.Err())
	}
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	if e := Send(a, M{"value": strings.Repeat("x", 101)}, 100); e == nil {
		t.Fatal("oversize send accepted")
	}
	if e := Send(a, M{"value": make(chan int)}, 100); e == nil {
		t.Fatal("unencodable send accepted")
	}
	a.Close()
	if scan.Scan() {
		t.Fatal("unexpected extra record")
	}
}
func TestScannerOversize(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	go func() { defer a.Close(); a.Write([]byte(strings.Repeat("x", RequestLimit+10) + "\n")) }()
	s := Scanner(b, RequestLimit)
	if s.Scan() || s.Err() == nil {
		t.Fatal("unbounded request scanner")
	}
}

func unixListener(t *testing.T) (*net.UnixListener, string) {
	t.Helper()
	dir, e := os.MkdirTemp("/tmp", "weather-ipc-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, "socket")
	l, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	return l, path
}
func TestCallValidatesReplyAndCredentials(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply M
		valid bool
	}{{"valid", M{"version": 1.0, "request_id": 1.0, "ok": true}, true}, {"wrong_version", M{"version": 2.0, "request_id": 1.0, "ok": true}, false}, {"wrong_id", M{"version": 1.0, "request_id": 2.0, "ok": true}, false}, {"nonboolean_status", M{"version": 1.0, "request_id": 1.0, "ok": 1.0}, false}} {
		t.Run(c.name, func(t *testing.T) {
			l, path := unixListener(t)
			done := make(chan error, 1)
			go func() {
				conn, e := l.AcceptUnix()
				if e != nil {
					done <- e
					return
				}
				defer conn.Close()
				if e = Peer(conn); e != nil {
					done <- e
					return
				}
				line, e := bufio.NewReader(conn).ReadBytes('\n')
				if e != nil {
					done <- e
					return
				}
				var request M
				if e = json.Unmarshal(line, &request); e != nil {
					done <- e
					return
				}
				if request["version"] != 1.0 || request["request_id"] != 1.0 {
					done <- io.ErrUnexpectedEOF
					return
				}
				done <- Send(conn, c.reply, ResponseLimit)
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request := M{"op": "snapshot", "request_id": 99.0}
			verified := false
			reply, e := CallVerified(ctx, path, request, func(pid int) error {
				verified = true
				if pid != os.Getpid() {
					t.Errorf("peer pid %d, want %d", pid, os.Getpid())
				}
				return nil
			})
			if !verified {
				t.Fatal("peer verifier was not called")
			}
			if (e == nil) != c.valid {
				t.Fatalf("reply %v: %v", reply, e)
			}
			if request["request_id"] != 99.0 {
				t.Fatal("Call mutated request")
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestCallVerifiedRejectsReplacementPeerBeforeWriting(t *testing.T) {
	l, path := unixListener(t)
	read := make(chan error, 1)
	go func() {
		conn, e := l.AcceptUnix()
		if e != nil {
			read <- e
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, e = bufio.NewReader(conn).ReadByte()
		if e == nil {
			read <- errors.New("request reached rejected peer")
		} else {
			read <- nil
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, e := CallVerified(ctx, path, M{"op": "snapshot"}, func(pid int) error { return errors.New("socket peer PID mismatch") }); e == nil {
		t.Fatal("replacement peer accepted")
	}
	if e := <-read; e != nil {
		t.Fatal(e)
	}
}

func TestCallRejectsInvalidRequestBeforeWriting(t *testing.T) {
	for name, request := range map[string]M{"nil": nil, "unencodable": {"op": make(chan int)}} {
		t.Run(name, func(t *testing.T) {
			listener, path := unixListener(t)
			done := make(chan error, 1)
			go func() {
				conn, err := listener.AcceptUnix()
				if err != nil {
					done <- err
					return
				}
				defer conn.Close()
				if err = conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
					done <- err
					return
				}
				_, err = bufio.NewReader(conn).ReadByte()
				done <- err
			}()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := Call(ctx, path, request); err == nil {
				t.Fatal("invalid request accepted")
			}
			if err := <-done; !errors.Is(err, io.EOF) {
				t.Fatalf("rejected request reached peer or left connection open: %v", err)
			}
		})
	}
}
func TestCallRejectsMalformedAndOversizedReply(t *testing.T) {
	for _, raw := range []string{`{"version":1,"request_id":1,"ok":true,"ok":false}`, `[]`, strings.Repeat("x", ResponseLimit+10)} {
		l, path := unixListener(t)
		go func() {
			conn, e := l.AcceptUnix()
			if e != nil {
				return
			}
			defer conn.Close()
			bufio.NewReader(conn).ReadBytes('\n')
			conn.Write([]byte(raw + "\n"))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if _, e := Call(ctx, path, M{"op": "snapshot"}); e == nil {
			cancel()
			t.Fatal("malformed server reply accepted")
		}
		cancel()
		l.Close()
	}
}
func TestCallDeadline(t *testing.T) {
	l, path := unixListener(t)
	go func() {
		conn, e := l.AcceptUnix()
		if e != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := Call(ctx, path, M{"op": "snapshot"}); e == nil {
		t.Fatal("response deadline ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("deadline response too late")
	}
}
func TestCallCancellation(t *testing.T) {
	l, path := unixListener(t)
	accepted := make(chan *net.UnixConn, 1)
	go func() {
		conn, e := l.AcceptUnix()
		if e == nil {
			accepted <- conn
			bufio.NewReader(conn).ReadBytes('\n')
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, e := Call(ctx, path, M{"op": "snapshot"}); done <- e }()
	var conn *net.UnixConn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Call did not connect")
	}
	defer conn.Close()
	cancel()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("canceled call succeeded")
		}
	case <-time.After(150 * time.Millisecond):
		conn.Close()
		<-done
		t.Fatal("context cancellation did not interrupt response read")
	}
}
