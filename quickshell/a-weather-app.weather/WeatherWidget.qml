import QtQuick
import Quickshell
import Quickshell.Io
import qs.Commons
import qs.Ui as OmarchyUi

OmarchyUi.BarWidget {
    id: root
    moduleName: "a-weather-app.weather"
    // Resolve from this entry point, never the shared shell's working directory.
    // Explicit settings remain supported for earlier development installations.
    readonly property string projectPath: safePath(setting("projectPath", sourceRoot()))
    readonly property string statePath: safePath(setting("statePath", defaultStatePath()))
    readonly property string instance: safeSelector(setting("instance", ""), /^[a-f0-9]+_[0-9]+_[0-9]+$/)
    readonly property string output: safeSelector(setting("output", ""), /^[A-Za-z0-9_.:-]{1,128}$/)
    readonly property bool configured: projectPath !== "" && statePath !== ""
    readonly property bool opened: app.running
    property string label: "A Weather App"
    property string tooltip: "Open A Weather App to choose a location"
    property string buffer: ""
    property bool rejected: false
    property bool pendingOpen: false
    property bool pendingRead: false
    property var update: ({state: "unsupported", installed: "", available: "", message: "Open the app for version information", checked_at: 0})
    property bool manualUpdateCheck: false
    property bool waitingForUpdater: false
    property string updateError: ""
    readonly property bool updateBusy: updateInstaller.running || waitingForUpdater || ["downloading", "verifying", "restarting"].indexOf(update.state) >= 0
    readonly property bool updateNotice: ["available", "failed", "rolled_back", "downloading", "verifying", "restarting"].indexOf(update.state) >= 0
    implicitWidth: textItem.implicitWidth + Style.space(14)
    implicitHeight: barSize
    activeFocusOnTab: configured
    Accessible.role: Accessible.Button
    Accessible.name: "Toggle A Weather App window"
    Accessible.description: plain(tooltip, 256)
    Accessible.onPressAction: activateWidget()
    Keys.onReturnPressed: activateWidget()
    Keys.onEnterPressed: activateWidget()
    Keys.onSpacePressed: activateWidget()
    Keys.onPressed: event => { if (event.key === Qt.Key_Menu) { updatePopup.open = !updatePopup.open; event.accepted = true; } }

    function activateWidget() {
        if (updateNotice) updatePopup.open = !updatePopup.open;
        else toggle();
    }

    function safePath(value) {
        return typeof value === "string" && value.length > 1 && value.length <= 4096 && /^\/[^\x00-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]+$/.test(value)
            && value.split("/").indexOf("..") === -1 ? value : "";
    }
    function sourceRoot() {
        const url = Qt.resolvedUrl("../../").toString();
        if (!url.startsWith("file:///")) return "";
        try { return safePath(decodeURIComponent(url.slice(7)).replace(/\/$/, "")); }
        catch (error) { return ""; }
    }
    function defaultStatePath() {
        const state = Quickshell.env("XDG_STATE_HOME");
        const home = safePath(Quickshell.env("HOME"));
        // An invalid explicitly supplied XDG path is refused, not silently ignored.
        const base = state ? safePath(state) : (home ? home.replace(/\/$/, "") + "/.local/state" : "");
        return base && base.startsWith("/") ? base.replace(/\/$/, "") + "/a-weather-app" : "";
    }
    function safeSelector(value, expression) {
        return typeof value === "string" && value.length <= 256 && expression.test(value) ? value : "";
    }
    function plain(value, limit) {
        return typeof value === "string" ? value.replace(/[<>&\x00-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/g, "").slice(0, limit) : "";
    }
    function refresh() {
        if (!configured) return;
        if (reader.running) { pendingRead = true; return; }
        pendingRead = false;
        buffer = ""; rejected = false; reader.running = true; readDeadline.restart();
    }
    function open() {
        if (!configured || app.running) return;
        if (backgroundRefresh.running) {
            pendingOpen = true;
            backgroundRefresh.signal(15);
            refreshKill.restart();
            return;
        }
        app.running = true;
    }
    function close() { if (app.running) app.signal(15); }
    function toggle() {
        if (!configured || windowToggle.running) return;
        if (app.running) { windowToggle.running = true; toggleDeadline.restart(); }
        else open();
    }
    function acceptRead() {
        try {
            const value = JSON.parse(buffer);
            if (!value || typeof value !== "object" || Array.isArray(value) || value.schema_version !== 1 || typeof value.label !== "string" || value.label.length > 96
                    || typeof value.tooltip !== "string" || value.tooltip.length > 256
                    || ["fresh", "stale", "expired", "invalid_future", "unavailable"].indexOf(value.freshness) < 0) throw "invalid";
            label = plain(value.label, 96); tooltip = plain(value.tooltip, 256);
            const u = value.update;
            if (u && typeof u === "object" && !Array.isArray(u)
                    && ["idle", "current", "available", "publishing", "failed", "checking", "downloading", "verifying", "restarting", "updated", "rolled_back", "development", "unsupported"].indexOf(u.state) >= 0
                    && typeof u.installed === "string" && u.installed.length <= 32
                    && typeof u.available === "string" && u.available.length <= 32
                    && typeof u.message === "string" && u.message.length <= 512
                    && typeof u.checked_at === "number" && isFinite(u.checked_at) && u.checked_at >= 0 && Math.floor(u.checked_at) === u.checked_at) {
                update = {state: u.state, installed: plain(u.installed, 32), available: plain(u.available, 32), message: plain(u.message, 512), checked_at: u.checked_at};
                if (["downloading", "verifying", "restarting", "failed", "rolled_back", "updated"].indexOf(u.state) >= 0) {
                    waitingForUpdater = false; updaterStartDeadline.stop();
                }
            }
        } catch (error) { label = "—° · Unavailable"; tooltip = "A Weather App forecast unavailable"; }
    }
    function refreshSaved() {
        if (configured && !backgroundRefresh.running && !app.running) {
            backgroundRefresh.running = true;
            refreshDeadline.restart();
        }
    }
    function checkUpdates(manual) {
        if (configured && !updateChecker.running && !updateBusy) {
            manualUpdateCheck = manual === true; updateError = "";
            updateChecker.running = true; updateDeadline.restart();
        }
    }
    function installUpdate() {
        if (!configured || updateBusy || update.available === "") return;
        updateError = ""; updateInstaller.running = true;
    }
    onConfiguredChanged: { if (configured) { refresh(); refreshSaved(); checkUpdates(); } }
    Component.onCompleted: { refresh(); refreshSaved(); checkUpdates(); }
    Component.onDestruction: {
        if (windowToggle.running) windowToggle.signal(15);
        if (reader.running) reader.signal(15);
        if (backgroundRefresh.running) backgroundRefresh.signal(15);
        if (app.running) app.signal(15);
        if (updateChecker.running) updateChecker.signal(15);
        if (updateInstaller.running) updateInstaller.signal(15);
    }
    Timer { interval: 30000; repeat: true; running: root.configured; onTriggered: root.refresh() }
    // Watch the directory so the first controls save and atomic replacements
    // of saved settings or location/forecast data all update the bar immediately.
    // Only the bounded helper reads and validates the saved files.
    FileView {
        path: root.configured ? root.statePath : ""
        preload: false
        watchChanges: true
        printErrors: false
        onFileChanged: root.refresh()
    }
    Timer { interval: 300000; repeat: true; running: root.configured; onTriggered: root.refreshSaved() }
    // The helper persists the daily deadline, shared with the native app. Keep
    // update requests separate from the weather refresh's shorter deadline.
    Timer { interval: 3600000; repeat: true; running: root.configured; onTriggered: root.checkUpdates() }
    Timer { id: updateDeadline; interval: 30000; onTriggered: { if (updateChecker.running) { updateChecker.signal(15); updateKill.restart(); } } }
    Timer { id: updateKill; interval: 1000; onTriggered: { if (updateChecker.running) updateChecker.signal(9); } }
    Timer { id: updaterStartDeadline; interval: 60000; onTriggered: { root.waitingForUpdater = false; root.updateError = "The updater did not start. Check your connection and try again."; } }
    Timer { id: readDeadline; interval: 3000; onTriggered: { root.rejected = true; reader.signal(15); readKill.restart(); } }
    Timer { id: readKill; interval: 1000; onTriggered: { if (reader.running) reader.signal(9); } }
    Timer { id: toggleDeadline; interval: 3000; onTriggered: { if (windowToggle.running) windowToggle.signal(9); } }
    Timer { id: refreshDeadline; interval: 35000; onTriggered: { if (backgroundRefresh.running) { backgroundRefresh.signal(15); refreshKill.restart(); } } }
    Timer { id: refreshKill; interval: 1000; onTriggered: { if (backgroundRefresh.running) backgroundRefresh.signal(9); } }
    Process {
        id: windowToggle
        // Let the launcher resolve symlinks just as it does when starting Qt.
        command: [root.projectPath + "/a-weather-app", "--toggle-window", "--state-dir", root.statePath]
        onExited: (code, status) => toggleDeadline.stop()
    }
    Process {
        id: reader
        command: [root.projectPath + "/a-weather-app", "--state-dir", root.statePath, "--bar"]
        stdout: SplitParser {
            splitMarker: ""
            onRead: chunk => {
                if (root.rejected) return;
                if (chunk.length > 4096 || root.buffer.length + chunk.length > 4096) {
                    root.rejected = true; root.buffer = ""; reader.signal(15); readKill.restart();
                } else root.buffer += chunk;
            }
        }
        // The fixed helper emits only a bounded fixed failure message; never
        // collect arbitrary stderr inside the shared shell process.
        onExited: (code, status) => {
            readDeadline.stop(); readKill.stop();
            if (code === 0 && !root.rejected) root.acceptRead();
            else { root.label = "—° · Unavailable"; root.tooltip = "A Weather App forecast unavailable"; }
            root.buffer = "";
            if (root.pendingRead) Qt.callLater(root.refresh);
        }
    }
    Process {
        id: updateChecker
        command: {
            let args = [root.projectPath + "/a-weather-app", "--state-dir", root.statePath, "--check-updates"];
            if (root.manualUpdateCheck) args.push("--force-update-check");
            return args;
        }
        onExited: (code, status) => {
            updateDeadline.stop(); updateKill.stop();
            if (code !== 0 && root.manualUpdateCheck) root.updateError = "Could not check for updates. Check your connection and try again.";
            root.refresh();
        }
    }
    Process {
        id: updateInstaller
        command: [root.projectPath + "/a-weather-app", "--state-dir", root.statePath, "--install-update"]
        onExited: (code, status) => {
            if (code === 0) { root.waitingForUpdater = true; updaterStartDeadline.restart(); }
            else root.updateError = "Update could not start. Check your connection and ensure the plugin has no local changes, then try again.";
            root.refresh();
        }
    }
    Process {
        id: backgroundRefresh
        command: [root.projectPath + "/a-weather-app", "--state-dir", root.statePath, "--refresh-bar"]
        onExited: (code, status) => {
            refreshDeadline.stop(); refreshKill.stop(); root.refresh();
            if (root.pendingOpen) { root.pendingOpen = false; Qt.callLater(root.open); }
        }
    }
    Process {
        id: app
        command: {
            let args = [root.projectPath + "/a-weather-app", "--state-dir", root.statePath];
            if (root.instance !== "") args.push("--instance", root.instance);
            if (root.output !== "") args.push("--output", root.output);
            return args;
        }
        onExited: (code, status) => root.refresh()
    }
    Text {
        id: textItem
        anchors.centerIn: parent
        textFormat: Text.PlainText
        text: (root.vertical ? "☁" : root.label) + (root.updateNotice ? " ↑" : "")
        color: root.bar ? root.bar.barForeground : "#eef4fc"
        font.family: root.bar ? root.bar.fontFamily : "sans-serif"
        font.pixelSize: Style.font.body
    }
    MouseArea {
        anchors.fill: parent
        hoverEnabled: true
        cursorShape: root.configured ? Qt.PointingHandCursor : Qt.ArrowCursor
        acceptedButtons: Qt.LeftButton | Qt.RightButton
        onClicked: mouse => { root.focus = false; if (mouse.button === Qt.RightButton) updatePopup.open = !updatePopup.open; else root.activateWidget(); }
        onEntered: { if (root.bar) root.bar.showTooltip(root, root.plain(root.tooltip, 256)); }
        onExited: { if (root.bar) root.bar.hideTooltip(root); }
    }
    OmarchyUi.PopupCard {
        id: updatePopup
        anchorItem: root
        bar: root.bar
        contentWidth: fittedContentWidth(Style.space(400))
        contentHeight: fittedContentHeight(updateColumn.implicitHeight)
        Column {
            id: updateColumn
            width: parent.width
            spacing: Style.space(10)
            Text { width: parent.width; text: "A Weather App updates"; textFormat: Text.PlainText; color: Color.foreground; font.pixelSize: Style.font.body; font.bold: true }
            Text { width: parent.width; text: "Installed " + (root.update.installed || "unknown") + (root.update.available ? " · Available " + root.update.available : ""); textFormat: Text.PlainText; wrapMode: Text.Wrap; color: Color.foreground; font.pixelSize: Style.font.body }
            Text {
                width: parent.width
                text: root.updateError || (updateInstaller.running ? "Downloading and verifying the update…" : root.waitingForUpdater ? "Starting the update…" : updateChecker.running ? "Checking for updates…" : root.update.message || "You have the latest supported release")
                textFormat: Text.PlainText; wrapMode: Text.Wrap; color: Color.foreground; font.pixelSize: Style.font.body
            }
            OmarchyUi.Button { text: "Update and restart"; visible: root.update.available !== ""; enabled: !root.updateBusy && !updateChecker.running; focusable: true; bordered: true; onClicked: root.installUpdate() }
            OmarchyUi.Button { text: "Check for updates"; enabled: !root.updateBusy && !updateChecker.running && root.update.state !== "development"; focusable: true; bordered: true; onClicked: root.checkUpdates(true) }
            OmarchyUi.Button { text: "Open forecast"; enabled: !root.updateBusy; focusable: true; onClicked: { updatePopup.open = false; root.open(); } }
            OmarchyUi.Button { text: "Close"; focusable: true; onClicked: updatePopup.open = false }
        }
    }
}
