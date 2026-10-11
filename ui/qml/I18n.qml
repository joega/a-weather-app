pragma Singleton
import QtQuick

QtObject {
    id: root
    property bool nativeAvailable: typeof nativeI18n !== "undefined" && nativeI18n !== null
    property int revision: 0
    property string language: nativeAvailable ? nativeI18n.language : "en"
    property string fontFamily: nativeAvailable ? nativeI18n.fontFamily : "sans-serif"
    property string preference: nativeAvailable ? nativeI18n.preference : "auto"
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
        target: root.nativeAvailable ? nativeI18n : null
        function onChanged() {
            root.revision++;
        }
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
            nativeI18n.setPreference(code);
        else {
            preference = code;
            language = code === "auto" ? "en" : code;
            revision++;
        }
        preferenceRequested(code);
    }
    function sync(choice, effective) {
        if (nativeAvailable)
            nativeI18n.sync(choice, effective);
        else if (preference !== choice || language !== effective) {
            preference = choice;
            language = effective;
            revision++;
        }
    }
    function number(value, decimals) {
        const generation = revision;
        return nativeAvailable ? nativeI18n.number(value, decimals || 0) : Number(value).toLocaleString(Qt.locale(), "f", decimals || 0);
    }
    function date(stamp, zone, kind) {
        const generation = revision;
        return nativeAvailable ? nativeI18n.date(stamp, zone, kind || "time") : "";
    }
}
