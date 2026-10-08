pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls

ComboBox {
    id: root
    property var enabledOptions: []
    property real popupWidth: width
    font.pixelSize: 17
    implicitHeight: 42
    implicitWidth: 240
    leftPadding: 12
    rightPadding: 36
    topPadding: 10
    bottomPadding: 10
    contentItem: PlainLabel {
        font.pixelSize: root.font.pixelSize
        text: root.displayText
        verticalAlignment: Text.AlignVCenter
        opacity: root.enabled ? 1 : 0.5
    }
    indicator: UiIcon {
        x: root.width - width - 12
        y: (root.height - height) / 2
        iconName: "chevron-down"
        opacity: root.enabled ? 1 : 0.5
    }
    background: Rectangle {
        radius: 10
        color: root.down ? "#506a80" : "#30435e72"
        border.color: root.activeFocus ? Tokens.accent : Tokens.border
        opacity: root.enabled ? 1 : 0.5
    }
    delegate: ItemDelegate {
        id: option
        required property int index
        required property var modelData
        width: root.popup.width - root.popup.leftPadding - root.popup.rightPadding
        height: 40
        enabled: root.enabledOptions.length === 0 || root.enabledOptions[option.index]
        highlighted: root.highlightedIndex === option.index
        contentItem: PlainLabel {
            font.pixelSize: root.font.pixelSize
            text: root.textRole ? option.modelData[root.textRole] : option.modelData
            verticalAlignment: Text.AlignVCenter
            color: root.currentIndex === option.index ? Tokens.accent : Tokens.foreground
            opacity: option.enabled ? 1 : 0.45
        }
        background: Rectangle {
            radius: 6
            color: option.highlighted || option.hovered ? "#506a80" : "transparent"
        }
    }
    popup: Popup {
        y: root.height + 6
        width: root.popupWidth
        padding: 6
        implicitHeight: Math.min(contentItem.implicitHeight + topPadding + bottomPadding, 300)
        background: Rectangle {
            radius: 10
            color: "#2c455a"
            border.color: Tokens.border
        }
        contentItem: ListView {
            clip: true
            implicitHeight: contentHeight
            model: root.popup.visible ? root.delegateModel : null
            currentIndex: root.highlightedIndex
            ScrollIndicator.vertical: ScrollIndicator {}
        }
    }
}
