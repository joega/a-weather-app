pragma ComponentBehavior: Bound

import QtQuick
import QtQuick.Controls

SettingsComboBox {
    id: root
    property string units: "F"
    property bool automaticUnits: true
    property bool compact: true
    property bool abbreviated: false
    hoverEnabled: true
    signal chosen(var patch)
    implicitWidth: abbreviated ? Math.max(44, contentItem.implicitWidth + leftPadding + rightPadding) : (compact ? 124 : 240) * Tokens.textScale
    leftPadding: abbreviated ? 10 : 12
    indicatorRightMargin: abbreviated ? 8 : 12
    rightPadding: abbreviated ? indicator.width + indicatorRightMargin + 6 : 36
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
    displayText: abbreviated ? "°" + units : automaticUnits ? "Auto (°" + units + ")" : compact ? "°" + units : units === "F" ? "Fahrenheit (°F)" : "Celsius (°C)"
    Accessible.name: "Measurement units: " + (automaticUnits ? "Automatic (°" + units + ")" : units === "F" ? "Fahrenheit" : "Celsius")
    ToolTip {
        id: tooltip
        visible: root.abbreviated && root.enabled && root.hovered && !root.popup.visible
        text: root.Accessible.name
        delay: 600
        padding: 10
        contentItem: PlainLabel {
            text: tooltip.text
            color: Tokens.foreground
            font.pixelSize: Tokens.fontSize(13)
        }
        background: Rectangle {
            radius: 8
            color: "#162b3e"
            border.color: Tokens.border
        }
    }
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
