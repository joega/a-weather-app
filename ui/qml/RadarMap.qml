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
    property bool imageError: false
    property string desiredID: ""
    property var displayedFrame: null
    property int frontSlot: 0
    property int reloadToken: 0
    property string loadedView: ""
    property double clockNow: Date.now()
    readonly property real mapTop: content.y + mapArea.y
    readonly property real mapHeight: mapArea.height
    readonly property var frames: radarState.frames.filter(f => f.state === "ready")
    readonly property var bounds: radarState.view
    readonly property real span: bounds ? bounds.east - bounds.west : 1
    readonly property int zoom: bounds ? Math.max(4, Math.min(10, Math.round(Math.log(80150033.37157849 / span) / Math.LN2))) : 7
    readonly property real centerLon: bounds ? (bounds.west + bounds.east) / 2 / 20037508.342789244 * 180 : locationLongitude
    readonly property real centerLat: bounds ? Math.atan(Math.sinh((bounds.south + bounds.north) / 2 / 20037508.342789244 * Math.PI)) * 180 / Math.PI : locationLatitude
    readonly property string viewKey: bounds ? [bounds.west, bounds.south, bounds.east, bounds.north].join("/") : ""
    readonly property bool canPlay: active && presentationActive && frames.length > 1 && !reducedMotion && visualQuality !== "static"
    readonly property int displayedIndex: displayedFrame ? frames.findIndex(f => f.id === displayedFrame.id) : -1
    readonly property int desiredIndex: frames.findIndex(f => f.id === desiredID)
    readonly property string freshnessText: {
        if (radarState.status === "unsupported")
            return "Observed radar currently covers the contiguous United States.";
        if (radarState.status === "offline")
            return "Radar is unavailable offline.";
        if (radarState.status === "unavailable")
            return "Radar is temporarily unavailable. Forecast maps are still available.";
        if (radarState.status === "loading")
            return "Loading observed radar…";
        if (radarState.latest === "")
            return "Observed radar loads when this map is in view.";
        const minutes = Math.max(0, Math.floor((clockNow - Date.parse(radarState.latest)) / 60000));
        return (radarState.status === "stale" ? "Stale radar · " : "Latest composite · ") + (minutes < 1 ? "less than a minute ago" : minutes + " min ago") + (radarState.refreshing ? " · updating" : "");
    }
    signal viewRequested(real latitude, real longitude, int zoom)
    signal clearBasemap
    function source(kind, id) {
        return id ? "image://radar/" + kind + "/" + id + "/" + reloadToken : "";
    }
    function resetImages() {
        playing = false;
        imageError = false;
        displayedFrame = null;
        desiredID = "";
        first.source = "";
        second.source = "";
        first.frame = null;
        second.frame = null;
        frontSlot = 0;
        followLatest = true;
        ++reloadToken;
    }
    function syncFrames() {
        if (!active || frames.length === 0) {
            resetImages();
            return;
        }
        if (followLatest || !frames.some(f => f.id === desiredID))
            desiredID = frames[frames.length - 1].id;
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
        playing = false;
        followLatest = false;
        if (index >= 0 && index < frames.length) {
            desiredID = frames[index].id;
            loadDesired();
        }
    }
    function latest() {
        playing = false;
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
    function tiles() {
        if (!active || !bounds)
            return [];
        const n = Math.pow(2, zoom), unit = 40075016.68557849 / n;
        const left = (bounds.west + 20037508.342789244) / unit;
        const top = (20037508.342789244 - bounds.north) / unit;
        let result = [];
        for (let y = Math.floor(top); y < Math.ceil(top + 2); ++y)
            for (let x = Math.floor(left); x < Math.ceil(left + 2); ++x)
                if (x >= 0 && y >= 0 && x < n && y < n)
                    result.push({
                        key: zoom + "/" + x + "/" + y,
                        x: x,
                        y: y,
                        left: (x - left) / 2,
                        top: (y - top) / 2
                    });
        return result;
    }
    readonly property var visibleTiles: tiles()
    function requestTiles() {
        if (!tileClient || !active)
            return;
        for (const tile of visibleTiles)
            tileClient.request(zoom, tile.x, tile.y, radarState.status === "offline");
    }
    onVisibleTilesChanged: tileSync.restart()
    onTileGenerationChanged: tileSync.restart()
    onRadarStateChanged: {
        if (viewKey !== loadedView) {
            loadedView = viewKey;
            resetImages();
            clearBasemap();
        }
        frameSync.restart();
    }
    onActiveChanged: if (!active)
        resetImages()
    else
        frameSync.restart()
    onCanPlayChanged: if (!canPlay)
        playing = false
    Component.onCompleted: {
        requestTiles();
        syncFrames();
    }
    Timer {
        id: frameSync
        interval: 0
        onTriggered: root.syncFrames()
    }
    Timer {
        id: tileSync
        interval: 0
        onTriggered: root.requestTiles()
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
            const index = (root.displayedIndex + 1) % root.frames.length;
            root.desiredID = root.frames[index].id;
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
            text: root.imageError ? "Could not display the selected frame. Choose Latest to retry." : root.displayedFrame ? "Observed " + root.displayedFrame.label + (root.desiredID !== root.displayedFrame.id ? " · loading selected frame…" : "") : "Waiting for an observed frame…"
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
            Accessible.description: "Arrow keys pan. Plus and minus zoom. Home returns to the viewed location. Blank areas can lack radar coverage."
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
                anchors.centerIn: parent
                width: parent.width
                height: width
                Repeater {
                    model: root.visibleTiles
                    Image {
                        required property var modelData
                        x: modelData.left * plane.width
                        y: modelData.top * plane.height
                        width: plane.width / 2 + 0.5
                        height: width
                        source: root.tileImages[modelData.key] || ""
                        asynchronous: true
                        cache: false
                    }
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
                anchors.fill: parent
                property real startX: 0
                property real startY: 0
                onPressed: mouse => {
                    startX = mouse.x;
                    startY = mouse.y;
                    mapArea.forceActiveFocus();
                }
                onReleased: mouse => {
                    const dx = (startX - mouse.x) / width, dy = (mouse.y - startY) / width;
                    if (Math.abs(dx) + Math.abs(dy) > 0.02)
                        root.pan(dx, dy);
                }
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
