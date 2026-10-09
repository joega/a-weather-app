pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import "Forecast.js" as Forecast
import "Dashboard.js" as Dashboard

GlassPanel {
    id: root
    property var hours: []
    property string units: "F"
    property string timezone: "America/New_York"
    property string windUnits: "auto"
    property bool compact: false
    property var values: Dashboard.defaults().hourly
    function valueText(hour, key) {
        if (key === "temperature_c" || key === "apparent_temperature_c")
            return Forecast.temp(hour[key], units);
        if (key === "wind_speed_m_s" || key === "wind_gust_m_s")
            return Forecast.wind(hour[key], units, windUnits);
        if (key === "uv_index")
            return Forecast.uv(hour[key]);
        return Forecast.percent(hour[key]);
    }
    signal hourSelected(var hour)
    implicitHeight: root.hours.length === 0 ? 124 : (compact ? 150 : 158) + values.length * (compact ? 42 : 46)
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
        height: root.implicitHeight - 82
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
                    width: Math.max(132, rail.width / 10)
                    height: implicitContentHeight + topPadding + bottomPadding
                    padding: root.compact ? 4 : 8
                    Accessible.name: modelData.local_hour + ", " + Forecast.title(modelData.condition) + ", " + root.values.map(key => Dashboard.title(key) + " " + root.valueText(modelData, key)).join(", ") + ". Open details"
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
                        Repeater {
                            model: root.values
                            delegate: Column {
                                id: valueRow
                                required property string modelData
                                required property int index
                                width: hourButton.width - hourButton.leftPadding - hourButton.rightPadding
                                spacing: 1
                                PlainLabel {
                                    width: parent.width
                                    horizontalAlignment: Text.AlignHCenter
                                    text: Dashboard.title(valueRow.modelData)
                                    font.pixelSize: 11
                                    color: Tokens.secondary
                                }
                                PlainLabel {
                                    objectName: "hourlyValue_" + hourButton.index + "_" + valueRow.modelData
                                    width: parent.width
                                    horizontalAlignment: Text.AlignHCenter
                                    text: root.valueText(hourButton.modelData, valueRow.modelData)
                                    font.pixelSize: valueRow.index === 0 ? 22 : 16
                                    color: valueRow.modelData === "precipitation_probability" ? Tokens.accent : Tokens.foreground
                                }
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
        text: "Select an hour for details"
        font.pixelSize: 12
        color: Tokens.secondary
    }
}
