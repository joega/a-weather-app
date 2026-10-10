import QtQuick
import QtQuick.Window

Item {
    id: root
    property string condition: "unknown"
    property bool isDay: true
    property real cloudCover: condition === "clear" ? 0.08 : condition === "partly_cloudy" ? 0.5 : 0.95
    property real fogDensity: condition === "fog" ? 0.8 : 0.0
    property real sunElevation: isDay ? 35.0 : -25.0
    property real sunAzimuth: 180.0
    // Cloud coverage controls density; precipitation controls storm shading.
    // Keep the palette stable when particles pause or reduced motion is used.
    readonly property real weatherDim: condition === "thunderstorm" ? 1 : condition === "rain" || condition === "sleet" ? 0.65 : condition === "drizzle" ? 0.35 : condition === "snow" ? 0.25 : 0
    // Reserve space for forecast text without moving clouds or adding a layer.
    property real celestialLeft: 0
    property real celestialTop: 0
    property real wind: 35.0
    property real windSpeed: 0.0
    property var graphicsCapabilities: null
    property string visualQuality: "auto"
    property bool pipelineFailed: false
    readonly property bool shaderSupported: graphicsCapabilities !== null && graphicsCapabilities.shaderSupported
    readonly property string effectiveQuality: !shaderSupported || pipelineFailed || visualQuality === "static" ? "static" : visualQuality === "economical" ? "economical" : "full"
    readonly property bool shaderAvailable: effectiveQuality !== "static" && shaderLoader.item !== null
    // Artistic scene drift stays visible within seconds, including calm and north/south
    // winds. Cap its wind response so a storm never sweeps the forecast backdrop.
    readonly property real driftRate: (wind < -3 ? -1 : 1) * (0.008 + Math.min(16, Math.max(0, windSpeed)) * 0.0007)
    property bool reducedMotion: false
    property bool lightningEnabled: false
    // The parent may suppress fully occluded presentation without changing weather.
    property bool presentationActive: true
    readonly property bool animationActive: visible && shaderAvailable && presentationActive && !reducedMotion && (!Window.window || (Window.window.visibility !== Window.Minimized && Window.window.visibility !== Window.Hidden))
    property real lightningIntensity: animationActive && lightningEnabled && condition === "thunderstorm" ? lightningAt(visualTime) : 0.0
    // Seed-zero envelope matches internal/weather/visual.go and native atmosphere flash().
    function lightningAt(seconds) {
        if (!Number.isFinite(seconds) || seconds < 0)
            return 0;
        const mixed = (Math.floor(seconds / 24) * 1664525 + 1013904223) >>> 0;
        const start = 4 + (mixed % 14000) / 1000;
        const local = seconds % 24;
        function pulse(onset, duration, peak) {
            const phase = (local - onset) / duration;
            return phase > 0 && phase < 1 ? peak * Math.pow(Math.sin(Math.PI * phase), 2) : 0;
        }
        return pulse(start, 0.18, 0.28) + ((mixed & 1) ? pulse(start + 0.8, 0.12, 0.09) : 0);
    }
    property real rainAmount: ["rain", "drizzle", "thunderstorm", "sleet"].indexOf(condition) >= 0 ? (condition === "drizzle" ? 0.3 : 0.7) : 0.0
    property real snowAmount: condition === "snow" ? 0.7 : condition === "sleet" ? 0.35 : 0.0
    // Clouds drift slowly enough for 20 Hz; precipitation and brief lightning
    // pulses retain 30 Hz. Integrate elapsed time so cadence never changes speed.
    readonly property int animationInterval: effectiveQuality === "economical" ? 100 : rainAmount > 0 || snowAmount > 0 || (lightningEnabled && condition === "thunderstorm") ? 33 : 50
    property real visualTime: 0.0
    property real cloudOffset: 0.0
    property double lastTick: 0
    readonly property real textureScale: Math.min(0.5, 1280 / Math.max(1, width), 720 / Math.max(1, height))
    readonly property size bufferSize: Qt.size(Math.max(1, Math.ceil(width * textureScale)), Math.max(1, Math.ceil(height * textureScale)))
    onReducedMotionChanged: lastTick = Date.now()
    onVisibleChanged: lastTick = Date.now()
    Timer {
        objectName: "atmosphereAnimationTimer"
        interval: root.animationInterval
        repeat: true
        running: root.animationActive && root.width > 0 && root.height > 0
        onRunningChanged: root.lastTick = Date.now()
        onTriggered: {
            const now = Date.now();
            const dt = Math.max(0, Math.min(root.effectiveQuality === "economical" ? 0.2 : 0.1, (now - root.lastTick) / 1000));
            root.lastTick = now;
            root.visualTime += dt;
            root.cloudOffset += root.driftRate * dt;
        }
    }
    // Software scene graphs do not support ShaderEffect. Keep a readable sky
    // beneath it for both unsupported APIs and shader compilation failures.
    Rectangle {
        objectName: "staticAtmosphere"
        anchors.fill: parent
        gradient: Gradient {
            GradientStop {
                position: 0
                color: root.sunElevation < -8 ? "#071020" : root.fogDensity > 0.4 ? "#819aa5" : root.weatherDim > 0.3 ? "#51687f" : root.cloudCover > 0.72 ? "#5687b3" : "#337bb0"
            }
            GradientStop {
                position: 1
                color: root.sunElevation < -8 ? "#19263b" : root.fogDensity > 0.4 ? "#b8c7cc" : root.weatherDim > 0.3 ? "#879aa8" : "#bed4df"
            }
        }
        Repeater {
            model: 3
            delegate: Rectangle {
                required property int index
                x: (index * 0.37 - 0.12) * root.width
                y: (0.12 + index * 0.18) * root.height
                width: root.width * 0.58
                height: root.height * 0.13
                radius: height / 2
                color: root.isDay ? "#f0f5f8" : "#596a81"
                opacity: Math.min(0.18, Math.max(0, root.cloudCover) * 0.22)
            }
        }
    }
    Loader {
        id: shaderLoader
        anchors.fill: parent
        active: root.effectiveQuality !== "static"
        sourceComponent: Item {
            ShaderEffect {
                id: sky
                onStatusChanged: if (status === ShaderEffect.Error)
                    root.pipelineFailed = true
                visible: true
                anchors.fill: parent
                property real scene_time: root.visualTime
                property real sun_elevation: Math.max(-90, Math.min(90, root.sunElevation))
                property real sun_azimuth: root.sunAzimuth
                property real celestial_left: root.celestialLeft / Math.max(1, root.width)
                property real celestial_top: root.celestialTop / Math.max(1, root.height)
                property real cloud_cover: Math.max(0, Math.min(1, root.cloudCover))
                property real fog_density: Math.max(0, Math.min(1, root.fogDensity))
                property real cloud_offset: root.cloudOffset
                property real lightning: root.animationActive && root.lightningEnabled && root.condition === "thunderstorm" ? Math.max(0, Math.min(0.28, root.lightningIntensity)) : 0
                property bool reduced_motion: root.reducedMotion
                property real aspect_ratio: root.width / Math.max(1, root.height)
                property real weather_dim: root.weatherDim
                property real rain_amount: root.animationActive ? Math.max(0, Math.min(1, root.rainAmount)) : 0
                property real snow_amount: root.animationActive ? Math.max(0, Math.min(1, root.snowAmount)) : 0
                property real wind_x: Math.max(-500, Math.min(500, root.wind))
                fragmentShader: "qrc:/ui/shaders/atmosphere.frag.qsb"
                blending: false
            }
            ShaderEffectSource {
                anchors.fill: parent
                visible: true
                sourceItem: sky
                hideSource: true
                live: true
                smooth: true
                textureSize: root.bufferSize
            }
        }
    }
}
