package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The network fetch is redirected to a private repository, but preflight,
// fast-forward, validation and rollback use the real git commands.
func TestPluginRuntimeUpdateAndRollback(t *testing.T) {
	for _, scenario := range []string{"success", "validation_failure", "dirty"} {
		t.Run(scenario, func(t *testing.T) {
			l, pin := managedFixture(t)
			remote := filepath.Join(privateState(t), "upstream")
			plugin := filepath.Join(privateState(t), "plugin")
			git := func(args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
				raw, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("git fixture: %v: %s", e, raw)
				}
				return strings.TrimSpace(string(raw))
			}
			git("init", "-b", "main", remote)
			file := filepath.Join(remote, "widget.qml")
			if e := os.WriteFile(file, []byte("old widget"), 0600); e != nil {
				t.Fatal(e)
			}
			git("-C", remote, "add", ".")
			git("-C", remote, "commit", "-m", "old plugin")
			oldCommit := git("-C", remote, "rev-parse", "HEAD")
			git("clone", remote, plugin)
			git("-C", plugin, "remote", "set-url", "origin", "https://github.com/joega/a-weather-app.git")
			if e := os.Mkdir(filepath.Join(remote, "packaging"), 0700); e != nil {
				t.Fatal(e)
			}
			pinBytes, _ := json.Marshal(pin)
			if e := os.WriteFile(filepath.Join(remote, "packaging/release-lock.json"), pinBytes, 0600); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(file, []byte("new widget"), 0600); e != nil {
				t.Fatal(e)
			}
			git("-C", remote, "add", ".")
			git("-C", remote, "commit", "-m", "supported release")
			newCommit := git("-C", remote, "rev-parse", "HEAD")
			l.PluginRoot = plugin
			l.command = func(ctx context.Context, name string, args ...string) (string, error) {
				if name == "omarchy-plugin-validate" {
					if scenario == "validation_failure" {
						return "", errors.New("fixture validator rejected plugin")
					}
					return "", nil
				}
				if name == "git" && len(args) > 2 && args[2] == "fetch" {
					args = []string{"-C", plugin, "fetch", "--quiet", remote, "refs/heads/main"}
				}
				return (&LinuxInstallation{}).run(ctx, name, args...)
			}
			userFile := filepath.Join(plugin, "my-notes")
			if scenario == "dirty" {
				if e := os.WriteFile(userFile, []byte("preserve my work"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			f := &fileInstallation{LinuxInstallation: l}
			err := (Engine{Config: l.Config, Installation: f}).Run(context.Background())
			if (err != nil) != (scenario != "success") {
				t.Fatal("unexpected result", err)
			}
			wantCommit, wantRuntime := oldCommit, "releases/v0.51.5"
			if scenario == "success" {
				wantCommit, wantRuntime = newCommit, "releases/v0.51.9"
			}
			if got := git("-C", plugin, "rev-parse", "HEAD"); got != wantCommit {
				t.Fatal("plugin revision", got, wantCommit)
			}
			if got, e := os.Readlink(filepath.Join(l.DataRoot, "current")); e != nil || got != wantRuntime {
				t.Fatal("runtime selection", got, e)
			}
			if scenario == "dirty" {
				raw, e := os.ReadFile(userFile)
				if e != nil || !bytes.Equal(raw, []byte("preserve my work")) {
					t.Fatal("user changes lost", e)
				}
				if len(f.started) != 0 {
					t.Fatal("dirty plugin caused app restart")
				}
			}
			if scenario == "validation_failure" && l.Config.Status().State != "rolled_back" {
				t.Fatal(l.Config.Status())
			}
		})
	}
}
