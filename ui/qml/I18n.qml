pragma Singleton
import QtQuick

QtObject {
    id: root
    // The application injects this QObject through QQmlContext. Standalone
    // component fixtures deliberately omit it and retain usable English.
    // qmllint disable unqualified
    property var nativeController: typeof nativeI18n !== "undefined" ? nativeI18n : null
    // qmllint enable unqualified
    property bool nativeAvailable: root.nativeController !== null
    property int revision: 0
    property string language: nativeAvailable ? root.nativeController.language : "en"
    property string fontFamily: nativeAvailable ? root.nativeController.fontFamily : "sans-serif"
    property string preference: nativeAvailable ? root.nativeController.preference : "auto"
    property var languages: [
        {
            code: "auto",
            name: t("System / Automatic")
        },
        {
            code: "en",
            name: "English"
        },
        {
            code: "es",
            name: "Español"
        },
        {
            code: "fr",
            name: "Français"
        },
        {
            code: "de",
            name: "Deutsch"
        },
        {
            code: "pt",
            name: "Português"
        },
        {
            code: "ja",
            name: "日本語"
        },
        {
            code: "zh-Hans",
            name: "简体中文"
        },
        {
            code: "zh-Hant",
            name: "繁體中文"
        },
        {
            code: "uk",
            name: "Українська"
        },
        {
            code: "hi",
            name: "हिन्दी"
        },
        {
            code: "it",
            name: "Italiano"
        },
        {
            code: "ko",
            name: "한국어"
        },
        {
            code: "id",
            name: "Bahasa Indonesia"
        },
        {
            code: "pl",
            name: "Polski"
        }
    ]
    signal preferenceRequested(string code)
    property var nativeConnection: Connections {
        target: root.nativeAvailable ? root.nativeController : null
        function onChanged() {
            root.revision++;
        }
    }
    function observe(value) {
        const generation = root.revision;
        return value;
    }
    function source(text) {
        return text;
    }
    function t(text, n) {
        const generation = revision;
        return qsTranslate("App", String(text || ""), "", n === undefined ? -1 : n);
    }
    function setPreference(code) {
        if (nativeAvailable)
            root.nativeController.setPreference(code);
        else {
            preference = code;
            language = code === "auto" ? "en" : code;
            revision++;
        }
        preferenceRequested(code);
    }
    function sync(choice, effective) {
        if (nativeAvailable)
            root.nativeController.sync(choice, effective);
        else if (preference !== choice || language !== effective) {
            preference = choice;
            language = effective;
            revision++;
        }
    }
    function number(value, decimals) {
        const generation = revision;
        return nativeAvailable ? root.nativeController.number(value, decimals || 0) : Number(value).toLocaleString(Qt.locale(), "f", decimals || 0);
    }
    function date(stamp, zone, kind) {
        const generation = revision;
        return nativeAvailable ? root.nativeController.date(stamp, zone, kind || "time") : "";
    }
}
