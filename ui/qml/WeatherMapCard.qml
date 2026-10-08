pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import QtQuick.Window
import "Forecast.js" as Forecast
import "WindField.js" as WindField

GlassPanel {
    id: root
    implicitHeight: 396
    property var mapData: null
    property string mapStatus: "closed"
    property bool hasLocation: false
    property bool offline: false
    property bool tileActive: false
    property bool reducedMotion: false
    property bool presentationActive: true
    readonly property real mapTop: mapArea.y
    readonly property real mapHeight: mapArea.height
    readonly property bool animationActive: mapLayer === "wind" && mapData !== null && tileActive && presentationActive && visible && !reducedMotion && mapArea.width > 0 && mapArea.height > 0 && (!Window.window || (Window.window.visibility !== Window.Minimized && Window.window.visibility !== Window.Hidden))
    property var windField: null
    property var particles: []
    property double lastTick: 0
    property real inspectionX: 0.5
    property real inspectionY: 0.5
    property bool pointSelected: false
    readonly property real flowRadius: desiredScale * 16093.44
    readonly property int trailCount: Math.max(16, Math.min(64, Math.round(mapArea.width * mapArea.height / 3200)))
    readonly property var inspectedVector: windField ? WindField.interpolate(windField.samples, inspectionX * mapArea.width, inspectionY * mapArea.height) : ({
            x: 0,
            y: 0
        })
    readonly property string pointReadout: windField ? windSpeed(WindField.speed(inspectedVector)) + " · " + WindField.direction(inspectedVector) : "—"
    function invalidateWind() {
        // Stop the old field immediately, before the coalesced rebuild runs.
        windField = null;
        particles = [];
        Qt.callLater(root.rebuildWind);
        overlay.requestPaint();
    }
    function rebuildWind() {
        windField = null;
        particles = [];
        if (mapLayer !== "wind" || !mapData || hourIndex >= mapData.hours.length || mapArea.width <= 0 || mapArea.height <= 0) {
            overlay.requestPaint();
            return;
        }
        const samples = mapData.cells.map(cell => {
            const v = WindField.vector(cell.wind_speed_m_s[hourIndex], cell.wind_from_deg[hourIndex]);
            return {
                x: mapX(cell.longitude),
                y: mapY(cell.latitude),
                vx: v.x,
                vy: v.y
            };
        });
        windField = WindField.build(samples, mapArea.width, mapArea.height);
        resetTrails();
    }
    function resetTrails() {
        if (windField) {
            particles = animationActive ? Array.from({
                length: trailCount
            }, (_, i) => WindField.seed(i, mapArea.width, mapArea.height, flowRadius, 0)) : WindField.staticTrails(trailCount, windField, flowRadius);
        }
        lastTick = Date.now();
        overlay.requestPaint();
    }
    function inspectPoint(x, y) {
        if (!WindField.inside(x, y, mapArea.width, mapArea.height, flowRadius))
            return;
        inspectionX = x / mapArea.width;
        inspectionY = y / mapArea.height;
        pointSelected = true;
    }
    onAnimationActiveChanged: resetTrails()
    Timer {
        objectName: "windAnimationTimer"
        interval: 67
        repeat: true
        running: root.animationActive && root.windField !== null
        onRunningChanged: root.lastTick = Date.now()
        onTriggered: {
            const now = Date.now();
            const dt = Math.max(0, Math.min(0.12, (now - root.lastTick) / 1000));
            root.lastTick = now;
            WindField.advance(root.particles, root.windField, dt, root.flowRadius);
            overlay.requestPaint();
        }
    }
    property string units: "F"
    property string windUnits: "auto"
    property int hourIndex: 0
    property string mapLayer: "temperature"
    property var tileImages: ({})
    property var failedTiles: ({})
    property var tileClient: null
    property int tileGeneration: 0
    readonly property var fieldValues: mapData && hourIndex < mapData.hours.length ? mapData.cells.map(c => mapLayer === "temperature" ? c.temperature_c[hourIndex] : mapLayer === "precipitation" ? c.precipitation_mm[hourIndex] : c.wind_speed_m_s[hourIndex]) : []
    readonly property real fieldMin: fieldValues.length ? (mapLayer === "precipitation" ? 0 : Math.min.apply(null, fieldValues)) : 0
    readonly property real fieldMax: fieldValues.length ? (mapLayer === "precipitation" ? Math.max(0.1, Math.max.apply(null, fieldValues)) : Math.max.apply(null, fieldValues)) : 1
    readonly property bool dryPrecipitation: mapLayer === "precipitation" && fieldValues.length > 0 && fieldValues.every(value => value === 0)
    readonly property real centerLat: mapData ? mapData.latitude : 0
    readonly property real centerLon: mapData ? mapData.longitude : 0
    readonly property real desiredScale: Math.min(mapArea.width, mapArea.height) * 0.43 / 16093.44
    readonly property int zoom: Math.max(1, Math.min(15, Math.ceil(Math.log(40075016.686 * Math.cos(centerLat * Math.PI / 180) * desiredScale / 256) / Math.log(2))))
    readonly property real tileScale: desiredScale * 40075016.686 * Math.cos(centerLat * Math.PI / 180) / (256 * Math.pow(2, zoom))
    readonly property real centerX: worldX(centerLon)
    readonly property real centerY: worldY(centerLat)
    readonly property var visibleTiles: tileActive ? tiles() : []
    function worldX(lon) {
        return (lon + 180) / 360 * 256 * Math.pow(2, zoom);
    }
    function worldY(lat) {
        let r = Math.max(-85, Math.min(85, lat)) * Math.PI / 180;
        return (1 - Math.log(Math.tan(r) + 1 / Math.cos(r)) / Math.PI) / 2 * 256 * Math.pow(2, zoom);
    }
    function mapX(lon) {
        let dx = worldX(lon) - centerX, n = 256 * Math.pow(2, zoom);
        if (dx > n / 2)
            dx -= n;
        if (dx < -n / 2)
            dx += n;
        return mapArea.width / 2 + dx * tileScale;
    }
    function mapY(lat) {
        return mapArea.height / 2 + (worldY(lat) - centerY) * tileScale;
    }
    function tiles() {
        if (!mapData || mapArea.width < 1 || mapArea.height < 1)
            return [];
        let tileSize = 256 * tileScale, x0 = Math.floor((centerX - mapArea.width / 2 / tileScale) / 256), x1 = Math.floor((centerX + mapArea.width / 2 / tileScale) / 256);
        let y0 = Math.floor((centerY - mapArea.height / 2 / tileScale) / 256), y1 = Math.floor((centerY + mapArea.height / 2 / tileScale) / 256), n = Math.pow(2, zoom), result = [];
        for (let y = y0; y <= y1; y++)
            for (let x = x0; x <= x1; x++)
                if (y >= 0 && y < n && result.length < 16) {
                    let wrapped = (x % n + n) % n;
                    result.push({
                        zoom: zoom,
                        x: x,
                        y: y,
                        key: zoom + "/" + wrapped + "/" + y,
                        requestX: wrapped,
                        left: mapArea.width / 2 + (x * 256 - centerX) * tileScale,
                        top: mapArea.height / 2 + (y * 256 - centerY) * tileScale,
                        size: tileSize
                    });
                }
        return result;
    }
    function requestTiles() {
        if (!tileActive || !tileClient)
            return;
        for (const tile of visibleTiles)
            tileClient.request(tile.zoom, tile.requestX, tile.y, offline);
    }
    // Map state propagation can reset the shared client after delegates update.
    // Coalesce requests after that reset, using the final geometry/offline state.
    onTileGenerationChanged: Qt.callLater(root.requestTiles)
    onVisibleTilesChanged: Qt.callLater(root.requestTiles)
    onTileClientChanged: Qt.callLater(root.requestTiles)
    onOfflineChanged: Qt.callLater(root.requestTiles)
    // A completion frees a bounded download slot for any remaining visible tiles.
    onTileImagesChanged: Qt.callLater(root.requestTiles)
    onFailedTilesChanged: Qt.callLater(root.requestTiles)
    function windSpeed(value) {
        return Forecast.wind(value, units, windUnits);
    }
    function temperature(value) {
        return units === "F" ? (value * 1.8 + 32).toFixed(0) + "°F" : value.toFixed(0) + "°C";
    }
    function rain(value) {
        return Forecast.amount(value, units);
    }
    function legendRain(value) {
        return units === "F" && value > 0 && value / 25.4 < 0.005 ? "<0.01 in" : rain(value);
    }
    function ramp(value, min, max) {
        let p = Math.max(0, Math.min(1, (value - min) / Math.max(0.01, max - min)));
        return Qt.rgba(0.13 + 0.8 * p, 0.49 - 0.18 * p, 0.88 - 0.68 * p, 0.47);
    }
    function shade(value, min, max) {
        return mapLayer === "precipitation" ? (value <= 0 ? Qt.rgba(0, 0, 0, 0) : Qt.rgba(0.05, 0.36, 0.92, 0.20 + 0.55 * Math.sqrt(value / Math.max(0.1, max)))) : ramp(value, min, max);
    }
    function paint() {
        let ctx = overlay.getContext("2d");
        ctx.clearRect(0, 0, overlay.width, overlay.height);
        if (!mapData || hourIndex >= mapData.hours.length)
            return;
        if (mapLayer !== "wind") {
            const cells = root.mapData.cells, vals = root.fieldValues;
            if (mapData.resolution_km >= 10) {
                ctx.fillStyle = shade(vals[12], root.fieldMin, root.fieldMax);
                ctx.fillRect(0, 0, overlay.width, overlay.height);
            } else
                for (let r = 0; r < 5; r++)
                    for (let c = 0; c < 5; c++) {
                        let i = r * 5 + c, cell = cells[i], prev = c > 0 ? cells[i - 1] : null, next = c < 4 ? cells[i + 1] : null, above = r > 0 ? cells[i - 5] : null, below = r < 4 ? cells[i + 5] : null;
                        let x = mapX(cell.longitude), y = mapY(cell.latitude), xl = prev ? (x + mapX(prev.longitude)) / 2 : x - 25, xr = next ? (x + mapX(next.longitude)) / 2 : x + 25, yt = above ? (y + mapY(above.latitude)) / 2 : y - 25, yb = below ? (y + mapY(below.latitude)) / 2 : y + 25;
                        ctx.fillStyle = shade(vals[i], root.fieldMin, root.fieldMax);
                        ctx.fillRect(Math.min(xl, xr), Math.min(yt, yb), Math.abs(xr - xl) + 1, Math.abs(yb - yt) + 1);
                    }
        } else {
            ctx.save();
            ctx.beginPath();
            ctx.arc(overlay.width / 2, overlay.height / 2, root.flowRadius, 0, Math.PI * 2);
            ctx.clip();
            ctx.lineCap = "round";
            ctx.lineWidth = 1.3;
            for (const particle of root.particles) {
                const points = particle.points;
                if (points.length < 2)
                    continue;
                const head = points[points.length - 1];
                if (WindField.speed(WindField.sample(root.windField, head.x, head.y)) < 0.2)
                    continue;
                const fade = root.animationActive ? Math.max(0, Math.min(1, particle.age / 0.7, (particle.life - particle.age) / 1.2)) : 1;
                for (let i = 1; i < points.length; ++i) {
                    ctx.strokeStyle = "rgba(80,136,156," + (fade * (0.10 + 0.45 * i / (points.length - 1))) + ")";
                    ctx.beginPath();
                    ctx.moveTo(points[i - 1].x, points[i - 1].y);
                    ctx.lineTo(points[i].x, points[i].y);
                    ctx.stroke();
                }
            }
            ctx.restore();
        }
    }
    onUnitsChanged: overlay.requestPaint()
    onWindUnitsChanged: overlay.requestPaint()
    onMapDataChanged: {
        inspectionX = 0.5;
        inspectionY = 0.5;
        pointSelected = false;
        invalidateWind();
    }
    onHourIndexChanged: invalidateWind()
    onMapLayerChanged: invalidateWind()
    onWidthChanged: overlay.requestPaint()
    onHeightChanged: overlay.requestPaint()
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 18
        spacing: 9
        PlainLabel {
            objectName: "mapModuleTitle"
            Layout.fillWidth: true
            text: root.mapLayer === "temperature" ? "Temperature" : root.mapLayer === "wind" ? "Wind" : "Precipitation"
            font.pixelSize: 20
            font.weight: Font.DemiBold
        }
        PlainLabel {
            Layout.fillWidth: true
            text: !root.mapData ? "" : root.mapLayer === "temperature" ? "At center · " + root.temperature(root.mapData.cells[12].temperature_c[root.hourIndex]) : root.mapLayer === "wind" ? (root.pointSelected ? "Selected point · " : "At center · ") + root.pointReadout : "Modeled total in preceding hour · " + root.rain(root.mapData.cells[12].precipitation_mm[root.hourIndex])
            objectName: "mapPointReadout"
            font.pixelSize: 13
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        Item {
            id: mapArea
            objectName: "mapArea"
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            activeFocusOnTab: root.mapLayer === "wind"
            enabled: root.mapLayer !== "wind" || root.mapData !== null
            Accessible.role: Accessible.Canvas
            Accessible.name: "Wind map. " + root.pointReadout
            Accessible.description: "Interpolated forecast. Click a point or use arrow keys to inspect wind; Home returns to center."
            onWidthChanged: root.invalidateWind()
            onHeightChanged: root.invalidateWind()
            Keys.onPressed: event => {
                let dx = 0, dy = 0;
                if (event.key === Qt.Key_Left)
                    dx = -0.04;
                else if (event.key === Qt.Key_Right)
                    dx = 0.04;
                else if (event.key === Qt.Key_Up)
                    dy = -0.04;
                else if (event.key === Qt.Key_Down)
                    dy = 0.04;
                else if (event.key === Qt.Key_Home) {
                    root.inspectionX = 0.5;
                    root.inspectionY = 0.5;
                    root.pointSelected = false;
                } else
                    return;
                if (dx || dy)
                    root.inspectPoint((root.inspectionX + dx) * width, (root.inspectionY + dy) * height);
                event.accepted = true;
            }
            Rectangle {
                anchors.fill: parent
                color: "#dce8e7"
            }
            Repeater {
                model: root.visibleTiles
                delegate: Image {
                    required property var modelData
                    x: modelData.left
                    y: modelData.top
                    width: modelData.size
                    height: modelData.size
                    source: root.tileImages[modelData.key] || ""
                    fillMode: Image.Stretch
                    asynchronous: true
                }
            }
            Canvas {
                id: overlay
                objectName: "mapOverlay"
                anchors.fill: parent
                onPaint: root.paint()
            }
            MouseArea {
                anchors.fill: parent
                enabled: root.mapLayer === "wind" && root.mapData !== null
                cursorShape: Qt.CrossCursor
                onClicked: mouse => {
                    mapArea.forceActiveFocus();
                    root.inspectPoint(mouse.x, mouse.y);
                }
            }
            Rectangle {
                width: 12
                height: 12
                radius: 6
                color: "#e6f4f7"
                border.color: "#17465c"
                border.width: 2
                x: root.inspectionX * mapArea.width - width / 2
                y: root.inspectionY * mapArea.height - height / 2
                visible: root.mapLayer === "wind" && root.mapData !== null && (root.pointSelected || mapArea.activeFocus)
            }
            Rectangle {
                anchors.fill: parent
                color: "transparent"
                border.width: 2
                border.color: Tokens.accent
                visible: mapArea.activeFocus
            }
            Rectangle {
                x: parent.width / 2 - root.desiredScale * 16093.44
                y: parent.height / 2 - root.desiredScale * 16093.44
                width: 2 * root.desiredScale * 16093.44
                height: width
                radius: width / 2
                color: "transparent"
                border.color: "#efffffff"
                border.width: 2
                visible: root.mapData !== null
            }
            Rectangle {
                width: 8
                height: 8
                radius: 4
                color: "#ffffff"
                border.color: "#23435c"
                x: parent.width / 2 - 4
                y: parent.height / 2 - 4
                visible: root.mapData !== null
            }
            Rectangle {
                anchors.top: parent.top
                anchors.right: parent.right
                anchors.margins: 7
                width: missingTiles.implicitWidth + 14
                height: missingTiles.implicitHeight + 8
                radius: 5
                color: "#eaf5fa"
                visible: root.tileActive && root.tiles().length > 0 && root.tiles().every(tile => root.failedTiles[tile.key]) && Object.keys(root.tileImages).length === 0
                PlainLabel {
                    id: missingTiles
                    anchors.centerIn: parent
                    text: "Geographic background unavailable"
                    font.pixelSize: 12
                    color: "#163448"
                }
            }
            Rectangle {
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.margins: 6
                width: mapCredit.width + 12
                height: mapCredit.height + 6
                radius: 4
                color: "#edf7fb"
                visible: root.mapData !== null
                PlainLabel {
                    id: mapCredit
                    objectName: "mapCredit"
                    anchors.centerIn: parent
                    text: Forecast.distance(root.mapData ? root.mapData.radius_miles * 1609.344 : 16093.44, root.units) + " radius · © OpenStreetMap contributors (ODbL)"
                    font.pixelSize: 11
                    color: "#132f43"
                }
                MouseArea {
                    anchors.fill: parent
                    cursorShape: Qt.PointingHandCursor
                    onClicked: Qt.openUrlExternally("https://www.openstreetmap.org/copyright")
                }
            }
            Rectangle {
                anchors.fill: parent
                visible: root.mapData === null
                color: "#dce8e7"
                opacity: 0.9
            }
            PlainLabel {
                anchors.centerIn: parent
                visible: root.mapData === null
                text: !root.hasLocation ? "Choose a location to see local maps" : root.mapStatus === "loading" ? "Loading local model forecast…" : root.offline ? "Map unavailable offline for this location" : "Map forecast unavailable"
                color: "#233e54"
            }
        }
        RowLayout {
            Layout.fillWidth: true
            visible: root.mapData !== null
            PlainLabel {
                objectName: "mapLegend"
                text: root.dryPrecipitation ? "No precipitation modeled for this hour" : root.mapLayer === "wind" ? (root.reducedMotion ? "Static trails" : "Trails flow downwind") + " · tap to inspect · " + Forecast.windUnit(root.units, root.windUnits) : "Legend"
                font.pixelSize: 12
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                Layout.fillWidth: root.mapLayer === "wind" || root.dryPrecipitation
            }
            PlainLabel {
                visible: root.mapLayer !== "wind" && !root.dryPrecipitation
                text: root.mapLayer === "temperature" ? root.temperature(root.fieldMin) : root.legendRain(root.fieldMin)
                font.pixelSize: 12
            }
            Rectangle {
                visible: root.mapLayer !== "wind" && !root.dryPrecipitation
                Layout.fillWidth: true
                Layout.preferredHeight: 12
                radius: 3
                gradient: Gradient {
                    orientation: Gradient.Horizontal
                    GradientStop {
                        position: 0
                        color: root.shade(0, 0, 1)
                    }
                    GradientStop {
                        position: 1
                        color: root.shade(1, 0, 1)
                    }
                }
            }
            PlainLabel {
                visible: root.mapLayer !== "wind" && !root.dryPrecipitation
                text: root.mapLayer === "temperature" ? root.temperature(root.fieldMax) : root.legendRain(root.fieldMax)
                font.pixelSize: 12
            }
        }
    }
}
