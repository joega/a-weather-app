// Package updater coordinates published releases independently of the weather service.
package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joega/a-weather-app/internal/safeio"
)

// CheckInterval is the minimum interval between automatic release checks.
const CheckInterval = 24 * time.Hour
const statusFile = "update-status.json"

var versionPattern = regexp.MustCompile(`^v?(0)\.([1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Pin binds a stable release tag to its archive digest and source revision.
// Validate it before trusting its paths, revision, or digest.
type Pin struct {
	SchemaVersion int    `json:"schemaVersion"`
	Tag           string `json:"tag"`
	ArchiveSHA256 string `json:"archive_sha256"`
	SourceCommit  string `json:"source_commit"`
}

// Validate rejects unsupported schemas, unstable versions, and malformed hashes.
func (p Pin) Validate() error {
	if p.SchemaVersion != 1 || !strings.HasPrefix(p.Tag, "v") || versionPattern.FindStringSubmatch(p.Tag) == nil ||
		!regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(p.ArchiveSHA256) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(p.SourceCommit) {
		return errors.New("invalid published release pin")
	}
	return nil
}

// Compare versions numerically, never lexically (0.51.10 follows 0.51.9).
func Compare(a, b string) (int, error) {
	x, y := versionPattern.FindStringSubmatch(a), versionPattern.FindStringSubmatch(b)
	if x == nil || y == nil {
		return 0, errors.New("invalid release version")
	}
	for i := 1; i < len(x); i++ {
		n, e := strconv.ParseUint(x[i], 10, 32)
		m, f := strconv.ParseUint(y[i], 10, 32)
		if e != nil || f != nil {
			return 0, errors.New("release version exceeds bound")
		}
		if n < m {
			return -1, nil
		}
		if n > m {
			return 1, nil
		}
	}
	return 0, nil
}

// Status is the persisted updater notice. CheckedAt is Unix seconds.
// Pin is retained internally and deliberately omitted by Map.
type Status struct {
	State     string `json:"state"`
	Installed string `json:"installed"`
	Available string `json:"available"`
	Message   string `json:"message"`
	CheckedAt int64  `json:"checked_at"`
	Pin       *Pin   `json:"pin,omitempty"`
}

// Map returns a fresh UI status object without the internal release pin.
func (s Status) Map() map[string]any {
	return map[string]any{"state": s.State, "installed": s.Installed, "available": s.Available, "message": s.Message, "checked_at": s.CheckedAt}
}

// Source resolves the supported release pin and latest published stable tag.
// Published must honor cancellation and must not return an unverified pin.
type Source interface {
	Published(context.Context) (Pin, string, error)
}

// Config describes a release installation and its private saved state.
// Nil Source uses GitHubSource; nil Now uses time.Now. Development disables
// release installation/checks. File locks serialize checks across processes.
type Config struct {
	Installed   string
	Development bool
	StatePath   string
	Source      Source
	Now         func() time.Time
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
func (c Config) base() Status {
	s := Status{State: "idle", Installed: c.Installed}
	if c.Development {
		s.State = "development"
		s.Message = "Development checkout · build updates locally"
	}
	return s
}

// Status reads a validated notice or returns a safe default if it is absent
// or corrupt. Stale worker progress becomes a retryable failure notice.
func (c Config) Status() Status {
	base := c.base()
	if c.Development {
		return base
	}
	v, e := safeio.Read(c.StatePath+"/"+statusFile, 16384)
	if e != nil {
		return base
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return base
	}
	var s Status
	if json.Unmarshal(raw, &s) != nil || s.CheckedAt < 0 || s.CheckedAt > c.now().Add(time.Minute).Unix() {
		return base
	}
	switch s.State {
	case "idle", "current", "available", "publishing", "failed", "checking", "downloading", "verifying", "restarting", "updated", "rolled_back":
	default:
		return base
	}
	if s.Pin != nil && s.Pin.Validate() != nil {
		return base
	}
	if len(s.Message) > 512 || len(s.Available) > 32 {
		return base
	}
	if (s.State == "downloading" || s.State == "verifying" || s.State == "restarting") && !c.workerActive() {
		s.State = "failed"
		s.Message = "The previous update stopped. Open the app to recover, or retry the update."
	}
	if s.Installed != c.Installed {
		if s.State == "restarting" && s.Pin != nil && strings.TrimPrefix(s.Pin.Tag, "v") == c.Installed {
			return Status{State: "restarting", Installed: c.Installed, Message: "Completing the update…", CheckedAt: s.CheckedAt}
		}
		return base
	}
	return s
}

func (c Config) workerActive() bool {
	d, e := safeio.OpenDir(c.StatePath, false)
	if e != nil {
		return false
	}
	defer d.Close()
	f, e := d.LockExisting("update-install.lock")
	if f != nil {
		f.Close()
	}
	return errors.Is(e, syscall.EWOULDBLOCK)
}

// PresentationStatus hides an update success already shown by a frontend. Keep
// the worker's status intact so acknowledgment cannot overwrite a newer update.
func (c Config) PresentationStatus() Status {
	s := c.Status()
	if s.State == "updated" {
		seen, err := safeio.Read(c.StatePath+"/update-notice-seen.json", 1024)
		if err == nil && seen["installed"] == s.Installed {
			s.State = "current"
			s.Message = "You’re up to date. Updates are checked daily."
		}
	}
	return s
}

// AcknowledgeUpdate persists the version actually displayed, independently of
// worker progress. A stale frontend cannot acknowledge a different release.
func (c Config) AcknowledgeUpdate(installed string) error {
	if installed != c.Installed || c.Status().State != "updated" {
		return errors.New("update notice does not match the installed update")
	}
	return safeio.Write(c.StatePath+"/update-notice-seen.json", map[string]any{"installed": installed}, 1024)
}

// Save atomically persists a bounded updater notice in the private state directory.
func (c Config) Save(s Status) error { return safeio.Write(c.StatePath+"/"+statusFile, s, 16384) }

// Check serializes bar and application checks. A failed automatic check preserves
// the last useful result and still backs off for a day; manual checks can retry.
func (c Config) Check(ctx context.Context, force bool) (Status, error) {
	if c.Development {
		return c.base(), nil
	}
	dir, e := safeio.OpenDir(c.StatePath, true)
	if e != nil {
		return c.base(), e
	}
	defer dir.Close()
	installLock, e := dir.Lock("update-install.lock")
	if errors.Is(e, syscall.EWOULDBLOCK) {
		return c.Status(), nil
	}
	if e != nil {
		return c.base(), e
	}
	defer installLock.Close()
	lock, e := dir.Lock("update-check.lock")
	if errors.Is(e, syscall.EWOULDBLOCK) {
		return c.Status(), nil
	}
	if e != nil {
		return c.base(), e
	}
	defer lock.Close()
	s := c.Status()
	now := c.now()
	if !force && s.CheckedAt > 0 && now.Sub(time.Unix(s.CheckedAt, 0)) < CheckInterval {
		return s, nil
	}
	source := c.Source
	if source == nil {
		source = GitHubSource{}
	}
	pin, latest, e := source.Published(ctx)
	s.CheckedAt = now.Unix()
	if e != nil {
		if force {
			s.State = "failed"
			s.Message = "Could not check for updates. Check your connection and try again."
		}
		return s, c.Save(s)
	}
	if e = pin.Validate(); e != nil {
		return s, e
	}
	cmp, e := Compare(c.Installed, pin.Tag)
	if e != nil {
		return s, e
	}
	s.Available = strings.TrimPrefix(pin.Tag, "v")
	s.Pin = &pin
	s.Message = ""
	if cmp < 0 {
		s.State = "available"
		s.Message = "A new version is ready to install"
	} else {
		s.State = "current"
		s.Available = ""
		s.Pin = nil
	}
	if n, e := Compare(latest, pin.Tag); e == nil && n > 0 && cmp >= 0 {
		s.State = "publishing"
		s.Message = "A new release is being prepared. It will be offered when its verified release pin is ready."
	}
	return s, c.Save(s)
}

// GitHubSource reads the official repository supported pin and stable release.
// Nil Client uses a bounded client with redirects refused; injected clients
// are responsible for equivalent timeout and redirect protections.
type GitHubSource struct{ Client *http.Client }

func (g GitHubSource) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 5 {
				return errors.New("unsafe redirect")
			}
			return nil
		}}
	}
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set("User-Agent", "a-weather-app-updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release request: HTTP %d", resp.StatusCode)
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(raw)) > limit {
		return nil, errors.New("release response exceeds bound")
	}
	return raw, nil
}

// Published resolves a pin whose tag is supported by a published stable release.
func (g GitHubSource) Published(ctx context.Context) (Pin, string, error) {
	raw, e := g.get(ctx, "https://api.github.com/repos/joega/a-weather-app/releases/latest", 1024*1024)
	if e != nil {
		return Pin{}, "", e
	}
	v, e := safeio.Object(raw, 1024*1024)
	if e != nil {
		return Pin{}, "", e
	}
	tag, _ := v["tag_name"].(string)
	if v["draft"] != false || v["prerelease"] != false || !strings.HasPrefix(tag, "v") || versionPattern.FindStringSubmatch(tag) == nil {
		return Pin{}, "", errors.New("not a stable release")
	}
	raw, e = g.get(ctx, "https://raw.githubusercontent.com/joega/a-weather-app/main/packaging/release-lock.json", 4096)
	if e != nil {
		return Pin{}, "", e
	}
	lock, e := safeio.Object(raw, 4096)
	if e != nil || len(lock) != 4 {
		return Pin{}, "", errors.New("invalid release pin")
	}
	var pin Pin
	if json.Unmarshal(raw, &pin) != nil || pin.Validate() != nil {
		return Pin{}, "", errors.New("invalid release pin")
	}
	if n, e := Compare(pin.Tag, tag); e != nil || n > 0 {
		return Pin{}, "", errors.New("release publication is not consistent yet")
	}
	// /latest is informational; installation is always selected by the reviewed pin.
	// The installer must independently verify that the pin's release assets exist.
	return pin, tag, nil
}
