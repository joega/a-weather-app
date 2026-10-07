pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts

ColumnLayout {
    id: root
    spacing: 8
    property var search: ({
            generation: 0,
            client_token: 0,
            status: "idle",
            results: [],
            error: null
        })
    property bool serviceAvailable: true
    property bool locationBusy: false
    property bool armed: false
    property int nextClientToken: 1
    property int currentClientToken: 0
    property int previousGeneration: -1
    property int activeGeneration: -1
    readonly property bool showingSearch: armed && activeGeneration >= 0 && search.client_token === currentClientToken && search.generation === activeGeneration
    readonly property var visibleRows: showingSearch && search.status === "ready" ? search.results : []
    signal searchRequested(var values)
    signal cancelRequested
    signal locationRequested(var values)

    function invalidate() {
        let hadSearch = armed || search.status !== "idle";
        debounce.stop();
        armed = false;
        activeGeneration = -1;
        currentClientToken = 0;
        if (serviceAvailable && hadSearch)
            cancelRequested();
    }
    function pick(row) {
        if (!showingSearch || search.status !== "ready" || locationBusy || !serviceAvailable)
            return;
        let generation = activeGeneration;
        debounce.stop();
        armed = false;
        activeGeneration = -1;
        currentClientToken = 0;
        locationRequested({
            mode: "place",
            place_id: row.id,
            search_generation: generation
        });
    }
    onSearchChanged: {
        if (armed && activeGeneration < 0 && search.client_token === currentClientToken && search.status !== "idle" && search.generation !== previousGeneration)
            activeGeneration = search.generation;
    }
    onVisibleChanged: if (!visible)
        invalidate()
    onServiceAvailableChanged: if (!serviceAvailable)
        invalidate()
    onLocationBusyChanged: if (locationBusy)
        invalidate()
    Timer {
        id: debounce
        objectName: "placeSearchDebounce"
        interval: 350
        repeat: false
        onTriggered: {
            let query = cityInput.text.trim();
            let code = countryInput.text.trim().toUpperCase();
            if (!root.visible || !root.serviceAvailable || root.locationBusy || query.length < 2 || query.length > 120 || (code !== "" && !/^[A-Z]{2}$/.test(code)))
                return;
            root.previousGeneration = root.search.generation;
            root.currentClientToken = root.nextClientToken;
            root.nextClientToken = root.nextClientToken === 2147483647 ? 1 : root.nextClientToken + 1;
            root.armed = true;
            root.searchRequested({
                query: query,
                country_code: code,
                client_token: root.currentClientToken
            });
        }
    }
    PlainLabel {
        text: "Search worldwide cities"
        font.pixelSize: 14
        color: Tokens.secondary
    }
    RowLayout {
        Layout.fillWidth: true
        TextField {
            id: cityInput
            objectName: "placeQuery"
            Layout.fillWidth: true
            maximumLength: 120
            enabled: root.serviceAvailable && !root.locationBusy
            Accessible.name: "City name"
            placeholderText: "City name"
            color: Tokens.foreground
            placeholderTextColor: Tokens.secondary
            font.pixelSize: 16
            selectByMouse: true
            background: Rectangle {
                implicitHeight: 42
                radius: 10
                color: "#30435e72"
                border.color: cityInput.activeFocus ? Tokens.accent : Tokens.border
            }
            onTextChanged: {
                root.invalidate();
                debounce.restart();
            }
            Keys.onDownPressed: event => {
                if (results.count > 0) {
                    results.currentIndex = 0;
                    results.forceActiveFocus();
                    event.accepted = true;
                }
            }
            onAccepted: if (results.count > 0)
                root.pick(root.visibleRows[0])
        }
        TextField {
            id: countryInput
            objectName: "placeCountry"
            Layout.preferredWidth: 106
            maximumLength: 2
            validator: RegularExpressionValidator {
                regularExpression: /[A-Za-z]{0,2}/
            }
            enabled: root.serviceAvailable && !root.locationBusy
            Accessible.name: "Optional two-letter country code"
            placeholderText: "Country"
            color: Tokens.foreground
            placeholderTextColor: Tokens.secondary
            font.pixelSize: 16
            selectByMouse: true
            background: Rectangle {
                implicitHeight: 42
                radius: 10
                color: "#30435e72"
                border.color: countryInput.activeFocus ? Tokens.accent : Tokens.border
            }
            onTextChanged: {
                root.invalidate();
                debounce.restart();
            }
        }
    }
    PlainLabel {
        Layout.fillWidth: true
        text: "Optional country code, such as DE or BR. Choose a result to change location."
        font.pixelSize: 13
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    PlainLabel {
        Layout.fillWidth: true
        text: "Place data: GeoNames via Open-Meteo (CC BY 4.0)."
        font.pixelSize: 12
        color: Tokens.secondary
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    PlainLabel {
        objectName: "placeSearchStatus"
        Layout.fillWidth: true
        visible: text !== ""
        text: !root.serviceAvailable ? "Search unavailable while disconnected." : cityInput.text.trim().length === 1 ? "Enter at least two characters." : countryInput.text.length === 1 ? "Enter a two-letter country code or leave it blank." : !root.showingSearch ? "" : root.search.status === "loading" ? "Searching places…" : root.search.status === "error" ? (root.search.error === "offline" ? "Search unavailable offline." : root.search.error === "timeout" ? "Search timed out. Edit the query to retry." : "Place search failed. Edit the query to retry.") : root.search.status === "ready" && root.search.results.length === 0 ? "No matching places found." : ""
        color: Tokens.secondary
        font.pixelSize: 14
        wrapMode: Text.Wrap
        elide: Text.ElideNone
    }
    ListView {
        id: results
        objectName: "placeResults"
        Layout.fillWidth: true
        Layout.preferredHeight: Math.min(contentHeight, 260)
        visible: count > 0
        clip: true
        model: root.visibleRows
        keyNavigationWraps: false
        boundsBehavior: Flickable.StopAtBounds
        onCurrentIndexChanged: if (currentIndex >= 0)
            positionViewAtIndex(currentIndex, ListView.Contain)
        delegate: Button {
            id: placeResult
            required property var modelData
            required property int index
            width: results.width
            height: Math.max(42, resultLabel.implicitHeight + 12)
            objectName: "placeResult_" + index
            enabled: root.serviceAvailable && !root.locationBusy
            Accessible.name: modelData.name + (modelData.admin1 ? ", " + modelData.admin1 : "") + ", " + modelData.country
            onClicked: root.pick(modelData)
            background: Rectangle {
                radius: 8
                color: placeResult.activeFocus || placeResult.hovered || results.activeFocus && results.currentIndex === placeResult.index ? "#304b667e" : "#18283b50"
                border.color: placeResult.activeFocus || results.activeFocus && results.currentIndex === placeResult.index ? Tokens.accent : "transparent"
            }
            contentItem: PlainLabel {
                id: resultLabel
                text: parent.Accessible.name
                verticalAlignment: Text.AlignVCenter
                leftPadding: 12
                rightPadding: 12
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
        Keys.onReturnPressed: event => {
            if (currentIndex >= 0 && currentIndex < root.visibleRows.length) {
                root.pick(root.visibleRows[currentIndex]);
                event.accepted = true;
            }
        }
        Keys.onEnterPressed: event => {
            if (currentIndex >= 0 && currentIndex < root.visibleRows.length) {
                root.pick(root.visibleRows[currentIndex]);
                event.accepted = true;
            }
        }
        Keys.onEscapePressed: event => {
            cityInput.forceActiveFocus();
            event.accepted = true;
        }
    }
}
