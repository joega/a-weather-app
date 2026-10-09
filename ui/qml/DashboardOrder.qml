pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Layouts
import "Dashboard.js" as Dashboard

ColumnLayout {
    id: root
    property var rows: []
    property string group: "sections"
    property int minimum: 0
    property int maximum: rows.length
    property bool selectedOnlyMoves: false
    readonly property int selectedCount: rows.filter(row => row.enabled).length
    signal changed(var rows)
    function change(index, action) {
        let next = rows.map(row => ({
                    id: row.id,
                    enabled: row.enabled
                }));
        const id = next[index].id;
        if (action === "toggle")
            next[index].enabled = !next[index].enabled;
        else {
            const destination = index + (action === "up" ? -1 : 1);
            const row = next.splice(index, 1)[0];
            next.splice(destination, 0, row);
        }
        changed(next);
        // A model replacement removes the old focused delegate. Restore the
        // same row after the layout settles, even at an ordering boundary.
        Qt.callLater(() => {
            for (let i = 0; i < items.count; i++) {
                const item = items.itemAt(i);
                if (item && item.modelData.id === id) {
                    item.focusAction(action);
                    break;
                }
            }
        });
    }
    spacing: 4
    Repeater {
        id: items
        model: root.rows
        delegate: RowLayout {
            id: row
            required property var modelData
            required property int index
            Layout.fillWidth: true
            spacing: 8
            function focusAction(action) {
                if (action === "up" && up.enabled)
                    up.forceActiveFocus();
                else if (action === "down" && down.enabled)
                    down.forceActiveFocus();
                else if (down.enabled)
                    down.forceActiveFocus();
                else if (up.enabled)
                    up.forceActiveFocus();
                else
                    toggle.focusControl();
            }
            ToggleControl {
                id: toggle
                Layout.fillWidth: true
                title: Dashboard.title(row.modelData.id)
                caption: row.modelData.enabled ? "Shown" : "Hidden"
                checked: row.modelData.enabled
                optimistic: false
                locked: checked ? root.selectedCount <= root.minimum : root.selectedCount >= root.maximum
                testName: "dashboard_" + root.group + "_" + row.modelData.id
                onToggled: root.change(row.index, "toggle")
            }
            ActionButton {
                id: up
                objectName: "dashboard_" + root.group + "_" + row.modelData.id + "_up"
                text: "↑"
                accessibleLabel: "Move " + Dashboard.title(row.modelData.id) + " up"
                enabled: row.index > 0 && (!root.selectedOnlyMoves || row.modelData.enabled)
                onClicked: root.change(row.index, "up")
            }
            ActionButton {
                id: down
                objectName: "dashboard_" + root.group + "_" + row.modelData.id + "_down"
                text: "↓"
                accessibleLabel: "Move " + Dashboard.title(row.modelData.id) + " down"
                enabled: row.index + 1 < (root.selectedOnlyMoves ? root.selectedCount : root.rows.length) && (!root.selectedOnlyMoves || row.modelData.enabled)
                onClicked: root.change(row.index, "down")
            }
        }
    }
}
