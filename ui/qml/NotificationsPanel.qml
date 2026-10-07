pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

ColumnLayout {
    id: root
    spacing: 14
    property var notifications: Forecast.notifications()
    property string timezone: "UTC"
    property bool busy: false
    property bool serviceAvailable: true
    property string actionError: ""
    readonly property var settings: notifications.settings
    readonly property bool locked: busy || !serviceAvailable
    readonly property string statusText: !serviceAvailable ? "Watching unavailable · weather service disconnected" : ({
            off: "Watching is off",
            waiting: "Waiting for a fresh forecast",
            watching: "Watching hourly precipitation outlooks",
            quiet: "Quiet hours · notifications paused",
            paused: "Watching is paused",
            unavailable: "Watching unavailable · check the forecast directly"
        })[notifications.state]
    readonly property var hours: Array.from({
        length: 24
    }, (_, hour) => (hour % 12 || 12) + (hour < 12 ? " AM" : " PM"))
    signal patch(var values)
    signal pauseRequested
    signal resumeRequested
    signal quitRequested
    PlainLabel {
        text: "Notifications"
        font.pixelSize: 23
        font.weight: Font.DemiBold
    }
    PlainLabel {
        objectName: "notificationPolicy"
        Layout.fillWidth: true
        text: "Quiet precipitation outlooks for the current hour and next three hours. Uses fresh hourly probabilities, not minute-by-minute onset predictions. Desktop effects can stay off."
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 14
        color: Tokens.secondary
    }
    PlainLabel {
        objectName: "notificationLifecycle"
        Layout.fillWidth: true
        text: "Watching keeps this app running when you close its window. Reopen it from the bar to stop watching or quit. Saved opt-in resumes when you open the app again; nothing starts at login."
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 14
        color: Tokens.secondary
    }
    ToggleControl {
        Layout.fillWidth: true
        testName: "notificationEnabled"
        title: "Precipitation watching"
        caption: root.settings.enabled ? "Keeps watching when the window is hidden." : "Off until you choose to enable it."
        checked: root.settings.enabled
        locked: root.locked || (!root.settings.enabled && !root.notifications.supported)
        optimistic: false
        onToggled: value => root.patch({
                enabled: value
            })
    }
    PlainLabel {
        objectName: "notificationStatus"
        Layout.fillWidth: true
        text: root.statusText
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 15
        color: Tokens.accent
    }
    PlainLabel {
        objectName: "notificationActionError"
        visible: root.actionError !== ""
        Layout.fillWidth: true
        text: root.actionError
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 13
        color: Tokens.accent
    }
    PlainLabel {
        objectName: "notificationUnsupported"
        visible: !root.notifications.supported
        Layout.fillWidth: true
        text: "Desktop delivery is unavailable. Install the optional libnotify package, then reopen the app. The forecast remains available."
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 13
        color: Tokens.secondary
    }
    PlainLabel {
        objectName: "notificationDeliveryFailure"
        visible: root.notifications.delivery === "failed"
        Layout.fillWidth: true
        text: "The last delivery could not be confirmed. It will not be retried for the same forecast event, to avoid duplicates."
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 13
        color: Tokens.secondary
    }
    PlainLabel {
        text: "Notify when hourly chance reaches"
        font.pixelSize: 15
    }
    ChoiceControl {
        Layout.fillWidth: true
        testName: "notificationProbability"
        choices: [
            {
                label: "50% or higher",
                value: 50
            },
            {
                label: "70% or higher",
                value: 70
            }
        ]
        value: root.settings.probability
        enabled: !root.locked
        onChosen: value => root.patch({
                probability: value
            })
    }
    ToggleControl {
        Layout.fillWidth: true
        testName: "notificationQuiet"
        title: "Quiet hours"
        caption: "Uses the forecast location’s local time."
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
        ColumnLayout {
            Layout.fillWidth: true
            spacing: 6
            PlainLabel {
                text: "From"
                font.pixelSize: 14
                color: Tokens.secondary
            }
            ComboBox {
                id: quietStart
                objectName: "notificationQuietStart"
                Layout.fillWidth: true
                model: root.hours
                currentIndex: root.settings.quiet_start
                enabled: !root.locked && root.settings.quiet_enabled
                Accessible.name: "Quiet hours start"
                onActivated: index => {
                    if (index !== root.settings.quiet_end)
                        root.patch({
                            quiet_start: index
                        });
                    currentIndex = Qt.binding(() => root.settings.quiet_start);
                }
                contentItem: PlainLabel {
                    text: quietStart.displayText
                    verticalAlignment: Text.AlignVCenter
                    leftPadding: 12
                }
                background: Rectangle {
                    implicitHeight: 42
                    radius: 10
                    color: "#30435e72"
                    border.color: quietStart.activeFocus ? Tokens.accent : Tokens.border
                }
                delegate: ItemDelegate {
                    id: unitOption
                    required property int index
                    required property string modelData
                    width: quietStart.width
                    enabled: index !== root.settings.quiet_end
                    contentItem: PlainLabel {
                        text: unitOption.modelData
                    }
                }
            }
        }
        ColumnLayout {
            Layout.fillWidth: true
            spacing: 6
            PlainLabel {
                text: "Until"
                font.pixelSize: 14
                color: Tokens.secondary
            }
            ComboBox {
                id: quietEnd
                objectName: "notificationQuietEnd"
                Layout.fillWidth: true
                model: root.hours
                currentIndex: root.settings.quiet_end
                enabled: !root.locked && root.settings.quiet_enabled
                Accessible.name: "Quiet hours end"
                onActivated: index => {
                    if (index !== root.settings.quiet_start)
                        root.patch({
                            quiet_end: index
                        });
                    currentIndex = Qt.binding(() => root.settings.quiet_end);
                }
                contentItem: PlainLabel {
                    text: quietEnd.displayText
                    verticalAlignment: Text.AlignVCenter
                    leftPadding: 12
                }
                background: Rectangle {
                    implicitHeight: 42
                    radius: 10
                    color: "#30435e72"
                    border.color: quietEnd.activeFocus ? Tokens.accent : Tokens.border
                }
                delegate: ItemDelegate {
                    id: leadOption
                    required property int index
                    required property string modelData
                    width: quietEnd.width
                    enabled: index !== root.settings.quiet_start
                    contentItem: PlainLabel {
                        text: leadOption.modelData
                    }
                }
            }
        }
    }
    PlainLabel {
        objectName: "notificationTimezone"
        Layout.fillWidth: true
        text: "Local time · " + root.timezone
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 13
        color: Tokens.secondary
    }
    RowLayout {
        Layout.fillWidth: true
        spacing: 10
        ActionButton {
            objectName: "notificationPause"
            Layout.fillWidth: true
            text: root.notifications.state === "paused" ? "Resume watching" : "Pause for 1 hour"
            enabled: !root.locked && root.settings.enabled
            onClicked: {
                if (root.notifications.state === "paused")
                    root.resumeRequested();
                else
                    root.pauseRequested();
            }
        }
        ActionButton {
            objectName: "notificationStop"
            Layout.fillWidth: true
            text: "Stop watching"
            enabled: !root.locked && root.settings.enabled
            onClicked: root.patch({
                enabled: false
            })
        }
    }
    PlainLabel {
        Layout.fillWidth: true
        text: "Stopping watching keeps the forecast open. Quit app stops all background watching and desktop effects for this session."
        wrapMode: Text.Wrap
        elide: Text.ElideNone
        font.pixelSize: 13
        color: Tokens.secondary
    }
    ActionButton {
        objectName: "quitWeatherApp"
        text: "Quit app"
        onClicked: root.quitRequested()
    }
}
