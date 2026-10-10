pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Layouts

Rectangle {
    id: root
    objectName: "forecastShareCard"
    required property var summary
    color: "#17374d"
    radius: 14
    implicitHeight: body.implicitHeight + 48
    ColumnLayout {
        id: body
        x: 24
        y: 24
        width: parent.width - 48
        spacing: 14
        PlainLabel {
            Layout.fillWidth: true
            text: "A WEATHER APP"
            color: "#a9d9eb"
            font.pixelSize: Tokens.fontSize(12)
            font.letterSpacing: 2
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.place : "Forecast unavailable"
            font.pixelSize: Tokens.fontSize(30)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.timezone : "Refresh the forecast to share it."
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(13)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        RowLayout {
            Layout.fillWidth: true
            visible: root.summary !== null
            PlainLabel {
                text: root.summary ? root.summary.temperature : ""
                font.pixelSize: Tokens.fontSize(60)
                font.weight: Font.Light
            }
            Item {
                Layout.fillWidth: true
            }
            WeatherIcon {
                Layout.preferredWidth: 72
                Layout.preferredHeight: 63
                condition: root.summary ? root.summary.conditionKey : "unknown"
                isDay: root.summary ? root.summary.isDay : true
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.condition + " · " + root.summary.feels + "\n" + root.summary.wind : ""
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.valid : ""
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        Repeater {
            model: root.summary ? root.summary.days : []
            delegate: ColumnLayout {
                id: day
                required property var modelData
                Layout.fillWidth: true
                spacing: 4
                Rectangle {
                    Layout.fillWidth: true
                    implicitHeight: 1
                    color: Tokens.border
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: day.modelData.date + " · " + day.modelData.condition
                    font.bold: true
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: day.modelData.temperatures + "\n" + day.modelData.precipitation
                    font.pixelSize: Tokens.fontSize(14)
                    color: Tokens.secondary
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.alerts : ""
            color: "#d7e8f0"
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: root.summary ? root.summary.freshness + "\n" + root.summary.retrieved + "\n" + root.summary.source + " · " + root.summary.attribution : ""
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
    }
}
