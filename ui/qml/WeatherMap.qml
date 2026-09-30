import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Forecast.js" as Forecast

ColumnLayout {
    id: root
    objectName: "weatherMaps"
    spacing: 12
    property var mapState: ({status:"closed", offline:false, error:"", data:null, hour_labels:[], fetched_label:null})
    property string location: ""
    property string units: "F"
    property string windUnits: "auto"
    property bool hasLocation: false
    property bool active: false
    property real viewportTop: 0
    property real viewportHeight: 0
    property int hourIndex: 0
    property var tileImages: ({})
    property var failedTiles: ({})
    readonly property var tileClient: typeof mapTiles === "undefined" ? null : mapTiles
    readonly property var mapData: mapState.data || null
    function cardVisible(card) { return active && mapData !== null && cards.y + card.y + card.height >= viewportTop - 32 && cards.y + card.y <= viewportTop + viewportHeight + 32 }
    function clearTiles() { if (tileClient) tileClient.close(); tileImages = {}; failedTiles = {} }
    onActiveChanged: if (!active) clearTiles()
    onMapDataChanged: { hourIndex = 0; clearTiles() }
    Component.onDestruction: if (tileClient) tileClient.close()
    Connections { target: root.tileClient; ignoreUnknownSignals: true
        function onTileReady(key, dataURL) { let next = Object.assign({}, root.tileImages); next[key] = dataURL; root.tileImages = next }
        function onTileFailed(key) { let next = Object.assign({}, root.failedTiles); next[key] = true; root.failedTiles = next }
    }
    RowLayout { Layout.fillWidth: true
        PlainLabel { Layout.fillWidth: true; text: "Local weather maps · " + root.location; font.pixelSize: 21; font.weight: Font.DemiBold; elide: Text.ElideRight }
        PlainLabel { text: root.mapData ? root.mapData.hours.length + " hours" : ""; color: Tokens.secondary; font.pixelSize: 13 }
    }
    RowLayout { Layout.fillWidth: true
        ActionButton { objectName: "mapPreviousHour"; text: "Previous"; enabled: root.mapData && root.hourIndex > 0; onClicked: root.hourIndex-- }
        Slider { id: timeline; objectName: "mapTimeline"; Layout.fillWidth: true; from: 0; to: root.mapData ? root.mapData.hours.length - 1 : 0; stepSize: 1; value: root.hourIndex; enabled: root.mapData && root.mapData.hours.length > 1; onMoved: root.hourIndex = Math.round(value) }
        ActionButton { objectName: "mapNextHour"; text: "Next"; enabled: root.mapData && root.hourIndex < root.mapData.hours.length - 1; onClicked: root.hourIndex++ }
    }
    PlainLabel { Layout.fillWidth: true; text: root.mapData ? "Forecast valid " + root.mapState.hour_labels[root.hourIndex] + " · Hour " + (root.hourIndex + 1) + " of " + root.mapData.hours.length : root.hasLocation ? "Local model maps load as you scroll here" : "Choose a location to see local maps"; font.pixelSize: 14; color: Tokens.secondary }
    GridLayout { id: cards; Layout.fillWidth: true; columns: root.width < 900 ? 1 : 2; columnSpacing: 16; rowSpacing: 16
        WeatherMapCard { id: temperatureCard; objectName: "mapTemperatureModule"; Layout.fillWidth: true; Layout.preferredWidth: root.width < 900 ? root.width : (root.width - 16) / 2; mapLayer: "temperature"; mapData: root.mapData; mapStatus: root.mapState.status; hasLocation: root.hasLocation; offline: root.mapState.offline; hourIndex: root.hourIndex; units: root.units; windUnits: root.windUnits; tileImages: root.tileImages; failedTiles: root.failedTiles; tileClient: root.tileClient; tileActive: root.cardVisible(temperatureCard) }
        WeatherMapCard { id: windCard; objectName: "mapWindModule"; Layout.fillWidth: true; Layout.preferredWidth: root.width < 900 ? root.width : (root.width - 16) / 2; mapLayer: "wind"; mapData: root.mapData; mapStatus: root.mapState.status; hasLocation: root.hasLocation; offline: root.mapState.offline; hourIndex: root.hourIndex; units: root.units; windUnits: root.windUnits; tileImages: root.tileImages; failedTiles: root.failedTiles; tileClient: root.tileClient; tileActive: root.cardVisible(windCard) }
        WeatherMapCard { id: precipitationCard; objectName: "mapPrecipitationModule"; Layout.fillWidth: true; Layout.columnSpan: root.width < 900 ? 1 : 2; Layout.preferredWidth: root.width; mapLayer: "precipitation"; mapData: root.mapData; mapStatus: root.mapState.status; hasLocation: root.hasLocation; offline: root.mapState.offline; hourIndex: root.hourIndex; units: root.units; windUnits: root.windUnits; tileImages: root.tileImages; failedTiles: root.failedTiles; tileClient: root.tileClient; tileActive: root.cardVisible(precipitationCard) }
    }
    PlainLabel { Layout.fillWidth: true; visible: root.mapData !== null; text: root.mapData ? root.mapData.model_name + " · native grid ~" + Forecast.distance(root.mapData.resolution_km*1000,root.units) + " · sampled/interpolated model forecast · fetched " + root.mapState.fetched_label + (root.mapState.status === "stale" ? " · stale" : "") + (root.mapState.offline ? " · offline" : "") : ""; font.pixelSize: 12; color: Tokens.secondary; wrapMode: Text.Wrap; elide: Text.ElideNone }
    PlainLabel { Layout.fillWidth: true; visible: root.mapData !== null; text: root.mapData ? root.mapData.attribution + (root.mapData.resolution_km >= 10 ? " · Coarse global pattern; neighborhood detail unavailable." : "") : ""; font.pixelSize: 11; color: Tokens.secondary; wrapMode: Text.Wrap; elide: Text.ElideNone }
}
