package app

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestServePreservesUntrustedSocketTarget(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "service.sock")
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		t.Fatal(e)
	}
	listener.SetUnlinkOnClose(false)
	if e = listener.Close(); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(socket, 0666); e != nil {
		t.Fatal(e)
	}
	before, e := os.Lstat(socket)
	if e != nil {
		t.Fatal(e)
	}
	if e = Serve(context.Background(), socket, nil, nil); e == nil {
		t.Fatal("replaced unsafe socket")
	}
	after, e := os.Lstat(socket)
	if e != nil || !os.SameFile(before, after) || after.Mode().Perm() != 0666 {
		t.Fatal("unsafe socket was modified", e)
	}
}
