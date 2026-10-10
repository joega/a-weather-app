pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Changes.js" as Changes

Popup {
    id: root
    objectName: "forecastChanges"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(740, parent ? parent.width - 40 : 740)
    height: Math.min(720, parent ? parent.height - 40 : 720)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var result: null
    property string state: "loading"
    property string units: "F"
    property string windUnits: "auto"
    property string place: ""
    signal retryRequested
    onOpened: closeButton.forceActiveFocus()
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        if (item === closeButton && flick)
            flick.contentY = 0;
    }
    background: Rectangle {
        color: "#2c455a"
        radius: 18
        border.color: Tokens.border
    }
    Overlay.modal: Rectangle {
        color: "#990b1725"
    }
    contentItem: ScrollView {
        id: scroll
        objectName: "forecastChangesScroll"
        clip: true
        contentWidth: availableWidth
        contentHeight: body.implicitHeight
        rightPadding: 18
        ScrollBar.vertical.active: true
        Keys.onPressed: event => {
            const flick = scroll.contentItem as Flickable;
            if (!flick || [Qt.Key_Down, Qt.Key_Up, Qt.Key_PageDown, Qt.Key_PageUp, Qt.Key_Home, Qt.Key_End].indexOf(event.key) < 0)
                return;
            const limit = Math.max(0, flick.contentHeight - flick.height);
            const step = event.key === Qt.Key_Down ? 40 : event.key === Qt.Key_Up ? -40 : event.key === Qt.Key_PageDown ? flick.height * 0.8 : -flick.height * 0.8;
            flick.contentY = event.key === Qt.Key_Home ? 0 : event.key === Qt.Key_End ? limit : Math.max(0, Math.min(limit, flick.contentY + step));
            event.accepted = true;
        }
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        ColumnLayout {
            id: body
            width: scroll.availableWidth
            spacing: 16
            ActionButton {
                objectName: "retryForecastChanges"
                visible: root.state === "unavailable"
                text: "Retry comparison"
                onClicked: root.retryRequested()
            }
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Forecast changes"
                    font.pixelSize: Tokens.fontSize(26)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeForecastChanges"
                    iconName: "close"
                    accessibleLabel: "Close forecast changes"
                    onClicked: root.close()
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: root.place + " · Next 48 hours"
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                objectName: "forecastChangesStatus"
                visible: !root.result || root.result.changes.length === 0
                Layout.fillWidth: true
                text: Changes.summary(root.result, root.state, root.units)
                font.pixelSize: Tokens.fontSize(18)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            Repeater {
                model: root.result ? root.result.changes : []
                delegate: ColumnLayout {
                    id: changeRow
                    required property var modelData
                    Layout.fillWidth: true
                    spacing: 6
                    PlainLabel {
                        Layout.fillWidth: true
                        text: Changes.title(changeRow.modelData, root.units)
                        font.pixelSize: Tokens.fontSize(19)
                        font.bold: true
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        text: changeRow.modelData.range_label
                        color: Tokens.secondary
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        text: Changes.values(changeRow.modelData, root.units, root.windUnits)
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                }
            }
            PlainLabel {
                objectName: "forecastChangesCoverage"
                visible: root.result !== null && root.result.coverage.expected_points > 0
                Layout.fillWidth: true
                text: !root.result ? "" : "Hours compared\nTemperature: " + root.result.coverage.temperature + "/" + root.result.coverage.expected_points + "\nPrecipitation chance: " + root.result.coverage.probability + "/" + root.result.coverage.expected_intervals + " · Amount: " + root.result.coverage.precipitation + "/" + root.result.coverage.expected_intervals + " · Gusts: " + root.result.coverage.gusts + "/" + root.result.coverage.expected_intervals + "\nMissing hours are excluded; partly elapsed precipitation and gust intervals are excluded."
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                objectName: "forecastChangesProvenance"
                visible: root.result !== null
                Layout.fillWidth: true
                text: !root.result ? "" : "Open-Meteo · Forecasts retrieved\nCurrent: " + root.result.current_retrieved_label + (root.result.previous_retrieved_label ? "\nPrevious viewed: " + root.result.previous_retrieved_label : "\nNo previous viewed forecast is available.") + "\n" + root.result.timezone
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                visible: root.result !== null && (root.result.save_status === "unconfirmed" || root.result.history_recovered)
                Layout.fillWidth: true
                text: root.result && root.result.save_status === "unconfirmed" ? "History could not be saved. This comparison is available for this session; it may be lost after restarting." : "Earlier saved history was unavailable. A new comparison history has started."
                color: "#ffd99b"
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                Layout.fillWidth: true
                text: "Highlights show larger revisions in matching forecast hours. Smaller changes may still matter. Precipitation windows describe modeled hours, not exact onset times. Retrieval times are when this app obtained the data. These comparisons do not measure forecast accuracy."
                font.pixelSize: Tokens.fontSize(12)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
