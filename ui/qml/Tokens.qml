pragma Singleton
import QtQuick

QtObject {
    property real textScale: 1
    property bool highContrast: false
    function fontSize(base) {
        return Math.round(base * textScale);
    }
    readonly property color foreground: "#f0f5fc"
    readonly property color secondary: highContrast ? "#e0edf5" : "#b7cbdc"
    readonly property color accent: "#8de4f6"
    readonly property color gold: "#f5d678"
    readonly property color panel: highContrast ? "#244055" : "#b8385369"
    readonly property color border: highContrast ? "#a6c5dc" : "#59aec5d4"
}
