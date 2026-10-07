import QtQuick
import QtQuick.Layouts
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    objectName: "updatePanel"
    property var status: Forecast.updateStatus(null)
    property bool compactUpdatedNotice: false
    property string dismissedUpdateKey: ""
    readonly property string updateKey: status.installed + ":" + status.checked_at
    readonly property bool compactSuccess: compactUpdatedNotice && status.state === "updated"
    readonly property bool successDismissed: compactSuccess && dismissedUpdateKey === updateKey
    readonly property bool updating: ["downloading", "verifying", "restarting"].indexOf(status.state) >= 0
    readonly property bool canUpdate: status.available !== "" && ["available", "failed", "rolled_back"].indexOf(status.state) >= 0
    readonly property bool canCheck: !updating && ["checking", "development", "unsupported"].indexOf(status.state) < 0
    signal checkRequested
    signal installRequested
    implicitHeight: compactSuccess ? successRow.implicitHeight + 24 : body.implicitHeight + 32
    RowLayout {
        id: successRow
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.margins: 12
        visible: root.compactSuccess
        spacing: 12
        PlainLabel {
            objectName: "updatedVersionNotice"
            Layout.fillWidth: true
            text: root.status.installed !== "" ? "Updated to " + root.status.installed + "." : "Updated successfully."
            font.pixelSize: 16
        }
        ActionButton {
            objectName: "dismissUpdateNotice"
            text: "Close"
            onClicked: root.dismissedUpdateKey = root.updateKey
        }
    }
    ColumnLayout {
        id: body
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.margins: 16
        spacing: 10
        visible: !root.compactSuccess
        PlainLabel {
            objectName: "installedAppVersion"
            Layout.fillWidth: true
            text: root.status.installed !== "" ? "A Weather App · " + root.status.installed : "App updates"
            font.pixelSize: 19
            font.weight: Font.DemiBold
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.status.available !== ""
            text: "Version " + root.status.available + " available"
            font.pixelSize: 16
            color: Tokens.accent
        }
        PlainLabel {
            objectName: "updateMessage"
            Layout.fillWidth: true
            text: root.status.message || (root.status.state === "current" ? "You’re up to date. Updates are checked daily." : "Updates are checked daily.")
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: 14
            color: Tokens.secondary
        }
        RowLayout {
            Layout.fillWidth: true
            ActionButton {
                objectName: "installUpdate"
                visible: root.canUpdate || root.updating
                enabled: root.canUpdate && !root.updating
                text: root.updating ? "Updating…" : "Update and restart"
                onClicked: root.installRequested()
            }
            ActionButton {
                objectName: "checkUpdates"
                enabled: root.canCheck
                text: root.status.state === "checking" ? "Checking…" : "Check for updates"
                onClicked: root.checkRequested()
            }
            Item {
                Layout.fillWidth: true
            }
        }
    }
}
