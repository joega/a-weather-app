import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "backend"
import "Forecast.js" as Forecast

QtObject {
    id: root
    property var graphicsCapabilities: null
    required property var weatherTransport
    property var mapTiles: null
    property string units: bridge.snapshot ? bridge.snapshot.controls.units : "F"
    readonly property string windUnits: bridge.snapshot ? bridge.snapshot.controls.wind_units : "auto"
    readonly property bool automaticUnits: bridge.snapshot ? bridge.snapshot.controls.units_mode === "auto" : true
    property string briefingPeriod: "today"
    readonly property var briefingRows: bridge.snapshot ? bridge.snapshot.briefing : []
    readonly property var selectedBriefing: briefingRows.find(row => row.period === briefingPeriod) || briefingRows[0] || null
    property Item settingsReturnFocus: null
    property bool effectsOpen: false
    onEffectsOpenChanged: {
        Qt.callLater(showUpdateNotice);
        if (!effectsOpen)
            Qt.callLater(() => {
                const target = root.settingsReturnFocus || settingsButton;
                if (target && target.visible && target.enabled)
                    target.forceActiveFocus();
                root.settingsReturnFocus = null;
            });
    }
    function openSettings(locationSearch) {
        if (!effectsOpen)
            settingsReturnFocus = window.activeFocusItem;
        effectsOpen = true;
        if (locationSearch)
            Qt.callLater(() => {
                if (root.effectsOpen)
                    effects.focusLocation();
            });
    }
    property bool initialLocationChecked: false
    readonly property var updateStatus: bridge.snapshot ? bridge.snapshot.update : Forecast.updateStatus(null)
    property string shownUpdateVersion: ""
    property bool updateNoticeActive: false
    onUpdateStatusChanged: Qt.callLater(showUpdateNotice)
    function showUpdateNotice() {
        if (!window.visible || root.effectsOpen || !bridge.available || bridge.busy || root.updateStatus.state !== "updated" || root.shownUpdateVersion === root.updateStatus.installed)
            return;
        if (bridge.send("acknowledge_update", {
            installed: root.updateStatus.installed
        })) {
            root.shownUpdateVersion = root.updateStatus.installed;
            root.updateNoticeActive = true;
            updateNoticeTimer.restart();
        }
    }
    property Timer updateNoticeTimer: Timer {
        interval: 5000
        onTriggered: root.updateNoticeActive = false
    }
    readonly property bool hasMapLocation: bridge.snapshot !== null && bridge.snapshot.location_settings.mode !== "default"
    readonly property bool mapsNearViewport: mapSection.height > 100 && mapSection.y + mapSection.height + 18 > forecastScroll.flickable.contentY - 64 && mapSection.y + 18 < forecastScroll.flickable.contentY + forecastScroll.height + 64
    readonly property bool mapActive: window.visible && window.visibility !== Window.Hidden && window.visibility !== Window.Minimized && !root.effectsOpen && !details.visible && bridge.available && root.hasMapLocation && !bridge.snapshot.location_settings.busy && root.mapsNearViewport
    onMapActiveChanged: {
        if (mapActive && !bridge.mapWanted)
            bridge.openMap();
        else if (!mapActive && bridge.mapWanted)
            bridge.closeMap();
    }
    readonly property bool liveDesktop: bridge.snapshot !== null && bridge.snapshot.effect_persistent && bridge.snapshot.effect_status !== "stopped"
    readonly property bool watchingPrecipitation: bridge.snapshot !== null && bridge.snapshot.notifications.settings.enabled
    function dismissWindow() {
        if ((root.liveDesktop || root.watchingPrecipitation) && bridge.available) {
            root.effectsOpen = false;
            window.hide();
        } else
            bridge.shutdown();
    }
    property bool alertsExpanded: false
    readonly property var activeAlerts: bridge.snapshot && bridge.snapshot.alerts ? bridge.snapshot.alerts.items : []
    readonly property string alertsStatusText: {
        if (!bridge.snapshot || !bridge.snapshot.alerts)
            return "Alert status unavailable";
        let a = bridge.snapshot.alerts;
        if (a.status === "not_supported_here")
            return "Official alerts are not supported here";
        if (a.freshness === "pending")
            return "Checking official alerts…";
        if (a.freshness === "stale")
            return a.refreshing ? "Cached alert feed · checking for updates…" : "Cached alert feed · current alert status unavailable";
        if (a.status === "unavailable")
            return "Alert status unavailable";
        return a.items.length === 0 ? (a.source === "National Weather Service" ? "No active NWS alerts reported" : "Alert status unavailable") : "";
    }
    property var forecast: bridge.snapshot ? bridge.snapshot.forecast : null
    property var current: forecast ? forecast.current : null
    property var atmosphere: bridge.snapshot ? bridge.snapshot.atmosphere : null
    property string timezone: bridge.snapshot ? bridge.snapshot.timezone : "UTC"
    property string location: bridge.snapshot ? bridge.snapshot.location : "Loading location…"
    readonly property string city: location.indexOf(", ") < 0 ? location : location.substring(0, location.lastIndexOf(", "))
    readonly property string region: {
        let suffix = location.indexOf(", ") < 0 ? "" : location.substring(location.lastIndexOf(", ") + 2);
        return suffix === "MA" ? "Massachusetts" : suffix;
    }
    property var days: forecast ? forecast.daily : []
    property var hours: forecast ? forecast.hourly.slice(0, 24) : []
    onLocationChanged: {
        if (details)
            details.close();
        if (mapActive && bridge.mapWanted) {
            bridge.closeMap();
            bridge.openMap();
        }
    }
    property var controls: bridge.snapshot ? bridge.snapshot.controls : ({
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
    property string freshness: {
        if (bridge.error)
            return bridge.error;
        if (bridge.snapshot && bridge.snapshot.source.refreshing)
            return "Refreshing live forecast…";
        if (bridge.snapshot && bridge.snapshot.location_settings.mode === "default" && !forecast)
            return "Choose a city, your current location, or a ZIP code in Settings";
        if (!bridge.snapshot || !forecast)
            return "Live forecast unavailable · Refresh to try again";
        let s = bridge.snapshot.source;
        let age = s.age_seconds === null ? "" : (s.age_seconds < 60 ? "just now" : Math.floor(s.age_seconds / 60) + " min ago");
        return (s.freshness === "fresh" ? "Weather data from " : s.freshness === "invalid_future" ? "Invalid forecast timestamp · " : s.freshness.charAt(0).toUpperCase() + s.freshness.slice(1) + " forecast · ") + age + (s.error ? " · Refresh failed" : "");
    }
    property QtObject backend: Bridge {
        id: bridge
        weatherTransport: root.weatherTransport
        onBusyChanged: Qt.callLater(root.showUpdateNotice)
        presentationActive: window.visible && window.visibility !== Window.Hidden && window.visibility !== Window.Minimized
        onClosed: exitCode => Qt.exit(exitCode)
        onToggleWindow: {
            if (window.visible)
                root.dismissWindow();
            else {
                window.show();
                window.raise();
                window.requestActivate();
            }
        }
        onSnapshotChanged: {
            if (snapshot && !root.initialLocationChecked) {
                root.initialLocationChecked = true;
                if (snapshot.location_settings.mode === "default")
                    root.effectsOpen = true;
            }
        }
    }
    property QtObject weatherWindow: ApplicationWindow {
        id: window
        objectName: "weatherWindow"
        title: "A Weather App"
        width: 1200
        height: 850
        minimumWidth: 700
        minimumHeight: 650
        visible: true
        color: "#263f55"
        onActiveFocusItemChanged: Qt.callLater(() => {
            let item = window.activeFocusItem;
            if (!item)
                return;
            if (details.visible) {
                details.revealFocus(item);
                return;
            }
            if (root.effectsOpen) {
                effects.revealFocus(item);
                return;
            }
            let flick = forecastScroll.contentItem, ancestor = item;
            if (!flick || !flick.contentItem)
                return;
            while (ancestor && ancestor !== flick.contentItem)
                ancestor = ancestor.parent;
            if (!ancestor)
                return;
            let point = item.mapToItem(flick.contentItem, 0, 0), next = flick.contentY;
            if (point.y < next + 16)
                next = point.y - 16;
            else if (point.y + item.height > next + flick.height - 16)
                next = point.y + item.height - flick.height + 16;
            flick.contentY = Math.max(0, Math.min(next, Math.max(0, flick.contentHeight - flick.height)));
        })
        onVisibleChanged: {
            if (!visible)
                root.updateNoticeActive = false;
            else
                Qt.callLater(() => {
                    root.showUpdateNotice();
                    if (forecastScroll.contentItem)
                        forecastScroll.flickable.contentY = 0;
                });
        }
        onClosing: event => {
            event.accepted = false;
            root.dismissWindow();
        }
        Shortcut {
            sequence: "Ctrl+L"
            enabled: !details.visible
            onActivated: root.openSettings(true)
        }
        Shortcut {
            sequence: "Escape"
            enabled: root.effectsOpen
            onActivated: root.effectsOpen = false
        }
        Atmosphere {
            id: forecastAtmosphere
            objectName: "forecastAtmosphere"
            anchors.fill: parent
            presentationActive: bridge.available && window.visible && !root.effectsOpen && !details.visible
            condition: root.current ? root.current.condition : "unknown"
            isDay: root.current ? root.current.is_day : true
            cloudCover: root.atmosphere ? root.atmosphere.cloud_cover : 0.5
            fogDensity: root.atmosphere ? root.atmosphere.fog_density : 0
            sunElevation: root.atmosphere ? root.atmosphere.sun_elevation : 20
            sunAzimuth: root.atmosphere ? root.atmosphere.sun_azimuth : 180
            wind: root.atmosphere ? root.atmosphere.wind_x : 0
            windSpeed: root.current && root.current.wind_speed_m_s !== null ? root.current.wind_speed_m_s : 0
            rainAmount: root.atmosphere ? root.atmosphere.rain_intensity : 0
            snowAmount: root.atmosphere ? root.atmosphere.snow_intensity : 0
            graphicsCapabilities: root.graphicsCapabilities
            visualQuality: root.controls.visual_quality || "auto"
            reducedMotion: root.controls.reduced_motion
            lightningEnabled: root.atmosphere ? root.atmosphere.lightning_enabled : false
        }
        ScrollView {
            id: forecastScroll
            readonly property Flickable flickable: contentItem as Flickable
            objectName: "forecastScroll"
            enabled: !root.effectsOpen
            anchors.fill: parent
            clip: true
            contentWidth: availableWidth
            ColumnLayout {
                width: window.width - 56
                x: 28
                y: 18
                spacing: 16
                Item {
                    Layout.fillWidth: true
                    Layout.preferredHeight: headerBody.implicitHeight
                    Rectangle {
                        x: -28
                        y: -18
                        width: window.width
                        height: parent.height + 180
                        gradient: Gradient {
                            GradientStop {
                                position: 0
                                color: "#800b1c30"
                            }
                            GradientStop {
                                position: 0.66
                                color: "#800b1c30"
                            }
                            GradientStop {
                                position: 1
                                color: "transparent"
                            }
                        }
                    }
                    ColumnLayout {
                        id: headerBody
                        readonly property bool actionsBelowLocation: window.width < 850 || locationFont.advanceWidth(root.city) + headerActions.implicitWidth + 20 > width
                        FontMetrics {
                            id: locationFont
                            font.pixelSize: 38
                            font.family: "sans-serif"
                        }
                        width: parent.width
                        spacing: 16
                        Item {
                            Layout.fillWidth: true
                            Layout.preferredHeight: Math.max(conditions.height, headerActions.y + headerActions.height + (updatedNotice.visible ? updatedNotice.height + 8 : 0), headerAlerts.visible ? headerAlerts.y + headerAlerts.height : 0)
                            RowLayout {
                                id: headerActions
                                objectName: "headerActions"
                                anchors.right: parent.right
                                anchors.top: parent.top
                                anchors.topMargin: headerBody.actionsBelowLocation ? locationHeading.height + regionHeading.height + 12 : 0
                                ActionButton {
                                    objectName: "refreshForecast"
                                    iconName: "refresh"
                                    accessibleLabel: "Refresh forecast"
                                    enabled: !bridge.busy
                                    onClicked: bridge.send("refresh")
                                }
                                UnitsChoice {
                                    objectName: "unitsChoice"
                                    units: root.units
                                    automaticUnits: root.automaticUnits
                                    enabled: bridge.available && !bridge.busy
                                    onChosen: values => bridge.send("set_controls", values)
                                }
                                ActionButton {
                                    objectName: "openLocation"
                                    text: "Location"
                                    accessibleLabel: "Choose location (Ctrl+L)"
                                    onClicked: root.openSettings(true)
                                }
                                ActionButton {
                                    id: settingsButton
                                    objectName: "openEffects"
                                    iconName: "sliders"
                                    text: "Settings"
                                    onClicked: root.openSettings(false)
                                }
                                ActionButton {
                                    objectName: "liveDesktop"
                                    text: root.liveDesktop ? "Live desktop · On" : "Live desktop"
                                    selected: root.liveDesktop
                                    enabled: bridge.available && !bridge.busy
                                    onClicked: {
                                        if (root.liveDesktop)
                                            bridge.send("stop_effects");
                                        else
                                            bridge.send("start_live_effects");
                                    }
                                }
                            }
                            GlassPanel {
                                id: updatedNotice
                                objectName: "updatedNotice"
                                anchors.right: parent.right
                                anchors.top: headerActions.bottom
                                anchors.topMargin: 8
                                width: Math.min(headerActions.width, updatedVersionNotice.implicitWidth + 24)
                                height: 34
                                visible: root.updateNoticeActive
                                PlainLabel {
                                    id: updatedVersionNotice
                                    objectName: "updatedVersionNotice"
                                    anchors.centerIn: parent
                                    text: "Updated to " + root.shownUpdateVersion + "."
                                    font.pixelSize: 13
                                }
                            }
                            Column {
                                id: conditions
                                spacing: 3
                                width: parent.width
                                PlainLabel {
                                    id: locationHeading
                                    objectName: "locationHeading"
                                    text: root.city
                                    font.pixelSize: 38
                                    wrapMode: Text.Wrap
                                    maximumLineCount: 3
                                    elide: Text.ElideRight
                                    width: headerBody.actionsBelowLocation ? parent.width : parent.width - headerActions.width - 20
                                }
                                PlainLabel {
                                    id: regionHeading
                                    text: root.region
                                    font.pixelSize: 19
                                    color: "#d4e3ee"
                                }
                                Item {
                                    width: 1
                                    height: headerBody.actionsBelowLocation ? headerActions.height + 12 + (updatedNotice.visible ? updatedNotice.height + 8 : 0) : 0
                                }
                                PlainLabel {
                                    objectName: "currentTemperature"
                                    text: root.current ? Forecast.temp(root.current.temperature_c, root.units) : "—°"
                                    font.pixelSize: 96
                                    font.weight: Font.Light
                                }
                                PlainLabel {
                                    text: root.current ? Forecast.title(root.current.condition) : "Forecast unavailable"
                                    font.pixelSize: 27
                                }
                                PlainLabel {
                                    width: window.width >= 850 && root.activeAlerts.length ? parent.width * 0.48 : parent.width
                                    text: "Feels like " + Forecast.temp(root.current ? root.current.apparent_temperature_c : null, root.units) + " · High " + Forecast.temp(root.days.length ? root.days[0].high_c : null, root.units) + " · Low " + Forecast.temp(root.days.length ? root.days[0].low_c : null, root.units)
                                    font.pixelSize: 18
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                            }
                            AlertsPanel {
                                id: headerAlerts
                                visible: root.activeAlerts.length > 0 && window.width >= 850
                                anchors.right: parent.right
                                anchors.top: headerActions.bottom
                                anchors.topMargin: 16 + (updatedNotice.visible ? updatedNotice.height + 8 : 0)
                                width: parent.width * 0.49
                                height: implicitHeight
                                alerts: root.activeAlerts
                                source: bridge.snapshot ? bridge.snapshot.alerts.source || "" : ""
                            }
                        }
                        AlertsPanel {
                            Layout.fillWidth: true
                            visible: root.activeAlerts.length > 0 && window.width < 850
                            alerts: root.activeAlerts
                            source: bridge.snapshot ? bridge.snapshot.alerts.source || "" : ""
                        }
                        RowLayout {
                            Layout.fillWidth: true
                            PlainLabel {
                                Layout.fillWidth: true
                                text: root.freshness
                                color: "#d4e3ee"
                                font.pixelSize: 13
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                objectName: "alertCoverageStatus"
                                visible: root.alertsStatusText !== ""
                                text: root.alertsStatusText
                                color: "#d4e3ee"
                                font.pixelSize: 12
                            }
                            PlainLabel {
                                visible: root.controls.mode === "manual" && !root.liveDesktop
                                text: "Manual effects preview · " + Forecast.title(root.controls.manual.condition)
                                font.pixelSize: 13
                                color: Tokens.accent
                            }
                        }
                        ColumnLayout {
                            visible: root.hours.length > 0 && bridge.snapshot !== null && ["fresh", "stale"].indexOf(bridge.snapshot.source.freshness) >= 0
                            Layout.fillWidth: true
                            spacing: 6
                            ChoiceControl {
                                visible: root.briefingRows.length > 0
                                Layout.maximumWidth: 420
                                Layout.fillWidth: true
                                testName: "briefingPeriod"
                                value: root.selectedBriefing ? root.selectedBriefing.period : ""
                                choices: root.briefingRows.map(row => ({
                                            label: ({
                                                    today: "Today",
                                                    tonight: "Tonight",
                                                    tomorrow: "Tomorrow"
                                                })[row.period],
                                            value: row.period
                                        }))
                                onChosen: value => root.briefingPeriod = value
                            }
                            PlainLabel {
                                objectName: "forecastBriefingRange"
                                visible: root.selectedBriefing !== null
                                Layout.fillWidth: true
                                text: root.selectedBriefing ? (bridge.snapshot.source.freshness === "stale" ? "Cached outlook · " : "") + root.selectedBriefing.range_label : ""
                                color: Tokens.secondary
                                font.pixelSize: 12
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                objectName: "forecastOutlook"
                                Layout.fillWidth: true
                                text: root.selectedBriefing ? Forecast.briefingText(root.selectedBriefing, root.units, root.windUnits) : Forecast.outlook(root.hours, root.units, root.windUnits)
                                font.pixelSize: 18
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                        }
                    }
                }
                UpdatePanel {
                    objectName: "updateNotice"
                    Layout.fillWidth: true
                    status: bridge.snapshot ? bridge.snapshot.update : Forecast.updateStatus(null)
                    visible: ["available", "publishing", "failed", "downloading", "verifying", "restarting", "rolled_back"].indexOf(status.state) >= 0
                    enabled: bridge.available && !bridge.busy
                    onCheckRequested: bridge.send("check_updates")
                    onInstallRequested: bridge.send("install_update")
                }
                HourlyPanel {
                    Layout.fillWidth: true
                    hours: root.hours
                    units: root.units
                    timezone: root.timezone
                    onHourSelected: hour => details.showHour(hour)
                }
                GridLayout {
                    id: forecastCards
                    Layout.fillWidth: true
                    objectName: "forecastCards"
                    columns: window.width < 1200 || root.days.length === 0 ? 1 : 2
                    columnSpacing: 16
                    rowSpacing: 16
                    DailyPanel {
                        id: daily
                        Layout.fillWidth: true
                        Layout.preferredWidth: forecastCards.columns === 1 ? forecastCards.width : forecastCards.width * 0.54
                        Layout.fillHeight: true
                        days: root.days
                        units: root.units
                        onDaySelected: day => details.showDay(day)
                    }
                    MetricsPanel {
                        id: metrics
                        objectName: "currentMetrics"
                        onMetricSelected: metric => {
                            if (metric === "daylight") {
                                if (root.days.length)
                                    details.showDay(root.days[0]);
                            } else
                                details.showMetric(metric);
                        }
                        windUnits: root.windUnits
                        Layout.fillWidth: true
                        Layout.preferredWidth: forecastCards.width * 0.44
                        Layout.fillHeight: true
                        current: root.current
                        day: root.days.length ? root.days[0] : null
                        units: root.units
                        timezone: root.timezone
                    }
                }
                WeatherMap {
                    id: mapSection
                    tileClient: root.mapTiles
                    Layout.fillWidth: true
                    mapState: bridge.weatherMap
                    location: root.city
                    units: root.units
                    windUnits: root.windUnits
                    hasLocation: root.hasMapLocation
                    active: root.mapActive
                    visualQuality: forecastAtmosphere.effectiveQuality
                    reducedMotion: root.controls.reduced_motion
                    viewportTop: forecastScroll.flickable.contentY - (mapSection.y + 18)
                    viewportHeight: forecastScroll.height
                }
                AirQualityPanel {
                    Layout.fillWidth: true
                    airQuality: bridge.snapshot ? bridge.snapshot.air_quality : Forecast.airQuality()
                }
                PlainLabel {
                    objectName: "sourceAttribution"
                    Layout.fillWidth: true
                    horizontalAlignment: Text.AlignRight
                    text: bridge.snapshot ? bridge.snapshot.source.attribution + (bridge.snapshot.alerts.source ? " · Alerts: " + bridge.snapshot.alerts.source : "") : "Forecast: Open-Meteo"
                    color: Tokens.secondary
                    font.pixelSize: 11
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                Item {
                    Layout.preferredHeight: 24
                }
            }
        }
        ForecastDetails {
            id: details
            windUnits: root.windUnits
            forecast: root.forecast
            units: root.units
            freshness: root.freshness
        }
        Rectangle {
            anchors.fill: parent
            visible: root.effectsOpen
            color: "#650b1c29"
            MouseArea {
                anchors.fill: parent
                onClicked: root.effectsOpen = false
            }
        }
        EffectsDrawer {
            id: effects
            effectiveVisualQuality: forecastAtmosphere.effectiveQuality
            enabled: !bridge.closing
            objectName: "effectsDrawer"
            visible: root.effectsOpen
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.margins: 18
            width: Math.min(610, window.width - 36)
            onVisibleChanged: if (visible)
                forceActiveFocus()
            controls: root.controls
            location: root.location
            busy: bridge.busy
            forecastAvailable: root.forecast !== null
            locationSettings: bridge.snapshot ? bridge.snapshot.location_settings : ({
                    mode: "default",
                    zip_code: null,
                    busy: false,
                    error: null
                })
            placeSearch: bridge.snapshot ? bridge.snapshot.place_search : ({
                    generation: 0,
                    status: "idle",
                    results: [],
                    error: null
                })
            setup: bridge.snapshot ? bridge.snapshot.effects_setup : ({
                    status: "unchecked",
                    reason: "not_checked",
                    outputs: [],
                    selected_output: null
                })
            notifications: bridge.snapshot ? bridge.snapshot.notifications : Forecast.notifications()
            timezone: root.timezone
            actionError: bridge.error
            canStart: bridge.available && root.forecast !== null && bridge.snapshot !== null && bridge.snapshot.effect_status !== "cleanup_failed" && setup.status === "ready"
            serviceAvailable: bridge.available
            effectsRunning: bridge.snapshot !== null && bridge.snapshot.effect_status !== "stopped"
            persistent: root.liveDesktop
            serviceStatus: bridge.disconnected ? (root.forecast ? "Disconnected · cached forecast" : "Disconnected · forecast unavailable") : root.forecast ? (bridge.snapshot.source.freshness === "fresh" ? "Ready" : "Forecast " + bridge.snapshot.source.freshness) : "Unavailable"
            rendererStatus: {
                if (bridge.disconnected)
                    return bridge.snapshot && bridge.snapshot.effect_status !== "stopped" ? "Status unknown · cleanup pending" : "Unavailable · close and reopen";
                if (!bridge.snapshot || bridge.snapshot.effect_status === "stopped")
                    return "Stopped";
                if (bridge.snapshot.effect_status === "running") {
                    if (root.liveDesktop)
                        return "Live desktop · On";
                    let seconds = Math.ceil(bridge.snapshot.effect_remaining_seconds);
                    return "Running · " + Math.floor(seconds / 60) + ":" + (seconds % 60 < 10 ? "0" : "") + (seconds % 60) + " remaining";
                }
                return bridge.snapshot.effect_status === "cleanup_failed" ? "Cleanup needs attention" : "Starting…";
            }
            onCloseRequested: root.effectsOpen = false
            onPatch: values => bridge.send("set_controls", values)
            onLocationRequested: values => bridge.send("set_location", values)
            onPlaceSearchRequested: values => bridge.send("search_places", values)
            onPlaceSearchCancelRequested: bridge.send("cancel_place_search")
            onStartRequested: bridge.send("start_effects")
            onStopRequested: bridge.send("stop_effects")
            onCheckRequested: bridge.send("check_effects")
            onOutputRequested: output => bridge.send("select_output", {
                    output: output
                })
            onNotificationsPatch: values => bridge.send("set_notifications", values)
            onNotificationPauseRequested: bridge.send("snooze_notifications")
            onNotificationResumeRequested: bridge.send("resume_notifications")
            launcherStatus: bridge.snapshot ? bridge.snapshot.launcher_status : "ready"
            updateStatus: bridge.snapshot ? bridge.snapshot.update : Forecast.updateStatus(null)
            onCheckUpdatesRequested: bridge.send("check_updates")
            onInstallUpdateRequested: bridge.send("install_update")
            onInstallLauncherRequested: bridge.send("install_launcher")
            onQuitRequested: bridge.shutdown()
        }
    }
}
