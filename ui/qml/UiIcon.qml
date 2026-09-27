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
        case "sliders":
            ctx.moveTo(4, 6); ctx.lineTo(8, 6); ctx.moveTo(12, 6); ctx.lineTo(20, 6);
            ctx.moveTo(4, 12); ctx.lineTo(13, 12); ctx.moveTo(17, 12); ctx.lineTo(20, 12);
            ctx.moveTo(4, 18); ctx.lineTo(5, 18); ctx.moveTo(9, 18); ctx.lineTo(20, 18);
            ctx.arc(10, 6, 2, 0, Math.PI * 2);
            ctx.arc(15, 12, 2, 0, Math.PI * 2);
            ctx.arc(7, 18, 2, 0, Math.PI * 2);
            ctx.stroke();
            break;
        case "refresh":
            ctx.arc(12, 12, 8, 0.6, 5.3, false);
            ctx.stroke();
            ctx.beginPath();
            ctx.moveTo(16.4, 5.3); ctx.lineTo(19.1, 5.8); ctx.lineTo(18.5, 2.9);
            ctx.stroke();
            break;
        case "info":
            ctx.arc(12, 12, 9, 0, Math.PI * 2);
            ctx.moveTo(12, 10.5); ctx.lineTo(12, 16.3);
            ctx.moveTo(12, 7.2); ctx.lineTo(12, 7.3);
            ctx.stroke();
            break;
        case "chevron-down":
            ctx.moveTo(6, 9); ctx.lineTo(12, 15); ctx.lineTo(18, 9);
            ctx.stroke();
            break;
        case "chevron-up":
            ctx.moveTo(6, 15); ctx.lineTo(12, 9); ctx.lineTo(18, 15);
            ctx.stroke();
            break;
        case "close":
            ctx.moveTo(6, 6); ctx.lineTo(18, 18);
            ctx.moveTo(18, 6); ctx.lineTo(6, 18);
            ctx.stroke();
            break;
        case "play":
            ctx.moveTo(8, 5); ctx.lineTo(19, 12); ctx.lineTo(8, 19); ctx.closePath();
            ctx.stroke();
            break;
        case "stop":
            ctx.rect(6, 6, 12, 12);
            ctx.stroke();
            break;
        }
    }
}
