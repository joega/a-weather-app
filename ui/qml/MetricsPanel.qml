pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import "Forecast.js" as Forecast
import "Dashboard.js" as Dashboard

GlassPanel {
    id: root
    signal metricSelected(string metric)
    property bool compact: false
    property bool presentationActive: true
    property var selection: Dashboard.defaults().metrics
    readonly property var selectedMetrics: selection.filter(row => row.enabled).map(row => ({
                kind: row.id
            }))
    readonly property int rowCount: Math.ceil(selectedMetrics.length / 2)
    readonly property var rowWeights: Array.from({
        length: rowCount
    }, (_, index) => selectedMetrics.slice(index * 2, index * 2 + 2).some(metric => metric.kind === "solar") ? 1.34 : 1)
    readonly property real totalWeight: rowWeights.reduce((a, b) => a + b, 0)
    function rowHeight(index) {
        return metricGrid.height * rowWeights[index] / totalWeight;
    }
    property var current: null
    property var day: null
    property string units: "F"
    property string windUnits: "auto"
    property string timezone: "America/New_York"
    property double currentTimeMs: Date.now()
    readonly property double sunriseMs: day && day.sunrise ? Date.parse(day.sunrise) : NaN
    readonly property double sunsetMs: day && day.sunset ? Date.parse(day.sunset) : NaN
    readonly property bool solarAvailable: Number.isFinite(sunriseMs) && Number.isFinite(sunsetMs) && sunsetMs > sunriseMs
    readonly property real solarProgress: solarAvailable ? (currentTimeMs - sunriseMs) / (sunsetMs - sunriseMs) : -1
    implicitHeight: 40 + (compact ? 102 : 115.6) * totalWeight
    Timer {
        interval: 60000
        repeat: true
        objectName: "metricsSolarTimer"
        running: root.visible && root.presentationActive && root.selection.some(row => row.id === "solar" && row.enabled)
        onRunningChanged: if (running)
            root.currentTimeMs = Date.now()
        onTriggered: root.currentTimeMs = Date.now()
    }
    readonly property var availableMetrics: [
        {
            kind: "wind",
            title: "Wind",
            value: root.current ? Forecast.direction(root.current.wind_direction_deg) + " " + Forecast.wind(root.current.wind_speed_m_s, root.units, root.windUnits) : "—",
            detail: root.current ? "Gusts " + Forecast.wind(root.current.wind_gust_m_s, root.units, root.windUnits) : "Unavailable"
        },
        {
            kind: "humidity",
            title: "Humidity",
            value: root.current ? Forecast.percent(root.current.humidity) : "—",
            detail: "Dew point " + Forecast.temp(root.current ? root.current.dew_point_c : null, root.units)
        },
        {
            kind: "visibility",
            title: "Visibility",
            value: Forecast.distance(root.current ? root.current.visibility_m : null, root.units),
            detail: ""
        },
        {
            kind: "solar",
            title: "Sunrise & sunset",
            value: root.solarAvailable ? (root.day.sunrise_label || "—") + " — " + (root.day.sunset_label || "—") : "Unavailable",
            detail: root.solarAvailable ? "" : "Solar times unavailable"
        },
        {
            kind: "uv",
            title: "UV index",
            value: Forecast.uv(root.current ? root.current.uv_index : null),
            detail: "Current model value"
        },
        {
            kind: "pressure",
            title: "Pressure",
            value: Forecast.pressure(root.current ? root.current.pressure_msl_hpa : null, root.units),
            detail: "Mean sea level"
        }
    ]
    Grid {
        id: metricGrid
        x: 22
        y: 20
        width: parent.width - 44
        height: parent.height - 40
        columns: 2
        Repeater {
            model: root.selectedMetrics
            delegate: AbstractButton {
                id: metric
                required property var modelData
                required property int index
                readonly property var metricData: root.availableMetrics.find(row => row.kind === modelData.kind)
                objectName: "currentMetric_" + modelData.kind
                Accessible.name: metricData.title + ": " + metricData.value + ". " + metricData.detail
                Accessible.description: "Open forecast details"
                enabled: modelData.kind !== "solar" || root.day !== null
                focusPolicy: Qt.StrongFocus
                onClicked: root.metricSelected(({
                        wind: "wind_speed_m_s",
                        humidity: "humidity",
                        visibility: "visibility_m",
                        solar: "daylight",
                        uv: "uv_index",
                        pressure: "pressure_msl_hpa"
                    })[modelData.kind])
                background: Rectangle {
                    color: metric.hovered ? "#203d5a70" : "transparent"
                    radius: 10
                    border.color: metric.activeFocus ? Tokens.accent : "transparent"
                    border.width: 2
                }
                width: metricGrid.width / metricGrid.columns
                height: root.rowHeight(Math.floor(metric.index / metricGrid.columns))
                Column {
                    id: metricContent
                    anchors.centerIn: parent
                    width: parent.width - 32
                    spacing: 6
                    PlainLabel {
                        width: parent.width
                        horizontalAlignment: Text.AlignHCenter
                        text: metric.metricData.title
                        color: Tokens.secondary
                        font.pixelSize: 15
                    }
                    Row {
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: 10
                        Canvas {
                            id: icon
                            anchors.verticalCenter: parent.verticalCenter
                            width: 36
                            height: 36
                            onWidthChanged: requestPaint()
                            onHeightChanged: requestPaint()
                            onPaint: {
                                const c = getContext("2d");
                                c.reset();
                                c.scale(width / 36, height / 36);
                                c.strokeStyle = "#e8f1fa";
                                c.fillStyle = "#e8f1fa";
                                c.lineWidth = 1.8;
                                c.lineCap = "round";
                                c.lineJoin = "round";
                                const kind = metric.metricData.kind;
                                if (kind === "wind") {
                                    c.beginPath();
                                    c.moveTo(2, 12);
                                    c.lineTo(21, 12);
                                    c.bezierCurveTo(30, 12, 29, 2, 23, 3);
                                    c.bezierCurveTo(20, 3, 19, 5, 20, 7);
                                    c.stroke();
                                    c.beginPath();
                                    c.moveTo(2, 18);
                                    c.lineTo(29, 18);
                                    c.bezierCurveTo(37, 18, 36, 8, 31, 9);
                                    c.stroke();
                                    c.beginPath();
                                    c.moveTo(2, 24);
                                    c.lineTo(19, 24);
                                    c.bezierCurveTo(29, 24, 28, 34, 22, 33);
                                    c.bezierCurveTo(19, 33, 18, 30, 20, 28);
                                    c.stroke();
                                } else if (kind === "humidity") {
                                    c.beginPath();
                                    c.moveTo(18, 3);
                                    c.bezierCurveTo(15, 9, 8, 17, 8, 23);
                                    c.bezierCurveTo(8, 36, 28, 36, 28, 23);
                                    c.bezierCurveTo(28, 17, 21, 9, 18, 3);
                                    c.closePath();
                                    c.stroke();
                                } else if (kind === "visibility") {
                                    c.beginPath();
                                    c.moveTo(2, 18);
                                    c.bezierCurveTo(11, 5, 25, 5, 34, 18);
                                    c.bezierCurveTo(25, 31, 11, 31, 2, 18);
                                    c.closePath();
                                    c.stroke();
                                    c.beginPath();
                                    c.arc(18, 18, 4.5, 0, Math.PI * 2);
                                    c.stroke();
                                } else if (kind === "uv") {
                                    c.beginPath();
                                    c.arc(18, 18, 7, 0, Math.PI * 2);
                                    c.stroke();
                                    for (let i = 0; i < 8; i++) {
                                        const angle = i * Math.PI / 4;
                                        c.beginPath();
                                        c.moveTo(18 + Math.cos(angle) * 11, 18 + Math.sin(angle) * 11);
                                        c.lineTo(18 + Math.cos(angle) * 15, 18 + Math.sin(angle) * 15);
                                        c.stroke();
                                    }
                                } else if (kind === "pressure") {
                                    c.beginPath();
                                    c.arc(18, 18, 13, Math.PI * 0.8, Math.PI * 2.2);
                                    c.stroke();
                                    c.beginPath();
                                    c.moveTo(18, 18);
                                    c.lineTo(26, 12);
                                    c.stroke();
                                    c.beginPath();
                                    c.arc(18, 18, 2, 0, Math.PI * 2);
                                    c.fill();
                                } else {
                                    c.beginPath();
                                    c.moveTo(2, 27);
                                    c.lineTo(34, 27);
                                    c.moveTo(6, 32);
                                    c.lineTo(30, 32);
                                    c.stroke();
                                    c.beginPath();
                                    c.arc(18, 25, 8, Math.PI, Math.PI * 2);
                                    c.stroke();
                                    for (let i = 0; i < 5; i++) {
                                        const angle = Math.PI + i * Math.PI / 4;
                                        c.beginPath();
                                        c.moveTo(18 + Math.cos(angle) * 12, 25 + Math.sin(angle) * 12);
                                        c.lineTo(18 + Math.cos(angle) * 15, 25 + Math.sin(angle) * 15);
                                        c.stroke();
                                    }
                                }
                            }
                        }
                        Column {
                            anchors.verticalCenter: parent.verticalCenter
                            width: Math.min(metricContent.width - icon.width - 10, Math.max(metricValue.implicitWidth, metricDetail.implicitWidth))
                            spacing: 5
                            PlainLabel {
                                id: metricValue
                                objectName: "currentMetricValue_" + metric.metricData.kind
                                width: parent.width
                                text: metric.metricData.value
                                font.pixelSize: metric.metricData.kind === "solar" ? 15 : 22
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                                maximumLineCount: 2
                            }
                            PlainLabel {
                                id: metricDetail
                                visible: text !== ""
                                objectName: "currentMetricDetail_" + metric.metricData.kind
                                width: parent.width
                                text: metric.metricData.detail
                                color: Tokens.secondary
                                font.pixelSize: 12
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                        }
                    }
                    Canvas {
                        id: arc
                        visible: metric.metricData.kind === "solar" && root.solarAvailable
                        width: parent.width
                        height: 26
                        onWidthChanged: requestPaint()
                        onHeightChanged: requestPaint()
                        Connections {
                            target: root
                            function onSolarProgressChanged() {
                                arc.requestPaint();
                            }
                            function onSolarAvailableChanged() {
                                arc.requestPaint();
                            }
                        }
                        onPaint: {
                            const c = getContext("2d");
                            c.reset();
                            if (!root.solarAvailable)
                                return;
                            const left = 8, right = width - 8, bottom = height - 3, rise = height - 8;
                            c.strokeStyle = "#afc6d8";
                            c.lineWidth = 1.2;
                            c.beginPath();
                            c.moveTo(left, bottom);
                            c.quadraticCurveTo(width / 2, bottom - rise * 2, right, bottom);
                            c.stroke();
                            c.strokeStyle = "rgba(174,197,212,0.35)";
                            c.beginPath();
                            c.moveTo(0, bottom);
                            c.lineTo(width, bottom);
                            c.stroke();
                            c.fillStyle = "#b7cbdc";
                            for (const x of [left, right]) {
                                c.beginPath();
                                c.arc(x, bottom, 2, 0, Math.PI * 2);
                                c.fill();
                            }
                            const p = root.solarProgress;
                            // No invented daytime sun when now lies outside actual sunrise/sunset.
                            if (p >= 0 && p <= 1) {
                                const x = left + (right - left) * p, y = bottom - 4 * p * (1 - p) * rise;
                                const glow = c.createRadialGradient(x, y, 0, x, y, 12);
                                glow.addColorStop(0, "rgba(245,214,120,0.32)");
                                glow.addColorStop(1, "rgba(245,214,120,0)");
                                c.fillStyle = glow;
                                c.fillRect(x - 12, y - 12, 24, 24);
                                c.fillStyle = "#f5d678";
                                c.beginPath();
                                c.arc(x, y, 4.5, 0, Math.PI * 2);
                                c.fill();
                            }
                        }
                    }
                    Row {
                        visible: metric.metricData.kind === "solar" && root.solarAvailable
                        width: parent.width
                        PlainLabel {
                            width: parent.width / 2
                            text: root.day ? (root.day.sunrise_label || "—") : "—"
                            color: Tokens.secondary
                            font.pixelSize: 10
                        }
                        PlainLabel {
                            width: parent.width / 2
                            horizontalAlignment: Text.AlignRight
                            text: root.day ? (root.day.sunset_label || "—") : "—"
                            color: Tokens.secondary
                            font.pixelSize: 10
                        }
                    }
                }
            }
        }
    }
    Repeater {
        model: Math.max(0, root.rowCount - 1)
        delegate: Rectangle {
            required property int index
            x: metricGrid.x
            y: metricGrid.y + root.rowWeights.slice(0, index + 1).reduce((a, b) => a + b, 0) / root.totalWeight * metricGrid.height
            width: metricGrid.width
            height: 1
            color: Tokens.border
            opacity: 0.55
        }
    }
    Rectangle {
        x: metricGrid.x + metricGrid.width / 2
        y: metricGrid.y
        width: 1
        height: metricGrid.height
        color: Tokens.border
        opacity: 0.55
    }
}
