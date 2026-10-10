pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    objectName: "radarMap"
    implicitHeight: content.implicitHeight + 32
    property var radarState: Forecast.emptyRadar()
    property var tileClient: null
    property var imageControl: null
    property var tileImages: ({})
    property var failedTiles: ({})
    property int tileGeneration: 0
    property bool active: false
    property bool presentationActive: true
    property bool reducedMotion: false
    property string visualQuality: "full"
    property real locationLatitude: 0
    property real locationLongitude: 0
    property real viewportHeight: 650
    property bool expanded: false
    property bool playing: false
    property bool followLatest: true
    property bool historyWanted: false
    property bool imageError: false
    property bool manualLoadingVisible: false
    readonly property bool manualFramePending: active && !playing && !followLatest && !imageError && displayedFrame !== null && desiredTime !== "" && desiredTime !== displayedFrame.time
    property string desiredTime: ""
    readonly property string desiredID: {
        const frame = frames.find(f => f.time === desiredTime);
        return frame && frame.state === "ready" ? frame.id : "";
    }
    property var displayedFrame: null
    property int frontSlot: 0
    property int reloadToken: 0
    property string loadedView: ""
    property double clockNow: Date.now()
    readonly property real mapTop: content.y + mapArea.y
    readonly property real mapHeight: mapArea.height
    readonly property var frames: radarState.frames.filter(f => f.state === "ready" || f.state === "pending")
    readonly property var readyFrames: frames.filter(f => f.state === "ready")
    readonly property var bounds: radarState.view
    readonly property real span: bounds ? bounds.east - bounds.west : 1
    readonly property int zoom: bounds ? Math.max(4, Math.min(10, Math.round(Math.log(80150033.37157849 / span) / Math.LN2))) : 7
    readonly property real centerLon: bounds ? (bounds.west + bounds.east) / 2 / 20037508.342789244 * 180 : locationLongitude
    readonly property real centerLat: bounds ? Math.atan(Math.sinh((bounds.south + bounds.north) / 2 / 20037508.342789244 * Math.PI)) * 180 / Math.PI : locationLatitude
    readonly property string viewKey: bounds ? [bounds.west, bounds.south, bounds.east, bounds.north].join("/") : ""
    readonly property bool atLocation: centeredOnLocation()
    function centeredOnLocation() {
        if (!bounds || zoom !== 7)
            return false;
        const extent = 20037508.342789244, half = span / 2;
        const homeX = Math.max(-extent + half, Math.min(extent - half, locationLongitude / 180 * extent));
        const homeY = Math.max(-extent + half, Math.min(extent - half, extent * Math.asinh(Math.tan(Math.max(-85, Math.min(85, locationLatitude)) * Math.PI / 180)) / Math.PI));
        return Math.abs((bounds.west + bounds.east) / 2 - homeX) < 1 && Math.abs((bounds.south + bounds.north) / 2 - homeY) < 1;
    }
    readonly property bool canPlay: active && presentationActive && frames.length > 1 && !reducedMotion && visualQuality !== "static"
    readonly property int displayedIndex: displayedFrame ? frames.findIndex(f => f.id === displayedFrame.id) : -1
    readonly property int desiredIndex: frames.findIndex(f => f.time === desiredTime)
    readonly property string freshnessText: {
        if (radarState.status === "unsupported")
            return "Observed radar currently covers the contiguous United States.";
        if (radarState.status === "offline")
            return "Radar is unavailable offline.";
        if (radarState.status === "unavailable")
            return "Radar is temporarily unavailable. Forecast maps are still available.";
        if (radarState.status === "loading" && displayedFrame === null)
            return "Loading radar…";
        if (radarState.latest === "")
            return "Observed radar loads when this map is in view.";
        const minutes = Math.max(0, Math.floor((clockNow - Date.parse(radarState.latest)) / 60000));
        return (radarState.status === "stale" ? "Stale radar · " : "Latest radar · ") + (minutes < 1 ? "less than a minute ago" : minutes + " min ago");
    }
    signal viewRequested(real latitude, real longitude, int zoom)
    signal historyRequested(bool enabled)
    signal clearBasemap
    onHistoryWantedChanged: historyRequested(historyWanted)
    function resetLoadingFeedback() {
        manualLoadingVisible = false;
        if (manualFramePending)
            manualLoadingDelay.restart();
        else
            manualLoadingDelay.stop();
    }
    onDesiredIDChanged: resetLoadingFeedback()
    onDisplayedFrameChanged: resetLoadingFeedback()
    onPlayingChanged: {
        historyWanted = playing;
        resetLoadingFeedback();
    }
    onFollowLatestChanged: resetLoadingFeedback()
    onImageErrorChanged: resetLoadingFeedback()
    onManualFramePendingChanged: resetLoadingFeedback()
    function source(kind, id) {
        return id ? "image://radar/" + kind + "/" + id + "/" + reloadToken : "";
    }
    function resetImages() {
        playing = false;
        historyWanted = false;
        imageError = false;
        displayedFrame = null;
        desiredTime = "";
        first.source = "";
        second.source = "";
        first.frame = null;
        second.frame = null;
        frontSlot = 0;
        followLatest = true;
    }
    function syncFrames() {
        if (!active || frames.length === 0) {
            resetImages();
            return;
        }
        if (followLatest || !frames.some(f => f.time === desiredTime))
            desiredTime = frames[frames.length - 1].time;
        loadDesired();
    }
    function loadDesired() {
        if (!active || desiredID === "" || displayedFrame && displayedFrame.id === desiredID)
            return;
        const back = frontSlot === 0 ? second : first;
        if (back.status === Image.Loading)
            return; // At most one replacement request; scrubbing coalesces to the last choice.
        const frame = frames.find(f => f.id === desiredID);
        if (!frame)
            return;
        back.frame = frame;
        back.source = source("frame", frame.id);
    }
    function imageReady(slot, which) {
        if (slot.status === Image.Error && slot.frame && active) {
            if (slot.frame.id !== desiredID) {
                slot.source = "";
                slot.frame = null;
                frameSync.restart();
                return;
            }
            imageError = true;
            playing = false;
            return;
        }
        if (slot.status !== Image.Ready || !slot.frame || !active)
            return;
        imageError = false;
        if (slot.frame.id !== desiredID) {
            slot.source = "";
            slot.frame = null;
            frameSync.restart();
            return;
        }
        const old = which === 0 ? second : first;
        displayedFrame = slot.frame;
        frontSlot = which;
        old.source = "";
        old.frame = null;
    }
    function select(index) {
        if (index < 0 || index >= frames.length)
            return;
        playing = false;
        followLatest = false;
        historyWanted = true;
        desiredTime = frames[index].time;
        loadDesired();
    }
    function latest() {
        playing = false;
        historyWanted = false;
        followLatest = true;
        if (imageError) {
            ++reloadToken;
            imageError = false;
            first.source = "";
            second.source = "";
            displayedFrame = null;
        }
        syncFrames();
    }
    function moveTo(lat, lon, level) {
        playing = false;
        if (imageControl)
            imageControl.invalidate();
        resetImages();
        clearBasemap();
        viewRequested(Math.max(-85, Math.min(85, lat)), Math.max(-180, Math.min(180, lon)), Math.max(4, Math.min(10, level)));
    }
    function pan(dx, dy) {
        if (!bounds)
            return;
        const x = (bounds.west + bounds.east) / 2 + dx * span;
        const y = (bounds.south + bounds.north) / 2 + dy * span;
        moveTo(Math.atan(Math.sinh(y / 20037508.342789244 * Math.PI)) * 180 / Math.PI, x / 20037508.342789244 * 180, zoom);
    }
    readonly property var visibleTiles: basemap.visibleTiles
    onViewKeyChanged: frameSync.restart()
    onRadarStateChanged: frameSync.restart()
    onActiveChanged: if (!active)
        resetImages()
    else
        frameSync.restart()
    onCanPlayChanged: if (!canPlay)
        playing = false
    onPresentationActiveChanged: if (!presentationActive) {
        playing = false;
        historyWanted = false;
    }
    Component.onCompleted: frameSync.restart()
    Timer {
        id: manualLoadingDelay
        objectName: "radarManualLoadingDelay"
        interval: 1000
        repeat: false
        onTriggered: if (root.manualFramePending)
            root.manualLoadingVisible = true
    }
    Timer {
        id: frameSync
        interval: 0
        onTriggered: {
            // Reconcile after derived bindings settle. Metadata refreshes with
            // unchanged geography preserve both the displayed image and tiles.
            if (root.viewKey !== root.loadedView) {
                root.loadedView = root.viewKey;
                mapDrag.cancelDrag();
                root.resetImages();
                root.clearBasemap();
            }
            root.syncFrames();
        }
    }
    Timer {
        interval: 30000
        repeat: true
        running: root.active
        onTriggered: root.clockNow = Date.now()
    }
    Timer {
        objectName: "radarPlaybackTimer"
        interval: 600
        repeat: true
        running: root.playing && root.canPlay
        onTriggered: {
            if (!root.displayedFrame || root.displayedFrame.id !== root.desiredID)
                return;
            // Wait for history to arrive; pending observations stay selectable
            // without making the paused view download them on its own.
            if (root.readyFrames.length < 2)
                return;
            const index = (root.readyFrames.findIndex(f => f.id === root.displayedFrame.id) + 1) % root.readyFrames.length;
            root.desiredTime = root.readyFrames[index].time;
            root.loadDesired();
        }
    }
    ColumnLayout {
        id: content
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.margins: 16
        spacing: 10
        RowLayout {
            Layout.fillWidth: true
            PlainLabel {
                Layout.fillWidth: true
                text: "Observed radar"
                font.pixelSize: Tokens.fontSize(21)
                font.weight: Font.DemiBold
            }
            ActionButton {
                objectName: "radarExpand"
                text: root.expanded ? "Collapse" : "Expand"
                accessibleLabel: root.expanded ? "Collapse radar map" : "Expand radar map"
                onClicked: root.expanded = !root.expanded
            }
        }
        PlainLabel {
            objectName: "radarFreshness"
            Layout.fillWidth: true
            text: root.freshnessText
            color: root.radarState.status === "stale" ? Tokens.gold : Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        RowLayout {
            Layout.fillWidth: true
            ActionButton {
                objectName: "radarPrevious"
                text: "Previous"
                enabled: root.desiredIndex > 0
                onClicked: root.select(root.desiredIndex - 1)
            }
            Slider {
                objectName: "radarTimeline"
                Layout.fillWidth: true
                from: 0
                to: Math.max(0, root.frames.length - 1)
                stepSize: 1
                value: Math.max(0, root.desiredIndex)
                enabled: root.frames.length > 1
                Accessible.name: "Radar observation time"
                onMoved: root.select(Math.round(value))
            }
            ActionButton {
                objectName: "radarNext"
                text: "Next"
                enabled: root.desiredIndex >= 0 && root.desiredIndex < root.frames.length - 1
                onClicked: root.select(root.desiredIndex + 1)
            }
            ActionButton {
                objectName: "radarPlay"
                text: root.playing ? "Pause" : "Play"
                enabled: root.canPlay
                selected: root.playing
                onClicked: {
                    root.followLatest = false;
                    root.playing = !root.playing;
                }
            }
            ActionButton {
                objectName: "radarLatest"
                text: "Latest"
                enabled: root.frames.length > 0
                onClicked: root.latest()
            }
        }
        PlainLabel {
            objectName: "radarObservationTime"
            Layout.fillWidth: true
            visible: text !== ""
            text: {
                if (root.imageError)
                    return "Could not load this radar image. Choose Latest to try again.";
                if (root.displayedFrame)
                    return "Radar at " + root.displayedFrame.label + (root.manualLoadingVisible ? " · loading selected time…" : "");
                return root.active && root.desiredID !== "" ? "Loading radar image…" : "";
            }
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(13)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        Item {
            id: mapArea
            objectName: "radarMapArea"
            Layout.fillWidth: true
            Layout.preferredHeight: root.expanded ? Math.max(360, Math.min(width, root.viewportHeight - 230)) : 340
            clip: true
            activeFocusOnTab: true
            Accessible.role: Accessible.Canvas
            Accessible.name: "Observed radar map. " + (root.displayedFrame ? root.displayedFrame.label : root.freshnessText)
            Accessible.description: "Drag to pan. Arrow keys pan. Plus and minus zoom. Home returns to the viewed location. Blank areas can lack radar coverage."
            Keys.onPressed: event => {
                if (event.key === Qt.Key_Left)
                    root.pan(-0.2, 0);
                else if (event.key === Qt.Key_Right)
                    root.pan(0.2, 0);
                else if (event.key === Qt.Key_Up)
                    root.pan(0, 0.2);
                else if (event.key === Qt.Key_Down)
                    root.pan(0, -0.2);
                else if (event.key === Qt.Key_Plus || event.key === Qt.Key_Equal)
                    root.moveTo(root.centerLat, root.centerLon, root.zoom + 1);
                else if (event.key === Qt.Key_Minus)
                    root.moveTo(root.centerLat, root.centerLon, root.zoom - 1);
                else if (event.key === Qt.Key_Home)
                    root.moveTo(root.locationLatitude, root.locationLongitude, 7);
                else
                    return;
                event.accepted = true;
            }
            Rectangle {
                anchors.fill: parent
                color: "#244355"
            }
            Item {
                id: plane
                objectName: "radarMapPlane"
                transform: Translate {
                    x: mapDrag.offsetX
                    y: mapDrag.offsetY
                }
                anchors.centerIn: parent
                width: parent.width
                height: width
                RadarBasemap {
                    id: basemap
                    anchors.fill: parent
                    bounds: root.bounds
                    viewportHeight: mapArea.height
                    tileClient: root.tileClient
                    tileImages: root.tileImages
                    failedTiles: root.failedTiles
                    tileGeneration: root.tileGeneration
                    active: root.active && root.presentationActive
                    offline: root.radarState.status === "offline"
                    onResetRequested: root.clearBasemap()
                }
                Image {
                    id: first
                    objectName: "radarImageA"
                    property var frame: null
                    anchors.fill: parent
                    asynchronous: true
                    cache: false
                    visible: root.frontSlot === 0
                    opacity: 0.78
                    onStatusChanged: root.imageReady(first, 0)
                }
                Image {
                    id: second
                    objectName: "radarImageB"
                    property var frame: null
                    anchors.fill: parent
                    asynchronous: true
                    cache: false
                    visible: root.frontSlot === 1
                    opacity: 0.78
                    onStatusChanged: root.imageReady(second, 1)
                }
                Rectangle {
                    visible: root.bounds !== null
                    x: root.bounds ? ((root.locationLongitude / 180 * 20037508.342789244 - root.bounds.west) / root.span) * plane.width - 5 : 0
                    y: root.bounds ? ((root.bounds.north - 20037508.342789244 * Math.asinh(Math.tan(Math.max(-85, Math.min(85, root.locationLatitude)) * Math.PI / 180)) / Math.PI) / root.span) * plane.height - 5 : 0
                    width: 10
                    height: 10
                    radius: 5
                    color: "#298bd0"
                    border.color: "white"
                    border.width: 2
                }
            }
            MouseArea {
                id: mapDrag
                objectName: "radarMapDrag"
                anchors.fill: parent
                enabled: root.active && root.presentationActive && root.bounds !== null
                preventStealing: true
                cursorShape: pressed ? Qt.ClosedHandCursor : Qt.OpenHandCursor
                property real startX: 0
                property real startY: 0
                property real offsetX: 0
                property real offsetY: 0
                property bool dragging: false
                function cancelDrag() {
                    dragging = false;
                    offsetX = 0;
                    offsetY = 0;
                }
                onEnabledChanged: if (!enabled)
                    cancelDrag()
                onPressed: mouse => {
                    cancelDrag();
                    startX = mouse.x;
                    startY = mouse.y;
                    mapArea.forceActiveFocus();
                }
                onPositionChanged: mouse => {
                    if (!pressed)
                        return;
                    const dx = mouse.x - startX, dy = mouse.y - startY;
                    if (!dragging && Math.abs(dx) + Math.abs(dy) < drag.threshold)
                        return;
                    dragging = true;
                    root.playing = false;
                    offsetX = dx;
                    offsetY = dy;
                }
                onReleased: mouse => {
                    const moved = dragging && Math.abs(startX - mouse.x) + Math.abs(startY - mouse.y) >= drag.threshold;
                    const dx = (startX - mouse.x) / plane.width, dy = (mouse.y - startY) / plane.width;
                    cancelDrag();
                    if (moved)
                        root.pan(dx, dy);
                }
                onCanceled: cancelDrag()
            }
            PlainLabel {
                anchors.horizontalCenter: parent.horizontalCenter
                anchors.top: parent.top
                anchors.topMargin: 55
                text: "Geographic background unavailable"
                visible: root.visibleTiles.length > 0 && root.visibleTiles.every(tile => root.failedTiles[tile.key])
                color: "white"
                font.pixelSize: Tokens.fontSize(13)
            }
            Rectangle {
                anchors.fill: parent
                color: "transparent"
                border.width: mapArea.activeFocus ? 2 : 0
                border.color: Tokens.accent
            }
            Row {
                anchors.right: parent.right
                anchors.top: parent.top
                anchors.margins: 10
                spacing: 6
                ActionButton {
                    objectName: "radarZoomOut"
                    text: "−"
                    accessibleLabel: "Zoom radar out"
                    enabled: root.bounds && root.zoom > 4
                    onClicked: root.moveTo(root.centerLat, root.centerLon, root.zoom - 1)
                }
                ActionButton {
                    objectName: "radarZoomIn"
                    text: "+"
                    accessibleLabel: "Zoom radar in"
                    enabled: root.bounds && root.zoom < 10
                    onClicked: root.moveTo(root.centerLat, root.centerLon, root.zoom + 1)
                }
                ActionButton {
                    objectName: "radarRecenter"
                    text: "Recenter"
                    enabled: root.bounds !== null && !root.atLocation
                    onClicked: root.moveTo(root.locationLatitude, root.locationLongitude, 7)
                }
            }
            Rectangle {
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.margins: 6
                width: credit.implicitWidth + 12
                height: credit.implicitHeight + 6
                radius: 4
                color: "#e6f2f6"
                PlainLabel {
                    id: credit
                    anchors.centerIn: parent
                    text: "© OpenStreetMap contributors (ODbL)"
                    color: "#15384a"
                    font.pixelSize: Tokens.fontSize(11)
                }
            }
        }
        Rectangle {
            Layout.alignment: Qt.AlignHCenter
            Layout.preferredWidth: Math.min(500, content.width)
            Layout.preferredHeight: 30
            visible: root.radarState.legend !== ""
            color: "white"
            radius: 4
            Image {
                anchors.fill: parent
                source: root.source("legend", root.radarState.legend)
                asynchronous: true
                cache: false
                fillMode: Image.PreserveAspectFit
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.frames.length + " / " + root.radarState.frames.length + " observations ready" + (root.radarState.frames.some(f => f.state === "failed" || f.state === "limited") ? " · some frames unavailable" : "") + " · NOAA/NWS MRMS · reflectivity (dBZ), ~1 km grid"
            font.pixelSize: Tokens.fontSize(12)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Past radar echoes, not a rain forecast. Blank areas may lack coverage; reflectivity does not identify rain or snow."
            font.pixelSize: Tokens.fontSize(12)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
    }
}
