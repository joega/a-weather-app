pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Layouts

Rectangle {
    id: root
    objectName: "forecastShareCard"
    required property var summary
    readonly property bool wideDays: body.width >= Tokens.fontSize(580)
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
                required property int index
                objectName: "shareDay_" + index
                Layout.fillWidth: true
                spacing: 10
                Rectangle {
                    Layout.fillWidth: true
                    implicitHeight: 1
                    color: Tokens.border
                }
                GridLayout {
                    Layout.fillWidth: true
                    columns: root.wideDays ? 2 : 1
                    columnSpacing: 16
                    rowSpacing: 14
                    ColumnLayout {
                        Layout.preferredWidth: root.wideDays ? Tokens.fontSize(210) : -1
                        Layout.fillWidth: !root.wideDays
                        Layout.alignment: Qt.AlignTop
                        spacing: 4
                        PlainLabel {
                            Layout.fillWidth: true
                            text: day.modelData.date
                            font.bold: true
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            text: day.modelData.condition
                            font.pixelSize: Tokens.fontSize(14)
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            text: day.modelData.temperatures + "\n" + day.modelData.precipitation
                            font.pixelSize: Tokens.fontSize(13)
                            color: Tokens.secondary
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                    }
                    RowLayout {
                        objectName: "sharePeriods_" + day.index
                        Layout.fillWidth: true
                        Layout.alignment: Qt.AlignTop
                        spacing: 10
                        Repeater {
                            model: day.modelData.periods || []
                            delegate: ColumnLayout {
                                id: period
                                required property var modelData
                                required property int index
                                objectName: "sharePeriod_" + day.index + "_" + index
                                Layout.fillWidth: true
                                Layout.preferredWidth: 1
                                Layout.alignment: Qt.AlignTop
                                spacing: 5
                                PlainLabel {
                                    Layout.fillWidth: true
                                    text: period.modelData.name
                                    font.pixelSize: Tokens.fontSize(12)
                                    font.bold: true
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    text: period.modelData.range
                                    font.pixelSize: Tokens.fontSize(10)
                                    color: Tokens.secondary
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    text: period.modelData.temperatures
                                    font.pixelSize: Tokens.fontSize(14)
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    text: period.modelData.precipitation
                                    font.pixelSize: Tokens.fontSize(11)
                                    color: Tokens.secondary
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    visible: period.modelData.coverage !== ""
                                    text: period.modelData.coverage
                                    font.pixelSize: Tokens.fontSize(10)
                                    color: Tokens.secondary
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                            }
                        }
                    }
                }
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.summary !== null
            text: "Hourly summaries use local time and the available forecast hours."
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(11)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
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
