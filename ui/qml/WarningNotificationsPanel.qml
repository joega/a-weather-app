pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

ColumnLayout {
    id: root
    spacing: 12
    property var notifications: Forecast.warningNotifications()
    property string primaryName: "Primary place"
    property string timezone: "UTC"
    property bool busy: false
    property bool serviceAvailable: true
    readonly property var settings: notifications.settings
    readonly property bool locked: busy || !serviceAvailable
    property double now: Date.now()
    readonly property bool paused: notifications.paused_until !== null && notifications.paused_until * 1000 > now
    readonly property var hours: Array.from({
        length: 24
    }, (_, hour) => (hour % 12 || 12) + (hour < 12 ? " AM" : " PM"))
    signal patch(var values)
    signal pauseRequested
    signal resumeRequested
    signal detailRequested(var reference)
    function focusNotice(reference) {
        if (!reference)
            return false;
        const index = notifications.recent.findIndex(row => row.location === reference.location && row.key === reference.key);
        const item = index >= 0 ? recentNotices.itemAt(index) : null;
        if (!item || !item.enabled)
            return false;
        item.forceActiveFocus();
        return true;
    }
    Timer {
        interval: 30000
        repeat: true
        running: root.visible && root.paused
        onTriggered: root.now = Date.now()
    }
    PlainLabel {
        text: "Official US warnings"
        font.pixelSize: Tokens.fontSize(20)
        font.weight: Font.DemiBold
    }
    PlainLabel {
        Layout.fillWidth: true
        text: "National Weather Service warnings for " + root.primaryName + ". Checks run roughly every 2–3 minutes when available. Delivery may be delayed; this is not a real-time emergency alert service."
        font.pixelSize: Tokens.fontSize(14)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    ToggleControl {
        Layout.fillWidth: true
        testName: "warningEnabled"
        title: "Official warning notifications"
        caption: root.settings.enabled ? "Continues checking while the window is hidden." : "Off until you enable it."
        checked: root.settings.enabled
        locked: root.locked || (!root.settings.enabled && !root.notifications.supported)
        optimistic: false
        onToggled: value => root.patch({
                enabled: value
            })
    }
    PlainLabel {
        objectName: "warningMonitoringStatus"
        Layout.fillWidth: true
        text: Forecast.warningStatus(root.notifications, root.serviceAvailable)
        font.pixelSize: Tokens.fontSize(15)
        color: Tokens.accent
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        Accessible.role: Accessible.StaticText
        Accessible.name: text
    }
    PlainLabel {
        Layout.fillWidth: true
        visible: root.settings.enabled && root.notifications.fetched_at !== null
        text: root.notifications.fetched_at ? "Latest warning data: " + root.notifications.fetched_at.replace("T", " ").replace("Z", " UTC") : ""
        font.pixelSize: Tokens.fontSize(13)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    PlainLabel {
        Layout.fillWidth: true
        visible: !root.notifications.supported
        text: "Official warning delivery requires the native desktop app and a supported warning feed."
        font.pixelSize: Tokens.fontSize(13)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    ColumnLayout {
        visible: root.settings.enabled
        Layout.fillWidth: true
        spacing: 12
        PlainLabel {
            text: "New warnings to deliver"
            font.pixelSize: Tokens.fontSize(15)
        }
        SettingsComboBox {
            objectName: "warningSeverity"
            Layout.fillWidth: true
            model: [
                {
                    label: "Severe and extreme",
                    value: "severe"
                },
                {
                    label: "Moderate and above",
                    value: "moderate"
                },
                {
                    label: "All severities, including unknown",
                    value: "all"
                }
            ]
            textRole: "label"
            valueRole: "value"
            currentIndex: model.findIndex(row => row.value === root.settings.minimum_severity)
            enabled: !root.locked
            Accessible.name: "New warning severity"
            onActivated: {
                root.patch({
                    minimum_severity: currentValue
                });
                currentIndex = Qt.binding(() => model.findIndex(row => row.value === root.settings.minimum_severity));
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Material updates and cancellations for previously attempted notices are included, even if their severity falls."
            font.pixelSize: Tokens.fontSize(13)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        ToggleControl {
            Layout.fillWidth: true
            testName: "warningQuiet"
            title: "Quiet hours for warnings"
            caption: "Uses the primary place’s local time."
            checked: root.settings.quiet_enabled
            locked: root.locked
            optimistic: false
            onToggled: value => root.patch({
                    quiet_enabled: value
                })
        }
        RowLayout {
            Layout.fillWidth: true
            spacing: 12
            SettingsComboBox {
                id: quietStart
                objectName: "warningQuietStart"
                Layout.fillWidth: true
                model: root.hours
                currentIndex: root.settings.quiet_start
                enabledOptions: root.hours.map((_, index) => index !== root.settings.quiet_end)
                enabled: !root.locked && root.settings.quiet_enabled
                Accessible.name: "Warning quiet hours start"
                onActivated: index => {
                    if (index !== root.settings.quiet_end)
                        root.patch({
                            quiet_start: index
                        });
                    currentIndex = Qt.binding(() => root.settings.quiet_start);
                }
            }
            PlainLabel {
                text: "to"
                font.pixelSize: Tokens.fontSize(14)
            }
            SettingsComboBox {
                id: quietEnd
                objectName: "warningQuietEnd"
                Layout.fillWidth: true
                model: root.hours
                currentIndex: root.settings.quiet_end
                enabledOptions: root.hours.map((_, index) => index !== root.settings.quiet_start)
                enabled: !root.locked && root.settings.quiet_enabled
                Accessible.name: "Warning quiet hours end"
                onActivated: index => {
                    if (index !== root.settings.quiet_start)
                        root.patch({
                            quiet_end: index
                        });
                    currentIndex = Qt.binding(() => root.settings.quiet_end);
                }
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Local time · " + root.timezone
            font.pixelSize: Tokens.fontSize(13)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        ToggleControl {
            Layout.fillWidth: true
            testName: "warningUrgent"
            title: "Allow urgent interruptions"
            caption: "Can bypass warning quiet hours."
            checked: root.settings.urgent_override
            locked: root.locked
            optimistic: false
            onToggled: value => root.patch({
                    urgent_override: value
                })
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Only imminent Severe or Extreme warnings reported as observed or likely qualify. Your desktop controls how urgent notices appear. A manual pause always stops delivery."
            font.pixelSize: Tokens.fontSize(13)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        RowLayout {
            Layout.fillWidth: true
            spacing: 10
            ActionButton {
                objectName: "warningPause"
                Layout.fillWidth: true
                text: root.paused ? "Resume delivery" : "Pause for 1 hour"
                enabled: !root.locked
                onClicked: root.paused ? root.resumeRequested() : root.pauseRequested()
            }
            ActionButton {
                objectName: "warningStop"
                Layout.fillWidth: true
                text: "Turn off warnings"
                enabled: !root.locked
                onClicked: root.patch({
                    enabled: false
                })
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.notifications.delivery === "uncertain" || root.notifications.delivery === "failed"
            text: "The last notice was not confirmed as delivered. It will not be retried automatically, to avoid duplicates."
            font.pixelSize: Tokens.fontSize(13)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.notifications.ready && !root.notifications.actions
            text: "This desktop does not offer notification buttons. Open recent notices below."
            font.pixelSize: Tokens.fontSize(13)
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
    }
    PlainLabel {
        visible: root.notifications.recent.length > 0
        text: "Recent notices"
        font.pixelSize: Tokens.fontSize(17)
        font.weight: Font.DemiBold
    }
    Repeater {
        id: recentNotices
        model: root.notifications.recent
        ItemDelegate {
            id: notice
            required property int index
            required property var modelData
            objectName: "recentWarning" + index
            Layout.fillWidth: true
            implicitHeight: noticeBody.implicitHeight + 20
            enabled: root.serviceAvailable
            Accessible.name: modelData.title + ", " + modelData.place + ", " + Forecast.warningDeliveryLabel(modelData.delivery)
            onClicked: root.detailRequested({
                location: modelData.location,
                key: modelData.key
            })
            contentItem: Column {
                id: noticeBody
                spacing: 5
                PlainLabel {
                    width: parent.width
                    text: notice.modelData.title
                    font.pixelSize: Tokens.fontSize(16)
                    wrapMode: Text.Wrap
                    maximumLineCount: 2
                }
                PlainLabel {
                    width: parent.width
                    text: notice.modelData.place + " · " + Forecast.warningDeliveryLabel(notice.modelData.delivery)
                    font.pixelSize: Tokens.fontSize(13)
                    color: Tokens.secondary
                    wrapMode: Text.Wrap
                    maximumLineCount: 2
                }
            }
            background: Rectangle {
                radius: 10
                color: notice.hovered ? "#40506a80" : "#203d5a70"
                border.color: notice.activeFocus ? Tokens.accent : Tokens.border
            }
        }
    }
    PlainLabel {
        visible: root.notifications.recent.length > 0
        Layout.fillWidth: true
        text: "Up to 16 recent notices remain available for 24 hours in this session. Desktop acceptance does not confirm that a notice was read."
        font.pixelSize: Tokens.fontSize(13)
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
}
