package buildmeta

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQuickshellWidgetUpdateActions(t *testing.T) {
	for _, packaged := range []bool{false, true} {
		t.Run(fmt.Sprintf("packaged=%t", packaged), func(t *testing.T) { testQuickshellWidgetUpdateActions(t, packaged) })
	}
}

func testQuickshellWidgetUpdateActions(t *testing.T, packaged bool) {
	qs, e := exec.LookPath("quickshell")
	if e != nil {
		if os.Getenv("WEATHER_REQUIRE_BAR_TEST") == "1" {
			t.Fatal("Quickshell is required for release widget checks")
		}
		t.Skip("Quickshell is needed for the isolated bar interaction test")
	}
	shell := os.Getenv("OMARCHY_SHELL_ROOT")
	if shell == "" {
		shell = "/usr/share/omarchy/shell"
	}
	if _, e = os.Stat(filepath.Join(shell, "Ui/PopupCard.qml")); e != nil {
		if os.Getenv("WEATHER_REQUIRE_BAR_TEST") == "1" {
			t.Fatal("Omarchy modules are required for release widget checks", e)
		}
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
	translations, err := os.ReadFile("../../quickshell/a-weather-app.weather/Translations.js")
	if err != nil {
		t.Fatal(err)
	}
	writeRuntimeFixture(t, filepath.Join(dir, "Translations.js"), translations, 0600)
	if packaged {
		cmd := exec.Command("bash", "../../scripts/package_widget.sh", filepath.Join(dir, "WeatherWidget.qml"))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(output))
		}
		if err := os.Remove(filepath.Join(dir, "Translations.js")); err != nil {
			t.Fatal(err)
		}
	}
	helper := `#!/usr/bin/bash
set -euo pipefail
printf '%s %s\n' "$LANGUAGE" "$*" >> "$HOME/actions"
if [[ " $* " == *' --bar '* ]]; then
printf '%s\n' '{"schema_version":1,"label":"20°C","tooltip":"Saved weather","freshness":"fresh","update":{"state":"available","installed":"0.51.5","available":"0.51.9","message":"A new version is ready","checked_at":1}}'
fi
`
	writeRuntimeFixture(t, filepath.Join(dir, "helper/a-weather-app"), []byte(helper), 0700)
	qml := `import QtQuick
import Quickshell
import qs.Commons as Commons
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
            if (widget.shellLanguage !== "es") throw "missing Omarchy language";
            if (widget.tr("Check for updates") === "Check for updates") throw "missing bundled translations";
            Commons.I18n.languageFile.setText("fr\n");
            Commons.I18n.languageFile.reload();
            widget.activateWidget();
            widget.checkUpdates(true);
        } }
        Timer { interval: 1400; running: true; onTriggered: {
            if (widget.shellLanguage !== "fr" || widget.language !== "fr") throw "missing live Omarchy language switch";
            if (widget.tr("Check for updates") !== "Rechercher des mises à jour") throw "missing live translation";
            widget.installUpdate();
        } }
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
	writeRuntimeFixture(t, filepath.Join(dir, "config/omarchy/language"), []byte("es\n"), 0600)
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
	if e != nil || !strings.Contains(string(actions), "--force-update-check") || !strings.Contains(string(actions), "fr --state-dir") || !strings.Contains(string(actions), "--install-update") {
		t.Fatal("widget actions did not invoke update helpers", e, string(actions), string(output))
	}
}
