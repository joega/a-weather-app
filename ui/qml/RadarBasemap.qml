pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Window
import "RadarGeometry.js" as Geometry

Item {
    id: root
    objectName: "radarBasemap"
    property var bounds: null
    property var tileClient: null
    property var tileImages: ({})
    property var failedTiles: ({})
    property int tileGeneration: 0
    property bool active: false
    property bool offline: false
    property real viewportHeight: height
    property real devicePixelRatio: Window.window ? Window.window.devicePixelRatio : 1
    // Frame/status snapshots replace the view object. Primitive bindings retain
    // the tile model and Image delegates when geographic bounds haven't changed.
    readonly property real west: bounds ? bounds.west : 0
    readonly property real south: bounds ? bounds.south : 0
    readonly property real east: bounds ? bounds.east : 0
    readonly property real north: bounds ? bounds.north : 0
    readonly property var geometry: Geometry.layout(west, south, east, north, width, viewportHeight, devicePixelRatio)
    readonly property var visibleTiles: active ? geometry.tiles : []
    readonly property int zoom: geometry.zoom
    property string installedGeometry: ""
    signal resetRequested

    function requestTiles() {
        if (!active || !tileClient || layoutSync.running)
            return;
        for (const tile of visibleTiles)
            if (!tileImages[tile.key] && !failedTiles[tile.key])
                tileClient.request(tile.zoom, tile.x, tile.y, offline);
    }
    onGeometryChanged: layoutSync.restart()
    onActiveChanged: {
        if (active)
            layoutSync.restart();
        else {
            layoutSync.stop();
            requestSync.stop();
        }
    }
    onTileClientChanged: requestSync.restart()
    onTileGenerationChanged: requestSync.restart()
    onTileImagesChanged: requestSync.restart()
    onFailedTilesChanged: requestSync.restart()
    onOfflineChanged: requestSync.restart()
    Component.onCompleted: if (active)
        layoutSync.restart()

    Timer {
        id: layoutSync
        interval: 100
        onTriggered: {
            if (!root.active)
                return;
            const key = [root.west, root.south, root.east, root.north, root.zoom].join("/") + ":" + root.visibleTiles.map(tile => tile.key).join(",");
            if (root.installedGeometry !== "" && root.installedGeometry !== key)
                root.resetRequested();
            root.installedGeometry = key;
            root.requestTiles();
        }
    }
    Timer {
        id: requestSync
        interval: 0
        onTriggered: root.requestTiles()
    }
    Repeater {
        model: root.visibleTiles
        Image {
            required property var modelData
            objectName: "radarBasemapTile_" + modelData.key
            x: modelData.left
            y: modelData.top
            width: modelData.width + 0.5
            height: modelData.height + 0.5
            source: root.tileImages[modelData.key] || ""
            asynchronous: true
            cache: false
            smooth: true
        }
    }
}
