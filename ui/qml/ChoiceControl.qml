import QtQuick
Row {
    id:root
    property var choices:[]
    property var value:""
    property string testName:"choice"
    signal chosen(var value)
    spacing:0
    Repeater {
        model:root.choices
        delegate:ActionButton {
            required property var modelData
            objectName:root.testName+"_"+modelData.value
            text:modelData.label
            width:root.width/root.choices.length
            selected:root.value===modelData.value
            onClicked:root.chosen(modelData.value)
        }
    }
}
