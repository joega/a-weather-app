package locale

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// DateFormatter runs only in the already bounded notification delivery worker.
// It preserves the selected city's instant/timezone and regional LC_TIME rules.
type DateFormatter func(context.Context, time.Time, *time.Location) string

const notificationTimeLimit = 256
const notificationTimeCacheLimit = 32

type notificationTimeOutput struct{ bytes.Buffer }

func (b *notificationTimeOutput) Write(raw []byte) (int, error) {
	if len(raw) > notificationTimeLimit-b.Len() {
		return 0, errors.New("notification time output limit")
	}
	return b.Buffer.Write(raw)
}
func validNotificationTime(raw string) bool {
	if raw == "" || len(raw) > notificationTimeLimit || !utf8.ValidString(raw) {
		return false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '<' || r == '>' || r == '&' {
			return false
		}
	}
	return true
}
func FallbackNotificationTime(_ context.Context, instant time.Time, zone *time.Location) string {
	if zone == nil {
		zone = time.UTC
	}
	return DateTime(instant.In(zone))
}

// NotificationDateFormatter uses the trusted runtime's headless Qt locale helper,
// never PATH or a shell. Failed/development helpers retain an honest local ISO
// date. The environment is captured once; interface-language changes do not alter
// regional formatting. One fixed FIFO bounds retained labels across notices.
func NotificationDateFormatter(root string) DateFormatter {
	helper := ""
	if filepath.IsAbs(root) {
		candidate := filepath.Join(filepath.Clean(root), "native", "qt", "a-weather-app-qt")
		if info, err := os.Lstat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && info.Mode().Perm()&0022 == 0 {
			helper = candidate
		}
	}
	environment := os.Environ()
	var mutex sync.Mutex
	labels := map[string]string{}
	order := []string{}
	return func(parent context.Context, instant time.Time, zone *time.Location) string {
		if zone == nil {
			zone = time.UTC
		}
		fallback := FallbackNotificationTime(parent, instant, zone)
		if helper == "" || parent.Err() != nil {
			return fallback
		}
		stamp := instant.UTC().Format(time.RFC3339Nano)
		key := stamp + "|" + zone.String()
		mutex.Lock()
		cached, found := labels[key]
		mutex.Unlock()
		if found {
			return cached
		}
		ctx, cancel := context.WithTimeout(parent, time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, helper, "--format-notification-time", stamp, zone.String())
		cmd.Env = environment
		cmd.WaitDelay = 100 * time.Millisecond
		output := &notificationTimeOutput{}
		cmd.Stdout = output
		if err := cmd.Run(); err != nil || ctx.Err() != nil {
			return fallback
		}
		result := strings.TrimSpace(output.String())
		if !validNotificationTime(result) {
			return fallback
		}
		mutex.Lock()
		if _, exists := labels[key]; !exists {
			if len(order) >= notificationTimeCacheLimit {
				delete(labels, order[0])
				order = order[1:]
			}
			order = append(order, key)
			labels[key] = result
		}
		mutex.Unlock()
		return result
	}
}
