package app

import (
	"bufio"
	"context"
	"errors"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"github.com/joega/a-weather-app/internal/weather"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type coordinatedEffects struct {
	mu                             sync.Mutex
	state                          string
	persistent                     bool
	checks, starts, stops, selects int
	ticks                          []time.Time
	blockCheck                     chan struct{}
	checkStarted                   chan struct{}
	startOnce                      sync.Once
	active, maxActive              atomic.Int64
}

func (f *coordinatedEffects) enter() func() {
	n := f.active.Add(1)
	for {
		old := f.maxActive.Load()
		if n <= old || f.maxActive.CompareAndSwap(old, n) {
			break
		}
	}
	return func() { f.active.Add(-1) }
}
func (f *coordinatedEffects) SetupSnapshot() M {
	defer f.enter()()
	return M{"status": "unchecked", "reason": "not_checked", "outputs": []any{}, "selected_output": nil}
}
func (f *coordinatedEffects) Status() M {
	defer f.enter()()
	f.mu.Lock()
	defer f.mu.Unlock()
	state := f.state
	if state == "" {
		state = "stopped"
	}
	return M{"state": state, "persistent": f.persistent, "remaining_seconds": 30.0, "session_generation": 1.0, "error": nil}
}
func (f *coordinatedEffects) Check(ctx context.Context) M {
	defer f.enter()()
	f.mu.Lock()
	f.checks++
	f.mu.Unlock()
	if f.checkStarted != nil {
		f.startOnce.Do(func() { close(f.checkStarted) })
	}
	if f.blockCheck != nil {
		select {
		case <-f.blockCheck:
		case <-ctx.Done():
		}
	}
	return M{"status": "ready", "reason": "ready", "outputs": []any{}, "selected_output": "DP-1"}
}
func (f *coordinatedEffects) SelectOutput(context.Context, string) (M, error) {
	defer f.enter()()
	f.mu.Lock()
	f.selects++
	f.mu.Unlock()
	return M{}, nil
}
func (f *coordinatedEffects) Start(ctx context.Context, _ int, p bool, _ M) error {
	defer f.enter()()
	if e := ctx.Err(); e != nil {
		return e
	}
	f.mu.Lock()
	f.starts++
	f.state = "running"
	f.persistent = p
	f.mu.Unlock()
	return nil
}
func (f *coordinatedEffects) Tick(ctx context.Context, _ M, _ M) error {
	defer f.enter()()
	if e := ctx.Err(); e != nil {
		return e
	}
	f.mu.Lock()
	f.ticks = append(f.ticks, time.Now())
	f.mu.Unlock()
	return nil
}
func (f *coordinatedEffects) Stop(context.Context) error {
	defer f.enter()()
	f.mu.Lock()
	f.stops++
	f.state = "stopped"
	f.persistent = false
	f.mu.Unlock()
	return nil
}

func TestCancelledAdmissionCannotMutateState(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	a.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	done := make(chan M, 1)
	go func() { r, _ := a.Handle(ctx, request("set_controls", M{"controls": M{"units": "C"}})); done <- r }()
	select {
	case r := <-done:
		if r["error"] != "request_timeout" {
			t.Fatal(r)
		}
	case <-time.After(250 * time.Millisecond):
		a.mu.Unlock()
		t.Fatal("action admission ignored deadline")
	}
	start := time.Now()
	snapshot := a.Snapshot()
	if time.Since(start) > 100*time.Millisecond || snapshot["snapshot_revision"] == nil {
		a.mu.Unlock()
		t.Fatal("cached snapshot blocked")
	}
	closeCtx, c := context.WithTimeout(context.Background(), 25*time.Millisecond)
	e := a.Close(closeCtx)
	c()
	if !errors.Is(e, context.DeadlineExceeded) {
		a.mu.Unlock()
		t.Fatal(e)
	}
	a.mu.Unlock()
	if a.controls["units"] != "F" || a.closed {
		t.Fatal("canceled action changed state")
	}
	saved, e := a.state.Read("controls.json", 8192)
	if e != nil || saved != nil {
		t.Fatal("canceled mutation persisted", saved, e)
	}
}
func TestEffectsCheckDoesNotBlockSnapshotsOrSettings(t *testing.T) {
	fx := &coordinatedEffects{blockCheck: make(chan struct{}), checkStarted: make(chan struct{})}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	done := make(chan M, 1)
	go func() { reply, _ := a.Handle(context.Background(), request("check_effects", nil)); done <- reply }()
	select {
	case <-fx.checkStarted:
	case <-time.After(time.Second):
		t.Fatal("check did not start")
	}
	start := time.Now()
	snapshot := a.Snapshot()
	reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"units": "C"}}))
	if time.Since(start) > 250*time.Millisecond || snapshot["snapshot_revision"] == nil || reply["ok"] != true {
		t.Fatal("native check blocked app state", reply)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := a.Close(ctx); e != nil {
		t.Fatal(e)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel native check")
	}
	if fx.maxActive.Load() != 1 {
		t.Fatal("concurrent manager calls", fx.maxActive.Load())
	}
}
func TestExpiredQueuedEffectsActionsAreNeverExecuted(t *testing.T) {
	fx := &coordinatedEffects{blockCheck: make(chan struct{}), checkStarted: make(chan struct{})}
	a := newTestApp(t, Options{Offline: true, Effects: fx})
	first := make(chan M, 1)
	go func() { reply, _ := a.Handle(context.Background(), request("check_effects", nil)); first <- reply }()
	<-fx.checkStarted
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
			defer cancel()
			reply, _ := a.Handle(ctx, request("select_output", M{"output": "DP-1"}))
			if reply["ok"] == true {
				t.Error("queued action unexpectedly completed")
			}
		}()
	}
	wg.Wait()
	if len(a.fx.jobs) > 8 {
		t.Fatal("unbounded action queue")
	}
	close(fx.blockCheck)
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("blocked action did not finish")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e := a.Close(ctx); e != nil {
		t.Fatal(e)
	}
	fx.mu.Lock()
	defer fx.mu.Unlock()
	if fx.selects != 0 {
		t.Fatal("expired actions executed", fx.selects)
	}
}
func TestHeartbeatIndependentOfAppStateLock(t *testing.T) {
	fx := &coordinatedEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx, Now: time.Now})
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	a.mu.Lock()
	time.Sleep(1150 * time.Millisecond)
	snapshot := a.Snapshot()
	a.mu.Unlock()
	if snapshot["snapshot_revision"] == nil {
		t.Fatal("missing cached snapshot")
	}
	fx.mu.Lock()
	ticks := append([]time.Time{}, fx.ticks...)
	fx.mu.Unlock()
	if len(ticks) < 3 {
		t.Fatal("app mutex starved independent heartbeat", len(ticks))
	}
	for i := 1; i < len(ticks); i++ {
		if ticks[i].Sub(ticks[i-1]) > 850*time.Millisecond {
			t.Fatal("heartbeat gap", ticks)
		}
	}
	if fx.maxActive.Load() != 1 {
		t.Fatal("manager calls overlapped")
	}
}

func unixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, e := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if e != nil {
		t.Fatal(e)
	}
	files := []*os.File{os.NewFile(uintptr(fds[0]), "pair-a"), os.NewFile(uintptr(fds[1]), "pair-b")}
	connections := []*net.UnixConn{}
	for _, file := range files {
		conn, e := net.FileConn(file)
		file.Close()
		if e != nil {
			t.Fatal(e)
		}
		connections = append(connections, conn.(*net.UnixConn))
	}
	t.Cleanup(func() { connections[0].Close(); connections[1].Close() })
	return connections[0], connections[1]
}
func TestSlowSubscribersCannotBlockBroadcast(t *testing.T) {
	s := &Server{peers: map[*peer]bool{}}
	peers := []*peer{}
	for i := 0; i < 16; i++ {
		server, _ := unixPair(t)
		server.SetWriteBuffer(1024)
		p := newPeer(context.Background(), server)
		p.subscribed = true
		s.peers[p] = true
		peers = append(peers, p)
		t.Cleanup(func() { p.close(); <-p.writerDone })
	}
	event := M{"version": 1.0, "event": "snapshot", "snapshot": M{"text": strings.Repeat("x", 128*1024)}}
	start := time.Now()
	for i := 0; i < 12; i++ {
		s.broadcast(event)
	}
	if time.Since(start) > time.Second {
		t.Fatal("broadcast waited for slow subscribers")
	}
	for _, p := range peers {
		select {
		case <-p.closed:
		case <-time.After(time.Second):
			t.Fatal("slow subscriber not disconnected")
		}
		if len(p.out) > peerQueueLimit {
			t.Fatal("unbounded outbound queue")
		}
	}
}
func TestSnapshotRevisionsAndAtomicSubscription(t *testing.T) {
	a := newTestApp(t, Options{Offline: true})
	old := a.Snapshot()
	reply, _ := a.Handle(context.Background(), request("set_controls", M{"controls": M{"units": "C"}}))
	updated := object(reply["snapshot"])
	if updated["snapshot_revision"].(float64) <= old["snapshot_revision"].(float64) {
		t.Fatal("revision did not advance")
	}
	server, client := unixPair(t)
	p := newPeer(context.Background(), server)
	defer func() { p.close(); <-p.writerDone }()
	s := &Server{app: a, peers: map[*peer]bool{p: true}}
	initial := M{"version": 1.0, "request_id": 7.0, "ok": true, "snapshot": old}
	if e := s.subscribe(p, initial); e != nil {
		t.Fatal(e)
	}
	s.broadcast(M{"version": 1.0, "event": "snapshot", "snapshot": a.Snapshot()})
	client.SetReadDeadline(time.Now().Add(time.Second))
	reader := bufio.NewReader(client)
	raw, e := reader.ReadBytes('\n')
	if e != nil {
		t.Fatal(e)
	}
	first, e := safeio.Object(raw, ipc.ResponseLimit)
	if e != nil || first["request_id"] != 7.0 || object(object(first["snapshot"])["controls"])["units"] != "C" {
		t.Fatal("subscription missed current state", first, e)
	}
	raw, e = reader.ReadBytes('\n')
	if e != nil {
		t.Fatal(e)
	}
	second, e := safeio.Object(raw, ipc.ResponseLimit)
	if e != nil || object(second["snapshot"])["snapshot_revision"].(float64) <= object(first["snapshot"])["snapshot_revision"].(float64) {
		t.Fatal("new event revision not ordered", second, e)
	}
}
func TestChangedTrafficCannotPostponeScheduledTicks(t *testing.T) {
	fx := &coordinatedEffects{}
	a := newTestApp(t, Options{Offline: true, Effects: fx, Now: time.Now})
	reply, _ := a.Handle(context.Background(), request("start_live_effects", nil))
	if reply["ok"] != true {
		t.Fatal(reply)
	}
	dir, e := os.MkdirTemp("/tmp", "weather-scheduling-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, filepath.Join(dir, "app.sock"), a, func() { close(ready) }) }()
	<-ready
	start := time.Now()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for time.Since(start) < 1200*time.Millisecond {
		<-ticker.C
		a.signal()
	}
	a.fx.mu.RLock()
	selected := a.fx.weather["selected_at"]
	a.fx.mu.RUnlock()
	stamp, e := weather.Instant(selected)
	if e != nil || stamp.Before(start.Add(750*time.Millisecond)) {
		cancel()
		<-done
		t.Fatal("Changed stream postponed app tick", selected, e)
	}
	cancel()
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
