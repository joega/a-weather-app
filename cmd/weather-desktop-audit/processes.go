package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/joega/a-weather-app/internal/safeio"
)

type processIdentity struct {
	PID        int      `json:"pid"`
	Parent     int      `json:"parent_pid"`
	StartTime  string   `json:"starttime"`
	State      string   `json:"state"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
	UID        uint32   `json:"uid"`
}

func inspectProcess(pid int) (processIdentity, error) {
	result := processIdentity{PID: pid}
	if pid < 1 || pid > 2147483647 {
		return result, errors.New("invalid process identity")
	}
	base := fmt.Sprintf("/proc/%d", pid)
	info, e := os.Stat(base)
	if e != nil {
		return result, e
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return result, errors.New("process UID unavailable")
	}
	result.UID = st.Uid
	raw, e := safeio.ReadFile(base+"/stat", 4096)
	if e != nil {
		return result, e
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return result, errors.New("invalid process stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 {
		return result, errors.New("short process stat")
	}
	result.State, result.StartTime = fields[0], fields[19]
	result.Parent, e = strconv.Atoi(fields[1])
	if e != nil {
		return result, e
	}
	if result.State == "Z" {
		return result, nil
	}
	result.Executable, e = filepath.EvalSymlinks(base + "/exe")
	if e != nil {
		return result, e
	}
	raw, e = safeio.ReadFile(base+"/cmdline", 8192)
	if e != nil {
		return result, e
	}
	result.Arguments = strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	return result, nil
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func boundedTaskNames(names []string, listingErr error) ([]string, error) {
	if len(names) > 256 {
		return nil, errors.New("owned thread count exceeds audit budget")
	}
	if listingErr != nil && listingErr != io.EOF {
		return nil, listingErr
	}
	return names, nil
}

func (a *audit) captureTree() error {
	if a.servicePID < 1 || a.guardianPID < 1 || a.guardianStart == "" {
		return errors.New("owned guardian/service identity missing")
	}
	type descendant struct{ pid, parent int }
	pending := []descendant{{a.guardianPID, 0}}
	seen := map[int]bool{}
	rows := []processIdentity{}
	serviceSeen := false
	for len(pending) > 0 {
		entry := pending[0]
		pending = pending[1:]
		pid := entry.pid
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if len(seen) > 64 {
			return errors.New("owned process tree exceeds audit budget")
		}
		identity, e := inspectProcess(pid)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if entry.parent != 0 && identity.Parent != entry.parent {
			return errors.New("owned descendant parent changed during observation")
		}
		if pid == a.guardianPID && (identity.StartTime != a.guardianStart || identity.Parent != os.Getpid() || identity.Executable != a.guardianExe || !reflect.DeepEqual(identity.Arguments, a.guardianArgs)) {
			return errors.New("owned guardian identity changed")
		}
		if pid == a.servicePID && (identity.Executable != a.executable || identity.Parent != a.guardianPID || !reflect.DeepEqual(identity.Arguments, a.serviceArgs) || !hasPair(identity.Arguments, "--state-dir", a.state)) {
			return errors.New("service identity changed")
		}
		if pid == a.servicePID {
			serviceSeen = true
		}
		if pid == a.servicePID {
			if a.serviceStart != "" && identity.StartTime != a.serviceStart {
				return errors.New("service PID generation changed")
			}
			a.serviceStart = identity.StartTime
		}
		a.processes[fmt.Sprintf("%d:%s", pid, identity.StartTime)] = identity
		rows = append(rows, identity)
		for i := 0; i+1 < len(identity.Arguments); i++ {
			if identity.Arguments[i] == "--weather" {
				path := filepath.Dir(identity.Arguments[i+1])
				if filepath.IsAbs(path) && strings.HasPrefix(filepath.Base(path), "a-weather-app-effects-") {
					a.runtimeDirs[path] = true
				}
			}
		}
		dir, e := os.Open(fmt.Sprintf("/proc/%d/task", pid))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		tasks, e := dir.Readdirnames(257)
		dir.Close()
		tasks, e = boundedTaskNames(tasks, e)
		if e != nil {
			return e
		}
		for _, task := range tasks {
			if _, e := strconv.Atoi(task); e != nil {
				return e
			}
			raw, e := safeio.ReadFile(fmt.Sprintf("/proc/%d/task/%s/children", pid, task), 8192)
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				return e
			}
			for _, field := range strings.Fields(string(raw)) {
				child, e := strconv.Atoi(field)
				if e != nil {
					return e
				}
				pending = append(pending, descendant{child, pid})
			}
		}
	}
	if !serviceSeen {
		return errors.New("guardian tree did not contain the captured service")
	}
	return a.record("owned-process-tree", object{"guardian_pid": a.guardianPID, "service_pid": a.servicePID, "processes": rows})
}

func (a *audit) verifyGone() error {
	return a.verifyGoneExcept(0)
}

func (a *audit) verifyGoneExcept(exceptPID int) error {
	var failures []error
	for _, before := range a.processes {
		if before.PID == exceptPID || (before.PID == a.guardianPID && !a.guardFinished) {
			continue
		}
		after, e := inspectProcess(before.PID)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			failures = append(failures, e)
			continue
		}
		if after.StartTime == before.StartTime && after.State != "Z" {
			failures = append(failures, fmt.Errorf("owned process remains: pid=%d starttime=%s", before.PID, before.StartTime))
		}
	}
	for path := range a.runtimeDirs {
		if expected, ok := a.retainedDirs[path]; ok {
			if e := verifyRetained(path, expected); e != nil {
				failures = append(failures, e)
			}
			continue
		}
		if _, e := os.Lstat(path); !os.IsNotExist(e) {
			failures = append(failures, fmt.Errorf("owned effects runtime remains (evidence preserved): %s", path))
		}
	}
	return errors.Join(failures...)
}
