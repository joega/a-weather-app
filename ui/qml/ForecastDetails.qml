pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

Popup {
    id: root
    objectName: "forecastDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(730, parent ? parent.height - 40 : 730)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var forecast: null
    readonly property string timezone: forecast ? forecast.location.timezone : "UTC"
    property string units: "F"
    property string windUnits: "auto"
    property string freshness: ""
    property string selectedDate: ""
    property string selectedTime: ""
    property string metric: "temperature_c"
    signal precipitationRequested(string date)
    readonly property var day: forecast && selectedDate ? forecast.daily.find(d => d.date === selectedDate) || null : null
    readonly property var hours: !visible || !forecast ? [] : selectedDate ? forecast.hourly.filter(h => h.local_date === selectedDate) : forecast.hourly.slice(0, 24)
    readonly property int selectedIndex: Math.max(0, hours.findIndex(h => h.time === selectedTime))
    readonly property var hour: hours.length ? hours[selectedIndex] : null
    function revealFocus(item) {
        let flick = scroll.contentItem as Flickable, ancestor = item;
        if (!flick || !flick.contentItem)
            return;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        let point = item.mapToItem(flick.contentItem, 0, 0), next = flick.contentY;
        if (point.y < next + 12)
            next = point.y - 12;
        else if (point.y + item.height > next + flick.height - 12)
            next = point.y + item.height - flick.height + 12;
        flick.contentY = Math.max(0, Math.min(next, Math.max(0, flick.contentHeight - flick.height)));
    }
    function showHour(row) {
        selectedDate = "";
        selectedTime = row.time;
        metric = "temperature_c";
        open();
    }
    function showMetric(key) {
        selectedDate = "";
        selectedTime = "";
        metric = key;
        open();
    }
    function showDay(row) {
        selectedDate = row.date;
        selectedTime = "";
        metric = "temperature_c";
        open();
    }
    onOpened: {
        closeButton.forceActiveFocus();
        if (scroll.contentItem)
            scroll.contentItem.contentY = 0;
    }
    background: Rectangle {
        color: "#2c455a"
        radius: 18
        border.color: Tokens.border
    }
    Overlay.modal: Rectangle {
        color: "#990b1725"
    }
    contentItem: ScrollView {
        id: scroll
        objectName: "forecastDetailsScroll"
        clip: true
        // Keep the overlay scrollbar outside the content and controls.
        rightPadding: 18
        contentWidth: availableWidth
        contentHeight: detailBody.implicitHeight
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.vertical.active: true
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        ColumnLayout {
            id: detailBody
            width: scroll.availableWidth
            spacing: 16
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: root.day ? (I18n.date(root.day.date, root.timezone, "day") || root.day.day_label) + " · " + root.day.date : I18n.t("Hourly forecast")
                    font.pixelSize: Tokens.fontSize(26)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeForecastDetails"
                    iconName: "close"
                    accessibleLabel: I18n.t("Close forecast details")
                    onClicked: root.close()
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: root.freshness
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(13)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                visible: root.day !== null
                Layout.fillWidth: true
                font.pixelSize: Tokens.fontSize(19)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                text: root.day ? I18n.t("%1 · High %2 · Low %3\nPrecipitation chance %4").arg((I18n.revision, Forecast.title(root.day.condition))).arg((I18n.revision, Forecast.temp(root.day.high_c, root.units))).arg((I18n.revision, Forecast.temp(root.day.low_c, root.units))).arg((I18n.revision, Forecast.percent(root.day.precipitation_probability))) : ""
            }
            PlainLabel {
                visible: root.day !== null
                Layout.fillWidth: true
                text: root.day ? I18n.t("Sunrise %1 · Sunset %2").arg(((I18n.date(root.day.sunrise, root.timezone, "time") || root.day.sunrise_label) || "—")).arg(((I18n.date(root.day.sunset, root.timezone, "time") || root.day.sunset_label) || "—")) : ""
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "detailPrecipitation"
                text: I18n.t("Rain & snow totals")
                onClicked: root.precipitationRequested(root.selectedDate || (root.hour ? root.hour.local_date : ""))
            }
            SettingsComboBox {
                objectName: "forecastMetric"
                Layout.fillWidth: true
                Accessible.name: I18n.t("Forecast measurement")
                model: Forecast.metricChoices
                textRole: "label"
                valueRole: "value"
                currentIndex: Forecast.metricChoices.findIndex(row => row.value === root.metric)
                onActivated: root.metric = currentValue
            }
            Loader {
                objectName: "forecastChartLoader"
                Layout.fillWidth: true
                Layout.preferredHeight: 180
                active: root.visible
                sourceComponent: ForecastChart {
                    timezone: root.timezone
                    hours: root.hours
                    units: root.units
                    windUnits: root.windUnits
                    metric: root.metric
                    selectedIndex: root.selectedIndex
                    onSelected: index => root.selectedTime = root.hours[index].time
                }
            }
            PlainLabel {
                objectName: "selectedForecastValue"
                Layout.fillWidth: true
                text: (I18n.revision, Forecast.metricInfo(root.metric)).label + ": " + (I18n.revision, Forecast.metricValue(root.hour ? root.hour[root.metric] : null, root.metric, root.units, root.windUnits))
                font.pixelSize: Tokens.fontSize(22)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                objectName: "forecastMetricRange"
                Layout.fillWidth: true
                text: (I18n.revision, Forecast.metricRange(root.hours, root.metric, root.units, root.windUnits))
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("%1 Only remaining forecast hours are shown.").arg((I18n.revision, Forecast.metricInfo(root.metric)).explanation)
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            RowLayout {
                Layout.fillWidth: true
                ActionButton {
                    objectName: "previousForecastHour"
                    text: I18n.t("Previous")
                    enabled: root.selectedIndex > 0
                    onClicked: root.selectedTime = root.hours[root.selectedIndex - 1].time
                }
                PlainLabel {
                    objectName: "selectedForecastHour"
                    Layout.fillWidth: true
                    text: root.hour ? (I18n.date(root.hour.time, root.timezone, "long") || root.hour.local_label) : I18n.t("Hourly detail unavailable")
                    horizontalAlignment: Text.AlignHCenter
                    font.pixelSize: Tokens.fontSize(16)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    objectName: "nextForecastHour"
                    text: I18n.t("Next")
                    enabled: root.selectedIndex < root.hours.length - 1
                    onClicked: root.selectedTime = root.hours[root.selectedIndex + 1].time
                }
            }
            PlainLabel {
                visible: root.hour !== null
                Layout.fillWidth: true
                text: root.hour ? I18n.t("%1 · %2 · Feels like %3").arg((I18n.revision, Forecast.title(root.hour.condition))).arg((I18n.revision, Forecast.temp(root.hour.temperature_c, root.units))).arg((I18n.revision, Forecast.temp(root.hour.apparent_temperature_c, root.units))) : ""
                font.pixelSize: Tokens.fontSize(22)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            GridLayout {
                visible: root.hour !== null
                Layout.fillWidth: true
                columns: 2
                columnSpacing: 24
                rowSpacing: 14
                Repeater {
                    model: root.hour ? [
                        {
                            title: I18n.t("Precipitation chance"),
                            value: (I18n.revision, Forecast.percent(root.hour.precipitation_probability))
                        },
                        {
                            title: I18n.t("Precipitation amount"),
                            value: (I18n.revision, Forecast.amount(root.hour.precipitation_rate_mm_hr, root.units))
                        },
                        {
                            title: I18n.t("Wind"),
                            value: (I18n.revision, Forecast.direction(root.hour.wind_direction_deg)) + " " + (I18n.revision, Forecast.wind(root.hour.wind_speed_m_s, root.units, root.windUnits))
                        },
                        {
                            title: I18n.t("Gusts"),
                            value: (I18n.revision, Forecast.wind(root.hour.wind_gust_m_s, root.units, root.windUnits))
                        },
                        {
                            title: I18n.t("Humidity"),
                            value: (I18n.revision, Forecast.percent(root.hour.humidity))
                        },
                        {
                            title: I18n.t("Cloud cover"),
                            value: (I18n.revision, Forecast.percent(root.hour.cloud_cover))
                        },
                        {
                            kind: "uv",
                            title: I18n.t("UV index"),
                            value: (I18n.revision, Forecast.uv(root.hour.uv_index))
                        },
                        {
                            kind: "pressure",
                            title: I18n.t("Mean sea level pressure"),
                            value: (I18n.revision, Forecast.pressure(root.hour.pressure_msl_hpa, root.units))
                        },
                        {
                            kind: "dew_point",
                            title: I18n.t("Dew point"),
                            value: (I18n.revision, Forecast.temp(root.hour.dew_point_c, root.units))
                        }
                    ] : []
                    delegate: ColumnLayout {
                        id: metricRow
                        required property var modelData
                        Layout.fillWidth: true
                        Layout.preferredWidth: 1
                        spacing: 4
                        PlainLabel {
                            Layout.fillWidth: true
                            text: metricRow.modelData.title
                            font.pixelSize: Tokens.fontSize(13)
                            color: Tokens.secondary
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        PlainLabel {
                            objectName: metricRow.modelData.kind ? "detailMetricValue_" + metricRow.modelData.kind : ""
                            Layout.fillWidth: true
                            text: metricRow.modelData.value
                            font.pixelSize: Tokens.fontSize(20)
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                    }
                }
            }
            PlainLabel {
                visible: root.hour !== null
                Layout.fillWidth: true
                text: root.hour ? I18n.t("Precipitation interval: %1. Times are local to the forecast location.").arg((root.hour.period_label || I18n.t("the preceding hour"))) : ""
                font.pixelSize: Tokens.fontSize(12)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
