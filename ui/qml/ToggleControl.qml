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
    implicitHeight: Math.max(68, labels.implicitHeight + 20, toggle.implicitHeight + 20)
    Column {
        id: labels
        y: 10
        width: Math.max(0, parent.width - 78)
        spacing: 4
        PlainLabel {
            text: root.title
            width: parent.width
            font.pixelSize: Tokens.fontSize(18)
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            Accessible.ignored: true
        }
        PlainLabel {
            text: root.caption
            visible: text !== ""
            color: Tokens.secondary
            font.pixelSize: Tokens.fontSize(13)
            width: parent.width
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            Accessible.ignored: true
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
        Accessible.description: root.caption
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
