import QtQuick
import QtQuick.Controls

Item {
    id: root
    property string title: ""
    property string caption: ""
    property bool checked: false
    property bool locked: false
    // Persistence-sensitive controls can wait for the bridge's saved value.
    property bool optimistic: true
    property string testName: "toggle"
    function focusControl() {
        toggle.forceActiveFocus();
    }
    signal toggled(bool value)
    implicitHeight: 68
    Column {
        y: 10
        width: parent.width - 78
        spacing: 4
        PlainLabel {
            text: root.title
            font.pixelSize: 18
        }
        PlainLabel {
            text: root.caption
            color: Tokens.secondary
            font.pixelSize: 13
            width: parent.width
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
    }
    Switch {
        id: toggle
        objectName: root.testName
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        checked: root.checked
        enabled: !root.locked
        Accessible.name: root.title
        onClicked: {
            root.toggled(checked);
            if (!root.optimistic)
                checked = Qt.binding(() => root.checked);
        }
        indicator: Rectangle {
            implicitWidth: 54
            implicitHeight: 28
            radius: 14
            color: toggle.checked ? "#49aeee" : "#49657b"
            border.color: toggle.activeFocus ? Tokens.accent : Tokens.border
            border.width: toggle.activeFocus ? 2 : 1
            Rectangle {
                x: toggle.checked ? 28 : 4
                y: 4
                width: 20
                height: 20
                radius: 10
                color: Tokens.foreground
            }
        }
    }
    Rectangle {
        anchors.bottom: parent.bottom
        width: parent.width
        height: 1
        color: Tokens.border
    }
}
