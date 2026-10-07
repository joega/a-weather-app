pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    property var days: []
    property string units: "F"
    signal daySelected(var day)
    property real rangeLow: {
        let n = 100;
        for (let d of days)
            if (d.low_c !== null)
                n = Math.min(n, d.low_c);
        return n;
    }
    property real rangeHigh: {
        let n = -150;
        for (let d of days)
            if (d.high_c !== null)
                n = Math.max(n, d.high_c);
        return n;
    }
    implicitHeight: root.days.length === 0 ? 252 : 66 + Math.min(10, days.length) * 37
    PlainLabel {
        x: 20
        y: 14
        text: "10-day forecast"
        font.pixelSize: 20
        font.weight: Font.DemiBold
    }
    PlainLabel {
        visible: root.days.length === 0
        x: 20
        y: 92
        text: "Daily forecast unavailable"
        color: Tokens.secondary
    }
    Column {
        x: 20
        y: 50
        width: parent.width - 40
        Repeater {
            model: root.days.slice(0, 10)
            delegate: Button {
                id: dayRow
                required property var modelData
                required property int index
                objectName: "forecastDay_" + index
                width: parent.width
                height: 37
                Accessible.name: modelData.day_label + ", " + Forecast.title(modelData.condition) + ", low " + Forecast.temp(modelData.low_c, root.units) + ", high " + Forecast.temp(modelData.high_c, root.units) + ". Open details"
                onClicked: root.daySelected(modelData)
                background: Rectangle {
                    radius: 7
                    color: dayRow.hovered || dayRow.activeFocus ? "#305e819a" : "transparent"
                    border.color: dayRow.activeFocus ? Tokens.accent : "transparent"
                }
                PlainLabel {
                    x: 0
                    anchors.verticalCenter: parent.verticalCenter
                    width: 58
                    text: dayRow.modelData.day_label
                    font.pixelSize: 15
                }
                WeatherIcon {
                    x: 58
                    anchors.verticalCenter: parent.verticalCenter
                    width: 34
                    height: 31
                    condition: dayRow.modelData.condition
                }
                PlainLabel {
                    x: 102
                    anchors.verticalCenter: parent.verticalCenter
                    width: Math.max(0, parent.width * 0.34 - 102)
                    visible: parent.width > 400
                    text: Forecast.title(dayRow.modelData.condition)
                    font.pixelSize: 13
                    color: Tokens.secondary
                }
                PlainLabel {
                    x: parent.width * 0.47 - 30
                    anchors.verticalCenter: parent.verticalCenter
                    text: Forecast.temp(dayRow.modelData.low_c, root.units)
                    font.pixelSize: 15
                }
                Rectangle {
                    x: parent.width * 0.53
                    y: 17
                    width: parent.width * 0.31
                    height: 5
                    radius: 3
                    color: "#507d94a9"
                    Rectangle {
                        visible: dayRow.modelData.low_c !== null && dayRow.modelData.high_c !== null
                        x: (dayRow.modelData.low_c - root.rangeLow) / Math.max(1, root.rangeHigh - root.rangeLow) * parent.width
                        width: Math.max(3, (dayRow.modelData.high_c - dayRow.modelData.low_c) / Math.max(1, root.rangeHigh - root.rangeLow) * parent.width)
                        height: 5
                        radius: 3
                        gradient: Gradient {
                            orientation: Gradient.Horizontal
                            GradientStop {
                                position: 0
                                color: "#77c4f3"
                            }
                            GradientStop {
                                position: 1
                                color: Tokens.gold
                            }
                        }
                    }
                }
                PlainLabel {
                    anchors.right: parent.right
                    anchors.verticalCenter: parent.verticalCenter
                    text: Forecast.temp(dayRow.modelData.high_c, root.units)
                    font.pixelSize: 15
                }
                Rectangle {
                    anchors.bottom: parent.bottom
                    width: parent.width
                    height: 1
                    color: Tokens.border
                    opacity: 0.45
                }
            }
        }
    }
}
