#include <QtTest>
#include <QClipboard>
#include <QImageReader>
#include <QAccessible>
#include "transport.h"
#include "maptiles.h"
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQmlExpression>
#include <QQuickWindow>
#include <QQuickItem>
#include <QProcess>
#include <QTemporaryDir>
#include <QFile>
#include <QSaveFile>
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonArray>
#include <QDir>
#include <QStandardPaths>
#include <unistd.h>
#include <functional>
#include <memory>

static void teardownTrace(const char* phase) {
    if (qEnvironmentVariableIsSet("WEATHER_QT_TEARDOWN_TRACE"))
        qInfo() << "TEARDOWN" << phase;
}

// Private offline fixtures have no precipitation and never request native effects.
// Each test owns its service process; even failed assertions leave no service running.
class ServiceFixture {
  public:
    QTemporaryDir directory;
    QString state, socket;
    QProcess service;
    ServiceFixture() {
        state = directory.path() + "/state";
        QDir().mkdir(state);
        QFile::setPermissions(state, QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner);
    }
    ~ServiceFixture() {
        teardownTrace("fixture begin");
        if (service.state() != QProcess::NotRunning) {
            service.terminate();
            if (!service.waitForFinished(3000)) {
                service.kill();
                service.waitForFinished(2000);
            }
        }
        teardownTrace("fixture stopped");
    }
    void save(const QString& name, const QJsonObject& object) {
        QFile file(state + "/" + name);
        if (!file.open(QIODevice::WriteOnly))
            qFatal("Fixture write failed");
        file.setPermissions(QFile::ReadOwner | QFile::WriteOwner);
        file.write(QJsonDocument(object).toJson(QJsonDocument::Compact));
    }
    bool start(bool offline = true) {
        const QString app = qEnvironmentVariable("GO_APP");
        QProcess query;
        query.start(app, {"--print-socket", "--state-dir", state});
        if (!query.waitForFinished(3000) || query.exitCode() != 0)
            return false;
        socket = QString::fromUtf8(query.readAllStandardOutput()).trimmed();
        auto env = QProcessEnvironment::systemEnvironment();
        env.remove("HYPRLAND_INSTANCE_SIGNATURE");
        env.remove("WAYLAND_DISPLAY");
        env.remove("DISPLAY");
        env.remove("DBUS_SESSION_BUS_ADDRESS");
        service.setProcessEnvironment(env);
        service.setProcessChannelMode(QProcess::MergedChannels);
        QStringList arguments{"--service",   "--headless", "--duration", offline ? "60" : "120",
                              "--state-dir", state};
        // Let the executable select its own root. A packaged GO_APP must verify
        // its assembled runtime, while development builds find their checkout.
        // The test source's go.mod belongs to neither a relocated runtime nor
        // its verified runtime inventory and must not override root discovery.
        if (offline)
            arguments.append("--offline");
        service.start(app, arguments);
        if (!service.waitForStarted(3000))
            return false;
        QElapsedTimer timer;
        timer.start();
        while (timer.elapsed() < 5000 && !QFile::exists(socket) &&
               service.state() != QProcess::NotRunning)
            QTest::qWait(10);
        return QFile::exists(socket);
    }
    QJsonObject saved(const QString& name) {
        QFile f(state + "/" + name);
        if (!f.open(QIODevice::ReadOnly))
            return {};
        return QJsonDocument::fromJson(f.readAll()).object();
    }
    void cache(int age, bool pointFields = false) {
        auto now = QDateTime::currentDateTimeUtc();
        QString stamp = now.toString(Qt::ISODate);
        QJsonObject location{{"name", "New York, NY"},
                             {"latitude", 40.7128},
                             {"longitude", -74.006},
                             {"timezone", "America/New_York"}};
        QJsonObject row{{"time", stamp},
                        {"condition", "clear"},
                        {"is_day", true},
                        {"temperature_c", 15},
                        {"apparent_temperature_c", 14},
                        {"humidity", .5},
                        {"cloud_cover", .2},
                        {"precipitation_rate_mm_hr", 0},
                        {"precipitation_probability", 0},
                        {"visibility_m", 10000},
                        {"wind_speed_m_s", 1},
                        {"wind_direction_deg", 180},
                        {"wind_gust_m_s", 2}};
        if (pointFields) {
            row["uv_index"] = 0;
            row["pressure_msl_hpa"] = 1013.2;
            row["dew_point_c"] = 12.5;
        }
        QJsonObject day{{"date", now.date().toString(Qt::ISODate)},
                        {"condition", "clear"},
                        {"high_c", 20},
                        {"low_c", 10},
                        {"precipitation_probability", 0},
                        {"sunrise", QJsonValue::Null},
                        {"sunset", QJsonValue::Null}};
        auto hour = row;
        hour["time"] = now.addSecs(3600).toString(Qt::ISODate);
        if (pointFields) {
            hour["uv_index"] = 3.2;
            hour["pressure_msl_hpa"] = 1008.8;
            hour["dew_point_c"] = 9.5;
        }
        save(
            "forecast.json",
            {{"schema_version", 1},
             {"location", location},
             {"fetched_at", now.addSecs(-age).toString(Qt::ISODate)},
             {"source", QJsonObject{{"name", "Open-Meteo"},
                                    {"attribution", "Weather data by Open-Meteo.com (CC BY 4.0)"}}},
             {"current", row},
             {"hourly", QJsonArray{hour}},
             {"daily", QJsonArray{day}},
             {"alerts", QJsonObject{{"status", "unavailable"}, {"items", QJsonArray{}}}}});
    }
    void savedBerlinPlace() {
        cache(60);
        auto forecast = saved("forecast.json");
        QJsonObject location{{"name", "Berlin, Berlin, Germany"},
                             {"latitude", 52.52},
                             {"longitude", 13.41},
                             {"timezone", "Europe/Berlin"}};
        forecast["location"] = location;
        save("location-profile.json",
             {{"schema_version", 2},
              {"mode", "place"},
              {"zip_code", QJsonValue::Null},
              {"location", location},
              {"forecast", forecast},
              {"country_code", "DE"},
              {"place", QJsonObject{{"provider", "open-meteo"}, {"id", 2950159}}}});
    }
    void savedNewYorkPlace() {
        const auto forecast = saved("forecast.json");
        const auto location = forecast["location"].toObject();
        save("location-profile.json",
             {{"schema_version", 2},
              {"mode", "place"},
              {"zip_code", QJsonValue::Null},
              {"location", location},
              {"forecast", forecast},
              {"country_code", "US"},
              {"place", QJsonObject{{"provider", "open-meteo"}, {"id", 5128581}}}});
    }
    void savedLondonPlace(int age = 60, double temperature = 15.5) {
        cache(age);
        auto forecast = saved("forecast.json");
        auto current = forecast["current"].toObject();
        current["temperature_c"] = temperature;
        forecast["current"] = current;
        QJsonObject location{{"name", "London, England, United Kingdom"},
                             {"latitude", 51.5085},
                             {"longitude", -.1257},
                             {"timezone", "Europe/London"}};
        forecast["location"] = location;
        save("location-profile.json",
             {{"schema_version", 2},
              {"mode", "place"},
              {"zip_code", QJsonValue::Null},
              {"location", location},
              {"forecast", forecast},
              {"country_code", "GB"},
              {"place", QJsonObject{{"provider", "open-meteo"}, {"id", 2643743}}}});
    }
    void savedCities(int count = 20) {
        cache(60);
        const auto source = saved("forecast.json");
        QJsonArray places, order;
        for (int i = 0; i < count; ++i) {
            const auto id = QString("place-%1").arg(100 + i);
            const auto token = QString("%1").arg(i + 1, 32, 16, QLatin1Char('0'));
            auto location = source["location"].toObject();
            location["name"] = QString("City %1").arg(i);
            location["latitude"] = 20 + i;
            location["timezone"] = i == 1 ? "Europe/London" : "America/New_York";
            QJsonObject profile{
                {"schema_version", 2},
                {"mode", "place"},
                {"zip_code", QJsonValue::Null},
                {"location", location},
                {"forecast", QJsonValue::Null},
                {"country_code", i == 1 ? "GB" : "US"},
                {"place", QJsonObject{{"provider", "open-meteo"}, {"id", 100 + i}}}};
            QJsonObject entry{{"id", id},
                              {"label", ""},
                              {"profile", profile},
                              {"cache_slot", QJsonValue::Null},
                              {"cache_token", QJsonValue::Null},
                              {"summary", QJsonValue::Null}};
            if (i < 4) {
                auto forecast = source;
                forecast["location"] = location;
                auto current = forecast["current"].toObject();
                current["temperature_c"] = 15 + i;
                forecast["current"] = current;
                auto cachedProfile = profile;
                cachedProfile["forecast"] = forecast;
                save(QString("saved-forecast-%1.json").arg(i),
                     {{"schema_version", 1}, {"token", token}, {"profile", cachedProfile}});
                entry["cache_slot"] = i;
                entry["cache_token"] = token;
                entry["summary"] =
                    QJsonObject{{"temperature_c", 15 + i},
                                {"condition", "clear"},
                                {"is_day", true},
                                {"fetched_at", source["fetched_at"]},
                                {"valid_at", current["time"]},
                                {"alert_expires", QJsonValue::Null},
                                {"alert_fetched_at", QJsonValue::Null},
                                {"alert_status", i == 1 ? "not_supported_here" : "unavailable"}};
                order.append(id);
            }
            places.append(entry);
        }
        save("saved-locations.json", {{"schema_version", 1},
                                      {"generation", QString(32, 'a')},
                                      {"primary", "place-100"},
                                      {"viewed", "place-100"},
                                      {"places", places},
                                      {"cache_order", order}});
    }
    void airQualityCache(int fetchedAge, int validAge) {
        const auto now = QDateTime::currentDateTimeUtc();
        auto location = saved("forecast.json")["location"].toObject();
        save("air-quality.json",
             {{"schema_version", 1},
              {"location", location},
              {"fetched_at", now.addSecs(-fetchedAge).toString(Qt::ISODate)},
              {"valid_at", now.addSecs(-validAge).toString(Qt::ISODate)},
              {"domain", "cams_global"},
              {"source",
               QJsonObject{{"provider", "Open-Meteo"},
                           {"model", "CAMS global model data"},
                           {"kind", "model_forecast"},
                           {"attribution", "CAMS global model data via Open-Meteo (CC BY 4.0)"}}},
              {"units", QJsonObject{{"us_aqi", "USAQI"},
                                    {"european_aqi", "EAQI"},
                                    {"pm2_5_ug_m3", QString::fromUtf8("μg/m³")}}},
              {"us_aqi", 0},
              {"european_aqi", 125},
              {"pm2_5_ug_m3", 12.4}});
    }
};

