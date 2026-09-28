package main

import (
	"os"
	"testing"
)

func TestProcessBreakdown(t *testing.T) {
	rows := map[int]*processMeasurement{2: {PID: 2, Ticks: 20, PeakRSS: 1048576, PeakPSS: 524288, Role: "Qt"}, 1: {PID: 1, Ticks: 10, Role: "service"}}
	stats := processStatistics(rows, 100, 10)
	if len(stats) != 2 || stats[0]["pid"] != 1 || stats[1]["cpu_percent_one_core"] != float64(2) || stats[1]["peak_rss_mib"] != float64(1) {
		t.Fatal(stats)
	}
	if processRole(os.Getpid()) == "unknown" {
		t.Fatal("cannot identify owned test process")
	}
}
