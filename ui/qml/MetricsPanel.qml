pragma ComponentBehavior: Bound
import QtQuick
import "Forecast.js" as Forecast

GlassPanel {
    id: root
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
    implicitHeight: 426
    Timer {
        interval: 60000
        repeat: true
        running: root.visible
        onTriggered: root.currentTimeMs = Date.now()
    }
    Grid {
        x: 22; y: 20
        width: parent.width - 44
        columns: 2
        rowSpacing: 13
        columnSpacing: 22
        Repeater {
            model: [
                {kind:"wind", title:"Wind", value:root.current ? Forecast.direction(root.current.wind_direction_deg)+" "+Forecast.wind(root.current.wind_speed_m_s,root.units,root.windUnits) : "—", detail:root.current ? "Gusts "+Forecast.wind(root.current.wind_gust_m_s,root.units,root.windUnits) : "Unavailable"},
                {kind:"humidity", title:"Humidity", value:root.current ? Forecast.percent(root.current.humidity) : "—", detail:"Dew point "+Forecast.temp(root.current ? root.current.dew_point_c : null,root.units)},
                {kind:"visibility", title:"Visibility", value:Forecast.distance(root.current ? root.current.visibility_m : null,root.units), detail:""},
                {kind:"solar", title:"Sunrise & sunset", value:root.solarAvailable ? (root.day.sunrise_label || "—")+" — "+(root.day.sunset_label || "—") : "Unavailable", detail:root.solarAvailable ? "" : "Solar times unavailable"},
                {kind:"uv", title:"UV index", value:Forecast.uv(root.current ? root.current.uv_index : null), detail:"Current model value"},
                {kind:"pressure", title:"Pressure", value:Forecast.pressure(root.current ? root.current.pressure_msl_hpa : null,root.units), detail:"Mean sea level"}
            ]
            delegate: Item {
                id: metric
                required property var modelData
                width: (root.width - 66) / 2
                height: 120
                PlainLabel { text: metric.modelData.title; color: Tokens.secondary; font.pixelSize: 15 }
                Canvas {
                    id: icon
                    x: 0; y: 34; width: 36; height: 36
                    onWidthChanged: requestPaint()
                    onHeightChanged: requestPaint()
                    onPaint: {
                        const c = getContext("2d"); c.reset(); c.scale(width/36,height/36)
                        c.strokeStyle = "#e8f1fa"; c.fillStyle = "#e8f1fa"
                        c.lineWidth = 1.8; c.lineCap = "round"; c.lineJoin = "round"
                        const kind = metric.modelData.kind
                        if (kind === "wind") {
                            c.beginPath(); c.moveTo(2,12); c.lineTo(21,12); c.bezierCurveTo(30,12,29,2,23,3); c.bezierCurveTo(20,3,19,5,20,7); c.stroke()
                            c.beginPath(); c.moveTo(2,18); c.lineTo(29,18); c.bezierCurveTo(37,18,36,8,31,9); c.stroke()
                            c.beginPath(); c.moveTo(2,24); c.lineTo(19,24); c.bezierCurveTo(29,24,28,34,22,33); c.bezierCurveTo(19,33,18,30,20,28); c.stroke()
                        } else if (kind === "humidity") {
                            c.beginPath(); c.moveTo(18,3); c.bezierCurveTo(15,9,8,17,8,23); c.bezierCurveTo(8,36,28,36,28,23); c.bezierCurveTo(28,17,21,9,18,3); c.closePath(); c.stroke()
                        } else if (kind === "visibility") {
                            c.beginPath(); c.moveTo(2,18); c.bezierCurveTo(11,5,25,5,34,18); c.bezierCurveTo(25,31,11,31,2,18); c.closePath(); c.stroke()
                            c.beginPath(); c.arc(18,18,4.5,0,Math.PI*2); c.stroke()
                        } else if (kind === "uv") {
                            c.beginPath(); c.arc(18,18,7,0,Math.PI*2); c.stroke()
                            for (let i=0;i<8;i++) { const angle=i*Math.PI/4; c.beginPath(); c.moveTo(18+Math.cos(angle)*11,18+Math.sin(angle)*11); c.lineTo(18+Math.cos(angle)*15,18+Math.sin(angle)*15); c.stroke() }
                        } else if (kind === "pressure") {
                            c.beginPath(); c.arc(18,18,13,Math.PI*0.8,Math.PI*2.2); c.stroke()
                            c.beginPath(); c.moveTo(18,18); c.lineTo(26,12); c.stroke()
                            c.beginPath(); c.arc(18,18,2,0,Math.PI*2); c.fill()
                        } else {
                            c.beginPath(); c.moveTo(2,27); c.lineTo(34,27); c.moveTo(6,32); c.lineTo(30,32); c.stroke()
                            c.beginPath(); c.arc(18,25,8,Math.PI,Math.PI*2); c.stroke()
                            for (let i=0;i<5;i++) { const angle=Math.PI+i*Math.PI/4; c.beginPath(); c.moveTo(18+Math.cos(angle)*12,25+Math.sin(angle)*12); c.lineTo(18+Math.cos(angle)*15,25+Math.sin(angle)*15); c.stroke() }
                        }
                    }
                }
                PlainLabel {
                    id: metricValue
                    objectName: "currentMetricValue_"+metric.modelData.kind
                    x: 46; y: 32
                    width: parent.width - 46
                    text: metric.modelData.value
                    font.pixelSize: metric.modelData.kind === "solar" ? 15 : 22
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                    maximumLineCount: 2
                }
                PlainLabel {
                    objectName: "currentMetricDetail_"+metric.modelData.kind
                    x: 46; y: metricValue.y + metricValue.height + 5
                    width: parent.width - 46
                    text: metric.modelData.detail
                    color: Tokens.secondary; font.pixelSize: 12
                }
                Canvas {
                    id: arc
                    visible: metric.modelData.kind === "solar" && root.solarAvailable
                    x: 3; y: 78; width: parent.width - 6; height: 30
                    onWidthChanged: requestPaint()
                    onHeightChanged: requestPaint()
                    Connections {
                        target: root
                        function onSolarProgressChanged() { arc.requestPaint() }
                        function onSolarAvailableChanged() { arc.requestPaint() }
                    }
                    onPaint: {
                        const c=getContext("2d"); c.reset()
                        if (!root.solarAvailable) return
                        const left=8, right=width-8, bottom=height-3, rise=height-8
                        c.strokeStyle="#afc6d8"; c.lineWidth=1.2
                        c.beginPath(); c.moveTo(left,bottom); c.quadraticCurveTo(width/2,bottom-rise*2,right,bottom); c.stroke()
                        c.strokeStyle="rgba(174,197,212,0.35)"; c.beginPath(); c.moveTo(0,bottom); c.lineTo(width,bottom); c.stroke()
                        c.fillStyle="#b7cbdc"
                        for (const x of [left,right]) { c.beginPath(); c.arc(x,bottom,2,0,Math.PI*2); c.fill() }
                        const p=root.solarProgress
                        // No invented daytime sun when now lies outside actual sunrise/sunset.
                        if (p>=0 && p<=1) {
                            const x=left+(right-left)*p, y=bottom-4*p*(1-p)*rise
                            const glow=c.createRadialGradient(x,y,0,x,y,12)
                            glow.addColorStop(0,"rgba(245,214,120,0.32)"); glow.addColorStop(1,"rgba(245,214,120,0)")
                            c.fillStyle=glow; c.fillRect(x-12,y-12,24,24)
                            c.fillStyle="#f5d678"; c.beginPath(); c.arc(x,y,4.5,0,Math.PI*2); c.fill()
                        }
                    }
                }
                PlainLabel {
                    visible: metric.modelData.kind === "solar" && root.solarAvailable
                    x: 3; y: 109; width: parent.width/2 - 3
                    text: root.day ? (root.day.sunrise_label || "—") : "—"
                    color: Tokens.secondary; font.pixelSize: 10
                }
                PlainLabel {
                    visible: metric.modelData.kind === "solar" && root.solarAvailable
                    x: parent.width/2; y: 109; width: parent.width/2 - 3
                    horizontalAlignment: Text.AlignRight
                    text: root.day ? (root.day.sunset_label || "—") : "—"
                    color: Tokens.secondary; font.pixelSize: 10
                }
            }
        }
    }
    Rectangle { x:22; y:143; width:parent.width-44; height:1; color:Tokens.border }
    Rectangle { x:22; y:276; width:parent.width-44; height:1; color:Tokens.border }
    Rectangle { x:parent.width/2; y:20; width:1; height:parent.height-40; color:Tokens.border }
}
