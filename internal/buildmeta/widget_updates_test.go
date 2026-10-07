package buildmeta

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuickshellWidgetUpdateActions(t *testing.T) {
	qs, e := exec.LookPath("quickshell")
	if e != nil {
		t.Skip("Quickshell is needed for the isolated bar interaction test")
	}
	shell := "/usr/share/omarchy/shell"
	if _, e = os.Stat(filepath.Join(shell, "Ui/PopupCard.qml")); e != nil {
		t.Skip("Omarchy UI components are needed for the bar interaction test")
	}
	dir, e := os.MkdirTemp("/tmp", "awu-bar-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	for _, module := range []string{"Ui", "Commons"} {
		if e = os.Symlink(filepath.Join(shell, module), filepath.Join(dir, module)); e != nil {
			t.Fatal(e)
		}
	}
	raw, e := os.ReadFile("../../quickshell/a-weather-app.weather/WeatherWidget.qml")
	if e != nil {
		t.Fatal(e)
	}
	writeRuntimeFixture(t, filepath.Join(dir, "WeatherWidget.qml"), raw, 0600)
	helper := `#!/usr/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$HOME/actions"
if [[ " $* " == *' --bar '* ]]; then
printf '%s\n' '{"schema_version":1,"label":"20°C","tooltip":"Saved weather","freshness":"fresh","update":{"state":"available","installed":"0.51.5","available":"0.51.9","message":"A new version is ready","checked_at":1}}'
fi
`
	writeRuntimeFixture(t, filepath.Join(dir, "helper/a-weather-app"), []byte(helper), 0700)
	qml := `import QtQuick
import Quickshell
ShellRoot {
    FloatingWindow {
        implicitWidth: 500; implicitHeight: 50
        WeatherWidget {
            id: widget
            width: 500; height: 50
            settings: ({projectPath: Quickshell.env("HOME") + "/helper", statePath: Quickshell.env("HOME") + "/state"})
        }
        Timer { interval: 800; running: true; onTriggered: {
            if (widget.update.installed !== "0.51.5" || widget.update.available !== "0.51.9" || !widget.updateNotice) throw "missing update notice";
            widget.activateWidget();
            widget.checkUpdates(true);
        } }
        Timer { interval: 1400; running: true; onTriggered: widget.installUpdate() }
        Timer { interval: 2000; running: true; onTriggered: { console.log("WEATHER_BAR_UPDATE_ACTIONS_OK"); Qt.quit(); } }
    }
}
`
	writeRuntimeFixture(t, filepath.Join(dir, "shell.qml"), []byte(qml), 0600)
	for _, name := range []string{"runtime", "config", "state"} {
		if e = os.Mkdir(filepath.Join(dir, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, qs, "--path", filepath.Join(dir, "shell.qml"), "--no-color")
	cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+filepath.Join(dir, "config"), "XDG_STATE_HOME="+filepath.Join(dir, "state"), "XDG_RUNTIME_DIR="+filepath.Join(dir, "runtime"), "HYPRLAND_INSTANCE_SIGNATURE=", "WAYLAND_DISPLAY=", "DISPLAY=", "QT_QPA_PLATFORM=offscreen", "QT_QPA_PLATFORMTHEME=generic", "QT_QUICK_CONTROLS_STYLE=Basic", "QT_IM_MODULE=none", "QT_QUICK_BACKEND=software")
	output, e := cmd.CombinedOutput()
	if e != nil || !strings.Contains(string(output), "WEATHER_BAR_UPDATE_ACTIONS_OK") {
		t.Fatal("bar fixture failed", e, string(output))
	}
	for _, problem := range []string{"TypeError:", "ReferenceError:", "Unable to assign", "missing update notice"} {
		if strings.Contains(string(output), problem) {
			t.Fatal("bar fixture reported a QML error", string(output))
		}
	}
	actions, e := os.ReadFile(filepath.Join(dir, "actions"))
	if e != nil || !strings.Contains(string(actions), "--force-update-check") || !strings.Contains(string(actions), "--install-update") {
		t.Fatal("widget actions did not invoke update helpers", e, string(actions), string(output))
	}
}
