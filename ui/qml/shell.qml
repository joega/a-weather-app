import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "backend"
import "Forecast.js" as Forecast
import "Dashboard.js" as Dashboard
import "Changes.js" as Changes

QtObject {
    id: root
    property var graphicsCapabilities: null
    required property var weatherTransport
    property var mapTiles: null
    property var radarImages: null
    property var windowActivation: null
    property string units: bridge.snapshot ? bridge.snapshot.controls.units : "F"
    readonly property string windUnits: bridge.snapshot ? bridge.snapshot.controls.wind_units : "auto"
    readonly property bool automaticUnits: bridge.snapshot ? bridge.snapshot.controls.units_mode === "auto" : true
    property string briefingPeriod: "today"
    readonly property var briefingRows: bridge.snapshot ? bridge.snapshot.briefing : []
    readonly property var selectedBriefing: briefingRows.find(row => row.period === briefingPeriod) || briefingRows[0] || null
    readonly property var savedLocations: bridge.snapshot ? bridge.snapshot.saved_locations : null
    readonly property var viewedPlace: savedLocations ? savedLocations.items.find(row => row.id === savedLocations.viewed) : null
    readonly property var primaryPlace: savedLocations ? savedLocations.items.find(row => row.id === savedLocations.primary) : null
    readonly property bool primaryForecastAvailable: savedLocations ? savedLocations.primary_forecast_available : root.forecast !== null
    readonly property string primaryName: primaryPlace ? Forecast.savedName(primaryPlace) : root.location
    readonly property string primaryTimezone: primaryPlace ? primaryPlace.timezone : root.timezone
    property var dashboardPreferences: Dashboard.defaults()
    property var appearance: Forecast.appearance()
    onAppearanceChanged: {
        Tokens.textScale = appearance.text_scale;
        Tokens.highContrast = appearance.high_contrast;
        Qt.callLater(() => {
            if (root.effectsOpen && window.activeFocusItem)
                effects.revealFocus(window.activeFocusItem);
        });
    }
    readonly property bool compactDashboard: dashboardPreferences.density === "compact"
    property bool astronomyOpen: false
    property bool shareOpen: false
    property Item shareReturnFocus: null
    function openShare() {
        if (shareOpen)
            return;
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (astronomyOpen)
            closeAstronomy();
        if (changesOpen)
            closeChanges();
        if (dashboardOpen)
            closeDashboard();
        if (outdoorOpen)
            closeOutdoor();
        shareReturnFocus = window.activeFocusItem;
        details.close();
        shareOpen = true;
    }
    function closeShare() {
        if (forecastSaveDialog.item)
            forecastSaveDialog.item.close();
        if (shareLoader.item)
            shareLoader.item.cancelExport();
        shareOpen = false;
        const target = shareReturnFocus;
        shareReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
        else if (window.visible)
            forecastScroll.forceActiveFocus();
    }
    property bool airOutlookOpen: false
    property Item airOutlookReturnFocus: null
    function openAirOutlook() {
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            return;
        if (precipitationOpen)
            closePrecipitation();
        if (astronomyOpen)
            closeAstronomy();
        if (changesOpen)
            closeChanges();
        if (outdoorOpen)
            closeOutdoor();
        if (dashboardOpen)
            closeDashboard();
        airOutlookReturnFocus = window.activeFocusItem;
        details.close();
        airOutlookOpen = true;
        bridge.loadAirOutlook();
    }
    function closeAirOutlook() {
        airOutlookOpen = false;
        bridge.closeAirOutlook();
        const target = airOutlookReturnFocus;
        airOutlookReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
        else if (window.visible)
            forecastScroll.forceActiveFocus();
    }
    property bool precipitationOpen: false
    property Item precipitationReturnFocus: null
    function openPrecipitation(date) {
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        precipitationReturnFocus = window.activeFocusItem;
        details.close();
        if (astronomyOpen)
            closeAstronomy();
        precipitationOpen = true;
        bridge.loadPrecipitation(date || "");
    }
    function closePrecipitation() {
        precipitationOpen = false;
        bridge.closePrecipitation();
        const target = precipitationReturnFocus;
        precipitationReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
        else if (window.visible)
            forecastScroll.forceActiveFocus();
    }
    property Item astronomyReturnFocus: null
    function openAstronomy() {
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        astronomyReturnFocus = window.activeFocusItem;
        astronomyOpen = true;
        bridge.loadAstronomy("");
    }
    function closeAstronomy() {
        astronomyOpen = false;
        bridge.closeAstronomy();
        const target = astronomyReturnFocus;
        astronomyReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
    }
    property bool changesOpen: false
    property Item changesReturnFocus: null
    property string renderedForecastContext: ""
    function openChanges() {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        changesReturnFocus = window.activeFocusItem;
        changesOpen = true;
    }
    function closeChanges() {
        changesOpen = false;
        const target = changesReturnFocus;
        changesReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
    }
    property bool dashboardOpen: false
    property Item dashboardReturnFocus: null
    function openDashboard() {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (changesOpen)
            closeChanges();
        if (outdoorOpen)
            closeOutdoor();
        dashboardReturnFocus = window.activeFocusItem;
        dashboardOpen = true;
    }
    function closeDashboard() {
        dashboardOpen = false;
        const target = dashboardReturnFocus;
        dashboardReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
    }
    property bool outdoorOpen: false
    property Item outdoorReturnFocus: null
    function openOutdoor() {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (changesOpen)
            closeChanges();
        if (dashboardOpen)
            closeDashboard();
        outdoorReturnFocus = window.activeFocusItem;
        outdoorOpen = true;
        bridge.loadOutdoor(null);
    }
    function closeOutdoor() {
        outdoorOpen = false;
        bridge.closeOutdoor();
        const target = outdoorReturnFocus;
        outdoorReturnFocus = null;
        if (target && target.visible && target.enabled && window.visible)
            target.forceActiveFocus();
    }
    property bool locationsOpen: false
    property Item locationReturnFocus: null
    property bool warningOpen: false
    property bool warningBackToSettings: false
    property Item warningReturnFocus: null
    function openWarning(reference, fromDesktop, activationToken) {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (changesOpen)
            closeChanges();
        if (dashboardOpen)
            closeDashboard();
        if (bridge.closing)
            return;
        if (!warningOpen) {
            warningReturnFocus = window.activeFocusItem;
            warningBackToSettings = !fromDesktop && effectsOpen;
        }
        if (outdoorOpen)
            closeOutdoor();
        warningOpen = true;
        effectsOpen = false;
        locationsOpen = false;
        details.close();
        bridge.loadWarning(reference);
        if (root.windowActivation)
            root.windowActivation.show(window, fromDesktop ? activationToken || "" : "");
        else {
            if (window.visibility === Window.Minimized)
                window.showNormal();
            else
                window.show();
            window.raise();
            window.requestActivate();
        }
    }
    function closeWarning() {
        const reference = bridge.warningTarget;
        warningOpen = false;
        bridge.closeWarning();
        const back = warningBackToSettings;
        warningBackToSettings = false;
        Qt.callLater(() => {
            if (root.warningOpen || !window.visible)
                return;
            if (back && bridge.available) {
                root.effectsOpen = true;
                effects.showNotifications(reference);
            } else {
                const target = root.warningReturnFocus;
                if (target && target.visible && target.enabled)
                    target.forceActiveFocus();
                else
                    settingsButton.forceActiveFocus();
            }
            root.warningReturnFocus = null;
        });
    }
    function openLocations() {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (changesOpen)
            closeChanges();
        if (dashboardOpen)
            closeDashboard();
        if (outdoorOpen)
            closeOutdoor();
        if (!savedLocations) {
            openSettings(true);
            return;
        }
        if (!locationsOpen)
            locationReturnFocus = window.activeFocusItem;
        effectsOpen = false;
        details.close();
        locationsOpen = true;
    }
    onLocationsOpenChanged: {
        Qt.callLater(showUpdateNotice);
        if (!locationsOpen) {
            if (bridge.queuedSearch !== null || bridge.pendingOp === "search_places" || (bridge.snapshot && bridge.snapshot.place_search.status !== "idle"))
                bridge.send("cancel_place_search");
            Qt.callLater(() => {
                const target = root.locationReturnFocus;
                if (!root.effectsOpen && !root.warningOpen) {
                    if (target && target.visible && target.enabled)
                        target.forceActiveFocus();
                    else
                        locationsButton.forceActiveFocus();
                }
                root.locationReturnFocus = null;
            });
        }
    }
    property Item settingsReturnFocus: null
    property bool effectsOpen: false
    onEffectsOpenChanged: {
        Qt.callLater(showUpdateNotice);
        if (!effectsOpen)
            Qt.callLater(() => {
                const target = root.settingsReturnFocus || settingsButton;
                if (!root.locationsOpen && !root.warningOpen && target && target.visible && target.enabled)
                    target.forceActiveFocus();
                root.settingsReturnFocus = null;
            });
    }
    function openSettings(locationSearch) {
        if (astronomyOpen)
            closeAstronomy();
        if (shareOpen)
            closeShare();
        if (airOutlookOpen)
            closeAirOutlook();
        if (precipitationOpen)
            closePrecipitation();
        if (changesOpen)
            closeChanges();
        if (dashboardOpen)
            closeDashboard();
        if (outdoorOpen)
            closeOutdoor();
        locationsOpen = false;
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
        if (!window.visible || root.effectsOpen || root.locationsOpen || root.warningOpen || root.outdoorOpen || root.dashboardOpen || root.changesOpen || root.astronomyOpen || root.precipitationOpen || root.airOutlookOpen || root.shareOpen || !bridge.available || bridge.busy || root.updateStatus.state !== "updated" || root.shownUpdateVersion === root.updateStatus.installed)
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
    readonly property var mapSection: dashboardLayout.mapItem
    readonly property real mapContentTop: forecastColumn.y + dashboardLayout.y + dashboardLayout.mapTop
    readonly property bool mapsNearViewport: mapSection !== null && mapSection.height > 100 && mapContentTop + mapSection.height > forecastScroll.flickable.contentY - 64 && mapContentTop < forecastScroll.flickable.contentY + forecastScroll.height + 64
    readonly property bool mapActive: window.visible && window.visibility !== Window.Hidden && window.visibility !== Window.Minimized && !root.effectsOpen && !root.locationsOpen && !root.warningOpen && !root.outdoorOpen && !root.dashboardOpen && !root.changesOpen && !root.astronomyOpen && !root.precipitationOpen && !root.airOutlookOpen && !root.shareOpen && !details.visible && bridge.available && root.hasMapLocation && !bridge.snapshot.location_settings.busy && root.mapsNearViewport
    readonly property bool forecastMapActive: root.mapActive && mapSection !== null && mapSection.selectedLayer !== "radar"
    readonly property bool radarActive: root.mapActive && mapSection !== null && mapSection.selectedLayer === "radar"
    onForecastMapActiveChanged: {
        if (forecastMapActive && !bridge.mapWanted)
            bridge.openMap();
        else if (!forecastMapActive && bridge.mapWanted)
            bridge.closeMap();
    }
    onRadarActiveChanged: {
        if (root.radarImages)
            root.radarImages.setActive(radarActive);
        if (radarActive)
            radarDemand.start();
        else {
            radarDemand.stop();
            if (bridge.radarWanted)
                bridge.closeRadar();
        }
    }
    // Let presentation bindings enqueue the foreground announcement first.
    // The service deliberately rejects radar demand while hidden.
    property Timer radarDemand: Timer {
        interval: 0
        onTriggered: if (root.radarActive && !bridge.radarWanted)
            bridge.openRadar(bridge.snapshot.latitude, bridge.snapshot.longitude, 7)
    }
    readonly property string mapLocationKey: bridge.snapshot ? [bridge.snapshot.latitude, bridge.snapshot.longitude, root.viewedPlace ? root.viewedPlace.id : ""].join("/") : ""
    onMapLocationKeyChanged: {
        if (forecastMapActive && bridge.mapWanted) {
            bridge.closeMap();
            bridge.openMap();
        }
        if (radarActive && bridge.radarWanted) {
            if (root.radarImages)
                root.radarImages.invalidate();
            bridge.openRadar(bridge.snapshot.latitude, bridge.snapshot.longitude, 7);
        }
    }
    readonly property bool liveDesktop: bridge.snapshot !== null && bridge.snapshot.effect_persistent && bridge.snapshot.effect_status !== "stopped"
    readonly property bool watchingPrecipitation: bridge.snapshot !== null && bridge.snapshot.notifications.settings.enabled
    readonly property bool watchingWarnings: bridge.snapshot !== null && bridge.snapshot.warning_notifications.settings.enabled
    function dismissWindow() {
        if ((root.liveDesktop || root.watchingPrecipitation || root.watchingWarnings) && bridge.available) {
            root.effectsOpen = false;
            root.locationsOpen = false;
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
    readonly property string city: viewedPlace && viewedPlace.label !== "" ? viewedPlace.label : location.indexOf(", ") < 0 ? location : location.substring(0, location.lastIndexOf(", "))
    readonly property string region: {
        if (viewedPlace && viewedPlace.label !== "")
            return location;
        let suffix = location.indexOf(", ") < 0 ? "" : location.substring(location.lastIndexOf(", ") + 2);
        return suffix === "MA" ? "Massachusetts" : suffix;
    }
    property var days: forecast ? forecast.daily : []
    property var hours: forecast ? forecast.hourly.slice(0, 24) : []
    onLocationChanged: {
        if (details)
            details.close();
    }
    property var controls: bridge.snapshot ? bridge.snapshot.controls : ({
            units: "F",
            units_mode: "auto",
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
        if (bridge.snapshot && bridge.snapshot.location_settings.busy)
            return "Finding location and loading forecast…";
        if (bridge.snapshot && bridge.snapshot.location_settings.error)
            return Forecast.locationErrorText(bridge.snapshot.location_settings.error);
        if (bridge.snapshot && bridge.snapshot.source.refreshing)
            return "Refreshing live forecast…";
        if (bridge.snapshot && bridge.snapshot.location_settings.mode === "default" && !forecast)
            return "Choose a city, your current location, or a ZIP code in Locations";
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
        forecastVisible: presentationActive && !root.effectsOpen && !root.locationsOpen && !root.warningOpen && !root.outdoorOpen && !root.dashboardOpen && !root.changesOpen && !root.astronomyOpen && !root.precipitationOpen && !root.airOutlookOpen && !root.shareOpen && !details.visible && forecastScroll.flickable.contentY < forecastColumn.y + headerBody.height - 80
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
        onWarningRequested: (reference, activationToken) => root.openWarning(reference, true, activationToken)
        onSnapshotChanged: {
            // Ordinary weather updates must not recreate the dashboard scene.
            if (snapshot && JSON.stringify(root.dashboardPreferences) !== JSON.stringify(snapshot.dashboard.preferences))
                root.dashboardPreferences = snapshot.dashboard.preferences;
            if (snapshot && (root.appearance.text_scale !== snapshot.appearance.text_scale || root.appearance.high_contrast !== snapshot.appearance.high_contrast || root.appearance.error !== snapshot.appearance.error))
                root.appearance = snapshot.appearance;
            if (snapshot && !root.initialLocationChecked) {
                root.initialLocationChecked = true;
                if (snapshot.location_settings.mode === "default") {
                    if (snapshot.saved_locations === null)
                        root.effectsOpen = true;
                    else
                        root.locationsOpen = true;
                }
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
        property Connections comparisonFrames: Connections {
            target: window
            enabled: bridge.forecastVisible && bridge.available && bridge.subscribed && bridge.forecastContext !== "" && bridge.changesAttemptedKey !== bridge.forecastContext
            function onAfterAnimating() {
                root.renderedForecastContext = bridge.forecastContext;
            }
            function onFrameSwapped() {
                bridge.acknowledgeRenderedForecast(root.renderedForecastContext);
            }
        }
        onActiveFocusItemChanged: Qt.callLater(() => {
            let item = window.activeFocusItem;
            if (!item)
                return;
            if (root.shareOpen && shareLoader.item) {
                shareLoader.item.revealFocus(item);
                return;
            }
            if (root.airOutlookOpen && airOutlookLoader.item) {
                airOutlookLoader.item.revealFocus(item);
                return;
            }
            if (root.precipitationOpen && precipitationLoader.item) {
                precipitationLoader.item.revealFocus(item);
                return;
            }
            if (root.astronomyOpen && astronomyLoader.item) {
                astronomyLoader.item.revealFocus(item);
                return;
            }
            if (root.changesOpen && changesLoader.item) {
                changesLoader.item.revealFocus(item);
                return;
            }
            if (root.dashboardOpen && dashboardLoader.item) {
                dashboardLoader.item.revealFocus(item);
                return;
            }
            if (root.outdoorOpen && outdoorLoader.item) {
                outdoorLoader.item.revealFocus(item);
                return;
            }
            if (root.warningOpen && warningLoader.item) {
                warningLoader.item.revealFocus(item);
                return;
            }
            if (root.locationsOpen && locationLoader.item) {
                locationLoader.item.revealFocus(item);
                return;
            }
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
        onVisibilityChanged: {
            if (window.visibility === Window.Minimized && root.precipitationOpen)
                root.closePrecipitation();
            if (window.visibility === Window.Minimized && root.airOutlookOpen)
                root.closeAirOutlook();
            if (window.visibility === Window.Minimized && root.shareOpen)
                root.closeShare();
            if (window.visibility === Window.Minimized && root.astronomyOpen)
                root.closeAstronomy();
            if (window.visibility === Window.Minimized && root.changesOpen)
                root.closeChanges();
            if (window.visibility === Window.Minimized && root.dashboardOpen)
                root.closeDashboard();
            if (window.visibility === Window.Minimized && root.outdoorOpen)
                root.closeOutdoor();
        }
        onVisibleChanged: {
            if (!visible) {
                if (root.precipitationOpen)
                    root.closePrecipitation();
                if (root.airOutlookOpen)
                    root.closeAirOutlook();
                if (root.shareOpen)
                    root.closeShare();
                if (root.astronomyOpen)
                    root.closeAstronomy();
                if (root.changesOpen)
                    root.closeChanges();
                if (root.dashboardOpen)
                    root.closeDashboard();
                root.updateNoticeActive = false;
                root.locationsOpen = false;
                if (root.outdoorOpen)
                    root.closeOutdoor();
                if (root.warningOpen)
                    root.closeWarning();
            } else
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
            enabled: !details.visible && !root.warningOpen
            onActivated: root.openLocations()
        }
        Shortcut {
            sequence: "Escape"
            enabled: root.effectsOpen || root.locationsOpen
            onActivated: {
                root.effectsOpen = false;
                root.locationsOpen = false;
            }
        }
        Atmosphere {
            id: forecastAtmosphere
            objectName: "forecastAtmosphere"
            anchors.fill: parent
            presentationActive: bridge.available && window.visible && !root.effectsOpen && !root.locationsOpen && !root.warningOpen && !root.outdoorOpen && !root.dashboardOpen && !root.changesOpen && !root.astronomyOpen && !root.precipitationOpen && !root.airOutlookOpen && !root.shareOpen && !details.visible
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
            enabled: !root.effectsOpen && !root.locationsOpen
            anchors.fill: parent
            clip: true
            contentWidth: availableWidth
            ColumnLayout {
                id: forecastColumn
                width: window.width - 56
                x: 28
                y: 18
                spacing: root.compactDashboard ? 10 : 16
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
                                color: Tokens.highContrast ? "#263f55" : "#800b1c30"
                            }
                            GradientStop {
                                position: 0.66
                                color: Tokens.highContrast ? "#263f55" : "#800b1c30"
                            }
                            GradientStop {
                                position: 1
                                color: Tokens.highContrast ? "#263f55" : "transparent"
                            }
                        }
                    }
                    ColumnLayout {
                        id: headerBody
                        readonly property bool actionsBelowLocation: window.width < 850 || locationFont.advanceWidth(root.city) + headerActions.preferredWidth + 20 > width
                        FontMetrics {
                            id: locationFont
                            font.pixelSize: Tokens.fontSize(38)
                            font.family: "sans-serif"
                        }
                        width: parent.width
                        spacing: 16
                        Item {
                            Layout.fillWidth: true
                            Layout.preferredHeight: Math.max(conditions.height, headerActions.y + headerActions.height + (updatedNotice.visible ? updatedNotice.height + 8 : 0), headerAlerts.visible ? headerAlerts.y + headerAlerts.height : 0)
                            Flow {
                                id: headerActions
                                objectName: "headerActions"
                                spacing: 5
                                readonly property real preferredWidth: {
                                    let total = -spacing;
                                    for (const item of children)
                                        if (item.visible)
                                            total += item.implicitWidth + spacing;
                                    return Math.max(0, total);
                                }
                                width: Math.min(parent.width, preferredWidth)
                                height: childrenRect.height
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
                                    id: locationsButton
                                    objectName: "openLocation"
                                    text: "Locations"
                                    accessibleLabel: "Saved locations (Ctrl+L)"
                                    onClicked: root.openLocations()
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
                                height: Math.max(34, updatedVersionNotice.implicitHeight + 16)
                                visible: root.updateNoticeActive
                                PlainLabel {
                                    id: updatedVersionNotice
                                    objectName: "updatedVersionNotice"
                                    anchors.centerIn: parent
                                    text: "Updated to " + root.shownUpdateVersion + "."
                                    font.pixelSize: Tokens.fontSize(13)
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
                                    font.pixelSize: Tokens.fontSize(38)
                                    wrapMode: Text.Wrap
                                    maximumLineCount: 3
                                    elide: Text.ElideRight
                                    width: headerBody.actionsBelowLocation ? parent.width : parent.width - headerActions.width - 20
                                }
                                PlainLabel {
                                    id: regionHeading
                                    width: parent.width
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                    text: root.region
                                    font.pixelSize: Tokens.fontSize(19)
                                    color: "#d4e3ee"
                                }
                                Item {
                                    width: 1
                                    height: headerBody.actionsBelowLocation ? headerActions.height + 12 + (updatedNotice.visible ? updatedNotice.height + 8 : 0) : 0
                                }
                                PlainLabel {
                                    objectName: "primaryLocationHint"
                                    visible: root.savedLocations !== null
                                    width: parent.width
                                    text: root.savedLocations && root.savedLocations.viewed === root.savedLocations.primary ? "Primary location" : "Desktop weather · " + root.primaryName
                                    font.pixelSize: Tokens.fontSize(13)
                                    color: Tokens.secondary
                                }
                                PlainLabel {
                                    objectName: "currentTemperature"
                                    text: root.current ? Forecast.temp(root.current.temperature_c, root.units) : "—°"
                                    font.pixelSize: Tokens.fontSize(96)
                                    font.weight: Font.Light
                                }
                                PlainLabel {
                                    text: root.current ? Forecast.title(root.current.condition) : "Forecast unavailable"
                                    font.pixelSize: Tokens.fontSize(27)
                                }
                                PlainLabel {
                                    width: window.width >= 850 && root.activeAlerts.length ? parent.width * 0.48 : parent.width
                                    text: "Feels like " + Forecast.temp(root.current ? root.current.apparent_temperature_c : null, root.units) + " · High " + Forecast.temp(root.days.length ? root.days[0].high_c : null, root.units) + " · Low " + Forecast.temp(root.days.length ? root.days[0].low_c : null, root.units)
                                    font.pixelSize: Tokens.fontSize(18)
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
                                font.pixelSize: Tokens.fontSize(13)
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                objectName: "alertCoverageStatus"
                                visible: root.alertsStatusText !== ""
                                text: root.alertsStatusText
                                color: "#d4e3ee"
                                font.pixelSize: Tokens.fontSize(12)
                            }
                            PlainLabel {
                                visible: root.controls.mode === "manual" && !root.liveDesktop
                                text: "Manual effects preview · " + Forecast.title(root.controls.manual.condition)
                                font.pixelSize: Tokens.fontSize(13)
                                color: Tokens.accent
                            }
                        }
                        ColumnLayout {
                            visible: root.forecast !== null && bridge.snapshot !== null && ["fresh", "stale"].indexOf(bridge.snapshot.source.freshness) >= 0
                            Layout.fillWidth: true
                            spacing: 6
                            RowLayout {
                                Layout.fillWidth: true
                                ActionButton {
                                    objectName: "openOutdoorPlanner"
                                    visible: root.hours.length > 0
                                    text: "Find a time to go outside"
                                    enabled: bridge.available && !bridge.busy && bridge.snapshot !== null && !bridge.snapshot.location_settings.busy
                                    onClicked: root.openOutdoor()
                                }
                                ActionButton {
                                    objectName: "openForecastShare"
                                    text: "Share forecast"
                                    enabled: bridge.snapshot !== null && !bridge.snapshot.location_settings.busy
                                    onClicked: root.openShare()
                                }
                            }
                            RowLayout {
                                visible: bridge.forecastContext !== ""
                                Layout.fillWidth: true
                                ActionButton {
                                    objectName: "openForecastChanges"
                                    text: "Forecast changes"
                                    enabled: bridge.available && (bridge.changesResult !== null || bridge.changesState === "unavailable")
                                    onClicked: root.openChanges()
                                }
                                PlainLabel {
                                    objectName: "forecastChangesSummary"
                                    Layout.fillWidth: true
                                    text: Changes.summary(bridge.changesResult, bridge.changesState, root.units)
                                    color: Tokens.secondary
                                    font.pixelSize: Tokens.fontSize(12)
                                    wrapMode: Text.Wrap
                                    elide: Text.ElideNone
                                }
                                ActionButton {
                                    objectName: "retryForecastChanges"
                                    visible: bridge.changesState === "unavailable"
                                    text: "Retry"
                                    enabled: bridge.available
                                    onClicked: {
                                        bridge.retryChanges();
                                        window.update();
                                    }
                                }
                            }
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
                                font.pixelSize: Tokens.fontSize(12)
                                wrapMode: Text.Wrap
                                elide: Text.ElideNone
                            }
                            PlainLabel {
                                objectName: "forecastOutlook"
                                Layout.fillWidth: true
                                text: root.selectedBriefing ? Forecast.briefingText(root.selectedBriefing, root.units, root.windUnits) : Forecast.outlook(root.hours, root.units, root.windUnits)
                                font.pixelSize: Tokens.fontSize(18)
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
                RowLayout {
                    Layout.fillWidth: true
                    Item {
                        Layout.fillWidth: true
                    }
                    ActionButton {
                        objectName: "openDashboardEditor"
                        text: "Customize dashboard"
                        enabled: bridge.available && !bridge.dashboardSaving && bridge.snapshot !== null
                        onClicked: root.openDashboard()
                    }
                }
                DashboardLayout {
                    id: dashboardLayout
                    objectName: "forecastCards"
                    Layout.fillWidth: true
                    preferences: root.dashboardPreferences
                    paired: window.width >= 1200 * Tokens.textScale && root.days.length > 0
                    components: ({
                            hourly: hourlyComponent,
                            daily: dailyComponent,
                            metrics: metricsComponent,
                            maps: mapsComponent,
                            air_quality: airQualityComponent
                        })
                }
                PlainLabel {
                    objectName: "sourceAttribution"
                    Layout.fillWidth: true
                    horizontalAlignment: Text.AlignRight
                    text: bridge.snapshot ? bridge.snapshot.source.attribution + (bridge.snapshot.alerts.source ? " · Alerts: " + bridge.snapshot.alerts.source : "") : "Forecast: Open-Meteo"
                    color: Tokens.secondary
                    font.pixelSize: Tokens.fontSize(11)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                Item {
                    Layout.preferredHeight: 24
                }
            }
        }
        Component {
            id: hourlyComponent
            HourlyPanel {
                hours: root.hours
                units: root.units
                windUnits: root.windUnits
                timezone: root.timezone
                compact: root.compactDashboard
                values: root.dashboardPreferences.hourly
                onHourSelected: hour => details.showHour(hour)
            }
        }
        Component {
            id: dailyComponent
            DailyPanel {
                days: root.days
                units: root.units
                compact: root.compactDashboard
                onDaySelected: day => details.showDay(day)
                detailsAvailable: bridge.available && bridge.snapshot !== null && bridge.snapshot.location_settings.mode !== "default" && !bridge.snapshot.location_settings.busy
                onPrecipitationRequested: root.openPrecipitation("")
            }
        }
        Component {
            id: metricsComponent
            MetricsPanel {
                objectName: "currentMetrics"
                current: root.current
                day: root.days.length ? root.days[0] : null
                units: root.units
                windUnits: root.windUnits
                timezone: root.timezone
                compact: root.compactDashboard
                selection: root.dashboardPreferences.metrics
                presentationActive: bridge.presentationActive && !root.dashboardOpen && !root.astronomyOpen && !root.precipitationOpen && !root.airOutlookOpen && !root.shareOpen
                onMetricSelected: metric => {
                    if (metric === "daylight") {
                        root.openAstronomy();
                    } else
                        details.showMetric(metric);
                }
            }
        }
        Component {
            id: mapsComponent
            WeatherMap {
                tileClient: root.mapTiles
                mapState: bridge.weatherMap
                radarState: bridge.weatherRadar
                imageControl: root.radarImages
                locationLatitude: bridge.snapshot && bridge.snapshot.latitude !== null ? bridge.snapshot.latitude : 0
                locationLongitude: bridge.snapshot && bridge.snapshot.longitude !== null ? bridge.snapshot.longitude : 0
                onRadarViewRequested: (latitude, longitude, zoom) => bridge.openRadar(latitude, longitude, zoom)
                location: root.city
                units: root.units
                windUnits: root.windUnits
                hasLocation: root.hasMapLocation
                active: root.mapActive
                visualQuality: forecastAtmosphere.effectiveQuality
                reducedMotion: root.controls.reduced_motion
                viewportTop: forecastScroll.flickable.contentY - root.mapContentTop
                viewportHeight: forecastScroll.height
            }
        }
        Component {
            id: airQualityComponent
            AirQualityPanel {
                objectName: "airQualityPanel"
                airQuality: bridge.snapshot ? bridge.snapshot.air_quality : Forecast.airQuality()
                outlookEnabled: bridge.available && root.hasMapLocation && !bridge.snapshot.location_settings.busy
                onOutlookRequested: root.openAirOutlook()
            }
        }
        ForecastDetails {
            id: details
            onPrecipitationRequested: date => root.openPrecipitation(date)
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
            presentationActive: bridge.presentationActive
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
            appearance: root.appearance
            onAppearanceRequested: values => bridge.send("set_appearance", values)
            location: root.location
            primaryLocation: root.primaryName
            hasSavedLocations: root.savedLocations !== null
            onManageLocationsRequested: root.openLocations()
            busy: bridge.busy
            forecastAvailable: root.primaryForecastAvailable
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
            officialWarnings: bridge.snapshot ? bridge.snapshot.warning_notifications : Forecast.warningNotifications()
            timezone: root.primaryTimezone
            actionError: bridge.error
            canStart: bridge.available && root.primaryForecastAvailable && bridge.snapshot !== null && bridge.snapshot.effect_status !== "cleanup_failed" && setup.status === "ready"
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
            onLocationRequested: values => bridge.send(root.savedLocations ? "add_location" : "set_location", values)
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
            onWarningPatch: values => bridge.send("set_warning_notifications", values)
            onWarningPauseRequested: bridge.send("pause_warning_notifications")
            onWarningResumeRequested: bridge.send("resume_warning_notifications")
            onWarningDetailRequested: reference => root.openWarning(reference, false)
            launcherStatus: bridge.snapshot ? bridge.snapshot.launcher_status : "ready"
            updateStatus: bridge.snapshot ? bridge.snapshot.update : Forecast.updateStatus(null)
            onCheckUpdatesRequested: bridge.send("check_updates")
            onInstallUpdateRequested: bridge.send("install_update")
            onInstallLauncherRequested: bridge.send("install_launcher")
            onQuitRequested: bridge.shutdown()
        }
        Rectangle {
            anchors.fill: parent
            visible: root.locationsOpen
            color: "#650b1c29"
            MouseArea {
                anchors.fill: parent
                onClicked: root.locationsOpen = false
            }
        }
        Loader {
            id: locationLoader
            objectName: "locationPickerLoader"
            active: root.locationsOpen && root.savedLocations !== null
            anchors.left: parent.left
            anchors.top: parent.top
            anchors.bottom: parent.bottom
            anchors.margins: 18
            width: Math.min(610, window.width - 36)
            onLoaded: Qt.callLater(() => {
                if (!item)
                    return;
                if (bridge.snapshot && bridge.snapshot.location_settings.mode === "default")
                    item.showAdd();
                else
                    item.focusInitial();
            })
            sourceComponent: LocationPicker {
                objectName: "locationPicker"
                registry: root.savedLocations
                controls: root.controls
                search: bridge.snapshot ? bridge.snapshot.place_search : Forecast.placeSearch(undefined)
                locationSettings: bridge.snapshot ? bridge.snapshot.location_settings : ({
                        busy: false,
                        error: null
                    })
                serviceAvailable: bridge.available
                busy: bridge.busy
                actionError: bridge.error
                onCloseRequested: root.locationsOpen = false
                onViewRequested: id => {
                    if (bridge.send("saved_location", {
                        action: "view",
                        id: id
                    }))
                        root.locationsOpen = false;
                }
                onActionRequested: action => bridge.send("saved_location", action)
                onAddRequested: selection => {
                    if (bridge.send("add_location", selection))
                        root.locationsOpen = false;
                }
                onSearchRequested: values => bridge.send("search_places", values)
                onCancelSearchRequested: bridge.send("cancel_place_search")
            }
        }
        Loader {
            id: dashboardLoader
            objectName: "dashboardEditorLoader"
            active: root.dashboardOpen
            onLoaded: item.open()
            sourceComponent: DashboardEditor {
                bridge: root.backend
                onClosed: if (root.dashboardOpen)
                    root.closeDashboard()
            }
        }
        Loader {
            id: shareLoader
            objectName: "forecastShareLoader"
            active: root.shareOpen
            source: "ForecastShare.qml"
            onLoaded: {
                item.snapshot = Qt.binding(() => bridge.snapshot);
                item.imageFileRequested.connect(() => {
                    if (forecastSaveDialog.item)
                        forecastSaveDialog.item.open();
                    else
                        forecastSaveDialog.active = true;
                });
                item.closed.connect(() => {
                    if (root.shareOpen)
                        root.closeShare();
                });
                item.open();
            }
        }
        // One chooser per window, created only on the first Save image action.
        // Recreating Qt's file-dialog infrastructure on every share session
        // retained memory. The small preview and its data still unload on close.
        Loader {
            id: forecastSaveDialog
            objectName: "forecastSaveDialogLoader"
            active: false
            source: "ForecastSaveDialog.qml"
            onLoaded: {
                item.parentWindow = window;
                if (typeof item.popupType === "number")
                    item.popupType = Popup.Item;
                item.open();
            }
            onStatusChanged: if (status === Loader.Error && shareLoader.item)
                shareLoader.item.notify("The file picker is unavailable. You can still copy the forecast text.")
        }
        Connections {
            target: forecastSaveDialog.item
            function onChosen(destination) {
                if (root.shareOpen && shareLoader.item)
                    shareLoader.item.saveImage(destination);
            }
            function onFinished() {
                if (root.shareOpen && shareLoader.item)
                    shareLoader.item.imageFileDialogFinished();
            }
        }
        Loader {
            id: airOutlookLoader
            objectName: "airOutlookLoader"
            active: root.airOutlookOpen
            onLoaded: item.open()
            sourceComponent: AirOutlookDetails {
                result: bridge.airOutlookResult
                state: bridge.airOutlookState
                error: bridge.airOutlookError
                place: root.location
                onRequested: bridge.loadAirOutlook()
                onClosed: if (root.airOutlookOpen)
                    root.closeAirOutlook()
            }
        }
        Loader {
            id: precipitationLoader
            objectName: "precipitationLoader"
            active: root.precipitationOpen
            onLoaded: item.open()
            sourceComponent: PrecipitationDetails {
                result: bridge.precipitationResult
                state: bridge.precipitationState
                error: bridge.precipitationError
                place: root.location
                units: root.units
                onRequested: date => bridge.loadPrecipitation(date)
                onClosed: if (root.precipitationOpen)
                    root.closePrecipitation()
            }
        }
        Loader {
            id: astronomyLoader
            objectName: "astronomyLoader"
            active: root.astronomyOpen
            onLoaded: item.open()
            sourceComponent: AstronomyDetails {
                result: bridge.astronomyResult
                state: bridge.astronomyState
                place: root.location
                onRequested: date => bridge.loadAstronomy(date)
                onClosed: if (root.astronomyOpen)
                    root.closeAstronomy()
            }
        }
        Loader {
            id: changesLoader
            objectName: "forecastChangesLoader"
            active: root.changesOpen
            onLoaded: item.open()
            sourceComponent: ForecastChanges {
                result: bridge.changesResult
                state: bridge.changesState
                units: root.units
                windUnits: root.windUnits
                place: root.location
                onClosed: if (root.changesOpen)
                    root.closeChanges()
            }
        }
        Loader {
            id: outdoorLoader
            objectName: "outdoorPlannerLoader"
            active: root.outdoorOpen
            onLoaded: item.open()
            sourceComponent: OutdoorPlanner {
                plan: bridge.outdoorResult
                state: bridge.outdoorState
                error: bridge.outdoorError
                available: bridge.available && bridge.snapshot !== null && !bridge.snapshot.location_settings.busy
                units: root.units
                windUnits: root.windUnits
                place: root.location
                freshness: root.freshness
                alertCount: root.activeAlerts.length
                onFindRequested: preferences => bridge.loadOutdoor(preferences)
                onClosed: {
                    if (root.outdoorOpen)
                        root.closeOutdoor();
                }
            }
        }
        Loader {
            id: warningLoader
            objectName: "warningDetailsLoader"
            active: root.warningOpen
            onLoaded: item.open()
            sourceComponent: WarningDetails {
                warning: bridge.warningDetail
                state: bridge.warningState
                error: bridge.warningError
                onClosed: {
                    if (root.warningOpen)
                        root.closeWarning();
                }
            }
        }
    }
}
