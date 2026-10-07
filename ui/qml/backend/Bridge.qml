import QtQuick
import "../Forecast.js" as Forecast

Item {
    id: root
    // Contract: connected/diagnostic, start/send/disconnectService and lifecycle signals.
    required property var weatherTransport
    visible: false
    property var snapshot: null
    property var weatherMap: ({
            status: "closed",
            offline: false,
            error: "",
            data: null
        })
    property bool mapWanted: false
    property bool presentationActive: true
    property bool subscribed: false
    property var queuedPresentation: null
    onPresentationActiveChanged: {
        if (subscribed)
            send("set_presentation", {
                active: presentationActive
            });
    }
    property double lastSnapshotRevision: 0
    property string error: ""
    readonly property int operationGraceMs: 120000
    readonly property int shutdownGraceMs: 90000
    readonly property bool diagnostic: root.weatherTransport.diagnostic
    property int nextId: 0
    property int pending: -1
    property string pendingOp: ""
    property bool closing: false
    property bool disconnected: false
    property bool shutdownFailed: false
    property bool quitAcknowledged: false
    property bool closeReported: false
    readonly property bool available: root.weatherTransport.connected && !disconnected && !closing
    property bool stopQueued: false
    property string queuedUserOp: ""
    property var queuedUserPatch: null
    property string queuedMapOp: ""
    property var queuedSearch: null
    property bool cancelSearchQueued: false
    property bool busy: closing || disconnected || stopQueued || queuedUserOp !== "" || (pending >= 0 && ["snapshot", "subscribe", "search_places", "cancel_place_search", "set_presentation"].indexOf(pendingOp) < 0)
    signal closed(int exitCode)
    signal toggleWindow
    function send(op, patch) {
        if (closing && op !== "quit")
            return false;
        if (!root.weatherTransport.connected || disconnected) {
            if (op !== "snapshot")
                error = "Weather service is unavailable for this action";
            return false;
        }
        if (pending >= 0) {
            if (op === "snapshot")
                return false;
            if (op === "set_presentation") {
                queuedPresentation = patch.active;
                return true;
            }
            if (op === "map_open" || op === "map_close") {
                queuedMapOp = op === pendingOp ? "" : op;
                return true;
            }
            if (op === "cancel_place_search") {
                queuedSearch = null;
                cancelSearchQueued = true;
                return true;
            }
            if (op === "search_places") {
                queuedSearch = {
                    query: patch.query,
                    country_code: patch.country_code,
                    client_token: patch.client_token
                };
                return true;
            }
            if (op === "stop_effects") {
                stopQueued = true;
                if (queuedUserOp === "start_effects" || queuedUserOp === "start_live_effects") {
                    queuedUserOp = "";
                    queuedUserPatch = null;
                    error = "Pending effects start cancelled by Stop";
                }
                return true;
            }
            if (queuedUserOp !== "") {
                error = "A control action is already pending. Try again.";
                return false;
            }
            queuedUserOp = op;
            queuedUserPatch = patch ? JSON.parse(JSON.stringify(patch)) : null;
            return true;
        }
        let id = nextId;
        nextId = (nextId + 1) % 2147483648;
        pendingOp = op;
        pending = id;
        let request = {
            version: 1,
            request_id: id,
            op: op
        };
        if (op === "set_presentation")
            request.active = patch.active;
        if (op === "set_controls")
            request.controls = patch;
        if (op === "set_notifications")
            request.notifications = patch;
        if (op === "set_location")
            request.location = patch;
        if (op === "search_places")
            request.search = patch;
        if (op === "select_output")
            request.output = patch.output;
        if (op === "start_effects")
            request.duration = 300;
        if (!root.weatherTransport.send(request)) {
            fail("Weather service could not accept the action");
            return false;
        }
        deadline.restart();
        return true;
    }
    function openMap() {
        mapWanted = true;
        weatherMap = {
            status: "loading",
            offline: false,
            error: "",
            data: null
        };
        return send("map_open");
    }
    function closeMap() {
        mapWanted = false;
        weatherMap = {
            status: "closed",
            offline: false,
            error: "",
            data: null
        };
        return send("map_close");
    }
    function drainUserAction() {
        if (closing) {
            if (pending < 0)
                send("quit");
            return;
        }
        if (stopQueued) {
            stopQueued = false;
            send("stop_effects");
            return;
        }
        if (queuedPresentation !== null) {
            let active = queuedPresentation;
            queuedPresentation = null;
            send("set_presentation", {
                active: active
            });
            return;
        }
        if (queuedMapOp !== "") {
            let op = queuedMapOp;
            queuedMapOp = "";
            send(op);
            return;
        }
        if (queuedUserOp !== "") {
            let op = queuedUserOp, patch = queuedUserPatch;
            queuedUserOp = "";
            queuedUserPatch = null;
            send(op, patch);
            return;
        }
        if (cancelSearchQueued) {
            cancelSearchQueued = false;
            send("cancel_place_search");
            return;
        }
        if (queuedSearch !== null) {
            let search = queuedSearch;
            queuedSearch = null;
            send("search_places", search);
        }
    }
    function clearQueuedActions() {
        queuedPresentation = null;
        queuedUserOp = "";
        queuedUserPatch = null;
        queuedMapOp = "";
        queuedSearch = null;
        cancelSearchQueued = false;
        stopQueued = false;
    }
    function finishClose(exitCode) {
        if (closeReported)
            return;
        closeReported = true;
        deadline.stop();
        closeTimer.stop();
        clearQueuedActions();
        closed(exitCode);
    }
    function fail(message) {
        if (diagnostic)
            console.log("Weather service bridge failed:", message);
        shutdownFailed = true;
        disconnected = true;
        error = message;
        pending = -1;
        pendingOp = "";
        clearQueuedActions();
        deadline.stop();
        root.weatherTransport.disconnectService();
        if (closing)
            finishClose(1);
    }
    function applySnapshot(raw) {
        let revision = raw.snapshot_revision;
        if (typeof revision !== "number" || !Number.isSafeInteger(revision) || revision < 1)
            throw Error("Invalid snapshot revision");
        if (revision <= lastSnapshotRevision)
            return;
        let next = Forecast.snapshot(raw);
        lastSnapshotRevision = revision;
        snapshot = next;
        if (diagnostic)
            console.log("Weather service snapshot accepted:", snapshot.source.freshness);
    }
    function accept(value) {
        if (disconnected || closeReported)
            return;
        try {
            Forecast.boundedTree(value);
            if (value.event !== undefined) {
                if (value.event === "toggle_window")
                    toggleWindow();
                else if (value.event === "map") {
                    if (mapWanted)
                        weatherMap = Forecast.weatherMap(value.map);
                } else if (value.event === "service_stopped") {
                    closing = true;
                    quitAcknowledged = value.ok;
                    shutdownFailed = !value.ok;
                    finishClose(value.ok ? 0 : 1);
                } else
                    applySnapshot(value.snapshot);
                return;
            }
            if (value.request_id !== pending)
                throw Error("Unexpected response");
            let completedOp = pendingOp;
            pendingOp = "";
            pending = -1;
            deadline.stop();
            if (completedOp === "subscribe" && value.ok) {
                subscribed = true;
                queuedPresentation = presentationActive;
            }
            if (value.snapshot)
                applySnapshot(value.snapshot);
            if (completedOp === "quit") {
                quitAcknowledged = value.ok;
                shutdownFailed = !value.ok;
                if (!value.ok)
                    finishClose(1);
                return;
            }
            if (!closing) {
                if (value.ok)
                    error = "";
                else
                    error = completedOp === "set_location" ? "Location change could not start. Try again." : completedOp === "start_effects" || completedOp === "start_live_effects" ? "Desktop effects could not start. Check compatibility in Settings." : completedOp === "stop_effects" ? "Desktop effects could not stop" : completedOp === "check_effects" || completedOp === "select_output" ? "Stop desktop effects before changing setup." : ["set_notifications", "snooze_notifications", "resume_notifications"].indexOf(completedOp) >= 0 ? "Notification settings could not be saved. Reopen the app and try again." : "Weather service rejected the request";
            }
            drainUserAction();
        } catch (e) {
            if (diagnostic)
                console.log("Weather service snapshot validation:", String(e).substring(0, 160));
            fail("Weather service returned invalid data");
        }
    }
    function shutdown() {
        if (closing)
            return;
        closing = true;
        error = "Closing weather app…";
        clearQueuedActions();
        if (!root.weatherTransport.connected || disconnected) {
            finishClose(shutdownFailed || disconnected ? 1 : 0);
            return;
        }
        closeTimer.restart();
        if (pending < 0)
            send("quit");
    }
    Connections {
        target: root.weatherTransport
        function onReady() {
            root.subscribed = false;
            root.lastSnapshotRevision = 0;
            root.send("subscribe");
        }
        function onMessage(json) {
            root.accept(JSON.parse(json));
        }
        function onShutdownRequested() {
            root.shutdown();
        }
        function onUnavailable(message) {
            root.disconnected = true;
            root.pending = -1;
            root.pendingOp = "";
            root.clearQueuedActions();
            deadline.stop();
            if (root.closing) {
                root.finishClose(root.shutdownFailed || !root.quitAcknowledged ? 1 : 0);
            } else
                root.error = message;
        }
    }
    Timer {
        id: deadline
        objectName: "bridgeRequestDeadline"
        interval: root.operationGraceMs
        onTriggered: root.fail("Weather request timed out")
    }
    Timer {
        id: closeTimer
        objectName: "bridgeCloseDeadline"
        interval: root.shutdownGraceMs
        onTriggered: root.fail("Weather service shutdown timed out")
    }
    Component.onCompleted: root.weatherTransport.start()
}
