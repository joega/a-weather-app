pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

GlassPanel {
    id: root
    property string effectiveVisualQuality: "static"
    property bool presentationActive: true
    property var appearance: (I18n.revision, Forecast.appearance())
    signal appearanceRequested(var values)
    property bool plannerAvailable: false
    property bool forecastChangesAvailable: false
    property bool shareAvailable: false
    property bool dashboardAvailable: false
    signal outdoorRequested
    signal forecastChangesRequested
    signal shareRequested
    signal dashboardRequested
    // A settings surface must obscure the forecast's text, not layer two
    // readable text grids on top of one another.
    color: Tokens.highContrast ? "#2c455a" : "#fa2c455a"
    property var controls: ({
            mode: "live",
            strength: "normal",
            manual: {
                condition: "rain"
            },
            fps: 30,
            window_physics: true,
            accumulation: true,
            lightning_enabled: false,
            reduced_motion: false,
            pause_fullscreen: true
        })
    property string location: I18n.t("Location unavailable")
    property string primaryLocation: location
    property bool hasSavedLocations: false
    signal manageLocationsRequested
    property var locationSettings: ({
            mode: "default",
            zip_code: null,
            busy: false,
            error: null
        })
    property var placeSearch: ({
            generation: 0,
            status: "idle",
            results: [],
            error: null
        })
    property string displayedSelection: ""
    onLocationSettingsChanged: {
        let selection = locationSettings.mode + ":" + (locationSettings.zip_code || "");
        if (selection !== displayedSelection) {
            displayedSelection = selection;
            zipInput.text = locationSettings.mode === "zip" ? locationSettings.zip_code : "";
        }
    }
    readonly property bool locationLocked: busy || !serviceAvailable || locationSettings.busy
    function applyZip() {
        if (!locationLocked && /^[0-9]{5}$/.test(zipInput.text))
            locationRequested({
                mode: "zip",
                zip_code: zipInput.text
            });
    }
    property string serviceStatus: I18n.t("Unavailable")
    property string rendererStatus: I18n.t("Stopped")
    property bool busy: false
    property bool canStart: false
    property bool forecastAvailable: false
    property bool serviceAvailable: true
    property bool effectsRunning: false
    property bool persistent: false
    property var notifications: (I18n.revision, Forecast.notifications())
    property var officialWarnings: (I18n.revision, Forecast.warningNotifications())
    property string timezone: "UTC"
    property var setup: ({
            status: "unchecked",
            reason: "not_checked",
            outputs: [],
            selected_output: null,
            output_selection: "automatic"
        })
    property string actionError: ""
    readonly property string compatibilityText: ({
            not_checked: I18n.t("Check this desktop before turning on effects."),
            ready: I18n.t("Compatibility checks passed for this desktop."),
            wayland_required: I18n.t("Desktop effects require a Hyprland Wayland session."),
            session_unavailable: I18n.t("The current Hyprland session could not be verified. Reopen the app from your desktop and check again."),
            output_unavailable: I18n.t("The selected monitor is no longer available. Check again to refresh the monitor list."),
            output_selection_required: I18n.t("Choose which monitor should show desktop effects. Other monitors stay available."),
            unsupported_output: I18n.t("Desktop effects require a physical, unrotated SDR monitor within the supported size limits. The forecast app remains available."),
            plugin_conflict: I18n.t("Another native Hyprland plugin is loaded. Effects require an otherwise empty native plugin list; no plugins will be removed by this check."),
            native_missing: I18n.t("Desktop effects components are missing. Update or reinstall A Weather App, then check again."),
            native_incompatible: I18n.t("Desktop effects do not match this Hyprland build or the compatibility check failed. Update A Weather App for a matching release. Forecasts remain available."),
            activation_failed: I18n.t("The desktop changed or effects could not start. Check compatibility again before retrying.")
        })[setup.reason] || I18n.t("Desktop effects unavailable.")
    function revealFocus(item) {
        let ancestor = item;
        while (ancestor && ancestor !== body)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        let point = item.mapToItem(settingsScroll.contentItem, 0, 0), next = settingsScroll.contentY;
        if (point.y < next + 12)
            next = point.y - 12;
        else if (point.y + item.height > next + settingsScroll.height - 12)
            next = point.y + item.height - settingsScroll.height + 12;
        settingsScroll.contentY = Math.max(0, Math.min(next, Math.max(0, settingsScroll.contentHeight - settingsScroll.height)));
    }
    signal closeRequested
    signal patch(var values)
    signal startRequested
    signal stopRequested
    signal locationRequested(var values)
    signal placeSearchRequested(var values)
    signal placeSearchCancelRequested
    signal checkRequested
    signal outputRequested(string output)
    signal notificationsPatch(var values)
    signal notificationPauseRequested
    signal notificationResumeRequested
    signal warningPatch(var values)
    signal warningPauseRequested
    signal warningResumeRequested
    signal warningDetailRequested(var reference)
    property string launcherStatus: "ready"
    property var updateStatus: (I18n.revision, Forecast.updateStatus(null))
    signal checkUpdatesRequested
    signal installUpdateRequested
    signal installLauncherRequested
    signal quitRequested
    property Item activeSection: applicationSection
    function updateActiveSection() {
        const sections = [applicationSection, locationSection, notificationsSection, effectsSection, previewSection, appearanceSection, experimentalSection];
        let current = applicationSection;
        if (settingsScroll.contentY > 0 && settingsScroll.contentY + settingsScroll.height >= settingsScroll.contentHeight - 1)
            current = experimentalSection;
        else
            for (const section of sections)
                if (section.y <= settingsScroll.contentY + 24)
                    current = section;
        activeSection = current;
    }
    function focusLocation() {
        showSection(locationSection);
        citySearch.focusQuery();
    }
    function showNotifications(reference) {
        showSection(notificationsSection);
        if (!notificationsSection.focusWarning(reference))
            notificationsButton.forceActiveFocus();
    }
    function showSection(section) {
        settingsScroll.contentY = Math.max(0, Math.min(section.y, Math.max(0, settingsScroll.contentHeight - settingsScroll.height)));
        activeSection = section;
    }
    ColumnLayout {
        id: settingsHeader
        anchors.top: parent.top
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.margins: 28
        spacing: 14
        RowLayout {
            Layout.fillWidth: true
            Column {
                Layout.fillWidth: true
                spacing: 5
                PlainLabel {
                    text: I18n.t("Settings")
                    font.pixelSize: Tokens.fontSize(32)
                }
                PlainLabel {
                    text: I18n.t("Location, notifications and desktop effects")
                    color: Tokens.secondary
                    font.pixelSize: Tokens.fontSize(14)
                }
            }
            ActionButton {
                objectName: "closeEffects"
                iconName: "close"
                accessibleLabel: I18n.t("Close settings")
                onClicked: root.closeRequested()
            }
        }
        Flow {
            Layout.fillWidth: true
            Layout.preferredHeight: childrenRect.height
            spacing: 6
            ActionButton {
                objectName: "settingsApplicationSection"
                text: I18n.t("Application")
                selected: root.activeSection === applicationSection
                onClicked: root.showSection(applicationSection)
            }
            ActionButton {
                objectName: "settingsLocationSection"
                text: I18n.t("Location")
                selected: root.activeSection === locationSection
                onClicked: root.showSection(locationSection)
            }
            ActionButton {
                id: notificationsButton
                objectName: "settingsNotificationsSection"
                text: I18n.t("Notifications")
                selected: root.activeSection === notificationsSection
                onClicked: root.showSection(notificationsSection)
            }
            ActionButton {
                objectName: "settingsEffectsSection"
                text: I18n.t("Effects")
                selected: root.activeSection === effectsSection
                onClicked: root.showSection(effectsSection)
            }
            ActionButton {
                objectName: "settingsPreviewSection"
                text: I18n.t("Preview")
                selected: root.activeSection === previewSection
                onClicked: root.showSection(previewSection)
            }
            ActionButton {
                objectName: "settingsAppearanceSection"
                text: I18n.t("Appearance")
                selected: root.activeSection === appearanceSection
                onClicked: root.showSection(appearanceSection)
            }
            ActionButton {
                objectName: "settingsExperimentalSection"
                text: I18n.t("Experimental")
                selected: root.activeSection === experimentalSection
                onClicked: root.showSection(experimentalSection)
            }
        }
    }
    Flickable {
        id: settingsScroll
        objectName: "settingsScroll"
        anchors.top: settingsHeader.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.margins: 28
        anchors.topMargin: 18
        clip: true
        // Let the last section align at the top when selected, even when its
        // contents are shorter than the viewport.
        contentHeight: Math.max(body.height, experimentalSection.y + height)
        onContentYChanged: root.updateActiveSection()
        boundsBehavior: Flickable.StopAtBounds
        ScrollBar.vertical: ScrollBar {}
        ColumnLayout {
            id: body
            width: parent.width - 18
            spacing: 16
            PlainLabel {
                id: applicationSection
                text: I18n.t("Application")
                font.pixelSize: Tokens.fontSize(23)
                font.weight: Font.DemiBold
            }
            PlainLabel {
                text: I18n.t("Language")
                font.pixelSize: Tokens.fontSize(19)
                font.weight: Font.DemiBold
            }
            SettingsComboBox {
                objectName: "languageChoice"
                Layout.fillWidth: true
                model: I18n.languages
                textRole: "name"
                valueRole: "code"
                currentIndex: Math.max(0, I18n.languages.findIndex(entry => entry.code === I18n.preference))
                Accessible.name: I18n.t("Interface language")
                Accessible.description: I18n.t("System / Automatic follows your system language preferences.")
                onActivated: index => I18n.setPreference(I18n.languages[index].code)
            }
            UpdatePanel {
                Layout.fillWidth: true
                status: root.updateStatus
                enabled: root.serviceAvailable && !root.busy
                onCheckRequested: root.checkUpdatesRequested()
                onInstallRequested: root.installUpdateRequested()
            }
            PlainLabel {
                text: I18n.t("Application launcher")
                font.pixelSize: Tokens.fontSize(19)
                font.weight: Font.DemiBold
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Add A Weather App and its icon to your application menu. This opens the same app without using the bar widget.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            ActionButton {
                objectName: "installLauncher"
                text: root.launcherStatus === "installed" ? I18n.t("Launcher installed") : I18n.t("Install application launcher")
                enabled: root.serviceAvailable && !root.busy && root.launcherStatus !== "installed"
                onClicked: root.installLauncherRequested()
            }
            PlainLabel {
                objectName: "launcherStatus"
                Layout.fillWidth: true
                text: ({
                        ready: I18n.t("Installed for your user only. Keep this app in its current location."),
                        installed: I18n.t("Ready. Reopen your application menu and search for A Weather App."),
                        conflict: I18n.t("An existing launcher or icon differs. Nothing was overwritten. See the README launcher instructions to resolve it."),
                        unsupported_path: I18n.t("Move the app to a path without quotes, backslashes, dollar signs, percent signs, equals signs or backticks, then try again."),
                        failed: I18n.t("Could not install the launcher. Check your user application directory permissions and try again.")
                    })[root.launcherStatus]
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            RowLayout {
                id: locationSection
                Layout.fillWidth: true
                PlainLabel {
                    text: I18n.t("Location")
                    font.pixelSize: Tokens.fontSize(23)
                    font.weight: Font.DemiBold
                }
            }
            PlainLabel {
                objectName: "settingsLocation"
                Layout.fillWidth: true
                text: I18n.t("Viewing · %1").arg(root.location)
                font.pixelSize: Tokens.fontSize(18)
            }
            PlainLabel {
                Layout.fillWidth: true
                visible: root.hasSavedLocations
                text: I18n.t("Home · %1").arg(root.primaryLocation)
                color: Tokens.accent
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(15)
            }
            ActionButton {
                objectName: "manageSavedLocations"
                text: I18n.t("Manage saved locations")
                visible: root.hasSavedLocations
                onClicked: root.manageLocationsRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                visible: root.hasSavedLocations
                text: I18n.t("Search below to add and view a city. Choose Home in the locations list.")
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
            }
            PlainLabel {
                objectName: "locationMode"
                Layout.fillWidth: true
                text: root.locationSettings.mode === "zip" ? I18n.t("ZIP code · %1").arg(root.locationSettings.zip_code) : root.locationSettings.mode === "place" ? I18n.t("Selected city · %1").arg((root.locationSettings.country_code || I18n.t("Country unavailable"))) : root.locationSettings.mode === "auto" ? I18n.t("Local location") : root.locationSettings.mode === "custom" ? I18n.t("Custom location") : I18n.t("New York is the fallback. Use your current location, search for a city, or enter a ZIP code.")
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlaceSearch {
                id: citySearch
                Layout.fillWidth: true
                search: root.placeSearch
                selectionHint: root.hasSavedLocations ? I18n.t("Choose a result to add and view it. Home stays the same.") : I18n.t("Optional country code, such as DE or BR. Choose a result to change location.")
                serviceAvailable: root.serviceAvailable
                locationBusy: root.locationLocked
                onSearchRequested: values => root.placeSearchRequested(values)
                onCancelRequested: root.placeSearchCancelRequested()
                onLocationRequested: values => root.locationRequested(values)
            }
            PlainLabel {
                text: I18n.t("5-digit US ZIP code")
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            RowLayout {
                Layout.fillWidth: true
                TextField {
                    id: zipInput
                    objectName: "locationZip"
                    Layout.fillWidth: true
                    maximumLength: 5
                    validator: RegularExpressionValidator {
                        regularExpression: /[0-9]{5}/
                    }
                    Accessible.name: I18n.t("US ZIP code")
                    enabled: !root.locationLocked
                    inputMethodHints: Qt.ImhDigitsOnly
                    color: Tokens.foreground
                    font.pixelSize: Tokens.fontSize(16)
                    selectByMouse: true
                    background: Rectangle {
                        implicitHeight: 42
                        radius: 10
                        color: "#30435e72"
                        border.color: zipInput.activeFocus ? Tokens.accent : Tokens.border
                    }
                    onAccepted: root.applyZip()
                }
                ActionButton {
                    objectName: "applyLocationZip"
                    text: root.hasSavedLocations ? I18n.t("Add ZIP") : I18n.t("Apply")
                    primary: true
                    enabled: !root.locationLocked && /^[0-9]{5}$/.test(zipInput.text)
                    onClicked: root.applyZip()
                }
            }
            ActionButton {
                objectName: "useLocalLocation"
                text: I18n.t("Use local location")
                primary: root.locationSettings.mode === "default"
                enabled: !root.locationLocked
                onClicked: root.locationRequested({
                    mode: "auto"
                })
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Uses ipwho.is to estimate your city from your public IP. VPNs can affect accuracy.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
            }
            PlainLabel {
                objectName: "locationStatus"
                Layout.fillWidth: true
                visible: root.locationSettings.busy || root.locationSettings.error !== null
                text: root.locationSettings.busy ? I18n.t("Finding location and loading forecast…") : ({
                        lookup_failed: I18n.t("Location lookup failed. Try again."),
                        zip_not_found: I18n.t("ZIP code not found. Check the code and try again."),
                        zip_ambiguous: I18n.t("This ZIP code matches more than one location. Try another code."),
                        place_not_found: I18n.t("Selected city is no longer available. Search again."),
                        stale_selection: I18n.t("Search result expired. Search again."),
                        timeout: I18n.t("Location lookup timed out. Try again."),
                        state_io_failed: I18n.t("Location could not be saved. Try again."),
                        save_unconfirmed: I18n.t("Location changed, but saving could not be confirmed. Try again.")
                    })[root.locationSettings.error] || ""
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.accent
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            PlainLabel {
                text: I18n.t("Measurement units")
                font.pixelSize: Tokens.fontSize(18)
            }
            UnitsChoice {
                objectName: "settingsUnitsChoice"
                Layout.fillWidth: true
                compact: false
                units: root.controls.units || "F"
                automaticUnits: (root.controls.units_mode || "auto") === "auto"
                enabled: !root.busy && root.serviceAvailable
                onChosen: values => root.patch(values)
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Auto follows the location. Manual units apply throughout the app and are saved.")
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            PlainLabel {
                text: I18n.t("Wind speed units")
                font.pixelSize: Tokens.fontSize(18)
            }
            SettingsComboBox {
                objectName: "windUnitsChoice"
                Layout.fillWidth: true
                model: [
                    {
                        label: I18n.t("Auto (mph with °F, km/h with °C)"),
                        value: "auto"
                    },
                    {
                        label: I18n.t("Miles per hour (mph)"),
                        value: "mph"
                    },
                    {
                        label: I18n.t("Kilometres per hour (km/h)"),
                        value: "km/h"
                    },
                    {
                        label: I18n.t("Metres per second (m/s)"),
                        value: "m/s"
                    },
                    {
                        label: I18n.t("Knots (kn)"),
                        value: "kn"
                    }
                ]
                textRole: "label"
                valueRole: "value"
                currentIndex: Math.max(0, model.findIndex(choice => choice.value === (root.controls.wind_units || "auto")))
                enabled: !root.busy && root.serviceAvailable
                onActivated: root.patch({
                    wind_units: currentValue
                })
                Accessible.name: I18n.t("Wind speed units")
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            NotificationsPanel {
                id: notificationsSection
                Layout.fillWidth: true
                notifications: root.notifications
                officialWarnings: root.officialWarnings
                primaryName: root.primaryLocation
                settingsVisible: root.visible && root.presentationActive
                timezone: root.timezone
                busy: root.busy
                serviceAvailable: root.serviceAvailable
                actionError: root.actionError.indexOf("Notification settings") === 0 ? root.actionError : ""
                onPatch: values => root.notificationsPatch(values)
                onPauseRequested: root.notificationPauseRequested()
                onResumeRequested: root.notificationResumeRequested()
                onQuitRequested: root.quitRequested()
                onWarningPatch: values => root.warningPatch(values)
                onWarningPauseRequested: root.warningPauseRequested()
                onWarningResumeRequested: root.warningResumeRequested()
                onWarningDetailRequested: reference => root.warningDetailRequested(reference)
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            PlainLabel {
                id: effectsSection
                text: I18n.t("Desktop effects")
                font.pixelSize: Tokens.fontSize(23)
                font.weight: Font.DemiBold
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Optional and off until you start them. The forecast app works without desktop effects.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(14)
            }
            PlainLabel {
                objectName: "effectsCompatibility"
                Layout.fillWidth: true
                text: root.compatibilityText
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: root.setup.status === "ready" ? Tokens.accent : Tokens.foreground
                font.pixelSize: Tokens.fontSize(16)
            }
            ActionButton {
                objectName: "checkEffects"
                text: root.setup.status === "unchecked" ? I18n.t("Check compatibility") : I18n.t("Check again")
                enabled: root.serviceAvailable && !root.busy && !root.effectsRunning
                onClicked: root.checkRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Checks the current Hyprland session, monitor and native components without starting effects. Activation checks them again.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(13)
            }
            PlainLabel {
                text: I18n.t("Monitor")
                font.pixelSize: Tokens.fontSize(16)
            }
            SettingsComboBox {
                id: monitorChoice
                enabledOptions: [true].concat(root.setup.outputs.map(row => row.enabled))
                objectName: "effectsMonitor"
                Layout.fillWidth: true
                enabled: !root.busy && !root.effectsRunning && root.serviceAvailable
                model: [I18n.t("Automatic · app's monitor")].concat(root.setup.outputs.map(row => row.name + " · " + Math.round(row.width) + " × " + Math.round(row.height) + (row.enabled ? "" : I18n.t(" · Disabled"))))
                currentIndex: root.setup.output_selection === "automatic" ? 0 : root.setup.selected_output === null ? -1 : root.setup.outputs.findIndex(row => row.name === root.setup.selected_output) + 1
                displayText: currentIndex < 0 ? I18n.t("Choose a monitor") : currentText
                Accessible.name: I18n.t("Desktop effects monitor")
                onActivated: index => {
                    if (index === 0)
                        root.outputRequested("");
                    else if (root.setup.outputs[index - 1].enabled)
                        root.outputRequested(root.setup.outputs[index - 1].name);
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Desktop renderer · %1").arg(root.rendererStatus)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(14)
            }
            RowLayout {
                Layout.fillWidth: true
                ActionButton {
                    objectName: "startPreview"
                    Layout.fillWidth: true
                    iconName: "play"
                    text: I18n.t("Start 5-minute preview")
                    primary: true
                    enabled: root.canStart && !root.busy && !root.effectsRunning
                    onClicked: root.startRequested()
                }
                ActionButton {
                    objectName: "stopEffects"
                    iconName: "stop"
                    text: I18n.t("Stop")
                    enabled: root.serviceAvailable && root.effectsRunning
                    onClicked: root.stopRequested()
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: !root.serviceAvailable ? I18n.t("Weather service unavailable. Close and reopen to reconnect.") : root.persistent ? I18n.t("Live desktop stays on when you close the window. Use Stop or the Live desktop button to turn it off.") : !root.forecastAvailable ? I18n.t("Choose a location and load its forecast to enable desktop effects.") : I18n.t("Previews stop after five minutes. Live desktop in the header keeps live weather on until you stop it.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(14)
            }
            PlainLabel {
                objectName: "effectsActionError"
                visible: root.actionError !== ""
                Layout.fillWidth: true
                text: I18n.t(root.actionError)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.accent
                font.pixelSize: Tokens.fontSize(14)
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            PlainLabel {
                id: previewSection
                text: I18n.t("Preview")
                font.pixelSize: Tokens.fontSize(23)
                font.weight: Font.DemiBold
            }
            PlainLabel {
                text: I18n.t("Weather source")
                font.pixelSize: Tokens.fontSize(16)
            }
            ChoiceControl {
                Layout.fillWidth: true
                testName: "weatherSource"
                choices: [
                    {
                        label: I18n.t("Live"),
                        value: "live"
                    },
                    {
                        label: I18n.t("Manual"),
                        value: "manual"
                    }
                ]
                value: root.persistent ? "live" : root.controls.mode
                enabled: !root.busy && !root.persistent
                onChosen: v => root.patch({
                        mode: v
                    })
            }
            PlainLabel {
                text: I18n.t("Preview condition")
                font.pixelSize: Tokens.fontSize(16)
            }
            SettingsComboBox {
                id: conditionChoice
                objectName: "previewCondition"
                Layout.fillWidth: true
                enabled: root.controls.mode === "manual" && !root.busy && !root.persistent
                model: [I18n.t("Clear"), I18n.t("Partly cloudy"), I18n.t("Cloudy"), I18n.t("Fog"), I18n.t("Drizzle"), I18n.t("Rain"), I18n.t("Snow"), I18n.t("Sleet"), I18n.t("Thunderstorm")]
                property var values: ["clear", "partly_cloudy", "cloudy", "fog", "drizzle", "rain", "snow", "sleet", "thunderstorm"]
                currentIndex: Math.max(0, values.indexOf(root.controls.manual.condition))
                Accessible.name: I18n.t("Preview condition")
                onActivated: index => root.patch({
                        manual: {
                            condition: values[index]
                        }
                    })
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Manual previews do not change the live forecast. Live desktop follows the location you are viewing.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(13)
                color: Tokens.secondary
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            PlainLabel {
                id: appearanceSection
                text: I18n.t("Appearance")
                font.pixelSize: Tokens.fontSize(23)
                font.weight: Font.DemiBold
            }
            ActionButton {
                objectName: "openDashboardEditor"
                text: I18n.t("Customize dashboard")
                enabled: root.dashboardAvailable
                onClicked: root.dashboardRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Choose sections, their order, and the values shown in your forecast.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            PlainLabel {
                text: I18n.t("Text size")
                font.pixelSize: Tokens.fontSize(16)
            }
            SettingsComboBox {
                objectName: "textSizePreference"
                Layout.fillWidth: true
                Accessible.name: I18n.t("Text size")
                model: [I18n.t("Standard · 100%"), I18n.t("Large · 125%"), I18n.t("Larger · 150%")]
                currentIndex: [1, 1.25, 1.5].indexOf(root.appearance.text_scale)
                enabled: root.serviceAvailable && !root.busy
                onActivated: index => {
                    root.appearanceRequested({
                        text_scale: [1, 1.25, 1.5][index]
                    });
                    currentIndex = Qt.binding(() => [1, 1.25, 1.5].indexOf(root.appearance.text_scale));
                }
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "highContrastPreference"
                title: I18n.t("High contrast")
                caption: I18n.t("Solid panels and stronger text and borders.")
                checked: root.appearance.high_contrast
                locked: !root.serviceAvailable || root.busy
                optimistic: false
                onToggled: value => root.appearanceRequested({
                        high_contrast: value
                    })
            }
            PlainLabel {
                Layout.fillWidth: true
                visible: root.appearance.error !== null
                text: root.appearance.error === "save_unconfirmed" ? I18n.t("Readability changed, but saving was not confirmed. Choose the setting again to retry.") : I18n.t("Saved readability preferences could not be read. Defaults are shown; choosing a setting saves them again.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.accent
            }
            PlainLabel {
                text: I18n.t("Intensity")
                font.pixelSize: Tokens.fontSize(16)
            }
            ChoiceControl {
                Layout.fillWidth: true
                testName: "intensity"
                choices: [
                    {
                        label: I18n.t("Subtle"),
                        value: "subtle"
                    },
                    {
                        label: I18n.t("Normal"),
                        value: "normal"
                    },
                    {
                        label: I18n.t("Immersive"),
                        value: "immersive"
                    }
                ]
                value: root.controls.strength
                enabled: !root.busy
                onChosen: v => root.patch({
                        strength: v
                    })
            }
            PlainLabel {
                text: I18n.t("Frame rate")
                font.pixelSize: Tokens.fontSize(16)
            }
            ChoiceControl {
                Layout.fillWidth: true
                testName: "fps"
                choices: [
                    {
                        label: I18n.t("15 FPS"),
                        value: 15
                    },
                    {
                        label: I18n.t("30 FPS"),
                        value: 30
                    },
                    {
                        label: I18n.t("60 FPS"),
                        value: 60
                    }
                ]
                value: root.controls.fps
                enabled: !root.busy
                onChosen: v => root.patch({
                        fps: v
                    })
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "windowPhysics"
                title: I18n.t("Window physics")
                caption: I18n.t("Precipitation collides with window edges.")
                checked: root.controls.window_physics
                locked: root.busy
                onToggled: v => root.patch({
                        window_physics: v
                    })
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "accumulation"
                title: I18n.t("Accumulation")
                caption: I18n.t("Subtle wetness and buildup on surfaces.")
                checked: root.controls.accumulation
                locked: root.busy
                onToggled: v => root.patch({
                        accumulation: v
                    })
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "lightning"
                title: I18n.t("Lightning flashes")
                caption: I18n.t("Off by default.")
                checked: root.controls.lightning_enabled
                locked: root.busy
                onToggled: v => root.patch({
                        lightning_enabled: v
                    })
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Window visual quality")
                font.pixelSize: Tokens.fontSize(16)
            }
            SettingsComboBox {
                objectName: "visualQualityChoice"
                Layout.fillWidth: true
                model: [I18n.t("Auto"), I18n.t("Full"), I18n.t("Economical"), I18n.t("Static")]
                currentIndex: Math.max(0, ["auto", "full", "economical", "static"].indexOf(root.controls.visual_quality || "auto"))
                enabled: !root.busy
                onActivated: index => root.patch({
                        visual_quality: ["auto", "full", "economical", "static"][index]
                    })
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Current mode: %1%2. Auto uses full visuals on supported hardware and a static sky and wind on software graphics. Economical reduces animation cadence and wind trail density. Unsupported graphics always use Static. Reduced motion always takes priority.").arg(root.effectiveVisualQuality.charAt(0).toUpperCase()).arg(root.effectiveVisualQuality.slice(1))
                wrapMode: Text.Wrap
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(12)
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "reducedMotion"
                title: I18n.t("Reduced motion")
                caption: I18n.t("Stops precipitation and freezes the sky.")
                checked: root.controls.reduced_motion
                locked: root.busy
                onToggled: v => root.patch({
                        reduced_motion: v
                    })
            }
            ToggleControl {
                Layout.fillWidth: true
                testName: "pauseFullscreen"
                title: I18n.t("Pause in fullscreen")
                caption: I18n.t("Safety pause is always enabled for fullscreen applications.")
                checked: true
                locked: true
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Weather service · %1").arg(root.serviceStatus)
                color: Tokens.accent
                font.pixelSize: Tokens.fontSize(14)
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Sky follows the current forecast. Effects use the selected weather source.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(14)
            }
            Rectangle {
                Layout.fillWidth: true
                Layout.preferredHeight: 1
                color: Tokens.border
            }
            PlainLabel {
                id: experimentalSection
                objectName: "experimentalSection"
                text: I18n.t("Experimental")
                font.pixelSize: Tokens.fontSize(23)
                font.weight: Font.DemiBold
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Features in development, available here to try as we refine them.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            PlainLabel {
                text: I18n.t("Forecast tools")
                font.pixelSize: Tokens.fontSize(19)
                font.weight: Font.DemiBold
            }
            ActionButton {
                objectName: "openOutdoorPlanner"
                text: I18n.t("Outdoor planner")
                enabled: root.plannerAvailable
                onClicked: root.outdoorRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Compare outdoor windows using your temperature, rain and wind preferences.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            ActionButton {
                objectName: "openForecastChanges"
                text: I18n.t("Forecast changes")
                enabled: root.forecastChangesAvailable
                onClicked: root.forecastChangesRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Compare the latest forecast with the one you previously viewed.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
            ActionButton {
                objectName: "openForecastShare"
                text: I18n.t("Share forecast")
                iconName: "share"
                enabled: root.shareAvailable
                onClicked: root.shareRequested()
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Copy a forecast summary or save an image.")
                wrapMode: Text.Wrap
                elide: Text.ElideNone
                font.pixelSize: Tokens.fontSize(14)
                color: Tokens.secondary
            }
        }
    }
}
