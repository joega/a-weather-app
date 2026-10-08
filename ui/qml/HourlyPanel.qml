pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    property var hours: []
    property string units: "F"
    property string timezone: "America/New_York"
    signal hourSelected(var hour)
    implicitHeight: root.hours.length === 0 ? 124 : 224
    PlainLabel {
        x: 20
        y: 14
        text: "Next 24 hours"
        font.pixelSize: 20
        font.weight: Font.DemiBold
    }
    PlainLabel {
        visible: root.hours.length === 0
        x: 20
        y: 64
        text: "Hourly forecast unavailable"
        color: Tokens.secondary
    }
    Flickable {
        id: rail
        visible: root.hours.length > 0
        objectName: "hourlyRail"
        x: 16
        y: 50
        width: parent.width - 32
        height: 157
        contentWidth: hourRow.width
        contentHeight: height
        clip: true
        boundsBehavior: Flickable.StopAtBounds
        ScrollBar.horizontal: ScrollBar {}
        Row {
            id: hourRow
            Repeater {
                model: root.hours
                delegate: Button {
                    id: hourButton
                    required property var modelData
                    required property int index
                    objectName: "forecastHour_" + index
                    width: Math.max(86, rail.width / 12)
                    height: implicitContentHeight + topPadding + bottomPadding
                    padding: 8
                    Accessible.name: modelData.local_hour + ", " + Forecast.title(modelData.condition) + ", " + Forecast.temp(modelData.temperature_c, root.units) + ", precipitation chance " + Forecast.percent(modelData.precipitation_probability) + ". Open details"
                    onActiveFocusChanged: if (activeFocus) {
                        if (x < rail.contentX)
                            rail.contentX = x;
                        else if (x + width > rail.contentX + rail.width)
                            rail.contentX = x + width - rail.width;
                    }
                    onClicked: root.hourSelected(modelData)
                    background: Rectangle {
                        radius: 9
                        color: hourButton.hovered || hourButton.activeFocus ? "#305e819a" : "transparent"
                        border.color: hourButton.activeFocus ? Tokens.accent : "transparent"
                    }
                    contentItem: Column {
                        spacing: 4
                        PlainLabel {
                            width: parent.width
                            horizontalAlignment: Text.AlignHCenter
                            text: hourButton.modelData.local_hour
                            font.pixelSize: 14
                        }
                        WeatherIcon {
                            anchors.horizontalCenter: parent.horizontalCenter
                            condition: hourButton.modelData.condition
                            isDay: hourButton.modelData.is_day
                        }
                        PlainLabel {
                            width: parent.width
                            horizontalAlignment: Text.AlignHCenter
                            text: Forecast.temp(hourButton.modelData.temperature_c, root.units)
                            font.pixelSize: 23
                        }
                        PlainLabel {
                            width: parent.width
                            horizontalAlignment: Text.AlignHCenter
                            text: Forecast.percent(hourButton.modelData.precipitation_probability)
                            color: Tokens.accent
                            font.pixelSize: 14
                        }
                        Rectangle {
                            width: 44
                            height: 5
                            radius: 2
                            color: "#36566e"
                            anchors.horizontalCenter: parent.horizontalCenter
                            Rectangle {
                                width: parent.width * (hourButton.modelData.precipitation_probability || 0)
                                height: parent.height
                                radius: 2
                                color: Tokens.accent
                            }
                        }
                    }
                }
            }
        }
    }
    PlainLabel {
        x: 20
        visible: root.hours.length > 0
        anchors.bottom: parent.bottom
        anchors.bottomMargin: 12
        text: "Precipitation chance · Select an hour for details"
        font.pixelSize: 12
        color: Tokens.secondary
    }
}
