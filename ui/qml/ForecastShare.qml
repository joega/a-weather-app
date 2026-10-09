pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Share.js" as Share

Popup {
    id: root
    objectName: "forecastShare"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(740, parent ? parent.width - 40 : 740)
    height: Math.min(800, parent ? parent.height - 40 : 800)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var snapshot: null
    property bool includePlace: true
    readonly property var preview: Share.summary(snapshot, includePlace)
    readonly property string plainText: Share.text(preview)
    property var frozenPreview: null
    property var pendingCapture: null
    property string notice: ""
    readonly property bool busy: pendingCapture !== null
    signal imageFileRequested
    function notify(message) {
        notice = message;
        noticeTimer.restart();
    }
    function cancelExport() {
        if (pendingCapture)
            pendingCapture.canceled = true;
        pendingCapture = null;
        frozenPreview = null;
    }
    function copyText() {
        if (!preview || busy)
            return;
        clipboardText.text = plainText;
        clipboardText.selectAll();
        clipboardText.copy();
        clipboardText.deselect();
        clipboardText.text = "";
        notify("Forecast copied.");
    }
    function chooseImageFile() {
        if (!visible || !preview || busy)
            return;
        imageFileRequested();
    }
    function imageFileDialogFinished() {
        if (visible)
            saveImageButton.forceActiveFocus();
    }
    function saveImage(destination) {
        if (!preview || busy || !visible)
            return false;
        const url = destination.toString();
        if (!url.startsWith("file:///") || !/\.png$/i.test(url)) {
            notify("Choose a local file with a .png extension.");
            return false;
        }
        const ticket = {
            canceled: false
        };
        pendingCapture = ticket;
        frozenPreview = preview;
        // Let the fixed preview settle before Qt takes its one asynchronous grab.
        Qt.callLater(() => {
            if (ticket.canceled)
                return;
            const height = Math.ceil(card.height * 720 / card.width);
            // Qt multiplies the grab target by the window's display scale.
            // Keep the exported physical pixels bounded on HiDPI displays too.
            const windowScale = card.Window.window.devicePixelRatio;
            const scale = typeof windowScale === "number" ? windowScale : card.Screen.devicePixelRatio;
            const target = Qt.size(Math.floor(720 / scale), Math.floor(height / scale));
            if (height > 1600 || target.width < 1 || target.height < 1 || !card.grabToImage(result => {
                if (ticket.canceled)
                    return;
                const saved = result.saveToFile(Qt.resolvedUrl(url));
                pendingCapture = null;
                frozenPreview = null;
                notify(saved ? "Forecast image saved." : "Could not save the image. Choose another location and try again.");
            }, target)) {
                pendingCapture = null;
                frozenPreview = null;
                notify("Could not create the image. Try again.");
            }
        });
        return true;
    }
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        if (!flick || !item)
            return;
        let ancestor = item;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const y = item.mapToItem(flick.contentItem, 0, 0).y;
        if (y < flick.contentY + 12)
            flick.contentY = Math.max(0, y - 12);
        else if (y + item.height > flick.contentY + flick.height - 12)
            flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, y + item.height - flick.height + 12));
    }
    onOpened: closeButton.forceActiveFocus()
    onClosed: cancelExport()
    Component.onDestruction: cancelExport()
    background: Rectangle {
        color: "#2c455a"
        radius: 18
        border.color: Tokens.border
    }
    Overlay.modal: Rectangle {
        color: "#990b1725"
    }
    Timer {
        id: noticeTimer
        interval: 5000
        onTriggered: root.notice = ""
    }
    TextEdit {
        id: clipboardText
        visible: false
        textFormat: TextEdit.PlainText
        readOnly: true
    }
    contentItem: ScrollView {
        id: scroll
        objectName: "forecastShareScroll"
        clip: true
        contentWidth: availableWidth
        contentHeight: body.implicitHeight
        rightPadding: 18
        Keys.onPressed: event => {
            const flick = scroll.contentItem as Flickable;
            if (!flick || [Qt.Key_PageDown, Qt.Key_PageUp].indexOf(event.key) < 0)
                return;
            flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + (event.key === Qt.Key_PageDown ? 1 : -1) * flick.height * .8));
            event.accepted = true;
        }
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        ColumnLayout {
            id: body
            width: scroll.availableWidth
            spacing: 14
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Share forecast"
                    font.pixelSize: 26
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeForecastShare"
                    iconName: "close"
                    accessibleLabel: "Close forecast sharing"
                    onClicked: root.close()
                }
            }
            ToggleControl {
                Layout.fillWidth: true
                title: "Include place name"
                caption: "The timezone stays visible. Coordinates are never included."
                testName: "shareIncludePlace"
                checked: root.includePlace
                locked: root.busy
                onToggled: value => root.includePlace = value
            }
            RowLayout {
                Layout.fillWidth: true
                ActionButton {
                    objectName: "copyForecastText"
                    text: "Copy text"
                    enabled: root.preview !== null && !root.busy
                    onClicked: root.copyText()
                }
                ActionButton {
                    id: saveImageButton
                    objectName: "saveForecastImage"
                    text: root.busy ? "Saving…" : "Save image…"
                    enabled: root.preview !== null && !root.busy
                    // Finish the triggering key/pointer event before a native
                    // dialog takes the window's input focus.
                    onClicked: Qt.callLater(root.chooseImageFile)
                }
            }
            PlainLabel {
                objectName: "forecastShareNotice"
                Layout.fillWidth: true
                visible: root.notice !== ""
                text: root.notice
                font.pixelSize: 13
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                Accessible.role: Accessible.AlertMessage
            }
            ForecastShareCard {
                id: card
                Layout.fillWidth: true
                summary: root.frozenPreview || root.preview
            }
        }
    }
}
