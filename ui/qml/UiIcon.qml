import QtQuick

Canvas {
    id: root
    property string iconName: ""
    property color color: "#eaf5fb"
    property real strokeWidth: 1.8
    implicitWidth: 18
    implicitHeight: 18
    antialiasing: true
    Accessible.ignored: true

    onIconNameChanged: requestPaint()
    onColorChanged: requestPaint()
    onWidthChanged: requestPaint()
    onHeightChanged: requestPaint()

    onPaint: {
        const ctx = getContext("2d");
        ctx.reset();
        ctx.clearRect(0, 0, width, height);
        ctx.scale(width / 24, height / 24);
        ctx.strokeStyle = color;
        ctx.lineWidth = strokeWidth;
        ctx.lineCap = "round";
        ctx.lineJoin = "round";
        ctx.beginPath();

        switch (iconName) {
        case "share":
            ctx.moveTo(8, 9);
            ctx.lineTo(4, 9);
            ctx.lineTo(4, 21);
            ctx.lineTo(20, 21);
            ctx.lineTo(20, 9);
            ctx.lineTo(16, 9);
            ctx.moveTo(12, 15);
            ctx.lineTo(12, 2);
            ctx.moveTo(7, 7);
            ctx.lineTo(12, 2);
            ctx.lineTo(17, 7);
            ctx.stroke();
            break;
        case "map-pin":
            ctx.moveTo(12, 22);
            ctx.bezierCurveTo(8, 17, 4, 13, 4, 9);
            ctx.arc(12, 9, 8, Math.PI, 0);
            ctx.bezierCurveTo(20, 13, 16, 17, 12, 22);
            ctx.stroke();
            ctx.beginPath();
            ctx.arc(12, 9, 2.5, 0, Math.PI * 2);
            ctx.stroke();
            break;
        case "sliders":
            ctx.moveTo(4, 6);
            ctx.lineTo(8, 6);
            ctx.moveTo(12, 6);
            ctx.lineTo(20, 6);
            ctx.moveTo(4, 12);
            ctx.lineTo(13, 12);
            ctx.moveTo(17, 12);
            ctx.lineTo(20, 12);
            ctx.moveTo(4, 18);
            ctx.lineTo(5, 18);
            ctx.moveTo(9, 18);
            ctx.lineTo(20, 18);
            ctx.stroke();
            for (const knob of [[10, 6], [15, 12], [7, 18]]) {
                ctx.beginPath();
                ctx.arc(knob[0], knob[1], 2, 0, Math.PI * 2);
                ctx.stroke();
            }
            break;
        case "refresh":
            ctx.arc(12, 12, 8, Math.PI * 0.25, Math.PI * 1.75, false);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(12.7, 6.3);
            ctx.lineTo(17.7, 6.3);
            ctx.lineTo(17.7, 1.3);
            ctx.stroke();
            break;
        case "sidebar":
            ctx.rect(3, 4, 18, 16);
            ctx.moveTo(9, 4);
            ctx.lineTo(9, 20);
            ctx.stroke();
            break;
        case "desktop":
            ctx.rect(3, 4, 18, 13);
            ctx.moveTo(12, 17);
            ctx.lineTo(12, 21);
            ctx.moveTo(8, 21);
            ctx.lineTo(16, 21);
            ctx.stroke();
            break;
        case "info":
            ctx.arc(12, 12, 9, 0, Math.PI * 2);
            ctx.moveTo(12, 10.5);
            ctx.lineTo(12, 16.3);
            ctx.moveTo(12, 7.2);
            ctx.lineTo(12, 7.3);
            ctx.stroke();
            break;
        case "chevron-down":
            ctx.moveTo(6, 9);
            ctx.lineTo(12, 15);
            ctx.lineTo(18, 9);
            ctx.stroke();
            break;
        case "chevron-up":
            ctx.moveTo(6, 15);
            ctx.lineTo(12, 9);
            ctx.lineTo(18, 15);
            ctx.stroke();
            break;
        case "minus":
            ctx.moveTo(5, 12);
            ctx.lineTo(19, 12);
            ctx.stroke();
            break;
        case "home":
            ctx.moveTo(3, 11);
            ctx.lineTo(12, 3);
            ctx.lineTo(21, 11);
            ctx.moveTo(6, 9);
            ctx.lineTo(6, 21);
            ctx.lineTo(18, 21);
            ctx.lineTo(18, 9);
            ctx.moveTo(10, 21);
            ctx.lineTo(10, 14);
            ctx.lineTo(14, 14);
            ctx.lineTo(14, 21);
            ctx.stroke();
            break;
        case "close":
            ctx.moveTo(6, 6);
            ctx.lineTo(18, 18);
            ctx.moveTo(18, 6);
            ctx.lineTo(6, 18);
            ctx.stroke();
            break;
        case "play":
            ctx.moveTo(8, 5);
            ctx.lineTo(19, 12);
            ctx.lineTo(8, 19);
            ctx.closePath();
            ctx.stroke();
            break;
        case "stop":
            ctx.rect(6, 6, 12, 12);
            ctx.stroke();
            break;
        }
    }
}
