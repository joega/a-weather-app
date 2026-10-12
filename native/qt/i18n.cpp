#include "i18n.h"
#include <QCoreApplication>
#include <QQmlEngine>
#include <QXmlStreamReader>
#include <QFile>
#include <QDateTime>
#include <QTimeZone>
#include <QGuiApplication>
#include <QFontDatabase>

bool CatalogTranslator::read(const QString& code) {
    entries.clear();
    language = code;
    QFile file(":/i18n/app_" + code + ".ts");
    if (!file.open(QIODevice::ReadOnly))
        return false;
    QXmlStreamReader xml(&file);
    QString source;
    QStringList forms;
    QString text;
    while (!xml.atEnd()) {
        xml.readNext();
        if (xml.isStartElement() && xml.name() == u"message") {
            source.clear();
            forms.clear();
            text.clear();
        } else if (xml.isStartElement() && xml.name() == u"source")
            source = xml.readElementText();
        else if (xml.isStartElement() && xml.name() == u"translation") {
            if (xml.attributes().value("type") == u"unfinished") {
                xml.skipCurrentElement();
                continue;
            }
            while (!xml.atEnd()) {
                xml.readNext();
                if (xml.isStartElement() && xml.name() == u"numerusform")
                    forms.append(xml.readElementText());
                else if (xml.isCharacters() && forms.isEmpty())
                    text += xml.text().toString();
                else if (xml.isEndElement() && xml.name() == u"translation") {
                    if (forms.isEmpty() && !text.isEmpty())
                        forms.append(text);
                    break;
                }
            }
        } else if (xml.isEndElement() && xml.name() == u"message" && !source.isEmpty() &&
                   !forms.isEmpty())
            entries.insert(source, forms);
    }
    if (xml.hasError()) {
        entries.clear();
        return false;
    }
    return !entries.isEmpty();
}
QString CatalogTranslator::translate(const char* context, const char* source, const char*,
                                     int n) const {
    if (qstrcmp(context, "App") != 0)
        return {};
    const auto forms = entries.value(QString::fromUtf8(source));
    if (forms.isEmpty())
        return {};
    int index = 0;
    if (n >= 0 && forms.size() > 1) {
        if (language == "uk" || language == "pl") {
            const bool one = language == "uk" ? n % 10 == 1 && n % 100 != 11 : n == 1;
            const bool few = n % 10 >= 2 && n % 10 <= 4 && !(n % 100 >= 12 && n % 100 <= 14);
            index = one ? 0 : few ? 1 : 2;
        } else if (language == "fr" || language == "pt" || language == "hi")
            index = n > 1 ? 1 : 0;
        else
            index = n == 1 ? 0 : 1;
    }
    return forms.value(qMin(index, int(forms.size()) - 1));
}
QString NativeI18n::match(const QString& input) {
    const QString tag = input.trimmed().replace('_', '-').section('.', 0, 0).section('@', 0, 0);
    const QString base = tag.section('-', 0, 0).toLower();
    if (base == "ru")
        return "uk";
    if (base == "zh") {
        auto parts = tag.toLower().split('-');
        for (int i = 1; i < parts.size(); ++i) {
            if (parts[i].size() == 1) {
                parts = parts.mid(0, i);
                break;
            }
        }
        if (parts.contains("hant"))
            return "zh-Hant";
        if (parts.contains("hans"))
            return "zh-Hans";
        for (const auto& part : parts)
            if (part.size() == 4)
                return {};
        if (parts.contains("tw") || parts.contains("hk") || parts.contains("mo"))
            return "zh-Hant";
        return "zh-Hans";
    }
    const QStringList supported{"en", "es", "fr", "de", "pt", "ja",
                                "uk", "hi", "it", "ko", "id", "pl"};
    return supported.contains(base) ? base : QString();
}
QString NativeI18n::detect(const QStringList& preferences) {
    for (const auto& tag : preferences) {
        const auto code = match(tag);
        if (!code.isEmpty())
            return code;
    }
    return "en";
}
NativeI18n::NativeI18n(QQmlEngine* target, const QLocale& formatting)
    : engine(target), regional(formatting) {
    auto preferences = qEnvironmentVariable("LANGUAGE").split(':', Qt::SkipEmptyParts);
    preferences.append(QLocale::system().uiLanguages());
    automatic = detect(preferences);
    // Shared .pragma library scripts have no component context. A per-engine
    // global gives the same owned native formatter to stateless JS helpers.
    // Keep C++ ownership: this object can be a stack-owned application member.
    QQmlEngine::setObjectOwnership(this, QQmlEngine::CppOwnership);
    if (engine) {
        const auto formatter = engine->newQObject(this);
        engine->globalObject().setProperty("weatherFormatNumber", formatter.property("number"));
        engine->globalObject().setProperty("weatherFormatDate", formatter.property("date"));
    }
    QCoreApplication::installTranslator(&translator);
    apply("auto", automatic);
}
NativeI18n::~NativeI18n() {
    QCoreApplication::removeTranslator(&translator);
}
void NativeI18n::setPreference(const QString& value) {
    apply(value, value == "auto" ? automatic : match(value));
}
void NativeI18n::sync(const QString& value, const QString& resolved) {
    apply(value, match(resolved));
}
void NativeI18n::apply(const QString& value, const QString& resolved) {
    const QString selected = value == "auto" ? "auto" : match(value);
    if (selected.isEmpty())
        return;
    QString code = resolved.isEmpty() ? "en" : resolved;
    if (choice == selected && effective == code && !translator.isEmpty())
        return;
    if (!translator.read(code)) {
        code = "en";
        translator.read(code);
    }
    choice = selected;
    effective = code;
    QFont font = QGuiApplication::font();
    font.setFamily(fontFamily());
    QGuiApplication::setFont(font);
    if (engine)
        engine->retranslate();
    emit changed();
}
QString NativeI18n::number(double value, int decimals) const {
    return regional.toString(value, 'f', qBound(0, decimals, 6));
}
QString NativeI18n::date(const QString& stamp, const QString& zone, const QString& kind) const {
    if (stamp.size() == 10) {
        const QDate date = QDate::fromString(stamp, Qt::ISODate);
        if (date.isValid())
            return kind == "day"
                       ? QLocale(effective).dayName(date.dayOfWeek(), QLocale::ShortFormat) + " " +
                             regional.toString(date, QLocale::ShortFormat)
                       : regional.toString(date, QLocale::ShortFormat);
    }
    QDateTime instant = QDateTime::fromString(stamp, Qt::ISODateWithMs);
    if (!instant.isValid())
        instant = QDateTime::fromString(stamp, Qt::ISODate);
    const QTimeZone tz(zone.toUtf8());
    if (!instant.isValid() || !tz.isValid())
        return QCoreApplication::translate("App", "Time unavailable");
    const auto local = instant.toTimeZone(tz);
    if (kind == "day")
        return QLocale(effective).dayName(local.date().dayOfWeek(), QLocale::ShortFormat) + " " +
               regional.toString(local.date(), QLocale::ShortFormat);
    if (kind == "date")
        return regional.toString(local.date(), QLocale::ShortFormat);
    if (kind == "long" || kind == "datetime")
        return regional.toString(local, QLocale::ShortFormat) + " " + local.timeZoneAbbreviation();
    return regional.toString(local.time(), QLocale::ShortFormat);
}

QString NativeI18n::fontFamily() const {
    const QHash<QString, QString> families{{"ja", "Noto Sans CJK JP"},
                                           {"zh-Hans", "Noto Sans CJK SC"},
                                           {"zh-Hant", "Noto Sans CJK TC"},
                                           {"ko", "Noto Sans CJK KR"},
                                           {"hi", "Noto Sans Devanagari"}};
    const auto candidate = families.value(effective);
    static const auto installed = QFontDatabase::families();
    return !candidate.isEmpty() && installed.contains(candidate) ? candidate
                                                                 : QString("sans-serif");
}
