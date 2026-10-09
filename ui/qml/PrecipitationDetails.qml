pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast
import "Precipitation.js" as Precipitation

Popup {
    id: root
    objectName: "precipitationDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(770, parent ? parent.height - 40 : 770)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var result: null
    property string state: "loading"
    property string error: ""
    property string place: ""
    property string units: "F"
    property int selectedIndex: 0
    readonly property var day: result ? result.days.find(d => d.date === result.date) || null : null
    readonly property var chartHours: result ? Precipitation.chartHours(result.hours) : []
    readonly property var hour: result && selectedIndex < chartHours.length ? result.hours.find(h => Date.parse(h.start) === Date.parse(chartHours[selectedIndex].time)) || null : null
    signal requested(string date)
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
    onResultChanged: selectedIndex = 0
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
        objectName: "precipitationScroll"
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
                    text: "Rain & snow"
                    font.pixelSize: 26
                }
                ActionButton {
                    id: closeButton
                    objectName: "closePrecipitation"
                    iconName: "close"
                    accessibleLabel: "Close rain and snow"
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
            RowLayout {
                Layout.fillWidth: true
                SettingsComboBox {
                    objectName: "precipitationDate"
                    Layout.fillWidth: true
                    Accessible.name: "Rain and snow forecast date"
                    model: root.result ? root.result.days : []
                    textRole: "label"
                    valueRole: "date"
                    currentIndex: root.result ? root.result.days.findIndex(d => d.date === root.result.date) : -1
                    enabled: count > 0
                    onActivated: root.requested(currentValue)
                }
                ActionButton {
                    objectName: "precipitationToday"
                    text: "Today"
                    onClicked: root.requested("")
                }
            }
            PlainLabel {
                objectName: "precipitationStatus"
                Layout.fillWidth: true
                text: root.error === "precipitation_in_use" ? "Rain and snow details are open in another window." : root.error === "precipitation_context_changed" ? "The place changed. Open its rain and snow details again." : root.state === "loading" ? "Loading rain and snow…" : root.state === "waiting" ? "Waiting before the next update…" : root.result && root.result.fetched_at ? (root.state === "stale" ? "Cached forecast · " : "Updated ") + root.result.fetched_label + (root.result.refreshing ? " · Refreshing…" : root.result.error === "fetch_failed" ? " · Refresh unavailable; retrying while open" : root.result.error === "offline" ? " · Offline" : "") : root.result && root.result.error === "offline" ? "No rain and snow details are saved for this place." : "Rain and snow details are unavailable."
                color: Tokens.secondary
                font.pixelSize: 13
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "retryPrecipitation"
                visible: root.state === "unavailable"
                text: "Try again"
                onClicked: root.requested(root.result ? root.result.date : "")
            }
            ColumnLayout {
                visible: root.day !== null
                Layout.fillWidth: true
                spacing: 16
                PlainLabel {
                    Layout.fillWidth: true
                    text: root.day ? Precipitation.indicated(root.day.kind) : ""
                    font.pixelSize: 20
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                GridLayout {
                    Layout.fillWidth: true
                    columns: width < 500 ? 1 : 3
                    columnSpacing: 16
                    rowSpacing: 12
                    Repeater {
                        model: [
                            {
                                key: "total_mm",
                                title: "Liquid equivalent"
                            },
                            {
                                key: "rain_mm",
                                title: "Rain & showers"
                            },
                            {
                                key: "snow_cm",
                                title: "New snowfall"
                            }
                        ]
                        delegate: ColumnLayout {
                            id: amount
                            required property var modelData
                            Layout.fillWidth: true
                            Layout.preferredWidth: 1
                            spacing: 6
                            PlainLabel {
                                Layout.fillWidth: true
                                text: amount.modelData.title
                                color: Tokens.secondary
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                objectName: "precipitationDaily_" + amount.modelData.key
                                Layout.fillWidth: true
                                text: Precipitation.accumulation(root.day, amount.modelData.key, root.units)
                                font.pixelSize: 26
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                Layout.fillWidth: true
                                text: Precipitation.coverage(root.day, amount.modelData.key)
                                color: Tokens.secondary
                                font.pixelSize: 12
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                        }
                    }
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Modeled totals for the whole local day, including earlier hours. Liquid equivalent includes snow and other precipitation expressed as water."
                    color: Tokens.secondary
                    font.pixelSize: 13
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "precipitationWetHours"
                    Layout.fillWidth: true
                    text: !root.day ? "" : "Highest available hourly chance: " + Forecast.percent(root.day.chance_max) + "\n" + (root.day.wet_hours_subtotal === null ? "Modeled wet hours unavailable" : root.day.wet_hours_subtotal + " modeled hours with at least 0.1 mm" + (root.units === "F" ? " (0.004 in)" : "") + (root.day.wet_hours === null ? " · Partial coverage" : "")) + ". This is not continuous rain or snow duration."
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    text: "Hourly liquid equivalent"
                    font.pixelSize: 18
                }
                ForecastChart {
                    objectName: "precipitationTrend"
                    Layout.fillWidth: true
                    Layout.preferredHeight: 180
                    hours: root.chartHours
                    units: root.units
                    metric: "precipitation_rate_mm_hr"
                    selectedIndex: root.selectedIndex
                    onSelected: index => root.selectedIndex = index
                }
                RowLayout {
                    Layout.fillWidth: true
                    ActionButton {
                        objectName: "precipitationPreviousHour"
                        text: "Previous"
                        enabled: root.selectedIndex > 0
                        onClicked: root.selectedIndex--
                    }
                    PlainLabel {
                        objectName: "precipitationInterval"
                        Layout.fillWidth: true
                        text: root.hour ? root.hour.label : "Hourly data unavailable"
                        horizontalAlignment: Text.AlignHCenter
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    ActionButton {
                        objectName: "precipitationNextHour"
                        text: "Next"
                        enabled: root.selectedIndex < root.chartHours.length - 1
                        onClicked: root.selectedIndex++
                    }
                }
                PlainLabel {
                    objectName: "precipitationHourAmounts"
                    Layout.fillWidth: true
                    text: root.hour ? "Liquid " + Precipitation.liquid(root.hour.total_mm, root.units) + " · Rain " + Precipitation.liquid(root.hour.rain_mm, root.units) + " · New snow " + Precipitation.snow(root.hour.snow_cm, root.units) + "\nChance " + Forecast.percent(root.hour.chance) + " · " + Precipitation.indicated(root.hour.kind) : "No amount is available for this interval."
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: root.hour ? "At the interval's end: ground snow " + Precipitation.snow(root.hour.depth_m === null ? null : root.hour.depth_m * 100, root.units) + " · Freezing level " + Precipitation.height(root.hour.freezing_m, root.units) + " above sea level." : ""
                    color: Tokens.secondary
                    font.pixelSize: 13
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "precipitationSnowDepth"
                    Layout.fillWidth: true
                    text: "Snow on the ground: " + Precipitation.range(root.day, "depth", root.units) + "\nFreezing level: " + Precipitation.range(root.day, "freezing", root.units) + " above sea level"
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Ground snow and freezing level show the range of available samples. Ground snow is not new snowfall. These values do not predict pavement icing or travel safety." + (root.day && root.day.boundary_hours ? " Some hourly intervals cross a local-day boundary and are excluded from totals." : "")
                    color: Tokens.secondary
                    font.pixelSize: 13
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
            PlainLabel {
                objectName: "precipitationSource"
                Layout.fillWidth: true
                text: "Source: Open-Meteo best match · Modeled forecast" + (root.result && root.result.save_failed ? "\nThese details could not be saved for offline use." : "")
                color: Tokens.secondary
                font.pixelSize: 12
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
