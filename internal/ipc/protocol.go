// Package ipc defines the bounded local-only Go/Qt protocol.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/joega/a-weather-app/internal/safeio"
	"net"
	"os"
	"syscall"
	"time"
)

const RequestLimit = 8192
const ResponseLimit = 262144

type M = map[string]any

func Peer(conn *net.UnixConn) error {
	_, e := PeerPID(conn)
	return e
}

func PeerPID(conn *net.UnixConn) (int, error) {
	raw, e := conn.SyscallConn()
	if e != nil {
		return 0, e
	}
	var inner error
	pid := 0
	e = raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if err != nil {
			inner = err
		} else if cred.Uid != uint32(os.Geteuid()) {
			inner = errors.New("peer owner")
		} else if cred.Pid <= 1 {
			inner = errors.New("peer PID")
		} else {
			pid = int(cred.Pid)
		}
	})
	if e != nil {
		return 0, e
	}
	return pid, inner
}
func Send(conn net.Conn, v any, limit int) error {
	b, e := Encode(v, limit)
	if e != nil {
		return e
	}
	return WriteEncoded(conn, b)
}
func Encode(v any, limit int) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(b) > limit {
		return nil, errors.New("IPC message too large")
	}
	return append(b, '\n'), nil
}

// WriteEncoded consumes immutable framed bytes. Each connection has one writer.
func WriteEncoded(conn net.Conn, b []byte) error {
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	for len(b) > 0 {
		n, e := conn.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 {
			return errors.New("IPC short write")
		}
		b = b[n:]
	}
	return nil
}
func Scanner(conn net.Conn, limit int) *bufio.Scanner {
	s := bufio.NewScanner(conn)
	s.Buffer(make([]byte, 4096), limit+2)
	return s
}
func Call(ctx context.Context, path string, request M) (M, error) {
	return CallVerified(ctx, path, request, nil)
}

// CallVerified authenticates the Unix peer before sending any request. The
// verifier receives SO_PEERCRED's PID and can bind it to a starttime/parent.
func CallVerified(ctx context.Context, path string, request M, verify func(int) error) (M, error) {
	d := net.Dialer{}
	conn, e := d.DialContext(ctx, "unix", path)
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	stopCancellation := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopCancellation()
	pid, e := PeerPID(conn.(*net.UnixConn))
	if e != nil {
		return nil, e
	}
	if verify != nil {
		if e = verify(pid); e != nil {
			return nil, e
		}
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetReadDeadline(deadline)
	}
	request = safeio.Clone(request)
	request["version"] = float64(1)
	request["request_id"] = float64(1)
	if e = Send(conn, request, RequestLimit); e != nil {
		return nil, e
	}
	s := Scanner(conn, ResponseLimit)
	if !s.Scan() {
		if s.Err() != nil {
			return nil, s.Err()
		}
		return nil, errors.New("IPC closed")
	}
	reply, e := safeio.Object(s.Bytes(), ResponseLimit)
	if e != nil {
		return nil, e
	}
	if reply["version"] != float64(1) || reply["request_id"] != float64(1) {
		return nil, errors.New("IPC reply mismatch")
	}
	if _, ok := reply["ok"].(bool); !ok {
		return nil, errors.New("IPC reply status")
	}
	return reply, nil
}
