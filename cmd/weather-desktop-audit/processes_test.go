package main

import (
	"errors"
	"io"
	"os"
	"reflect"
	"syscall"
	"testing"
)

func TestBoundedTaskNamesRefusesPartialListingError(t *testing.T) {
	partial := errors.New("injected directory read failure")
	if _, err := boundedTaskNames([]string{"1234"}, partial); !errors.Is(err, partial) {
		t.Fatalf("partial child listing accepted: %v", err)
	}
	rows, err := boundedTaskNames([]string{"1234"}, io.EOF)
	if err != nil || len(rows) != 1 {
		t.Fatalf("EOF partial page should remain usable: %v %v", rows, err)
	}
	if _, err = boundedTaskNames(make([]string, 257), nil); err == nil {
		t.Fatal("oversized task listing accepted")
	}
}

func TestProcessInspectionExitRace(t *testing.T) {
	before := processIdentity{PID: 1234, Parent: 1000, StartTime: "42", State: "R", UID: 1000}
	for _, tc := range []struct {
		name       string
		state      string
		commandErr error
		recheckErr error
		change     func(*processIdentity)
		wantErr    error
		wantChange bool
	}{
		{name: "permission then zombie", state: "Z", commandErr: syscall.EACCES},
		{name: "permission then dead", state: "X", commandErr: syscall.EPERM},
		{name: "permission then gone", commandErr: syscall.EACCES, recheckErr: os.ErrNotExist, wantErr: os.ErrNotExist},
		{name: "live permission denial", state: "S", commandErr: syscall.EACCES, wantErr: syscall.EACCES},
		{name: "changed generation", state: "Z", commandErr: syscall.EACCES, change: func(p *processIdentity) { p.StartTime = "43" }, wantErr: syscall.EACCES, wantChange: true},
		{name: "changed parent", state: "Z", commandErr: syscall.EACCES, change: func(p *processIdentity) { p.Parent++ }, wantErr: syscall.EACCES, wantChange: true},
		{name: "changed uid", state: "Z", commandErr: syscall.EACCES, change: func(p *processIdentity) { p.UID++ }, wantErr: syscall.EACCES, wantChange: true},
		{name: "unreadable recheck", commandErr: syscall.EACCES, recheckErr: syscall.EIO, wantErr: syscall.EIO},
		{name: "unrelated read failure", commandErr: syscall.EIO, wantErr: syscall.EIO},
		{name: "successful command", state: "S"},
		{name: "successful command changed generation", state: "S", change: func(p *processIdentity) { p.StartTime = "43" }, wantChange: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			after := before
			after.State = tc.state
			if tc.change != nil {
				tc.change(&after)
			}
			observed, err := inspectProcessWith(before.PID, func(int) (processIdentity, error) {
				reads++
				if reads == 1 {
					return before, nil
				}
				return after, tc.recheckErr
			}, func(int) (string, []string, error) {
				return "/owned/helper", []string{"/owned/helper", "--fixture"}, tc.commandErr
			})
			if tc.wantErr != nil || tc.wantChange {
				if err == nil || tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
					t.Fatalf("inspection error = %v, want %v (changed=%t)", err, tc.wantErr, tc.wantChange)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if tc.commandErr != nil {
				if !reflect.DeepEqual(observed, after) {
					t.Fatalf("exit metadata = %+v, want %+v", observed, after)
				}
			} else if observed.State != after.State || observed.Executable != "/owned/helper" || !reflect.DeepEqual(observed.Arguments, []string{"/owned/helper", "--fixture"}) {
				t.Fatalf("incomplete command identity: %+v", observed)
			}
			wantReads := 2
			if tc.commandErr != nil && !os.IsPermission(tc.commandErr) {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("metadata reads = %d, want bounded %d", reads, wantReads)
			}
		})
	}
}
