package supervision

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Short native queries are frequent while effects run. Include supervision and
// cleanup costs, not only the child executable, in performance diagnostics.
func BenchmarkRunShortCommand(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Run(context.Background(), []string{"/usr/bin/true"}, []string{"PATH=/usr/bin:/bin"}, 8192); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRunBoundedStreamsAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name, script string
		limit        int
		timeout      time.Duration
	}{{"stdout", "yes x", 64, time.Second}, {"stderr", "yes x >&2", 1024, time.Second}, {"deadline", "sleep 30", 1024, 100 * time.Millisecond}, {"descendant pipe", "sleep 30 & exit 0", 1024, 100 * time.Millisecond}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), test.timeout)
			defer cancel()
			start := time.Now()
			if _, e := Run(ctx, []string{"/bin/sh", "-c", test.script}, os.Environ(), test.limit); e == nil {
				t.Fatal("accepted unbounded process")
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("deadline exceeded")
			}
		})
	}
	data, e := Run(context.Background(), []string{"/usr/bin/printf", "valid"}, os.Environ(), 1024)
	if e != nil || string(data) != "valid" {
		t.Fatal(string(data), e)
	}
}
func TestGroupLeaderStaysAnchoredUntilCleanup(t *testing.T) {
	p, e := Start([]string{"/bin/sh", "-c", "sleep 30 & exit 0"}, os.Environ(), "", nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for p.Alive() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	raw, e := os.ReadFile("/proc/" + strconv.Itoa(p.PID()) + "/stat")
	if e != nil || !strings.Contains(string(raw), ") Z ") {
		t.Fatal("leader PID anchor not retained", string(raw), e)
	}
	if p.Cmd.ProcessState != nil {
		t.Fatal("leader reaped during observation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e = p.Cleanup(ctx, time.Second); e != nil {
		t.Fatal(e)
	}
	live, e := members(p.PID())
	if e != nil || len(live) > 0 {
		t.Fatal("descendant survived cleanup", live, e)
	}
}

func TestExitNotificationDoesNotReapAndSupportsConcurrentWaiters(t *testing.T) {
	p, e := Start([]string{"/bin/sh", "-c", "sleep 0.1; exit 0"}, os.Environ(), "", nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Cleanup(context.Background(), 0)
	if p.pidfd < 0 {
		t.Fatal("test requires Linux pidfds")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if p.WaitExit(ctx) == nil {
		t.Fatal("live child signaled exit")
	}
	cancel()
	var clients sync.WaitGroup
	for i := 0; i < 8; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if e := p.WaitExit(ctx); e != nil {
				t.Error(e)
			}
		}()
	}
	clients.Wait()
	if p.Cmd.ProcessState != nil {
		t.Fatal("exit notification reaped the leader")
	}
	raw, e := os.ReadFile("/proc/" + strconv.Itoa(p.PID()) + "/stat")
	if e != nil || !strings.Contains(string(raw), ") Z ") {
		t.Fatal("exit notification lost PID anchor", string(raw), e)
	}
	if e = p.Cleanup(context.Background(), 0); e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("repeated exit notification not closed")
	}
}

func TestExitNotificationAfterAlreadyReapedChild(t *testing.T) {
	p, e := Start([]string{"/bin/sleep", "30"}, os.Environ(), "", nil, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Cleanup(context.Background(), time.Second); e != nil {
		t.Fatal(e)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("already-reaped process has no exit notification")
	}
}
