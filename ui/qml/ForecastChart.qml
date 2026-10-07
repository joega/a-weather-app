import QtQuick
import "Forecast.js" as Forecast

Item {
    id: root
    objectName: "forecastTrend"
    property var hours: []
    property string units: "F"
    property string windUnits: "auto"
    property string metric: "temperature_c"
    property int selectedIndex: 0
    signal selected(int index)
    implicitHeight: 180
    function valueText(value) {
        return metric === "temperature_c" ? Forecast.temp(value, units) : metric === "precipitation_rate_mm_hr" ? Forecast.amount(value, units) : Forecast.wind(value, units, windUnits);
    }
    onHoursChanged: plot.requestPaint()
    onMetricChanged: plot.requestPaint()
    onSelectedIndexChanged: plot.requestPaint()
    onUnitsChanged: plot.requestPaint()
    onWindUnitsChanged: plot.requestPaint()
    Canvas {
        id: plot
        anchors.fill: parent
        anchors.bottomMargin: 26
        onWidthChanged: requestPaint()
        onHeightChanged: requestPaint()
        onPaint: {
            const c = getContext("2d");
            c.reset();
            c.clearRect(0, 0, width, height);
            const values = root.hours.map(h => h[root.metric]);
            const known = values.filter(v => v !== null && v !== undefined);
            if (!known.length)
                return;
            const low = root.metric === "temperature_c" ? Math.min.apply(null, known) - 1 : 0;
            const high = Math.max(low + 1, Math.max.apply(null, known));
            const left = 64, right = width - 18, top = 22, bottom = height - 18;
            const x = i => left + (right - left) * (i + 0.5) / values.length;
            const y = v => bottom - (v - low) / (high - low) * (bottom - top);
            c.font = "12px sans-serif";
            c.fillStyle = "#b7cbdc";
            c.fillText(root.valueText(high), 0, top + 4);
            c.fillText(root.valueText(low), 0, bottom + 4);
            c.strokeStyle = "#426078";
            c.lineWidth = 1;
            for (let i = 0; i < 3; i++) {
                const line = top + (bottom - top) * i / 2;
                c.beginPath();
                c.moveTo(left, line);
                c.lineTo(right, line);
                c.stroke();
            }
            if (root.metric === "precipitation_rate_mm_hr") {
                for (let i = 0; i < values.length; i++)
                    if (values[i] !== null && values[i] !== undefined) {
                        c.fillStyle = i === root.selectedIndex ? "#f0f5fc" : "#8de4f6";
                        const w = Math.max(2, (right - left) / values.length * 0.55);
                        c.fillRect(x(i) - w / 2, y(values[i]), w, Math.max(1, bottom - y(values[i])));
                    }
            } else {
                c.strokeStyle = "#8de4f6";
                c.lineWidth = 2.5;
                c.beginPath();
                let drawing = false;
                for (let i = 0; i < values.length; i++) {
                    if (values[i] === null || values[i] === undefined) {
                        drawing = false;
                        continue;
                    }
                    if (drawing)
                        c.lineTo(x(i), y(values[i]));
                    else
                        c.moveTo(x(i), y(values[i]));
                    drawing = true;
                }
                c.stroke();
                // A lone known value has no line segment. Keep it visible when
                // surrounding forecast hours are missing, without bridging gaps.
                c.fillStyle = "#8de4f6";
                for (let i = 0; i < values.length; i++) {
                    const valid = values[i] !== null && values[i] !== undefined;
                    const before = i > 0 && values[i - 1] !== null && values[i - 1] !== undefined;
                    const after = i + 1 < values.length && values[i + 1] !== null && values[i + 1] !== undefined;
                    if (valid && !before && !after) {
                        c.beginPath();
                        c.arc(x(i), y(values[i]), 3, 0, Math.PI * 2);
                        c.fill();
                    }
                }
            }
            if (root.selectedIndex >= 0 && root.selectedIndex < values.length) {
                c.strokeStyle = "#b7cbdc";
                c.lineWidth = 1;
                c.beginPath();
                c.moveTo(x(root.selectedIndex), top);
                c.lineTo(x(root.selectedIndex), bottom);
                c.stroke();
                const v = values[root.selectedIndex];
                if (v !== null && v !== undefined) {
                    c.fillStyle = "#f0f5fc";
                    c.beginPath();
                    c.arc(x(root.selectedIndex), y(v), 4, 0, Math.PI * 2);
                    c.fill();
                }
            }
        }
        MouseArea {
            anchors.fill: parent
            enabled: root.hours.length > 0
            cursorShape: Qt.PointingHandCursor
            onClicked: mouse => root.selected(Math.max(0, Math.min(root.hours.length - 1, Math.floor((mouse.x - 64) / (width - 82) * root.hours.length))))
        }
    }
    PlainLabel {
        anchors.left: parent.left
        anchors.leftMargin: 64
        anchors.bottom: parent.bottom
        text: root.hours.length ? root.hours[0].local_hour : ""
        font.pixelSize: 12
        color: Tokens.secondary
    }
    PlainLabel {
        anchors.right: parent.right
        anchors.rightMargin: 18
        anchors.bottom: parent.bottom
        text: root.hours.length ? root.hours[root.hours.length - 1].local_hour : ""
        font.pixelSize: 12
        color: Tokens.secondary
    }
    PlainLabel {
        objectName: "forecastTrendUnavailable"
        anchors.centerIn: parent
        visible: !root.hours.some(h => h[root.metric] !== null && h[root.metric] !== undefined)
        text: "Trend unavailable"
        color: Tokens.secondary
    }
}
