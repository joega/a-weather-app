pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls

GlassPanel {
    id: root
    objectName: "alertsPanel"
    property var alerts: []
    property string source: ""
    property string expandedId: ""
    implicitHeight: Math.min(290, body.implicitHeight + 32)
    function key(alert) {
        return alert.id || alert.event + "|" + alert.expires;
    }
    function accent(severity) {
        return severity === "Extreme" || severity === "Severe" ? "#ffb5a7" : severity === "Moderate" ? Tokens.gold : Tokens.accent;
    }
    Flickable {
        id: viewport
        objectName: "alertsViewport"
        anchors.fill: parent
        anchors.margins: 16
        clip: true
        contentWidth: width
        contentHeight: body.implicitHeight
        boundsBehavior: Flickable.StopAtBounds
        function scrollTo(value) {
            contentY = Math.max(0, Math.min(Math.max(0, contentHeight - height), value));
        }
        function reveal(item) {
            const point = item.mapToItem(contentItem, 0, 0);
            if (point.y < contentY)
                scrollTo(point.y);
            else if (point.y + item.height > contentY + height)
                scrollTo(point.y + item.height - height);
        }
        onContentHeightChanged: scrollTo(contentY)
        Keys.onPressed: event => {
            let position = contentY;
            if (event.key === Qt.Key_Down)
                position += 40;
            else if (event.key === Qt.Key_Up)
                position -= 40;
            else if (event.key === Qt.Key_PageDown)
                position += height * 0.8;
            else if (event.key === Qt.Key_PageUp)
                position -= height * 0.8;
            else if (event.key === Qt.Key_Home)
                position = 0;
            else if (event.key === Qt.Key_End)
                position = contentHeight - height;
            else
                return;
            scrollTo(position);
            event.accepted = true;
        }
        ScrollBar.vertical: ScrollBar {
            active: true
        }
        Column {
            id: body
            width: Math.max(0, viewport.width - 10)
            spacing: 9
            PlainLabel {
                text: "Weather alerts · " + root.alerts.length
                font.pixelSize: Tokens.fontSize(17)
                font.weight: Font.DemiBold
            }
            Repeater {
                model: root.alerts
                delegate: Column {
                    id: entry
                    required property var modelData
                    required property int index
                    readonly property bool expanded: root.expandedId === root.key(modelData)
                    width: body.width
                    spacing: 6
                    Button {
                        id: alertButton
                        objectName: "alertDetails_" + entry.index
                        width: parent.width
                        leftPadding: 11
                        rightPadding: 9
                        topPadding: 8
                        bottomPadding: 8
                        Accessible.name: entry.modelData.event + ", " + entry.modelData.severity + ", expires " + entry.modelData.expires_label + (entry.expanded ? ". Hide details" : ". Show details")
                        Accessible.description: "Use the arrow or Page Up and Page Down keys to read alert details."
                        onActiveFocusChanged: if (activeFocus)
                            viewport.reveal(alertButton)
                        onClicked: {
                            root.expandedId = entry.expanded ? "" : root.key(entry.modelData);
                            Qt.callLater(() => viewport.reveal(alertButton));
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
                                color: root.accent(entry.modelData.severity)
                            }
                        }
                        contentItem: Column {
                            spacing: 4
                            PlainLabel {
                                width: parent.width
                                text: entry.modelData.event + (entry.expanded ? "  −" : "  +")
                                font.weight: Font.DemiBold
                                font.pixelSize: Tokens.fontSize(16)
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                width: parent.width
                                text: entry.modelData.severity + " · Until " + (entry.modelData.expires_label || "time unavailable")
                                color: root.accent(entry.modelData.severity)
                                font.pixelSize: Tokens.fontSize(12)
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                        }
                    }
                    PlainLabel {
                        visible: entry.expanded
                        width: parent.width
                        text: entry.modelData.headline || entry.modelData.event
                        font.weight: Font.DemiBold
                        font.pixelSize: Tokens.fontSize(15)
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    PlainLabel {
                        objectName: "alertShortened_" + entry.index
                        visible: entry.expanded && entry.modelData.text_truncated === true
                        width: parent.width
                        text: root.source === "National Weather Service" ? "This alert text has been shortened. Read the complete warning and instructions from the National Weather Service." : "This alert text has been shortened. Check the official alert source for complete details."
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(13)
                        color: Tokens.gold
                    }
                    PlainLabel {
                        visible: entry.expanded && entry.modelData.instruction !== ""
                        width: parent.width
                        text: entry.modelData.instruction
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(14)
                    }
                    PlainLabel {
                        visible: entry.expanded
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
                        visible: entry.expanded && root.source === "National Weather Service"
                        text: "Open official NWS alerts"
                        onActiveFocusChanged: if (activeFocus)
                            viewport.reveal(officialSource)
                        onClicked: Qt.openUrlExternally("https://www.weather.gov/alerts")
                    }
                }
            }
        }
    }
}
