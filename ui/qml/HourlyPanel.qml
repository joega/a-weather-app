import QtQuick
import QtQuick.Controls
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    property var hours: []
    property string units: "F"
    property string timezone: "America/New_York"
    signal hourSelected(var hour)
    implicitHeight: 224
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
        y: 80
        text: "Hourly forecast unavailable"
        color: Tokens.secondary
    }
    Flickable {
        id: rail
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
                    required property var modelData
                    required property int index
                    objectName: "forecastHour_" + index
                    width: Math.max(86, rail.width / 12)
                    height: 146
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
                        color: parent.hovered || parent.activeFocus ? "#305e819a" : "transparent"
                        border.color: parent.activeFocus ? Tokens.accent : "transparent"
                    }
                    Rectangle {
                        anchors.right: parent.right
                        y: 4
                        width: 1
                        height: 108
                        color: Tokens.border
                        visible: index < root.hours.length - 1
                    }
                    Column {
                        anchors.horizontalCenter: parent.horizontalCenter
                        spacing: 4
                        PlainLabel {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: modelData.local_hour
                            font.pixelSize: 14
                        }
                        WeatherIcon {
                            anchors.horizontalCenter: parent.horizontalCenter
                            condition: modelData.condition
                            isDay: modelData.is_day
                        }
                        PlainLabel {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: Forecast.temp(modelData.temperature_c, root.units)
                            font.pixelSize: 23
                        }
                        PlainLabel {
                            anchors.horizontalCenter: parent.horizontalCenter
                            text: Forecast.percent(modelData.precipitation_probability)
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
                                width: parent.width * (modelData.precipitation_probability || 0)
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
        anchors.bottom: parent.bottom
        anchors.bottomMargin: 12
        text: "Precipitation chance · Select an hour for details"
        font.pixelSize: 12
        color: Tokens.secondary
    }
}
