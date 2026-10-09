pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Layouts
import "Dashboard.js" as Dashboard

ColumnLayout {
    id: root
    property var preferences: Dashboard.defaults()
    property bool paired: false
    property var components: ({})
    property var mapItem: null
    property var rows: []
    readonly property string rowKey: JSON.stringify(Dashboard.rows(preferences.sections, true))
    onRowKeyChanged: rows = JSON.parse(rowKey)
    readonly property real mapTop: {
        let item = mapItem, top = 0;
        // Reading each ancestor's y keeps this binding current after resize,
        // section reordering and changes to a preceding card's height.
        while (item && item !== root) {
            top += item.y;
            item = item.parent;
        }
        return top;
    }
    spacing: preferences.density === "compact" ? 10 : 16
    Repeater {
        model: root.rows
        delegate: GridLayout {
            id: row
            required property var modelData
            Layout.fillWidth: true
            columns: root.paired ? row.modelData.length : 1
            columnSpacing: root.spacing
            rowSpacing: root.spacing
            Repeater {
                model: row.modelData
                delegate: Loader {
                    id: section
                    required property string modelData
                    objectName: "dashboardSection_" + modelData
                    Layout.fillWidth: true
                    Layout.fillHeight: true
                    Layout.preferredWidth: root.paired && row.modelData.length === 2 ? (modelData === "daily" ? 54 : 44) : 1
                    sourceComponent: root.components[modelData] || null
                    onItemChanged: if (modelData === "maps")
                        root.mapItem = item
                    Component.onDestruction: if (modelData === "maps" && root.mapItem === item)
                        root.mapItem = null
                }
            }
        }
    }
}
