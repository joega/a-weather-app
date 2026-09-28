package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type processMeasurement struct {
	PID                               int
	Identity, Ticks, PeakRSS, PeakPSS int64
	Role                              string
}

func processRole(pid int) string {
	file, err := os.Open(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "exited"
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return "unknown"
	}
	arguments := strings.Split(string(raw), "\x00")
	role := filepath.Base(arguments[0])
	for _, arg := range arguments {
		if arg == "--guardian" || arg == "--internal-guardian" {
			return role + " guardian"
		}
		if arg == "--service" {
			return role + " service"
		}
	}
	return role
}
func processStatistics(rows map[int]*processMeasurement, hz, seconds float64) []map[string]any {
	pids := []int{}
	for pid := range rows {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	result := []map[string]any{}
	for _, pid := range pids {
		row := rows[pid]
		result = append(result, map[string]any{"pid": pid, "role": row.Role, "cpu_percent_one_core": 100 * float64(row.Ticks) / hz / seconds, "peak_rss_mib": float64(row.PeakRSS) / 1048576, "peak_pss_mib": float64(row.PeakPSS) / 1048576})
	}
	return result
}
