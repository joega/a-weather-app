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
    property bool editing: false
    property string selectedId: ""
    property string aliasId: ""
    property string aliasDraft: ""
    property var pendingMutation: null
    readonly property var rows: registry ? registry.items : []
    readonly property bool canAct: serviceAvailable && !busy && !adding
    readonly property bool adding: locationSettings.busy
    signal closeRequested
    signal viewRequested(string id)
    signal actionRequested(var action)
    signal addRequested(var selection)
    signal searchRequested(var search)
    signal cancelSearchRequested
    color: Tokens.highContrast ? "#2c455a" : "#fc2c455a"

    function selectPlace(id) {
        selectedId = id;
        places.currentIndex = rows.findIndex(row => row.id === id);
    }
    function focusInitial() {
        if (page === "add")
            citySearch.focusQuery();
        else {
            selectPlace(rows.some(row => row.id === selectedId) ? selectedId : registry.viewed);
            places.forceActiveFocus();
        }
    }
    function renamePlace(row) {
        if (!editing || !canAct || !rows.some(item => item.id === row.id))
            return;
        selectPlace(row.id);
        if (aliasId !== row.id)
            aliasDraft = row.label;
        aliasId = row.id;
        places.positionViewAtIndex(places.currentIndex, ListView.Contain);
    }
    function activatePlace(row) {
        if (!canAct)
            return;
        if (editing)
            renamePlace(row);
        else
            viewRequested(row.id);
    }
    function mutatePlace(row, action) {
        if (!editing || !canAct || !rows.some(item => item.id === row.id))
            return;
        selectPlace(row.id);
        pendingMutation = action;
        if (action.action !== "rename") {
            aliasId = "";
            places.forceActiveFocus();
        }
        actionRequested(action);
    }
    function showSaved() {
        page = "saved";
        Qt.callLater(focusInitial);
    }
    function showAdd() {
        editing = false;
        page = "add";
        Qt.callLater(citySearch.focusQuery);
    }
    function revealFocus(item) {
        let ancestor = item;
        while (ancestor && ancestor !== places.contentItem && ancestor !== formBody)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const scroll = ancestor === places.contentItem ? places : formScroll;
        const point = item.mapToItem(scroll.contentItem, 0, 0);
        let next = scroll.contentY;
        if (point.y < next + 12)
            next = point.y - 12;
        else if (point.y + item.height > next + scroll.height - 12)
            next = point.y + item.height - scroll.height + 12;
        scroll.contentY = Math.max(0, Math.min(next, Math.max(0, scroll.contentHeight - scroll.height)));
    }
    onEditingChanged: {
        if (!editing) {
            aliasId = "";
            pendingMutation = null;
        }
    }
    onActionErrorChanged: if (actionError !== "")
        pendingMutation = null
    function syncRegistry() {
        if (aliasId !== "" && !rows.some(row => row.id === aliasId))
            aliasId = "";
        if (page !== "saved")
            return;
        selectPlace(rows.some(row => row.id === selectedId) ? selectedId : registry.viewed);
        const action = pendingMutation;
        if (!action)
            return;
        const row = rows.find(item => item.id === action.id);
        const confirmed = action.action === "rename" ? row && row.label === action.label : action.action === "primary" ? registry.primary === action.id : action.action === "remove" ? !row : action.action === "move" ? rows[action.index] && rows[action.index].id === action.id : false;
        if (confirmed) {
            pendingMutation = null;
            if (action.action === "rename" && aliasId === action.id)
                aliasId = "";
            places.forceActiveFocus();
        }
    }
    onRegistryChanged: registrySync.restart()
    Timer {
        id: registrySync
        interval: 0
        repeat: false
        onTriggered: root.syncRegistry()
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
                text: "Locations"
                font.pixelSize: Tokens.fontSize(24)
                Layout.fillWidth: true
            }
            ActionButton {
                id: editingToggle
                objectName: "toggleLocationEditing"
                visible: root.page === "saved"
                iconName: "sliders"
                checkable: true
                checked: root.editing
                selected: root.editing
                accessibleLabel: root.editing ? "Done editing locations" : "Edit locations"
                onClicked: {
                    root.editing = !root.editing;
                    editingToggle.forceActiveFocus();
                }
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
            visible: root.page !== "saved" || root.editing
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
                visible: root.page === "saved" && root.editing
                Layout.fillWidth: true
                enabled: root.rows.length < 20 && root.canAct && !root.adding
                onClicked: root.showAdd()
            }
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.page === "saved" && root.editing && root.rows.length >= 20
            text: "You have 20 saved locations. Remove one to add another."
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: Tokens.fontSize(13)
        }
        PlainLabel {
            Layout.fillWidth: true
            visible: root.page === "saved" && root.editing
            text: "Select a name to rename. Home opens by default and controls desktop weather."
            color: Tokens.secondary
            wrapMode: Text.Wrap
            elide: Text.ElideNone
            font.pixelSize: Tokens.fontSize(12)
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
                if (currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.activatePlace(root.rows[currentIndex]);
                    event.accepted = true;
                }
            }
            Keys.onEnterPressed: event => {
                if (currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.activatePlace(root.rows[currentIndex]);
                    event.accepted = true;
                }
            }
            Keys.onDownPressed: event => {
                if (currentIndex + 1 < root.rows.length)
                    root.selectPlace(root.rows[currentIndex + 1].id);
                event.accepted = true;
            }
            Keys.onUpPressed: event => {
                if (currentIndex > 0)
                    root.selectPlace(root.rows[currentIndex - 1].id);
                event.accepted = true;
            }
            Keys.onRightPressed: event => {
                if (root.editing && currentIndex >= 0 && currentIndex < root.rows.length) {
                    root.renamePlace(root.rows[currentIndex]);
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
                    Accessible.name: (root.editing ? "Rename " : "View ") + placeRow.label + (placeRow.home ? ", home location" : "") + (placeRow.viewing ? ", currently viewed" : "") + ". " + (placeRow.usable ? Forecast.temp(placeRow.summary.temperature_c, placeRow.units) + placeRow.units + ", " + Forecast.title(placeRow.summary.condition) : "Weather not loaded or expired") + ". " + Forecast.savedAlertText(placeRow.modelData)
                    onClicked: root.activatePlace(placeRow.modelData)
                    onActiveFocusChanged: if (activeFocus)
                        root.selectPlace(placeRow.modelData.id)
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
                                Button {
                                    objectName: "renameSaved_" + placeRow.index
                                    Layout.fillWidth: true
                                    enabled: root.canAct
                                    activeFocusOnTab: root.editing
                                    leftPadding: 0
                                    rightPadding: 0
                                    topPadding: 0
                                    bottomPadding: 0
                                    Accessible.name: (root.editing ? "Rename " : "View ") + placeRow.label
                                    onClicked: root.activatePlace(placeRow.modelData)
                                    background: Rectangle {
                                        radius: 4
                                        color: "transparent"
                                        border.color: parent.activeFocus ? Tokens.accent : "transparent"
                                    }
                                    contentItem: PlainLabel {
                                        objectName: "savedLocationName_" + placeRow.index
                                        text: placeRow.label
                                        font.pixelSize: Tokens.fontSize(18)
                                        font.weight: Font.DemiBold
                                    }
                                }
                                PlainLabel {
                                    Layout.fillWidth: true
                                    visible: placeRow.modelData.label !== ""
                                    text: Forecast.savedPlaceName(placeRow.modelData)
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
                            visible: placeRow.summary !== null
                            text: placeRow.summary === null ? "" : placeRow.summary.freshness === "invalid_future" ? "Weather time unavailable" : (placeRow.summary.freshness === "expired" ? "Expired · " : placeRow.summary.freshness === "stale" ? "Earlier weather · " : "As of ") + Qt.formatDateTime(new Date(placeRow.summary.valid_at), "MMM d, h:mm AP")
                            font.pixelSize: Tokens.fontSize(11)
                            color: Tokens.secondary
                        }
                        PlainLabel {
                            Layout.fillWidth: true
                            visible: placeRow.summary !== null && (placeRow.summary.alert_status === "active" || placeRow.summary.alert_status === "cached")
                            text: placeRow.summary && placeRow.summary.alert_status === "cached" ? "Cached weather alert" : "Weather alert"
                            color: Tokens.gold
                            font.pixelSize: Tokens.fontSize(12)
                        }
                        ColumnLayout {
                            Layout.fillWidth: true
                            visible: root.editing
                            spacing: 8
                            RowLayout {
                                Layout.fillWidth: true
                                spacing: 6
                                ActionButton {
                                    objectName: "removeSaved_" + placeRow.index
                                    iconName: "minus"
                                    accessibleLabel: "Remove " + placeRow.label
                                    enabled: root.canAct && root.rows.length > 1 && !placeRow.home
                                    onClicked: if (enabled && root.rows.length > 1 && !placeRow.home)
                                        root.mutatePlace(placeRow.modelData, {
                                            action: "remove",
                                            id: placeRow.modelData.id,
                                            replacement: ""
                                        })
                                }
                                ActionButton {
                                    objectName: "moveSavedUp_" + placeRow.index
                                    iconName: "chevron-up"
                                    accessibleLabel: "Move " + placeRow.label + " up"
                                    enabled: root.canAct && placeRow.index > 0
                                    onClicked: if (enabled)
                                        root.mutatePlace(placeRow.modelData, {
                                            action: "move",
                                            id: placeRow.modelData.id,
                                            index: placeRow.index - 1
                                        })
                                }
                                ActionButton {
                                    objectName: "moveSavedDown_" + placeRow.index
                                    iconName: "chevron-down"
                                    accessibleLabel: "Move " + placeRow.label + " down"
                                    enabled: root.canAct && placeRow.index < root.rows.length - 1
                                    onClicked: if (enabled)
                                        root.mutatePlace(placeRow.modelData, {
                                            action: "move",
                                            id: placeRow.modelData.id,
                                            index: placeRow.index + 1
                                        })
                                }
                                Item {
                                    Layout.fillWidth: true
                                }
                                ActionButton {
                                    objectName: "primarySaved_" + placeRow.index
                                    iconName: "home"
                                    selected: placeRow.home
                                    accessibleLabel: placeRow.home ? placeRow.label + " is Home" : "Set " + placeRow.label + " as Home"
                                    enabled: root.canAct && !placeRow.home
                                    onClicked: if (enabled && !placeRow.home)
                                        root.mutatePlace(placeRow.modelData, {
                                            action: "primary",
                                            id: placeRow.modelData.id
                                        })
                                }
                            }
                            PlainLabel {
                                Layout.fillWidth: true
                                visible: root.rows.length <= 1 || placeRow.home
                                text: root.rows.length <= 1 ? "Keep at least one location." : "Set another location as Home before removing this one."
                                color: Tokens.secondary
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                                font.pixelSize: Tokens.fontSize(12)
                            }
                        }
                        Loader {
                            id: aliasLoader
                            Layout.fillWidth: true
                            active: root.editing && root.aliasId === placeRow.modelData.id
                            visible: active
                            sourceComponent: ColumnLayout {
                                Component.onCompleted: Qt.callLater(() => {
                                    aliasInput.forceActiveFocus();
                                    aliasInput.selectAll();
                                    places.positionViewAtIndex(placeRow.index, ListView.Contain);
                                })
                                spacing: 8
                                TextField {
                                    id: aliasInput
                                    objectName: "savedLocationAlias"
                                    Layout.fillWidth: true
                                    text: root.aliasDraft
                                    onTextChanged: root.aliasDraft = text
                                    maximumLength: 160
                                    placeholderText: "Custom name, or leave blank"
                                    Accessible.name: "Custom name for " + placeRow.label + ", up to 80 characters"
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
                                    enabled: root.canAct && Forecast.codepoints(root.aliasDraft.trim()) <= 80 && root.aliasDraft.trim() !== placeRow.modelData.label
                                    onClicked: if (enabled)
                                        root.mutatePlace(placeRow.modelData, {
                                            action: "rename",
                                            id: placeRow.modelData.id,
                                            label: root.aliasDraft.trim()
                                        })
                                }
                            }
                        }
                    }
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
