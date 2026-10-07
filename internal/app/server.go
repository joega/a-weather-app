package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/joega/a-weather-app/internal/ipc"
	"github.com/joega/a-weather-app/internal/safeio"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

const peerQueueLimit = 4

type outbound struct {
	raw []byte
	ack chan error
}
type peer struct {
	conn               *net.UnixConn
	out                chan outbound
	closed, writerDone chan struct{}
	closeOnce          sync.Once
	ctx                context.Context
	cancel             context.CancelFunc
	subscribed         bool
	presentationActive bool
}

func newPeer(ctx context.Context, conn *net.UnixConn) *peer {
	ctx, cancel := context.WithCancel(ctx)
	p := &peer{conn: conn, out: make(chan outbound, peerQueueLimit), closed: make(chan struct{}), writerDone: make(chan struct{}), ctx: ctx, cancel: cancel}
	go p.writeLoop()
	return p
}
func (p *peer) close() { p.closeOnce.Do(func() { p.cancel(); close(p.closed); p.conn.Close() }) }
func (p *peer) writeLoop() {
	defer close(p.writerDone)
	for {
		select {
		case <-p.closed:
			return
		case message := <-p.out:
			e := ipc.WriteEncoded(p.conn, message.raw)
			if message.ack != nil {
				message.ack <- e
			}
			if e != nil {
				p.close()
				return
			}
		}
	}
}
func (p *peer) enqueue(raw []byte, ack bool) (<-chan error, error) {
	var result chan error
	if ack {
		result = make(chan error, 1)
	}
	select {
	case <-p.closed:
		return nil, net.ErrClosed
	default:
	}
	select {
	case p.out <- outbound{raw, result}:
		return result, nil
	default:
		p.close()
		return nil, errors.New("slow IPC peer")
	}
}

type Server struct {
	app              *App
	listener         *net.UnixListener
	mu               sync.Mutex
	peers            map[*peer]bool
	ctx              context.Context
	cancel           context.CancelFunc
	uiSeen           bool
	last             []byte
	lastMapRevision  uint64
	presentationWake chan struct{}
}

func (s *Server) mapEvent() {
	revision, value := s.app.Map()
	if value == nil || revision == s.lastMapRevision {
		return
	}
	s.lastMapRevision = revision
	s.broadcast(M{"version": 1.0, "event": "map", "map": value})
}

func (s *Server) send(p *peer, value M) error {
	raw, e := ipc.Encode(value, ipc.ResponseLimit)
	if e != nil {
		return e
	}
	_, e = p.enqueue(raw, false)
	return e
}
func (s *Server) subscribers() []*peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	peers := []*peer{}
	for p := range s.peers {
		if p.subscribed {
			peers = append(peers, p)
		}
	}
	return peers
}
func (s *Server) broadcast(value M) {
	raw, e := ipc.Encode(value, ipc.ResponseLimit)
	if e != nil {
		return
	}
	for _, p := range s.subscribers() {
		p.enqueue(raw, false)
	}
}

