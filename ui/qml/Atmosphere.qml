import QtQuick
import QtQuick.Window
import Quickshell

Item {
    id: root
    property string condition: "unknown"
    property bool isDay: true
    property real cloudCover: condition === "clear" ? 0.08 : condition === "partly_cloudy" ? 0.5 : 0.95
    property real fogDensity: condition === "fog" ? 0.8 : 0.0
    property real sunElevation: isDay ? 35.0 : -25.0
    property real sunAzimuth: 180.0
    property real wind: 35.0
    property bool reducedMotion: false
    property bool lightningEnabled: false
    // The parent may suppress fully occluded presentation without changing weather.
    property bool presentationActive: true
    readonly property bool animationActive: visible && presentationActive && !reducedMotion
        && (!Window.window || (Window.window.visibility !== Window.Minimized
                              && Window.window.visibility !== Window.Hidden))
    property real lightningIntensity: animationActive && lightningEnabled && condition === "thunderstorm"
        ? lightningAt(visualTime) : 0.0
    // Seed-zero envelope matches weather/lightning.py and native atmosphere flash().
    function lightningAt(seconds) {
        if (!Number.isFinite(seconds) || seconds < 0) return 0
        const mixed = (Math.floor(seconds / 24) * 1664525 + 1013904223) >>> 0
        const start = 4 + (mixed % 14000) / 1000
        const local = seconds % 24
        function pulse(onset, duration, peak) {
            const phase = (local - onset) / duration
            return phase > 0 && phase < 1 ? peak * Math.pow(Math.sin(Math.PI * phase), 2) : 0
        }
        return pulse(start, 0.18, 0.28) + ((mixed & 1) ? pulse(start + 0.8, 0.12, 0.09) : 0)
    }
    property real rainAmount: ["rain", "drizzle", "thunderstorm", "sleet"].indexOf(condition) >= 0 ? (condition === "drizzle" ? 0.3 : 0.7) : 0.0
    property real snowAmount: condition === "snow" ? 0.7 : condition === "sleet" ? 0.35 : 0.0
    property real visualTime: 0.0
    property real cloudOffset: 0.0
    property double lastTick: 0
    readonly property real textureScale: Math.min(0.5, 1280 / Math.max(1, width), 720 / Math.max(1, height))
    readonly property size bufferSize: Qt.size(Math.max(1, Math.ceil(width * textureScale)), Math.max(1, Math.ceil(height * textureScale)))
    onReducedMotionChanged: lastTick = Date.now()
    onVisibleChanged: lastTick = Date.now()
    Timer {
        interval: 33
        repeat: true
        running: root.animationActive && root.width > 0 && root.height > 0
        onRunningChanged: root.lastTick = Date.now()
        onTriggered: {
            const now = Date.now()
            const dt = Math.max(0, Math.min(0.1, (now - root.lastTick) / 1000))
            root.lastTick = now
            root.visualTime += dt
            root.cloudOffset += Math.max(-500, Math.min(500, root.wind)) * dt * 0.00008
        }
    }
    ShaderEffect {
        id: sky
        anchors.fill: parent
        property real scene_time: root.visualTime
        property real sun_elevation: Math.max(-90, Math.min(90, root.sunElevation))
        property real sun_azimuth: root.sunAzimuth
        property real cloud_cover: Math.max(0, Math.min(1, root.cloudCover))
        property real fog_density: Math.max(0, Math.min(1, root.fogDensity))
        property real cloud_offset: root.cloudOffset
        property real lightning: root.animationActive && root.lightningEnabled && root.condition === "thunderstorm"
            ? Math.max(0, Math.min(0.28, root.lightningIntensity)) : 0
        property bool reduced_motion: root.reducedMotion
        property real aspect_ratio: root.width / Math.max(1, root.height)
        property real rain_amount: root.animationActive ? Math.max(0, Math.min(1, root.rainAmount)) : 0
        property real snow_amount: root.animationActive ? Math.max(0, Math.min(1, root.snowAmount)) : 0
        property real wind_x: Math.max(-500, Math.min(500, root.wind))
        fragmentShader: "file://" + Quickshell.shellDir + "/../shaders/atmosphere.frag.qsb"
        blending: false
    }
    ShaderEffectSource {
        anchors.fill: parent
        sourceItem: sky
        hideSource: true
        live: true
        smooth: true
        textureSize: root.bufferSize
    }
}
