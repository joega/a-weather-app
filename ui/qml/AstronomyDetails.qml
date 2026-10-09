pragma ComponentBehavior: Bound
import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import "Astronomy.js" as Astronomy

Popup {
    id: root
    objectName: "astronomyDetails"
    parent: Overlay.overlay
    anchors.centerIn: parent
    width: Math.min(740, parent ? parent.width - 40 : 740)
    height: Math.min(740, parent ? parent.height - 40 : 740)
    padding: 24
    modal: true
    focus: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    property var result: null
    property string state: "loading"
    property string place: ""
    property double now: Date.now()
    signal requested(string date)
    function revealFocus(item) {
        const flick = scroll.contentItem as Flickable;
        if (!flick || !item)
            return;
        let ancestor = item;
        while (ancestor && ancestor !== flick.contentItem)
            ancestor = ancestor.parent;
        if (!ancestor)
            return;
        const y = item.mapToItem(flick.contentItem, 0, 0).y;
        if (y < flick.contentY + 12)
            flick.contentY = Math.max(0, y - 12);
        else if (y + item.height > flick.contentY + flick.height - 12)
            flick.contentY = Math.min(flick.contentHeight - flick.height, y + item.height - flick.height + 12);
    }
    onOpened: closeButton.forceActiveFocus()
    onResultChanged: {
        now = Date.now();
        if (result)
            dateInput.text = result.date;
    }
    // Only this visible popup owns a minute clock, for the remaining daylight
    // label. Astronomy is not recalculated or fetched on each clock tick.
    Timer {
        interval: 60000
        repeat: true
        running: root.visible
        onTriggered: root.now = Date.now()
    }
    background: Rectangle {
        color: "#2c455a"
        radius: 18
        border.color: Tokens.border
    }
    Overlay.modal: Rectangle {
        color: "#990b1725"
    }
    contentItem: ScrollView {
        id: scroll
        objectName: "astronomyScroll"
        clip: true
        contentWidth: availableWidth
        contentHeight: body.implicitHeight
        rightPadding: 18
        ScrollBar.vertical.active: true
        ScrollBar.vertical.policy: ScrollBar.AsNeeded
        ScrollBar.horizontal.policy: ScrollBar.AlwaysOff
        Keys.onPressed: event => {
            const flick = scroll.contentItem as Flickable;
            if (!flick || [Qt.Key_PageDown, Qt.Key_PageUp].indexOf(event.key) < 0)
                return;
            flick.contentY = Math.max(0, Math.min(flick.contentHeight - flick.height, flick.contentY + (event.key === Qt.Key_PageDown ? 1 : -1) * flick.height * 0.8));
            event.accepted = true;
        }
        ColumnLayout {
            id: body
            width: scroll.availableWidth
            spacing: 16
            RowLayout {
                Layout.fillWidth: true
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Sun & moon"
                    font.pixelSize: 26
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeAstronomy"
                    iconName: "close"
                    accessibleLabel: "Close sun and moon"
                    onClicked: root.close()
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: root.place + (root.result ? " · " + root.result.timezone : "")
                color: Tokens.secondary
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            RowLayout {
                Layout.fillWidth: true
                ActionButton {
                    objectName: "astronomyPrevious"
                    text: "Previous"
                    enabled: root.result !== null && root.result.previous_date >= root.result.min_date
                    onClicked: root.requested(root.result.previous_date)
                }
                TextField {
                    id: dateInput
                    objectName: "astronomyDate"
                    Layout.fillWidth: true
                    Layout.minimumWidth: 135
                    Accessible.name: "Astronomy date in year-month-day format"
                    placeholderText: "YYYY-MM-DD"
                    maximumLength: 10
                    selectByMouse: true
                    color: Tokens.foreground
                    font.pixelSize: 16
                    padding: 10
                    background: Rectangle {
                        color: "#20384b"
                        radius: 8
                        border.color: dateInput.activeFocus ? Tokens.accent : Tokens.border
                    }
                    onAccepted: root.requested(text)
                }
                ActionButton {
                    objectName: "astronomyNext"
                    text: "Next"
                    enabled: root.result !== null && root.result.next_date <= root.result.max_date
                    onClicked: root.requested(root.result.next_date)
                }
                ActionButton {
                    objectName: "astronomyToday"
                    text: "Today"
                    onClicked: root.requested("")
                }
            }
            PlainLabel {
                text: "Browse 30 days before or after today"
                font.pixelSize: 12
                color: Tokens.secondary
            }
            Slider {
                objectName: "astronomyDateScrubber"
                Layout.fillWidth: true
                from: -30
                to: 30
                stepSize: 1
                snapMode: Slider.SnapAlways
                enabled: root.result !== null
                Accessible.name: "Date, up to 30 days before or after today"
                value: root.result ? Math.max(-30, Math.min(30, (Date.parse(root.result.date) - Date.parse(root.result.today)) / 86400000)) : 0
                onMoved: if (!pressed && root.result)
                    root.requested(Astronomy.shiftedDate(root.result.today, value))
                onPressedChanged: if (!pressed && root.result)
                    root.requested(Astronomy.shiftedDate(root.result.today, value))
            }
            PlainLabel {
                objectName: "astronomyStatus"
                Layout.fillWidth: true
                text: root.result ? root.result.date_label : root.state === "loading" ? "Calculating sun and moon…" : "Unavailable. Choose a valid date within a year of today, or select Today to retry."
                font.pixelSize: 18
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ColumnLayout {
                visible: root.result !== null
                Layout.fillWidth: true
                spacing: 16
                PlainLabel {
                    objectName: "astronomyDaylight"
                    Layout.fillWidth: true
                    text: !root.result ? "" : Astronomy.duration(root.result.daylight_seconds) + " of daylight\n" + (Math.abs(root.result.change_seconds) < 30 ? "About the same as the previous day" : Astronomy.duration(Math.abs(root.result.change_seconds)) + (root.result.change_seconds > 0 ? " longer" : " shorter") + " than the previous day") + (root.now >= Date.parse(root.result.day_start) && root.now < Date.parse(root.result.day_end) ? "\n" + Astronomy.duration(Astronomy.remaining(root.result, root.now)) + " remaining today" : "")
                    font.pixelSize: 19
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "astronomySunTimes"
                    Layout.fillWidth: true
                    text: root.result ? Astronomy.horizon(root.result, "sun") : ""
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                RowLayout {
                    Layout.fillWidth: true
                    spacing: 20
                    Canvas {
                        id: moonDisc
                        objectName: "astronomyMoonDisc"
                        Layout.preferredWidth: 72
                        Layout.preferredHeight: 72
                        property real fraction: root.result ? root.result.illumination : 0
                        property bool waxing: root.result ? root.result.phase < 0.5 : true
                        onFractionChanged: requestPaint()
                        onWaxingChanged: requestPaint()
                        onPaint: {
                            const ctx = getContext("2d");
                            ctx.reset();
                            ctx.fillStyle = "#162e43";
                            ctx.beginPath();
                            ctx.arc(36, 36, 34, 0, 2 * Math.PI);
                            ctx.fill();
                            ctx.fillStyle = "#e7eff6";
                            for (let y = -34; y < 34; y += 0.5) {
                                const x = Math.sqrt(34 * 34 - y * y), terminator = (1 - 2 * moonDisc.fraction) * x;
                                ctx.fillRect(36 + (moonDisc.waxing ? terminator : -x), 36 + y, x - terminator, 0.6);
                            }
                        }
                    }
                    PlainLabel {
                        objectName: "astronomyMoonPhase"
                        Layout.fillWidth: true
                        text: !root.result ? "" : Astronomy.phaseName(root.result.phase) + " · " + Math.round(root.result.illumination * 100) + "% illuminated\nAt " + root.result.phase_label + " on this date\n" + Astronomy.horizon(root.result, "moon")
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: "Twilight & light"
                    font.pixelSize: 20
                }
                PlainLabel {
                    objectName: "astronomyTwilight"
                    Layout.fillWidth: true
                    text: !root.result ? "" : "Civil (−6°): " + Astronomy.events(root.result.civil_events, "Dawn", "Dusk") + "\nNautical (−12°): " + Astronomy.events(root.result.nautical_events, "Dawn", "Dusk") + "\nAstronomical (−18°): " + Astronomy.events(root.result.astronomical_events, "Dawn", "Dusk")
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "astronomyGolden"
                    Layout.fillWidth: true
                    text: !root.result ? "" : "Golden light (−4° to +6°)\n" + (root.result.golden.length ? root.result.golden.map(s => s.label).join("\n") : "No window on this date")
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: "Calculated locally using SunCalc/Meeus formulas. Times assume a level horizon and standard refraction; terrain, altitude and weather can change what you see. Golden light is a sun-angle window, not a clear-sky forecast. Moon shape is a schematic phase view. All times use this place’s timezone."
                color: Tokens.secondary
                font.pixelSize: 12
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
