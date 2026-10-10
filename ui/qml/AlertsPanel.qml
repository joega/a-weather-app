pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls

GlassPanel {
    id: root
    objectName: "alertsPanel"
    property var alerts: []
    property string source: ""
    property string expandedId: ""
    property Item expandedHeading: null
    readonly property int columnCount: alerts.length > 1 && body.width >= 650 * Tokens.textScale ? 2 : 1
    implicitHeight: body.implicitHeight + 32
    function key(alert) {
        return alert.id || alert.event + "|" + alert.expires;
    }
    function accent(severity) {
        return severity === "Extreme" || severity === "Severe" ? "#ffb5a7" : severity === "Moderate" ? Tokens.gold : Tokens.accent;
    }
    function reveal(item, showDetails = false) {
        // The dashboard owns scrolling, including keyboard focus in expanded details.
        let ancestor = root.parent;
        while (ancestor) {
            const flickable = ancestor as Flickable;
            if (flickable) {
                const point = item.mapToItem(flickable.contentItem, 0, 0);
                let position = flickable.contentY;
                if (point.y < position)
                    position = point.y;
                else if (point.y + item.height > position + flickable.height)
                    position = showDetails ? point.y - 16 : point.y + item.height - flickable.height;
                flickable.contentY = Math.max(flickable.originY, Math.min(flickable.originY + Math.max(0, flickable.contentHeight - flickable.height), position));
                return;
            }
            ancestor = ancestor.parent;
        }
    }
    Timer {
        id: revealExpanded
        interval: 16
        onTriggered: if (root.expandedHeading && root.expandedHeading.visible)
            root.reveal(root.expandedHeading, true)
    }
    Column {
        id: body
        x: 16
        y: 16
        width: Math.max(0, root.width - 32)
        spacing: 9
        PlainLabel {
            text: "Weather alerts"
            font.pixelSize: Tokens.fontSize(17)
            font.weight: Font.DemiBold
        }
        Grid {
            id: summaryGrid
            objectName: "alertsGrid"
            width: body.width
            columns: root.columnCount
            spacing: 9
            Repeater {
                model: root.alerts
                delegate: Button {
                    id: alertButton
                    required property var modelData
                    required property int index
                    readonly property bool expanded: root.expandedId === root.key(modelData)
                    objectName: "alertDetails_" + index
                    width: Math.max(0, (summaryGrid.width - summaryGrid.spacing * (root.columnCount - 1)) / root.columnCount)
                    leftPadding: 11
                    rightPadding: 9
                    topPadding: 8
                    bottomPadding: 8
                    Accessible.name: modelData.event + ", " + modelData.severity + ", expires " + modelData.expires_label + (expanded ? ". Hide details" : ". Show details")
                    Accessible.description: expanded ? "Full alert details appear below the alert list." : "Select to read the full alert details below the alert list."
                    onActiveFocusChanged: if (activeFocus)
                        root.reveal(alertButton)
                    onClicked: {
                        root.expandedId = expanded ? "" : root.key(modelData);
                        if (root.expandedId !== "")
                            revealExpanded.restart();
                        else {
                            revealExpanded.stop();
                            root.reveal(alertButton);
                        }
                    }
                    background: Rectangle {
                        radius: 8
                        color: alertButton.hovered ? "#304b667e" : "#18283b50"
                        border.color: alertButton.activeFocus ? Tokens.accent : "transparent"
                        Rectangle {
                            width: 3
                            height: parent.height - 12
                            y: 6
                            radius: 1
                            color: root.accent(alertButton.modelData.severity)
                        }
                    }
                    contentItem: Column {
                        spacing: 4
                        PlainLabel {
                            objectName: "alertTitle_" + alertButton.index
                            width: parent.width
                            text: alertButton.modelData.event + (alertButton.expanded ? "  −" : "  +")
                            font.weight: Font.DemiBold
                            font.pixelSize: Tokens.fontSize(16)
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                        PlainLabel {
                            width: parent.width
                            text: alertButton.modelData.severity + " · Until " + (alertButton.modelData.expires_label || "time unavailable")
                            color: root.accent(alertButton.modelData.severity)
                            font.pixelSize: Tokens.fontSize(12)
                            wrapMode: Text.Wrap
                            elide: Text.ElideNone
                        }
                    }
                }
            }
        }
        Repeater {
            model: root.alerts
            delegate: Column {
                id: entry
                required property var modelData
                required property int index
                visible: root.expandedId === root.key(modelData)
                onVisibleChanged: {
                    if (visible)
                        root.expandedHeading = detailHeading;
                    else if (root.expandedHeading === detailHeading)
                        root.expandedHeading = null;
                }
                width: body.width
                spacing: 6
                PlainLabel {
                    id: detailHeading
                    objectName: "alertDetailHeading_" + entry.index
                    width: parent.width
                    text: entry.modelData.headline || entry.modelData.event
                    font.weight: Font.DemiBold
                    font.pixelSize: Tokens.fontSize(15)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "alertShortened_" + entry.index
                    visible: entry.modelData.text_truncated === true
                    width: parent.width
                    text: root.source === "National Weather Service" ? "This alert text has been shortened. Read the complete warning and instructions from the National Weather Service." : "This alert text has been shortened. Check the official alert source for complete details."
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                    font.pixelSize: Tokens.fontSize(13)
                    color: Tokens.gold
                }
                PlainLabel {
                    visible: entry.modelData.instruction !== ""
                    width: parent.width
                    text: entry.modelData.instruction
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                    font.pixelSize: Tokens.fontSize(14)
                }
                PlainLabel {
                    objectName: "alertDescription_" + entry.index
                    width: parent.width
                    text: entry.modelData.description + (root.source ? "\nSource: " + root.source : "")
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                    color: Tokens.secondary
                    font.pixelSize: Tokens.fontSize(14)
                }
                ActionButton {
                    id: officialSource
                    objectName: "alertOfficialSource_" + entry.index
                    visible: root.source === "National Weather Service"
                    text: "Open official NWS alerts"
                    onActiveFocusChanged: if (activeFocus)
                        root.reveal(officialSource)
                    onClicked: Qt.openUrlExternally("https://www.weather.gov/alerts")
                }
            }
        }
    }
}
