import QtQuick
import "../Forecast.js" as Forecast
import "../Outdoor.js" as Outdoor

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
    property var weatherRadar: Forecast.emptyRadar()
    property bool radarWanted: false
    property int radarToken: 0
    property int pendingRadarToken: -1
    property var queuedRadar: null
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
    property var warningTarget: null
    property var warningDetail: null
    property string warningState: "closed"
    property string warningError: ""
    property int warningGeneration: 0
    property int pendingWarningGeneration: -1
    property var pendingWarningTarget: null
    property var queuedWarning: null
    property var outdoorResult: null
    property string outdoorState: "closed"
    property string outdoorError: ""
    property int outdoorGeneration: 0
    property int pendingOutdoorGeneration: -1
    property var pendingOutdoorQuery: null
    property var queuedOutdoor: null
    property bool busy: closing || disconnected || stopQueued || queuedUserOp !== "" || (pending >= 0 && ["snapshot", "subscribe", "search_places", "cancel_place_search", "set_presentation"].indexOf(pendingOp) < 0)
    signal closed(int exitCode)
    signal toggleWindow
    signal warningRequested(var reference, string activationToken)
    function loadOutdoor(preferences) {
        outdoorGeneration++;
        outdoorResult = null;
        outdoorError = "";
        outdoorState = "loading";
        if (!snapshot || !snapshot.forecast || !send("outdoor_plan", Outdoor.query(snapshot, preferences === undefined ? null : preferences))) {
            outdoorState = "unavailable";
            outdoorError = "Connect and load a forecast to find outdoor times.";
        }
    }
    function closeOutdoor() {
        outdoorGeneration++;
        queuedOutdoor = null;
        outdoorResult = null;
        outdoorState = "closed";
        outdoorError = "";
    }
    function invalidateOutdoor(message) {
        if (outdoorState === "closed")
            return;
        outdoorGeneration++;
        queuedOutdoor = null;
        outdoorResult = null;
        outdoorState = "unavailable";
        outdoorError = message;
    }
    function loadWarning(reference) {
        warningGeneration++;
        warningTarget = Forecast.warningReference(reference);
        warningDetail = null;
        warningError = "";
        warningState = "loading";
        if (!send("warning_detail", warningTarget)) {
            warningState = "unavailable";
            warningError = "Warning details are unavailable while the weather service is disconnected.";
        }
    }
    function closeWarning() {
        warningGeneration++;
        warningTarget = null;
        warningDetail = null;
        warningState = "closed";
        warningError = "";
        queuedWarning = null;
    }
    function send(op, patch) {
        if (closing && op !== "quit")
            return false;
        if (!root.weatherTransport.connected || disconnected) {
            if (op !== "snapshot")
                error = "Weather service is unavailable for this action";
            return false;
        }
        if (pending >= 0) {
            if (op === "outdoor_plan") {
                queuedOutdoor = JSON.parse(JSON.stringify(patch));
                return true;
            }
            if (op === "warning_detail") {
                queuedWarning = Forecast.warningReference(patch);
                return true;
            }
            if (op === "snapshot")
                return false;
            if (op === "set_presentation") {
                queuedPresentation = patch.active;
                return true;
            }
            if (op === "radar_view" || op === "radar_close") {
                queuedRadar = {
                    op: op,
                    patch: patch ? JSON.parse(JSON.stringify(patch)) : null
                };
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
        if (op === "acknowledge_update")
            request.installed = patch.installed;
        if (op === "set_presentation")
            request.active = patch.active;
        if (op === "set_controls")
            request.controls = patch;
        if (op === "set_notifications" || op === "set_warning_notifications")
            request.notifications = patch;
        if (op === "warning_detail") {
            request.warning = Forecast.warningReference(patch);
            pendingWarningTarget = request.warning;
            pendingWarningGeneration = warningGeneration;
        }
        if (op === "outdoor_plan") {
            request.plan = patch;
            pendingOutdoorQuery = patch;
            pendingOutdoorGeneration = outdoorGeneration;
        }
        if (op === "radar_view") {
            request.view = patch;
            pendingRadarToken = patch.client_token;
        }
        if (op === "set_location" || op === "add_location" || op === "saved_location")
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
    function openRadar(latitude, longitude, zoom) {
        radarWanted = true;
        radarToken = (radarToken + 1) % 2147483648;
        weatherRadar = Forecast.emptyRadar("loading");
        if (latitude === null || longitude === null || Math.abs(latitude) > 85) {
            weatherRadar = Forecast.emptyRadar("unsupported");
            return false;
        }
        return send("radar_view", {
            latitude: latitude,
            longitude: longitude,
            zoom: zoom,
            client_token: radarToken
        });
    }
    function closeRadar() {
        radarWanted = false;
        weatherRadar = Forecast.emptyRadar();
        return send("radar_close");
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
        if (queuedRadar !== null) {
            const next = queuedRadar;
            queuedRadar = null;
            send(next.op, next.patch);
            return;
        }
        if (queuedUserOp !== "") {
            let op = queuedUserOp, patch = queuedUserPatch;
            queuedUserOp = "";
            queuedUserPatch = null;
            send(op, patch);
            return;
        }
        if (queuedWarning !== null) {
            const reference = queuedWarning;
            queuedWarning = null;
            send("warning_detail", reference);
            return;
        }
        if (queuedOutdoor !== null) {
            const next = queuedOutdoor;
            queuedOutdoor = null;
            send("outdoor_plan", next);
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
        queuedRadar = null;
        queuedSearch = null;
        cancelSearchQueued = false;
        stopQueued = false;
        queuedWarning = null;
        queuedOutdoor = null;
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
        invalidateOutdoor("Weather service disconnected. Reopen the app to find outdoor times.");
        if (warningState !== "closed") {
            warningDetail = null;
            warningState = "unavailable";
            warningError = "Warning details are unavailable while the weather service is disconnected.";
        }
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
        if (outdoorState !== "closed" && Outdoor.context(snapshot) !== Outdoor.context(next))
            invalidateOutdoor("The place or forecast changed. Find times again using the latest forecast.");
        if (snapshot && snapshot.warning_notifications.settings.enabled && !next.warning_notifications.settings.enabled && warningTarget !== null) {
            warningGeneration++;
            queuedWarning = null;
            warningDetail = null;
            warningState = "unavailable";
            warningError = "Warning monitoring was turned off. Recent notices have been cleared.";
        }
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
                else if (value.event === "warning_open") {
                    const reference = Forecast.warningReference(value.warning);
                    if (!closing)
                        warningRequested(reference, typeof value.activation_token === "string" ? value.activation_token : "");
                } else if (value.event === "map") {
                    if (mapWanted)
                        weatherMap = Forecast.weatherMap(value.map);
                } else if (value.event === "radar") {
                    if (radarWanted) {
                        const next = Forecast.radarState(value.radar);
                        if (next.client_token === radarToken)
                            weatherRadar = next;
                    }
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
            // Clear the ID first: an empty op with a live ID would briefly
            // make busy true and cancel a search through locationBusy bindings.
            pending = -1;
            pendingOp = "";
            deadline.stop();
            if (completedOp === "subscribe" && value.ok) {
                subscribed = true;
                queuedPresentation = presentationActive;
            }
            if (value.snapshot)
                applySnapshot(value.snapshot);
            if (completedOp === "radar_view" && !value.ok && radarWanted && pendingRadarToken === radarToken)
                weatherRadar = Forecast.emptyRadar("unavailable");
            if (completedOp === "quit") {
                quitAcknowledged = value.ok;
                shutdownFailed = !value.ok;
                if (!value.ok)
                    finishClose(1);
                return;
            }
            if (completedOp === "warning_detail") {
                let detail = value.ok ? Forecast.warningDetail(value.warning) : null;
                if (detail && (detail.location !== pendingWarningTarget.location || detail.key !== pendingWarningTarget.key))
                    throw Error("Warning reply does not match its request");
                if (!closing && warningTarget !== null && pendingWarningGeneration === warningGeneration) {
                    warningDetail = detail;
                    warningState = detail ? "ready" : "unavailable";
                    warningError = detail ? "" : value.error === "warning_detail_too_large" ? "This original notice exceeds the detail size limit. Its instructions have not been shortened." : "This notice is no longer available in this session. It may have expired from the recent list, or monitoring was turned off. Check the current alert feed.";
                }
                pendingWarningTarget = null;
                pendingWarningGeneration = -1;
                drainUserAction();
                return;
            }
            if (completedOp === "outdoor_plan") {
                const plan = value.ok ? Outdoor.result(value.outdoor) : null;
                if (plan && (plan.forecast_at !== pendingOutdoorQuery.forecast_at || plan.timezone !== pendingOutdoorQuery.timezone))
                    throw Error("Outdoor reply does not match its request");
                if (!closing && outdoorState !== "closed" && pendingOutdoorGeneration === outdoorGeneration) {
                    outdoorResult = plan;
                    outdoorState = plan ? "ready" : "unavailable";
                    outdoorError = plan ? "" : value.error === "outdoor_context_changed" ? "The place or forecast changed. Find times again using the latest forecast." : "Outdoor times are unavailable for this forecast.";
                }
                pendingOutdoorQuery = null;
                pendingOutdoorGeneration = -1;
                drainUserAction();
                return;
            }
            if (!closing) {
                if (value.ok)
                    error = "";
                else
                    error = value.error === "location_limit" ? "You can save up to 20 places. Remove one before adding another." : value.error === "offline" ? "Adding a place requires an internet connection. Saved places remain available." : value.error === "save_unconfirmed" ? "The change is visible, but saving could not be confirmed." : completedOp === "saved_location" ? "The saved place could not be changed. Try again." : completedOp === "set_location" || completedOp === "add_location" ? "Location change could not start. Try again." : completedOp === "start_effects" || completedOp === "start_live_effects" ? "Desktop effects could not start. Check compatibility in Settings." : completedOp === "stop_effects" ? "Desktop effects could not stop" : completedOp === "check_effects" || completedOp === "select_output" ? "Stop desktop effects before changing setup." : ["set_notifications", "snooze_notifications", "resume_notifications", "set_warning_notifications", "pause_warning_notifications", "resume_warning_notifications"].indexOf(completedOp) >= 0 ? "Notification settings could not be saved. Reopen the app and try again." : "Weather service rejected the request";
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
        closeOutdoor();
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
            root.invalidateOutdoor("Weather service disconnected. Reopen the app to find outdoor times.");
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
