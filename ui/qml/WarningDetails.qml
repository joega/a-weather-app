pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

Popup {
    id: root
    objectName: "warningDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(760, parent ? parent.height - 40 : 760)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var warning: null
    property string state: "loading"
    property string error: ""
    property double now: Date.now()
    readonly property bool expired: warning !== null && now > Date.parse(warning.expires)
    Timer {
        interval: 60000
        repeat: true
        running: root.visible && root.warning !== null
        onTriggered: root.now = Date.now()
    }
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        if (!flick || !flick.contentItem || item === closeButton)
            return;
        let ancestor = item;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const point = item.mapToItem(flick.contentItem, 0, 0);
        // Large original text blocks can exceed the viewport. Bring their
        // start into view instead of jumping to the end of the entire notice.
        flick.contentY = Math.max(0, Math.min(point.y - 12, Math.max(0, flick.contentHeight - flick.height)));
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
    contentItem: ColumnLayout {
        spacing: 14
        RowLayout {
            Layout.fillWidth: true
            PlainLabel {
                Layout.fillWidth: true
                text: root.warning && root.warning.kind === "canceled" ? I18n.t("Warning cancellation") : I18n.t("Official warning")
                font.pixelSize: Tokens.fontSize(25)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                id: closeButton
                objectName: "closeWarningDetails"
                iconName: "close"
                accessibleLabel: I18n.t("Close warning details")
                onClicked: root.close()
            }
        }
        PlainLabel {
            objectName: "warningDetailStatus"
            Layout.fillWidth: true
            visible: root.state !== "ready"
            text: root.state === "loading" ? I18n.t("Loading original notice…") : I18n.t(root.error)
            font.pixelSize: Tokens.fontSize(15)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            color: Tokens.secondary
        }
        ScrollView {
            id: scroll
            objectName: "warningDetailsScroll"
            Layout.fillWidth: true
            Layout.fillHeight: true
            visible: root.state === "ready"
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
                PlainLabel {
                    objectName: "warningDetailPlace"
                    Layout.fillWidth: true
                    text: root.warning ? root.warning.place : ""
                    font.pixelSize: Tokens.fontSize(17)
                    color: Tokens.accent
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "warningDetailEvent"
                    Layout.fillWidth: true
                    text: root.warning ? root.warning.event || I18n.t("Official weather notice") : ""
                    font.pixelSize: Tokens.fontSize(23)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: root.warning ? I18n.t("%1\nIssued %2\n%3 severity · %4 · %5").arg((root.warning.issuer || "National Weather Service")).arg((I18n.date(root.warning.sent, root.warning.timezone, "long") || root.warning.sent_label)).arg(I18n.t(root.warning.severity)).arg(I18n.t(root.warning.urgency)).arg(I18n.t(root.warning.certainty)) : ""
                    font.pixelSize: Tokens.fontSize(14)
                    color: Tokens.secondary
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "warningDetailValidity"
                    Layout.fillWidth: true
                    text: !root.warning ? "" : root.warning.kind === "canceled" ? I18n.t("This notice cancels an earlier warning.") : I18n.t("%1Valid from %2\nUntil %3 · %4").arg((root.expired ? I18n.t("The stated validity period has ended.\n") : "")).arg((I18n.date(root.warning.effective, root.warning.timezone, "long") || root.warning.effective_label)).arg((I18n.date(root.warning.expires, root.warning.timezone, "long") || root.warning.expires_label)).arg(root.warning.timezone)
                    font.pixelSize: Tokens.fontSize(14)
                    color: Tokens.accent
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: I18n.t("Original notice from this session. Its status may have changed; check the current alert feed for later updates.")
                    font.pixelSize: Tokens.fontSize(13)
                    color: Tokens.secondary
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                Repeater {
                    model: root.warning ? [
                        {
                            name: I18n.t("Instructions"),
                            field: "instruction"
                        },
                        {
                            name: I18n.t("Headline"),
                            field: "headline"
                        },
                        {
                            name: I18n.t("Description"),
                            field: "description"
                        },
                        {
                            name: I18n.t("Affected area"),
                            field: "area"
                        }
                    ] : []
                    ColumnLayout {
                        id: section
                        required property var modelData
                        Layout.fillWidth: true
                        visible: root.warning !== null && root.warning[modelData.field] !== ""
                        spacing: 8
                        PlainLabel {
                            text: section.modelData.name
                            font.pixelSize: Tokens.fontSize(17)
                            font.weight: Font.DemiBold
                        }
                        TextArea {
                            objectName: "warningSource_" + section.modelData.field
                            Layout.fillWidth: true
                            text: root.warning ? root.warning[section.modelData.field] : ""
                            textFormat: TextEdit.PlainText
                            readOnly: true
                            selectByMouse: true
                            wrapMode: TextEdit.Wrap
                            font.family: I18n.fontFamily
                            font.pixelSize: Tokens.fontSize(16)
                            color: Tokens.foreground
                            selectedTextColor: Tokens.foreground
                            selectionColor: "#506a80"
                            padding: 0
                            Accessible.name: section.modelData.name
                            background: Rectangle {
                                color: "transparent"
                                border.color: parent.activeFocus ? Tokens.accent : "transparent"
                                radius: 4
                            }
                        }
                    }
                }
            }
        }
    }
}
