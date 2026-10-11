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
    property string error: ""
    property string place: ""
    property bool changingDate: false
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
                    text: I18n.t("Sun & moon")
                    font.pixelSize: Tokens.fontSize(26)
                }
                ActionButton {
                    id: closeButton
                    objectName: "closeAstronomy"
                    iconName: "close"
                    accessibleLabel: I18n.t("Close sun and moon")
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
                PlainLabel {
                    objectName: "astronomySelectedDate"
                    Layout.fillWidth: true
                    text: root.result ? (root.result.date === root.result.today ? I18n.t("Today · ") : "") + (I18n.date(root.result.date, root.result.timezone, "date") || root.result.date_label) : root.state === "loading" ? I18n.t("Today") : I18n.t("Sun & moon unavailable")
                    font.pixelSize: Tokens.fontSize(18)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                ActionButton {
                    objectName: "changeAstronomyDate"
                    text: root.changingDate ? I18n.t("Done") : I18n.t("Change date")
                    enabled: root.result !== null || root.changingDate
                    onClicked: {
                        root.changingDate = !root.changingDate;
                        if (root.changingDate)
                            Qt.callLater(() => {
                                dateInput.forceActiveFocus();
                                dateInput.selectAll();
                            });
                    }
                }
            }
            RowLayout {
                visible: root.changingDate
                Layout.fillWidth: true
                ActionButton {
                    objectName: "astronomyPrevious"
                    text: I18n.t("Previous")
                    enabled: root.result !== null && root.result.previous_date >= root.result.min_date
                    onClicked: root.requested(root.result.previous_date)
                }
                TextField {
                    id: dateInput
                    objectName: "astronomyDate"
                    Layout.fillWidth: true
                    Layout.minimumWidth: 135
                    Accessible.name: I18n.t("Astronomy date in year-month-day format")
                    placeholderText: "YYYY-MM-DD"
                    maximumLength: 10
                    selectByMouse: true
                    color: Tokens.foreground
                    font.pixelSize: Tokens.fontSize(16)
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
                    text: I18n.t("Next")
                    enabled: root.result !== null && root.result.next_date <= root.result.max_date
                    onClicked: root.requested(root.result.next_date)
                }
                ActionButton {
                    objectName: "astronomyToday"
                    text: I18n.t("Today")
                    onClicked: root.requested("")
                }
            }
            PlainLabel {
                visible: root.changingDate
                text: I18n.t("Browse 30 days before or after today")
                font.pixelSize: Tokens.fontSize(12)
                color: Tokens.secondary
            }
            Slider {
                visible: root.changingDate
                objectName: "astronomyDateScrubber"
                Layout.fillWidth: true
                from: -30
                to: 30
                stepSize: 1
                snapMode: Slider.SnapAlways
                enabled: root.result !== null
                Accessible.name: I18n.t("Date, up to 30 days before or after today")
                value: root.result ? Math.max(-30, Math.min(30, (Date.parse(root.result.date) - Date.parse(root.result.today)) / 86400000)) : 0
                onMoved: if (!pressed && root.result)
                    root.requested((I18n.revision, Astronomy.shiftedDate(root.result.today, value)))
                onPressedChanged: if (!pressed && root.result)
                    root.requested((I18n.revision, Astronomy.shiftedDate(root.result.today, value)))
            }
            PlainLabel {
                objectName: "astronomyStatus"
                visible: root.result === null
                Layout.fillWidth: true
                text: root.state === "loading" ? I18n.t("Calculating sun and moon…") : root.error === "service_unsupported" || root.error === "invalid_request" ? I18n.t("This running weather service does not support Sun & moon. Quit and reopen the updated app.") : root.error === "service_unavailable" ? I18n.t("The weather service is disconnected. Reopen the app to reconnect.") : root.error === "forecast_loading" ? I18n.t("The location is still loading. Try today again in a moment.") : root.error === "location_busy" ? I18n.t("The location is changing. Try today when it is ready.") : root.error === "location_required" ? I18n.t("Choose a location using the pin beside the city name, then open Sun & moon.") : root.error === "astronomy_context_changed" ? I18n.t("The location changed. Reopen Sun & moon for the current location.") : I18n.t("Choose a valid date within a year of today, or select Today to retry.")
                font.pixelSize: Tokens.fontSize(18)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
            ActionButton {
                objectName: "retryAstronomy"
                visible: !root.changingDate && root.state === "unavailable" && ["service_unsupported", "invalid_request", "service_unavailable", "location_required"].indexOf(root.error) < 0
                text: I18n.t("Try today")
                onClicked: root.requested("")
            }
            ColumnLayout {
                visible: root.result !== null
                Layout.fillWidth: true
                spacing: 16
                PlainLabel {
                    objectName: "astronomyDaylight"
                    Layout.fillWidth: true
                    text: !root.result ? "" : I18n.t("%1 of daylight\n%2%3").arg((I18n.revision, Astronomy.duration(root.result.daylight_seconds))).arg((Math.abs(root.result.change_seconds) < 30 ? I18n.t("About the same as the previous day") : (root.result.change_seconds > 0 ? I18n.t("%1 longer than the previous day") : I18n.t("%1 shorter than the previous day")).arg((I18n.revision, Astronomy.duration(Math.abs(root.result.change_seconds)))))).arg((root.now >= Date.parse(root.result.day_start) && root.now < Date.parse(root.result.day_end) ? "\n" + I18n.t("%1 remaining today").arg((I18n.revision, Astronomy.duration((I18n.revision, Astronomy.remaining(root.result, root.now))))) : ""))
                    font.pixelSize: Tokens.fontSize(19)
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "astronomySunTimes"
                    Layout.fillWidth: true
                    text: root.result ? (I18n.revision, Astronomy.horizon(root.result, "sun")) : ""
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
                        text: !root.result ? "" : I18n.t("%1 · %2% illuminated\nAt %3 on this date\n%4").arg((I18n.revision, Astronomy.phaseName(root.result.phase))).arg(I18n.number(Math.round(root.result.illumination * 100))).arg((I18n.date(root.result.phase_at, root.result.timezone, "time") || root.result.phase_label)).arg((I18n.revision, Astronomy.horizon(root.result, "moon")))
                        wrapMode: Text.Wrap
                        elide: Text.ElideNone
                    }
                }
                PlainLabel {
                    Layout.fillWidth: true
                    text: I18n.t("Twilight & light")
                    font.pixelSize: Tokens.fontSize(20)
                }
                PlainLabel {
                    objectName: "astronomyTwilight"
                    Layout.fillWidth: true
                    text: !root.result ? "" : I18n.t("Civil (−6°): %1\nNautical (−12°): %2\nAstronomical (−18°): %3").arg((I18n.revision, Astronomy.events(root.result.civil_events, I18n.t("Dawn"), I18n.t("Dusk")))).arg((I18n.revision, Astronomy.events(root.result.nautical_events, I18n.t("Dawn"), I18n.t("Dusk")))).arg((I18n.revision, Astronomy.events(root.result.astronomical_events, I18n.t("Dawn"), I18n.t("Dusk"))))
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
                PlainLabel {
                    objectName: "astronomyGolden"
                    Layout.fillWidth: true
                    text: !root.result ? "" : I18n.t("Golden light (−4° to +6°)\n%1").arg((root.result.golden.length ? root.result.golden.map(s => s.label).join("\n") : I18n.t("No window on this date")))
                    wrapMode: Text.Wrap
                    elide: Text.ElideNone
                }
            }
            PlainLabel {
                Layout.fillWidth: true
                text: I18n.t("Calculated locally using SunCalc/Meeus formulas. Times assume a level horizon and standard refraction; terrain, altitude and weather can change what you see. Golden light is a sun-angle window, not a clear-sky forecast. Moon shape is a schematic phase view. All times use this place’s timezone.")
                color: Tokens.secondary
                font.pixelSize: Tokens.fontSize(12)
                wrapMode: Text.Wrap
                elide: Text.ElideNone
            }
        }
    }
}
