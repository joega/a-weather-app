pragma ComponentBehavior: Bound

import QtQuick

SettingsComboBox {
    id: root
    property string units: "F"
    property bool automaticUnits: true
    property bool compact: true
    signal chosen(var patch)
    implicitWidth: (compact ? 124 : 240) * Tokens.textScale
    implicitHeight: Math.max(40, contentItem.implicitHeight + topPadding + bottomPadding)
    font.pixelSize: Tokens.fontSize(compact ? 15 : 17)
    popupWidth: Math.max(width, 220)
    model: [
        {
            label: "Auto (°" + root.units + ")",
            value: "auto"
        },
        {
            label: "Fahrenheit (°F)",
            value: "F"
        },
        {
            label: "Celsius (°C)",
            value: "C"
        }
    ]
    textRole: "label"
    valueRole: "value"
    currentIndex: automaticUnits ? 0 : units === "F" ? 1 : 2
    displayText: automaticUnits ? "Auto (°" + units + ")" : compact ? "°" + units : units === "F" ? "Fahrenheit (°F)" : "Celsius (°C)"
    Accessible.name: "Measurement units"
    onActivated: {
        if (currentValue === "auto")
            chosen({
                units_mode: "auto"
            });
        else
            chosen({
                units_mode: "manual",
                units: currentValue
            });
    }
}
