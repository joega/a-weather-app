import QtQuick

Item {
    id: root
    property string condition: "unknown"
    property bool isDay: true
    implicitWidth: 48
    implicitHeight: 42
    Canvas {
        id: canvas
        anchors.fill: parent
        onWidthChanged: requestPaint()
        onHeightChanged: requestPaint()
        Connections {
            target: root
            function onConditionChanged() {
                canvas.requestPaint();
            }
            function onIsDayChanged() {
                canvas.requestPaint();
            }
        }
        onPaint: {
            const c = getContext("2d");
            c.reset();
            c.scale(width / 48, height / 42);
            const sun = root.condition === "clear" || root.condition === "partly_cloudy";
            if (sun && root.isDay) {
                c.strokeStyle = "#f5d678";
                c.lineWidth = 1.6;
                for (let i = 0; i < 8; i++) {
                    let a = i * Math.PI / 4;
                    c.beginPath();
                    c.moveTo(17 + Math.cos(a) * 10, 16 + Math.sin(a) * 10);
                    c.lineTo(17 + Math.cos(a) * 14, 16 + Math.sin(a) * 14);
                    c.stroke();
                }
                c.fillStyle = "#f5d678";
                c.beginPath();
                c.arc(17, 16, 7, 0, Math.PI * 2);
                c.fill();
            }
            if (sun && !root.isDay) {
                c.fillStyle = "#e7eff9";
                c.beginPath();
                c.arc(17, 16, 10, Math.PI * 0.35, Math.PI * 1.65);
                c.bezierCurveTo(12, 12, 12, 20, 21.5, 25);
                c.closePath();
                c.fill();
            }
            if (root.condition !== "clear" && root.condition !== "unknown") {
                c.fillStyle = "#e7eff9";
                // Separate filled circles avoid implicit connecting chords and
                // winding holes between the cloud's overlapping lobes.
                for (let lobe of [[14, 24, 8], [24, 18, 10], [35, 25, 8]]) {
                    c.beginPath();
                    c.arc(lobe[0], lobe[1], lobe[2], 0, Math.PI * 2);
                    c.fill();
                }
                c.fillRect(14, 23, 22, 10);
            }
            if (root.condition === "thunderstorm") {
                c.fillStyle = "#f5d678";
                c.beginPath();
                c.moveTo(28, 25);
                c.lineTo(22, 34);
                c.lineTo(27, 34);
                c.lineTo(23, 41);
                c.lineTo(34, 30);
                c.lineTo(28, 30);
                c.closePath();
                c.fill();
            }
            if (["rain", "drizzle", "sleet", "thunderstorm"].indexOf(root.condition) >= 0) {
                c.strokeStyle = "#8de4f6";
                c.lineWidth = 3;
                c.lineCap = "round";
                for (let x = 13; x < 40; x += 10) {
                    c.beginPath();
                    c.moveTo(x, 36);
                    c.lineTo(x - 2, 40);
                    c.stroke();
                }
            }
            if (root.condition === "snow") {
                c.fillStyle = "#e7eff9";
                for (let x = 13; x < 40; x += 10) {
                    c.beginPath();
                    c.arc(x, 38, 2, 0, Math.PI * 2);
                    c.fill();
                }
            }
            if (root.condition === "fog") {
                c.strokeStyle = "#b7cbdc";
                c.lineWidth = 2;
                for (let y = 35; y < 42; y += 5) {
                    c.beginPath();
                    c.moveTo(7, y);
                    c.lineTo(41, y);
                    c.stroke();
                }
            }
            if (root.condition === "unknown") {
                c.fillStyle = "#b7cbdc";
                c.font = "24px sans-serif";
                c.fillText("—", 12, 29);
            }
        }
    }
}
