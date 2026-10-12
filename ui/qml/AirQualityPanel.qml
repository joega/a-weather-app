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
    property var airQuality: I18n.observe(Forecast.airQuality())
    implicitHeight: Math.max(224, body.implicitHeight + 40)
    function index(value) {
        return value === null ? "—" : Math.round(value).toString();
    }
    function particulate(value) {
        return value === null ? "—" : value.toFixed(1) + I18n.t(" µg/m³");
    }
    readonly property string statusText: {
        let a = airQuality;
        let label = ({
                fresh: I18n.t("Fresh air quality model data"),
                stale: I18n.t("Stale air quality model data"),
                expired: I18n.t("Air quality data expired · values hidden"),
                invalid_future: I18n.t("Invalid air quality timestamp · values hidden"),
                unavailable: I18n.t("Air quality unavailable")
            })[a.freshness];
        if (a.offline)
            label += I18n.t(" · Offline");
        if (a.refreshing)
            label += I18n.t(" · Updating");
        if (a.error !== null)
            label += " · " + ({
                    fetch_failed: I18n.t("Update failed"),
                    timeout: I18n.t("Update timed out"),
                    cache_invalid: I18n.t("Cached data invalid"),
                    save_failed: I18n.t("Could not save update")
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
                text: I18n.t("Air quality · CAMS global model")
                font.pixelSize: Tokens.fontSize(20)
                font.weight: Font.DemiBold
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "openAirOutlook"
                text: I18n.t("Outlook")
                accessibleLabel: I18n.t("Open 48-hour air quality outlook")
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
                        title: I18n.t("US AQI scale"),
                        value: root.index(root.airQuality.us_aqi),
                        category: I18n.observe(AirQuality.category(root.airQuality.us_aqi, "us"))
                    },
                    {
                        kind: "eu",
                        title: I18n.t("European AQI scale"),
                        value: root.index(root.airQuality.european_aqi),
                        category: I18n.observe(AirQuality.category(root.airQuality.european_aqi, "eu"))
                    },
                    {
                        kind: "pm",
                        title: I18n.t("PM2.5 · µg/m³"),
                        value: root.particulate(root.airQuality.pm2_5_ug_m3),
                        category: I18n.t("Fine particles")
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
            text: I18n.t("The two indices use different scales. Lower values indicate cleaner air.")
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            objectName: "airQualityTimes"
            Layout.fillWidth: true
            text: root.airQuality.valid_label === null ? I18n.t("Model forecast time unavailable") : I18n.t("Model forecast valid %1 · Fetched %2").arg(root.airQuality.valid_label).arg(root.airQuality.fetched_label)
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(12)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            text: I18n.t("Air quality updates separately, at most hourly. Forecast Refresh does not force an air quality update.")
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
