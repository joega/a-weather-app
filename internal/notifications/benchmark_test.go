package notifications

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

func benchmarkWatcher(b *testing.B) (*Watcher, M, M) {
	b.Helper()
	path := b.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		b.Fatal(err)
	}
	d, err := safeio.OpenDir(path, false)
	if err != nil {
		b.Fatal(err)
	}
	w := New(d, func(context.Context, string, string) error {
		b.Error("reserved/paused benchmark delivered")
		return nil
	})
	b.Cleanup(func() { w.Close(); d.Close() })
	if err := w.Configure(M{"enabled": true, "quiet_enabled": false}); err != nil {
		b.Fatal(err)
	}
	f, l := testForecast(testNow)
	rows := make([]any, 240)
	for i := range rows {
		rows[i] = M{"time": testNow.Add(time.Duration(i+1) * time.Hour).Format(time.RFC3339), "precipitation_probability": float64(.8)}
	}
	f["hourly"] = rows
	return w, f, l
}

func BenchmarkPausedTick(b *testing.B) {
	w, f, l := benchmarkWatcher(b)
	if err := w.Snooze(testNow, false); err != nil {
		b.Fatal(err)
	}
	w.Tick(f, l, testNow, true)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.Tick(f, l, testNow, true)
	}
}

func BenchmarkReservedTick(b *testing.B) {
	w, f, l := benchmarkWatcher(b)
	event := Candidate(f, l, testNow, 50)
	w.document["events"] = []any{M{"location": event["location"], "start": event["start"], "end": event["end"], "reserved": float64(testNow.Unix()), "expires": float64(testNow.Unix()) + retention}}
	w.Tick(f, l, testNow, true)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		w.Tick(f, l, testNow, true)
	}
}