// Load the real widget in Quickshell with only its Omarchy presentation API
// stubbed. The helper is real; background provider requests are disabled.
class BarFixture {
  public:
    QTemporaryDir directory;
    QProcess process;
    QByteArray output;
    ~BarFixture() {
        if (process.state() != QProcess::NotRunning) {
            process.terminate();
            if (!process.waitForFinished(3000)) {
                process.kill();
                process.waitForFinished(2000);
            }
        }
    }
    bool write(const QString& name, const QByteArray& data) {
        QFile file(directory.path() + "/" + name);
        return file.open(QIODevice::WriteOnly) && file.write(data) == data.size();
    }
    bool start(const QString& state) {
        const auto widget =
            QFINDTESTDATA("../../../quickshell/a-weather-app.weather/WeatherWidget.qml");
        if (widget.isEmpty())
            return false;
        for (const auto& folder : QStringList{"Widget", "Commons", "Ui", "runtime", "cache"})
            if (!QDir().mkdir(directory.path() + "/" + folder))
                return false;
        QFile::setPermissions(directory.path() + "/runtime",
                              QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner);
        if (!QFile::copy(widget, directory.path() + "/Widget/WeatherWidget.qml"))
            return false;
        if (!write("Commons/qmldir", "module qs.Commons\nsingleton Style 1.0 Style.qml\nsingleton "
                                     "Color 1.0 Color.qml\n") ||
            !write("Commons/Color.qml", "pragma Singleton\nimport QtQuick\nQtObject { readonly "
                                        "property color foreground: 'white' }\n") ||
            !write("Commons/Style.qml",
                   "pragma Singleton\nimport QtQuick\nQtObject { readonly property var font: "
                   "({body:14}); function space(n) { return n; } }\n") ||
            !write("Ui/qmldir", "module qs.Ui\nBarWidget 1.0 BarWidget.qml\nPopupCard 1.0 "
                                "PopupCard.qml\nButton 1.0 Button.qml\n") ||
            !write("Ui/PopupCard.qml",
                   "import QtQuick\nItem { property var anchorItem; property var bar; property "
                   "bool open:false; property real contentWidth; property real contentHeight; "
                   "visible:open; function fittedContentWidth(n) { return n; } function "
                   "fittedContentHeight(n) { return n; } }\n") ||
            !write("Ui/Button.qml", "import QtQuick\nItem { property string text; property bool "
                                    "focusable; property bool bordered; signal clicked(); }\n") ||
            !write("Ui/BarWidget.qml",
                   "import QtQuick\nItem { property QtObject bar:null; property string moduleName; "
                   "property var settings: ({}); readonly property bool vertical:false; readonly "
                   "property int barSize:32; function setting(name,fallback) { return "
                   "settings[name]===undefined?fallback:settings[name]; } }\n") ||
            !write("a-weather-app",
                   "#!/bin/sh\nfor argument in \"$@\"; do\n  case \"$argument\" in "
                   "--refresh-bar|--check-updates) exit 0;; esac\ndone\nif \"$WEATHER_BAR_APP\" "
                   "\"$@\"; then sleep 0.15; else exit $?; fi\n") ||
            !write("shell.qml", R"(import QtQuick
import Quickshell
import "Widget" as Widget
ShellRoot {
    Widget.WeatherWidget {
        settings: ({projectPath:Quickshell.env("WEATHER_BAR_PROJECT"),statePath:Quickshell.env("WEATHER_BAR_STATE")})
        onLabelChanged: console.log("BAR_TEST_LABEL:"+label)
        onBufferChanged: if(buffer!=="")console.log("BAR_TEST_BUFFER:"+buffer)
    }
    Timer { interval:20000;running:true;onTriggered:Qt.quit() }
}
)"))
            return false;
        QFile::setPermissions(directory.path() + "/a-weather-app",
                              QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner);
        auto env = QProcessEnvironment::systemEnvironment();
        env.insert("XDG_RUNTIME_DIR", directory.path() + "/runtime");
        env.insert("XDG_CACHE_HOME", directory.path() + "/cache");
        env.insert("WEATHER_BAR_APP", qEnvironmentVariable("GO_APP"));
        env.insert("WEATHER_BAR_PROJECT", directory.path());
        env.insert("WEATHER_BAR_STATE", state);
        process.setProcessEnvironment(env);
        process.setProcessChannelMode(QProcess::MergedChannels);
        process.start("qs", {"--path", directory.path() + "/shell.qml", "--no-color"});
        return process.waitForStarted(3000);
    }
    QString label() {
        output += process.readAll();
        const QByteArray marker = "BAR_TEST_LABEL:";
        auto offset = output.lastIndexOf(marker);
        if (offset < 0)
            return {};
        auto tail = output.mid(offset + marker.size());
        return QString::fromUtf8(tail.left(tail.indexOf('\n'))).trimmed();
    }
    bool captured(const QString& temperature) {
        output += process.readAll();
        return output.contains(("\"label\":\"" + temperature + "\"").toUtf8());
    }
};

class ServiceFrontendTest : public QObject {
    Q_OBJECT
    std::unique_ptr<WeatherTransport> transport;
    std::unique_ptr<MapTiles> mapTiles;
    std::unique_ptr<QQmlApplicationEngine> engine;
    QObject* root = nullptr;
    QQuickWindow* window = nullptr;
    QVariant eval(const QString& source) {
        QQmlExpression expression(QQmlEngine::contextForObject(root), root, source);
        auto result = expression.evaluate();
        if (expression.hasError())
            QTest::qFail(qPrintable(expression.error().toString()), __FILE__, __LINE__);
        return result;
    }
    bool attach(ServiceFixture& fixture) {
        transport = std::make_unique<WeatherTransport>(fixture.socket, false);
        mapTiles = std::make_unique<MapTiles>();
        engine = std::make_unique<QQmlApplicationEngine>();
        engine->setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(transport.get())},
             {"mapTiles", QVariant::fromValue<QObject*>(mapTiles.get())}});
        // Capture exit requests without stopping the QtTest application's event loop.
        QObject::disconnect(engine.get(), nullptr, QCoreApplication::instance(), nullptr);
        engine->load(QUrl("qrc:/ui/qml/shell.qml"));
        if (engine->rootObjects().size() != 1)
            return false;
        root = engine->rootObjects().first();
        window = qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        return window != nullptr;
    }
    QObject* visualNamed(QQuickItem* item, const char* name) {
        if (!item)
            return nullptr;
        if (item->objectName() == QLatin1String(name))
            return item;
        for (auto* child : item->childItems())
            if (auto* found = visualNamed(child, name))
                return found;
        return nullptr;
    }
    QObject* named(const char* name) {
        auto object = root->findChild<QObject*>(name);
        if (!object && window)
            object = visualNamed(window->contentItem(), name);
        if (!object) {
            QTest::qFail(qPrintable(QString("Missing UI control %1").arg(name)), __FILE__,
                         __LINE__);
            return root;
        }
        return object;
    }
    void click(const char* name) {
        auto item = qobject_cast<QQuickItem*>(named(name));
        QVERIFY(item);
        QVERIFY(item->isEnabled());
        QVERIFY(item->isVisible());
        QTest::qWait(100);
        auto point = item->mapToScene(QPointF(item->width() / 2, item->height() / 2)).toPoint();
        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, point);
    }
    void showSettingsSection(const char* section) {
        QTest::qWait(100); // Settle any queued initial location-picker focus return.
        if (!eval("root.effectsOpen").toBool()) {
            auto* item = qobject_cast<QQuickItem*>(named("openEffects"));
            QVERIFY(item && item->isEnabled());
            item->forceActiveFocus();
            QTest::qWait(60);
            QTest::keyClick(window, Qt::Key_Space);
        }
        QTRY_VERIFY(eval("root.effectsOpen").toBool());
        auto* tab = qobject_cast<QQuickItem*>(named(section));
        QVERIFY(tab && tab->isEnabled());
        tab->forceActiveFocus();
        QTest::qWait(60);
        QTest::keyClick(window, Qt::Key_Space);
    }
    void selectUnits(const QString& units, const char* name = "unitsChoice") {
        QTRY_VERIFY_WITH_TIMEOUT(!eval("bridge.busy").toBool(), 3000);
        auto* choice = qobject_cast<QQuickItem*>(named(name));
        QVERIFY(choice);
        choice->forceActiveFocus();
        click(name);
        auto* popup = choice->property("popup").value<QObject*>();
        QVERIFY(popup);
        QTRY_VERIFY(popup->property("visible").toBool());
        QTest::keyClick(window, Qt::Key_Home);
        const int index = units == "auto" ? 0 : units == "F" ? 1 : 2;
        for (int step = 0; step < index; ++step)
            QTest::keyClick(window, Qt::Key_Down);
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_VERIFY(!popup->property("visible").toBool());
        QTRY_VERIFY_WITH_TIMEOUT(!eval("bridge.busy").toBool(), 3000);
    }
    void toggle(const char* name, bool checked) {
        auto object = named(name);
        QVERIFY(object->property("enabled").toBool());
        object->setProperty("checked", checked);
        QVERIFY(QMetaObject::invokeMethod(object, "clicked", Qt::DirectConnection));
    }
    struct Usage {
        qint64 ticks = 0, rssKiB = 0, pssKiB = 0;
    };
    Usage usage(qint64 pid) {
        Usage value;
        QFile stat(QString("/proc/%1/stat").arg(pid));
        if (stat.open(QIODevice::ReadOnly)) {
            auto raw = stat.readAll();
            auto fields = raw.mid(raw.lastIndexOf(')') + 1).trimmed().split(' ');
            if (fields.size() > 12)
                value.ticks = fields[11].toLongLong() + fields[12].toLongLong();
        }
        QFile memory(QString("/proc/%1/smaps_rollup").arg(pid));
        if (memory.open(QIODevice::ReadOnly)) {
            for (const auto& line : memory.readAll().split('\n')) {
                if (line.startsWith("Rss:"))
                    value.rssKiB = line.mid(4).trimmed().split(' ').first().toLongLong();
                if (line.startsWith("Pss:"))
                    value.pssKiB = line.mid(4).trimmed().split(' ').first().toLongLong();
            }
        }
        return value;
    }
    QJsonObject measure(const QString& phase, qint64 servicePID) {
        const qint64 uiPID = QCoreApplication::applicationPid();
        auto beforeUI = usage(uiPID), beforeGo = usage(servicePID);
        qint64 peakRSS = 0, peakPSS = 0;
        QElapsedTimer timer;
        timer.start();
        for (int i = 0; i < 20; i++) {
            QTest::qWait(250);
            auto ui = usage(uiPID), go = usage(servicePID);
            peakRSS = qMax(peakRSS, ui.rssKiB + go.rssKiB);
            peakPSS = qMax(peakPSS, ui.pssKiB + go.pssKiB);
        }
        auto afterUI = usage(uiPID), afterGo = usage(servicePID);
        const double seconds = timer.elapsed() / 1000.0, hz = sysconf(_SC_CLK_TCK);
        return {{"phase", phase},
                {"seconds", seconds},
                {"cpu_percent_one_core",
                 100.0 * (afterUI.ticks + afterGo.ticks - beforeUI.ticks - beforeGo.ticks) / hz /
                     seconds},
                {"peak_rss_kib", peakRSS},
                {"peak_pss_kib", peakPSS},
                {"ui_pid", uiPID},
                {"service_pid", servicePID}};
    }
  private slots:
    void mapWholeAppOfflineMeasurement() {
        const auto capture = qEnvironmentVariable("WEATHER_QT_MAP_CAPTURE");
        if (capture.isEmpty())
            QSKIP("Set WEATHER_QT_MAP_CAPTURE for private whole-app map measurement");
        QFile file(capture);
        QVERIFY(file.open(QIODevice::ReadOnly));
        auto map = QJsonDocument::fromJson(file.readAll()).object();
        QVERIFY(!map.isEmpty());
        // Reuse geographic tiles from the reviewed visual capture, cache-only.
        QCoreApplication::setApplicationName("frontend-test");
        ServiceFixture fixture;
        fixture.cache(60);
        fixture.savedNewYorkPlace();
        fixture.save("weather-map.json", map);
        QElapsedTimer startup;
        startup.start();
        QVERIFY(fixture.start());
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(window->isExposed() && eval("backend.snapshot!==null").toBool(),
                                 5000);
        const auto startupMs = startup.elapsed();
        QTest::keyClick(window, Qt::Key_Escape);
        window->hide();
        QTRY_VERIFY_WITH_TIMEOUT(!eval("backend.mapWanted").toBool(), 3000);
        QTest::qWait(1000);
        auto closed = measure("closed", fixture.service.processId());
        QSignalSpy tiles(mapTiles.get(), &MapTiles::tileReady);
        QSignalSpy tileReplies(mapTiles.get(), &MapTiles::tileReply);
        window->show();
        QTRY_VERIFY(window->isExposed());
        auto* section = named("weatherMaps");
        QVERIFY(section);
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = scroll->property("contentItem").value<QObject*>();
        QVERIFY(flick);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 40);
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.mapWanted").toBool(), 5000);
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.weatherMap.data!==null").toBool(), 5000);
        QTRY_VERIFY_WITH_TIMEOUT(tiles.size() >= 2, 3000);
        QTest::qWait(1000);
        auto opened = measure("open", fixture.service.processId());
        int networkTiles = 0;
        for (const auto& reply : tileReplies)
            if (!reply.at(1).toBool())
                ++networkTiles;
        QCOMPARE(networkTiles, 0);
        QJsonObject result{{"startup_to_exposed_snapshot_ms", startupMs},
                           {"closed", closed},
                           {"open", opened},
                           {"cached_tile_replies", tileReplies.size()},
                           {"tile_network_replies", networkTiles}};
        qInfo().noquote() << "MAP_PERF" << QJsonDocument(result).toJson(QJsonDocument::Compact);
        window->hide();
        QTRY_VERIFY_WITH_TIMEOUT(!eval("backend.mapWanted").toBool(), 3000);
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("backend.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
    }
    void initTestCase() {
        QVERIFY2(!qEnvironmentVariable("GO_APP").isEmpty(),
                 "GO_APP must name the compiled Go service");
        QGuiApplication::setQuitOnLastWindowClosed(false);
    }
    void init() {
        QTest::failOnWarning(
            QRegularExpression(".*(TypeError:|ReferenceError:|Binding loop|Unable to assign|Cannot "
                               "assign|failed to load component).*"));
    }
    void cleanup() {
        teardownTrace("engine begin");
        engine.reset();
        teardownTrace("map tiles begin");
        mapTiles.reset();
        teardownTrace("transport begin");
        transport.reset();
        root = nullptr;
        window = nullptr;
        teardownTrace("cleanup complete");
    }
    void hiddenPresentationSuppressesPeriodicSnapshots_data() {
        QTest::addColumn<QString>("shutdownMode");
        QTest::newRow("orderly") << QString("orderly");
        QTest::newRow("fixture-terminate") << QString("fixture-terminate");
        QTest::newRow("kill-disconnect") << QString("kill-disconnect");
    }
    void hiddenPresentationSuppressesPeriodicSnapshots() {
        QFETCH(QString, shutdownMode);
        ServiceFixture fixture;
        fixture.cache(60);
        fixture.save("notifications.json",
                     {{"schema_version", 1},
                      {"settings", QJsonObject{{"enabled", true},
                                               {"quiet_enabled", false},
                                               {"quiet_start", 22},
                                               {"quiet_end", 7},
                                               {"probability", 50}}},
                      {"snoozed_until", QDateTime::currentSecsSinceEpoch() + 3600},
                      {"events", QJsonArray{}}});
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QSignalSpy events(transport.get(), &WeatherTransport::message);
        QTRY_VERIFY_WITH_TIMEOUT(
            eval("backend.subscribed&&backend.snapshot!==null&&backend.pending<0").toBool(), 5000);
        QVERIFY(eval("backend.snapshot.notifications.settings.enabled").toBool());
        window->hide();
        QTRY_VERIFY_WITH_TIMEOUT(!eval("backend.presentationActive").toBool() &&
                                     eval("backend.pending<0").toBool(),
                                 3000);
        const auto hiddenRevision = eval("backend.lastSnapshotRevision").toDouble();
        events.clear();
        QTest::qWait(6200);
        QCOMPARE(eval("backend.lastSnapshotRevision").toDouble(), hiddenRevision);
        for (const auto& event : events) {
            const auto message =
                QJsonDocument::fromJson(event.first().toString().toUtf8()).object();
            QVERIFY(message.value("event").toString() != QStringLiteral("snapshot"));
        }
        // Explicit changes remain responsive while presentation is hidden.
        QVERIFY(eval("backend.send('set_controls',{units:'C'})").toBool());
        QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(), QString("C"), 3000);
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.pending<0").toBool(), 3000);
        const auto changedRevision = eval("backend.lastSnapshotRevision").toDouble();
        QVERIFY(changedRevision > hiddenRevision);
        window->showNormal();
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.presentationActive").toBool() &&
                                     eval("backend.pending<0").toBool(),
                                 3000);
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.lastSnapshotRevision").toDouble() > changedRevision,
                                 3000);
        window->showMinimized();
        QVERIFY(window->isVisible());
        QTRY_VERIFY_WITH_TIMEOUT(!eval("backend.presentationActive").toBool() &&
                                     eval("backend.pending<0").toBool(),
                                 3000);
        const auto minimizedRevision = eval("backend.lastSnapshotRevision").toDouble();
        QTest::qWait(6200);
        QCOMPARE(eval("backend.lastSnapshotRevision").toDouble(), minimizedRevision);
        window->showNormal();
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.presentationActive").toBool() &&
                                     eval("backend.pending<0").toBool(),
                                 3000);
        QTRY_VERIFY_WITH_TIMEOUT(
            eval("backend.lastSnapshotRevision").toDouble() > minimizedRevision, 3000);
        QVERIFY(!eval("backend.disconnected").toBool());
        if (shutdownMode == "fixture-terminate") {
            // Preserve the original failure sequence: the local fixture stops
            // the service before QtTest cleanup destroys the live QML engine.
            return;
        }
        if (shutdownMode == "kill-disconnect") {
            fixture.service.kill();
            QVERIFY(fixture.service.waitForFinished(3000));
            QTRY_VERIFY_WITH_TIMEOUT(eval("backend.disconnected").toBool(), 3000);
            QVERIFY(!transport->connected());
            QVERIFY(!eval("backend.send('set_controls',{units:'F'})").toBool());
            return;
        }
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("backend.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
    }
    void savedLocationsOfflineLifecycle() {
        ServiceFixture fixture;
        fixture.savedCities();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QTRY_VERIFY(window->isExposed());
        QCOMPARE(eval("root.savedLocations.items.length").toInt(), 20);
        QCOMPARE(eval("root.primaryName").toString(), QString("City 0"));
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(eval("root.locationsOpen").toBool());
        auto* list = qobject_cast<QQuickItem*>(named("savedLocationList"));
        QTRY_VERIFY(list->hasActiveFocus());
        QTest::keyClick(window, Qt::Key_Down);
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(eval("root.location").toString(), QString("City 1"));
        QCOMPARE(eval("root.current.temperature_c").toInt(), 16);
        QCOMPARE(eval("root.units").toString(), QString("C"));
        QCOMPARE(eval("root.primaryName").toString(), QString("City 0"));
        QCOMPARE(fixture.saved("saved-locations.json")["primary"].toString(), QString("place-100"));
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(eval("root.locationsOpen").toBool());
        list = qobject_cast<QQuickItem*>(named("savedLocationList"));
        QTRY_VERIFY(list->hasActiveFocus());
        QCOMPARE(list->property("currentIndex").toInt(), 1);
        QTest::keyClick(window, Qt::Key_Right);
        auto* alias = qobject_cast<QQuickItem*>(named("savedLocationAlias"));
        QTRY_VERIFY(alias->hasActiveFocus());
        alias->setProperty("text", "Office");
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(eval("root.viewedPlace.label").toString(), QString("Office"));
        QTRY_VERIFY(!eval("bridge.busy").toBool());
        auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            item->forceActiveFocus();
            QTest::keyClick(window, Qt::Key_Space);
        };
        activate("moveLocationUp");
        QTRY_COMPARE(eval("root.savedLocations.items[0].id").toString(), QString("place-101"));
        QTRY_VERIFY(!eval("bridge.busy").toBool());
        activate("makeLocationPrimary");
        QTRY_COMPARE(eval("root.primaryName").toString(), QString("Office"));
        QCOMPARE(eval("root.primaryTimezone").toString(), QString("Europe/London"));
        QTRY_VERIFY(!eval("bridge.busy").toBool());
        QVERIFY(!named("removeSavedLocation")->property("enabled").toBool());
        auto* replacement = qobject_cast<QQuickItem*>(named("replacementPrimary"));
        replacement->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        QTRY_VERIFY(named("removeSavedLocation")->property("enabled").toBool());
        activate("removeSavedLocation");
        QTRY_COMPARE(eval("root.savedLocations.items.length").toInt(), 19);
        QTRY_COMPARE(eval("root.location").toString(), QString("City 0"));
        QCOMPARE(eval("root.primaryName").toString(), QString("City 0"));
        QCOMPARE(eval("root.current.temperature_c").toInt(), 15);
        auto saved = fixture.saved("saved-locations.json");
        QCOMPARE(saved["primary"].toString(), QString("place-100"));
        QCOMPARE(saved["viewed"].toString(), QString("place-100"));
        // Browsing is temporary; the same saved list opens at Home next launch.
        eval("bridge.send('saved_location', {action:'view', id:'place-102'})");
        QTRY_COMPARE(eval("root.location").toString(), QString("City 2"));
        QTRY_VERIFY(!eval("bridge.busy").toBool());
        QCOMPARE(fixture.saved("saved-locations.json")["viewed"].toString(), QString("place-102"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        cleanup();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QCOMPARE(eval("root.savedLocations.items.length").toInt(), 19);
        QCOMPARE(eval("root.savedLocations.primary").toString(), QString("place-100"));
        QCOMPARE(eval("root.savedLocations.viewed").toString(), QString("place-100"));
        QCOMPARE(eval("root.current.temperature_c").toInt(), 15);
        QCOMPARE(fixture.saved("saved-locations.json")["places"], saved["places"]);
        QCOMPARE(fixture.saved("saved-locations.json")["viewed"].toString(), QString("place-100"));
    }
    void emptyOfflineWindow() {
        ServiceFixture fixture;
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(window->isVisible() && window->isExposed(), 3000);
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QVERIFY(eval("root.freshness").toString().contains("Choose a city"));
        QVERIFY(!QFile::exists(fixture.state + "/notifications.json"));
        QVERIFY(!eval("root.liveDesktop").toBool());
        // The optional detail guard also handles calls before a city is chosen,
        // without fetching data or exposing a totals button on the dashboard.
        eval("root.effectsOpen=false;root.locationsOpen=false");
        eval("root.openPrecipitation('')");
        QTRY_VERIFY(eval("root.precipitationOpen").toBool());
        QCOMPARE(eval("backend.precipitationState").toString(), QString("unavailable"));
        QCOMPARE(eval("backend.precipitationError").toString(),
                 QString("precipitation_location_required"));
        QVERIFY(named("precipitationStatus")
                    ->property("text")
                    .toString()
                    .contains("Choose a location"));
        QVERIFY(!QFile::exists(fixture.state + "/precipitation-detail.json"));
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!eval("root.precipitationOpen").toBool());
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QCOMPARE(exit.first().first().toInt(), 0);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        QCOMPARE(fixture.service.exitCode(), 0);
        QVERIFY(!QFile::exists(fixture.socket));
    }
    void multiMonitorCompatibilitySnapshot() {
        ServiceFixture fixture;
        QVERIFY(fixture.start());
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        const auto ready =
            eval("Forecast.effectsSetup({status:'ready',reason:'ready',outputs:[{name:'eDP-1',"
                 "width:1920,height:1200,scale:1.25,enabled:true},{name:'DP-1',width:3840,height:"
                 "2160,scale:2,enabled:true}],selected_output:'DP-1'}).status");
        QCOMPARE(ready.toString(), QString("ready"));
        const auto choose =
            eval("Forecast.effectsSetup({status:'unavailable',reason:'output_selection_required',"
                 "outputs:[{name:'eDP-1',width:1920,height:1200,scale:1.25,enabled:true},{name:'DP-"
                 "1',width:3840,height:2160,scale:2,enabled:true}],selected_output:null}).reason");
        QCOMPARE(choose.toString(), QString("output_selection_required"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void cacheFreshness_data() {
        QTest::addColumn<int>("age");
        QTest::addColumn<QString>("prefix");
        QTest::newRow("fresh-offline") << 60 << QString("Weather data from");
        QTest::newRow("stale-offline") << 4000 << QString("Stale forecast");
    }
    void cacheFreshness() {
        QFETCH(int, age);
        QFETCH(QString, prefix);
        ServiceFixture fixture;
        fixture.cache(age);
        QVERIFY(fixture.start());
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QTRY_VERIFY(window->isExposed());
        QVERIFY2(eval("root.freshness").toString().startsWith(prefix),
                 qPrintable(eval("root.freshness").toString()));
        QCOMPARE(eval("root.current.temperature_c").toDouble(), 15.0);
        QCOMPARE(eval("root.days.length").toInt(), 1);
        QCOMPARE(eval("root.hours.length").toInt(), 1);
        QCOMPARE(named("currentMetricValue_uv")->property("text").toString(), QString("—"));
        QCOMPARE(named("currentMetricValue_pressure")->property("text").toString(), QString("—"));
        QCOMPARE(named("currentMetricDetail_humidity")->property("text").toString(),
                 QString("Dew point —"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
    }
    void temperatureUnitsMatchBar_data() {
        QTest::addColumn<double>("celsius");
        QTest::addColumn<QString>("fahrenheitLabel");
        QTest::addColumn<QString>("celsiusLabel");
        QTest::addColumn<int>("age");
        QTest::newRow("reported-60F-16C") << 15.5 << QString("60°") << QString("16°") << 60;
        QTest::newRow("fahrenheit-half-degree") << 2.5 << QString("37°") << QString("3°") << 60;
        QTest::newRow("below-zero") << -1.5 << QString("29°") << QString("-1°") << 60;
        QTest::newRow("negative-half-degree") << -0.5 << QString("31°") << QString("0°") << 60;
        QTest::newRow("stale-forecast") << 15.5 << QString("60°") << QString("16°") << 4000;
    }
    void temperatureUnitsMatchBar() {
        QFETCH(double, celsius);
        QFETCH(QString, fahrenheitLabel);
        QFETCH(QString, celsiusLabel);
        QFETCH(int, age);
        ServiceFixture fixture;
        fixture.savedLondonPlace(age, celsius);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.current!==null").toBool(), 5000);
        QTRY_VERIFY(window->isExposed());
        QTest::keyClick(window, Qt::Key_Escape);
        BarFixture widget;
        const bool hasQuickshell = !QStandardPaths::findExecutable("qs").isEmpty();
        QVERIFY2(hasQuickshell || !qEnvironmentVariableIsSet("WEATHER_REQUIRE_BAR_TEST"),
                 "Release acceptance requires Quickshell for immediate widget refresh tests");
        if (hasQuickshell)
            QVERIFY(widget.start(fixture.state));
        auto checkBar = [&](const QString& temperature) {
            QCOMPARE(named("currentTemperature")->property("text").toString(), temperature);
            QProcess bar;
            bar.start(qEnvironmentVariable("GO_APP"), {"--bar", "--state-dir", fixture.state});
            QVERIFY(bar.waitForFinished(3000));
            QCOMPARE(bar.exitCode(), 0);
            const auto status = QJsonDocument::fromJson(bar.readAllStandardOutput()).object();
            QCOMPARE(status["label"].toString(),
                     temperature + " · Clear" + (age > 2700 ? " · Stale" : ""));
            QCOMPARE(status["freshness"].toString(), QString(age > 2700 ? "stale" : "fresh"));
            QVERIFY(status["tooltip"].toString().contains("Alerts not supported here"));
            QVERIFY(bar.readAllStandardError().isEmpty());
            if (hasQuickshell)
                QTRY_COMPARE_WITH_TIMEOUT(widget.label(), status["label"].toString(), 2000);
        };
        QCOMPARE(eval("root.units").toString(), QString("C"));
        QVERIFY(eval("root.automaticUnits").toBool());
        checkBar(celsiusLabel);
        QCOMPARE(named("unitsChoice")->property("displayText").toString(), QString("Auto (°C)"));
        QVERIFY(!QFile::exists(fixture.state + "/controls.json"));
        for (const auto& units : QStringList{"F", "C", "F", "C"}) {
            selectUnits(units);
            QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(), units, 3000);
            QVERIFY(!eval("root.automaticUnits").toBool());
            QCOMPARE(fixture.saved("controls.json")["units_mode"].toString(), QString("manual"));
            QCOMPARE(fixture.saved("controls.json")["units"].toString(), units);
            checkBar(units == "C" ? celsiusLabel : fahrenheitLabel);
        }
        selectUnits("auto");
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.automaticUnits").toBool(), 3000);
        checkBar(celsiusLabel);
        QCOMPARE(fixture.saved("controls.json")["units_mode"].toString(), QString("auto"));
        eval("root.openSettings(false)");
        selectUnits("C", "settingsUnitsChoice");
        QTRY_VERIFY_WITH_TIMEOUT(!eval("root.automaticUnits").toBool(), 3000);
        QCOMPARE(named("unitsChoice")->property("displayText").toString(), QString("°C"));
        QCOMPARE(named("settingsUnitsChoice")->property("displayText").toString(),
                 QString("Celsius (°C)"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        engine.reset();
        transport.reset();
        QVERIFY(fixture.start());
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.current!==null").toBool(), 5000);
        QCOMPARE(eval("root.units").toString(), QString("C"));
        QVERIFY(!eval("root.automaticUnits").toBool());
        QCOMPARE(named("unitsChoice")->property("displayText").toString(), QString("°C"));
        checkBar(celsiusLabel);
        QSignalSpy finalExit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(finalExit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
    }
    void barStartsWatchingANewStateDirectory() {
        if (QStandardPaths::findExecutable("qs").isEmpty()) {
            QVERIFY2(!qEnvironmentVariableIsSet("WEATHER_REQUIRE_BAR_TEST"),
                     "Release acceptance requires Quickshell");
            QSKIP("Quickshell not installed");
        }
        ServiceFixture fixture;
        QVERIFY(QDir().rmdir(fixture.state));
        BarFixture widget;
        QVERIFY(widget.start(fixture.state));
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("--° · Unavailable"), 2000);
        QVERIFY(QDir().mkdir(fixture.state));
        QFile::setPermissions(fixture.state,
                              QFile::ReadOwner | QFile::WriteOwner | QFile::ExeOwner);
        fixture.savedLondonPlace();
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("16° · Clear"), 2000);
        fixture.save("controls.json", {{"units", "F"}});
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("60° · Clear"), 2000);
    }
    void barImmediatelyTracksLocationAndRapidChanges() {
        if (QStandardPaths::findExecutable("qs").isEmpty()) {
            QVERIFY2(!qEnvironmentVariableIsSet("WEATHER_REQUIRE_BAR_TEST"),
                     "Release acceptance requires Quickshell");
            QSKIP("Quickshell not installed");
        }
        ServiceFixture fixture;
        fixture.cache(60);
        fixture.savedNewYorkPlace();
        BarFixture widget;
        QVERIFY(widget.start(fixture.state));
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("59° · Clear"), 2000);
        auto profile = fixture.saved("location-profile.json");
        profile["country_code"] = "GB";
        profile["place"] = QJsonObject{{"provider", "open-meteo"}, {"id", 2643743}};
        auto location = profile["location"].toObject();
        location["name"] = "London, England, United Kingdom";
        location["latitude"] = 51.5085;
        location["longitude"] = -.1257;
        location["timezone"] = "Europe/London";
        auto forecast = profile["forecast"].toObject();
        forecast["location"] = location;
        auto current = forecast["current"].toObject();
        current["temperature_c"] = 15.5;
        forecast["current"] = current;
        profile["location"] = location;
        profile["forecast"] = forecast;
        auto save = [&](const QString& name, const QJsonObject& value) {
            QSaveFile file(fixture.state + "/" + name);
            QVERIFY(file.open(QIODevice::WriteOnly));
            file.setPermissions(QFile::ReadOwner | QFile::WriteOwner);
            QVERIFY(file.write(QJsonDocument(value).toJson(QJsonDocument::Compact)) > 0);
            QVERIFY(file.commit());
        };
        save("location-profile.json", profile);
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("16° · Clear"), 2000);
        save("controls.json", {{"units", "F"}});
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("60° · Clear"), 2000);
        // The helper deliberately takes 150ms to exit. Changes arriving during
        // that read must queue another read rather than wait for the 30s timer.
        widget.output.clear();
        save("controls.json", {{"units", "C"}});
        QTRY_VERIFY_WITH_TIMEOUT(widget.captured("16° · Clear"), 2000);
        save("controls.json", {{"units", "F"}});
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("16° · Clear"), 2000);
        QTRY_COMPARE_WITH_TIMEOUT(widget.label(), QString("60° · Clear"), 2000);
    }
    void dashboardRepeatedCustomizationResources() {
        if (!qEnvironmentVariableIsSet("WEATHER_QT_DASHBOARD_MEASURE"))
            QSKIP("Set WEATHER_QT_DASHBOARD_MEASURE for sequential dashboard resource sampling");
        ServiceFixture fixture;
        fixture.cache(60, true);
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        auto forecast = fixture.saved("forecast.json");
        QJsonArray hours, days;
        const auto now = QDateTime::currentDateTimeUtc();
        for (int i = 0; i < 24; i++) {
            auto row = forecast["hourly"].toArray()[0].toObject();
            row["time"] = now.addSecs((i + 1) * 3600).toString(Qt::ISODate);
            hours.append(row);
        }
        for (int i = 0; i < 10; i++) {
            auto row = forecast["daily"].toArray()[0].toObject();
            row["date"] = now.date().addDays(i).toString(Qt::ISODate);
            days.append(row);
        }
        forecast["hourly"] = hours;
        forecast["daily"] = days;
        fixture.save("forecast.json", forecast);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(
            eval("backend.snapshot!==null && root.hours.length===24 && !backend.busy").toBool(),
            5000);
        QTest::qWait(100); // Allow initial picker focus work before dismissing it.
        eval("root.effectsOpen=false;root.locationsOpen=false");
        QTest::qWait(100);
        QSignalSpy frames(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            QTest::qWait(200);
            const auto before = frames.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = frames.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "DASHBOARD_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("default_before_editor");
        if (qEnvironmentVariableIsSet("WEATHER_QT_DASHBOARD_BASELINE_ONLY"))
            return;
        const auto prefix = qEnvironmentVariable("WEATHER_QT_DASHBOARD_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            eval("forecastScroll.flickable.contentY=dashboardLayout.y");
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-full-default.png"));
            eval("forecastScroll.flickable.contentY=0");
        }
        for (int i = 0; i < 40; i++) {
            named("openDashboardEditor");
            eval("root.openDashboard()");
            QPointer<QObject> editor = named("dashboardEditor");
            if (i % 2 == 0)
                eval("dashboardLoader.item.update('density','compact');dashboardLoader.item.update("
                     "'sections',dashboardLoader.item.draft.sections.map(r=>({id:r.id,enabled:r.id!"
                     "=='maps' && r.id!=='air_quality'})))");
            else
                QMetaObject::invokeMethod(named("dashboardDefaults"), "clicked");
            QMetaObject::invokeMethod(named("dashboardApply"), "clicked");
            QTRY_VERIFY_WITH_TIMEOUT(
                !eval("root.dashboardOpen || backend.dashboardSaving").toBool(), 3000);
            QTRY_VERIFY(editor.isNull());
            QTest::qWait(40);
            QVERIFY(!eval("backend.mapWanted || backend.radarWanted").toBool());
            if (i == 19 || i == 39)
                phase(QString("default_after_%1_applies").arg(i + 1));
        }
        eval("root.openDashboard()");
        phase("editor_open");
        eval("root.closeDashboard()");
        window->hide();
        phase("hidden");
        QVERIFY(!eval("root.dashboardOpen || dashboardLoader.item!==null").toBool());
    }
    void dashboardCustomizeApplyResetAndRestart() {
        ServiceFixture fixture;
        fixture.cache(60, true);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot !== null && !backend.busy").toBool(), 5000);
        eval("root.effectsOpen=false;root.locationsOpen=false");
        QTest::qWait(100); // Let the location picker finish returning keyboard focus.
        const auto exists = [&](const char* name) {
            return root->findChild<QObject*>(name) || visualNamed(window->contentItem(), name);
        };
        const auto activate = [&](const char* name) {
            if (QString::fromLatin1(name) == "openDashboardEditor")
                showSettingsSection("settingsAppearanceSection");
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY2(item, name);
            item->forceActiveFocus();
            QTest::qWait(60);
            const auto center = item->mapToScene(QPointF(item->width() / 2, item->height() / 2));
            QVERIFY2(center.y() >= 0 && center.y() < window->height(), name);
            QTest::keyClick(window, Qt::Key_Space);
        };
        QVERIFY(named("currentMetric_solar"));
        QVERIFY(named("weatherMaps"));
        QVERIFY(fixture.saved("dashboard.json").isEmpty());
        activate("openDashboardEditor");
        QTRY_VERIFY(eval("root.dashboardOpen && dashboardLoader.item !== null").toBool());
        QVERIFY(named("dashboardEditor")->property("width").toDouble() >= 600);
        QVERIFY(named("dashboardEditor")->property("height").toDouble() >= 500);
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QVERIFY(!eval("root.mapActive").toBool());
        activate("dashboard_sections_maps");
        activate("dashboard_sections_air_quality");
        activate("dashboard_sections_daily_down");
        QTRY_COMPARE(eval("dashboardLoader.item.draft.sections[1].id").toString(),
                     QString("metrics"));
        QVERIFY(window->activeFocusItem()->objectName().startsWith("dashboard_sections_daily"));
        activate("dashboardDensity_compact");
        named("dashboardEditorTabs")->setProperty("currentIndex", 1);
        activate("dashboard_metrics_solar");
        activate("dashboard_metrics_pressure_up");
        named("dashboardEditorTabs")->setProperty("currentIndex", 2);
        activate("dashboard_hourly_wind_gust_m_s");
        activate("dashboard_hourly_wind_gust_m_s_up");
        QVERIFY(fixture.saved("dashboard.json").isEmpty());
        // Draft edits do not change the forecast or construct new map scenes.
        QVERIFY(named("currentMetric_solar"));
        QVERIFY(named("weatherMaps"));
        const auto prefix = qEnvironmentVariable("WEATHER_QT_DASHBOARD_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-editor.png"));
        }
        activate("dashboardApply");
        QTRY_VERIFY_WITH_TIMEOUT(!eval("root.dashboardOpen || backend.dashboardSaving").toBool(),
                                 3000);
        QTRY_VERIFY(!exists("currentMetric_solar"));
        QVERIFY(!exists("weatherMaps"));
        QVERIFY(!exists("airQualityPanel"));
        QVERIFY(named("hourlyValue_0_wind_gust_m_s"));
        auto* solarTimer = named("currentMetrics")->findChild<QObject*>("metricsSolarTimer");
        QVERIFY(solarTimer);
        QVERIFY(!solarTimer->property("running").toBool());
        QVERIFY(!eval("backend.mapWanted || backend.radarWanted").toBool());
        QCOMPARE(eval("root.dashboardPreferences.density").toString(), QString("compact"));
        auto saved = fixture.saved("dashboard.json");
        QCOMPARE(saved["density"].toString(), QString("compact"));
        QCOMPARE(saved["hourly"].toArray()[1].toString(), QString("wind_gust_m_s"));
        // A routine snapshot must keep existing cards and focus intact.
        auto* metric = named("currentMetric_wind");
        eval("backend.send('snapshot')");
        QTRY_VERIFY(!eval("backend.busy || backend.pending>=0").toBool());
        QCOMPARE(named("currentMetric_wind"), metric);
        auto* lastMetric = qobject_cast<QQuickItem*>(named("currentMetric_uv"));
        QVERIFY(lastMetric);
        activate("closeEffects");
        QTRY_VERIFY(!eval("root.effectsOpen").toBool());
        QCOMPARE(lastMetric->nextItemInFocusChain(true)->objectName(), QString("forecastDay_0"));
        if (!prefix.isEmpty()) {
            for (int width : {1200, 700}) {
                window->resize(width, 850);
                eval("forecastScroll.flickable.contentY=dashboardLayout.y");
                QTest::qWait(100);
                QVERIFY(window->grabWindow().save(prefix + QString("-compact-%1.png").arg(width)));
            }
        }
        // Preferences survive a real service and native UI restart.
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("backend.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        cleanup();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        eval("root.effectsOpen=false;root.locationsOpen=false");
        QTest::qWait(100); // Let the location picker finish returning keyboard focus.
        QVERIFY(!exists("weatherMaps"));
        QVERIFY(!exists("currentMetric_solar"));
        QCOMPARE(fixture.saved("dashboard.json"), saved);
        activate("openDashboardEditor");
        activate("dashboardDefaults");
        QCOMPARE(fixture.saved("dashboard.json"), saved);
        activate("dashboardApply");
        QTRY_VERIFY_WITH_TIMEOUT(!eval("root.dashboardOpen || backend.dashboardSaving").toBool(),
                                 3000);
        QVERIFY(named("currentMetric_solar"));
        QVERIFY(named("weatherMaps"));
        QVERIFY(named("airQualityPanel"));
        QCOMPARE(fixture.saved("dashboard.json")["density"].toString(), QString("spacious"));
        // All optional sections may be hidden, but alerts, current conditions,
        // customization and attribution remain reachable.
        activate("openDashboardEditor");
        for (const auto* id : {"hourly", "daily", "metrics", "maps", "air_quality"})
            activate(qPrintable(QString("dashboard_sections_") + id));
        activate("dashboardApply");
        QTRY_VERIFY(!eval("root.dashboardOpen").toBool());
        QVERIFY(!exists("currentMetrics"));
        QVERIFY(!exists("hourlyRail"));
        QVERIFY(named("sourceAttribution"));
        QVERIFY(named("openDashboardEditor"));
        activate("openDashboardEditor");
        window->hide();
        QTRY_VERIFY(!eval("root.dashboardOpen || dashboardLoader.item !== null").toBool());
    }
    void precipitationDetailsDatesAndLazyLifecycle() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        fixture.savedNewYorkPlace();
        const auto now = QDateTime::currentDateTimeUtc();
        auto start = now;
        start.setTime(QTime(now.time().hour(), 0));
        QJsonArray hours;
        for (int i = 0; i < 290; ++i)
            hours.append(QJsonArray{double(start.addSecs((i - 26) * 3600).toSecsSinceEpoch()), 1,
                                    .3, .2, .4, .1, 1500, .45});
        fixture.save("precipitation-detail.json", {{"version", 1},
                                                   {"source", "open-meteo/hourly-precipitation/v1"},
                                                   {"latitude", 40.7128},
                                                   {"longitude", -74.006},
                                                   {"grid_latitude", 40.7128},
                                                   {"grid_longitude", -74.006},
                                                   {"timezone", "America/New_York"},
                                                   {"fetched_at", now.toString(Qt::ISODate)},
                                                   {"hours", hours}});
        const auto original = fixture.saved("forecast.json"),
                   cache = fixture.saved("precipitation-detail.json");
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_PRECIPITATION_MEASURE");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "PRECIPITATION_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("initial");
        auto* loader = named("precipitationLoader");
        QVERIFY(loader);
        QVERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(eval("backend.precipitationState==='closed' && backend.precipitationResult===null")
                    .toBool());
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(50);
            QTest::keyClick(window, Qt::Key_Space);
        };
        auto* dayEntry = qobject_cast<QQuickItem*>(named("forecastDay_0"));
        QVERIFY(dayEntry);
        dayEntry->forceActiveFocus();
        QTest::qWait(80);
        click("forecastDay_0");
        QTRY_VERIFY(eval("details.opened").toBool());
        auto* entry = qobject_cast<QQuickItem*>(named("detailPrecipitation"));
        QVERIFY(entry);
        const auto entryCenter =
            entry->mapToScene(QPointF(entry->width() / 2, entry->height() / 2)).toPoint();
        QVERIFY(entryCenter.y() > 0 && entryCenter.y() < window->height());
        QVERIFY(entry->isEnabled());
        QTest::mousePress(window, Qt::LeftButton, Qt::NoModifier, entryCenter);
        QTest::qWait(80);
        QTest::mouseRelease(window, Qt::LeftButton, Qt::NoModifier, entryCenter);
        QTRY_COMPARE_WITH_TIMEOUT(eval("backend.precipitationState").toString(), QString("fresh"),
                                  5000);
        QVERIFY(
            eval("root.precipitationOpen && !root.mapActive && !backend.forecastVisible").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QCOMPARE(eval("backend.precipitationResult.days.length").toInt(), 10);
        QCOMPARE(eval("backend.precipitationResult.date").toString(),
                 eval("root.days[0].date").toString());
        QVERIFY(named("precipitationDaily_total_mm")->property("text").toString().contains("in"));
        QVERIFY(named("precipitationDaily_snow_cm")->property("text").toString().contains("in"));
        QVERIFY(named("precipitationSnowDepth")
                    ->property("text")
                    .toString()
                    .contains("above sea level"));
        const auto day = eval("backend.precipitationResult.date").toString();
        const auto today = eval("backend.precipitationResult.today").toString();
        const auto interval = named("precipitationInterval")->property("text").toString();
        activate("precipitationNextHour");
        QVERIFY(named("precipitationInterval")->property("text").toString() != interval);
        auto* chart = qobject_cast<QQuickItem*>(named("precipitationTrend"));
        chart->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Home);
        QCOMPARE(named("precipitationInterval")->property("text").toString(), interval);
        auto* accessibleChart = QAccessible::queryAccessibleInterface(chart);
        QVERIFY(accessibleChart);
        QVERIFY(accessibleChart->text(QAccessible::Description)
                    .startsWith(interval + ". Hourly liquid-equivalent precipitation: 0.04 in."));
        auto* date = qobject_cast<QQuickItem*>(named("precipitationDate"));
        date->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        QTRY_VERIFY(eval("backend.precipitationResult && backend.precipitationResult.date !== '" +
                         day + "'")
                        .toBool());
        QTRY_COMPARE(eval("backend.precipitationState").toString(), QString("fresh"));
        activate("precipitationToday");
        QTRY_COMPARE(
            eval("backend.precipitationResult ? backend.precipitationResult.date : ''").toString(),
            today);
        // A completion event may precede the older opening reply on the socket.
        // Its revision must keep a ready dataset from being replaced by loading.
        QVERIFY(
            eval("(function(){const newer=JSON.parse(JSON.stringify(backend.precipitationResult)); "
                 "newer.revision+=2; backend.applyPrecipitation(newer); const "
                 "older=JSON.parse(JSON.stringify(newer)); older.revision--; "
                 "older.status='loading'; older.fetched_at=null; older.fetched_label=''; "
                 "older.days=[]; older.hours=[]; backend.applyPrecipitation(older); return "
                 "backend.precipitationState==='fresh' && "
                 "backend.precipitationResult.revision===newer.revision;})()")
                .toBool());
        // Invalid units/coverage/intervals must not pass the native view contract.
        QVERIFY(
            eval("(function(){const bad=JSON.parse(JSON.stringify(backend.precipitationResult)); "
                 "bad.revision++; bad.days[0].total_mm_coverage=1; try "
                 "{backend.applyPrecipitation(bad);return false;} catch(e){return true;}})()")
                .toBool());
        auto* popup = loader->property("item").value<QObject*>();
        QVERIFY(popup->setProperty("units", "C"));
        QCOMPARE(named("precipitationDaily_total_mm")->property("text").toString(),
                 QString("24.0 mm"));
        QCOMPARE(named("precipitationDaily_snow_cm")->property("text").toString(),
                 QString("9.6 cm"));
        QVERIFY(accessibleChart->text(QAccessible::Description)
                    .contains("Hourly liquid-equivalent precipitation: 1.0 mm."));
        QVERIFY(named("precipitationSnowDepth")->property("text").toString().contains("10.0 cm"));
        // Missing snow stays unknown; a partial liquid day shows its subtotal.
        const auto complete = eval("JSON.stringify(backend.precipitationResult)").toString();
        QVERIFY(eval("(function(){const "
                     "partial=JSON.parse(JSON.stringify(backend.precipitationResult)); "
                     "partial.revision++; const d=partial.days.find(d=>d.date===partial.date); "
                     "d.total_mm=null; d.total_mm_subtotal=23; d.total_mm_coverage=23; "
                     "d.snow_cm=null; d.snow_cm_subtotal=null; d.snow_cm_coverage=0; "
                     "partial.hours[0].total_mm=null; partial.hours.forEach(h=>h.snow_cm=null); "
                     "backend.applyPrecipitation(partial); return true;})()")
                    .toBool());
        QCOMPARE(named("precipitationDaily_total_mm")->property("text").toString(),
                 QString("23.0 mm"));
        QCOMPARE(named("precipitationDaily_snow_cm")->property("text").toString(),
                 QString::fromUtf8("—"));
        eval("(function(){const original=" + complete +
             "; original.revision=backend.precipitationResult.revision+1; "
             "backend.applyPrecipitation(original);})()");
        QVERIFY(popup->setProperty("units", "F"));
        phase("details_open");
        const auto prefix = qEnvironmentVariable("WEATHER_QT_PRECIPITATION_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
        }
        activate("closePrecipitation");
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QCOMPARE(window->activeFocusItem(), dayEntry);
        window->resize(700, 650);
        eval("root.openPrecipitation('')");
        QTRY_COMPARE(eval("backend.precipitationState").toString(), QString("fresh"));
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
        }
        QTest::keyClick(window, Qt::Key_PageDown);
        auto* flick = named("precipitationScroll")->property("contentItem").value<QObject*>();
        QTRY_VERIFY(flick->property("contentY").toReal() > 0);
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-footer.png"));
        // Closing after replacing an unsent date cancels the last SENT token,
        // not the newer queued token that the service never received.
        eval("backend.loadPrecipitation(''); backend.loadPrecipitation(''); "
             "root.closePrecipitation()");
        QTRY_VERIFY(!eval("backend.busy").toBool());
        QVERIFY(eval("backend.precipitationResult===null && !backend.precipitationWanted && "
                     "backend.queuedPrecipitation===null")
                    .toBool());
        // A second subscribed peer can acquire the view only after the main
        // bridge's queued close has actually released its service ownership.
        QLocalSocket lease;
        lease.connectToServer(fixture.socket);
        QVERIFY(lease.waitForConnected(1000));
        QByteArray incoming;
        const auto leaseRequest = [&](const QJsonObject& request) {
            lease.write(QJsonDocument(request).toJson(QJsonDocument::Compact) + '\n');
            lease.flush();
            QElapsedTimer deadline;
            deadline.start();
            while (deadline.elapsed() < 2000) {
                incoming += lease.readAll();
                while (incoming.contains('\n')) {
                    const auto index = incoming.indexOf('\n');
                    const auto message = QJsonDocument::fromJson(incoming.left(index)).object();
                    incoming.remove(0, index + 1);
                    if (message["request_id"] == request["request_id"])
                        return message;
                }
                lease.waitForReadyRead(50);
            }
            return QJsonObject{};
        };
        QVERIFY(
            leaseRequest({{"version", 1}, {"request_id", 91}, {"op", "subscribe"}})["ok"].toBool());
        const auto query =
            QJsonDocument::fromJson(
                eval("JSON.stringify({location_id:backend.snapshot.saved_locations.viewed,latitude:"
                     "backend.snapshot.latitude,longitude:backend.snapshot.longitude,timezone:"
                     "backend.snapshot.timezone,date:'',client_token:90})")
                    .toString()
                    .toUtf8())
                .object();
        QVERIFY(leaseRequest({{"version", 1},
                              {"request_id", 92},
                              {"op", "precipitation_open"},
                              {"detail", query}})["ok"]
                    .toBool());
        QVERIFY(leaseRequest({{"version", 1},
                              {"request_id", 93},
                              {"op", "precipitation_close"},
                              {"detail", QJsonObject{{"client_token", 90}}}})["ok"]
                    .toBool());
        lease.disconnectFromServer();
        for (int i = 0; i < (probe ? 40 : 5); i++) {
            eval("root.openPrecipitation('')");
            QTRY_COMPARE(eval("backend.precipitationState").toString(), QString("fresh"));
            QPointer<QObject> popup = loader->property("item").value<QObject*>();
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(popup.isNull());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        QCOMPARE(fixture.saved("forecast.json"), original);
        QCOMPARE(fixture.saved("precipitation-detail.json"), cache);
        eval("root.openPrecipitation('')");
        window->hide();
        QTRY_VERIFY(eval("!root.precipitationOpen && !backend.precipitationWanted && "
                         "backend.precipitationResult===null && precipitationLoader.item===null")
                        .toBool());
        phase("hidden");
    }
    void appearanceSettingsPersistAndLargeLayouts() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60, true);
        auto cached = fixture.saved("forecast.json");
        QJsonArray hours, days;
        const auto now = QDateTime::currentDateTimeUtc();
        for (int i = 0; i < 60; ++i) {
            auto hour = cached["hourly"].toArray().first().toObject();
            hour["time"] = now.addSecs(i * 3600).toString(Qt::ISODate);
            hours.append(hour);
        }
        for (int i = 0; i < 10; ++i) {
            auto day = cached["daily"].toArray().first().toObject();
            day["date"] = now.date().addDays(i).toString(Qt::ISODate);
            days.append(day);
        }
        cached["hourly"] = hours;
        cached["daily"] = days;
        fixture.save("forecast.json", cached);
        fixture.savedNewYorkPlace();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const auto forecast = fixture.saved("forecast.json");
        const auto controls = fixture.saved("controls.json");
        window->resize(700, 650);
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(50);
            QTest::keyClick(window, Qt::Key_Space);
        };
        const auto prefix = qEnvironmentVariable("WEATHER_QT_READABILITY_SCREENSHOT_PREFIX");
        const auto capture = [&](const QString& suffix) {
            QTest::qWait(120);
            if (!prefix.isEmpty())
                QVERIFY(window->grabWindow().save(prefix + suffix + ".png"));
        };
        activate("openEffects");
        activate("settingsAppearanceSection");
        auto* selector = qobject_cast<QQuickItem*>(named("textSizePreference"));
        QVERIFY(selector);
        for (const double scale : {1.25, 1.5}) {
            selector->forceActiveFocus();
            QTest::keyClick(window, Qt::Key_Down);
            QTRY_COMPARE(eval("root.appearance.text_scale").toDouble(), scale);
            QTRY_VERIFY(!eval("backend.busy").toBool());
            QCOMPARE(fixture.saved("appearance.json")["text_scale"].toDouble(), scale);
        }
        activate("highContrastPreference");
        QTRY_VERIFY(eval("root.appearance.high_contrast").toBool());
        capture("-settings-150");
        activate("closeEffects");
        capture("-forecast-150");
        auto* temperature = qobject_cast<QQuickItem*>(named("currentTemperature"));
        QVERIFY(temperature);
        QCOMPARE(temperature->property("font").value<QFont>().pixelSize(), 144);
        auto* actions = qobject_cast<QQuickItem*>(named("headerActions"));
        QVERIFY(actions);
        for (auto* child : actions->childItems()) {
            if (!child->isVisible())
                continue;
            QVERIFY(child->x() >= 0 && child->x() + child->width() <= actions->width() + 1);
            QVERIFY(child->y() + child->height() <= actions->height() + 1);
        }
        for (const int width : {700, 1200}) {
            window->resize(width, 650);
            QTest::qWait(50);
            for (const char* name :
                 {"forecastHour_0", "forecastDay_0", "currentMetrics", "weatherMaps"}) {
                auto* item = qobject_cast<QQuickItem*>(named(name));
                QVERIFY(item);
                auto* scroll = named("forecastScroll");
                auto* flick = scroll->property("contentItem").value<QObject*>();
                auto* content = flick->property("contentItem").value<QQuickItem*>();
                QVERIFY(content);
                const auto y = item->mapToItem(content, QPointF()).y();
                flick->setProperty("contentY", qMax(0.0, y - 85));
                capture(QString("-%1-%2-150").arg(name).arg(width));
            }
            for (int index = 0; index < 4; ++index) {
                const auto name = QString("mapLayerTab%1").arg(index).toLatin1();
                auto* tab = named(name.constData());
                QVERIFY(tab);
                auto* label = tab->property("contentItem").value<QQuickItem*>();
                QVERIFY(label);
                QVERIFY(!label->property("truncated").toBool());
                QVERIFY(label->height() >= label->implicitHeight());
            }
        }
        window->resize(700, 650);
        eval("details.showMetric('pressure_msl_hpa')");
        QTRY_VERIFY(eval("details.opened").toBool());
        capture("-details-150");
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!eval("details.visible").toBool());
        eval("root.openShare()");
        QTRY_VERIFY(eval("shareLoader.item!==null && shareLoader.item.opened").toBool());
        capture("-sharing-150");
        const auto output = fixture.directory.path() + "/large-text.png";
        QVERIFY(eval("shareLoader.item.saveImage('" + QUrl::fromLocalFile(output).toString() + "')")
                    .toBool());
        QTRY_VERIFY(!eval("shareLoader.item.busy").toBool());
        QCOMPARE(eval("shareLoader.item.notice").toString(), QString("Forecast image saved."));
        QImageReader reader(output);
        QVERIFY(reader.size().width() > 0 && reader.size().width() <= 720);
        QVERIFY(reader.size().height() > 0 && reader.size().height() <= 1600);
        if (!prefix.isEmpty())
            QVERIFY(reader.read().save(prefix + "-export-150.png"));
        eval("root.closeShare()");
        // Restart both processes, so the first new snapshot must restore the
        // saved enlarged text and contrast without changing weather controls.
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("backend.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        cleanup();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        QCOMPARE(eval("root.appearance.text_scale").toDouble(), 1.5);
        QVERIFY(eval("root.appearance.high_contrast").toBool());
        selector = qobject_cast<QQuickItem*>(named("textSizePreference"));
        temperature = qobject_cast<QQuickItem*>(named("currentTemperature"));
        QVERIFY(selector && temperature);
        activate("openEffects");
        activate("settingsAppearanceSection");
        selector->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Home);
        QTRY_COMPARE(eval("root.appearance.text_scale").toDouble(), 1.0);
        activate("highContrastPreference");
        QTRY_VERIFY(!eval("root.appearance.high_contrast").toBool());
        QTRY_COMPARE(temperature->property("font").value<QFont>().pixelSize(), 96);
        QCOMPARE(fixture.saved("forecast.json"), forecast);
        QCOMPARE(fixture.saved("controls.json"), controls);
        QCOMPARE(fixture.saved("appearance.json")["text_scale"].toDouble(), 1.0);
        QVERIFY(!fixture.saved("appearance.json")["high_contrast"].toBool());
    }
    void featureSessionResources() {
        if (!qEnvironmentVariableIsSet("WEATHER_QT_FEATURE_SESSION_MEASURE"))
            QSKIP("Set WEATHER_QT_FEATURE_SESSION_MEASURE for sequential feature-session sampling");
        ServiceFixture fixture;
        fixture.cache(60, true);
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        auto forecast = fixture.saved("forecast.json");
        const auto now = QDateTime::currentDateTimeUtc();
        auto start = now;
        start.setTime(QTime(now.time().hour(), 0));
        QJsonArray hours, days, oldHours, precip, air;
        for (int i = 0; i < 240; ++i) {
            auto row = forecast["hourly"].toArray().first().toObject();
            const auto stamp = start.addSecs(i * 3600).toString(Qt::ISODate);
            row["time"] = stamp;
            hours.append(row);
            if (i < 60)
                oldHours.append(QJsonArray{stamp, 10, .6, 2, 2});
        }
        for (int i = 0; i < 10; ++i) {
            auto row = forecast["daily"].toArray().first().toObject();
            row["date"] = now.date().addDays(i).toString(Qt::ISODate);
            days.append(row);
        }
        forecast["hourly"] = hours;
        forecast["daily"] = days;
        fixture.save("forecast.json", forecast);
        fixture.savedNewYorkPlace();
        QJsonObject baseline{{"latitude", 40.7128},
                             {"longitude", -74.006},
                             {"timezone", "America/New_York"},
                             {"source", "open-meteo-hourly-v1"},
                             {"retrieved", now.addSecs(-3660).toString(Qt::ISODate)},
                             {"hours", oldHours}};
        fixture.save("forecast-history.json",
                     {{"schema_version", 1},
                      {"places", QJsonArray{QJsonObject{{"id", "place-5128581"},
                                                        {"current", baseline},
                                                        {"previous", QJsonValue::Null}}}}});
        for (int i = 0; i < 290; ++i)
            precip.append(QJsonArray{double(start.addSecs((i - 26) * 3600).toSecsSinceEpoch()), 1,
                                     .3, .2, .4, .1, 1500, .45});
        for (int i = 0; i < 48; ++i)
            air.append(QJsonArray{double(start.addSecs(i * 3600).toSecsSinceEpoch()), 25, 20, 5, 10,
                                  8, 12, 1, 230});
        const auto cache = [&](const QString& name, const QString& source, const QJsonArray& data) {
            fixture.save(name, {{"version", 1},
                                {"source", source},
                                {"latitude", 40.7128},
                                {"longitude", -74.006},
                                {"grid_latitude", 40.7128},
                                {"grid_longitude", -74.006},
                                {"timezone", "America/New_York"},
                                {"fetched_at", now.toString(Qt::ISODate)},
                                {"hours", data}});
        };
        cache("precipitation-detail.json", "open-meteo/hourly-precipitation/v1", precip);
        cache("air-quality-outlook.json", "open-meteo/cams-global-outlook/v1", air);
        // Production bindings are compiled once. Reuse inspection expressions so
        // repeated test-only compilation does not contaminate retention samples.
        QHash<QString, std::shared_ptr<QQmlExpression>> expressions;
        const auto sessionEval = [&](const QString& source) {
            auto& expression = expressions[source];
            if (!expression)
                expression = std::make_shared<QQmlExpression>(QQmlEngine::contextForObject(root),
                                                              root, source);
            auto result = expression->evaluate();
            if (expression->hasError())
                QTest::qFail(qPrintable(expression->error().toString()), __FILE__, __LINE__);
            return result;
        };
        QElapsedTimer startup;
        startup.start();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(sessionEval("backend.snapshot!==null && !backend.busy && "
                                             "backend.changesState==='ready'")
                                     .toBool(),
                                 5000);
        const auto startupMs = startup.elapsed();
        const auto original = fixture.saved("forecast.json");
        const auto precipCache = fixture.saved("precipitation-detail.json");
        const auto airCache = fixture.saved("air-quality-outlook.json");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            QTest::qWait(600);
            const auto before = swaps.size();
            const auto requests = sessionEval("backend.nextId").toInt();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ipc_requests"] = sessionEval("backend.nextId").toInt() - requests;
            result["fixture_startup_ms"] = startupMs;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "FEATURE_SESSION_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        const auto allClosed = [&]() {
            return sessionEval(
                       "!root.outdoorOpen && !root.dashboardOpen && !root.changesOpen && "
                       "!root.astronomyOpen && !root.precipitationOpen && !root.airOutlookOpen && "
                       "!root.shareOpen && !details.visible && outdoorLoader.item===null && "
                       "dashboardLoader.item===null && changesLoader.item===null && "
                       "astronomyLoader.item===null && precipitationLoader.item===null && "
                       "airOutlookLoader.item===null && shareLoader.item===null && "
                       "(!forecastSaveDialog.item || !forecastSaveDialog.item.visible) && "
                       "backend.outdoorState==='closed' && backend.astronomyState==='closed' && "
                       "!backend.precipitationWanted && backend.precipitationResult===null && "
                       "!backend.airOutlookWanted && backend.airOutlookResult===null && "
                       "!backend.mapWanted && !backend.radarWanted")
                .toBool();
        };
        QVERIFY(allClosed());
        phase("initial");
        if (qEnvironmentVariableIsSet("WEATHER_QT_FEATURE_SESSION_INITIAL_ONLY"))
            return;
        const QStringList panels{"outdoor",       "dashboard",   "changes", "astronomy",
                                 "precipitation", "air_quality", "metric",  "share"};
        const QStringList opens{"root.openOutdoor()",
                                "root.openDashboard()",
                                "root.openChanges()",
                                "root.openAstronomy()",
                                "root.openPrecipitation('')",
                                "root.openAirOutlook()",
                                "details.showMetric('pressure_msl_hpa')",
                                "root.openShare()"};
        const QStringList ready{
            "outdoorLoader.item!==null && backend.outdoorState==='ready'",
            "dashboardLoader.item!==null",
            "changesLoader.item!==null && backend.changesState==='ready'",
            "astronomyLoader.item!==null && backend.astronomyState==='ready'",
            "precipitationLoader.item!==null && backend.precipitationState==='fresh'",
            "airOutlookLoader.item!==null && backend.airOutlookState==='fresh'",
            "details.opened",
            "shareLoader.item!==null && shareLoader.item.opened"};
        const QStringList closes{"root.closeOutdoor()",       "root.closeDashboard()",
                                 "root.closeChanges()",       "root.closeAstronomy()",
                                 "root.closePrecipitation()", "root.closeAirOutlook()",
                                 "details.close()",           "root.closeShare()"};
        const auto quoted = [](const QString& value) {
            const auto json = QJsonDocument(QJsonArray{value}).toJson(QJsonDocument::Compact);
            return QString::fromUtf8(json.mid(1, json.size() - 2));
        };
        const auto output = fixture.directory.path() + "/session.png";
        QPointer<QObject> retainedDialog;
        for (int cycle = 1; cycle <= 20; ++cycle) {
            for (int index = 0; index < panels.size(); ++index) {
                sessionEval(opens[index]);
                QTRY_VERIFY2_WITH_TIMEOUT(sessionEval(ready[index]).toBool(),
                                          qPrintable(panels[index]), 3000);
                QTest::qWait(40); // Allow an actual rendered frame, not just object creation.
                if (panels[index] == "share") {
                    // Warm the real picker, then export only to the fixture's private path.
                    if (!qEnvironmentVariableIsSet("WEATHER_QT_FEATURE_SESSION_NO_PICKER")) {
                        sessionEval("shareLoader.item.chooseImageFile()");
                        auto* loader = named("forecastSaveDialogLoader");
                        QTRY_VERIFY(loader->property("item").value<QObject*>());
                        auto* dialog = loader->property("item").value<QObject*>();
                        if (retainedDialog)
                            QCOMPARE(dialog, retainedDialog.data());
                        else
                            retainedDialog = dialog;
                        QTRY_VERIFY(dialog->property("visible").toBool());
                        QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
                        QTRY_VERIFY(!dialog->property("visible").toBool());
                    }
                    if (!qEnvironmentVariableIsSet("WEATHER_QT_FEATURE_SESSION_NO_EXPORT")) {
                        QVERIFY(sessionEval("shareLoader.item.saveImage(" +
                                            quoted(QUrl::fromLocalFile(output).toString()) + ")")
                                    .toBool());
                        QTRY_VERIFY(!sessionEval("shareLoader.item.busy").toBool());
                        QCOMPARE(sessionEval("shareLoader.item.notice").toString(),
                                 QString("Forecast image saved."));
                        QVERIFY(QFileInfo(output).size() > 0);
                    }
                }
                if (cycle == 1) {
                    qInfo().noquote()
                        << "FEATURE_SESSION_PANEL"
                        << QJsonDocument(
                               QJsonObject{
                                   {"panel", panels[index]},
                                   {"ui_pss_kib", usage(QCoreApplication::applicationPid()).pssKiB},
                                   {"service_pss_kib", usage(fixture.service.processId()).pssKiB}})
                               .toJson(QJsonDocument::Compact);
                }
                sessionEval(closes[index]);
                QTRY_VERIFY(allClosed());
                QTRY_VERIFY(!sessionEval("backend.busy").toBool());
            }
            if (cycle == 1 || cycle == 10 || cycle == 20)
                phase(QString("closed_after_%1_sessions").arg(cycle));
        }
        QCOMPARE(fixture.saved("forecast.json"), original);
        QCOMPARE(fixture.saved("precipitation-detail.json"), precipCache);
        QCOMPARE(fixture.saved("air-quality-outlook.json"), airCache);
        window->hide();
        QTRY_VERIFY(!sessionEval("backend.presentationActive").toBool());
        QVERIFY(allClosed());
        phase("hidden");
        if (qEnvironmentVariableIsSet("WEATHER_QT_FEATURE_SESSION_COLLECT")) {
            // Diagnostic only: never force global collection in production to
            // conceal whether natural repeated use retains image or UI objects.
            engine->collectGarbage();
            phase("hidden_after_diagnostic_collection");
        }
    }
    void forecastSharingCopyImageAndLifecycle() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        auto forecast = fixture.saved("forecast.json");
        const auto day = forecast["daily"].toArray().first().toObject();
        QJsonArray days;
        for (int i = 0; i < 3; ++i) {
            auto row = day;
            row["date"] = QDate::fromString(day["date"].toString(), Qt::ISODate)
                              .addDays(i)
                              .toString(Qt::ISODate);
            days.append(row);
        }
        forecast["daily"] = days;
        fixture.save("forecast.json", forecast);
        fixture.savedNewYorkPlace();
        const auto original = fixture.saved("forecast.json");
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_SHARE_MEASURE");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "SHARE_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("initial");
        auto* loader = named("forecastShareLoader");
        QVERIFY(loader);
        QVERIFY(!loader->property("item").value<QObject*>());
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::keyClick(window, Qt::Key_Space);
        };
        auto* entry = qobject_cast<QQuickItem*>(named("openForecastShare"));
        QVERIFY(entry);
        activate("openForecastShare");
        QTRY_VERIFY(loader->property("item").value<QObject*>());
        auto* popup = loader->property("item").value<QObject*>();
        QTRY_VERIFY(popup->property("opened").toBool());
        QVERIFY(eval("root.shareOpen && !root.mapActive && !backend.forecastVisible").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QVERIFY(!named("forecastSaveDialogLoader")->property("item").value<QObject*>());
        const auto before = eval("backend.nextId").toInt();
        activate("copyForecastText");
        auto text = QGuiApplication::clipboard()->text();
        QVERIFY(text.contains("New York, NY"));
        QVERIFY(text.contains("America/New_York"));
        QVERIFY(text.contains("59°F"));
        QVERIFY(text.contains("Current conditions valid "));
        QVERIFY(text.contains(" UTC"));
        QVERIFY(text.contains("Precipitation chance 0%"));
        QVERIFY(text.contains("Current official alert status unavailable."));
        QVERIFY(text.contains("Open-Meteo"));
        QVERIFY(text.contains("CC BY 4.0"));
        QVERIFY(!text.contains("40.7128"));
        QVERIFY(!text.contains("-74.006"));
        activate("shareIncludePlace");
        activate("copyForecastText");
        text = QGuiApplication::clipboard()->text();
        QVERIFY(text.startsWith("Weather forecast\n"));
        QVERIFY(!text.contains("New York, NY"));
        QCOMPARE(eval("backend.nextId").toInt(), before);
        if (probe)
            QTest::qWait(5200);
        phase("preview_open");
        // Exercise actual PNG encoding, including the card portion outside the
        // scroll viewport. No app chrome, coordinates or place label is exported.
        const auto output = fixture.directory.path() + "/forecast.png";
        const auto quoted = [](const QString& value) {
            const auto json = QJsonDocument(QJsonArray{value}).toJson(QJsonDocument::Compact);
            return QString::fromUtf8(json.mid(1, json.size() - 2));
        };
        // Save goes through the lazily loaded file picker. Cancel creates no file.
        activate("saveForecastImage");
        auto* dialogLoader = named("forecastSaveDialogLoader");
        QTRY_VERIFY(dialogLoader->property("item").value<QObject*>());
        auto* dialog = dialogLoader->property("item").value<QObject*>();
        auto* firstFilename = qobject_cast<QQuickItem*>(named("fileNameTextField"));
        QVERIFY(firstFilename);
        QTRY_VERIFY(firstFilename->isVisible());
        if (!qEnvironmentVariable("WEATHER_QT_SHARE_SCREENSHOT_PREFIX").isEmpty()) {
            QTest::qWait(200);
            QVERIFY(window->grabWindow().save(
                qEnvironmentVariable("WEATHER_QT_SHARE_SCREENSHOT_PREFIX") + "-picker.png"));
        }
        firstFilename->window()->requestActivate();
        QTRY_VERIFY(firstFilename->window()->isActive());
        QTest::keyClick(firstFilename->window(), Qt::Key_Escape);
        QTRY_VERIFY(!dialog->property("visible").toBool());
        QTest::qWait(250); // Allow the fallback picker's exit transition to finish.
        QVERIFY(!QFile::exists(output));
        window->requestActivate();
        QTRY_VERIFY(window->isActive());
        activate("saveForecastImage");
        QTRY_VERIFY(dialogLoader->property("item").value<QObject*>());
        dialog = dialogLoader->property("item").value<QObject*>();
        QTRY_VERIFY(dialog->property("visible").toBool());
        QVERIFY(
            dialog->setProperty("currentFolder", QUrl::fromLocalFile(fixture.directory.path())));
        QTRY_COMPARE(dialog->property("currentFolder").toUrl(),
                     QUrl::fromLocalFile(fixture.directory.path()));
        auto* filename = qobject_cast<QQuickItem*>(named("fileNameTextField"));
        QVERIFY(filename);
        filename->forceActiveFocus();
        QInputMethodEvent input;
        input.setCommitString("forecast.png");
        QCoreApplication::sendEvent(filename, &input);
        QCOMPARE(filename->property("text").toString(), QString("forecast.png"));
        filename->window()->requestActivate();
        QTRY_VERIFY(filename->window()->isActive());
        QTest::keyClick(filename->window(), Qt::Key_Return);
        const std::function<QQuickItem*(QQuickItem*)> findSave =
            [&](QQuickItem* item) -> QQuickItem* {
            if (item->inherits("QQuickAbstractButton") &&
                item->property("text").toString().remove('&') == "Save")
                return item;
            for (auto* child : item->childItems())
                if (auto* result = findSave(child))
                    return result;
            return nullptr;
        };
        auto* saveAction = findSave(filename->window()->contentItem());
        QVERIFY(saveAction);
        saveAction->forceActiveFocus();
        QTest::keyClick(saveAction->window(), Qt::Key_Space);
        QTRY_COMPARE(popup->property("notice").toString(), QString("Forecast image saved."));
        QVERIFY(!popup->property("busy").toBool());
        QImageReader reader(output);
        QCOMPARE(reader.format(), QByteArray("png"));
        QCOMPARE(reader.size().width(), 720);
        QVERIFY(reader.size().height() > 400 && reader.size().height() <= 1600);
        const auto exported = reader.read();
        QVERIFY(!exported.isNull());
        QVERIFY(exported.pixelColor(20, exported.height() - 20).alpha() == 255);
        const auto prefix = qEnvironmentVariable("WEATHER_QT_SHARE_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QVERIFY(exported.save(prefix + "-export.png"));
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
        }
        // A failed destination is visible and does not disable retry.
        const auto bad =
            QUrl::fromLocalFile(fixture.directory.path() + "/missing/forecast.png").toString();
        QVERIFY(eval("shareLoader.item.saveImage(" + quoted(bad) + ")").toBool());
        QTRY_VERIFY(!popup->property("busy").toBool());
        QVERIFY(popup->property("notice").toString().startsWith("Could not save"));
        // Preview updates cannot leak unavailable values or turn null into zero.
        eval(
            "(function(){const s=JSON.parse(JSON.stringify(backend.snapshot)); "
            "s.source.freshness='stale'; s.forecast.current.temperature_c=null; "
            "s.forecast.daily[0].precipitation_probability=null; shareLoader.item.snapshot=s;})()");
        text = popup->property("plainText").toString();
        QVERIFY(text.contains("Stale forecast"));
        QVERIFY(text.contains("Precipitation chance —"));
        QVERIFY(!text.contains("59°F"));
        eval("(function(){const s=JSON.parse(JSON.stringify(backend.snapshot)); "
             "s.source.freshness='expired'; shareLoader.item.snapshot=s;})()");
        QCOMPARE(popup->property("plainText").toString(), QString());
        QVERIFY(!named("copyForecastText")->property("enabled").toBool());
        eval("shareLoader.item.snapshot=backend.snapshot");
        if (probe)
            QTest::qWait(5200); // Let the one-shot success/error notice expire.
        phase("details_open");
        activate("closeForecastShare");
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QCOMPARE(dialogLoader->property("item").value<QObject*>(), dialog);
        QVERIFY(!dialog->property("visible").toBool());
        QCOMPARE(window->activeFocusItem(), entry);
        window->resize(700, 650);
        eval("root.openShare()");
        QTRY_VERIFY(loader->property("item").value<QObject*>());
        popup = loader->property("item").value<QObject*>();
        QTRY_VERIFY(popup->property("opened").toBool());
        // Reuse the lazily created chooser after its original preview is gone.
        activate("saveForecastImage");
        QTRY_VERIFY(dialog->property("visible").toBool());
        QCOMPARE(dialogLoader->property("item").value<QObject*>(), dialog);
        QVERIFY(QMetaObject::invokeMethod(dialog, "reject"));
        QTRY_COMPARE(window->activeFocusItem(),
                     qobject_cast<QQuickItem*>(named("saveForecastImage")));
        window->requestActivate();
        QTRY_VERIFY(window->isActive());
        qobject_cast<QQuickItem*>(named("closeForecastShare"))->forceActiveFocus();
        QTest::qWait(250); // Finish picker exit and the queued focus-reveal scroll.
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
        }
        auto* flick = named("forecastShareScroll")->property("contentItem").value<QObject*>();
        QTRY_VERIFY(flick->property("contentHeight").toReal() > flick->property("height").toReal());
        QTRY_COMPARE(window->activeFocusItem(),
                     qobject_cast<QQuickItem*>(named("closeForecastShare")));
        QTest::keyClick(window, Qt::Key_PageDown);
        QTRY_VERIFY(flick->property("contentY").toReal() > 0);
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-footer.png"));
        // Closing before the queued capture begins leaves no output file.
        const auto canceled = fixture.directory.path() + "/canceled.png";
        QVERIFY(eval("shareLoader.item.saveImage(" +
                     quoted(QUrl::fromLocalFile(canceled).toString()) +
                     "); root.closeShare(); true")
                    .toBool());
        QTest::qWait(50);
        QVERIFY(!QFile::exists(canceled));
        for (int i = 0; i < (probe ? 40 : 5); ++i) {
            eval("root.openShare()");
            QTRY_VERIFY(loader->property("item").value<QObject*>());
            QPointer<QObject> guard = loader->property("item").value<QObject*>();
            QTRY_VERIFY(guard->property("opened").toBool());
            activate("copyForecastText");
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(guard.isNull());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        QCOMPARE(fixture.saved("forecast.json"), original);
        eval("root.openShare()");
        QTRY_VERIFY(loader->property("item").value<QObject*>());
        eval("shareLoader.item.chooseImageFile()");
        QTRY_VERIFY(dialog->property("visible").toBool());
        window->hide();
        QTRY_VERIFY(eval("!root.shareOpen && shareLoader.item===null").toBool());
        QTRY_VERIFY(!dialog->property("visible").toBool());
        phase("hidden");
    }
    void airQualityOutlookAndLazyLifecycle() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        fixture.savedNewYorkPlace();
        const auto now = QDateTime::currentDateTimeUtc();
        auto start = now;
        start.setTime(QTime(now.time().hour(), 0));
        QJsonArray hours;
        for (int i = 0; i < 48; ++i) {
            if (i == 3)
                continue; // Missing whole hour, distinct from known zero.
            hours.append(QJsonArray{double(start.addSecs(i * 3600).toSecsSinceEpoch()),
                                    i == 0 ? 0 : 125, 40, 12.5, 20, 8, QJsonValue::Null, 1, 230});
        }
        fixture.save("air-quality-outlook.json", {{"version", 1},
                                                  {"source", "open-meteo/cams-global-outlook/v1"},
                                                  {"latitude", 40.7128},
                                                  {"longitude", -74.006},
                                                  {"grid_latitude", 40.7128},
                                                  {"grid_longitude", -74.006},
                                                  {"timezone", "America/New_York"},
                                                  {"fetched_at", now.toString(Qt::ISODate)},
                                                  {"hours", hours}});
        const auto original = fixture.saved("forecast.json"),
                   cache = fixture.saved("air-quality-outlook.json");
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_AIR_OUTLOOK_MEASURE");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "AIR_OUTLOOK_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("initial");
        auto* loader = named("airOutlookLoader");
        QVERIFY(loader);
        QVERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(
            eval("backend.airOutlookState==='closed' && backend.airOutlookResult===null").toBool());
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(50);
            QTest::keyClick(window, Qt::Key_Space);
        };
        auto* entry = qobject_cast<QQuickItem*>(named("openAirOutlook"));
        QVERIFY(entry);
        activate("openAirOutlook");
        QTRY_COMPARE_WITH_TIMEOUT(eval("backend.airOutlookState").toString(), QString("fresh"),
                                  5000);
        QVERIFY(
            eval("root.airOutlookOpen && !root.mapActive && !backend.forecastVisible").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QCOMPARE(eval("backend.airOutlookResult.hours.length").toInt(), 48);
        QCOMPARE(named("airOutlookValue")->property("text").toString(), QString("0"));
        QCOMPARE(named("airOutlookCategory")->property("text").toString(), QString("Good"));
        const auto requestCount = eval("backend.nextId").toInt();
        auto* chart = qobject_cast<QQuickItem*>(named("airOutlookChart"));
        QVERIFY(chart);
        auto* accessibleChart = QAccessible::queryAccessibleInterface(chart);
        QVERIFY(accessibleChart);
        QVERIFY(accessibleChart->text(QAccessible::Description).contains(": 0, Good."));
        QVERIFY(accessibleChart->text(QAccessible::Description)
                    .startsWith(named("airOutlookTime")->property("text").toString() + ". "));
        chart->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_End);
        QCOMPARE(named("airOutlookValue")->property("text").toString(), QString("125"));
        QCOMPARE(named("airOutlookCategory")->property("text").toString(),
                 QString("Unhealthy for sensitive groups"));
        QVERIFY(accessibleChart->text(QAccessible::Description)
                    .contains(": 125, Unhealthy for sensitive groups."));
        QTest::keyClick(window, Qt::Key_Home);
        for (int i = 0; i < 3; ++i)
            QTest::keyClick(window, Qt::Key_Right);
        QCOMPARE(named("airOutlookValue")->property("text").toString(), QString::fromUtf8("—"));
        QCOMPARE(named("airOutlookCategory")->property("text").toString(), QString("Unavailable"));
        QVERIFY(accessibleChart->text(QAccessible::Description).contains(": unavailable."));
        QTest::keyClick(window, Qt::Key_Home);
        auto* selector = qobject_cast<QQuickItem*>(named("airOutlookMetric"));
        QVERIFY(selector);
        selector->forceActiveFocus();
        const QStringList values{"40", "12.5 µg/m³", "20.0 µg/m³", "8.0 µg/m³",
                                 "—",  "1.0 µg/m³",  "230.0 µg/m³"};
        for (const auto& value : values) {
            QTest::keyClick(window, Qt::Key_Down);
            QCOMPARE(named("airOutlookValue")->property("text").toString(), value);
            QVERIFY(
                accessibleChart->text(QAccessible::Description)
                    .contains(": " + (value == QString::fromUtf8("—") ? "unavailable" : value)));
        }
        QCOMPARE(eval("backend.nextId").toInt(), requestCount);
        auto* popup = loader->property("item").value<QObject*>();
        QVERIFY(popup->setProperty("metric", "ozone"));
        QVERIFY(named("airOutlookRange")->property("text").toString().startsWith("No samples"));
        QVERIFY(popup->setProperty("metric", "us_aqi"));
        // A newer completion must survive an older opening reply.
        QVERIFY(eval("(function(){const v=JSON.parse(JSON.stringify(backend.airOutlookResult)); "
                     "v.revision+=2; backend.applyAirOutlook(v); const "
                     "old=JSON.parse(JSON.stringify(v)); "
                     "old.revision--; old.status='loading'; old.fetched_at=null; old.hours=[]; "
                     "backend.applyAirOutlook(old); return "
                     "backend.airOutlookResult.revision===v.revision;})()")
                    .toBool());
        for (const auto& mutation :
             {"v.latitude=0", "v.hours[0].us_aqi=-1", "v.hours[1].time=v.hours[0].time",
              "v.hours.pop()", "v.source='station'"}) {
            QVERIFY(
                eval("(function(){const v=JSON.parse(JSON.stringify(backend.airOutlookResult)); "
                     "v.revision++; " +
                     QString(mutation) +
                     "; try {backend.applyAirOutlook(v); return false;} "
                     "catch(e){return true;}})()")
                    .toBool());
        }
        const auto ready = eval("JSON.stringify(backend.airOutlookResult)").toString();
        eval("(function(){const v=JSON.parse(JSON.stringify(backend.airOutlookResult)); "
             "v.revision++; v.status='stale'; v.error='offline'; backend.applyAirOutlook(v);})()");
        QVERIFY(named("airOutlookStatus")->property("text").toString().contains("Cached outlook"));
        QVERIFY(named("airOutlookStatus")->property("text").toString().contains("Offline"));
        eval("(function(){const v=" + ready +
             "; v.revision=backend.airOutlookResult.revision+1; "
             "backend.applyAirOutlook(v);})()");
        phase("details_open");
        const auto prefix = qEnvironmentVariable("WEATHER_QT_AIR_OUTLOOK_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
        }
        activate("closeAirOutlook");
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QCOMPARE(window->activeFocusItem(), entry);
        window->resize(700, 650);
        eval("root.openAirOutlook()");
        QTRY_COMPARE(eval("backend.airOutlookState").toString(), QString("fresh"));
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
        }
        QTest::keyClick(window, Qt::Key_PageDown);
        auto* flick = named("airOutlookScroll")->property("contentItem").value<QObject*>();
        QTRY_VERIFY(flick->property("contentY").toReal() > 0);
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-footer.png"));
        // Closing after an unsent retry cancels the last SENT token,
        // not the newer queued token that the service never received.
        eval("backend.loadAirOutlook(); backend.loadAirOutlook(); "
             "root.closeAirOutlook()");
        QTRY_VERIFY(!eval("backend.busy").toBool());
        QVERIFY(eval("backend.airOutlookResult===null && !backend.airOutlookWanted && "
                     "backend.queuedAirOutlook===null")
                    .toBool());
        // A second subscribed peer can acquire the view only after the main
        // bridge's queued close has actually released its service ownership.
        QLocalSocket lease;
        lease.connectToServer(fixture.socket);
        QVERIFY(lease.waitForConnected(1000));
        QByteArray incoming;
        const auto leaseRequest = [&](const QJsonObject& request) {
            lease.write(QJsonDocument(request).toJson(QJsonDocument::Compact) + '\n');
            lease.flush();
            QElapsedTimer deadline;
            deadline.start();
            while (deadline.elapsed() < 2000) {
                incoming += lease.readAll();
                while (incoming.contains('\n')) {
                    const auto index = incoming.indexOf('\n');
                    const auto message = QJsonDocument::fromJson(incoming.left(index)).object();
                    incoming.remove(0, index + 1);
                    if (message["request_id"] == request["request_id"])
                        return message;
                }
                lease.waitForReadyRead(50);
            }
            return QJsonObject{};
        };
        QVERIFY(
            leaseRequest({{"version", 1}, {"request_id", 91}, {"op", "subscribe"}})["ok"].toBool());
        const auto query =
            QJsonDocument::fromJson(
                eval("JSON.stringify({location_id:backend.snapshot.saved_locations.viewed,latitude:"
                     "backend.snapshot.latitude,longitude:backend.snapshot.longitude,timezone:"
                     "backend.snapshot.timezone,client_token:90})")
                    .toString()
                    .toUtf8())
                .object();
        QVERIFY(leaseRequest({{"version", 1},
                              {"request_id", 92},
                              {"op", "air_outlook_open"},
                              {"detail", query}})["ok"]
                    .toBool());
        QVERIFY(leaseRequest({{"version", 1},
                              {"request_id", 93},
                              {"op", "air_outlook_close"},
                              {"detail", QJsonObject{{"client_token", 90}}}})["ok"]
                    .toBool());
        lease.disconnectFromServer();
        for (int i = 0; i < (probe ? 40 : 5); i++) {
            eval("root.openAirOutlook()");
            QTRY_COMPARE(eval("backend.airOutlookState").toString(), QString("fresh"));
            QPointer<QObject> popup = loader->property("item").value<QObject*>();
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(popup.isNull());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        QCOMPARE(fixture.saved("forecast.json"), original);
        QCOMPARE(fixture.saved("air-quality-outlook.json"), cache);
        eval("root.openAirOutlook()");
        window->hide();
        QTRY_VERIFY(eval("!root.airOutlookOpen && !backend.airOutlookWanted && "
                         "backend.airOutlookResult===null && airOutlookLoader.item===null")
                        .toBool());
        phase("hidden");
    }
    void astronomyDateNavigationAndLazyLifecycle() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        fixture.savedNewYorkPlace();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_ASTRONOMY_MEASURE");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "ASTRONOMY_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("initial");
        if (qEnvironmentVariableIsSet("WEATHER_QT_ASTRONOMY_INITIAL_ONLY"))
            return;
        auto* loader = named("astronomyLoader");
        QVERIFY(loader);
        QVERIFY(!loader->property("item").value<QObject*>());
        QCOMPARE(eval("backend.astronomyState").toString(), QString("closed"));
        const auto original = fixture.saved("forecast.json");
        // Open the real solar card with the mouse; today must load on its own.
        auto* card = qobject_cast<QQuickItem*>(named("currentMetric_solar"));
        QVERIFY(card);
        card->forceActiveFocus();
        QTest::qWait(100);
        click("currentMetric_solar");
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.astronomyState==='ready'").toBool(), 5000);
        QVERIFY(eval("root.astronomyOpen && !root.mapActive && !backend.forecastVisible").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        const auto today = eval("backend.astronomyResult.date").toString();
        QCOMPARE(today, eval("backend.astronomyResult.today").toString());
        QVERIFY(named("astronomySelectedDate")->property("text").toString().startsWith("Today · "));
        auto* input = qobject_cast<QQuickItem*>(named("astronomyDate"));
        QVERIFY(input);
        QCOMPARE(input->property("text").toString(), today);
        QVERIFY(!input->isVisible());
        QVERIFY(
            named("astronomyDaylight")->property("text").toString().contains("remaining today"));
        QVERIFY(named("astronomyMoonPhase")->property("text").toString().contains("illuminated"));
        auto* closeButton = qobject_cast<QQuickItem*>(named("closeAstronomy"));
        QVERIFY(closeButton);
        auto* tooltip = closeButton->findChild<QObject*>("actionTooltip");
        QVERIFY(tooltip);
        QTest::mouseMove(window, QPoint(5, 5));
        closeButton->forceActiveFocus();
        QTest::qWait(700);
        QVERIFY(closeButton->hasActiveFocus());
        QVERIFY(!tooltip->property("visible").toBool());
        QTest::mouseMove(
            window,
            closeButton->mapToScene(QPointF(closeButton->width() / 2, closeButton->height() / 2))
                .toPoint());
        QTRY_VERIFY(tooltip->property("visible").toBool());
        const auto tooltipPrefix = qEnvironmentVariable("WEATHER_QT_ASTRONOMY_SCREENSHOT_PREFIX");
        if (!tooltipPrefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(tooltipPrefix + "-tooltip.png"));
        }
        QTest::mouseMove(window, QPoint(5, 5));
        QTRY_VERIFY(!tooltip->property("visible").toBool());
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(50);
            QTest::keyClick(window, Qt::Key_Space);
        };
        activate("changeAstronomyDate");
        QTRY_VERIFY(input->isVisible() && input->hasActiveFocus());
        activate("astronomyNext");
        QTRY_VERIFY(eval("backend.astronomyState==='ready'").toBool());
        QVERIFY(eval("backend.astronomyResult.date").toString() > today);
        QVERIFY(
            !named("astronomyDaylight")->property("text").toString().contains("remaining today"));
        activate("astronomyPrevious");
        QTRY_COMPARE(eval("backend.astronomyResult ? backend.astronomyResult.date : ''").toString(),
                     today);
        input->setProperty("text", "bad-date");
        input->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(eval("backend.astronomyState").toString(), QString("unavailable"));
        QVERIFY(eval("backend.available").toBool());
        qobject_cast<QQuickItem*>(named("astronomyToday"))->forceActiveFocus();
        QTest::qWait(80);
        click("astronomyToday");
        QTRY_VERIFY(eval("backend.astronomyState==='ready'").toBool());
        QCOMPARE(input->property("text").toString(), today);
        QVERIFY(named("astronomySelectedDate")->property("text").toString().startsWith("Today · "));
        activate("changeAstronomyDate");
        QVERIFY(!input->isVisible());
        phase("details_open");
        const auto prefix = qEnvironmentVariable("WEATHER_QT_ASTRONOMY_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
        }
        window->resize(700, 650);
        activate("closeAstronomy");
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QCOMPARE(window->activeFocusItem(), card);
        eval("root.openAstronomy()");
        QTRY_VERIFY(eval("backend.astronomyState==='ready'").toBool());
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
        }
        QTest::keyClick(window, Qt::Key_PageDown);
        auto* flick = named("astronomyScroll")->property("contentItem").value<QObject*>();
        QTRY_VERIFY(flick->property("contentY").toReal() > 0);
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-footer.png"));
        // The latest date replaces queued dates; a close invalidates pending replies.
        eval("backend.loadAstronomy(''); backend.loadAstronomy(''); root.closeAstronomy()");
        QTRY_VERIFY(!eval("backend.busy").toBool());
        QVERIFY(
            eval("backend.astronomyResult===null && backend.astronomyState==='closed'").toBool());
        for (int i = 0; i < (probe ? 40 : 5); i++) {
            eval("root.openAstronomy()");
            QTRY_VERIFY(eval("backend.astronomyState==='ready'").toBool());
            QPointer<QObject> details = loader->property("item").value<QObject*>();
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(details.isNull());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        QCOMPARE(fixture.saved("forecast.json"), original);
        eval("root.openAstronomy()");
        window->hide();
        QTRY_VERIFY(eval("!root.astronomyOpen && backend.astronomyResult===null && "
                         "astronomyLoader.item===null")
                        .toBool());
        phase("hidden");
    }
    void forecastChangesPresentationDetailsAndRestart() {
        ServiceFixture fixture;
        fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        auto forecast = fixture.saved("forecast.json");
        auto row = forecast["hourly"].toArray()[0].toObject();
        auto start = QDateTime::currentDateTimeUtc();
        start.setTime(QTime(start.time().hour(), 0));
        QJsonArray hours, oldHours;
        for (int i = 0; i < 60; ++i) {
            const auto stamp = start.addSecs(i * 3600).toString(Qt::ISODate);
            row["time"] = stamp;
            hours.append(row);
            oldHours.append(QJsonArray{stamp, 10, .6, 2, 2});
        }
        forecast["hourly"] = hours;
        fixture.save("forecast.json", forecast);
        fixture.savedNewYorkPlace();
        const auto location = forecast["location"].toObject();
        QJsonObject baseline{
            {"latitude", location["latitude"]},
            {"longitude", location["longitude"]},
            {"timezone", location["timezone"]},
            {"source", "open-meteo-hourly-v1"},
            {"retrieved", QDateTime::fromString(forecast["fetched_at"].toString(), Qt::ISODate)
                              .addSecs(-3600)
                              .toString(Qt::ISODate)},
            {"hours", oldHours}};
        fixture.save("forecast-history.json",
                     {{"schema_version", 1},
                      {"places", QJsonArray{QJsonObject{{"id", "place-5128581"},
                                                        {"current", baseline},
                                                        {"previous", QJsonValue::Null}}}}});
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.snapshot!==null && !backend.busy").toBool(), 5000);
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_CHANGES_MEASURE");
        const bool baselineOnly = qEnvironmentVariableIsSet("WEATHER_QT_CHANGES_BASELINE_ONLY");
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "CHANGES_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        if (baselineOnly) {
            phase("initial");
            return;
        }
        QTRY_VERIFY2_WITH_TIMEOUT(
            eval("backend.changesState==='ready'").toBool(),
            qPrintable(
                eval("JSON.stringify({state:backend.changesState,error:backend.error,key:backend."
                     "forecastContext,visible:backend.forecastVisible,op:backend.pendingOp})")
                    .toString()),
            5000);
        QCOMPARE(eval("backend.changesResult.changes.length").toInt(), 3);
        const auto saved = fixture.saved("forecast-history.json");
        QCOMPARE(saved["places"].toArray()[0].toObject()["previous"].toObject()["retrieved"],
                 baseline["retrieved"]);
        phase("initial");
        if (qEnvironmentVariableIsSet("WEATHER_QT_CHANGES_INITIAL_ONLY"))
            return;
        const auto activate = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(80);
            QTest::keyClick(window, Qt::Key_Space);
        };
        auto* loader = named("forecastChangesLoader");
        QVERIFY(!loader->property("item").value<QObject*>());
        showSettingsSection("settingsApplicationSection");
        activate("openForecastChanges");
        QTRY_VERIFY(eval("root.changesOpen && changesLoader.item!==null").toBool());
        QVERIFY(!eval("root.mapActive || backend.forecastVisible").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QVERIFY(named("forecastChangesProvenance")
                    ->property("text")
                    .toString()
                    .contains("Previous viewed:"));
        QVERIFY(named("forecastChangesCoverage")->property("text").toString().contains("48/48"));
        phase("details_open");
        const auto prefix = qEnvironmentVariable("WEATHER_QT_CHANGES_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
            window->resize(700, 650);
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
        }
        window->resize(700, 650);
        QTest::qWait(100);
        auto* detailScroll =
            named("forecastChangesScroll")->property("contentItem").value<QObject*>();
        QVERIFY(detailScroll);
        QTest::keyClick(window, Qt::Key_End);
        QTRY_VERIFY(detailScroll->property("contentY").toReal() > 0);
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-compact-footer.png"));
        QTest::keyClick(window, Qt::Key_Home);
        QTRY_COMPARE(detailScroll->property("contentY").toReal(), 0.0);
        QPointer<QObject> detail = loader->property("item").value<QObject*>();
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(detail.isNull());
        QTRY_VERIFY(eval("backend.changesState==='ready'").toBool());
        QCOMPARE(window->activeFocusItem()->objectName(), QString("openForecastChanges"));
        QVERIFY(eval("root.effectsOpen").toBool());
        for (int i = 0; i < (probe ? 40 : 5); ++i) {
            eval("root.openChanges()");
            QTRY_VERIFY(loader->property("item").value<QObject*>());
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(!loader->property("item").value<QObject*>());
            QTRY_VERIFY(eval("backend.changesState==='ready'").toBool());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        QCOMPARE(fixture.saved("forecast-history.json"), saved);
        eval("root.openChanges()");
        window->hide();
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        phase("hidden");
        window->show();
        QTRY_VERIFY(eval("backend.changesState==='ready'").toBool());
        eval("backend.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        cleanup();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.changesState==='ready'").toBool(), 5000);
        QCOMPARE(eval("backend.changesResult.changes.length").toInt(), 3);
        QCOMPARE(fixture.saved("forecast-history.json"), saved);
        if (!prefix.isEmpty()) {
            QTest::qWait(100);
            QVERIFY(window->grabWindow().save(prefix + "-summary.png"));
        }
    }
    void outdoorPlannerUsesCachedForecastAndSavesPreferences() {
        ServiceFixture fixture;
        const bool probe = qEnvironmentVariableIsSet("WEATHER_QT_OUTDOOR_MEASURE");
        if (probe)
            fixture.save("controls.json", {{"visual_quality", "static"}, {"reduced_motion", true}});
        fixture.cache(60);
        auto forecast = fixture.saved("forecast.json");
        auto row = forecast["hourly"].toArray()[0].toObject();
        auto start = QDateTime::currentDateTimeUtc();
        start.setTime(QTime(start.time().hour(), 0));
        QJsonArray hours;
        for (int i = 0; i < 60; ++i) {
            row["time"] = start.addSecs(i * 3600).toString(Qt::ISODate);
            hours.append(row);
        }
        forecast["hourly"] = hours;
        fixture.save("forecast.json", forecast);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY2_WITH_TIMEOUT(
            eval("root.forecast !== null && root.forecast.hourly.length > 40 && !backend.busy")
                .toBool(),
            qPrintable(eval("JSON.stringify({error:backend.error, source:backend.snapshot ? "
                            "backend.snapshot.source : null, count:root.hours.length})")
                           .toString() +
                       fixture.service.readAll()),
            5000);
        eval("root.effectsOpen = false; root.locationsOpen = false");
        const auto focusClick = [&](const char* name) {
            auto* item = qobject_cast<QQuickItem*>(named(name));
            QVERIFY(item);
            item->forceActiveFocus();
            QTest::qWait(100); // Let keyboard reveal scroll the control into view.
            const auto center = item->mapToScene(QPointF(item->width() / 2, item->height() / 2));
            QVERIFY(center.y() >= 0 && center.y() < window->height());
            click(name);
        };
        auto* loader = named("outdoorPlannerLoader");
        QVERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(fixture.saved("outdoor-preferences.json").isEmpty());
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        const auto phase = [&](const QString& name) {
            if (!probe)
                return;
            QTest::qWait(200);
            const auto before = swaps.size();
            auto result = measure(name, fixture.service.processId());
            result["frame_swaps"] = swaps.size() - before;
            result["ui_pss_kib"] = usage(QCoreApplication::applicationPid()).pssKiB;
            result["service_pss_kib"] = usage(fixture.service.processId()).pssKiB;
            qInfo().noquote() << "OUTDOOR_PERF"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        phase("before_open");
        showSettingsSection("settingsApplicationSection");
        focusClick("openOutdoorPlanner");
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.outdoorState === 'ready'").toBool(), 3000);
        QVERIFY(!eval("root.mapActive").toBool());
        QVERIFY(!named("forecastAtmosphere")->property("presentationActive").toBool());
        QCOMPARE(eval("backend.outdoorResult.windows.length").toInt(), 3);
        QVERIFY(eval("backend.outdoorResult.windows.every(w => !w.fits && "
                     "w.missing.indexOf('daylight') >= 0)")
                    .toBool());
        QVERIFY(fixture.saved("outdoor-preferences.json").isEmpty());
        focusClick("outdoorAdjustPreferences");
        auto* duration = qobject_cast<QQuickItem*>(named("outdoor_hours"));
        QVERIFY(duration);
        duration->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        QTRY_VERIFY(eval("outdoorLoader.item.preferences.hours === 2 && outdoorLoader.item.dirty")
                        .toBool());
        focusClick("outdoorDaylight");
        QVERIFY(fixture.saved("outdoor-preferences.json").isEmpty());
        focusClick("outdoorFindTimes");
        QTRY_VERIFY_WITH_TIMEOUT(
            eval("backend.outdoorState === 'ready' && backend.outdoorResult.preferences.hours === "
                 "2 && backend.outdoorResult.windows.every(w => w.fits)")
                .toBool(),
            3000);
        QCOMPARE(fixture.saved("outdoor-preferences.json")["hours"].toInt(), 2);
        QCOMPARE(fixture.saved("outdoor-preferences.json")["daylight_only"].toBool(), false);
        focusClick("outdoorAdjustPreferences");
        phase("open_after_preferences");
        const auto prefix = qEnvironmentVariable("WEATHER_QT_OUTDOOR_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-wide.png"));
            window->resize(700, 650);
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-compact.png"));
            focusClick("outdoorAdjustPreferences");
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-preferences.png"));
        }
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(
            eval("backend.outdoorResult === null && backend.outdoorState === 'closed'").toBool());
        for (int i = 0; i < (probe ? 40 : 20); ++i) {
            eval("root.openOutdoor()");
            QTRY_VERIFY_WITH_TIMEOUT(eval("backend.outdoorState === 'ready'").toBool(), 3000);
            QVERIFY(eval("outdoorLoader.item.preferences.hours === 2").toBool());
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(!loader->property("item").value<QObject*>());
            if (i == 19)
                phase("closed_after_20");
            if (i == 39)
                phase("closed_after_40");
        }
        eval("root.openOutdoor()");
        QTRY_VERIFY_WITH_TIMEOUT(eval("backend.outdoorState === 'ready'").toBool(), 3000);
        window->hide();
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(eval("backend.outdoorResult === null && !root.outdoorOpen").toBool());
        phase("hidden");
    }
    void cachedPointMetricsUnitsAndHour() {
        ServiceFixture fixture;
        fixture.cache(4000, true);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.current!==null").toBool(), 5000);
        QVERIFY(eval("root.freshness").toString().startsWith("Stale forecast"));
        QCOMPARE(named("currentMetricValue_uv")->property("text").toString(), QString("0.0"));
        QCOMPARE(named("currentMetricValue_pressure")->property("text").toString(),
                 QString("29.92 inHg"));
        QCOMPARE(named("currentMetricDetail_pressure")->property("text").toString(),
                 QString("Mean sea level"));
        QCOMPARE(named("currentMetricDetail_humidity")->property("text").toString(),
                 QString("Dew point 55°"));
        QTest::keyClick(window, Qt::Key_Escape);
        eval("details.showHour(root.hours[0])");
        QTRY_VERIFY(named("forecastDetails")->property("visible").toBool());
        QCOMPARE(named("detailMetricValue_uv")->property("text").toString(), QString("3.2"));
        QCOMPARE(named("detailMetricValue_pressure")->property("text").toString(),
                 QString("29.79 inHg"));
        QCOMPARE(named("detailMetricValue_dew_point")->property("text").toString(), QString("49°"));
        click("closeForecastDetails");
        QTest::keyClick(window, Qt::Key_Escape);
        selectUnits("C");
        QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(), QString("C"), 3000);
        QCOMPARE(named("currentMetricDetail_humidity")->property("text").toString(),
                 QString("Dew point 13°"));
        QCOMPARE(named("currentMetricValue_pressure")->property("text").toString(),
                 QString("1013 hPa"));
        eval("details.showHour(root.hours[0])");
        QTRY_VERIFY(named("forecastDetails")->property("visible").toBool());
        QCOMPARE(named("detailMetricValue_dew_point")->property("text").toString(), QString("10°"));
        QCOMPARE(named("detailMetricValue_pressure")->property("text").toString(),
                 QString("1009 hPa"));
        auto* windChoice = named("windUnitsChoice");
        QVERIFY(windChoice);
        QCOMPARE(windChoice->property("currentIndex").toInt(),
                 0); // Legacy controls default to Auto.
        windChoice->setProperty("currentIndex", 4);
        QVERIFY(QMetaObject::invokeMethod(windChoice, "activated", Q_ARG(int, 4)));
        QTRY_COMPARE_WITH_TIMEOUT(eval("root.windUnits").toString(), QString("kn"), 3000);
        QCOMPARE(eval("root.units").toString(), QString("C"));
        QCOMPARE(named("currentMetricValue_wind")->property("text").toString(), QString("S 2 kn"));
        QCOMPARE(named("currentMetricDetail_wind")->property("text").toString(),
                 QString("Gusts 4 kn"));
        eval("details.metric='wind_speed_m_s'");
        auto* trend = named("forecastTrend");
        QVERIFY(trend);
        QQmlExpression chartText(QQmlEngine::contextForObject(trend), trend, "valueText(10)");
        QCOMPARE(chartText.evaluate().toString(), QString("19 kn"));
        QCOMPARE(fixture.saved("controls.json")["wind_units"].toString(), QString("kn"));

        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void airQualityOfflineCache_data() {
        QTest::addColumn<int>("fetchedAge");
        QTest::addColumn<int>("validAge");
        QTest::addColumn<QString>("freshness");
        QTest::addColumn<QString>("value");
        QTest::newRow("fresh") << 60 << 120 << QString("fresh") << QString("0");
        QTest::newRow("stale-valid-time") << 60 << 10800 << QString("stale") << QString("0");
        QTest::newRow("expired") << 25200 << 25200 << QString("expired") << QString("—");
        QTest::newRow("invalid-future")
            << -600 << -600 << QString("invalid_future") << QString("—");
    }
    void airQualityOfflineCache() {
        QFETCH(int, fetchedAge);
        QFETCH(int, validAge);
        QFETCH(QString, freshness);
        QFETCH(QString, value);
        ServiceFixture fixture;
        fixture.cache(60, true);
        fixture.airQualityCache(fetchedAge, validAge);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QCOMPARE(eval("bridge.snapshot.air_quality.freshness").toString(), freshness);
        QVERIFY(eval("bridge.snapshot.air_quality.offline").toBool());
        QCOMPARE(eval("root.current.temperature_c").toDouble(), 15.0);
        QCOMPARE(named("airQualityValue_us")->property("text").toString(), value);
        QCOMPARE(named("airQualityValue_eu")->property("text").toString(),
                 value == "—" ? QString("—") : QString("125"));
        QCOMPARE(named("airQualityValue_pm")->property("text").toString(),
                 value == "—" ? QString("—") : QString::fromUtf8("12.4 µg/m³"));
        QVERIFY(named("airQualityStatus")->property("text").toString().contains("Offline"));
        QVERIFY(named("airQualityAttribution")
                    ->property("text")
                    .toString()
                    .contains("CAMS global model"));
        const auto validLabel = eval("bridge.snapshot.air_quality.valid_label").toString();
        const auto fetchedLabel = eval("bridge.snapshot.air_quality.fetched_label").toString();
        QVERIFY(!validLabel.isEmpty());
        QVERIFY(!fetchedLabel.isEmpty());
        QCOMPARE(named("airQualityTimes")->property("text").toString(),
                 QString("Model forecast valid ") + validLabel + " · Fetched " + fetchedLabel);
        QVERIFY(validLabel.contains("(-04:00)") || validLabel.contains("(-05:00)"));
        if (freshness == "fresh") {
            QTest::keyClick(window, Qt::Key_Escape);
            selectUnits("C");
            QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(), QString("C"), 3000);
            QCOMPARE(named("airQualityValue_us")->property("text").toString(), QString("0"));
            QCOMPARE(named("airQualityValue_eu")->property("text").toString(), QString("125"));
            QCOMPARE(named("airQualityValue_pm")->property("text").toString(),
                     QString::fromUtf8("12.4 µg/m³"));
        }
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void invalidAirQualityCacheKeepsWeather() {
        ServiceFixture fixture;
        fixture.cache(60, true);
        fixture.airQualityCache(60, 120);
        auto bad = fixture.saved("air-quality.json");
        auto source = bad["source"].toObject();
        source["model"] = "Station";
        bad["source"] = source;
        fixture.save("air-quality.json", bad);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QCOMPARE(eval("root.current.temperature_c").toDouble(), 15.0);
        QCOMPARE(eval("bridge.snapshot.air_quality.freshness").toString(), QString("unavailable"));
        QCOMPARE(eval("bridge.snapshot.air_quality.error").toString(), QString("cache_invalid"));
        QVERIFY(eval("bridge.snapshot.air_quality.valid_label===null&&bridge.snapshot.air_quality."
                     "fetched_label===null")
                    .toBool());
        QCOMPARE(named("airQualityValue_us")->property("text").toString(), QString("—"));
        QVERIFY(
            named("airQualityStatus")->property("text").toString().contains("Cached data invalid"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void offlinePlaceAndSearchCoverage() {
        ServiceFixture fixture;
        fixture.savedBerlinPlace();
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QCOMPARE(eval("root.location").toString(), QString("Berlin, Berlin, Germany"));
        QCOMPARE(eval("bridge.snapshot.location_settings.mode").toString(), QString("place"));
        QCOMPARE(eval("bridge.snapshot.location_settings.place.id").toInt(), 2950159);
        QCOMPARE(eval("bridge.snapshot.alerts.status").toString(), QString("not_supported_here"));
        QCOMPARE(eval("bridge.snapshot.alerts.coverage").toString(), QString("unsupported"));
        QVERIFY(
            named("alertCoverageStatus")->property("text").toString().contains("not supported"));
        QVERIFY(!named("sourceAttribution")
                     ->property("text")
                     .toString()
                     .contains("National Weather Service"));
        eval("root.openSettings(false)");
        named("placeQuery")->setProperty("text", "Berlin");
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status").toString(),
                                  QString("error"), 5000);
        QCOMPARE(eval("bridge.snapshot.place_search.error").toString(), QString("offline"));
        QVERIFY(named("placeSearchStatus")->property("text").toString().contains("offline"));
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status").toString(),
                                  QString("idle"), 3000);
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void liveBerlinPicker() {
        if (qEnvironmentVariable("A_WEATHER_APP_LIVE_PLACES") != "1")
            QSKIP("Opt-in live provider UI test");
        ServiceFixture fixture;
        QVERIFY2(fixture.start(false), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QTRY_VERIFY(window->isExposed());
        eval("root.openSettings(false)");
        auto* query = qobject_cast<QQuickItem*>(named("placeQuery"));
        QVERIFY(query);
        named("placeCountry")->setProperty("text", "DE");
        query->setProperty("text", "Berlin");
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status==='ready' && "
                                      "bridge.snapshot.place_search.results.length>0")
                                     .toBool(),
                                 20000);
        QCOMPARE(eval("bridge.snapshot.place_search.results[0].country_code").toString(),
                 QString("DE"));
        query->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        QCOMPARE(named("placeResults")->property("currentIndex").toInt(), 0);
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.location_settings.mode").toString(),
                                  QString("place"), 30000);
        QCOMPARE(eval("bridge.snapshot.location_settings.country_code").toString(), QString("DE"));
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.current!==null").toBool(), 30000);
        QCOMPARE(eval("bridge.snapshot.alerts.status").toString(), QString("not_supported_here"));
        QVERIFY(
            named("alertCoverageStatus")->property("text").toString().contains("not supported"));
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
    }
    void cachedControlsDetailsAndReload() {
        ServiceFixture fixture;
        fixture.cache(4000);
        QVERIFY2(fixture.start(), qPrintable(fixture.service.readAll()));
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QTRY_VERIFY(window->isExposed());
        QVERIFY(eval("root.freshness").toString().startsWith("Stale forecast"));
        QCOMPARE(eval("root.hours.length").toInt(), 1);
        QTest::keyClick(window, Qt::Key_Escape);
        selectUnits("C");
        QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(), QString("C"), 3000);
        QCOMPARE(fixture.saved("controls.json")["units"].toString(), QString("C"));
        eval("details.showHour(root.hours[0])");
        QTRY_VERIFY(named("forecastDetails")->property("visible").toBool());
        QCOMPARE(named("detailMetricValue_uv")->property("text").toString(), QString("—"));
        QCOMPARE(named("detailMetricValue_pressure")->property("text").toString(), QString("—"));
        QCOMPARE(named("detailMetricValue_dew_point")->property("text").toString(), QString("—"));
        click("closeForecastDetails");
        click("openEffects");
        QTRY_VERIFY(named("effectsDrawer")->property("visible").toBool());
        toggle("reducedMotion", true);
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.controls.reduced_motion").toBool(), 3000);
        toggle("windowPhysics", false);
        QTRY_VERIFY_WITH_TIMEOUT(!eval("root.controls.window_physics").toBool(), 3000);
        QCOMPARE(fixture.saved("controls.json")["reduced_motion"].toBool(), true);
        // UI signal dispatch for drawer switches tests the actual onClicked persistence path.
        toggle("notificationQuiet", false);
        QTRY_VERIFY_WITH_TIMEOUT(
            !eval("bridge.snapshot.notifications.settings.quiet_enabled").toBool(), 3000);
        QVERIFY2(eval("bridge.snapshot.notifications.supported").toBool(),
                 "Full notification UI acceptance requires the optional libnotify test dependency");
        toggle("notificationEnabled", true);
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.watchingPrecipitation").toBool(), 3000);
        QVERIFY(
            QMetaObject::invokeMethod(named("notificationPause"), "clicked", Qt::DirectConnection));
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.notifications.state").toString(),
                                  QString("paused"), 3000);
        QVERIFY(fixture.saved("notifications.json")["snoozed_until"].toDouble() >
                QDateTime::currentSecsSinceEpoch());
        eval("root.dismissWindow()");
        QTRY_VERIFY(!window->isVisible());
        QCOMPARE(fixture.service.state(), QProcess::Running);
        QProcess toggleProcess;
        toggleProcess.start(qEnvironmentVariable("GO_APP"),
                            {"--toggle-window", "--state-dir", fixture.state});
        QVERIFY(toggleProcess.waitForFinished(3000));
        QCOMPARE(toggleProcess.exitCode(), 0);
        QTRY_VERIFY_WITH_TIMEOUT(window->isVisible() && window->isExposed(), 3000);
        QSignalSpy exit(engine.get(), SIGNAL(exit(int)));
        eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(), 1, 5000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        engine.reset();
        transport.reset();
        QVERIFY(fixture.start());
        QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(), 5000);
        QCOMPARE(eval("root.units").toString(), QString("C"));
        QVERIFY(eval("root.controls.reduced_motion").toBool());
        QVERIFY(!eval("root.controls.window_physics").toBool());
        QVERIFY(eval("root.watchingPrecipitation").toBool());
        QCOMPARE(eval("bridge.snapshot.notifications.state").toString(), QString("paused"));
        eval("effects.notificationsPatch({enabled:false})");
        QTRY_VERIFY_WITH_TIMEOUT(!eval("root.watchingPrecipitation").toBool(), 3000);
        QSignalSpy finalExit(engine.get(), SIGNAL(exit(int)));
        eval("root.dismissWindow()");
        QTRY_COMPARE_WITH_TIMEOUT(finalExit.size(), 1, 5000);
        QCOMPARE(finalExit.first().first().toInt(), 0);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(), QProcess::NotRunning, 5000);
        QVERIFY(!QFile::exists(fixture.socket));
    }
};
QTEST_MAIN(ServiceFrontendTest)
#include "service_frontend_test.moc"