// Terminal acknowledgements share one two-second drain budget, independent of
// the number of peers. Ordinary broadcasts never wait for a socket write.
func (s *Server) broadcastAndFlush(value M) {
	raw, e := ipc.Encode(value, ipc.ResponseLimit)
	if e != nil {
		return
	}
	type wait struct {
		peer *peer
		ack  <-chan error
	}
	pending := []wait{}
	for _, p := range s.subscribers() {
		ack, e := p.enqueue(raw, true)
		if e == nil {
			pending = append(pending, wait{p, ack})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, w := range pending {
		select {
		case <-w.ack:
		case <-w.peer.closed:
		case <-ctx.Done():
			return
		}
	}
}
func (s *Server) snapshot() { s.snapshotTo(false) }
func (s *Server) snapshotTo(presentedOnly bool) {
	value := s.app.Snapshot()
	comparison := M{}
	for k, v := range value {
		if k != "snapshot_revision" {
			comparison[k] = v
		}
	}
	raw, e := json.Marshal(comparison)
	if e != nil || bytes.Equal(raw, s.last) {
		return
	}
	s.last = raw
	event := M{"version": 1.0, "event": "snapshot", "snapshot": value}
	if !presentedOnly {
		s.broadcast(event)
		return
	}
	raw, e = ipc.Encode(event, ipc.ResponseLimit)
	if e != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.peers {
		if p.subscribed && p.presentationActive {
			p.enqueue(raw, false)
		}
	}
}
func (s *Server) subscribe(p *peer, reply M) error {
	// Registration, refreshed initial snapshot and its enqueue are atomic with
	// subscriber enumeration. A subsequent event cannot precede this first reply.
	s.mu.Lock()
	defer s.mu.Unlock()
	reply["snapshot"] = s.app.Snapshot()
	if e := s.send(p, reply); e != nil {
		return e
	}
	p.subscribed = true
	p.presentationActive = true
	s.uiSeen = true
	return nil
}

// Presentation is a subscriber property; hiding never suspends weather,
// notifications, effects, or state-change events.
func (s *Server) hasPresentation() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.peers {
		if p.subscribed && p.presentationActive {
			return true
		}
	}
	return false
}
func (s *Server) presentation(p *peer, request M) error {
	reply := M{"version": 1.0, "request_id": request["request_id"], "ok": false, "error": "invalid_request"}
	id, validID := request["request_id"].(float64)
	active, validActive := request["active"].(bool)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(request) == 4 && request["version"] == 1.0 && validID && id >= 0 && id <= 2147483647 && id == float64(int64(id)) && validActive && p.subscribed {
		p.presentationActive = active
		if s.presentationWake != nil {
			select {
			case s.presentationWake <- struct{}{}:
			default:
			}
		}
		reply = M{"version": 1.0, "request_id": id, "ok": true}
		if active {
			reply["snapshot"] = s.app.Snapshot()
		}
	}
	return s.send(p, reply)
}
func (s *Server) handle(p *peer) {
	defer func() {
		p.close()
		<-p.writerDone
		s.mu.Lock()
		wasUI := p.subscribed
		delete(s.peers, p)
		remaining := 0
		for x := range s.peers {
			if x.subscribed {
				remaining++
			}
		}
		seen := s.uiSeen
		s.mu.Unlock()
		if wasUI && seen && remaining == 0 {
			s.cancel()
		}
	}()
	if ipc.Peer(p.conn) != nil {
		return
	}
	scan := ipc.Scanner(p.conn, ipc.RequestLimit)
	for scan.Scan() {
		request, e := safeio.Object(scan.Bytes(), ipc.RequestLimit)
		if e != nil {
			if s.send(p, M{"version": 1.0, "request_id": nil, "ok": false, "error": "invalid_request"}) != nil {
				return
			}
			continue
		}
		if request["op"] == "set_presentation" {
			if s.presentation(p, request) != nil {
				return
			}
			continue
		}
		if request["op"] == "toggle_window" && len(request) == 3 && request["version"] == 1.0 {
			if id, ok := request["request_id"].(float64); ok && id >= 0 && id <= 2147483647 && id == float64(int64(id)) {
				s.broadcast(M{"version": 1.0, "event": "toggle_window"})
				if s.send(p, M{"version": 1.0, "request_id": id, "ok": true}) != nil {
					return
				}
				continue
			}
		}
		ctx, cancel := context.WithTimeout(p.ctx, 45*time.Second)
		reply, quit := s.app.Handle(ctx, request)
		cancel()
		if request["op"] == "snapshot" && reply["ok"] == true {
			reply["frontend_ready"] = s.hasPresentation()
		}
		if request["op"] == "subscribe" && reply["ok"] == true {
			if s.subscribe(p, reply) != nil {
				return
			}
			continue
		}
		if quit {
			ctx, cancel := context.WithTimeout(context.Background(), CloseBudget)
			e = s.app.Close(ctx)
			cancel()
			if e != nil {
				reply["ok"] = false
				reply["error"] = "cleanup_failed"
			}
			raw, e := ipc.Encode(reply, ipc.ResponseLimit)
			if e == nil {
				if ack, e := p.enqueue(raw, true); e == nil {
					select {
					case <-ack:
					case <-p.closed:
					case <-time.After(2 * time.Second):
					}
				}
			}
			s.cancel()
			return
		}
		if e = s.send(p, reply); e != nil {
			return
		}
	}
}

// Serve requires the caller to hold a runtime-directory lock for this socket.
func Serve(ctx context.Context, path string, a *App, onReady func()) error {
	if info, e := os.Lstat(path); e == nil {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
			return errors.New("unsafe socket target")
		}
		probe, e := net.DialTimeout("unix", path, 200*time.Millisecond)
		if e == nil {
			probe.Close()
			return errors.New("service already running")
		}
		if !errors.Is(e, syscall.ECONNREFUSED) {
			return errors.New("socket liveness is uncertain")
		}
		current, e := os.Lstat(path)
		if e != nil || !os.SameFile(info, current) {
			return errors.New("socket identity changed")
		}
		if e = os.Remove(path); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if e != nil {
		return e
	}
	listener.SetUnlinkOnClose(false)
	if e = os.Chmod(path, 0600); e != nil {
		listener.Close()
		return e
	}
	original, e := os.Lstat(path)
	if e != nil {
		listener.Close()
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Server{app: a, listener: listener, peers: map[*peer]bool{}, ctx: ctx, cancel: cancel, presentationWake: make(chan struct{}, 1)}
	var peersWG sync.WaitGroup
	defer func() {
		cancel()
		listener.Close()
		s.mu.Lock()
		peers := []*peer{}
		for p := range s.peers {
			peers = append(peers, p)
			p.close()
		}
		s.mu.Unlock()
		peersWG.Wait()
		for _, p := range peers {
			<-p.writerDone
		}
		if current, e := os.Lstat(path); e == nil && os.SameFile(original, current) {
			os.Remove(path)
		}
	}()
	go func() { <-ctx.Done(); listener.Close() }()
	accepted := make(chan *net.UnixConn)
	go func() {
		defer close(accepted)
		for {
			conn, e := listener.AcceptUnix()
			if e != nil {
				return
			}
			select {
			case accepted <- conn:
			case <-ctx.Done():
				conn.Close()
				return
			}
		}
	}()
	if onReady != nil {
		onReady()
	}
	nextTick := time.Now()
	timer := time.NewTimer(0)
	defer timer.Stop()
	lastBroadcast := time.Time{}
	reset := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(time.Until(nextTick))
	}
	for {
		// Absolute scheduling prevents a continuous Changed stream from moving the
		// next weather/notification tick. Effects have their own independent clock.
		if !time.Now().Before(nextTick) && ctx.Err() == nil {
			tick, cancel := context.WithTimeout(ctx, time.Second)
			a.Tick(tick)
			cancel()
			if s.hasPresentation() && time.Since(lastBroadcast) >= 5*time.Second {
				s.snapshotTo(true)
				lastBroadcast = time.Now()
			}
			s.mapEvent()
			nextTick = time.Now().Add(a.interval(s.hasPresentation()))
			reset()
		}
		select {
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), CloseBudget)
			e := a.Close(cleanup)
			cancel()
			event := M{"version": 1.0, "event": "service_stopped", "ok": e == nil}
			if e != nil {
				event["error"] = "cleanup_failed"
			}
			s.broadcastAndFlush(event)
			return e
		case conn, ok := <-accepted:
			if !ok {
				cancel()
				continue
			}
			s.mu.Lock()
			if len(s.peers) >= 16 {
				s.mu.Unlock()
				conn.Close()
				continue
			}
			p := newPeer(ctx, conn)
			s.peers[p] = true
			s.mu.Unlock()
			peersWG.Add(1)
			go func() { defer peersWG.Done(); s.handle(p) }()
		case <-timer.C:
			s.mapEvent()
			continue
		case <-s.presentationWake:
			candidate := time.Now().Add(a.interval(s.hasPresentation()))
			if candidate.Before(nextTick) {
				nextTick = candidate
				reset()
			}
		case <-a.Changed:
			s.snapshot()
			s.mapEvent()
			lastBroadcast = time.Now()
			candidate := time.Now().Add(a.interval(s.hasPresentation()))
			if candidate.Before(nextTick) {
				nextTick = candidate
				reset()
			}
		}
	}
}
