pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast
import "Outdoor.js" as Outdoor

Popup {
    id: root
    objectName: "outdoorPlanner"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(760, parent ? parent.height - 40 : 760)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var plan: null
    property string state: "loading"
    property string error: ""
    property bool available: false
    property string units: "F"
    property string windUnits: "auto"
    property string place: ""
    property string freshness: ""
    property int alertCount: 0
    property var preferences: null
    property bool editing: false
    property bool dirty: false
    signal findRequested(var preferences)
    onPlanChanged: {
        if (plan) {
            preferences = Outdoor.preferences(plan.preferences);
            dirty = false;
        }
    }
    function updatePreference(key, value) {
        let next = Object.assign({}, preferences);
        next[key] = value;
        if (key === "min_temperature_c")
            next.max_temperature_c = Math.max(next.max_temperature_c, value);
        if (key === "max_temperature_c")
            next.min_temperature_c = Math.min(next.min_temperature_c, value);
        if (key === "max_wind_m_s")
            next.max_gust_m_s = Math.max(next.max_gust_m_s, value);
        if (key === "max_gust_m_s")
            next.max_wind_m_s = Math.min(next.max_wind_m_s, value);
        preferences = next;
        dirty = true;
    }
    function label(key, n) {
        if (key === "hours")
            return n + (n === 1 ? " hour" : " hours");
        if (key.indexOf("temperature") >= 0)
            return Forecast.temp(n, units);
        if (key === "max_probability")
            return Forecast.percent(n);
        if (key === "max_hourly_precipitation_mm")
            return units === "F" ? (n / 25.4).toFixed(3) + " in/hour" : n + " mm/hour";
        return Forecast.wind(n, units, windUnits);
    }
    function choices(row) {
        if (!preferences)
            return [];
        const current = preferences[row.key];
        let values = row.values.slice();
        if (values.indexOf(current) < 0)
            values.push(current);
        return values.sort((a, b) => a - b).map(n => ({
                    value: n,
                    label: label(row.key, n)
                }));
    }
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
        objectName: "outdoorPlannerScroll"
        clip: true
        rightPadding: 18
        contentWidth: availableWidth
        contentHeight: body.implicitHeight
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.vertical.active: true
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        ColumnLayout {
            id: body
            width: scroll.availableWidth
            spacing: 16
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Find a time to go outside"
                    font.pixelSize: 26
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeOutdoorPlanner"
                    iconName: "close"
                    accessibleLabel: "Close outdoor planner"
                    onClicked: root.close()
                }
            }
            PlainLabel {
                objectName: "outdoorPlannerPlace"
                Layout.fillWidth: true
                text: root.place + " · Next 48 hours\n" + root.freshness
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                visible: root.alertCount > 0
                Layout.fillWidth: true
                text: root.alertCount + (root.alertCount === 1 ? " official alert is" : " official alerts are") + " active here. Review the alerts on the forecast screen before planning."
                color: "#ffd99b"
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            RowLayout {
                Layout.fillWidth: true
                ActionButton {
                    objectName: "outdoorAdjustPreferences"
                    text: root.editing ? "Hide preferences" : "Adjust preferences"
                    enabled: root.preferences !== null && root.state !== "loading"
                    onClicked: root.editing = !root.editing
                }
                Item {
                    Layout.fillWidth: true
                }
                ActionButton {
                    objectName: "outdoorFindTimes"
                    text: root.state === "loading" ? "Finding times…" : "Find times"
                    primary: true
                    enabled: root.available && root.state !== "loading"
                    onClicked: root.findRequested(root.preferences)
                }
            }
            PlainLabel {
                visible: root.preferences !== null && !root.editing
                Layout.fillWidth: true
                text: !root.preferences ? "" : root.label("hours", root.preferences.hours) + " · " + root.label("min_temperature_c", root.preferences.min_temperature_c) + " to " + root.label("max_temperature_c", root.preferences.max_temperature_c) + " · " + (root.preferences.daylight_only ? "Daylight only" : "Day or night") + "\nHourly precipitation: up to " + root.label("max_probability", root.preferences.max_probability) + " and " + root.label("max_hourly_precipitation_mm", root.preferences.max_hourly_precipitation_mm) + "\nWind up to " + root.label("max_wind_m_s", root.preferences.max_wind_m_s) + " · Gusts up to " + root.label("max_gust_m_s", root.preferences.max_gust_m_s)
                font.pixelSize: 13
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            GridLayout {
                visible: root.editing && root.preferences !== null
                enabled: root.state !== "loading"
                Layout.fillWidth: true
                columns: 2
                columnSpacing: 16
                rowSpacing: 14
                Repeater {
                    model: [
                        {
                            key: "hours",
                            title: "Time outside",
                            values: [1, 2, 3, 4]
                        },
                        {
                            key: "max_probability",
                            title: "Maximum hourly rain / snow chance",
                            values: [0, .1, .2, .4, .6, .8, 1]
                        },
                        {
                            key: "min_temperature_c",
                            title: "Minimum temperature",
                            values: [-20, -10, 0, 5, 10, 15, 20, 25, 30, 40]
                        },
                        {
                            key: "max_temperature_c",
                            title: "Maximum temperature",
                            values: [0, 5, 10, 15, 20, 25, 27, 30, 35, 40, 50]
                        },
                        {
                            key: "max_wind_m_s",
                            title: "Maximum wind",
                            values: [0, 2, 4, 6, 8, 10, 15, 20]
                        },
                        {
                            key: "max_gust_m_s",
                            title: "Maximum gusts",
                            values: [0, 4, 6, 8, 10, 15, 20, 30]
                        },
                        {
                            key: "max_hourly_precipitation_mm",
                            title: "Maximum hourly precipitation",
                            values: [0, .1, .5, 1, 2, 5, 10]
                        }
                    ]
                    delegate: ColumnLayout {
                        id: preferenceRow
                        required property var modelData
                        Layout.fillWidth: true
                        Layout.preferredWidth: 1
                        Layout.minimumWidth: 180
                        PlainLabel {
                            Layout.fillWidth: true
                            text: preferenceRow.modelData.title
                            font.pixelSize: 13
                            color: Tokens.secondary
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        SettingsComboBox {
                            objectName: "outdoor_" + preferenceRow.modelData.key
                            Layout.fillWidth: true
                            Accessible.name: preferenceRow.modelData.title
                            model: root.choices(preferenceRow.modelData)
                            textRole: "label"
                            currentIndex: root.preferences ? model.findIndex(row => row.value === root.preferences[preferenceRow.modelData.key]) : -1
                            onActivated: index => root.updatePreference(preferenceRow.modelData.key, model[index].value)
                        }
                    }
                }
                ToggleControl {
                    Layout.fillWidth: true
                    Layout.preferredWidth: 1
                    Layout.minimumWidth: 180
                    testName: "outdoorDaylight"
                    title: "Daylight only"
                    caption: "Use sunrise and sunset"
                    checked: root.preferences ? root.preferences.daylight_only : true
                    optimistic: false
                    onToggled: value => root.updatePreference("daylight_only", value)
                }
            }
            PlainLabel {
                visible: root.editing
                Layout.fillWidth: true
                text: "Find times saves these preferences for every place. Hourly precipitation is liquid equivalent, including rain and melted snow."
                font.pixelSize: 12
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                objectName: "outdoorPlannerStatus"
                Layout.fillWidth: true
                visible: text !== ""
                text: root.state === "loading" ? "Checking the loaded hourly forecast…" : root.dirty ? "Preferences changed. Find times to update the suggestions." : root.error || (!root.plan ? "" : ["fresh", "stale"].indexOf(root.plan.freshness) < 0 ? "This forecast is too old or unavailable for planning. Refresh the forecast and try again." : root.plan.windows.length === 0 ? "There are not enough consecutive forecast hours for this duration." : root.plan.windows.some(row => row.fits) ? "" : "No complete match found. These are the closest available windows.")
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            Repeater {
                model: !root.dirty && root.plan ? root.plan.windows : []
                delegate: Rectangle {
                    id: resultCard
                    required property var modelData
                    required property int index
                    objectName: "outdoorWindow_" + index
                    Layout.fillWidth: true
                    implicitHeight: resultBody.implicitHeight + 28
                    color: "#303d5a70"
                    radius: 12
                    border.color: Tokens.border
                    ColumnLayout {
                        id: resultBody
                        anchors.left: parent.left
                        anchors.right: parent.right
                        anchors.top: parent.top
                        anchors.margins: 14
                        spacing: 8
                        PlainLabel {
                            Layout.fillWidth: true
                            text: resultCard.modelData.range_label
                            font.pixelSize: 18
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        PlainLabel {
                            objectName: "outdoorExplanation_" + resultCard.index
                            Layout.fillWidth: true
                            text: Outdoor.explanation(resultCard.modelData)
                            color: resultCard.modelData.fits ? Tokens.accent : "#ffd99b"
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            text: Outdoor.metrics(resultCard.modelData, root.units, root.windUnits)
                            font.pixelSize: 13
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                    }
                }
            }
            PlainLabel {
                objectName: "outdoorSaveStatus"
                visible: root.plan !== null && ["unavailable", "unconfirmed"].indexOf(root.plan.save_status) >= 0
                Layout.fillWidth: true
                text: root.plan && root.plan.save_status === "unconfirmed" ? "These preferences apply here, but saving could not be confirmed." : "Saved preferences could not be read. Default preferences are shown."
                color: "#ffd99b"
                font.pixelSize: 13
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                Layout.fillWidth: true
                text: "Hourly forecast by Open-Meteo · Times are local to this place. Windows are ranked by your preferences, then earliest first; incomplete data ranks last. UV, air quality, ice and official warnings are not included in the ranking."
                color: Tokens.secondary
                font.pixelSize: 12
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
