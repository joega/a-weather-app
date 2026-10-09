package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/weather"
)

// TestWarningNativeServiceFixture is a child process used only by the private
// Qt/D-Bus integration target. It runs the real App and Serve with an injected
// clock and provider. No fixture operation is added to production IPC or CLI.
func TestWarningNativeServiceFixture(t *testing.T) {
	if os.Getenv("WEATHER_NATIVE_WARNING_FIXTURE") != "1" {
		t.Skip("private native warning integration helper")
	}
	if os.Getenv("WEATHER_WARNING_PRIVATE_BUS") != "1" {
		t.Fatal("private notification bus required")
	}
	base := time.Now().UTC().Truncate(time.Second)
	var clock, requests, history, active, forecasts atomic.Int64
	clock.Store(base.Unix())
	now := func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
	var feedMu sync.RWMutex
	var messages []weather.AlertMessage
	failed := false
	options := Options{
		Now: now,
		FetchCountry: func(context.Context, M, time.Time, string) (M, error) {
			forecasts.Add(1)
			return nil, errors.New("private fixture has no forecast provider")
		},
		FetchAlertMessages: func(_ context.Context, query weather.AlertMessageQuery, at time.Time) (weather.AlertMessagePage, error) {
			requests.Add(1)
			if query.Active {
				active.Add(1)
			} else {
				history.Add(1)
			}
			feedMu.RLock()
			defer feedMu.RUnlock()
			if failed {
				return weather.AlertMessagePage{}, errors.New("private warning feed unavailable")
			}
			selected := append([]weather.AlertMessage{}, messages...)
			if query.Active {
				selected = nil
				if len(messages) > 0 {
					latest := messages[len(messages)-1]
					if latest.Type != "Cancel" && latest.Expires.After(at) {
						selected = []weather.AlertMessage{latest}
					}
				}
			}
			return weather.AlertMessagePage{Messages: selected, FetchedAt: at, Complete: true}, nil
		},
		ResolveSelection: func(context.Context, M) (M, error) { return nil, errors.New("fixture resolution disabled") },
		SearchPlaces:     func(context.Context, M) ([]any, error) { return nil, errors.New("fixture search disabled") },
	}
	a, state := runtimeLocations(t, options, 1)
	runtime, err := os.MkdirTemp("/tmp", "weather-warning-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(runtime)
	socket := filepath.Join(runtime, "app.sock")
	var cancel context.CancelFunc
	var done chan error
	start := func() {
		ctx, stop := context.WithCancel(context.Background())
		cancel, done = stop, make(chan error, 1)
		ready := make(chan struct{})
		go func(current *App) { done <- Serve(ctx, socket, current, func() { close(ready) }) }(a)
		select {
		case <-ready:
		case err := <-done:
			t.Fatal(err)
		case <-time.After(3 * time.Second):
			t.Fatal("fixture socket did not start")
		}
	}
	stop := func() {
		if cancel == nil {
			return
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(6 * time.Second):
			t.Error("fixture service did not stop")
		}
		cancel = nil
	}
	defer stop()
	start()
	respond := func(extra M) {
		value := M{"socket": socket, "now": now().Format(time.RFC3339), "requests": requests.Load(), "active": active.Load(), "history": history.Load(), "forecasts": forecasts.Load()}
		for key, item := range extra {
			value[key] = item
		}
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("WARNING_FIXTURE %s\n", data)
	}
	respond(M{"ready": true})
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		var command struct {
			Op      string `json:"op"`
			Stage   string `json:"stage"`
			Seconds int64  `json:"seconds"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &command); err != nil {
			t.Fatal(err)
		}
		switch command.Op {
		case "advance":
			if command.Seconds < 0 || command.Seconds > 86400 {
				t.Fatal("invalid fixture clock advance")
			}
			clock.Add(command.Seconds)
		case "stage":
			feedMu.Lock()
			switch command.Stage {
			case "error":
				failed = true
			case "recover":
				failed = false
			case "new", "update", "cancel", "oversized":
				failed = false
				message := alertTestMessage("native-"+command.Stage, now())
				message.Expires = now().Add(time.Hour)
				message.Instruction = "Original fixture instructions.\nKeep both lines intact. <b>Plain text.</b>"
				if command.Stage == "update" || command.Stage == "cancel" {
					if len(messages) == 0 {
						t.Fatal("fixture update lacks a predecessor")
					}
					message.References = []weather.AlertReference{messages[len(messages)-1].Identity}
					message.Type = "Update"
					message.Instruction = "Updated fixture instructions. Use the northern route."
					if command.Stage == "cancel" {
						message.Type = "Cancel"
						message.Instruction = "This fixture warning has been canceled."
					}
				} else if command.Stage == "oversized" {
					message.Description = strings.Repeat("\x01", 32000)
					message.Instruction = strings.Repeat("\x02", 16000)
				}
				messages = append(messages, message)
			default:
				t.Fatal("unknown fixture stage")
			}
			feedMu.Unlock()
		case "restart":
			stop()
			a, err = New(state, options)
			if err != nil {
				t.Fatal(err)
			}
			start()
		case "status":
		case "stop":
			return
		default:
			t.Fatal("unknown fixture command")
		}
		// The provider finishes immediately, but still traverses the production
		// asynchronous scheduler. Drain completion over a bounded few turns.
		for range 5 {
			a.Tick(context.Background())
			time.Sleep(2 * time.Millisecond)
		}
		a.signal()
		respond(M{"warning": a.Snapshot()["warning_notifications"]})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
