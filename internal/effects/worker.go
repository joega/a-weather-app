package effects

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/joega/a-weather-app/internal/safeio"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// RunWorker is an internal dispatch target. Its private input pipe is the sole
// ownership heartbeat; EOF, signals and stale updates initiate bounded cleanup.
func RunWorker(args []string) int {
	flags := flag.NewFlagSet("effects-worker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", "", "")
	instance := flags.String("instance", "", "")
	output := flags.String("output", "", "")
	if flags.Parse(args) != nil || *root == "" || !instancePattern.MatchString(*instance) || !outputPattern.MatchString(*output) {
		return 2
	}
	info, e := os.Stdin.Stat()
	if e != nil || info.Mode()&os.ModeNamedPipe == 0 {
		return 2
	}
	// Standard output inherited through exec is blocking. Rewrap a nonblocking
	// duplicate so Go's poller can enforce the private reply deadline.
	fd, e := syscall.Dup(1)
	if e != nil {
		return 2
	}
	syscall.CloseOnExec(fd)
	if e = syscall.SetNonblock(fd, true); e != nil {
		syscall.Close(fd)
		return 2
	}
	outputFile := os.NewFile(uintptr(fd), "effects-owner-reply")
	defer outputFile.Close()
	s := newSession(*root, *instance, *output)
	return serveWorker(s, os.Stdin, outputFile)
}

type workerRequest struct {
	value object
	err   error
}

func serveWorker(s *session, input, output *os.File) (result int) {
	return serveWorkerObservedWithTimeout(s, input, output, nil, 8*time.Second)
}

func serveWorkerObserved(s *session, input, output *os.File, ownerLostObserved func()) (result int) {
	return serveWorkerObservedWithTimeout(s, input, output, ownerLostObserved, 8*time.Second)
}

func serveWorkerObservedWithTimeout(s *session, input, output *os.File, ownerLostObserved func(), ownerTimeout time.Duration) (result int) {
	owner, ownerLost := context.WithCancel(context.Background())
	defer ownerLost()
	requests := make(chan workerRequest, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		reader := bufio.NewReaderSize(input, requestLimit)
		for {
			line, e := reader.ReadSlice('\n')
			var request object
			if e == nil {
				request, e = safeio.Object(line, requestLimit)
			}
			if e != nil {
				// Interrupt an in-flight native command immediately; start/tick
				// own a separate cleanup budget after cancellation.
				ownerLost()
				if ownerLostObserved != nil {
					ownerLostObserved()
				}
			}
			select {
			case requests <- workerRequest{request, e}:
			case <-done:
				return
			}
			if e != nil {
				return
			}
		}
	}()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	lastOwner := time.Now()
	var weather, flags object
	defer func() {
		// A failed cleanup is evidence, not permission to retry and erase its
		// diagnostics after the child exit status has already been consumed.
		if s.state == "cleanup_failed" {
			result = 1
			return
		}
		if s.state == "stopped" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if e := s.stop(ctx); e != nil {
			fmt.Fprintln(os.Stderr, "Effects worker cleanup:", bounded(e.Error(), 1024))
			result = 1
		}
	}()
	for {
		var timeout <-chan time.Time
		if s.state == "running" {
			next := s.deadline
			if ownerExpiry := lastOwner.Add(ownerTimeout); ownerExpiry.Before(next) {
				next = ownerExpiry
			}
			wait := next.Sub(s.now())
			if wait < 0 {
				wait = 0
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(wait)
			timeout = timer.C
		} else if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		select {
		case <-signals:
			return 0
		case <-timeout:
			if s.state == "running" && (time.Since(lastOwner) >= ownerTimeout || !s.now().Before(s.deadline)) {
				// The service coordinator owns ordinary updates and native lease
				// heartbeats. This independent worker only enforces owner loss and
				// finite expiry; repeating the full native/compositor tick here
				// duplicated every 500 ms and doubled active-effects work.
				// Owner cancellation interrupted the operation that discovered EOF;
				// cleanup itself is an independent obligation and gets its own budget.
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				e := s.stop(ctx)
				cancel()
				if e != nil && s.state != "stopped" || s.state == "cleanup_failed" {
					if e != nil {
						fmt.Fprintln(os.Stderr, "Effects worker cleanup:", bounded(e.Error(), 1024))
					}
					return 1
				}
				// A clean expiry remains alive to acknowledge stopped on the
				// next parent RPC. EOF then retires this now-inactive worker.
			}
		case item := <-requests:
			if item.err != nil {
				if item.err == io.EOF {
					return 0
				}
				return 1
			}
			r := item.value
			id, ok := integer(r["id"])
			if !ok || id > 2147483647 {
				return 1
			}
			op := text(r["op"])
			base := owner
			if op == "stop" {
				base = context.Background()
			}
			ctx, cancel := context.WithTimeout(base, 25*time.Second)
			var operationError error
			switch op {
			case "start":
				duration, ok := integer(r["duration"])
				persistent, pok := r["persistent"].(bool)
				if len(r) != 5 || !ok || !pok {
					operationError = errors.New("effects start schema")
				} else {
					flags = obj(r["controls"])
					operationError = s.start(ctx, int(duration), persistent, flags)
					lastOwner = time.Now()
				}
			case "tick":
				if len(r) != 4 || (r["weather"] != nil && obj(r["weather"]) == nil) {
					operationError = errors.New("effects tick schema")
				} else {
					weather = obj(r["weather"])
					flags = obj(r["controls"])
					lastOwner = time.Now()
					operationError = s.tick(ctx, weather, flags)
				}
			case "status":
				if len(r) != 2 {
					operationError = errors.New("effects status schema")
				}
			case "stop":
				if len(r) != 2 {
					operationError = errors.New("effects stop schema")
				} else {
					operationError = s.stop(ctx)
				}
			default:
				operationError = errors.New("effects operation schema")
			}
			cancel()
			reply := object{"id": id, "ok": operationError == nil, "status": s.status()}
			if operationError != nil {
				reply["error"] = bounded(operationError.Error(), 512)
			}
			if op == "start" && s.generation > 0 {
				if b, ok := s.b.(*nativeBackend); ok {
					reply["ownership"] = object{"instance": b.instance, "pid": b.pid, "starttime": b.starttime, "plugin_inventory": b.inventoryValue, "generation": s.generation}
				}
			}
			raw, e := json.Marshal(reply)
			if e != nil || len(raw)+1 > replyLimit {
				return 1
			}
			if e = output.SetWriteDeadline(time.Now().Add(3 * time.Second)); e != nil {
				return 1
			}
			if _, e = output.Write(append(raw, '\n')); e != nil {
				return 1
			}
			if op == "stop" {
				if s.state != "stopped" {
					return 1
				}
				return 0
			}
		}
	}
}
