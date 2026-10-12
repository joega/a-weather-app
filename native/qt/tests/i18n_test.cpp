#include <QtTest>
#include <QQmlEngine>
#include <QQmlComponent>
#include <QQmlExpression>
#include <QQmlContext>
#include <QTextLayout>
#include <QGlyphRun>
#include <QSet>
#include "../i18n.h"
class I18nTest : public QObject {
    Q_OBJECT
  private slots:
    void matching() {
        const QList<QPair<QString, QString>> cases{{"hi-IN", "hi"},
                                                   {"en-IN", "en"},
                                                   {"zh-Hant", "zh-Hant"},
                                                   {"zh-TW", "zh-Hant"},
                                                   {"zh_HK.UTF-8", "zh-Hant"},
                                                   {"zh-Hans-TW", "zh-Hans"},
                                                   {"zh-Hant-CN", "zh-Hant"},
                                                   {"ru-RU", "uk"},
                                                   {"pl-PL", "pl"},
                                                   {"ar-SA", ""},
                                                   {"zh-Latn", ""},
                                                   {"zh-Latn-TW", ""},
                                                   {"zh-Hant-u-nu-latn", "zh-Hant"},
                                                   {"zh-TW-u-nu-latn", "zh-Hant"},
                                                   {"zh-u-nu-latn", "zh-Hans"},
                                                   {"zh_TW@stroke", "zh-Hant"}};
        for (const auto& row : cases)
            QCOMPARE(NativeI18n::match(row.first), row.second);
        QCOMPARE(NativeI18n::detect({"ar-SA", "hi-IN", "en-US"}), QString("hi"));
        QCOMPARE(NativeI18n::detect({"ar-SA"}), QString("en"));
    }
    void installedScriptFontsAndShaping() {
        const QStringList scripts{
            QString::fromUtf8("日本語の天気"), QString::fromUtf8("简体中文天气"),
            QString::fromUtf8("繁體中文天氣"), QString::fromUtf8("Українська погода"),
            QString::fromUtf8("क्षेत्रीय मौसम"),  QString::fromUtf8("한국어 날씨")};
        const QStringList codes{"ja", "zh-Hans", "zh-Hant", "uk", "hi", "ko"};
        QQmlEngine engine;
        NativeI18n controller(&engine);
        for (int i = 0; i < scripts.size(); ++i) {
            const auto text = scripts[i];
            controller.setPreference(codes[i]);
            QTextLayout layout(text, QFont(controller.fontFamily(), 16));
            layout.beginLayout();
            auto line = layout.createLine();
            line.setLineWidth(800);
            layout.endLayout();
            QVERIFY(line.isValid());
            const auto runs = layout.glyphRuns();
            QVERIFY(!runs.isEmpty());
            for (const auto& run : runs) {
                QVERIFY(run.rawFont().isValid());
                for (auto glyph : run.glyphIndexes())
                    QVERIFY2(glyph != 0, qPrintable(text));
                qInfo().noquote() << text << run.rawFont().familyName()
                                  << run.glyphIndexes().size();
            }
        }
    }
    void bundledCatalogsAndPluralCategories() {
        const QStringList languages{"en",      "es", "fr", "de", "pt", "ja", "zh-Hans",
                                    "zh-Hant", "uk", "hi", "it", "ko", "id", "pl"};
        for (const auto& code : languages) {
            CatalogTranslator catalog;
            QVERIFY2(catalog.read(code), qPrintable(code));
            QVERIFY2(!catalog.translate("App", "Weather forecast").isEmpty(), qPrintable(code));
            QVERIFY(catalog.translate("Provider", "Weather forecast").isEmpty());
        }
        for (const auto& code : {QString("uk"), QString("pl")}) {
            CatalogTranslator catalog;
            QVERIFY(catalog.read(code));
            const QStringList forms = code == "uk"
                                          ? QStringList{"%n година", "%n години", "%n годин"}
                                          : QStringList{"%n godzina", "%n godziny", "%n godzin"};
            for (const auto& row : QList<QPair<int, int>>{
                     {1, 0}, {2, 1}, {5, 2}, {11, 2}, {21, code == "uk" ? 0 : 2}, {22, 1}})
                QCOMPARE(catalog.translate("App", "%n hour(s)", nullptr, row.first),
                         forms[row.second]);
        }
        CatalogTranslator hindi;
        QVERIFY(hindi.read("hi"));
        QCOMPARE(hindi.translate("App", "%n hour(s)", nullptr, 0), QString::fromUtf8("%n घंटा"));
        QCOMPARE(hindi.translate("App", "%n hour(s)", nullptr, 2), QString::fromUtf8("%n घंटे"));
    }
    void sharedJavaScriptUsesNativeRegionalFormat() {
        QQmlEngine engine;
        NativeI18n controller(&engine, QLocale("en_IN"));
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY2(scope, qPrintable(component.errorString()));
        QQmlExpression number(qmlContext(scope.data()), scope.data(),
                              "Forecast.displayNumber(1234567, 0)");
        QCOMPARE(number.evaluate().toString(), QString("12,34,567"));
        QVERIFY(!number.hasError());
        QQmlExpression time(
            qmlContext(scope.data()), scope.data(),
            "Forecast.localTime('2026-11-01T05:30:00Z', 'America/New_York', 'long')");
        QCOMPARE(time.evaluate().toString(),
                 controller.date("2026-11-01T05:30:00Z", "America/New_York", "long"));
        QVERIFY(!time.hasError());
    }
    void indianRegionalFormat() {
        QQmlEngine engine;
        NativeI18n controller(&engine, QLocale("en_IN"));
        QCOMPARE(controller.number(1234567, 0), QString("12,34,567"));
        controller.setPreference("hi");
        QCOMPARE(controller.number(1234567, 0), QString("12,34,567"));
        QCOMPARE(controller.preference(), QString("hi"));
        controller.setPreference("auto");
        QCOMPARE(controller.preference(), QString("auto"));
    }
    void regionalTimeAndDST() {
        QQmlEngine engine;
        NativeI18n controller(&engine);
        const QString before = controller.date("2026-11-01T05:30:00Z", "America/New_York", "time");
        const QString after = controller.date("2026-11-01T06:30:00Z", "America/New_York", "time");
        QCOMPARE(before, after);
        QVERIFY(!before.isEmpty());
        const auto locale = controller.formatLocale();
        const auto numeric = controller.number(1234567, 0);
        controller.setPreference("hi");
        QCOMPARE(controller.formatLocale(), locale);
        QCOMPARE(controller.number(1234567, 0), numeric);
        controller.setPreference("auto");
        QCOMPARE(controller.preference(), QString("auto"));
    }
};
QTEST_MAIN(I18nTest)
#include "i18n_test.moc"
