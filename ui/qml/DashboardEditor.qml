pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Dashboard.js" as Dashboard

Popup {
    id: root
    objectName: "dashboardEditor"
    required property var bridge
    property var draft: Dashboard.defaults()
    property double revision: 0
    property bool submitted: false
    property string message: ""
    readonly property var saved: bridge.snapshot ? bridge.snapshot.dashboard : null
    readonly property bool conflict: saved !== null && saved.revision !== revision && !bridge.dashboardSaving
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(760, parent ? parent.width - 40 : 760)
    height: Math.min(780, parent ? parent.height - 40 : 780)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    function reload() {
        if (!saved)
            return;
        draft = Dashboard.preferences(saved.preferences);
        revision = saved.revision;
        message = saved.error === "state_unavailable" ? "Saved layout could not be read. Defaults are shown; Apply saves a new layout." : saved.error === "save_unconfirmed" ? "The layout is visible, but saving was not confirmed. Apply to try again." : "";
    }
    function update(key, value) {
        let next = Dashboard.preferences(draft);
        next[key] = value;
        draft = Dashboard.preferences(next);
    }
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        let ancestor = item;
        if (!flick)
            return;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const point = item.mapToItem(flick.contentItem, 0, 0);
        let next = flick.contentY;
        if (point.y < next + 8)
            next = point.y - 8;
        else if (point.y + item.height > next + flick.height - 8)
            next = point.y + item.height - flick.height + 8;
        flick.contentY = Math.max(0, Math.min(next, Math.max(0, flick.contentHeight - flick.height)));
    }
    Component.onCompleted: reload()
    onOpened: closeButton.forceActiveFocus()
    Connections {
        target: root.bridge
        function onDashboardFinished(ok, code) {
            if (!root.submitted)
                return;
            root.submitted = false;
            if (ok) {
                root.close();
                return;
            }
            if (code === "save_unconfirmed" && root.saved && JSON.stringify(root.saved.preferences) === JSON.stringify(root.draft))
                root.revision = root.saved.revision;
            root.message = code === "save_unconfirmed" ? "The layout is visible, but saving could not be confirmed. Apply to retry." : code === "dashboard_changed" ? "The saved layout changed. Reload it before applying edits." : "The layout could not be saved. Your edits are still here. Try Apply again when connected.";
        }
    }
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
                text: "Customize dashboard"
                font.pixelSize: 25
            }
            ActionButton {
                id: closeButton
                objectName: "closeDashboardEditor"
                iconName: "close"
                accessibleLabel: "Close dashboard editor"
                onClicked: root.close()
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            text: "Choose what you see and its order. Current conditions, official alerts and source information always stay visible."
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            color: Tokens.secondary
            font.pixelSize: 13
        }
        TabBar {
            id: tabs
            objectName: "dashboardEditorTabs"
            Layout.fillWidth: true
            spacing: 6
            background: Item {}
            Repeater {
                model: ["Sections", "Metrics", "Hourly values"]
                delegate: TabButton {
                    id: tab
                    required property string modelData
                    required property int index
                    objectName: "dashboardEditorTab_" + index
                    text: modelData
                    implicitHeight: 40
                    background: Rectangle {
                        radius: 10
                        color: tab.checked ? "#704fa6d4" : tab.down ? "#80506a80" : "#303d5a70"
                        border.color: tab.activeFocus ? Tokens.accent : tab.checked ? "#7bd6fc" : Tokens.border
                        border.width: tab.activeFocus ? 2 : 1
                    }
                    contentItem: PlainLabel {
                        text: tab.text
                        horizontalAlignment: Text.AlignHCenter
                        verticalAlignment: Text.AlignVCenter
                    }
                }
            }
            onCurrentIndexChanged: if (scroll.contentItem)
                scroll.contentItem.contentY = 0
        }
        ScrollView {
            id: scroll
            objectName: "dashboardEditorScroll"
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            rightPadding: 18
            contentWidth: availableWidth
            contentHeight: body.implicitHeight
            ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
            ColumnLayout {
                id: body
                width: scroll.availableWidth
                spacing: 12
                enabled: !root.bridge.dashboardSaving
                PlainLabel {
                    Layout.fillWidth: true
                    text: tabs.currentIndex === 0 ? "Hide sections you don't need. Hidden maps and air quality stop requesting new data." : tabs.currentIndex === 1 ? "Keep at least one metric selected. These appear inside Current metrics." : "Pin 1–3 values per hour. Use the arrows to choose their order. Open any hour for all details."
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                    color: Tokens.secondary
                    font.pixelSize: 13
                }
                DashboardOrder {
                    Layout.fillWidth: true
                    visible: tabs.currentIndex === 0
                    rows: root.draft.sections
                    group: "sections"
                    onChanged: rows => root.update("sections", rows)
                }
                ColumnLayout {
                    visible: tabs.currentIndex === 0
                    Layout.fillWidth: true
                    PlainLabel {
                        text: "Spacing"
                        font.pixelSize: 18
                    }
                    ChoiceControl {
                        Layout.fillWidth: true
                        testName: "dashboardDensity"
                        choices: [
                            {
                                value: "spacious",
                                label: "Spacious"
                            },
                            {
                                value: "compact",
                                label: "Compact"
                            }
                        ]
                        value: root.draft.density
                        onChosen: value => root.update("density", value)
                    }
                }
                DashboardOrder {
                    Layout.fillWidth: true
                    visible: tabs.currentIndex === 1
                    rows: root.draft.metrics
                    group: "metrics"
                    minimum: 1
                    onChanged: rows => root.update("metrics", rows)
                }
                DashboardOrder {
                    Layout.fillWidth: true
                    visible: tabs.currentIndex === 2
                    rows: root.draft.hourly.concat(Dashboard.hourly.filter(id => root.draft.hourly.indexOf(id) < 0)).map(id => ({
                                id: id,
                                enabled: root.draft.hourly.indexOf(id) >= 0
                            }))
                    group: "hourly"
                    minimum: 1
                    maximum: 3
                    selectedOnlyMoves: true
                    onChanged: rows => root.update("hourly", rows.filter(row => row.enabled).map(row => row.id))
                }
            }
        }
        PlainLabel {
            objectName: "dashboardSaveStatus"
            Layout.fillWidth: true
            visible: text !== ""
            text: root.conflict ? "The saved layout changed. Reload it before applying edits." : root.message
            color: "#ffd99b"
            font.pixelSize: 13
            wrapMode: Text.Wrap
            elide: Text.ElideNone
        }
        RowLayout {
            Layout.fillWidth: true
            ActionButton {
                objectName: "dashboardDefaults"
                text: "Restore defaults"
                enabled: !root.bridge.dashboardSaving
                onClicked: {
                    root.draft = Dashboard.defaults();
                    root.message = "Defaults selected. Apply to save them.";
                }
            }
            Item {
                Layout.fillWidth: true
            }
            ActionButton {
                objectName: "dashboardReload"
                visible: root.conflict
                text: "Reload saved"
                onClicked: root.reload()
            }
            ActionButton {
                objectName: "dashboardApply"
                text: root.bridge.dashboardSaving ? "Saving…" : "Apply"
                primary: true
                enabled: root.bridge.available && !root.bridge.dashboardSaving && !root.conflict && root.saved !== null
                onClicked: {
                    root.submitted = true;
                    root.bridge.saveDashboard(root.revision, root.draft);
                }
            }
        }
    }
}
