package locale

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func formatterHelper(t *testing.T, script string) string {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "native", "qt", "a-weather-app-qt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func TestNotificationFormatterCanonicalArgsAndRegionalEnvironment(t *testing.T) {
	t.Setenv("LC_TIME", "en_GB.UTF-8")
	t.Setenv("LANGUAGE", "en")
	root := formatterHelper(t, `printf '%s\n' "$LC_TIME|$1|$2|$3"`)
	formatter := NotificationDateFormatter(root)
	zone, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	instant := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	expected := "en_GB.UTF-8|--format-notification-time|2026-10-25T01:30:00Z|Europe/London"
	if got := formatter(context.Background(), instant, zone); got != expected {
		t.Fatal(got)
	}
	// Choosing a different interface language does not enter formatter args or
	// change the captured LC_TIME region. A separate uncached date proves it.
	t.Setenv("LANGUAGE", "hi")
	t.Setenv("LC_TIME", "hi_IN.UTF-8")
	expected = "en_GB.UTF-8|--format-notification-time|2026-10-25T02:30:00Z|Europe/London"
	if got := formatter(context.Background(), instant.Add(time.Hour), zone); got != expected {
		t.Fatal(got)
	}
}
func TestNotificationFormatterRejectsInvalidBoundedOutput(t *testing.T) {
	instant := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	for name, script := range map[string]string{"oversize": "printf '%0300d' 1", "utf8": `printf '\377'`, "markup": `printf '<b>time</b>'`, "control": `printf 'time\001'`, "error": "exit 1"} {
		t.Run(name, func(t *testing.T) {
			formatter := NotificationDateFormatter(formatterHelper(t, script))
			got := formatter(context.Background(), instant, time.UTC)
			if got != DateTime(instant) {
				t.Fatal(got)
			}
		})
	}
	if got := NotificationDateFormatter("relative")(context.Background(), instant, time.UTC); got != DateTime(instant) {
		t.Fatal(got)
	}
}
func TestNotificationFormatterCancellationAndTimeout(t *testing.T) {
	formatter := NotificationDateFormatter(formatterHelper(t, "exec sleep 5"))
	instant := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	got := formatter(ctx, instant, time.UTC)
	if got != DateTime(instant) || time.Since(start) > 100*time.Millisecond {
		t.Fatal("canceled formatter spawned work", got, time.Since(start))
	}
	start = time.Now()
	got = formatter(context.Background(), instant, time.UTC)
	if got != DateTime(instant) || time.Since(start) > 1500*time.Millisecond {
		t.Fatal("helper deadline unbounded", got, time.Since(start))
	}
}
func TestNotificationFormatterCacheBoundedAndSymlinkRefused(t *testing.T) {
	root := formatterHelper(t, `printf x >> "$0.calls"; printf 'regional time'`)
	formatter := NotificationDateFormatter(root)
	instant := time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)
	for range 3 {
		formatter(context.Background(), instant, time.UTC)
	}
	path := filepath.Join(root, "native", "qt", "a-weather-app-qt")
	raw, err := os.ReadFile(path + ".calls")
	if err != nil || string(raw) != "x" {
		t.Fatal("identical date reran helper", string(raw), err)
	}
	for i := 1; i <= notificationTimeCacheLimit; i++ {
		formatter(context.Background(), instant.Add(time.Duration(i)*time.Minute), time.UTC)
	}
	formatter(context.Background(), instant, time.UTC)
	raw, err = os.ReadFile(path + ".calls")
	if err != nil || len(raw) != notificationTimeCacheLimit+2 {
		t.Fatal("cache eviction did not bound labels", len(raw), err)
	}
	if err := os.Rename(path, path+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".real", path); err != nil {
		t.Fatal(err)
	}
	if got := NotificationDateFormatter(root)(context.Background(), instant, time.UTC); !strings.HasPrefix(got, "2026-10-25") {
		t.Fatal("symlinked helper used", got)
	}
}
