import QtQuick
import QtQuick.Controls

Button {
    id: root
    property bool selected: false
    property bool primary: false
    property string iconName: ""
    property string accessibleLabel: ""
    property real iconSize: text === "" ? 16 : 18
    implicitHeight: 40
    implicitWidth: Math.max(44, contentItem.implicitWidth + 28)
    Accessible.name: accessibleLabel !== "" ? accessibleLabel : text
    background: Rectangle {
        radius: 12
        color: root.primary ? "#176da0" : root.selected ? "#704fa6d4" : root.down ? "#80506a80" : "#303d5a70"
        border.color: root.activeFocus ? Tokens.accent : root.selected ? "#7bd6fc" : Tokens.border
        border.width: root.activeFocus ? 2 : 1
        opacity: root.enabled ? 1 : 0.45
    }
    contentItem: Item {
        implicitWidth: contentRow.implicitWidth
        implicitHeight: contentRow.implicitHeight
        opacity: root.enabled ? 1 : 0.5
        Row {
            id: contentRow
            anchors.centerIn: parent
            spacing: root.iconName !== "" && root.text !== "" ? 7 : 0
            UiIcon {
                anchors.verticalCenter: parent.verticalCenter
                visible: root.iconName !== ""
                iconName: root.iconName
                color: root.primary ? "#ffffff" : Tokens.foreground
                width: root.iconSize
                height: root.iconSize
            }
            PlainLabel {
                visible: root.text !== ""
                text: root.text
                horizontalAlignment: Text.AlignHCenter
                verticalAlignment: Text.AlignVCenter
            }
        }
    }
}
