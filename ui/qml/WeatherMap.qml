pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

ColumnLayout {
    id: root
    objectName: "weatherMaps"
    spacing: 12
    property var mapState: ({
            status: "closed",
            offline: false,
            error: "",
            data: null,
            hour_labels: [],
            fetched_label: null
        })
    property var radarState: Forecast.emptyRadar()
    property var imageControl: null
    property real locationLatitude: 0
    property real locationLongitude: 0
    signal radarViewRequested(real latitude, real longitude, int zoom)
    property string location: ""
    property string units: "F"
    property string windUnits: "auto"
    property bool hasLocation: false
    property bool active: false
    property string visualQuality: "full"
    property bool reducedMotion: false
    property real viewportTop: 0
    property real viewportHeight: 0
    property alias layerIndex: layerTabs.currentIndex
    readonly property string selectedLayer: ["radar", "precipitation", "temperature", "wind"][layerTabs.currentIndex] || "radar"
    property string previousLayer: "radar"
    property bool defaultLayer: true
    property bool adjustingDefault: false
    function resetDefaultLayer() {
        adjustingDefault = true;
        defaultLayer = true;
        layerTabs.currentIndex = 0;
        adjustingDefault = false;
    }
    onLocationChanged: resetDefaultLayer()
    onRadarStateChanged: defaultLayerSync.restart()
    function applyDefaultFallback() {
        // Forecast maps remain the useful default outside observed-radar coverage.
        // A user's explicit tab choice is never changed by a refresh.
        if (defaultLayer && radarState.status === "unsupported") {
            adjustingDefault = true;
            layerTabs.currentIndex = 1;
            adjustingDefault = false;
        }
    }
    onSelectedLayerChanged: {
        playing = false;
        if (selectedLayer === "radar" || previousLayer === "radar")
            clearTiles();
        previousLayer = selectedLayer;
    }
    property int hourIndex: 0
    property bool playing: false
    property var tileImages: ({})
    property var failedTiles: ({})
    property real radarHeight: 630
    property int tileGeneration: 0
    // Contract: request/close and tileReady/tileFailed signals; null disables tiles.
    property var tileClient: null
    readonly property var mapData: mapState.data || null
    readonly property bool canPlay: active && !reducedMotion && visualQuality !== "static" && mapData !== null && mapData.hours.length > 1
    readonly property var hourTickLabels: compactHourLabels()
    function compactHourLabels() {
        const labels = mapData ? mapState.hour_labels : [];
        const parts = labels.map(label => label.match(/^(.*), (\d{1,2}):(\d{2}) (AM|PM) (\S+)$/));
        const firstDate = parts.length && parts[0] ? parts[0][1] : "";
        return parts.map((part, index) => {
            if (!part)
                return labels[index];
            const clock = part[2] + (part[3] === "00" ? "" : ":" + part[3]) + " " + part[4];
            const repeated = parts.some((other, otherIndex) => otherIndex !== index && other && other[1] === part[1] && other[2] === part[2] && other[3] === part[3] && other[4] === part[4]);
            return (part[1] !== firstDate ? part[1].split(" ")[0] + " " : "") + clock + (repeated ? " " + part[5] : "");
        });
    }
    function startPlayback() {
        if (canPlay)
            playing = true;
    }
    function stopPlayback() {
        playing = false;
        hourIndex = 0;
    }
    function selectHour(index) {
        playing = false;
        hourIndex = index;
    }
    function cardVisible(card) {
        return active && mapData !== null && mapView.y + card.y + card.height >= viewportTop - 32 && mapView.y + card.y <= viewportTop + viewportHeight + 32;
    }
    function flowVisible(card) {
        const top = mapView.y + card.y + card.mapTop;
        return active && top + card.mapHeight > viewportTop && top < viewportTop + viewportHeight;
    }
    function clearTiles() {
        if (tileClient)
            tileClient.close();
        tileImages = {};
        failedTiles = {};
        ++tileGeneration;
    }
    onActiveChanged: if (!active) {
        stopPlayback();
        clearTiles();
    }
    onVisualQualityChanged: if (visualQuality === "static")
        playing = false
    onReducedMotionChanged: if (reducedMotion)
        playing = false
    onMapDataChanged: {
        stopPlayback();
        clearTiles();
    }
    Component.onDestruction: if (tileClient)
        tileClient.close()
    Timer {
        id: defaultLayerSync
        interval: 0
        onTriggered: root.applyDefaultFallback()
    }
    Timer {
        interval: 1000
        repeat: true
        running: root.playing && root.canPlay
        onTriggered: root.hourIndex = (root.hourIndex + 1) % root.mapData.hours.length
    }
    Connections {
        target: root.tileClient
        ignoreUnknownSignals: true
        function onTileReady(key, dataURL) {
            let next = Object.assign({}, root.tileImages);
            next[key] = dataURL;
            root.tileImages = next;
        }
        function onTileFailed(key) {
            let next = Object.assign({}, root.failedTiles);
            next[key] = true;
            root.failedTiles = next;
        }
    }
    RowLayout {
        Layout.fillWidth: true
        PlainLabel {
            Layout.fillWidth: true
            text: "Local weather maps · " + root.location
            font.pixelSize: Tokens.fontSize(21)
            font.weight: Font.DemiBold
            elide: Text.ElideRight
        }
        PlainLabel {
            text: root.selectedLayer === "radar" ? "Past observations" : root.mapData ? root.mapData.hours.length + " hours" : ""
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(13)
        }
    }
    TabBar {
        id: layerTabs
        objectName: "mapLayerTabs"
        Layout.fillWidth: true
        // The layout owns the bar width; its tabs divide that available space.
        implicitWidth: 0
        currentIndex: 0
        onCurrentIndexChanged: if (!root.adjustingDefault)
            root.defaultLayer = false
        spacing: 6
        background: Item {}
        Repeater {
            model: ["Radar", "Precipitation", "Temperature", "Wind"]
            TabButton {
                id: tab
                required property string modelData
                required property int index
                objectName: "mapLayerTab" + index
                text: modelData
                Accessible.name: modelData === "Radar" ? "Observed radar map" : modelData + " forecast map"
                width: (layerTabs.availableWidth - layerTabs.spacing * 3) * [0.20, 0.32, 0.30, 0.18][index]
                implicitHeight: Math.max(40, implicitContentHeight + topPadding + bottomPadding)
                background: Rectangle {
                    radius: 10
                    color: tab.checked ? "#704fa6d4" : tab.down ? "#80506a80" : "#303d5a70"
                    border.color: tab.activeFocus ? Tokens.accent : tab.checked ? "#7bd6fc" : Tokens.border
                    border.width: tab.activeFocus ? 2 : 1
                }
                contentItem: PlainLabel {
                    text: tab.text
                    horizontalAlignment: Text.AlignHCenter
                    verticalAlignment: Text.AlignVCenter
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
        }
    }
    RowLayout {
        visible: root.selectedLayer !== "radar"
        Layout.fillWidth: true
        ActionButton {
            Layout.alignment: Qt.AlignTop
            objectName: "mapPreviousHour"
            text: "Previous"
            enabled: root.mapData && root.hourIndex > 0
            onClicked: root.selectHour(root.hourIndex - 1)
        }
        ColumnLayout {
            id: hourRail
            Layout.fillWidth: true
            Layout.alignment: Qt.AlignTop
            spacing: 0
            readonly property int count: root.mapData ? root.mapData.hours.length : 0
            readonly property real minimumLabelSpacing: {
                // Reading the font also keeps measurements reactive to text size.
                const font = hourFont.font;
                return Math.max(0, ...root.hourTickLabels.map(label => hourFont.advanceWidth(label))) + 12;
            }
            readonly property int labelStride: Math.max(1, Math.ceil(minimumLabelSpacing / Math.max(1, timeline.tickTravel / Math.max(1, count - 1))))
            FontMetrics {
                id: hourFont
                font.family: "sans-serif"
                font.pixelSize: Tokens.fontSize(11)
            }
            Slider {
                id: timeline
                objectName: "mapTimeline"
                Layout.fillWidth: true
                implicitHeight: Math.max(40, Tokens.fontSize(28))
                padding: 0
                from: 0
                to: root.mapData ? root.mapData.hours.length - 1 : 0
                stepSize: 1
                snapMode: Slider.SnapAlways
                value: root.hourIndex
                enabled: root.mapData && root.mapData.hours.length > 1
                hoverEnabled: true
                readonly property real tickStart: leftPadding + handle.width / 2
                readonly property real tickTravel: Math.max(0, availableWidth - handle.width)
                readonly property int previewIndex: Math.max(0, Math.min(to, Math.round((hoverProbe.point.position.x - tickStart) / Math.max(1, tickTravel) * to)))
                Accessible.name: "Forecast map hour"
                Accessible.description: root.mapData ? root.mapState.hour_labels[root.hourIndex] : "Forecast map not loaded"
                onMoved: root.selectHour(Math.round(value))
                background: Item {
                    Rectangle {
                        x: timeline.tickStart
                        y: parent.height / 2 - 2
                        width: timeline.tickTravel
                        height: 4
                        radius: 2
                        color: "#556c8090"
                        Rectangle {
                            width: parent.width * timeline.visualPosition
                            height: parent.height
                            radius: parent.radius
                            color: Tokens.accent
                        }
                    }
                    Repeater {
                        model: hourRail.count
                        Rectangle {
                            required property int index
                            objectName: "mapHourTick_" + index
                            x: timeline.tickStart + timeline.tickTravel * index / Math.max(1, hourRail.count - 1) - width / 2
                            y: parent.height / 2 - height / 2
                            width: 1
                            height: 10
                            color: index === root.hourIndex ? Tokens.foreground : Tokens.secondary
                            opacity: timeline.enabled ? 0.85 : 0.45
                            Accessible.ignored: true
                        }
                    }
                }
                handle: Rectangle {
                    x: timeline.leftPadding + timeline.visualPosition * timeline.tickTravel
                    y: timeline.topPadding + (timeline.availableHeight - height) / 2
                    width: 16
                    height: 16
                    radius: 8
                    color: timeline.pressed ? Tokens.accent : Tokens.foreground
                    border.color: timeline.activeFocus ? Tokens.accent : "#30435e"
                    border.width: timeline.activeFocus ? 2 : 1
                    opacity: timeline.enabled ? 1 : 0.45
                }
                HoverHandler {
                    id: hoverProbe
                }
                ToolTip {
                    id: preview
                    visible: timeline.enabled && hoverProbe.hovered
                    text: root.mapData ? root.mapState.hour_labels[timeline.pressed ? root.hourIndex : timeline.previewIndex] : ""
                    delay: 150
                    padding: 10
                    contentItem: PlainLabel {
                        text: preview.text
                        color: Tokens.foreground
                        font.pixelSize: Tokens.fontSize(13)
                    }
                    background: Rectangle {
                        radius: 8
                        color: "#162b3e"
                        border.color: Tokens.border
                    }
                }
            }
            Item {
                Layout.fillWidth: true
                implicitHeight: hourFont.height + 4
                visible: hourRail.count > 0
                Repeater {
                    model: hourRail.count
                    PlainLabel {
                        required property int index
                        objectName: "mapHourLabel_" + index
                        visible: index === 0 || index === hourRail.count - 1 || (index % hourRail.labelStride === 0 && hourRail.count - 1 - index >= hourRail.labelStride && x >= hourFont.advanceWidth(root.hourTickLabels[0] || "") + 6 && x + width <= parent.width - hourFont.advanceWidth(root.hourTickLabels[hourRail.count - 1] || "") - 6)
                        text: root.hourTickLabels[index] || ""
                        font.pixelSize: hourFont.font.pixelSize
                        x: Math.max(0, Math.min(parent.width - width, timeline.tickStart + timeline.tickTravel * index / Math.max(1, hourRail.count - 1) - width / 2))
                        color: Tokens.secondary
                        Accessible.ignored: true
                    }
                }
            }
        }
        ActionButton {
            Layout.alignment: Qt.AlignTop
            objectName: "mapNextHour"
            text: "Next"
            enabled: root.mapData && root.hourIndex < root.mapData.hours.length - 1
            onClicked: root.selectHour(root.hourIndex + 1)
        }
        ActionButton {
            Layout.alignment: Qt.AlignTop
            objectName: "mapPlayback"
            text: root.playing ? "Stop" : "Play"
            accessibleLabel: root.playing ? "Stop map playback and return to the current hour" : "Play the map forecast timeline"
            selected: root.playing
            enabled: root.canPlay
            onClicked: root.playing ? root.stopPlayback() : root.startPlayback()
        }
    }
    PlainLabel {
        objectName: "mapSelectedTime"
        visible: root.selectedLayer !== "radar"
        Layout.fillWidth: true
        text: root.mapData ? "Forecast valid " + root.mapState.hour_labels[root.hourIndex] + " · Hour " + (root.hourIndex + 1) + " of " + root.mapData.hours.length : root.hasLocation ? "Local model maps load as you scroll here" : "Choose a location to see local maps"
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: Tokens.fontSize(14)
        color: Tokens.secondary
    }
    Loader {
        id: mapView
        objectName: "selectedMapView"
        Layout.fillWidth: true
        Layout.preferredHeight: root.selectedLayer === "radar" ? root.radarHeight : 396
        // Keep section geometry stable while releasing hidden map objects.
        active: root.active
        sourceComponent: root.selectedLayer === "radar" ? radarComponent : forecastComponent
    }
    PlainLabel {
        Layout.fillWidth: true
        visible: root.selectedLayer !== "radar" && root.mapData !== null
        text: root.mapData ? root.mapData.model_name + " · native grid ~" + Forecast.distance(root.mapData.resolution_km * 1000, root.units) + " · 5×5 samples requested ~" + Forecast.distance(root.mapData.radius_miles * 1609.344 / 2, root.units) + " apart · smooth visual interpolation, no added forecast detail · fetched " + root.mapState.fetched_label + (root.mapState.status === "stale" ? " · stale" : "") + (root.mapState.offline ? " · offline" : "") : ""
        font.pixelSize: Tokens.fontSize(12)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    PlainLabel {
        Layout.fillWidth: true
        visible: root.selectedLayer !== "radar" && root.mapData !== null
        text: root.mapData ? root.mapData.attribution + (root.mapData.resolution_km >= 10 ? " · Coarse global pattern; neighborhood detail unavailable." : "") : ""
        font.pixelSize: Tokens.fontSize(11)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }

    Component {
        id: forecastComponent
        WeatherMapCard {
            id: selectedCard
            objectName: root.selectedLayer === "precipitation" ? "mapPrecipitationModule" : root.selectedLayer === "temperature" ? "mapTemperatureModule" : "mapWindModule"
            mapLayer: root.selectedLayer
            mapData: root.mapData
            mapStatus: root.mapState.status
            hasLocation: root.hasLocation
            offline: root.mapState.offline
            hourIndex: root.hourIndex
            units: root.units
            windUnits: root.windUnits
            visualQuality: root.visualQuality
            reducedMotion: root.reducedMotion
            presentationActive: root.flowVisible(selectedCard)
            tileImages: root.tileImages
            failedTiles: root.failedTiles
            tileClient: root.tileClient
            tileGeneration: root.tileGeneration
            tileActive: root.cardVisible(selectedCard)
        }
    }
    Component {
        id: radarComponent
        RadarMap {
            id: radarCard
            failedTiles: root.failedTiles
            // The column can briefly report padding-only height while it is
            // being constructed. Keep the placeholder until the map is laid out
            // so viewport demand cannot collapse and destroy its own loader.
            onImplicitHeightChanged: if (implicitHeight >= 372)
                root.radarHeight = implicitHeight
            radarState: root.radarState
            imageControl: root.imageControl
            tileClient: root.tileClient
            tileImages: root.tileImages
            tileGeneration: root.tileGeneration
            active: root.active
            presentationActive: root.flowVisible(radarCard)
            reducedMotion: root.reducedMotion
            visualQuality: root.visualQuality
            locationLatitude: root.locationLatitude
            locationLongitude: root.locationLongitude
            viewportHeight: root.viewportHeight
            onViewRequested: (latitude, longitude, zoom) => root.radarViewRequested(latitude, longitude, zoom)
            onClearBasemap: root.clearTiles()
        }
    }
}
