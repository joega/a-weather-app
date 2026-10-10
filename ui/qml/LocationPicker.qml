pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    required property var registry
    required property var controls
    required property var search
    required property var locationSettings
    property bool serviceAvailable: true
    property bool busy: false
    property string actionError: ""
    property string page: "saved"
    property string editId: ""
    property string replacementId: ""
    readonly property var rows: registry ? registry.items : []
    readonly property var edited: rows.find(row => row.id === editId) || null
    readonly property int editIndex: rows.findIndex(row => row.id === editId)
    readonly property var replacements: rows.filter(row => row.id !== editId)
    readonly property bool canAct: serviceAvailable && !busy
    readonly property bool adding: locationSettings.busy
    signal closeRequested
    signal viewRequested(string id)
    signal actionRequested(var action)
    signal addRequested(var selection)
    signal searchRequested(var search)
    signal cancelSearchRequested
    color: Tokens.highContrast ? "#2c455a" : "#fc2c455a"

    function focusInitial() {
        if (page === "add")
            citySearch.focusQuery();
        else {
            places.currentIndex = Math.max(0, rows.findIndex(row => row.id === registry.viewed));
            places.forceActiveFocus();
        }
    }
    function editPlace(row) {
        editId = row.id;
        replacementId = "";
        aliasInput.text = row.label;
        page = "edit";
        Qt.callLater(() => {
            aliasInput.forceActiveFocus();
            aliasInput.selectAll();
        });
    }
    function showSaved() {
        page = "saved";
        Qt.callLater(focusInitial);
    }
    function showAdd() {
        page = "add";
        Qt.callLater(citySearch.focusQuery);
    }
    function revealFocus(item) {
        let ancestor = item;
        while (ancestor && ancestor !== formBody)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const point = item.mapToItem(formScroll.contentItem, 0, 0);
        let next = formScroll.contentY;
        if (point.y < next + 12)
            next = point.y - 12;
        else if (point.y + item.height > next + formScroll.height - 12)
            next = point.y + item.height - formScroll.height + 12;
        formScroll.contentY = Math.max(0, Math.min(next, Math.max(0, formScroll.contentHeight - formScroll.height)));
    }
    onRegistryChanged: {
        if (page === "edit" && edited === null)
            showSaved();
    }
    onPageChanged: if (formScroll)
        formScroll.contentY = 0
    ColumnLayout {
        anchors.fill: parent
        anchors.margins: 16
        spacing: 12
        RowLayout {
            Layout.fillWidth: true
            PlainLabel {
                text: root.page === "edit" ? "Edit location" : "Locations"
                font.pixelSize: Tokens.fontSize(24)
                Layout.fillWidth: true
            }
            ActionButton {
                objectName: "closeLocations"
                iconName: "close"
                accessibleLabel: "Close locations"
                onClicked: root.closeRequested()
            }
        }
        RowLayout {
            Layout.fillWidth: true
            ActionButton {
                objectName: "showSavedLocations"
                text: "Back to locations"
                visible: root.page !== "saved"
                Layout.fillWidth: true
                onClicked: root.showSaved()
            }
            ActionButton {
                objectName: "addSavedLocation"
                text: "Add location"
                visible: root.page === "saved"
                Layout.fillWidth: true
                enabled: root.rows.length < 20 && root.serviceAvailable
                onClicked: root.showAdd()
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.page === "saved" && root.rows.length >= 20
            text: "You have 20 saved locations. Remove one to add another."
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: Tokens.fontSize(13)
        }
        PlainLabel {
            objectName: "savedLocationStatus"
            Layout.fillWidth: true
            visible: text !== ""
            text: root.actionError || (root.adding ? "Finding location and loading forecast…" : Forecast.locationErrorText(root.locationSettings.error))
            color: Tokens.gold
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: Tokens.fontSize(14)
        }
        ListView {
            id: places
            objectName: "savedLocationList"
            visible: root.page === "saved"
            Layout.fillWidth: true
            Layout.fillHeight: true
            clip: true
            spacing: 8
            model: root.page === "saved" ? root.rows : []
            boundsBehavior: Flickable.StopAtBounds
            keyNavigationWraps: false
            ScrollBar.vertical: ScrollBar {}
            onCurrentIndexChanged: if (currentIndex >= 0)
                positionViewAtIndex(currentIndex, ListView.Contain)
            Keys.onReturnPressed: event => {
                if (root.canAct && currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.viewRequested(root.rows[currentIndex].id);
                    event.accepted = true;
                }
            }
            Keys.onEnterPressed: event => {
                if (root.canAct && currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.viewRequested(root.rows[currentIndex].id);
                    event.accepted = true;
                }
            }
            Keys.onRightPressed: event => {
                if (currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.editPlace(root.rows[currentIndex]);
                    event.accepted = true;
                }
            }
            delegate: Item {
                id: placeRow
                required property var modelData
                required property int index
                width: places.width - 12
                height: viewButton.implicitHeight
                readonly property var summary: modelData.summary
                readonly property bool usable: summary !== null && (summary.freshness === "fresh" || summary.freshness === "stale")
                readonly property string units: Forecast.savedUnits(modelData, root.controls)
                readonly property string label: Forecast.savedName(modelData)
                readonly property bool home: modelData.id === root.registry.primary
                readonly property bool viewing: modelData.id === root.registry.viewed
                Button {
                    id: viewButton
                    objectName: "viewSaved_" + placeRow.index
                    anchors.fill: parent
                    implicitHeight: Math.max(112, contentColumn.implicitHeight + 28)
                    leftPadding: 14
                    rightPadding: 14
                    topPadding: 12
                    bottomPadding: 16
                    hoverEnabled: true
                    enabled: root.canAct
                    Accessible.name: placeRow.label + (placeRow.home ? ", home location" : "") + (placeRow.viewing ? ", currently viewed" : "") + ". " + (placeRow.usable ? Forecast.temp(placeRow.summary.temperature_c, placeRow.units) + placeRow.units + ", " + Forecast.title(placeRow.summary.condition) : "Weather not loaded or expired") + ". " + Forecast.savedAlertText(placeRow.modelData)
                    onClicked: root.viewRequested(placeRow.modelData.id)
                    onActiveFocusChanged: if (activeFocus)
                        places.currentIndex = placeRow.index
                    background: Rectangle {
                        radius: 14
                        color: placeRow.viewing ? "#365a76" : viewButton.hovered ? "#334c62" : "#283f54"
                        border.width: viewButton.activeFocus || (places.activeFocus && places.currentIndex === placeRow.index) ? 2 : 1
                        border.color: viewButton.activeFocus || (places.activeFocus && places.currentIndex === placeRow.index) || placeRow.viewing ? Tokens.accent : Tokens.border
                    }
                    contentItem: ColumnLayout {
                        id: contentColumn
                        spacing: 5
                        RowLayout {
                            Layout.fillWidth: true
                            spacing: 8
                            ColumnLayout {
                                Layout.fillWidth: true
                                spacing: 3
                                PlainLabel {
                                    Layout.fillWidth: true
                                    text: placeRow.label
                                    font.pixelSize: Tokens.fontSize(18)
                                    font.weight: Font.DemiBold
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    visible: placeRow.modelData.label !== ""
                                    text: placeRow.modelData.name
                                    font.pixelSize: Tokens.fontSize(12)
                                    color: Tokens.secondary
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    visible: text !== ""
                                    text: [placeRow.home ? "Home" : "", placeRow.viewing ? "Viewing" : "", placeRow.modelData.mode === "auto" ? "Current location" : ""].filter(value => value !== "").join(" · ")
                                    font.pixelSize: Tokens.fontSize(12)
                                    color: Tokens.accent
                                }
                            }
                            PlainLabel {
                                text: placeRow.usable ? Forecast.temp(placeRow.summary.temperature_c, placeRow.units) : "—"
                                font.pixelSize: Tokens.fontSize(32)
                            }
                        }
                        RowLayout {
                            Layout.fillWidth: true
                            Layout.rightMargin: 46
                            spacing: 6
                            WeatherIcon {
                                condition: placeRow.usable ? placeRow.summary.condition : "unknown"
                                isDay: placeRow.usable ? placeRow.summary.is_day !== false : true
                                Layout.preferredWidth: 26
                                Layout.preferredHeight: 24
                            }
                            PlainLabel {
                                Layout.fillWidth: true
                                text: placeRow.usable ? Forecast.title(placeRow.summary.condition) : placeRow.summary === null ? "Weather not loaded" : "Weather unavailable"
                                font.pixelSize: Tokens.fontSize(13)
                            }
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            Layout.rightMargin: 46
                            visible: placeRow.summary !== null
                            text: placeRow.summary === null ? "" : placeRow.summary.freshness === "invalid_future" ? "Weather time unavailable" : (placeRow.summary.freshness === "expired" ? "Expired · " : placeRow.summary.freshness === "stale" ? "Earlier weather · " : "As of ") + Qt.formatDateTime(new Date(placeRow.summary.valid_at), "MMM d, h:mm AP")
                            font.pixelSize: Tokens.fontSize(11)
                            color: Tokens.secondary
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            Layout.rightMargin: 46
                            visible: placeRow.summary !== null && (placeRow.summary.alert_status === "active" || placeRow.summary.alert_status === "cached")
                            text: placeRow.summary && placeRow.summary.alert_status === "cached" ? "Cached weather alert" : "Weather alert"
                            color: Tokens.gold
                            font.pixelSize: Tokens.fontSize(12)
                        }
                    }
                }
                ActionButton {
                    objectName: "editSaved_" + placeRow.index
                    anchors.right: parent.right
                    anchors.bottom: parent.bottom
                    anchors.margins: 8
                    iconName: "sliders"
                    accessibleLabel: "Edit " + placeRow.label
                    enabled: root.canAct
                    onClicked: root.editPlace(placeRow.modelData)
                }
            }
        }
        Flickable {
            id: formScroll
            objectName: "locationFormScroll"
            Layout.fillWidth: true
            Layout.fillHeight: true
            visible: root.page !== "saved"
            clip: true
            contentHeight: formBody.height
            boundsBehavior: Flickable.StopAtBounds
            ScrollBar.vertical: ScrollBar {}
            ColumnLayout {
                id: formBody
                width: parent.width - 16
                spacing: 16
                ColumnLayout {
                    visible: root.page === "edit" && root.edited !== null
                    Layout.fillWidth: true
                    spacing: 14
                    PlainLabel {
                        Layout.fillWidth: true
                        text: root.edited ? root.edited.name : ""
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(20)
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        visible: root.registry && root.editId === root.registry.primary
                        text: "The app opens at Home. Desktop weather and notifications follow it too."
                        color: Tokens.secondary
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(13)
                    }
                    PlainLabel {
                        text: "Custom name"
                        color: Tokens.secondary
                        font.pixelSize: Tokens.fontSize(14)
                    }
                    TextField {
                        id: aliasInput
                        objectName: "savedLocationAlias"
                        Layout.fillWidth: true
                        maximumLength: 160
                        placeholderText: "Work, cabin, or leave blank"
                        Accessible.name: "Custom location name, up to 80 characters"
                        color: Tokens.foreground
                        placeholderTextColor: Tokens.secondary
                        selectByMouse: true
                        enabled: root.canAct
                        font.pixelSize: Tokens.fontSize(17)
                        background: Rectangle {
                            implicitHeight: 44
                            radius: 10
                            color: "#30435e72"
                            border.color: aliasInput.activeFocus ? Tokens.accent : Tokens.border
                        }
                        onAccepted: if (saveName.enabled)
                            saveName.clicked()
                    }
                    ActionButton {
                        id: saveName
                        objectName: "saveLocationAlias"
                        text: "Save name"
                        enabled: root.canAct && root.edited !== null && Forecast.codepoints(aliasInput.text.trim()) <= 80 && aliasInput.text.trim() !== root.edited.label
                        onClicked: root.actionRequested({
                            action: "rename",
                            id: root.editId,
                            label: aliasInput.text.trim()
                        })
                    }
                    RowLayout {
                        ActionButton {
                            objectName: "moveLocationUp"
                            text: "Move up"
                            enabled: root.canAct && root.editIndex > 0
                            onClicked: root.actionRequested({
                                action: "move",
                                id: root.editId,
                                index: root.editIndex - 1
                            })
                        }
                        ActionButton {
                            objectName: "moveLocationDown"
                            text: "Move down"
                            enabled: root.canAct && root.editIndex >= 0 && root.editIndex < root.rows.length - 1
                            onClicked: root.actionRequested({
                                action: "move",
                                id: root.editId,
                                index: root.editIndex + 1
                            })
                        }
                    }
                    ActionButton {
                        objectName: "makeLocationPrimary"
                        text: root.registry && root.editId === root.registry.primary ? "Home location" : "Set as Home"
                        enabled: root.canAct && root.registry && root.editId !== root.registry.primary
                        onClicked: root.actionRequested({
                            action: "primary",
                            id: root.editId
                        })
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        text: Forecast.savedAlertText(root.edited)
                        color: Tokens.secondary
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(14)
                    }
                    Rectangle {
                        Layout.fillWidth: true
                        Layout.preferredHeight: 1
                        color: Tokens.border
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        text: root.rows.length <= 1 ? "Keep at least one saved location." : root.registry && root.editId === root.registry.primary ? "Choose a new Home before removing this location." : "Remove this location from your saved list."
                        color: Tokens.secondary
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(14)
                    }
                    SettingsComboBox {
                        id: replacement
                        objectName: "replacementPrimary"
                        Layout.fillWidth: true
                        visible: root.registry && root.editId === root.registry.primary && root.rows.length > 1
                        model: root.replacements.map(row => ({
                                    id: row.id,
                                    name: Forecast.savedName(row)
                                }))
                        textRole: "name"
                        currentIndex: root.replacements.findIndex(row => row.id === root.replacementId)
                        displayText: currentIndex >= 0 ? currentText : "Choose replacement…"
                        onActivated: index => root.replacementId = root.replacements[index].id
                        Accessible.name: "New Home location"
                        enabled: root.canAct
                    }
                    ActionButton {
                        objectName: "removeSavedLocation"
                        text: "Remove location"
                        enabled: root.canAct && root.rows.length > 1 && (root.registry && root.editId !== root.registry.primary || root.replacements.some(row => row.id === root.replacementId))
                        onClicked: root.actionRequested({
                            action: "remove",
                            id: root.editId,
                            replacement: root.editId === root.registry.primary ? root.replacementId : ""
                        })
                    }
                }
                ColumnLayout {
                    Layout.fillWidth: true
                    visible: root.page === "add"
                    spacing: 14
                    PlaceSearch {
                        id: citySearch
                        objectPrefix: "saved"
                        Layout.fillWidth: true
                        search: root.search
                        serviceAvailable: root.serviceAvailable
                        locationBusy: root.busy || root.adding || root.rows.length >= 20
                        selectionHint: "Optional country code, such as DE or BR. Choose a city to save and view it."
                        onSearchRequested: values => root.searchRequested(values)
                        onCancelRequested: root.cancelSearchRequested()
                        onLocationRequested: values => root.addRequested(values)
                    }
                    PlainLabel {
                        text: "Or use a 5-digit US ZIP code"
                        color: Tokens.secondary
                        font.pixelSize: Tokens.fontSize(14)
                    }
                    RowLayout {
                        Layout.fillWidth: true
                        TextField {
                            id: zip
                            objectName: "savedLocationZip"
                            Layout.fillWidth: true
                            maximumLength: 5
                            validator: RegularExpressionValidator {
                                regularExpression: /[0-9]{0,5}/
                            }
                            Accessible.name: "US ZIP code to add"
                            placeholderText: "ZIP code"
                            color: Tokens.foreground
                            placeholderTextColor: Tokens.secondary
                            enabled: root.canAct && !root.adding && root.rows.length < 20
                            inputMethodHints: Qt.ImhDigitsOnly
                            font.pixelSize: Tokens.fontSize(17)
                            selectByMouse: true
                            background: Rectangle {
                                implicitHeight: 44
                                radius: 10
                                color: "#30435e72"
                                border.color: zip.activeFocus ? Tokens.accent : Tokens.border
                            }
                            onAccepted: if (addZip.enabled)
                                addZip.clicked()
                        }
                        ActionButton {
                            id: addZip
                            objectName: "addSavedZip"
                            text: "Add ZIP"
                            enabled: zip.enabled && /^[0-9]{5}$/.test(zip.text)
                            onClicked: root.addRequested({
                                mode: "zip",
                                zip_code: zip.text
                            })
                        }
                    }
                    ActionButton {
                        objectName: "addCurrentLocation"
                        text: "Use current location"
                        enabled: root.canAct && !root.adding && root.rows.length < 20
                        onClicked: root.addRequested({
                            mode: "auto"
                        })
                    }
                    PlainLabel {
                        Layout.fillWidth: true
                        text: "Uses ipwho.is to estimate your city from your public IP. VPNs can affect accuracy."
                        color: Tokens.secondary
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                        font.pixelSize: Tokens.fontSize(13)
                    }
                }
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.page === "saved"
            text: "Weather times use your desktop timezone."
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: Tokens.fontSize(12)
        }
    }
}
