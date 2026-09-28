package effects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/ipc"
)

// These fixed observational requests use Hyprland's documented command socket
// directly. Keep mutations on the inventory/generation-checked command path.
func compositorReadRequest(args []string) (string, bool) {
	if len(args) == 1 && args[0] == "clients" {
		return "j/clients", true
	}
	if len(args) == 2 {
		switch {
		case args[0] == "monitors" && args[1] == "all":
			return "j/monitors all", true
		case args[0] == "plugin" && args[1] == "list":
			return "j/plugin list", true
		case args[0] == "a-weather-app:rain" && args[1] == "status":
			return "j/a-weather-app:rain status", true
		}
	}
	return "", false
}

const compositorReplyLimit = 2 * 1024 * 1024

// Authentication finishes before any request bytes are sent. A held directory
// descriptor anchors path resolution; SO_PEERCRED binds the connection to the
// captured compositor, and verify rechecks its starttime/executable after dial.
func compositorQuery(ctx context.Context, directory string, pid int, request string, verify func() error) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := openDirectory(directory)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	if err = socketAt(fd, ".socket.sock"); err != nil {
		return nil, err
	}
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", fmt.Sprintf("/proc/self/fd/%d/.socket.sock", fd))
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	peer, err := ipc.PeerPID(conn.(*net.UnixConn))
	if err != nil || peer != pid {
		return nil, errors.New("compositor socket peer changed")
	}
	if err = verify(); err != nil {
		return nil, err
	}
	for remaining := []byte(request); len(remaining) > 0; {
		n, err := conn.Write(remaining)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrShortWrite
		}
		remaining = remaining[n:]
	}
	raw, err := io.ReadAll(io.LimitReader(conn, compositorReplyLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > compositorReplyLimit {
		return nil, errors.New("compositor reply byte limit")
	}
	return raw, nil
}
