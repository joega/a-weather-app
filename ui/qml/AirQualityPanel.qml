pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Layouts
import "Forecast.js" as Forecast
import "AirQuality.js" as AirQuality

GlassPanel {
    id: root
    objectName: "airQualityPanel"
    property bool outlookEnabled: false
    signal outlookRequested
    property var airQuality: Forecast.airQuality()
    implicitHeight: Math.max(224, body.implicitHeight + 40)
    function index(value) {
        return value === null ? "—" : Math.round(value).toString();
    }
    function particulate(value) {
        return value === null ? "—" : value.toFixed(1) + " µg/m³";
    }
    readonly property string statusText: {
        let a = airQuality;
        let label = ({
                fresh: "Fresh air quality model data",
                stale: "Stale air quality model data",
                expired: "Air quality data expired · values hidden",
                invalid_future: "Invalid air quality timestamp · values hidden",
                unavailable: "Air quality unavailable"
            })[a.freshness];
        if (a.offline)
            label += " · Offline";
        if (a.refreshing)
            label += " · Updating";
        if (a.error !== null)
            label += " · " + ({
                    fetch_failed: "Update failed",
                    timeout: "Update timed out",
                    cache_invalid: "Cached data invalid",
                    save_failed: "Could not save update"
                })[a.error];
        return label;
    }
    ColumnLayout {
        id: body
        x: 22
        y: 20
        width: parent.width - 44
        spacing: 10
        RowLayout {
            Layout.fillWidth: true
            PlainLabel {
                Layout.fillWidth: true
                text: "Air quality · CAMS global model"
                font.pixelSize: Tokens.fontSize(20)
                font.weight: Font.DemiBold
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "openAirOutlook"
                text: "Outlook"
                accessibleLabel: "Open 48-hour air quality outlook"
                enabled: root.outlookEnabled
                onClicked: root.outlookRequested()
            }
        }
        PlainLabel {
            objectName: "airQualityStatus"
            Layout.fillWidth: true
            text: root.statusText
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(14)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        RowLayout {
            Layout.fillWidth: true
            spacing: 12
            Repeater {
                model: [
                    {
                        kind: "us",
                        title: "US AQI scale",
                        value: root.index(root.airQuality.us_aqi),
                        category: AirQuality.category(root.airQuality.us_aqi, "us")
                    },
                    {
                        kind: "eu",
                        title: "European AQI scale",
                        value: root.index(root.airQuality.european_aqi),
                        category: AirQuality.category(root.airQuality.european_aqi, "eu")
                    },
                    {
                        kind: "pm",
                        title: "PM2.5 · µg/m³",
                        value: root.particulate(root.airQuality.pm2_5_ug_m3),
                        category: "Fine particles"
                    }
                ]
                delegate: ColumnLayout {
                    id: pollutantRow
                    required property var modelData
                    Layout.fillWidth: true
                    Layout.preferredWidth: 1
                    spacing: 4
                    PlainLabel {
                        Layout.fillWidth: true
                        text: pollutantRow.modelData.title
                        horizontalAlignment: Text.AlignHCenter
                        color: Tokens.secondary
                        font.pixelSize: Tokens.fontSize(13)
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    PlainLabel {
                        objectName: "airQualityValue_" + pollutantRow.modelData.kind
                        Layout.fillWidth: true
                        text: pollutantRow.modelData.value
                        horizontalAlignment: Text.AlignHCenter
                        font.pixelSize: Tokens.fontSize(22)
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    PlainLabel {
                        objectName: "airQualityCategory_" + pollutantRow.modelData.kind
                        Layout.fillWidth: true
                        text: pollutantRow.modelData.category
                        horizontalAlignment: Text.AlignHCenter
                        color: Tokens.secondary
                        font.pixelSize: Tokens.fontSize(13)
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                }
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "The two indices use different scales. Lower values indicate cleaner air."
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            objectName: "airQualityTimes"
            Layout.fillWidth: true
            text: root.airQuality.valid_label === null ? "Model forecast time unavailable" : "Model forecast valid " + root.airQuality.valid_label + " · Fetched " + root.airQuality.fetched_label
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Air quality updates separately, at most hourly. Forecast Refresh does not force an air quality update."
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            objectName: "airQualityAttribution"
            Layout.fillWidth: true
            text: root.airQuality.attribution
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(11)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
    }
}
