#include <QtTest>
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
        auto* section = root->findChild<QObject*>("weatherMaps");
        QVERIFY(section);
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = scroll->property("contentItem").value<QObject*>();
        QVERIFY(flick);
        flick->setProperty("contentY", section->property("y").toReal() + 40);
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
        QCOMPARE(fixture.saved("saved-locations.json"), saved);
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
