pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "AirOutlook.js" as AirOutlook

Popup {
    id: root
    objectName: "airOutlookDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(750, parent ? parent.height - 40 : 750)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var result: null
    property string state: "loading"
    property string error: ""
    property string place: ""
    property string metric: "us_aqi"
    property string selectedTime: ""
    readonly property var hours: result ? result.hours : []
    readonly property var info: AirOutlook.info(metric)
    readonly property int selectedIndex: Math.max(0, hours.findIndex(h => h.time === selectedTime))
    readonly property var hour: hours[selectedIndex] || null
    signal requested
    function selectHour(index) {
        if (index >= 0 && index < hours.length)
            selectedTime = hours[index].time;
    }
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        if (!flick || !item)
            return;
        let ancestor = item;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const y = item.mapToItem(flick.contentItem, 0, 0).y;
        if (y < flick.contentY + 12)
            flick.contentY = Math.max(0, y - 12);
        else if (y + item.height > flick.contentY + flick.height - 12)
            flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, y + item.height - flick.height + 12));
    }
    onOpened: closeButton.forceActiveFocus()
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
        objectName: "airOutlookScroll"
        clip: true
        contentWidth: availableWidth
        contentHeight: body.implicitHeight
        rightPadding: 18
        ScrollBar.vertical.active: true
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        Keys.onPressed: event => {
            const flick = scroll.contentItem as Flickable;
            if (!flick || [Qt.Key_PageDown, Qt.Key_PageUp].indexOf(event.key) < 0)
                return;
            flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + (event.key === Qt.Key_PageDown ? 1 : -1) * flick.height * .8));
            event.accepted = true;
        }
        ColumnLayout {
            id: body
            width: scroll.availableWidth
            spacing: 16
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Air quality outlook"
                    font.pixelSize: Tokens.fontSize(26)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeAirOutlook"
                    iconName: "close"
                    accessibleLabel: "Close air quality outlook"
                    onClicked: root.close()
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: root.place + (root.result ? " · " + root.result.timezone : "")
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                objectName: "airOutlookStatus"
                Layout.fillWidth: true
                text: root.error === "air_outlook_in_use" ? "Air quality outlook is open in another window." : root.error === "air_outlook_context_changed" ? "The place changed. Open its outlook again." : root.state === "loading" ? "Loading air quality outlook…" : root.state === "waiting" ? "Waiting before the next update…" : root.result && root.result.fetched_at ? (root.state === "stale" ? "Cached outlook · " : "Updated ") + root.result.fetched_label + (root.result.refreshing ? " · Refreshing…" : root.result.error === "fetch_failed" ? " · Refresh unavailable; retrying while open" : root.result.error === "offline" ? " · Offline" : "") : root.result && root.result.error === "offline" ? "No air quality outlook is saved for this place." : "Air quality outlook is unavailable."
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(13)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "retryAirOutlook"
                visible: root.state === "unavailable"
                text: "Try again"
                onClicked: root.requested()
            }
            SettingsComboBox {
                objectName: "airOutlookMetric"
                Layout.fillWidth: true
                Accessible.name: "Air quality measure"
                model: AirOutlook.metrics
                textRole: "title"
                valueRole: "key"
                currentIndex: AirOutlook.metrics.findIndex(m => m.key === root.metric)
                onActivated: root.metric = currentValue
            }
            PlainLabel {
                Layout.fillWidth: true
                text: root.info.description
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ColumnLayout {
                visible: root.hours.length > 0
                Layout.fillWidth: true
                spacing: 14
                PlainLabel {
                    objectName: "airOutlookTime"
                    Layout.fillWidth: true
                    text: root.hour ? root.hour.label : ""
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "airOutlookValue"
                    Layout.fillWidth: true
                    text: AirOutlook.format(root.hour ? root.hour[root.metric] : null, root.metric)
                    font.pixelSize: Tokens.fontSize(30)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "airOutlookCategory"
                    Layout.fillWidth: true
                    visible: root.info.scale !== ""
                    text: AirOutlook.category(root.hour ? root.hour[root.metric] : null, root.metric)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Next 48 hours from the current hour" + (root.info.unit ? " · " + root.info.unit : "")
                    font.pixelSize: Tokens.fontSize(18)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ForecastChart {
                    objectName: "airOutlookChart"
                    Layout.fillWidth: true
                    Layout.preferredHeight: 190
                    hours: root.hours
                    metric: root.metric
                    metricLabel: root.info.title
                    chartScale: AirOutlook.scale(root.hours, root.metric)
                    rangeDescription: AirOutlook.range(root.hours, root.metric)
                    valueFormatter: value => AirOutlook.format(value, root.metric, false)
                    accessibleValueFormatter: value => AirOutlook.format(value, root.metric) + (root.info.scale ? ", " + AirOutlook.category(value, root.metric) : "")
                    selectedIndex: root.selectedIndex
                    onSelected: index => root.selectHour(index)
                }
                RowLayout {
                    Layout.fillWidth: true
                    ActionButton {
                        objectName: "airOutlookPrevious"
                        text: "Previous"
                        enabled: root.selectedIndex > 0
                        onClicked: root.selectHour(root.selectedIndex - 1)
                    }
                    Item {
                        Layout.fillWidth: true
                    }
                    ActionButton {
                        objectName: "airOutlookNow"
                        text: "Current hour"
                        onClicked: root.selectHour(0)
                    }
                    ActionButton {
                        objectName: "airOutlookNext"
                        text: "Next"
                        enabled: root.selectedIndex < root.hours.length - 1
                        onClicked: root.selectHour(root.selectedIndex + 1)
                    }
                }
                PlainLabel {
                    objectName: "airOutlookRange"
                    Layout.fillWidth: true
                    text: AirOutlook.range(root.hours, root.metric)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: "Modeled forecasts, not local station readings. CAMS global uses a roughly 45 km grid and native three-hourly output; Open-Meteo supplies hourly values. Retrieval time is not model issuance."
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                Layout.fillWidth: true
                text: "AQI averaging periods differ from these hourly concentrations. PM2.5 alone does not identify wildfire smoke. For health advice and local alerts, consult your air-quality authority."
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            GridLayout {
                Layout.fillWidth: true
                columns: width < 500 ? 1 : 3
                ActionButton {
                    text: "US AQI guide"
                    accessibleLabel: "US AQI guide, opens browser"
                    onClicked: Qt.openUrlExternally("https://www.airnow.gov/aqi/aqi-basics/")
                }
                ActionButton {
                    text: "European AQI guide"
                    accessibleLabel: "European AQI guide, opens browser"
                    onClicked: Qt.openUrlExternally("https://airindex.eea.europa.eu/AQI/index.html")
                }
                ActionButton {
                    text: "Data source"
                    accessibleLabel: "Air quality data source, opens browser"
                    onClicked: Qt.openUrlExternally("https://open-meteo.com/en/docs/air-quality-api")
                }
            }
            PlainLabel {
                objectName: "airOutlookSource"
                Layout.fillWidth: true
                text: "CAMS global model data via Open-Meteo (CC BY 4.0)" + (root.result && root.result.save_failed ? "\nThis outlook could not be saved for offline use." : "")
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(12)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
