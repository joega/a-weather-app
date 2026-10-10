#include <QtTest>
#include <QQmlEngine>
#include <QQmlContext>
#include <QQmlComponent>
#include <QQmlExpression>
#include <QQmlApplicationEngine>
#include <QWindow>
#include <QAccessible>
#include <QQuickWindow>
#include <QQuickItem>
#include <QQuickItemGrabResult>
#include <QSGRendererInterface>
#include <QFileInfo>
#include <QDir>
#include <QFile>
#include <QTimeZone>
#include <QSet>
#include <QJsonDocument>
#include <QJsonObject>
#include <QBuffer>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTimer>
#include <QTemporaryDir>
#include <QNetworkDiskCache>
#include <QNetworkReply>
#include "maptiles.h"
#include "graphicscapabilities.h"
#include "windowactivation.h"
#include "radarimages.h"
#include <QThread>
#include <cmath>
#include <functional>

// Build exact PNG framing without asking an image decoder to parse metadata.
static QByteArray tileChunk(const QByteArray& type, const QByteArray& data) {
    QByteArray chunk;
    auto word = [&](quint32 value) {
        for (int shift = 24; shift >= 0; shift -= 8)
            chunk.append(char(value >> shift));
    };
    word(quint32(data.size()));
    chunk += type + data;
    quint32 crc = 0xffffffffU;
    for (const auto byte : type + data) {
        crc ^= static_cast<unsigned char>(byte);
        for (int bit = 0; bit < 8; ++bit)
            crc = (crc >> 1) ^ ((crc & 1U) ? 0xedb88320U : 0U);
    }
    word(crc ^ 0xffffffffU);
    return chunk;
}

class FakeTransport : public QObject {
    Q_OBJECT
    Q_PROPERTY(bool connected MEMBER connected CONSTANT)
    Q_PROPERTY(bool diagnostic MEMBER diagnostic CONSTANT)
  public:
    bool connected = true, diagnostic = false;
    QList<QVariantMap> requests;
    Q_INVOKABLE void start() {}
    Q_INVOKABLE bool send(const QVariantMap& request) {
        requests.append(request);
        return true;
    }
    Q_INVOKABLE void disconnectService() {
        emit unavailable("Disconnected");
    }
  signals:
    void ready();
    void message(const QString& json);
    void unavailable(const QString& error);
    void shutdownRequested();
};
class FakeMapTiles : public QObject {
    Q_OBJECT
  public:
    int requests = 0, closes = 0;
    QSet<QString> active;
    Q_INVOKABLE void request(int z, int x, int y, bool) {
        auto key = QString("%1/%2/%3").arg(z).arg(x).arg(y);
        if (!active.contains(key)) {
            active.insert(key);
            ++requests;
        }
    }
    Q_INVOKABLE void close() {
        ++closes;
        active.clear();
    }
  signals:
    void tileReady(const QString& key, const QString& dataURL);
    void tileFailed(const QString& key, const QString& reason);
};

// Delayed images make timestamp/texture swaps and scrub coalescing observable.
class FakeRadarImages : public QQuickImageProvider {
  public:
    std::shared_ptr<RadarImageDemand> demand;
    std::atomic<int> calls{0}, inactiveCalls{0};
    std::atomic<bool> failNext{false};
    explicit FakeRadarImages(std::shared_ptr<RadarImageDemand> state)
        : QQuickImageProvider(Image, ForceAsynchronousImageLoading), demand(std::move(state)) {}
    QImage requestImage(const QString& id, QSize* size, const QSize&) override {
        ++calls;
        if (!demand->active.load())
            ++inactiveCalls;
        const bool legend = id.startsWith("legend/");
        if (!legend)
            QThread::msleep(200);
        if (failNext.exchange(false))
            return {};
        QImage image(legend ? QSize(500, 30) : QSize(512, 512), QImage::Format_ARGB32);
        image.fill(legend ? QColor("#e9e069") : QColor("#7aae6050"));
        *size = image.size();
        return image;
    }
};

// A cacheable local basemap; no public tile service is needed for lifecycle tests.
class LocalTileServer : public QTcpServer {
  public:
    QByteArray png;
    QStringList requests;
    bool hold = false;
    QList<QPointer<QTcpSocket>> pending;
    LocalTileServer() {
        QImage image(256, 256, QImage::Format_ARGB32);
        image.fill(QColor("#7ea9a2"));
        for (int y = 0; y < 256; ++y)
            for (int x = 0; x < 256; ++x)
                if (x % 64 < 4 || y % 64 < 4)
                    image.setPixelColor(x, y, QColor("#edf5ef"));
        QBuffer buffer(&png);
        buffer.open(QIODevice::WriteOnly);
        image.save(&buffer, "PNG");
        connect(this, &QTcpServer::newConnection, this, [this] {
            while (hasPendingConnections()) {
                auto* socket = nextPendingConnection();
                connect(socket, &QTcpSocket::readyRead, socket, [this, socket] {
                    if (socket->property("received").toBool())
                        return;
                    const auto bytes =
                        socket->property("headers").toByteArray() + socket->readAll();
                    socket->setProperty("headers", bytes);
                    if (!bytes.contains("\r\n\r\n"))
                        return;
                    socket->setProperty("received", true);
                    requests.append(QString::fromLatin1(bytes.split(' ').value(1)));
                    if (hold)
                        pending.append(socket);
                    else
                        respond(socket);
                });
            }
        });
    }
    QUrl url() const {
        return QUrl(QStringLiteral("http://127.0.0.1:%1").arg(serverPort()));
    }
    void respond(QTcpSocket* socket) {
        if (!socket || socket->state() != QAbstractSocket::ConnectedState)
            return;
        socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nCache-Control: "
                      "max-age=3600\r\nConnection: close\r\nContent-Length: " +
                      QByteArray::number(png.size()) + "\r\n\r\n" + png);
        socket->disconnectFromHost();
    }
};

class FrontendTest : public QObject {
    Q_OBJECT
    QJsonObject snapshot(qint64 revision, const QString& name) {
        const auto raw = QByteArray(
            R"({"schema_version":1,"location":{"name":"Current location","timezone":"UTC"},"current":null,"hourly":[],"daily":[],"alerts":{"status":"unavailable","items":[]},"source":{"name":"Open-Meteo","attribution":"Open-Meteo","freshness":"unavailable","age_seconds":null,"refreshing":false,"error":null},"controls":{"units":"F","mode":"live","strength":"normal","fps":30,"manual":{"condition":"rain"},"reduced_motion":false,"lightning_enabled":false,"window_physics":true,"accumulation":true,"pause_fullscreen":true},"atmosphere":{"rain_intensity":0,"snow_intensity":0,"cloud_cover":0.5,"fog_density":0,"sun_elevation":0,"sun_azimuth":180,"wind_x":0,"reduced_motion":false,"lightning_enabled":false,"thunderstorm":false},"effect_status":{"state":"stopped","remaining_seconds":0,"persistent":false}})");
        auto v = QJsonDocument::fromJson(raw).object();
        v["snapshot_revision"] = revision;
        auto location = v["location"].toObject();
        location["name"] = name;
        v["location"] = location;
        return v;
    }
    QJsonObject selectedSnapshot(qint64 revision, const QString& name) {
        auto value = snapshot(revision, name);
        value["location_settings"] =
            QJsonObject{{"mode", "custom"},     {"zip_code", QJsonValue::Null},
                        {"busy", false},        {"error", QJsonValue::Null},
                        {"country_code", "US"}, {"place", QJsonValue::Null}};
        return value;
    }
    QJsonObject savedSnapshot(qint64 revision, int count = 20) {
        auto value = selectedSnapshot(revision, "City 0");
        QJsonArray rows;
        for (int i = 0; i < count; ++i) {
            QJsonValue summary = QJsonValue::Null;
            if (i < 4)
                summary = QJsonObject{{"temperature_c", 15 + i},
                                      {"condition", "rain"},
                                      {"is_day", true},
                                      {"fetched_at", "2026-10-08T12:00:00Z"},
                                      {"valid_at", "2026-10-08T12:00:00Z"},
                                      {"freshness", i == 2 ? "expired" : "stale"},
                                      {"alert_status", i == 0 ? "cached" : "unavailable"}};
            rows.append(QJsonObject{{"id", QString("place-%1").arg(100 + i)},
                                    {"name", QString("City %1").arg(i)},
                                    {"label", i == 0 ? "Home" : ""},
                                    {"mode", "place"},
                                    {"country_code", "US"},
                                    {"timezone", "America/New_York"},
                                    {"summary", summary}});
        }
        value["saved_locations"] = QJsonObject{{"schema_version", 1},
                                               {"primary", "place-100"},
                                               {"viewed", "place-100"},
                                               {"primary_forecast_available", false},
                                               {"items", rows}};
        return value;
    }
    QJsonObject metricSnapshot(qint64 revision) {
        auto v = snapshot(revision, "Metric fixture");
        auto source = v["source"].toObject();
        source["fetched_at"] = "2026-09-28T12:00:00Z";
        source["freshness"] = "stale";
        v["source"] = source;
        QJsonObject current{{"time", "2026-09-28T12:00:00Z"},
                            {"condition", "clear"},
                            {"is_day", true},
                            {"temperature_c", 15},
                            {"apparent_temperature_c", 14},
                            {"humidity", 0.65},
                            {"wind_speed_m_s", 2},
                            {"wind_gust_m_s", 4},
                            {"wind_direction_deg", 90},
                            {"visibility_m", 10000},
                            {"uv_index", 0},
                            {"pressure_msl_hpa", 1013.2},
                            {"dew_point_c", 12.5}};
        auto hour = current;
        hour["local_hour"] = "12 PM";
        hour["time"] = "2026-09-28T13:00:00Z";
        hour["uv_index"] = 3.2;
        v["current"] = current;
        v["hourly"] = QJsonArray{hour};
        return v;
    }
    QJsonObject outdoorFixture() {
        return {
            {"preferences", QJsonObject{{"schema_version", 1},
                                        {"hours", 1},
                                        {"min_temperature_c", 10},
                                        {"max_temperature_c", 27},
                                        {"max_probability", .2},
                                        {"max_hourly_precipitation_mm", .1},
                                        {"max_wind_m_s", 6},
                                        {"max_gust_m_s", 10},
                                        {"daylight_only", false}}},
            {"save_status", "defaults"},
            {"freshness", "stale"},
            {"forecast_at", "2026-09-28T12:00:00Z"},
            {"generated_at", "2026-09-28T12:30:00.123456Z"},
            {"place", "Metric fixture"},
            {"timezone", "UTC"},
            {"evaluated", 1},
            {"gaps", 0},
            {"windows", QJsonArray{QJsonObject{
                            {"start", "2026-09-28T13:00:00Z"},
                            {"end", "2026-09-28T14:00:00Z"},
                            {"range_label", "Mon Sep 28, 1:00 PM UTC – Mon Sep 28, 2:00 PM UTC"},
                            {"fits", true},
                            {"missing", QJsonArray{}},
                            {"exceeds", QJsonArray{}},
                            {"low_c", 15},
                            {"high_c", 20},
                            {"peak_probability", .1},
                            {"peak_hourly_mm", 0},
                            {"total_mm", 0},
                            {"wind_m_s", 2},
                            {"gust_m_s", 4},
                            {"daylight", "not_requested"}}}}};
    }
    QJsonObject airQuality(qint64 age = 3600) {
        return {{"freshness", "fresh"},
                {"refreshing", false},
                {"offline", false},
                {"error", QJsonValue::Null},
                {"domain", "cams_global"},
                {"source", "CAMS global model data"},
                {"attribution", "CAMS global model data via Open-Meteo (CC BY 4.0)"},
                {"valid_at", "2026-09-28T12:00:00Z"},
                {"fetched_at", "2026-09-28T12:20:00Z"},
                {"valid_label", "2026-09-28 12:00 UTC (+00:00)"},
                {"fetched_label", "2026-09-28 12:20 UTC (+00:00)"},
                {"age_seconds", age},
                {"us_aqi", 0},
                {"european_aqi", 125},
                {"pm2_5_ug_m3", 12.4}};
    }
    QJsonObject warningDetailFixture(QChar key = 'b') {
        return {
            {"location", QString(64, 'a')},
            {"key", QString(64, key)},
            {"place", "Boston, MA"},
            {"kind", "new"},
            {"timezone", "America/New_York"},
            {"sent_label", "Fri Oct 9, 8:00 AM EDT"},
            {"effective_label", "Fri Oct 9, 8:00 AM EDT"},
            {"expires_label", "Sat Oct 10, 12:00 PM EDT"},
            {"event", "Flood Warning"},
            {"issuer", "NWS Boston / Norton"},
            {"headline", "Flood warning for the Charles River"},
            {"description", "Water levels are rising after sustained rainfall. Low-lying roads may "
                            "become impassable.\nThis is a test fixture, not a live warning."},
            {"instruction", "Move to higher ground.\nDo not drive through flooded roads.\n<b>This "
                            "source text must remain plain text.</b>"},
            {"area", "Boston and surrounding communities"},
            {"severity", "Severe"},
            {"urgency", "Immediate"},
            {"certainty", "Observed"},
            {"sent", "2026-10-09T12:00:00Z"},
            {"effective", "2026-10-09T12:00:00Z"},
            {"expires", "2026-10-10T16:00:00Z"}};
    }
    QJsonObject warningSnapshot(qint64 revision, bool enabled = true) {
        auto value = selectedSnapshot(revision, "Berlin, Germany");
        auto saved = savedSnapshot(revision, 2)["saved_locations"].toObject();
        auto places = saved["items"].toArray();
        auto primary = places[0].toObject();
        primary["name"] = "Boston, MA";
        primary["label"] = "";
        places[0] = primary;
        auto viewed = places[1].toObject();
        viewed["name"] = "Berlin, Germany";
        viewed["country_code"] = "DE";
        viewed["timezone"] = "Europe/Berlin";
        viewed["summary"] = QJsonValue::Null;
        places[1] = viewed;
        saved["items"] = places;
        saved["viewed"] = viewed["id"];
        value["saved_locations"] = saved;
        auto locationSettings = value["location_settings"].toObject();
        locationSettings["country_code"] = "DE";
        value["location_settings"] = locationSettings;
        auto detail = warningDetailFixture();
        QJsonObject summary{{"key", detail["key"]},
                            {"location", detail["location"]},
                            {"place", detail["place"]},
                            {"title", detail["event"]},
                            {"kind", "new"}};
        auto recent = summary;
        recent["created_at"] = "2026-10-09T12:00:00Z";
        recent["delivery"] = "sent";
        value["warning_notifications"] = QJsonObject{
            {"settings", QJsonObject{{"enabled", enabled},
                                     {"minimum_severity", "severe"},
                                     {"quiet_enabled", true},
                                     {"quiet_start", 22},
                                     {"quiet_end", 7},
                                     {"urgent_override", false}}},
            {"state", enabled ? "watching" : "off"},
            {"reason", ""},
            {"paused_until", QJsonValue::Null},
            {"supported", true},
            {"ready", enabled},
            {"actions", false},
            {"delivery", enabled ? "sent" : "none"},
            {"fetched_at", enabled ? QJsonValue("2026-10-09T12:00:00Z") : QJsonValue::Null},
            {"complete", enabled},
            {"last", enabled ? QJsonValue(summary) : QJsonValue::Null},
            {"recent", enabled ? QJsonArray{recent} : QJsonArray{}}};
        return value;
    }
    QJsonObject radarFixture(int token = 1) {
        const double x = -74.006 / 180 * 20037508.342789244;
        const double y = std::asinh(std::tan(40.7128 * M_PI / 180)) / M_PI * 20037508.342789244;
        const double half = 40075016.68557849 / 128;
        QJsonArray frames;
        for (int i = 0; i < 3; ++i)
            frames.append(
                QJsonObject{{"time", QString("2026-10-09T17:%1:13.123456789Z").arg(10 + i * 5)},
                            {"label", QString("Fri Oct 9, 1:%1:13 PM EDT").arg(10 + i * 5)},
                            {"id", QString(64, QChar('a' + i))},
                            {"state", "ready"}});
        return {{"status", "current"},
                {"refreshing", false},
                {"error", ""},
                {"client_token", token},
                {"latest", "2026-10-09T17:20:13.123456789Z"},
                {"latest_label", "Fri Oct 9, 1:20:13 PM EDT"},
                {"frames", frames},
                {"legend", QString(64, 'd')},
                {"view", QJsonObject{{"west", x - half},
                                     {"east", x + half},
                                     {"south", y - half},
                                     {"north", y + half}}}};
    }
    void drain(FakeTransport& transport, int& acknowledged) {
        for (int i = 0; i < 20 && acknowledged < transport.requests.size(); ++i) {
            const auto request = transport.requests.at(acknowledged++);
            deliver(transport,
                    {{"version", 1}, {"request_id", request["request_id"].toInt()}, {"ok", true}});
        }
        QCOMPARE(acknowledged, transport.requests.size());
    }
    void deliver(FakeTransport& transport, const QJsonObject& v) {
        emit transport.message(QString::fromUtf8(QJsonDocument(v).toJson(QJsonDocument::Compact)));
    }
    QVariant evaluate(QQmlEngine& engine, QObject* bridge, const QString& expression) {
        QQmlExpression e(qmlContext(bridge) ? qmlContext(bridge) : engine.rootContext(), bridge,
                         expression);
        auto v = e.evaluate();
        if (e.hasError())
            qFatal("%s", qPrintable(e.error().toString()));
        return v;
    }
    QQuickItem* visualItem(QQuickItem* item, const QString& name) {
        if (item->objectName() == name)
            return item;
        for (auto* child : item->childItems())
            if (auto* found = visualItem(child, name))
                return found;
        return nullptr;
    }
    QQuickItem* shellItem(QObject* root, const QString& name) {
        // Loader-created sections belong to the visual tree, not necessarily
        // the shell's QObject ownership tree.
        if (auto* item = root->findChild<QQuickItem*>(name))
            return item;
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        return window ? visualItem(window->contentItem(), name) : nullptr;
    }
    QJsonObject mapFixture(double latitude = 40.7128, double longitude = -74.006) {
        QJsonArray cells;
        for (int row = 0; row < 5; ++row)
            for (int col = 0; col < 5; ++col)
                cells.append(QJsonObject{{"latitude", latitude + (2 - row) * 0.07},
                                         {"longitude", longitude + (col - 2) * 0.09},
                                         {"temperature_c", QJsonArray{15, 16, 17}},
                                         {"wind_speed_m_s", QJsonArray{3, 4, 5}},
                                         {"wind_from_deg", QJsonArray{0, 90, 180}},
                                         {"precipitation_mm", QJsonArray{0, 1, 2}}});
        return {{"latitude", latitude},
                {"longitude", longitude},
                {"radius_miles", 10},
                {"model_id", "ncep_nbm_conus"},
                {"model_name", "NOAA NBM CONUS"},
                {"resolution_km", 2.5},
                {"fetched_at", "2026-09-28T12:00:00Z"},
                {"hours", QJsonArray{1790596800, 1790600400, 1790604000}},
                {"cells", cells},
                {"attribution", "Model forecast via Open-Meteo (CC BY 4.0)"}};
    }
    QJsonObject mapEvent(const QJsonObject& data, bool offline) {
        return {{"version", 1},
                {"event", "map"},
                {"map", QJsonObject{{"status", offline ? "stale" : "fresh"},
                                    {"offline", offline},
                                    {"error", ""},
                                    {"fetched_at", data.value("fetched_at")},
                                    {"fetched_label", "Mon Sep 28, 8:00 AM EDT"},
                                    {"hour_labels", QJsonArray{"8 AM", "9 AM", "10 AM"}},
                                    {"data", data}}}};
    }
    void useTemporaryTileCache(MapTiles& tiles, const QTemporaryDir& directory) {
        tiles.cache = new QNetworkDiskCache(&tiles);
        tiles.cache->setCacheDirectory(directory.path());
        tiles.cache->setMaximumCacheSize(32 * 1024 * 1024);
        tiles.manager.setCache(tiles.cache);
    }
    bool tileImagesRendered(QObject* panel) {
        int count = 0;
        for (auto* area : panel->findChildren<QQuickItem*>("mapArea"))
            for (auto* item : area->childItems()) {
                const auto source = item->property("source");
                if (!source.isValid())
                    continue;
                if (!source.toUrl().toString().startsWith("data:image/png;base64,") ||
                    item->property("status").toInt() != 1)
                    return false;
                ++count;
            }
        return count >= 2;
    }
  private slots:
    void init() {
        QTest::failOnWarning(
            QRegularExpression(".*(TypeError:|ReferenceError:|Binding loop|Unable to assign|Cannot "
                               "assign|failed to load component).*"));
    }
    void graphicsPolicyCannotBypassUnsupportedPipeline() {
        for (const auto& name : {"llvmpipe (LLVM)", "softpipe", "Software Rasterizer",
                                 "SwiftShader", "GDI Generic", ""})
            QVERIFY(GraphicsCapabilities::softwareRenderer(QString::fromLatin1(name)));
        QVERIFY(!GraphicsCapabilities::softwareRenderer("AMD Radeon 740M"));
        QSurfaceFormat format;
        format.setVersion(3, 3);
        format.setProfile(QSurfaceFormat::CoreProfile);
        QVERIFY(GraphicsCapabilities::supportedFormat(format, false));
        format.setProfile(QSurfaceFormat::CompatibilityProfile);
        QVERIFY(!GraphicsCapabilities::supportedFormat(format, false));
        format.setVersion(2, 0);
        QVERIFY(!GraphicsCapabilities::supportedFormat(format, true));
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/Atmosphere.qml"));
        std::unique_ptr<QObject> sky(component.create());
        QVERIFY(sky);
        for (const auto& quality : {"auto", "full", "economical", "static"}) {
            sky->setProperty("visualQuality", quality);
            QCOMPARE(sky->property("effectiveQuality").toString(), QString("static"));
            QVERIFY(!sky->property("shaderAvailable").toBool());
            QVERIFY(!sky->property("animationActive").toBool());
        }
        sky->setProperty("pipelineFailed", true);
        sky->setProperty("visualQuality", "full");
        QVERIFY(!sky->property("shaderAvailable").toBool());
    }
    void atmosphereLifecycleAndFallback() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/Atmosphere.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* sky = qobject_cast<QQuickItem*>(owner.get());
        QVERIFY(sky);
        QQuickWindow window;
        sky->setParentItem(window.contentItem());
        sky->setSize(QSizeF(700, 650));
        window.resize(700, 650);
        window.show();
        QTRY_VERIFY(window.isExposed());
        sky->setProperty("windSpeed", 0);
        // Even calm conditions must move the scene perceptibly within seconds.
        QVERIFY(sky->property("driftRate").toDouble() >= 0.005);
        sky->setProperty("windSpeed", 150);
        const auto cappedRate = sky->property("driftRate").toDouble();
        sky->setProperty("windSpeed", 16);
        QCOMPARE(sky->property("driftRate").toDouble(), cappedRate);
        QVERIFY(cappedRate < 0.025);
        sky->setProperty("windSpeed", 8);
        for (const double wind : {0.0, 180.0, -180.0}) {
            sky->setProperty("wind", wind);
            QVERIFY(std::abs(sky->property("driftRate").toDouble()) > 0.001);
            QCOMPARE(sky->property("driftRate").toDouble() < 0, wind < 0);
        }
        sky->setProperty("wind", 0); // Both north and south have zero east/west component.
        if (sky->property("shaderAvailable").toBool()) {
            const double offset = sky->property("cloudOffset").toDouble();
            QTRY_VERIFY(sky->property("cloudOffset").toDouble() > offset);
        }
        sky->setProperty("reducedMotion", true);
        QVERIFY(!sky->property("animationActive").toBool());
        const double frozen = sky->property("cloudOffset").toDouble();
        QTest::qWait(120);
        QCOMPARE(sky->property("cloudOffset").toDouble(), frozen);
        sky->setProperty("reducedMotion", false);
        sky->setProperty("presentationActive", false);
        QVERIFY(!sky->property("animationActive").toBool());
        sky->setProperty("presentationActive", true);
        window.showMinimized();
        QTRY_VERIFY(!sky->property("animationActive").toBool());
        window.hide();
        QVERIFY(!sky->property("animationActive").toBool());
        sky->setProperty("shaderSupported", false);
        QVERIFY(!sky->property("shaderAvailable").toBool());
        window.showNormal();
        QTRY_VERIFY(window.isExposed());
        QVERIFY(!sky->property("animationActive").toBool());
        const auto image = window.grabWindow();
        QVERIFY(!image.isNull());
        QVERIFY(image.pixelColor(100, 50) != image.pixelColor(100, 600));
    }
    void atmosphereAdaptiveRefreshPreservesMotion() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/Atmosphere.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* sky = qobject_cast<QQuickItem*>(owner.get());
        QVERIFY(sky);
        QQuickWindow window;
        window.resize(700, 650);
        sky->setParentItem(window.contentItem());
        sky->setSize(QSizeF(700, 650));
        sky->setProperty("wind", 0);
        sky->setProperty("windSpeed", 6);
        sky->setProperty("rainAmount", 0);
        sky->setProperty("snowAmount", 0);
        auto* timer = sky->findChild<QObject*>("atmosphereAnimationTimer");
        QVERIFY(timer);
        for (const auto& condition : {"clear", "partly_cloudy", "cloudy", "fog"}) {
            sky->setProperty("condition", condition);
            QCOMPARE(timer->property("interval").toInt(), 50);
        }
        for (const auto& condition : {"rain", "drizzle", "snow", "sleet"}) {
            sky->setProperty("condition", condition);
            QCOMPARE(timer->property("interval").toInt(), 33);
        }
        sky->setProperty("condition", "cloudy");
        sky->setProperty("snowAmount", 0.2);
        QCOMPARE(timer->property("interval").toInt(), 33);
        sky->setProperty("snowAmount", 0);
        sky->setProperty("condition", "thunderstorm");
        // Make dry lightning explicit; QObject writes retain the weather bindings.
        evaluate(engine, sky, "rainAmount = 0; snowAmount = 0");
        sky->setProperty("lightningEnabled", true);
        QCOMPARE(timer->property("interval").toInt(), 33);
        sky->setProperty("lightningEnabled", false);
        QCOMPARE(timer->property("interval").toInt(), 50);
        sky->setProperty("condition", "partly_cloudy");
        window.show();
        QTRY_VERIFY(window.isExposed());
        for (const double rain : {0.0, 0.2, 0.0}) {
            const double phase = sky->property("cloudOffset").toDouble();
            const double clock = sky->property("visualTime").toDouble();
            sky->setProperty("rainAmount", rain);
            QCOMPARE(timer->property("interval").toInt(), rain > 0 ? 33 : 50);
            // Changing cadence never resets or repositions the scene.
            QCOMPARE(sky->property("cloudOffset").toDouble(), phase);
            QCOMPARE(sky->property("visualTime").toDouble(), clock);
            if (!sky->property("animationActive").toBool())
                continue; // The regular software run still checks cadence selection.
            QSignalSpy ticks(timer, SIGNAL(triggered()));
            QVERIFY(ticks.isValid());
            QTest::qWait(1100);
            const double elapsed = sky->property("visualTime").toDouble() - clock;
            QVERIFY(elapsed > 0.7);
            const int minTicks = rain > 0 ? 22 : 14, maxTicks = rain > 0 ? 38 : 27;
            QVERIFY(ticks.count() >= minTicks && ticks.count() <= maxTicks);
            const double distance = sky->property("cloudOffset").toDouble() - phase;
            QVERIFY(std::abs(distance - elapsed * sky->property("driftRate").toDouble()) <
                    0.000001);
        }
        sky->setProperty("reducedMotion", true);
        QVERIFY(!timer->property("running").toBool());
        sky->setProperty("rainAmount", 0.3);
        QVERIFY(!timer->property("running").toBool());
    }
    void atmosphereCloudDriftIsVisible() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/Atmosphere.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* sky = qobject_cast<QQuickItem*>(owner.get());
        QVERIFY(sky);
        QQuickWindow window;
        window.resize(700, 650);
        sky->setParentItem(window.contentItem());
        sky->setSize(QSizeF(700, 650));
        sky->setProperty("wind", 0);
        sky->setProperty("windSpeed", 0);
        sky->setProperty("rainAmount", 0);
        sky->setProperty("snowAmount", 0);
        sky->setProperty("reducedMotion", true);
        window.show();
        QTRY_VERIFY(window.isExposed());
        if (!sky->property("shaderAvailable").toBool())
            QSKIP("Cloud pixel-motion check requires a hardware ShaderEffect renderer");
        for (const double cover : {0.48, 0.95}) {
            sky->setProperty("cloudCover", cover);
            sky->setProperty("cloudOffset", 0);
            QTest::qWait(200);
            const auto before = window.grabWindow();
            // Advance only cloud phase by four seconds of the calm-wind floor.
            // Time, precipitation, light, and all other scene inputs stay fixed.
            sky->setProperty("cloudOffset", sky->property("driftRate").toDouble() * 4);
            QTest::qWait(200);
            const auto after = window.grabWindow();
            QVERIFY(!before.isNull());
            QCOMPARE(before.size(), after.size());
            double difference = 0;
            const int bottom = before.height() * 3 / 4;
            for (int y = 0; y < bottom; ++y)
                for (int x = 0; x < before.width(); ++x) {
                    const auto a = before.pixelColor(x, y), b = after.pixelColor(x, y);
                    difference += std::abs(a.red() - b.red()) + std::abs(a.green() - b.green()) +
                                  std::abs(a.blue() - b.blue());
                }
            const double mean = difference / (before.width() * bottom * 3);
            qInfo() << "Four-second cloud pixel change, cover" << cover << "mean" << mean;
            QVERIFY2(mean > 0.2,
                     "Cloud drift must visibly change the rendered scene in four seconds");
        }
    }
    void windInterpolationAndBounds() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/WeatherMapCard.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* card = owner.get();
        QVERIFY(evaluate(engine, card, "WindField.vector(10, 0).y > 9.99").toBool());
        QVERIFY(evaluate(engine, card, "WindField.vector(10, 90).x < -9.99").toBool());
        QVERIFY(evaluate(engine, card, "WindField.vector(10, 180).y < -9.99").toBool());
        QVERIFY(evaluate(engine, card, "WindField.vector(10, 270).x > 9.99").toBool());
        // A north bearing must interpolate across zero, rather than turning south.
        QVERIFY(evaluate(engine, card, R"((function() {
            const a = WindField.vector(10, 359), b = WindField.vector(10, 1);
            const samples = [{x: 0, y: 0, vx: a.x, vy: a.y}, {x: 100, y: 0, vx: b.x, vy: b.y}];
            const v = WindField.interpolate(samples, 50, 0);
            return Math.abs(v.x) < 0.001 && v.y > 9.99;
        })())")
                    .toBool());
        QVERIFY(evaluate(engine, card, R"((function() {
            const samples = [{x: 0, y: 0, vx: 10, vy: 0}, {x: 100, y: 0, vx: -10, vy: 0}];
            const v = WindField.interpolate(samples, 50, 0);
            const field = WindField.build(samples.concat(samples[0]), 100, 100);
            return WindField.speed(v) < 0.001 && field.samples.length === 2
                && field.values.length === 825 && WindField.sample(field, 0, 0).x === 10
                && WindField.sample(field, 50, 0).x < 0.001;
        })())")
                    .toBool());
        QVERIFY(evaluate(engine, card, R"((function() {
            const field = WindField.build([{x: 50, y: 50, vx: 0, vy: 150}], 100, 100);
            const p = [WindField.seed(0, 100, 100, 43, 0)];
            for (let i = 0; i < 1000; ++i) WindField.advance(p, field, 0.08, 43);
            return p.length === 1 && p[0].points.length <= 12
                && WindField.inside(p[0].x, p[0].y, 100, 100, 43)
                && WindField.velocity({x: 150, y: 0}) * 150 <= 48;
        })())")
                    .toBool());
    }
    void windTrailsStayShortAtLowerRefreshRates() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/WeatherMapCard.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        QVERIFY(evaluate(engine, owner.get(), R"((function() {
            function bounded(p) {
                let length = 0;
                for (let i = 1; i < p.points.length; ++i) {
                    const a = p.points[i - 1], b = p.points[i];
                    length += Math.hypot(b.x - a.x, b.y - a.y);
                }
                return p.points.length <= 12 && length <= 18.00001;
            }
            // Fast, curved, and calm fields all retain the same visual bound.
            for (const speed of [0, 3, 15, 150]) {
                const field = WindField.build([
                    {x: 0, y: 0, vx: speed, vy: 0},
                    {x: 400, y: 400, vx: 0, vy: speed}
                ], 400, 400);
                for (const dt of [0.04, 0.067, 0.12]) {
                    const particles = [WindField.seed(0, 400, 400, 172, 0)];
                    for (let i = 0; i < 1000; ++i) {
                        WindField.advance(particles, field, dt, 172);
                        if (!bounded(particles[0])) return false;
                    }
                }
                if (!WindField.staticTrails(64, field, 172).every(bounded)) return false;
            }
            // Preserve a partial tail segment instead of chopping whole points.
            const points = [{x: 0, y: 0}, {x: 10, y: 0}, {x: 20, y: 0}];
            WindField.trimTrail(points);
            return points[0].x === 2 && points[points.length - 1].x === 20;
        })())")
                    .toBool());
    }
    void windAnimationLifecycleAndInspection() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/WeatherMapCard.qml"));
        std::unique_ptr<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* card = qobject_cast<QQuickItem*>(owner.get());
        QQuickWindow window;
        window.resize(600, 396);
        card->setParentItem(window.contentItem());
        card->setSize(QSizeF(600, 396));
        card->setProperty("mapLayer", "wind");
        card->setProperty("mapData", mapFixture().toVariantMap());
        card->setProperty("tileActive", true);
        window.show();
        QTRY_VERIFY(window.isExposed());
        auto* timer = card->findChild<QObject*>("windAnimationTimer");
        QVERIFY(timer);
        QVERIFY(timer->property("interval").toInt() >= 60);
        QTRY_VERIFY(timer->property("running").toBool());
        QTRY_VERIFY(evaluate(engine, card, "particles[0].points.length > 1").toBool());
        QVERIFY(evaluate(engine, card, "particles.length <= 64").toBool());
        QVERIFY(card->property("pointReadout").toString().contains("from N (0°)"));
        card->setProperty("windUnits", "m/s");
        QCOMPARE(card->property("pointReadout").toString(), QString("3.0 m/s · from N (0°)"));
        auto* area = card->findChild<QQuickItem*>("mapArea");
        QVERIFY(area);
        area->forceActiveFocus();
        QTest::keyClick(&window, Qt::Key_Right);
        QVERIFY(card->property("pointSelected").toBool());
        QVERIFY(card->property("inspectionX").toReal() > 0.5);
        card->setProperty("hourIndex", 1);
        QTRY_COMPARE(card->property("pointReadout").toString(), QString("4.0 m/s · from E (90°)"));
        card->setProperty("reducedMotion", true);
        QVERIFY(!timer->property("running").toBool());
        QVERIFY(evaluate(engine, card, "particles.some(p => p.points.length > 2)").toBool());
        const auto frozen = evaluate(engine, card, "JSON.stringify(particles)").toString();
        QTest::qWait(150);
        QCOMPARE(evaluate(engine, card, "JSON.stringify(particles)").toString(), frozen);
        card->setProperty("reducedMotion", false);
        QTRY_VERIFY(timer->property("running").toBool());
        card->setProperty("presentationActive", false); // Map pixels are outside the viewport.
        QVERIFY(!timer->property("running").toBool());
        card->setProperty("presentationActive", true);
        QTRY_VERIFY(timer->property("running").toBool());
        card->setProperty("tileActive", false);
        QVERIFY(!timer->property("running").toBool());
        card->setProperty("tileActive", true);
        QTRY_VERIFY(timer->property("running").toBool());
        window.showMinimized();
        QTRY_VERIFY(!timer->property("running").toBool());
        window.showNormal();
        QTRY_VERIFY(timer->property("running").toBool());
        window.hide();
        QTRY_VERIFY(!timer->property("running").toBool());
        window.showNormal();
        QTRY_VERIFY(window.isExposed());
        auto relocated = mapFixture(42.3, -71.1);
        QJsonArray relocatedCells;
        for (const auto& value : relocated["cells"].toArray()) {
            auto cell = value.toObject();
            cell["wind_speed_m_s"] = QJsonArray{7, 7, 7};
            cell["wind_from_deg"] = QJsonArray{270, 270, 270};
            relocatedCells.append(cell);
        }
        relocated["cells"] = relocatedCells;
        card->setProperty("mapData", relocated.toVariantMap());
        QVERIFY(evaluate(engine, card, "windField === null && particles.length === 0").toBool());
        QTRY_COMPARE(card->property("pointReadout").toString(), QString("7.0 m/s · from W (270°)"));
        QTRY_VERIFY(timer->property("running").toBool());
        QVERIFY(!window.grabWindow().isNull());
        QTRY_VERIFY(!card->property("pointSelected").toBool());
        QCOMPARE(card->property("inspectionX").toReal(), 0.5);
        evaluate(engine, card, "mapData = null");
        QTRY_VERIFY(
            evaluate(engine, card, "windField === null && particles.length === 0").toBool());
        QVERIFY(!timer->property("running").toBool());
    }
    void renderAtmosphereAndWindReview() {
        const auto celestialOutput = qEnvironmentVariable("WEATHER_QT_CELESTIAL_SCREENSHOTS");
        const auto output = celestialOutput.isEmpty()
                                ? qEnvironmentVariable("WEATHER_QT_FLOW_SCREENSHOTS")
                                : celestialOutput;
        if (output.isEmpty())
            QSKIP("Set WEATHER_QT_FLOW_SCREENSHOTS for native visual review");
        QVERIFY(QDir().mkpath(output));
        // Match the production launcher's desktop GL format for shader preflight.
        QSurfaceFormat format;
        format.setVersion(3, 3);
        format.setProfile(QSurfaceFormat::CoreProfile);
        QSurfaceFormat::setDefaultFormat(format);
        FakeTransport transport;
        GraphicsCapabilities capabilities;
        MapTiles tiles;
        QTemporaryDir temporary;
        const auto tileCache = qEnvironmentVariable("WEATHER_QT_FLOW_TILE_CACHE");
        tiles.cache = new QNetworkDiskCache(&tiles);
        tiles.cache->setCacheDirectory(tileCache.isEmpty() ? temporary.path() : tileCache);
        tiles.cache->setMaximumCacheSize(32 * 1024 * 1024);
        tiles.manager.setCache(tiles.cache);
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"graphicsCapabilities", QVariant::fromValue<QObject*>(&capabilities)},
             {"mapTiles", QVariant::fromValue<QObject*>(&tiles)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        capabilities.observe(window);
        qInfo() << "Native review graphics API:" << window->rendererInterface()->graphicsApi();
        auto state = metricSnapshot(1);
        state["location_settings"] = selectedSnapshot(1, "")["location_settings"];
        state["location"] =
            QJsonObject{{"name", "Demo forecast, MA"}, {"timezone", "America/New_York"}};
        auto current = state["current"].toObject();
        current["temperature_c"] = 20;
        current["wind_speed_m_s"] = 6;
        current["wind_direction_deg"] = 0;
        QJsonArray hours, days;
        for (int i = 0; i < 24; ++i) {
            auto hour = current;
            hour["time"] = QDateTime::fromSecsSinceEpoch(1791475200 + i * 3600, QTimeZone::UTC)
                               .toString(Qt::ISODate);
            hour["local_hour"] = QString("%1 %2").arg((i + 12) % 12 + 1).arg(i < 11 ? "PM" : "AM");
            hours.append(hour);
        }
        for (int i = 0; i < 10; ++i)
            days.append(QJsonObject{
                {"date", QDate(2026, 10, 8).addDays(i).toString(Qt::ISODate)},
                {"day_label", i == 0 ? "Today" : QDate(2026, 10, 8).addDays(i).toString("ddd")},
                {"condition", i % 3 ? "clear" : "partly_cloudy"},
                {"low_c", 9 + i % 3},
                {"high_c", 20 + i % 4},
                {"sunrise", QJsonValue::Null},
                {"sunset", QJsonValue::Null},
                {"sunrise_label", "7:01 AM"},
                {"sunset_label", "6:14 PM"},
                {"precipitation_probability", 0}});
        state["hourly"] = hours;
        state["daily"] = days;
        auto atmosphere = state["atmosphere"].toObject();
        atmosphere["sun_elevation"] = 35;
        auto* sky = root->findChild<QObject*>("forecastAtmosphere");
        QVERIFY(sky);
        qint64 revision = 0;
        const auto present = [&](const QString& condition, double cover, double wind,
                                 bool reduced) {
            state["snapshot_revision"] = ++revision;
            current["condition"] = condition;
            state["current"] = current;
            atmosphere["cloud_cover"] = cover;
            atmosphere["wind_x"] = wind;
            state["atmosphere"] = atmosphere;
            auto controls = state["controls"].toObject();
            controls["reduced_motion"] = reduced;
            state["controls"] = controls;
            deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
            root->setProperty("effectsOpen", false);
        };
        const auto capture = [&](const QString& name) {
            QTest::qWait(250);
            const auto image = window->grabWindow();
            QVERIFY(!image.isNull());
            QVERIFY(image.save(output + "/" + name + ".png"));
        };
        if (!celestialOutput.isEmpty()) {
            for (const int width : {1200, 700}) {
                window->setMaximumSize(QSize(1600, 1200));
                window->setMinimumSize(QSize(width, 850));
                window->setMaximumSize(QSize(width, 850));
                window->resize(width, 850);
                window->showNormal();
                QTRY_VERIFY(window->isExposed());
                QTRY_COMPARE(window->size(), QSize(width, 850));
                QTRY_VERIFY(capabilities.shaderSupported());
                qInfo() << "Celestial review renderer:" << capabilities.renderer();
                for (const double scale : {1.0, 1.5}) {
                    state["appearance"] = QJsonObject{{"schema_version", 1},
                                                      {"text_scale", scale},
                                                      {"high_contrast", false},
                                                      {"error", QJsonValue::Null}};
                    current["temperature_c"] = scale == 1.0 ? 13.3 : -40;
                    for (const bool day : {false, true}) {
                        current["is_day"] = day;
                        // Both original trajectories cross the left-hand text.
                        atmosphere["sun_azimuth"] = day ? 90 : 270;
                        atmosphere["sun_elevation"] = day ? 45 : -25;
                        present("clear", 0, 0, true);
                        QTest::qWait(200);
                        QVERIFY(sky->property("shaderAvailable").toBool());
                        QVERIFY(!sky->property("pipelineFailed").toBool());
                        auto frame = window->contentItem()->grabToImage();
                        QVERIFY(frame);
                        QTRY_VERIFY(!frame->image().isNull());
                        QVERIFY(frame->image().save(output + QString("/%1-%2-%3.png")
                                                                 .arg(day ? "sun" : "moon")
                                                                 .arg(width)
                                                                 .arg(qRound(scale * 100))));
                    }
                }
            }
            return;
        }
        for (const int width : {1200, 700}) {
            window->setMaximumSize(QSize(1600, 1200));
            window->setMinimumSize(QSize(width, 850));
            window->setMaximumSize(QSize(width, 850));
            window->resize(width, 850);
            window->showNormal();
            QTRY_VERIFY(window->isExposed());
            QTRY_COMPARE(window->width(), width);
            QTRY_COMPARE(window->height(), 850);
            present("clear", 0.06, 0, false);
            capture(QString("clear-%1").arg(width));
            present("partly_cloudy", 0.48, 0, false);
            QTest::qWait(1200);
            capture(QString("partly-north-%1").arg(width));
            const auto clip = qEnvironmentVariable("WEATHER_QT_CLOUD_CLIP");
            if (!clip.isEmpty()) {
                const auto directory = clip + QString("/%1").arg(width);
                QVERIFY(QDir().mkpath(directory));
                const double initialOffset = sky->property("cloudOffset").toDouble();
                for (int frame = 0; frame < 24; ++frame) {
                    QTest::qWait(333);
                    QVERIFY(window->grabWindow().save(
                        directory + QString("/frame-%1.png").arg(frame, 3, 10, QChar('0'))));
                }
                QVERIFY(sky->property("cloudOffset").toDouble() > initialOffset + 0.05);
            }
            present("partly_cloudy", 0.48, -180, false);
            capture(QString("partly-east-%1").arg(width));
            present("partly_cloudy", 0.48, 180, false);
            capture(QString("partly-west-%1").arg(width));
            present("partly_cloudy", 0.48, 0, true);
            capture(QString("partly-reduced-%1").arg(width));
            present("cloudy", 0.95, 0, false);
            capture(QString("overcast-%1").arg(width));
            atmosphere["sun_elevation"] = -25;
            current["is_day"] = false;
            present("partly_cloudy", 0.48, 0, false);
            capture(QString("partly-night-%1").arg(width));
            atmosphere["sun_elevation"] = 35;
            current["is_day"] = true;
        }
        sky->setProperty("shaderSupported", false);
        capture("static-fallback-700");
        sky->setProperty("shaderSupported", true);
        present("partly_cloudy", 0.48, 0, false);
        auto data = mapFixture();
        const auto mapPath = qEnvironmentVariable("WEATHER_QT_FLOW_MAP");
        if (!mapPath.isEmpty()) {
            QFile file(mapPath);
            QVERIFY(file.open(QIODevice::ReadOnly));
            data = QJsonDocument::fromJson(file.readAll()).object();
        }
        auto event = mapEvent(data, true);
        auto mapState = event["map"].toObject();
        QJsonArray labels;
        for (const auto value : data["hours"].toArray())
            labels.append(
                QDateTime::fromSecsSinceEpoch(value.toInteger(), QTimeZone("America/New_York"))
                    .toString("ddd MMM d, h:mm AP t"));
        mapState["hour_labels"] = labels;
        mapState["fetched_label"] =
            QDateTime::fromString(data["fetched_at"].toString(), Qt::ISODate)
                .toTimeZone(QTimeZone("America/New_York"))
                .toString("ddd MMM d, h:mm AP t");
        event["map"] = mapState;
        auto* panel = qobject_cast<QQuickItem*>(shellItem(root, "weatherMaps"));
        QVERIFY(panel);
        panel->setProperty("layerIndex", 2);
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        auto* flick = scroll->property("contentItem").value<QObject*>();
        QVERIFY(panel && flick);
        for (const int width : {1200, 700}) {
            window->setMaximumSize(QSize(1600, 1200));
            window->setMinimumSize(QSize(width, 850));
            window->setMaximumSize(QSize(width, 850));
            window->resize(width, 850);
            QTRY_COMPARE(window->width(), width);
            QTRY_COMPARE(window->height(), 850);
            QTest::qWait(100);
            flick->setProperty("contentY", root->property("mapContentTop").toReal() - 12);
            QTRY_VERIFY(root->property("mapActive").toBool());
            deliver(transport, event);
            QTRY_VERIFY(shellItem(root, "mapWindModule"));
            auto* wind = shellItem(root, "mapWindModule");
            QTest::qWait(100);
            if (width == 700)
                flick->setProperty("contentY", root->property("mapContentTop").toReal() +
                                                   wind->parentItem()->y() + wind->y() - 24);
            QTRY_VERIFY(wind->property("animationActive").toBool());
            QTest::qWait(1600);
            capture(QString("wind-%1").arg(width));
            auto* area = wind->findChild<QQuickItem*>("mapArea");
            QVERIFY(area);
            const auto point =
                area->mapToScene(QPointF(area->width() / 2 + 20, area->height() / 2));
            QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, point.toPoint());
            QVERIFY(wind->property("pointSelected").toBool());
            capture(QString("wind-inspected-%1").arg(width));
            panel->setProperty("hourIndex", 1);
            QTest::qWait(1400);
            capture(QString("wind-next-hour-%1").arg(width));
            present("partly_cloudy", 0.48, 0, true);
            QVERIFY(!wind->property("animationActive").toBool());
            QVERIFY(!panel->property("canPlay").toBool());
            capture(QString("wind-reduced-%1").arg(width));
            present("partly_cloudy", 0.48, 0, false);
        }
        window->hide();
    }
    void updateNoticeAndActions() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto state = snapshot(1, "Update fixture");
        QJsonObject update{{"state", "available"},
                           {"installed", "0.51.5"},
                           {"available", "0.51.9"},
                           {"message", "A new version is ready to install"},
                           {"checked_at", 1791333900}};
        state["update"] = update;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        root->setProperty("effectsOpen", false);
        auto* notice = qobject_cast<QQuickItem*>(root->findChild<QObject*>("updateNotice"));
        QVERIFY(notice);
        QTRY_VERIFY(notice->isVisible());
        auto* install = notice->findChild<QObject*>("installUpdate");
        auto* check = notice->findChild<QObject*>("checkUpdates");
        QVERIFY(install);
        QVERIFY(check);
        QCOMPARE(notice->findChild<QObject*>("installedAppVersion")->property("text").toString(),
                 QString("A Weather App · 0.51.5"));
        QVERIFY(install->property("enabled").toBool());
        QVERIFY(QMetaObject::invokeMethod(check, "clicked"));
        QCOMPARE(transport.requests.last()["op"].toString(), QString("check_updates"));
        auto requestID = transport.requests.last()["request_id"].toInt();
        deliver(transport, {{"version", 1}, {"request_id", requestID}, {"ok", true}});
        QVERIFY(QMetaObject::invokeMethod(install, "clicked"));
        QCOMPARE(transport.requests.last()["op"].toString(), QString("install_update"));
        requestID = transport.requests.last()["request_id"].toInt();
        deliver(transport, {{"version", 1}, {"request_id", requestID}, {"ok", true}});
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        const auto prefix = qEnvironmentVariable("WEATHER_QT_UPDATE_SCREENSHOT_PREFIX");
        for (int width : {1200, 700}) {
            window->resize(width, 850);
            QTest::qWait(100);
            auto* scroll = root->findChild<QObject*>("forecastScroll");
            auto* flick = scroll->property("contentItem").value<QQuickItem*>();
            QVERIFY(flick);
            flick->setProperty("contentY", notice->y() + 18);
            QTest::qWait(100);
            QVERIFY(notice->width() > 600);
            QVERIFY(notice->height() > 100);
            if (!prefix.isEmpty())
                QVERIFY(
                    window->grabWindow().save(prefix + QString("-%1-available.png").arg(width)));
        }
        update["state"] = "downloading";
        update["message"] = "Downloading the new version…";
        state["saved_locations"] = savedSnapshot(1, 3)["saved_locations"];
        state["update"] = update;
        state["snapshot_revision"] = 2;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTRY_VERIFY(!install->property("enabled").toBool());
        QVERIFY(!check->property("enabled").toBool());
        QCOMPARE(install->property("text").toString(), QString("Updating…"));
        evaluate(engine, root, "openLocations()");
        QVERIFY(root->property("locationsOpen").toBool());
        update["state"] = "updated";
        update["installed"] = "0.51.9";
        update["available"] = "";
        update["message"] = "Updated successfully";
        state["update"] = update;
        state["snapshot_revision"] = 3;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        auto* success = qobject_cast<QQuickItem*>(root->findChild<QObject*>("updatedNotice"));
        QVERIFY(success);
        QVERIFY(!success->isVisible());
        root->setProperty("locationsOpen", false);
        QTRY_VERIFY(success->isVisible());
        QCOMPARE(success->findChild<QObject*>("updatedVersionNotice")->property("text").toString(),
                 QString("Updated to 0.51.9."));
        QVERIFY(!notice->isVisible());
        QVERIFY(success->height() <= 34);
        QVERIFY(success->width() < 300);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("acknowledge_update"));
        QCOMPARE(transport.requests.last()["installed"].toString(), QString("0.51.9"));
        requestID = transport.requests.last()["request_id"].toInt();
        deliver(transport, {{"version", 1}, {"request_id", requestID}, {"ok", true}});
        update["state"] = "current";
        state["update"] = update;
        state["snapshot_revision"] = 4;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QVERIFY(success->isVisible());
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        scroll->property("contentItem").value<QQuickItem*>()->setProperty("contentY", 0);
        for (int width : {1200, 700}) {
            window->resize(width, 850);
            QTest::qWait(100);
            auto* actions = qobject_cast<QQuickItem*>(root->findChild<QObject*>("headerActions"));
            QVERIFY(actions);
            QVERIFY(success->y() >= actions->y() + actions->height());
            if (!prefix.isEmpty())
                QVERIFY(window->grabWindow().save(prefix + QString("-%1-updated.png").arg(width)));
        }
        QTRY_VERIFY_WITH_TIMEOUT(!success->isVisible(), 6000);
        const auto requestsAfterTimeout = transport.requests.size();
        window->hide();
        window->show();
        QTest::qWait(100);
        QVERIFY(!success->isVisible());
        QCOMPARE(transport.requests.size(), requestsAfterTimeout);
        update["state"] = "updated";
        update["installed"] = "0.52.0";
        state["update"] = update;
        state["snapshot_revision"] = 5;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTRY_VERIFY(success->isVisible());
        QCOMPARE(success->findChild<QObject*>("updatedVersionNotice")->property("text").toString(),
                 QString("Updated to 0.52.0."));
        requestID = transport.requests.last()["request_id"].toInt();
        deliver(transport, {{"version", 1}, {"request_id", requestID}, {"ok", true}});
        update["state"] = "available";
        update["available"] = "0.52.1";
        state["update"] = update;
        state["snapshot_revision"] = 6;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTRY_VERIFY(notice->isVisible());
        QVERIFY(check->property("visible").toBool());
        QVERIFY(install->property("visible").toBool());
    }
    void mapTileInvalidPNGs_data() {
        QTest::addColumn<QByteArray>("payload");
        QImage oversized(4096, 4096, QImage::Format_ARGB32);
        oversized.fill(Qt::blue);
        QByteArray png;
        QBuffer buffer(&png);
        QVERIFY(buffer.open(QIODevice::WriteOnly));
        QVERIFY(oversized.save(&buffer, "PNG"));
        QVERIFY(png.size() < 128 * 1024);
        QTest::newRow("large compressed image") << png;
        QTest::newRow("truncated header") << png.left(20);
        QTest::newRow("malformed payload") << QByteArray("not a PNG");
        QImage normal(256, 256, QImage::Format_ARGB32);
        normal.fill(Qt::blue);
        QByteArray normalPNG;
        QBuffer normalBuffer(&normalPNG);
        QVERIFY(normalBuffer.open(QIODevice::WriteOnly));
        QVERIFY(normal.save(&normalBuffer, "PNG"));
        QTest::newRow("truncated pixel data") << normalPNG.left(70);
        QByteArray other;
        QBuffer otherBuffer(&other);
        QVERIFY(otherBuffer.open(QIODevice::WriteOnly));
        QVERIFY(normal.save(&otherBuffer, "BMP"));
        QTest::newRow("non PNG") << other.left(128 * 1024);
    }
    void mapTileInvalidPNGs() {
        QFETCH(QByteArray, payload);
        QTcpServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        connect(&server, &QTcpServer::newConnection, &server, [&] {
            auto* socket = server.nextPendingConnection();
            connect(socket, &QTcpSocket::readyRead, socket, [socket, &payload] {
                socket->readAll();
                socket->write("HTTP/1.1 200 OK\r\nContent-Length: " +
                              QByteArray::number(payload.size()) + "\r\n\r\n" + payload);
            });
        });
        MapTiles tiles(nullptr,
                       QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(&tiles, &MapTiles::tileReady), failed(&tiles, &MapTiles::tileFailed);
        tiles.request(0, 0, 0, false);
        QTRY_COMPARE_WITH_TIMEOUT(failed.size(), 1, 3000);
        QCOMPARE(ready.size(), 0);
        QCOMPARE(failed.first().at(1).toString(),
                 QStringLiteral("invalid PNG dimensions or payload"));
    }
    void mapTileMetadataPolicy_data() {
        QTest::addColumn<QByteArray>("payload");
        QTest::addColumn<bool>("accepted");
        QByteArray base;
        for (const auto format :
             {QImage::Format_RGB888, QImage::Format_ARGB32, QImage::Format_Indexed8}) {
            QImage image(256, 256, format);
            if (format == QImage::Format_Indexed8) {
                image.setColorTable({qRgba(0, 0, 255, 0), qRgba(255, 0, 0, 255)});
                image.fill(1);
            } else {
                image.fill(QColor(0, 0, 255, 128));
            }
            QByteArray png;
            QBuffer buffer(&png);
            QVERIFY(buffer.open(QIODevice::WriteOnly));
            QVERIFY(image.save(&buffer, "PNG"));
            QTest::newRow(qPrintable(QString::number(format))) << png << true;
            base = png;
        }
        const auto compressed = qCompress(QByteArray(4 * 1024 * 1024, 'A')).mid(4);
        QByteArray texts;
        for (int i = 0; i < 4; ++i)
            texts += tileChunk("zTXt", QByteArray("comment\0\0", 9) + compressed);
        auto insert = [&](const QByteArray& chunks) {
            return base.left(33) + chunks + base.mid(33);
        };
        QTest::newRow("multiple compressed texts") << insert(texts) << false;
        QTest::newRow("international compressed text")
            << insert(tileChunk("iTXt", QByteArray("comment\0\1\0\0\0", 13) + compressed)) << false;
        QTest::newRow("compressed ICC profile")
            << insert(tileChunk("iCCP", QByteArray("profile\0\0", 9) + compressed)) << false;
        QTest::newRow("uncompressed text") << insert(tileChunk("tEXt", "name")) << false;
        QTest::newRow("EXIF") << insert(tileChunk("eXIf", "profile")) << false;
        auto badCRC = base;
        badCRC[29] = char(badCRC[29] ^ 1);
        QTest::newRow("bad CRC") << badCRC << false;
        auto badLength = base;
        badLength[8] = char(0xff);
        QTest::newRow("overflow length") << badLength << false;
        QTest::newRow("trailing data") << (base + "garbage") << false;
    }
    void mapTileMetadataPolicy() {
        QFETCH(QByteArray, payload);
        QFETCH(bool, accepted);
        QTcpServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        connect(&server, &QTcpServer::newConnection, &server, [&] {
            auto* socket = server.nextPendingConnection();
            connect(socket, &QTcpSocket::readyRead, socket, [socket, &payload] {
                socket->readAll();
                socket->write("HTTP/1.1 200 OK\r\nContent-Length: " +
                              QByteArray::number(payload.size()) + "\r\n\r\n" + payload);
            });
        });
        MapTiles tiles(nullptr,
                       QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(&tiles, &MapTiles::tileReady), failed(&tiles, &MapTiles::tileFailed);
        // Exercise the maximum simultaneous reply budget, including bomb inputs.
        for (int x = 0; x < 16; ++x)
            tiles.request(4, x, 0, false);
        QTRY_COMPARE_WITH_TIMEOUT(ready.size() + failed.size(), 16, 5000);
        QCOMPARE(ready.size(), accepted ? 16 : 0);
        if (accepted) {
            const auto forwarded = ready.first().at(1).toString().section(',', 1).toLatin1();
            QCOMPARE(QByteArray::fromBase64(forwarded), payload);
        }
    }
    void mapTileCooldownBoundAndExpiry() {
        auto now = QDateTime::fromSecsSinceEpoch(1700000000, QTimeZone::UTC);
        MapTiles tiles(nullptr, QUrl("http://127.0.0.1:1"), [&] { return now; });
        for (int x = 0; x < MapTiles::maxCooldownEntries; x++)
            tiles.rememberFailure(QString("10/%1/0").arg(x));
        QCOMPARE(tiles.retryAfter.size(), MapTiles::maxCooldownEntries);
        QSignalSpy started(&tiles, &MapTiles::tileStarted);
        tiles.close();
        tiles.request(10, 0, 0, false);
        tiles.request(10, 512, 0, false);
        QCOMPARE(started.size(), 0);
        QCOMPARE(tiles.retryAfter.size(), MapTiles::maxCooldownEntries);
        now = now.addSecs(299);
        tiles.close();
        tiles.request(10, 0, 0, false);
        QCOMPARE(started.size(), 0);
        now = now.addSecs(1);
        tiles.close();
        QCOMPARE(tiles.retryAfter.size(), 0);
        tiles.request(10, 0, 0, false);
        QCOMPARE(started.size(), 1);
        tiles.close();
        // Sequential failures across locations remain bounded even over many expiry cycles.
        for (int cycle = 0; cycle < 3; cycle++) {
            for (int x = 0; x < MapTiles::maxCooldownEntries; x++)
                tiles.rememberFailure(QString("%1/%2").arg(cycle).arg(x));
            QCOMPARE(tiles.retryAfter.size(), MapTiles::maxCooldownEntries);
            now = now.addSecs(300);
            tiles.close();
            QCOMPARE(tiles.retryAfter.size(), 0);
        }
    }
    void mapTileCloseDiscardsDelayedReply() {
        QTcpServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        QList<QPointer<QTcpSocket>> pending;
        connect(&server, &QTcpServer::newConnection, &server, [&] {
            while (server.hasPendingConnections())
                pending.append(server.nextPendingConnection());
        });
        MapTiles tiles(nullptr,
                       QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(&tiles, &MapTiles::tileReady), failed(&tiles, &MapTiles::tileFailed),
            started(&tiles, &MapTiles::tileStarted);
        for (int x = 0; x < 3; x++)
            tiles.request(2, x, 0, false);
        QTRY_COMPARE(pending.size(), 3);
        QCOMPARE(tiles.active.size(), 3);
        tiles.close();
        QCOMPARE(tiles.active.size(), 0);
        QCOMPARE(ready.size(), 0);
        QCOMPARE(failed.size(), 0);
        for (auto socket : pending)
            QTRY_COMPARE(socket->state(), QAbstractSocket::UnconnectedState);
        tiles.request(2, 0, 0, false);
        QCOMPARE(started.size(), 4);
        tiles.close();
        QCoreApplication::processEvents();
        QCOMPARE(ready.size(), 0);
        QCOMPARE(failed.size(), 0);
        QCOMPARE(tiles.retryAfter.size(), 0);
    }
    void mapTileDestructionWithActiveReplies() {
        QTcpServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        QList<QPointer<QTcpSocket>> pending;
        connect(&server, &QTcpServer::newConnection, &server, [&] {
            while (server.hasPendingConnections())
                pending.append(server.nextPendingConnection());
        });
        auto tiles = std::make_unique<MapTiles>(
            nullptr, QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(tiles.get(), &MapTiles::tileReady);
        QSignalSpy failed(tiles.get(), &MapTiles::tileFailed);
        for (int x = 0; x < 3; ++x)
            tiles->request(2, x, 0, false);
        QTRY_COMPARE(pending.size(), 3);
        QCOMPARE(tiles->active.size(), 3);
        tiles.reset(); // Abort while all callback state is alive; no external notification.
        QCOMPARE(ready.size(), 0);
        QCOMPARE(failed.size(), 0);
        for (auto socket : pending)
            QTRY_COMPARE(socket->state(), QAbstractSocket::UnconnectedState);
    }
    void mapTileObserverCancelsDelivery_data() {
        QTest::addColumn<bool>("destroy");
        QTest::newRow("close-in-tileReply") << false;
        QTest::newRow("destroy-in-tileReply") << true;
    }
    void mapTileObserverCancelsDelivery() {
        QFETCH(bool, destroy);
        QImage image(256, 256, QImage::Format_ARGB32);
        image.fill(Qt::blue);
        QByteArray payload;
        QBuffer buffer(&payload);
        QVERIFY(buffer.open(QIODevice::WriteOnly));
        QVERIFY(image.save(&buffer, "PNG"));
        QTcpServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        connect(&server, &QTcpServer::newConnection, &server, [&] {
            auto* socket = server.nextPendingConnection();
            connect(socket, &QTcpSocket::readyRead, socket, [socket, &payload] {
                socket->readAll();
                socket->write("HTTP/1.1 200 OK\r\nContent-Length: " +
                              QByteArray::number(payload.size()) + "\r\n\r\n" + payload);
            });
        });
        auto tiles = std::make_unique<MapTiles>(
            nullptr, QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy replies(tiles.get(), &MapTiles::tileReply);
        QSignalSpy ready(tiles.get(), &MapTiles::tileReady);
        connect(tiles.get(), &MapTiles::tileReply, &server, [&] {
            if (destroy)
                tiles.reset();
            else
                tiles->close();
        });
        tiles->request(0, 0, 0, false);
        QTRY_COMPARE(replies.size(), 1);
        QCOMPARE(ready.size(), 0);
        if (tiles) {
            QCOMPARE(tiles->active.size(), 0);
            QCOMPARE(tiles->completed.size(), 0);
        }
    }
    void mapTileCloseWithBufferedCache() {
        QTemporaryDir directory;
        QVERIFY(directory.isValid());
        LocalTileServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        MapTiles tiles(nullptr, server.url());
        useTemporaryTileCache(tiles, directory);
        QSignalSpy ready(&tiles, &MapTiles::tileReady), failed(&tiles, &MapTiles::tileFailed),
            replies(&tiles, &MapTiles::tileReply);
        tiles.request(0, 0, 0, false);
        QTRY_COMPARE_WITH_TIMEOUT(ready.size(), 1, 3000);
        QCOMPARE(server.requests.size(), 1);
        ready.clear();
        replies.clear();
        QTest::failOnWarning(QRegularExpression(".*QIODevice::read.*device not open.*"));
        for (int cycle = 0; cycle < 3; ++cycle) {
            tiles.close();
            tiles.request(0, 0, 0, false);
            auto* reply = tiles.active.value("0/0/0").data();
            QVERIFY(reply);
            connect(reply, &QNetworkReply::metaDataChanged, &tiles, [&tiles, reply] {
                QVERIFY(reply->bytesAvailable() > 0);
                tiles.close();
            });
            QTRY_VERIFY(tiles.active.isEmpty());
        }
        QCoreApplication::processEvents();
        QCOMPARE(ready.size(), 0);
        QCOMPARE(failed.size(), 0);
        QCOMPARE(replies.size(), 0);
        QCOMPARE(tiles.active.size(), 0);
        QCOMPARE(tiles.retryAfter.size(), 0);
        tiles.request(0, 0, 0, true);
        QTRY_COMPARE_WITH_TIMEOUT(ready.size(), 1, 3000);
        QCOMPARE(replies.size(), 1);
        QVERIFY(replies.first().at(1).toBool());
        QCOMPARE(server.requests.size(), 1);
    }
    void mapTileDownloadLimit() {
        QImage image(256, 256, QImage::Format_ARGB32);
        image.fill(Qt::blue);
        QByteArray png;
        QBuffer buffer(&png);
        QVERIFY(buffer.open(QIODevice::WriteOnly));
        QVERIFY(image.save(&buffer, "PNG"));
        QVERIFY(png.size() < 128 * 1024);
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            connect(&server, &QTcpServer::newConnection, &server, [&] {
                auto* socket = server.nextPendingConnection();
                connect(socket, &QTcpSocket::readyRead, socket, [socket, &png] {
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nCache-Control: "
                                  "max-age=3600\r\nContent-Length: " +
                                  QByteArray::number(png.size()) + "\r\n\r\n" + png);
                });
            });
            MapTiles tiles(nullptr,
                           QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles, &MapTiles::tileReady);
            QSignalSpy failed(&tiles, &MapTiles::tileFailed);
            tiles.request(0, 0, 0, false);
            QTRY_COMPARE_WITH_TIMEOUT(ready.size(), 1, 3000);
            QCOMPARE(failed.size(), 0);
            QVERIFY(ready.first().at(1).toString().startsWith("data:image/png;base64,"));
            tiles.close();
            QSignalSpy replies(&tiles, &MapTiles::tileReply);
            tiles.request(0, 0, 0, true);
            QTRY_COMPARE_WITH_TIMEOUT(ready.size(), 2, 3000);
            QCOMPARE(failed.size(), 0);
            QCOMPARE(replies.size(), 1);
            QVERIFY(replies.first().at(1).toBool());
        }
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            connect(&server, &QTcpServer::newConnection, &server, [&] {
                auto* socket = server.nextPendingConnection();
                connect(socket, &QTcpSocket::readyRead, socket, [socket] {
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: "
                                  "1048576\r\n\r\n");
                });
            });
            MapTiles tiles(nullptr,
                           QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles, &MapTiles::tileReady);
            QSignalSpy failed(&tiles, &MapTiles::tileFailed);
            tiles.request(0, 0, 0, false);
            QTRY_COMPARE_WITH_TIMEOUT(failed.size(), 1, 3000);
            QCOMPARE(failed.first().at(1).toString(), QStringLiteral("tile exceeds 128 KiB"));
            QCOMPARE(ready.size(), 0);
        }
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            int sent = 0;
            connect(&server, &QTcpServer::newConnection, &server, [&] {
                auto* socket = server.nextPendingConnection();
                connect(socket, &QTcpSocket::readyRead, socket, [socket, &sent] {
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: "
                                  "image/png\r\nTransfer-Encoding: chunked\r\n\r\n");
                    auto* timer = new QTimer(socket);
                    connect(timer, &QTimer::timeout, socket, [socket, timer, &sent] {
                        if (socket->state() != QAbstractSocket::ConnectedState ||
                            sent >= 1024 * 1024) {
                            timer->stop();
                            return;
                        }
                        QByteArray chunk(16 * 1024, 'x');
                        socket->write(QByteArray::number(chunk.size(), 16) + "\r\n" + chunk +
                                      "\r\n");
                        sent += chunk.size();
                    });
                    timer->start(5);
                });
            });
            MapTiles tiles(nullptr,
                           QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles, &MapTiles::tileReady);
            QSignalSpy failed(&tiles, &MapTiles::tileFailed);
            tiles.request(0, 0, 0, false);
            QTRY_COMPARE_WITH_TIMEOUT(failed.size(), 1, 3000);
            QCOMPARE(failed.first().at(1).toString(), QStringLiteral("tile exceeds 128 KiB"));
            QCOMPARE(ready.size(), 0);
            QVERIFY(sent < 1024 * 1024);
        }
    }
    void mapTileReloadLifecycle_data() {
        QTest::addColumn<int>("width");
        QTest::addColumn<bool>("reopen");
        QTest::addColumn<bool>("coldOffline");
        QTest::newRow("wide-refresh") << 1200 << false << false;
        QTest::newRow("narrow-refresh") << 700 << false << false;
        QTest::newRow("wide-offline-reopen") << 1200 << true << false;
        QTest::newRow("narrow-offline-reopen") << 700 << true << false;
        QTest::newRow("wide-cold-offline") << 1200 << false << true;
        QTest::newRow("narrow-cold-offline") << 700 << false << true;
    }
    void mapTileReloadLifecycle() {
        QFETCH(int, width);
        QFETCH(bool, reopen);
        QFETCH(bool, coldOffline);
        QTemporaryDir directory;
        QVERIFY(directory.isValid());
        LocalTileServer server;
        QVERIFY(server.listen(QHostAddress::LocalHost));
        FakeTransport transport;
        MapTiles tiles(nullptr, server.url());
        useTemporaryTileCache(tiles, directory);
        QQmlApplicationEngine engine;
        engine.setInitialProperties({{"weatherTransport", QVariant::fromValue(&transport)},
                                     {"mapTiles", QVariant::fromValue(&tiles)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        window->resize(width, 850);
        deliver(transport, {{"version", 1},
                            {"event", "snapshot"},
                            {"snapshot", selectedSnapshot(1, "New York, NY")}});
        root->setProperty("effectsOpen", false);
        QTRY_VERIFY(window->isExposed());
        auto* panel = qobject_cast<QQuickItem*>(shellItem(root, "weatherMaps"));
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(panel);
        QVERIFY(scroll);
        auto* flick = scroll->property("contentItem").value<QQuickItem*>();
        QVERIFY(flick);
        QTest::qWait(100);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 20);
        QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
        QSignalSpy ready(&tiles, &MapTiles::tileReady), replies(&tiles, &MapTiles::tileReply),
            failed(&tiles, &MapTiles::tileFailed);
        deliver(transport, mapEvent(mapFixture(), coldOffline));
        const auto screenshotPrefix =
            qEnvironmentVariable("WEATHER_QT_TILE_RELOAD_SCREENSHOT_PREFIX");
        if (coldOffline) {
            QTRY_VERIFY_WITH_TIMEOUT(failed.size() >= 2, 3000);
            QTRY_VERIFY(tiles.active.isEmpty());
            QCOMPARE(server.requests.size(), 0);
            QCOMPARE(ready.size(), 0);
            QVERIFY(evaluate(engine, panel, "Object.keys(tileImages).length === 0").toBool());
            auto backgroundUnavailable = [panel] {
                for (auto* label : panel->findChildren<QQuickItem*>())
                    if (label->property("text").toString() == "Geographic background unavailable" &&
                        label->isVisible())
                        return true;
                return false;
            };
            QTRY_VERIFY(backgroundUnavailable());
            if (!screenshotPrefix.isEmpty()) {
                QTest::qWait(50);
                QVERIFY(window->grabWindow().save(screenshotPrefix +
                                                  QString("-%1-cold-offline.png").arg(width)));
            }
            window->hide();
            return;
        }
        QTRY_VERIFY_WITH_TIMEOUT(ready.size() >= 2, 3000);
        QTRY_VERIFY(tiles.active.isEmpty());
        QCOMPARE(failed.size(), 0);
        const auto networkRequests = server.requests.size();
        QVERIFY(networkRequests >= 2);
        for (int cycle = 0; cycle < 3; ++cycle) {
            if (reopen) {
                window->hide();
                QTRY_VERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
                window->show();
                QTRY_VERIFY(window->isExposed());
                // The shell deliberately returns to the top in a deferred show handler.
                QTRY_COMPARE(flick->property("contentY").toReal(), 0);
                flick->setProperty("contentY", root->property("mapContentTop").toReal() + 20);
                QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
            }
            ready.clear();
            replies.clear();
            auto data = mapFixture();
            data["fetched_at"] = QString("2026-09-28T12:0%1:00Z").arg(cycle + 1);
            deliver(transport, mapEvent(data, reopen));
            QTRY_VERIFY_WITH_TIMEOUT(ready.size() >= 2, 3000);
            QTRY_VERIFY(tiles.active.isEmpty());
            QCOMPARE(failed.size(), 0);
            QCOMPARE(server.requests.size(), networkRequests);
            for (const auto& reply : replies)
                QVERIFY(reply.at(1).toBool());
            QVERIFY(evaluate(engine, panel, "Object.keys(tileImages).length >= 2").toBool());
            QTRY_VERIFY(tileImagesRendered(panel));
            if (cycle == 2 && !screenshotPrefix.isEmpty()) {
                QTest::qWait(50);
                QVERIFY(window->grabWindow().save(
                    screenshotPrefix +
                    QString("-%1-%2.png").arg(width).arg(reopen ? "offline" : "refresh")));
            }
        }
        if (reopen) {
            // Hold replies for a new region, then cancel and rapidly reopen before they finish.
            server.hold = true;
            ready.clear();
            replies.clear();
            deliver(transport, mapEvent(mapFixture(42, -71), false));
            QTRY_VERIFY(server.pending.size() >= 2);
            QVERIFY(tiles.active.size() <= 16);
            const auto canceledKeys = QSet<QString>(tiles.active.keyBegin(), tiles.active.keyEnd());
            for (int cycle = 0; cycle < 3; ++cycle) {
                window->hide();
                QCOMPARE(tiles.active.size(), 0);
                window->show();
                window->hide();
            }
            QVERIFY(evaluate(engine, panel, "Object.keys(tileImages).length === 0").toBool());
            for (auto socket : server.pending)
                if (socket)
                    server.respond(socket);
            QCoreApplication::processEvents();
            QCOMPARE(ready.size(), 0);
            QCOMPARE(failed.size(), 0);
            QCOMPARE(tiles.retryAfter.size(), 0);
            server.hold = false;
            window->show();
            QTRY_VERIFY(window->isExposed());
            QTRY_COMPARE(flick->property("contentY").toReal(), 0);
            flick->setProperty("contentY", root->property("mapContentTop").toReal() + 20);
            QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
            deliver(transport, mapEvent(mapFixture(35, 139), false));
            QTRY_VERIFY_WITH_TIMEOUT(ready.size() >= 2, 3000);
            QTRY_VERIFY(tiles.active.isEmpty());
            QTRY_VERIFY(tileImagesRendered(panel));
            auto* card = panel->findChild<QObject*>("mapPrecipitationModule");
            QVERIFY(card);
            QSet<QString> expected;
            for (const auto& value :
                 evaluate(engine, card, "visibleTiles").value<QJSValue>().toVariant().toList())
                expected.insert(value.toMap().value("key").toString());
            QVERIFY(!expected.isEmpty());
            for (const auto& key : expected)
                QVERIFY(tiles.completed.contains(key));
            for (const auto& reply : ready)
                QVERIFY(!canceledKeys.contains(reply.at(0).toString()));
            QCOMPARE(failed.size(), 0);
        }
        window->hide();
    }
    void renderLiveMapCapture() {
        const auto capture = qEnvironmentVariable("WEATHER_QT_MAP_CAPTURE");
        const auto output = qEnvironmentVariable("WEATHER_QT_MAP_SCREENSHOT");
        if (capture.isEmpty() || output.isEmpty())
            QSKIP("Set WEATHER_QT_MAP_CAPTURE and WEATHER_QT_MAP_SCREENSHOT for private map render "
                  "validation");
        QFile file(capture);
        QVERIFY(file.open(QIODevice::ReadOnly));
        const auto data = QJsonDocument::fromJson(file.readAll()).object();
        QVERIFY(!data.isEmpty());
        const auto zone =
            QTimeZone(qEnvironmentVariable("WEATHER_QT_MAP_TIMEZONE", "America/New_York").toUtf8());
        QVERIFY(zone.isValid());
        QJsonArray labels;
        for (const auto value : data.value("hours").toArray())
            labels.append(QDateTime::fromSecsSinceEpoch(value.toInteger(), zone)
                              .toString("ddd MMM d, h:mm AP t"));
        const auto fetched = QDateTime::fromString(data.value("fetched_at").toString(), Qt::ISODate)
                                 .toTimeZone(zone)
                                 .toString("ddd MMM d, h:mm AP t");
        FakeTransport transport;
        MapTiles tiles;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.rootContext()->setContextProperty("mapTiles", &tiles);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        const auto location = qEnvironmentVariable("WEATHER_QT_MAP_LOCATION", "New York, NY");
        auto snapshotState = selectedSnapshot(1, location);
        const auto snapshotPath = qEnvironmentVariable("WEATHER_QT_MAP_SNAPSHOT");
        if (!snapshotPath.isEmpty()) {
            QFile snapshotFile(snapshotPath);
            QVERIFY(snapshotFile.open(QIODevice::ReadOnly));
            snapshotState = QJsonDocument::fromJson(snapshotFile.readAll()).object();
            QCOMPARE(snapshotState.value("location").toObject().value("name").toString(), location);
        }
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshotState}});
        root->setProperty("effectsOpen", false);
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto* panel = qobject_cast<QQuickItem*>(shellItem(root, "weatherMaps"));
        QVERIFY(panel);
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = qobject_cast<QQuickItem*>(scroll->property("contentItem").value<QObject*>());
        QVERIFY(flick);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 20);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine, root, "backend.mapWanted").toBool(), 3000);
        QSignalSpy received(&tiles, &MapTiles::tileReady);
        QSignalSpy started(&tiles, &MapTiles::tileStarted);
        QSignalSpy replies(&tiles, &MapTiles::tileReply);
        QObject::connect(&tiles, &MapTiles::tileFailed, &tiles,
                         [](const QString& key, const QString& reason) {
                             qInfo().noquote() << "Map tile failure" << key << reason;
                         });
        deliver(transport, {{"version", 1},
                            {"event", "map"},
                            {"map", QJsonObject{{"status", "fresh"},
                                                {"offline", false},
                                                {"error", ""},
                                                {"fetched_at", data.value("fetched_at")},
                                                {"fetched_label", fetched},
                                                {"hour_labels", labels},
                                                {"data", data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(started.size() > 0, 3000);
        QTRY_VERIFY_WITH_TIMEOUT(received.size() >= 2, 15000);
        QTRY_VERIFY_WITH_TIMEOUT(tileImagesRendered(panel), 15000);
        QTest::qWait(400);
        QVERIFY(window->grabWindow().save(output));
        panel->setProperty("layerIndex", 2);
        panel->setProperty("hourIndex", 1);
        QTest::qWait(150);
        QVERIFY(window->grabWindow().save(QString(output).replace(".png", "-wind.png")));
        panel->setProperty("layerIndex", 0);
        panel->setProperty("hourIndex", 1);
        auto* precipitation = qobject_cast<QQuickItem*>(shellItem(root, "mapPrecipitationModule"));
        QVERIFY(precipitation);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() +
                                           precipitation->parentItem()->y() + precipitation->y() -
                                           24);
        QTRY_VERIFY_WITH_TIMEOUT(tileImagesRendered(panel), 15000);
        QTest::qWait(250);
        QVERIFY(window->grabWindow().save(QString(output).replace(".png", "-precipitation.png")));
        int onlineNetwork = 0;
        for (const auto& reply : replies)
            if (!reply.at(1).toBool())
                ++onlineNetwork;
        qInfo() << "Map tiles on first view:" << replies.size() << "valid replies," << onlineNetwork
                << "network replies";
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        window->hide();
        QTRY_VERIFY_WITH_TIMEOUT(!evaluate(engine, root, "backend.mapWanted").toBool(), 3000);
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        window->show();
        QTRY_VERIFY(window->isExposed());
        // Wait for the shell's deferred scroll-to-top before returning to the maps.
        QTRY_COMPARE(flick->property("contentY").toReal(), 0);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 20);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine, root, "backend.mapWanted").toBool(), 3000);
        QSignalSpy offlineTiles(&tiles, &MapTiles::tileReady);
        QSignalSpy offlineReplies(&tiles, &MapTiles::tileReply);
        deliver(transport, {{"version", 1},
                            {"event", "map"},
                            {"map", QJsonObject{{"status", "stale"},
                                                {"offline", true},
                                                {"error", ""},
                                                {"fetched_at", data.value("fetched_at")},
                                                {"fetched_label", fetched},
                                                {"hour_labels", labels},
                                                {"data", data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(offlineTiles.size() >= 2, 3000);
        for (const auto& reply : offlineReplies)
            QVERIFY(reply.at(1).toBool());
        QTRY_VERIFY_WITH_TIMEOUT(tileImagesRendered(panel), 3000);
        QTest::qWait(50);
        QVERIFY(window->grabWindow().save(QString(output).replace(".png", "-offline.png")));
        window->hide();
    }
    void briefingValidationAndInteraction() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"mapTiles", QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto value = metricSnapshot(1);
        value["location_settings"] = selectedSnapshot(1, "Fixture")["location_settings"];
        value["location"] = QJsonObject{{"name", "Boston, Massachusetts, Suffolk, United States"},
                                        {"timezone", "America/New_York"}};
        auto saved = savedSnapshot(1, 1)["saved_locations"].toObject();
        auto places = saved["items"].toArray();
        auto home = places[0].toObject();
        home["label"] = "";
        home["name"] = "Boston, Massachusetts, Suffolk, United States";
        places[0] = home;
        saved["items"] = places;
        value["saved_locations"] = saved;
        QJsonObject today{{"period", "today"},
                          {"start", "2026-09-28T12:00:00Z"},
                          {"end", "2026-09-28T18:00:00Z"},
                          {"range_label", "12 PM UTC – 6 PM UTC"},
                          {"low_c", 15},
                          {"high_c", 20},
                          {"peak_probability", 0.1},
                          {"peak_label", "12 PM UTC – 1 PM UTC"},
                          {"gust_m_s", 10},
                          {"temperature_complete", true},
                          {"precipitation_complete", true},
                          {"wind_complete", true}};
        auto tomorrow = today;
        tomorrow["period"] = "tomorrow";
        tomorrow["start"] = "2026-09-29T06:00:00Z";
        tomorrow["end"] = "2026-09-29T18:00:00Z";
        tomorrow["range_label"] = "6 AM UTC – 6 PM UTC";
        tomorrow["precipitation_complete"] = false;
        value["briefing"] = QJsonArray{today, tomorrow};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        root->setProperty("effectsOpen", false);
        auto* outlook = root->findChild<QQuickItem*>("forecastOutlook");
        QVERIFY(outlook);
        QVERIFY(outlook->property("text").toString().startsWith("Today · "));
        QVERIFY(outlook->property("text").toString().contains("Low precipitation chances."));
        QVERIFY(!root->findChild<QObject*>("forecastBriefingRange"));
        QVERIFY(!visualItem(window->contentItem(), "briefingPeriod_tomorrow"));
        for (const char* name :
             {"openOutdoorPlanner", "openForecastChanges", "openDashboardEditor"}) {
            auto* tool = root->findChild<QQuickItem*>(name);
            QVERIFY(tool);
            QVERIFY(!tool->isVisible());
        }
        auto* share = root->findChild<QQuickItem*>("openForecastShare");
        auto* refresh = root->findChild<QQuickItem*>("refreshForecast");
        auto* location = root->findChild<QQuickItem*>("openLocation");
        auto* heading = root->findChild<QQuickItem*>("locationHeading");
        QVERIFY(share && refresh && location && heading);
        QCOMPARE(share->parentItem(), location->parentItem());
        QCOMPARE(share->parentItem(), refresh->parentItem());
        QCOMPARE(heading->property("text").toString(), QString("Boston, Massachusetts"));
        QCOMPARE(root->property("region").toString(), QString("United States"));
        QVERIFY(share->property("text").toString().isEmpty());
        QVERIFY(location->property("text").toString().isEmpty());
        QCOMPARE(QAccessible::queryAccessibleInterface(share)->text(QAccessible::Name),
                 QString("Share forecast"));
        const auto requests = transport.requests.size();
        share->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_VERIFY(root->property("shareOpen").toBool());
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!root->property("shareOpen").toBool());
        QTRY_VERIFY(share->hasActiveFocus());
        QCOMPARE(transport.requests.size(), requests);
        root->findChild<QQuickItem*>("forecastScroll")->forceActiveFocus();
        const auto prefix = qEnvironmentVariable("WEATHER_QT_BRIEFING_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
            for (const int width : {700, 1200}) {
                window->resize(width, 850);
                QTest::qWait(120);
                const auto heroCenter = heading->mapToScene(QPointF(heading->width() / 2, 0));
                QVERIFY(qAbs(heroCenter.x() - window->width() / 2.0) < 1);
                QVERIFY(heading->mapToScene(QPointF()).y() >=
                        share->mapToScene(QPointF(0, share->height())).y());
                QVERIFY(location->mapToScene(QPointF()).x() < refresh->mapToScene(QPointF()).x());
                QCOMPARE(refresh->mapToScene(QPointF()).y(), share->mapToScene(QPointF()).y());
                QVERIFY(window->grabWindow().save(prefix + QString::number(width) + ".png"));
            }
        }
        // Follow the current available period automatically, with no header tabs.
        value["snapshot_revision"] = 2;
        value["briefing"] = QJsonArray{tomorrow};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QVERIFY(outlook->property("text").toString().startsWith("Tomorrow · "));
        QVERIFY(outlook->property("text").toString().contains("Partial hourly forecast."));
        auto source = value["source"].toObject();
        source["freshness"] = "expired";
        value["source"] = source;
        value["snapshot_revision"] = 3;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QVERIFY(!outlook->isVisible());

        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY(scope);
        auto valid = [&](const QJsonArray& rows) {
            QQmlExpression expression(
                qmlContext(scope.data()), scope.data(),
                "Forecast.briefings(" +
                    QString::fromUtf8(QJsonDocument(rows).toJson(QJsonDocument::Compact)) + ")");
            expression.evaluate();
            return !expression.hasError();
        };
        QVERIFY(valid(QJsonArray{today, tomorrow}));
        QVERIFY(!valid(QJsonArray{today, today}));
        QVERIFY(!valid(QJsonArray{tomorrow, today}));
        for (const auto& mutation :
             QList<QPair<QString, QJsonValue>>{{"peak_probability", 1.1},
                                               {"low_c", 40},
                                               {"end", "bad"},
                                               {"temperature_complete", "true"},
                                               {"gust_m_s", QJsonValue::Null}}) {
            auto invalid = today;
            invalid[mutation.first] = mutation.second;
            QVERIFY2(!valid(QJsonArray{invalid}), qPrintable(mutation.first));
        }
        // Older service snapshots remain accepted.
        QCOMPARE(evaluate(engine, scope.data(), "Forecast.briefings(undefined).length").toInt(), 0);
    }
    void liveTimestampPrecision() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY(scope);
        const auto valid = [&](const QString& value) {
            QQmlExpression expression(qmlContext(scope.data()), scope.data(),
                                      "Forecast.time('" + value + "')");
            expression.evaluate();
            return !expression.hasError();
        };
        // Live Go/NWS retrieval times use RFC3339Nano; whole-second fixtures
        // must not conceal a startup rejection of real cached observations.
        for (const auto& value :
             {"2026-10-10T00:10:22Z", "2026-10-10T00:10:22.1Z", "2026-10-10T00:10:22.144973Z",
              "2026-10-10T00:10:22.144973867Z", "2026-10-09T20:10:22.144973867-04:00"})
            QVERIFY2(valid(value), value);
        for (const auto& value : {"2026-10-10T00:10:22.1449738671Z", "2026-10-10T00:10:22.Z",
                                  "2026-10-10T00:10:22.123", "2026-13-10T00:10:22.123456789Z"})
            QVERIFY2(!valid(value), value);
    }
    void appearanceContract() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY(scope);
        QCOMPARE(
            evaluate(engine, scope.data(), "Forecast.appearance(undefined).text_scale").toDouble(),
            1.0);
        QVERIFY(!evaluate(engine, scope.data(), "Forecast.appearance(undefined).high_contrast")
                     .toBool());
        auto valid = [&](const QString& document) {
            QQmlExpression expression(qmlContext(scope.data()), scope.data(),
                                      "Forecast.appearance(" + document + ")");
            expression.evaluate();
            return !expression.hasError();
        };
        for (const auto& scale : {"1", "1.25", "1.5"})
            QVERIFY(valid(QString("{schema_version:1,text_scale:%1,high_contrast:true,error:null}")
                              .arg(scale)));
        for (const auto& invalid :
             {"null", "{}", "{schema_version:1,text_scale:2,high_contrast:false,error:null}",
              "{schema_version:1,text_scale:1,high_contrast:'true',error:null}",
              "{schema_version:1,text_scale:1,high_contrast:false,error:'unknown'}",
              "{schema_version:1,text_scale:1,high_contrast:false,error:null,extra:1}"})
            QVERIFY2(!valid(invalid), invalid);
    }
    void metricDetailsOnDemandAndKeyboard() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"mapTiles", QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto value = metricSnapshot(1);
        auto first = value["hourly"].toArray()[0].toObject();
        first["local_label"] = "Mon 1 PM UTC";
        first["dew_point_c"] = -10;
        auto gap = first;
        gap["time"] = "2026-09-28T14:00:00Z";
        gap["local_hour"] = "2 PM";
        gap["local_label"] = "Mon 2 PM UTC";
        gap["pressure_msl_hpa"] = QJsonValue::Null;
        auto last = first;
        last["time"] = "2026-09-28T15:00:00Z";
        last["local_hour"] = "3 PM";
        last["local_label"] = "Mon 3 PM UTC";
        last["pressure_msl_hpa"] = 1005;
        last["uv_index"] = 5;
        value["hourly"] = QJsonArray{first, gap, last};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        root->setProperty("effectsOpen", false);
        QCoreApplication::processEvents();
        auto* details = root->findChild<QObject*>("forecastDetails");
        auto* loader = root->findChild<QObject*>("forecastChartLoader");
        QVERIFY(details && loader);
        QVERIFY(!qvariant_cast<QObject*>(loader->property("item")));
        const auto requests = transport.requests.size();
        auto* card = visualItem(window->contentItem(), "currentMetric_uv");
        QVERIFY(card);
        card->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_VERIFY(details->property("visible").toBool());
        QCOMPARE(details->property("metric").toString(), QString("uv_index"));
        auto* selected = details->findChild<QObject*>("selectedForecastValue");
        auto* range = details->findChild<QObject*>("forecastMetricRange");
        QVERIFY(selected && range);
        QCOMPARE(selected->property("text").toString(), QString("UV index: 3.2"));
        auto* picker = details->findChild<QQuickItem*>("forecastMetric");
        QVERIFY(picker);
        picker->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTest::keyClick(window, Qt::Key_End);
        QTest::keyClick(window, Qt::Key_Return);
        QCOMPARE(details->property("metric").toString(), QString("pressure_msl_hpa"));
        QVERIFY(range->property("text").toString().contains("Some hours are unavailable"));
        auto* chart = qvariant_cast<QQuickItem*>(loader->property("item"));
        QVERIFY(chart);
        auto* accessibleChart = QAccessible::queryAccessibleInterface(chart);
        QVERIFY(accessibleChart);
        QCOMPARE(accessibleChart->role(), QAccessible::Chart);
        QVERIFY(accessibleChart->state().focusable);
        QCOMPARE(accessibleChart->text(QAccessible::Name), QString("Pressure forecast chart"));
        QVERIFY(accessibleChart->text(QAccessible::Description).contains("Mon 1 PM UTC"));
        QVERIFY(accessibleChart->text(QAccessible::Description).contains("29.92 inHg"));
        chart->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Right);
        QCOMPARE(details->property("selectedIndex").toInt(), 1);
        QCOMPARE(selected->property("text").toString(), QString("Pressure: —"));
        QVERIFY(accessibleChart->text(QAccessible::Description)
                    .startsWith("Mon 2 PM UTC. Pressure: unavailable."));
        QTest::keyClick(window, Qt::Key_End);
        QCOMPARE(details->property("selectedIndex").toInt(), 2);
        QCOMPARE(selected->property("text").toString(), QString("Pressure: 29.68 inHg"));
        auto* actions = accessibleChart->actionInterface();
        QVERIFY(actions);
        QVERIFY(actions->actionNames().contains(QAccessibleActionInterface::decreaseAction()));
        QVERIFY(actions->actionNames().contains(QAccessibleActionInterface::increaseAction()));
        actions->doAction(QAccessibleActionInterface::decreaseAction());
        QCOMPARE(details->property("selectedIndex").toInt(), 1);
        actions->doAction(QAccessibleActionInterface::increaseAction());
        QCOMPARE(details->property("selectedIndex").toInt(), 2);
        actions->doAction(QAccessibleActionInterface::increaseAction());
        QCOMPARE(details->property("selectedIndex").toInt(), 2);
        const auto prefix = qEnvironmentVariable("WEATHER_QT_DETAILS_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
            for (const int width : {700, 1200}) {
                window->resize(width, 850);
                QTest::qWait(120);
                QVERIFY(window->grabWindow().save(prefix + QString::number(width) + ".png"));
            }
        }
        QPointer<QQuickItem> oldChart(chart);
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!details->property("visible").toBool());
        QTRY_VERIFY(oldChart.isNull());
        QVERIFY(!qvariant_cast<QObject*>(loader->property("item")));
        QCOMPARE(transport.requests.size(), requests);
        // Older running services omit saved locations and cannot calculate
        // astronomy. Show that reason instead of a blank date and inert Today.
        evaluate(engine, root, "openAstronomy()");
        QTRY_VERIFY(root->property("astronomyOpen").toBool());
        QCOMPARE(evaluate(engine, root, "backend.astronomyError").toString(),
                 QString("service_unsupported"));
        QVERIFY(shellItem(root, "astronomyStatus")
                    ->property("text")
                    .toString()
                    .contains("does not support Sun & moon"));
        QCOMPARE(transport.requests.size(), requests);
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!root->property("astronomyOpen").toBool());
        // Negative dew points and a narrow pressure range must not use a zero baseline.
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY(scope);
        QVERIFY(evaluate(engine, scope.data(),
                         "Forecast.metricScale([{dew_point_c:-10},{dew_point_c:null},{dew_point_c:-"
                         "5}], 'dew_point_c').low < -10")
                    .toBool());
        QVERIFY(evaluate(engine, scope.data(),
                         "Forecast.metricScale([{pressure_msl_hpa:1005},{pressure_msl_hpa:1013}], "
                         "'pressure_msl_hpa').low > 1000")
                    .toBool());
        QVERIFY(evaluate(engine, scope.data(),
                         "Forecast.metricScale([{humidity:null}], 'humidity') === null")
                    .toBool());
        QCOMPARE(evaluate(engine, scope.data(), "Forecast.metricValue(0.65,'humidity','C','auto')")
                     .toString(),
                 QString("65%"));
        QCOMPARE(
            evaluate(engine, scope.data(), "Forecast.metricValue(-10,'dew_point_c','F','auto')")
                .toString(),
            QString("14°"));
    }
    void toggleReadableLayoutAndAccessibility() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(R"(
            import QtQuick
            import "qrc:/ui/qml"
            Window {
                width: 300; height: 230; visible: true; color: "#2c455a"
                Column {
                    x: 16; y: 16; width: parent.width - 32; spacing: 12
                    ToggleControl {
                        objectName: "wrappingToggle"; width: parent.width
                        title: "Official warning notifications"
                        caption: "Continues checking while the window is hidden."
                        testName: "wrappingSwitch"
                        onToggled: value => checked = value
                    }
                    PlainLabel { objectName: "followingLabel"; text: "Next setting" }
                    ActionButton { text: "Apply"; primary: true }
                }
            })",
                          QUrl());
        QScopedPointer<QObject> owner(component.create());
        QVERIFY2(owner, qPrintable(component.errorString()));
        auto* window = qobject_cast<QQuickWindow*>(owner.data());
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto* control = owner->findChild<QQuickItem*>("wrappingToggle");
        auto* toggle = owner->findChild<QQuickItem*>("wrappingSwitch");
        QVERIFY(control && toggle);
        auto* accessible = QAccessible::queryAccessibleInterface(toggle);
        QVERIFY(accessible);
        QCOMPARE(accessible->text(QAccessible::Name), control->property("title").toString());
        QTest::qWait(60);
        const auto prefix = qEnvironmentVariable("WEATHER_QT_ACCESSIBILITY_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty())
            QVERIFY(window->grabWindow().save(prefix + "-toggle.png"));
        for (auto* label : control->findChildren<QQuickItem*>()) {
            const auto text = label->property("text").toString();
            if (text != control->property("title").toString() &&
                text != control->property("caption").toString())
                continue;
            QVERIFY2(label->mapToItem(control, QPointF(label->width(), 0)).x() < toggle->x(),
                     qPrintable(text));
            QVERIFY(label->mapToItem(control, QPointF(0, label->height())).y() <=
                    control->height() - 8);
            QVERIFY(!label->property("truncated").toBool());
        }
        QCOMPARE(accessible->text(QAccessible::Description),
                 control->property("caption").toString());
        auto* actions = accessible->actionInterface();
        QVERIFY(actions);
        actions->doAction(QAccessibleActionInterface::toggleAction());
        QVERIFY(control->property("checked").toBool());
        QVERIFY(accessible->state().checked);
        toggle->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QVERIFY(!control->property("checked").toBool());
        control->setProperty("locked", true);
        QVERIFY(accessible->state().disabled);
    }
    void savedLocationValidation() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY2(scope, qPrintable(component.errorString()));
        auto valid = savedSnapshot(1)["saved_locations"].toObject();
        auto accepts = [&](const QJsonObject& value) {
            const auto json =
                QString::fromUtf8(QJsonDocument(value).toJson(QJsonDocument::Compact));
            return evaluate(engine, scope.data(),
                            "(function(){try { Forecast.savedLocations(" + json +
                                "); return true; } catch(e) { return false; }})()")
                .toBool();
        };
        QVERIFY(accepts(valid));
        auto changed = valid;
        changed["unexpected"] = true;
        QVERIFY(!accepts(changed));
        changed = valid;
        changed["primary"] = "place-999";
        QVERIFY(!accepts(changed));
        changed = valid;
        auto rows = valid["items"].toArray();
        rows.append(rows.first());
        changed["items"] = rows;
        QVERIFY(!accepts(changed));
        for (const auto& field : QStringList{"label", "mode", "country_code", "summary"}) {
            changed = valid;
            rows = valid["items"].toArray();
            auto row = rows.first().toObject();
            if (field == "label")
                row[field] = "Home\u202e";
            else if (field == "mode")
                row[field] = "zip";
            else if (field == "country_code")
                row[field] = "DE"; // A cached NWS warning cannot claim foreign coverage.
            else {
                auto summary = row[field].toObject();
                summary["temperature_c"] = 101;
                row[field] = summary;
            }
            rows.replace(0, row);
            changed["items"] = rows;
            QVERIFY2(!accepts(changed), qPrintable(field));
        }
        QCOMPARE(evaluate(engine, scope.data(), "Forecast.savedLocations(undefined)").isNull(),
                 true);
    }
    void savedLocationPickerNavigationAndActions_data() {
        QTest::addColumn<double>("textScale");
        QTest::newRow("normal") << 1.0;
        QTest::newRow("enlarged") << 1.5;
    }
    void savedLocationPickerNavigationAndActions() {
        QFETCH(double, textScale);
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"mapTiles", QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window = root->findChild<QQuickWindow*>("weatherWindow");
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        const QJsonObject appearance{{"schema_version", 1},
                                     {"text_scale", textScale},
                                     {"high_contrast", false},
                                     {"error", QJsonValue::Null}};
        auto state = savedSnapshot(1);
        state["appearance"] = appearance;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        root->setProperty("effectsOpen", false);
        window->resize(700, 650);
        auto* location = root->findChild<QQuickItem*>("openLocation");
        auto* loader = root->findChild<QQuickItem*>("locationPickerLoader");
        QVERIFY(location && loader);
        QVERIFY(loader->property("item").isNull());
        location->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(root->property("locationsOpen").toBool());
        QTRY_VERIFY(!loader->property("item").isNull());
        auto* picker = qobject_cast<QQuickItem*>(loader->property("item").value<QObject*>());
        QVERIFY(picker);
        auto* list = picker->findChild<QQuickItem*>("savedLocationList");
        QVERIFY(list);
        QTRY_VERIFY(list->hasActiveFocus());
        QCOMPARE(list->property("count").toInt(), 20);
        QVERIFY(!picker->findChild<QQuickItem*>("showSavedLocations")->isVisible());
        QVERIFY(!visualItem(picker, "viewSaved_19")); // Offscreen rows are not instantiated.
        const auto prefix = qEnvironmentVariable("WEATHER_QT_SAVED_SCREENSHOT_PREFIX");
        auto capture = [&](const QString& suffix) {
            if (prefix.isEmpty())
                return true;
            if (!QFileInfo(prefix).absoluteDir().mkpath("."))
                return false;
            QTest::qWait(120);
            const auto scaleSuffix = textScale == 1.5 ? QString("-scale150") : QString();
            return window->grabWindow().save(prefix + suffix + scaleSuffix + ".png");
        };
        QVERIFY(capture("list700"));
        QTest::keyClick(window, Qt::Key_Right);
        auto* alias = picker->findChild<QQuickItem*>("savedLocationAlias");
        auto* remove = picker->findChild<QQuickItem*>("removeSavedLocation");
        auto* replacement = picker->findChild<QQuickItem*>("replacementPrimary");
        QVERIFY(alias && remove && replacement);
        QTRY_VERIFY(alias->hasActiveFocus());
        QCOMPARE(alias->property("text").toString(), QString("Home"));
        QVERIFY(!remove->isEnabled());
        QCOMPARE(replacement->property("currentIndex").toInt(), -1);
        alias->setProperty("text", "Apartment");
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(transport.requests.size(), 1);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("saved_location"));
        QCOMPARE(transport.requests.last()["location"].toMap(),
                 (QVariantMap{{"action", "rename"}, {"id", "place-100"}, {"label", "Apartment"}}));
        auto ack = [&] {
            deliver(transport, {{"version", 1},
                                {"request_id", transport.requests.last()["request_id"].toInt()},
                                {"ok", true}});
            QCoreApplication::processEvents();
        };
        ack();
        replacement->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        QTRY_VERIFY(remove->isEnabled());
        QCOMPARE(picker->property("replacementId").toString(), QString("place-101"));
        QVERIFY(capture("edit700"));
        remove->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_COMPARE(transport.requests.size(), 2);
        QCOMPARE(
            transport.requests.last()["location"].toMap(),
            (QVariantMap{{"action", "remove"}, {"id", "place-100"}, {"replacement", "place-101"}}));
        ack();
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(loader->property("item").isNull());
        QTRY_VERIFY(location->hasActiveFocus());
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(!loader->property("item").isNull());
        picker = qobject_cast<QQuickItem*>(loader->property("item").value<QObject*>());
        list = picker->findChild<QQuickItem*>("savedLocationList");
        QTRY_VERIFY(list->hasActiveFocus());
        QTest::keyClick(window, Qt::Key_Down);
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(transport.requests.size(), 3);
        QCOMPARE(transport.requests.last()["location"].toMap(),
                 (QVariantMap{{"action", "view"}, {"id", "place-101"}}));
        QTRY_VERIFY(loader->property("item").isNull());
        ack();
        // Opening and closing the picker without searching does not request data.
        for (int i = 0; i < 5; ++i) {
            QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
            QTRY_VERIFY(!loader->property("item").isNull());
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(loader->property("item").isNull());
        }
        QTest::qWait(400);
        QCOMPARE(transport.requests.size(), 3);
        state = savedSnapshot(2, 3);
        state["appearance"] = appearance;
        // A selected built-in default must not hide existing saved cities
        // behind the Add page (the original review regression).
        auto locationSettings = state["location_settings"].toObject();
        locationSettings["mode"] = "default";
        state["location_settings"] = locationSettings;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(!loader->property("item").isNull());
        picker = qobject_cast<QQuickItem*>(loader->property("item").value<QObject*>());
        QCOMPARE(picker->property("page").toString(), QString("saved"));
        auto* add = picker->findChild<QQuickItem*>("addSavedLocation");
        add->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        auto* query = picker->findChild<QQuickItem*>("savedplaceQuery");
        QVERIFY(query);
        QTRY_VERIFY(query->hasActiveFocus());
        QVERIFY(capture("add700"));
        auto* zip = picker->findChild<QQuickItem*>("savedLocationZip");
        zip->setProperty("text", "10001");
        zip->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Return);
        QTRY_COMPARE(transport.requests.size(), 4);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("add_location"));
        QCOMPARE(transport.requests.last()["location"].toMap(),
                 (QVariantMap{{"mode", "zip"}, {"zip_code", "10001"}}));
        QTRY_VERIFY(loader->property("item").isNull());
    }
    void mainLocationNavigation() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"mapTiles", QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        deliver(transport, {{"version", 1},
                            {"event", "snapshot"},
                            {"snapshot", selectedSnapshot(1, "New York, NY")}});
        root->setProperty("effectsOpen", false);
        QCoreApplication::processEvents();
        auto* location = root->findChild<QQuickItem*>("openLocation");
        auto* query = root->findChild<QQuickItem*>("placeQuery");
        QVERIFY(location && query);
        window->resize(700, 650);
        location->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_VERIFY(query->hasActiveFocus());
        const auto point = query->mapToScene(QPointF(0, 0));
        QVERIFY(point.y() >= 0 && point.y() + query->height() <= window->height());
        for (const char key : QByteArray("Berlin"))
            QTest::keyClick(window, key);
        // Close before debounce: opening and dismissing search never fetches.
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!root->property("effectsOpen").toBool());
        QTRY_VERIFY(location->hasActiveFocus());
        QTest::qWait(400);
        QCOMPARE(transport.requests.size(), 0);
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTRY_VERIFY(query->hasActiveFocus());
        // Repeated shortcut invocation must preserve the original return target.
        QTest::keyClick(window, Qt::Key_L, Qt::ControlModifier);
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(location->hasActiveFocus());
        const auto prefix = qEnvironmentVariable("WEATHER_QT_LOCATION_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
            QTest::qWait(120);
            QVERIFY(window->grabWindow().save(prefix + "700.png"));
        }
    }
    void measurementConversions() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(component.create());
        QVERIFY2(scope, qPrintable(component.errorString()));
        for (const auto& test : QList<QPair<QString, QString>>{
                 {"Forecast.wind(10,'F')", "22 mph"},
                 {"Forecast.wind(10,'C')", "36 km/h"},
                 {"Forecast.wind(10,'C','mph')", "22 mph"},
                 {"Forecast.wind(10,'F','km/h')", "36 km/h"},
                 {"Forecast.wind(10,'C','m/s')", "10.0 m/s"},
                 {"Forecast.wind(10,'C','kn')", "19 kn"},
                 {"Forecast.wind(0,'C')", "0 km/h"},
                 {"Forecast.wind(null,'F')", "—"},
                 {"Forecast.pressure(1013.25,'F')", "29.92 inHg"},
                 {"Forecast.pressure(1013.25,'C')", "1013 hPa"},
                 {"Forecast.pressure(null,'F')", "—"},
                 {"Forecast.distance(16093.44,'C')", "16.1 km"},
                 {"Forecast.distance(16093.44,'F')", "10.0 mi"},
                 {"Forecast.distance(0,'C')", "0.0 km"},
                 {"Forecast.distance(null,'C')", "—"},
                 {"Forecast.amount(25.4,'F')", "1.00 in"},
                 {"Forecast.amount(25.4,'C')", "25.4 mm"},
                 {"Forecast.outlook([{precipitation_probability:0,wind_gust_m_s:10}],'C','kn')",
                  "Low precipitation chances in the next 1 hourly forecasts. Gusts up to 19 "
                  "kn."}}) {
            QQmlExpression expression(qmlContext(scope.data()), scope.data(), test.first);
            const auto result = expression.evaluate();
            QVERIFY2(!expression.hasError(), qPrintable(expression.error().toString()));
            QCOMPARE(result.toString(), test.second);
        }
    }
    void mapOnDemandAndTimeline() {
        FakeTransport transport;
        FakeMapTiles tiles;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.rootContext()->setContextProperty("mapTiles", &tiles);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        root->setProperty("effectsOpen", true);
        deliver(transport, {{"version", 1},
                            {"event", "snapshot"},
                            {"snapshot", selectedSnapshot(1, "New York, NY")}});
        auto* panel = shellItem(root, "weatherMaps");
        QVERIFY(panel);
        auto* playback = panel->findChild<QObject*>("mapPlayback");
        QVERIFY(playback);
        QVERIFY(!playback->property("enabled").toBool());
        QVERIFY(!shellItem(root, "mapTemperatureModule"));
        QVERIFY(!shellItem(root, "mapWindModule"));
        QVERIFY(!shellItem(root, "mapPrecipitationModule"));
        QCOMPARE(panel->property("selectedLayer").toString(), QString("precipitation"));
        QCOMPARE(tiles.requests, 0);
        QCOMPARE(transport.requests.size(), 0);
        root->setProperty("effectsOpen", false);
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = scroll->property("contentItem").value<QObject*>();
        QVERIFY(flick);
        QTest::qWait(
            100); // Let the snapshot and forecast sections settle before scrolling to the maps.
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 40);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine, root, "backend.mapWanted").toBool(), 3000);
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_open"));
        QTRY_VERIFY(shellItem(root, "mapPrecipitationModule"));
        QPointer<QObject> card = shellItem(root, "mapPrecipitationModule");
        QCOMPARE(card->findChild<QObject*>("mapModuleTitle")->property("text").toString(),
                 QString("Precipitation"));
        QCOMPARE(panel->findChildren<QObject*>("mapModuleTitle").size(), 1);

        QJsonArray hours{1790596800, 1790600400, 1790604000}, cells;
        for (int row = 0; row < 5; row++)
            for (int col = 0; col < 5; col++) {
                cells.append(QJsonObject{{"latitude", 40.7128 + (2 - row) * 0.07},
                                         {"longitude", -74.006 + (col - 2) * 0.09},
                                         {"temperature_c", QJsonArray{15, 16, 17}},
                                         {"wind_speed_m_s", QJsonArray{3, 4, 5}},
                                         {"wind_from_deg", QJsonArray{0, 90, 180}},
                                         {"precipitation_mm", QJsonArray{0, 1, 2}}});
            }
        QJsonObject data{{"latitude", 40.7128},
                         {"longitude", -74.006},
                         {"radius_miles", 10},
                         {"model_id", "ncep_nbm_conus"},
                         {"model_name", "NOAA NBM CONUS"},
                         {"resolution_km", 2.5},
                         {"fetched_at", "2026-09-28T12:00:00Z"},
                         {"hours", hours},
                         {"cells", cells},
                         {"attribution", "Model forecast via Open-Meteo (CC BY 4.0)"}};
        deliver(transport,
                {{"version", 1},
                 {"event", "map"},
                 {"map", QJsonObject{{"status", "fresh"},
                                     {"offline", false},
                                     {"error", ""},
                                     {"fetched_at", "2026-09-28T12:00:00Z"},
                                     {"fetched_label", "Mon Sep 28, 8:00 AM EDT"},
                                     {"hour_labels", QJsonArray{"Mon Sep 28, 8:00 AM EDT",
                                                                "Mon Sep 28, 9:00 AM EDT",
                                                                "Mon Sep 28, 10:00 AM EDT"}},
                                     {"data", data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(tiles.requests > 0, 3000);
        QVERIFY(tiles.requests <= 16);
        QCOMPARE(panel->property("hourIndex").toInt(), 0);
        for (int i = 0; i < 100; i++)
            panel->setProperty("hourIndex", i % 3);
        panel->setProperty("hourIndex", 2);
        QCOMPARE(panel->property("hourIndex").toInt(), 2);
        QCOMPARE(card->property("hourIndex").toInt(), 2);
        QCOMPARE(tiles.requests, tiles.active.size());
        QCOMPARE(transport.requests.size(), 1);
        // Exercise timeline mechanics independently of the shell's raster
        // Auto policy; that policy is covered by the graphics fallback test.
        panel->setProperty("visualQuality", "full");
        QVERIFY(playback->property("enabled").toBool());
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(panel->property("playing").toBool());
        QCOMPARE(playback->property("text").toString(), QString("Stop"));
        QTest::qWait(100);
        QCOMPARE(panel->property("hourIndex").toInt(), 2);
        QTRY_COMPARE_WITH_TIMEOUT(panel->property("hourIndex").toInt(), 0,
                                  1500); // Wrap the available horizon.
        QTRY_COMPARE_WITH_TIMEOUT(panel->property("hourIndex").toInt(), 1, 1500);
        QCOMPARE(card->property("hourIndex").toInt(), 1);
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(!panel->property("playing").toBool());
        QCOMPARE(playback->property("text").toString(), QString("Play"));
        QCOMPARE(panel->property("hourIndex").toInt(), 0);
        QTest::qWait(1100);
        QCOMPARE(panel->property("hourIndex").toInt(), 0);
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(QMetaObject::invokeMethod(panel->findChild<QObject*>("mapNextHour"), "clicked"));
        QCOMPARE(panel->property("hourIndex").toInt(), 1);
        QVERIFY(!panel->property("playing").toBool());
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        auto* slider = panel->findChild<QObject*>("mapTimeline");
        QVERIFY(slider);
        slider->setProperty("value", 2);
        QVERIFY(QMetaObject::invokeMethod(slider, "moved"));
        QCOMPARE(panel->property("hourIndex").toInt(), 2);
        QVERIFY(!panel->property("playing").toBool());
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(tiles.requests, tiles.active.size());
        const auto tileRequestsBeforeSwitch = tiles.requests;
        auto* mapWindow =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        auto* tabs = panel->findChild<QObject*>("mapLayerTabs");
        QVERIFY(mapWindow && tabs);
        QTRY_COMPARE(tabs->property("count").toInt(), 4);
        QQuickItem* firstTab = nullptr;
        QVERIFY(QMetaObject::invokeMethod(tabs, "itemAt", Q_RETURN_ARG(QQuickItem*, firstTab),
                                          Q_ARG(int, 0)));
        QVERIFY(firstTab);
        firstTab->forceActiveFocus();
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(panel->property("playing").toBool());
        QTest::keyClick(mapWindow, Qt::Key_Right);
        QTRY_COMPARE(panel->property("layerIndex").toInt(), 1);
        QCOMPARE(card->objectName(), QString("mapTemperatureModule"));
        QVERIFY(!panel->property("playing").toBool());
        QCOMPARE(panel->property("hourIndex").toInt(), 2);
        QTest::keyClick(mapWindow, Qt::Key_Right);
        QTRY_COMPARE(panel->property("layerIndex").toInt(), 2);
        QTRY_VERIFY(card->findChild<QObject*>("windAnimationTimer")->property("running").toBool());
        QCOMPARE(card->objectName(), QString("mapWindModule"));
        panel->setProperty("layerIndex", 1);
        QTRY_VERIFY(!card->findChild<QObject*>("windAnimationTimer")->property("running").toBool());
        QVERIFY(evaluate(engine, card, "windField === null && particles.length === 0").toBool());
        QCOMPARE(card->objectName(), QString("mapTemperatureModule"));
        panel->setProperty("layerIndex", 2);
        QTest::qWait(100);
        QCOMPARE(tiles.requests, tileRequestsBeforeSwitch);
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(panel->findChildren<QObject*>("mapModuleTitle").size(), 1);
        QCOMPARE(evaluate(engine, card, "windSpeed(10)").toString(), QString("22 mph"));
        auto metricSnapshot = selectedSnapshot(2, "New York, NY");
        auto controls = metricSnapshot["controls"].toObject();
        controls["units"] = "C";
        controls["units_mode"] = "auto";
        metricSnapshot["controls"] = controls;
        auto* overlay = card->findChild<QObject*>("mapOverlay");
        QVERIFY(overlay);
        QSignalSpy painted(overlay, SIGNAL(painted()));
        QTest::qWait(100);
        painted.clear();
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot}});
        QCOMPARE(card->property("units").toString(), QString("C"));
        QCOMPARE(evaluate(engine, card, "windSpeed(10)").toString(), QString("36 km/h"));
        QCOMPARE(card->findChild<QObject*>("mapLegend")->property("text").toString(),
                 QString(card->property("animationActive").toBool()
                             ? "Trails flow downwind · tap to inspect · km/h"
                             : "Static trails · tap to inspect · km/h"));
        QVERIFY(card->findChild<QObject*>("mapCredit")
                    ->property("text")
                    .toString()
                    .startsWith("16.1 km radius"));
        QTRY_VERIFY_WITH_TIMEOUT(painted.size() > 0, 3000);
        controls["wind_units"] = "kn";
        metricSnapshot["controls"] = controls;
        metricSnapshot["snapshot_revision"] = 3;
        painted.clear();
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot}});
        QCOMPARE(evaluate(engine, card, "windSpeed(10)").toString(), QString("19 kn"));
        QVERIFY(card->findChild<QObject*>("mapLegend")
                    ->property("text")
                    .toString()
                    .endsWith("tap to inspect · kn"));
        QTRY_VERIFY_WITH_TIMEOUT(painted.size() > 0, 3000);
        // Changing the display units must reuse the loaded map and tiles.
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(tiles.requests, tiles.active.size());
        QCOMPARE(evaluate(engine, card, "mapX(-73.9)>mapX(-74.0)").toBool(), true);
        QCOMPARE(evaluate(engine, card, "mapY(40.8)<mapY(40.7)").toBool(), true);
        auto* window = qobject_cast<QWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(panel->property("playing").toBool());
        window->showMinimized();
        QVERIFY(window->isVisible());
        QTRY_VERIFY(!root->property("mapActive").toBool());
        QTRY_VERIFY(card.isNull());
        QVERIFY(!panel->property("playing").toBool());
        QCOMPARE(panel->property("hourIndex").toInt(), 0);
        QTRY_VERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
        QVERIFY(tiles.active.isEmpty());
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_close"));
        const auto requestsBeforeRestore = tiles.requests;
        // A late forecast event while minimized must not restart geographic tile work.
        deliver(transport,
                {{"version", 1},
                 {"event", "map"},
                 {"map",
                  QJsonObject{
                      {"status", "loading"}, {"offline", false}, {"error", ""}, {"data", data}}}});
        QCOMPARE(tiles.requests, requestsBeforeRestore);
        window->showNormal();
        QTRY_VERIFY(root->property("mapActive").toBool());
        QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QTRY_COMPARE(transport.requests.last()["op"].toString(), QString("map_open"));
        const auto capture = qEnvironmentVariable("WEATHER_QT_MAP_PLAYBACK_SCREENSHOT");
        if (!capture.isEmpty()) {
            window->resize(700, 850);
            QTest::qWait(100);
            flick->setProperty("contentY", panel->property("y").toReal() - 12);
            QTest::qWait(100);
        }
        deliver(transport,
                {{"version", 1},
                 {"event", "map"},
                 {"map", QJsonObject{{"status", "fresh"},
                                     {"offline", false},
                                     {"error", ""},
                                     {"fetched_at", data["fetched_at"]},
                                     {"fetched_label", "Mon Sep 28, 8:00 AM EDT"},
                                     {"hour_labels", QJsonArray{"Mon Sep 28, 8:00 AM EDT",
                                                                "Mon Sep 28, 9:00 AM EDT",
                                                                "Mon Sep 28, 10:00 AM EDT"}},
                                     {"data", data}}}});
        if (!capture.isEmpty()) {
            QTest::qWait(100);
            auto* quickWindow = qobject_cast<QQuickWindow*>(window);
            QVERIFY(quickWindow);
            QVERIFY(quickWindow->grabWindow().save(capture));
            for (const int width : {700, 1200}) {
                window->setMaximumWidth(1600);
                window->resize(width, 850);
                for (int layer = 0; layer < 3; ++layer) {
                    panel->setProperty("layerIndex", layer);
                    QTest::qWait(100);
                    flick->setProperty("contentY", panel->property("y").toReal() - 12);
                    QTest::qWait(100);
                    QVERIFY(quickWindow->grabWindow().save(QString(capture).replace(
                        ".png", QString("-%1-%2.png").arg(width).arg(layer))));
                }
            }
        }
        QVERIFY(QMetaObject::invokeMethod(playback, "clicked"));
        QVERIFY(panel->property("playing").toBool());
        auto oneHour = data;
        oneHour["hours"] = QJsonArray{hours.first()};
        QJsonArray shortenedCells;
        for (const auto& value : cells) {
            auto cell = value.toObject();
            for (const auto key :
                 {"temperature_c", "wind_speed_m_s", "wind_from_deg", "precipitation_mm"})
                cell[key] = QJsonArray{cell[key].toArray().first()};
            shortenedCells.append(cell);
        }
        oneHour["cells"] = shortenedCells;
        deliver(transport,
                {{"version", 1},
                 {"event", "map"},
                 {"map", QJsonObject{{"status", "fresh"},
                                     {"offline", false},
                                     {"error", ""},
                                     {"fetched_at", data["fetched_at"]},
                                     {"fetched_label", "Mon Sep 28, 8:00 AM EDT"},
                                     {"hour_labels", QJsonArray{"Mon Sep 28, 8:00 AM EDT"}},
                                     {"data", oneHour}}}});
        QVERIFY(!panel->property("playing").toBool());
        QCOMPARE(panel->property("hourIndex").toInt(), 0);
        QVERIFY(!playback->property("enabled").toBool());
        evaluate(engine, panel, "startPlayback()");
        QVERIFY(!panel->property("playing").toBool());
        window->hide();
        QTRY_VERIFY(tiles.closes > 0);
        QVERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
    }
    void radarLatestIntentAndValidation() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QVERIFY(evaluate(engine, bridge.data(), "openRadar(40,-74,7)").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "closeRadar()").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "openRadar(41,-75,8)").toBool());
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport, {{"version", 1}, {"event", "radar"}, {"radar", radarFixture(1)}});
        QCOMPARE(evaluate(engine, bridge.data(), "weatherRadar.status").toString(),
                 QString("loading"));
        deliver(transport,
                {{"version", 1}, {"request_id", 0}, {"ok", false}, {"error", "unavailable"}});
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["view"].toMap()["latitude"].toDouble(), 41.0);
        QCOMPARE(transport.requests.last()["view"].toMap()["client_token"].toInt(), 2);
        QCOMPARE(evaluate(engine, bridge.data(), "weatherRadar.status").toString(),
                 QString("loading"));
        deliver(transport, {{"version", 1}, {"event", "radar"}, {"radar", radarFixture(2)}});
        QCOMPARE(evaluate(engine, bridge.data(), "weatherRadar.frames.length").toInt(), 3);
        QVERIFY(!bridge->property("disconnected").toBool());
        // Zero time in initial/loading metadata and nanosecond NOAA times are accepted.
        QVERIFY(
            evaluate(engine, bridge.data(), "Forecast.radarTime('0001-01-01T00:00:00Z').length > 0")
                .toBool());
        const auto json =
            QString::fromUtf8(QJsonDocument(radarFixture()).toJson(QJsonDocument::Compact));
        const QStringList mutations{"v.frames[0].id='image://elsewhere/file'",
                                    "v.frames[1].time=v.frames[0].time",
                                    "v.frames[1].id=v.frames[0].id",
                                    "v.frames[0].state='pending'",
                                    "v.view.east=v.view.west",
                                    "v.legend='../bad'",
                                    "v.client_token=-1",
                                    "v.extra=true",
                                    "v.status='unsupported'",
                                    "v.frames[0].time='not a timestamp'",
                                    "v.latest_label='x'.repeat(81)",
                                    "v.frames=Array(25).fill(v.frames[0])"};
        for (const auto& mutation : mutations)
            QVERIFY2(
                evaluate(engine, bridge.data(),
                         "(function(){let v=" + json + ";" + mutation +
                             ";try{Forecast.radarState(v);return false}catch(e){return true}})()")
                    .toBool(),
                qPrintable(mutation));
        QVERIFY(evaluate(engine, bridge.data(), "closeRadar()").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QCOMPARE(transport.requests.last()["op"].toString(), QString("radar_close"));
        deliver(transport, {{"version", 1}, {"event", "radar"}, {"radar", radarFixture(2)}});
        QCOMPARE(evaluate(engine, bridge.data(), "weatherRadar.frames.length").toInt(), 0);
    }
    void radarOnDemandPlaybackAndNavigation() {
        FakeTransport transport;
        FakeMapTiles tiles;
        RadarImageControl control;
        QQmlApplicationEngine engine;
        auto* images = new FakeRadarImages(control.state());
        engine.addImageProvider("radar", images);
        engine.setInitialProperties({{"weatherTransport", QVariant::fromValue(&transport)},
                                     {"mapTiles", QVariant::fromValue(&tiles)},
                                     {"radarImages", QVariant::fromValue(&control)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto data = selectedSnapshot(1, "New York, NY");
        auto location = data["location"].toObject();
        location["latitude"] = 40.7128;
        location["longitude"] = -74.006;
        data["location"] = location;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", data}});
        auto* panel = shellItem(root, "weatherMaps");
        auto* flick =
            root->findChild<QObject*>("forecastScroll")->property("contentItem").value<QObject*>();
        QVERIFY(panel && flick);
        QVERIFY(!shellItem(root, "radarMap"));
        QCOMPARE(images->calls.load(), 0);
        QTest::qWait(100);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 40);
        QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
        int acknowledged = 0;
        drain(transport, acknowledged);
        panel->findChild<QObject*>("mapLayerTabs")->setProperty("currentIndex", 3);
        QTRY_VERIFY(shellItem(root, "radarMap"));
        QTRY_VERIFY(evaluate(engine, root, "backend.radarWanted").toBool());
        drain(transport, acknowledged);
        QVERIFY(control.state()->active.load());
        QTRY_VERIFY(!shellItem(root, "mapPrecipitationModule"));
        QVERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
        QPointer<QObject> radar = shellItem(root, "radarMap");
        const int token = evaluate(engine, root, "backend.radarToken").toInt();
        deliver(transport, {{"version", 1}, {"event", "radar"}, {"radar", radarFixture(token)}});
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine, radar, "displayedFrame !== null").toBool(), 2000);
        QCOMPARE(evaluate(engine, radar, "displayedFrame.id").toString(), QString(64, 'c'));
        QCOMPARE(evaluate(engine, radar, "zoom").toInt(), 7);
        QVERIFY(qAbs(evaluate(engine, radar, "centerLat").toDouble() - 40.7128) < 0.00001);
        QVERIFY(qAbs(evaluate(engine, radar, "centerLon").toDouble() + 74.006) < 0.00001);
        QVERIFY(tiles.requests <= 9);
        QTRY_COMPARE(images->calls.load(),
                     2); // One displayed frame plus legend, no history textures.
        QCOMPARE(images->inactiveCalls.load(), 0);
        evaluate(engine, radar, "select(0)");
        QCOMPARE(evaluate(engine, radar, "displayedFrame.id").toString(), QString(64, 'c'));
        QTRY_COMPARE(images->calls.load(), 3);
        evaluate(engine, radar, "select(1)"); // Coalesce while the first replacement is loading.
        QTRY_COMPARE_WITH_TIMEOUT(evaluate(engine, radar, "displayedFrame.id").toString(),
                                  QString(64, 'b'), 2000);
        QCOMPARE(images->calls.load(), 4);
        QVERIFY(radar->findChild<QObject*>("radarObservationTime")
                    ->property("text")
                    .toString()
                    .contains("1:15:13 PM EDT"));
        QCOMPARE(
            int(!radar->findChild<QObject*>("radarImageA")->property("source").toUrl().isEmpty()) +
                int(!radar->findChild<QObject*>("radarImageB")
                         ->property("source")
                         .toUrl()
                         .isEmpty()),
            1);
        radar->setProperty("visualQuality", "full");
        QVERIFY(radar->property("canPlay").toBool());
        evaluate(engine, radar, "playing=true");
        QTRY_VERIFY_WITH_TIMEOUT(
            evaluate(engine, radar, "displayedFrame.id !== '" + QString(64, 'b') + "'").toBool(),
            2000);
        radar->setProperty("reducedMotion", true);
        QVERIFY(!radar->property("playing").toBool());
        images->failNext.store(true);
        evaluate(engine, radar, "select(0)");
        QTRY_VERIFY(radar->property("imageError").toBool());
        evaluate(engine, radar, "latest()");
        QTRY_COMPARE(evaluate(engine, radar, "displayedFrame ? displayedFrame.id : ''").toString(),
                     QString(64, 'c'));
        QVERIFY(!radar->property("imageError").toBool());
        const auto previousHeight =
            radar->findChild<QObject*>("radarMapArea")->property("height").toDouble();
        QMetaObject::invokeMethod(radar->findChild<QObject*>("radarExpand"), "clicked");
        QTRY_VERIFY(radar->findChild<QObject*>("radarMapArea")->property("height").toDouble() >
                    previousHeight);
        auto* area = qobject_cast<QQuickItem*>(radar->findChild<QObject*>("radarMapArea"));
        QVERIFY(area && area->window());
        area->forceActiveFocus();
        QTest::keyClick(area->window(), Qt::Key_Right);
        drain(transport, acknowledged);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("radar_view"));
        QVERIFY(transport.requests.last()["view"].toMap()["longitude"].toDouble() > -74.006);
        QVERIFY(evaluate(engine, radar, "displayedFrame === null").toBool());
        QVERIFY(control.state()->generation.load() > 1);
        // Old images/events cannot refill the new viewport while it loads.
        deliver(transport, {{"version", 1}, {"event", "radar"}, {"radar", radarFixture(token)}});
        QTest::qWait(250);
        QVERIFY(evaluate(engine, radar, "displayedFrame === null").toBool());
        QTest::keyClick(area->window(), Qt::Key_Home);
        drain(transport, acknowledged);
        QCOMPARE(transport.requests.last()["view"].toMap()["zoom"].toInt(), 7);
        QCOMPARE(transport.requests.last()["view"].toMap()["longitude"].toDouble(), -74.006);
        root->setProperty("effectsOpen", true);
        drain(transport, acknowledged);
        QTRY_VERIFY(radar.isNull());
        QVERIFY(!control.state()->active.load());
        QVERIFY(!evaluate(engine, root, "backend.radarWanted").toBool());
        const int calls = images->calls.load();
        QTest::qWait(750);
        QCOMPARE(images->calls.load(), calls);
        QVERIFY(tiles.active.isEmpty());
    }
    void mapRapidToggleKeepsLatestIntent() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QVERIFY(evaluate(engine, bridge.data(), "openMap()").toBool());
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_open"));
        QVERIFY(evaluate(engine, bridge.data(), "closeMap()").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "openMap()").toBool());
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 1);
        QVERIFY(bridge->property("mapWanted").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "closeMap()").toBool());
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_close"));
        QVERIFY(evaluate(engine, bridge.data(), "openMap()").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 3);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_open"));
        QVERIFY(evaluate(engine, bridge.data(), "closeMap()").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 2}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 4);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("map_close"));
        deliver(transport, {{"version", 1}, {"request_id", 3}, {"ok", true}});
        QCOMPARE(bridge->property("pending").toInt(), -1);
        QVERIFY(!bridge->property("mapWanted").toBool());
        QCOMPARE(evaluate(engine, bridge.data(), "weatherMap.status").toString(),
                 QString("closed"));
        QVERIFY(!bridge->property("disconnected").toBool());
    }
    void applicationWindowMaps() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        QWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = candidate;
        QVERIFY(window);
        QTRY_VERIFY(window->isVisible());
        QTRY_VERIFY(window->isExposed());
    }
    void dashboardEditorConflictsAndReorderedMapDemand() {
        FakeTransport transport;
        FakeMapTiles tiles;
        QQmlApplicationEngine engine;
        engine.setInitialProperties({{"weatherTransport", QVariant::fromValue(&transport)},
                                     {"mapTiles", QVariant::fromValue(&tiles)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto state = selectedSnapshot(1, "New York, NY");
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTest::qWait(100);
        QVERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
        auto preferences =
            QJsonDocument::fromJson(
                evaluate(engine, root, "JSON.stringify(Dashboard.defaults())").toString().toUtf8())
                .object();
        auto sections = preferences["sections"].toArray();
        auto maps = sections.takeAt(3);
        sections.prepend(maps);
        preferences["sections"] = sections;
        state["snapshot_revision"] = 2;
        state["dashboard"] =
            QJsonObject{{"revision", 2}, {"error", QJsonValue::Null}, {"preferences", preferences}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QTRY_VERIFY(evaluate(engine, root, "backend.mapWanted").toBool());
        auto* map = shellItem(root, "weatherMaps");
        QVERIFY(map);
        auto* window = map->window();
        QVERIFY(window);
        auto* flick =
            root->findChild<QObject*>("forecastScroll")->property("contentItem").value<QObject*>();
        QVERIFY(flick);
        auto top = map->mapToScene(QPointF()).y() + flick->property("contentY").toReal();
        QVERIFY(qAbs(top - root->property("mapContentTop").toReal()) < 1);
        int acknowledged = 0;
        drain(transport, acknowledged);
        deliver(transport, mapEvent(mapFixture(), true));
        QTRY_VERIFY(shellItem(root, "mapPrecipitationModule"));
        window->resize(700, 850);
        QTest::qWait(100);
        QCOMPARE(shellItem(root, "weatherMaps"), map); // Width changes retain the scene.
        evaluate(engine, root, "openDashboard();dashboardLoader.item.update('density','compact')");
        QTRY_VERIFY(!evaluate(engine, root, "backend.mapWanted").toBool());
        drain(transport, acknowledged);
        QVERIFY(!shellItem(root, "mapPrecipitationModule"));
        QVERIFY(!tiles.active.size());
        // Another accepted save cannot be silently overwritten by this draft.
        state["snapshot_revision"] = 3;
        auto dashboard = state["dashboard"].toObject();
        dashboard["revision"] = 3;
        state["dashboard"] = dashboard;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QVERIFY(
            evaluate(
                engine, root,
                "dashboardLoader.item.conflict && dashboardLoader.item.draft.density==='compact'")
                .toBool());
        QVERIFY(!shellItem(root, "dashboardApply")->isEnabled());
        evaluate(engine, root, "dashboardLoader.item.reload()");
        QVERIFY(
            evaluate(
                engine, root,
                "!dashboardLoader.item.conflict && dashboardLoader.item.draft.density==='spacious'")
                .toBool());
        evaluate(engine, root, "dashboardLoader.item.update('density','compact')");
        QMetaObject::invokeMethod(shellItem(root, "dashboardApply"), "clicked");
        QCOMPARE(transport.requests.last()["op"].toString(), "set_dashboard");
        deliver(transport, {{"version", 1},
                            {"request_id", transport.requests.last()["request_id"].toInt()},
                            {"ok", false},
                            {"error", "state_io_failed"}});
        acknowledged = transport.requests.size();
        QVERIFY(evaluate(engine, root,
                         "root.dashboardOpen && dashboardLoader.item.draft.density==='compact' && "
                         "dashboardLoader.item.message.length>0")
                    .toBool());
        QMetaObject::invokeMethod(shellItem(root, "dashboardApply"), "clicked");
        state["snapshot_revision"] = 4;
        dashboard["revision"] = 4;
        dashboard["error"] = "save_unconfirmed";
        preferences["density"] = "compact";
        dashboard["preferences"] = preferences;
        state["dashboard"] = dashboard;
        deliver(transport, {{"version", 1},
                            {"request_id", transport.requests.last()["request_id"].toInt()},
                            {"ok", false},
                            {"error", "save_unconfirmed"},
                            {"snapshot", state}});
        acknowledged = transport.requests.size();
        QVERIFY(evaluate(engine, root,
                         "root.dashboardOpen && !dashboardLoader.item.conflict && "
                         "dashboardLoader.item.revision===4")
                    .toBool());
        QVERIFY(shellItem(root, "dashboardApply")->isEnabled());
        QMetaObject::invokeMethod(shellItem(root, "dashboardApply"), "clicked");
        dashboard["error"] = QJsonValue::Null;
        dashboard["revision"] = 5;
        state["dashboard"] = dashboard;
        state["snapshot_revision"] = 5;
        deliver(transport, {{"version", 1},
                            {"request_id", transport.requests.last()["request_id"].toInt()},
                            {"ok", true},
                            {"snapshot", state}});
        QTRY_VERIFY(!root->property("dashboardOpen").toBool());
        acknowledged++; // The successful save can immediately enqueue map demand.
        drain(transport, acknowledged);
        // Moving maps below the viewport stops demand and removes the active map.
        sections = preferences["sections"].toArray();
        maps = sections.takeAt(0);
        sections.append(maps);
        preferences["sections"] = sections;
        dashboard["preferences"] = preferences;
        dashboard["revision"] = 6;
        state["dashboard"] = dashboard;
        state["snapshot_revision"] = 6;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        flick->setProperty("contentY", 0);
        QTRY_VERIFY(!evaluate(engine, root, "backend.mapWanted || backend.radarWanted").toBool());
        QVERIFY(!shellItem(root, "mapPrecipitationModule"));
        drain(transport, acknowledged);
    }
    void dashboardValidationAndSaveQueue() {
        QQmlEngine engine;
        QQmlComponent schema(&engine);
        schema.setData(
            "import QtQml\nimport \"qrc:/ui/qml/Dashboard.js\" as Dashboard\nQtObject {}", QUrl());
        QScopedPointer<QObject> scope(schema.create());
        QVERIFY2(scope, qPrintable(schema.errorString()));
        auto valid = [&](const QString& mutation) {
            QQmlExpression expression(qmlContext(scope.data()), scope.data(),
                                      "(function(){let p=Dashboard.defaults();" + mutation +
                                          "; return Dashboard.preferences(p)})()");
            expression.evaluate();
            return !expression.hasError();
        };
        QVERIFY(valid(""));
        QVERIFY(valid("p.sections.forEach(r=>r.enabled=false);p.hourly=['wind_gust_m_s']"));
        for (const auto& mutation :
             {"p.extra=1", "p.density='dense'", "p.schema_version=2", "p.sections[0].id='maps'",
              "p.metrics[0].enabled=1", "p.metrics.forEach(r=>r.enabled=false)", "p.hourly=[]",
              "p.hourly=['humidity','humidity']", "p.hourly=['unknown']",
              "p.hourly=['humidity','wind_speed_m_s','wind_gust_m_s','uv_index']"})
            QVERIFY2(!valid(QString::fromLatin1(mutation)), mutation);
        QCOMPARE(evaluate(engine, scope.data(),
                          "JSON.stringify(Dashboard.rows(Dashboard.defaults().sections,true))")
                     .toString(),
                 QString("[[\"hourly\"],[\"daily\",\"metrics\"],[\"maps\"],[\"air_quality\"]]"));
        QVERIFY(evaluate(engine, scope.data(),
                         "Dashboard.state(undefined).preferences.hourly.length===2")
                    .toBool());
        for (const auto& input :
             {"null", "{preferences:Dashboard.defaults(),revision:0,error:null}",
              "{preferences:Dashboard.defaults(),revision:9007199254740992,error:null}",
              "{preferences:Dashboard.defaults(),revision:1,error:'unknown'}"}) {
            QQmlExpression expression(qmlContext(scope.data()), scope.data(),
                                      "Dashboard.state(" + QString::fromLatin1(input) + ")");
            expression.evaluate();
            QVERIFY(expression.hasError());
        }
        FakeTransport transport;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QSignalSpy saved(bridge.data(), SIGNAL(dashboardFinished(bool, QString)));
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot(1)}});
        QVERIFY(
            evaluate(engine, bridge.data(), "snapshot.dashboard.preferences.density==='spacious'")
                .toBool());
        evaluate(
            engine, bridge.data(),
            "send('set_controls',{units:'C'});let "
            "p=Dashboard.defaults();p.density='compact';saveDashboard(1,p);p.density='spacious'");
        QCOMPARE(transport.requests.size(), 1);
        QVERIFY(bridge->property("dashboardSaving").toBool());
        QVERIFY(!evaluate(engine, bridge.data(), "saveDashboard(1,Dashboard.defaults())").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(transport.requests.last()["op"].toString(), "set_dashboard");
        QCOMPARE(transport.requests.last()["dashboard"]
                     .toMap()["preferences"]
                     .toMap()["density"]
                     .toString(),
                 "compact");
        deliver(transport,
                {{"version", 1}, {"request_id", 1}, {"ok", false}, {"error", "dashboard_changed"}});
        QCOMPARE(saved.size(), 1);
        QCOMPARE(saved.last()[1].toString(), "dashboard_changed");
        QVERIFY(!bridge->property("dashboardSaving").toBool());
        evaluate(engine, bridge.data(), "saveDashboard(2,Dashboard.defaults())");
        deliver(transport,
                {{"version", 1}, {"request_id", 2}, {"ok", false}, {"error", "state_io_failed"}});
        QCOMPARE(saved.last()[1].toString(), "state_io_failed");
        evaluate(engine, bridge.data(), "saveDashboard(2,Dashboard.defaults())");
        deliver(transport, {{"version", 1}, {"request_id", 3}, {"ok", true}});
        QVERIFY(saved.last()[0].toBool());
        evaluate(engine, bridge.data(),
                 "saveDashboard(3,Dashboard.defaults());fail('disconnected')");
        QVERIFY(!bridge->property("dashboardSaving").toBool());
        QCOMPARE(saved.last()[1].toString(), "unavailable");
    }
    void forecastChangesQueueVisibilityAndValidation() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        auto state = metricSnapshot(1);
        auto location = state["location"].toObject();
        location["latitude"] = 40.7128;
        location["longitude"] = -74.006;
        state["location"] = location;
        state["location_settings"] = selectedSnapshot(1, "x")["location_settings"];
        state["saved_locations"] = savedSnapshot(1)["saved_locations"];
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        evaluate(engine, bridge.data(),
                 "subscribed=true; acknowledgeRenderedForecast(forecastContext)");
        QCOMPARE(transport.requests.size(), 0); // No visible header.
        evaluate(engine, bridge.data(),
                 "forecastVisible=true; send('set_controls',{units:'C'}); "
                 "acknowledgeRenderedForecast(forecastContext)");
        QCOMPARE(transport.requests.size(), 1);
        QVERIFY(evaluate(engine, bridge.data(), "queuedChanges!==null").toBool());
        evaluate(engine, bridge.data(), "forecastVisible=false");
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 1); // Hidden queued acknowledgment discarded.
        evaluate(engine, bridge.data(),
                 "forecastVisible=true; acknowledgeRenderedForecast(forecastContext)");
        QCOMPARE(transport.requests.last()["op"].toString(), QString("forecast_presented"));
        QVERIFY(!bridge->property("busy").toBool()); // Optional calculation never locks controls.
        auto query = QJsonObject::fromVariantMap(transport.requests.last()["forecast"].toMap());
        QJsonObject change{{"kind", "temperature"},
                           {"start", "2026-09-28T13:00:00Z"},
                           {"end", "2026-09-28T13:00:00Z"},
                           {"range_label", "Mon Sep 28, 1:00 PM UTC"},
                           {"samples", 1},
                           {"before", 15},
                           {"after", 20},
                           {"previous_start", QJsonValue::Null},
                           {"previous_end", QJsonValue::Null},
                           {"previous_range_label", QJsonValue::Null}};
        QJsonObject result{{"status", "ready"},
                           {"location_id", query["location_id"]},
                           {"latitude", query["latitude"]},
                           {"longitude", query["longitude"]},
                           {"timezone", query["timezone"]},
                           {"source", "Open-Meteo"},
                           {"current_retrieved", query["forecast_at"]},
                           {"previous_retrieved", "2026-09-28T11:00:00Z"},
                           {"current_retrieved_label", "Mon Sep 28, 12:00 PM UTC"},
                           {"previous_retrieved_label", "Mon Sep 28, 11:00 AM UTC"},
                           {"start", "2026-09-28T12:30:00.123456789Z"},
                           {"end", "2026-09-30T12:30:00.123456789Z"},
                           {"save_status", "saved"},
                           {"history_recovered", false},
                           {"coverage", QJsonObject{{"expected_points", 48},
                                                    {"expected_intervals", 47},
                                                    {"temperature", 1},
                                                    {"probability", 0},
                                                    {"precipitation", 0},
                                                    {"gusts", 0}}},
                           {"changes", QJsonArray{change}}};
        // A reply after hide is validated but not displayed; restore can retry.
        evaluate(engine, bridge.data(), "forecastVisible=false");
        deliver(transport,
                {{"version", 1}, {"request_id", 1}, {"ok", true}, {"forecast_changes", result}});
        QVERIFY(!bridge->property("disconnected").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "changesResult===null").toBool());
        evaluate(engine, bridge.data(),
                 "forecastVisible=true; acknowledgeRenderedForecast(forecastContext)");
        deliver(transport,
                {{"version", 1}, {"request_id", 2}, {"ok", true}, {"forecast_changes", result}});
        QCOMPARE(bridge->property("changesState").toString(), QString("ready"));
        QCOMPARE(evaluate(engine, bridge.data(), "Changes.title(changesResult.changes[0],'F')")
                     .toString(),
                 QString("9°F warmer"));
        evaluate(engine, bridge.data(), "acknowledgeRenderedForecast(forecastContext)");
        QCOMPARE(transport.requests.size(), 3); // Repeated frames are free.
        const auto json = QString::fromUtf8(QJsonDocument(result).toJson(QJsonDocument::Compact));
        const auto qjson = QString::fromUtf8(QJsonDocument(query).toJson(QJsonDocument::Compact));
        const QStringList edits{"v.changes.push(v.changes[0])", "v.location_id='wrong'",
                                "v.coverage.temperature=49",    "v.changes[0].after=NaN",
                                "v.changes[0].end=v.start",     "v.changes[0].samples=2",
                                "v.previous_retrieved=null",    "v.extra=true",
                                "v.status='no_previous'",       "v.save_status='maybe'"};
        for (const auto& edit : edits)
            QVERIFY2(evaluate(engine, bridge.data(),
                              "(function(){let v=" + json + ";" + edit + ";try{Changes.result(v," +
                                  qjson + ");return false}catch(e){return true}})()")
                         .toBool(),
                     qPrintable(edit));
        // Replacement clears the old display before any response can arrive.
        state["snapshot_revision"] = 2;
        auto source = state["source"].toObject();
        source["fetched_at"] = "2026-09-28T12:15:00Z";
        state["source"] = source;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
        QVERIFY(evaluate(engine, bridge.data(), "changesResult===null && changesAttemptedKey===''")
                    .toBool());
        evaluate(engine, bridge.data(), "acknowledgeRenderedForecast(forecastContext)");
        deliver(transport, {{"version", 1},
                            {"request_id", 3},
                            {"ok", false},
                            {"error", "forecast_changes_unavailable"}});
        QCOMPARE(bridge->property("changesState").toString(), QString("unavailable"));
        QVERIFY(!bridge->property("disconnected").toBool());
    }
    void outdoorQueueFreshnessAndValidation() {
        FakeTransport transport;
        transport.diagnostic = true;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot(1)}});
        evaluate(engine, bridge.data(),
                 "send('set_controls', {units:'C'}); loadOutdoor(null); loadOutdoor(null)");
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["op"].toString(), "outdoor_plan");
        evaluate(engine, bridge.data(), "closeOutdoor()");
        deliver(transport,
                {{"version", 1}, {"request_id", 1}, {"ok", true}, {"outdoor", outdoorFixture()}});
        QVERIFY(
            evaluate(engine, bridge.data(), "outdoorState === 'closed' && outdoorResult === null")
                .toBool());
        evaluate(engine, bridge.data(), "loadOutdoor(null)");
        auto changed = metricSnapshot(2);
        auto source = changed["source"].toObject();
        source["freshness"] = "expired";
        changed["source"] = source;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", changed}});
        deliver(transport,
                {{"version", 1}, {"request_id", 2}, {"ok", true}, {"outdoor", outdoorFixture()}});
        QVERIFY(evaluate(engine, bridge.data(),
                         "outdoorState === 'unavailable' && outdoorResult === null && available")
                    .toBool());
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot(3)}});
        evaluate(engine, bridge.data(), "loadOutdoor(null)");
        deliver(transport,
                {{"version", 1}, {"request_id", 3}, {"ok", true}, {"outdoor", outdoorFixture()}});
        QVERIFY(evaluate(engine, bridge.data(),
                         "outdoorResult.windows[0].fits && outdoorState === 'ready'")
                    .toBool());
        evaluate(engine, bridge.data(), "loadOutdoor(null)");
        auto malformed = outdoorFixture();
        auto windows = malformed["windows"].toArray();
        auto row = windows[0].toObject();
        row["low_c"] = QJsonValue::Null; // Cannot present missing temperature as a match.
        windows[0] = row;
        malformed["windows"] = windows;
        deliver(transport,
                {{"version", 1}, {"request_id", 4}, {"ok", true}, {"outdoor", malformed}});
        QVERIFY(evaluate(engine, bridge.data(), "disconnected && outdoorResult === null").toBool());
    }
    void warningQueueRejectsOldOrMismatchedDetails() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        auto load = [&](QChar key) {
            const QVariant reference =
                QVariantMap{{"location", QString(64, 'a')}, {"key", QString(64, key)}};
            return QMetaObject::invokeMethod(bridge.data(), "loadWarning",
                                             Q_ARG(QVariant, reference));
        };
        QVERIFY(evaluate(engine, bridge.data(), "send('set_controls', {units:'C'})").toBool());
        QVERIFY(load('b'));
        QVERIFY(load('c'));
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["op"].toString(), "warning_detail");
        QCOMPARE(transport.requests.last()["warning"].toMap()["key"].toString(), QString(64, 'c'));
        QVERIFY(QMetaObject::invokeMethod(bridge.data(), "closeWarning"));
        deliver(transport, {{"version", 1},
                            {"request_id", 1},
                            {"ok", true},
                            {"warning", warningDetailFixture('c')}});
        QCOMPARE(bridge->property("warningState").toString(), "closed");
        QVERIFY(evaluate(engine, bridge.data(), "warningDetail === null").toBool());
        QVERIFY(load('b'));
        deliver(
            transport,
            {{"version", 1}, {"request_id", 2}, {"ok", true}, {"warning", warningDetailFixture()}});
        QCOMPARE(bridge->property("warningState").toString(), "ready");
        QCOMPARE(evaluate(engine, bridge.data(), "warningDetail.instruction").toString(),
                 warningDetailFixture()["instruction"].toString());
        QVERIFY(load('c'));
        deliver(
            transport,
            {{"version", 1}, {"request_id", 3}, {"ok", true}, {"warning", warningDetailFixture()}});
        QVERIFY(bridge->property("disconnected").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "warningDetail === null").toBool());
    }
    void warningContractsAndUnavailableDetails() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        const auto status = warningSnapshot(1)["warning_notifications"].toObject();
        auto valid = [&](const QJsonObject& value, const QString& validator) {
            return evaluate(
                       engine, bridge.data(),
                       "(function(){try { Forecast." + validator + "(" +
                           QString::fromUtf8(QJsonDocument(value).toJson(QJsonDocument::Compact)) +
                           "); return true; } catch(e) { return false; }})()")
                .toBool();
        };
        QVERIFY(valid(status, "warningNotifications"));
        auto bad = status;
        auto settings = bad["settings"].toObject();
        settings["quiet_start"] = 7;
        bad["settings"] = settings;
        QVERIFY(!valid(bad, "warningNotifications"));
        bad = status;
        bad["ready"] = false;
        bad["actions"] = true;
        QVERIFY(!valid(bad, "warningNotifications"));
        bad = status;
        auto recent = bad["recent"].toArray();
        for (int i = 0; i < 16; ++i)
            recent.append(recent.first());
        bad["recent"] = recent;
        QVERIFY(!valid(bad, "warningNotifications"));
        auto detail = warningDetailFixture();
        QVERIFY(valid(detail, "warningDetail"));
        detail["description"] = QString(32001, 'x');
        QVERIFY(!valid(detail, "warningDetail"));
        const QVariant reference =
            QVariantMap{{"location", QString(64, 'a')}, {"key", QString(64, 'b')}};
        QVERIFY(
            QMetaObject::invokeMethod(bridge.data(), "loadWarning", Q_ARG(QVariant, reference)));
        deliver(
            transport,
            {{"version", 1}, {"request_id", 0}, {"ok", false}, {"error", "warning_unavailable"}});
        QCOMPARE(bridge->property("warningState").toString(), "unavailable");
        QVERIFY(!bridge->property("disconnected").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "warningDetail === null").toBool());
    }
    void warningOptOutClearsReadyAndPendingDetails() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        const QVariant reference =
            QVariantMap{{"location", QString(64, 'a')}, {"key", QString(64, 'b')}};
        for (int attempt = 0; attempt < 2; ++attempt) {
            deliver(transport, {{"version", 1},
                                {"event", "snapshot"},
                                {"snapshot", warningSnapshot(attempt * 2 + 1)}});
            QVERIFY(QMetaObject::invokeMethod(bridge.data(), "loadWarning",
                                              Q_ARG(QVariant, reference)));
            const QJsonObject reply{{"version", 1},
                                    {"request_id", attempt},
                                    {"ok", true},
                                    {"warning", warningDetailFixture()}};
            if (attempt == 0) {
                deliver(transport, reply);
                QCOMPARE(bridge->property("warningState").toString(), "ready");
            }
            deliver(transport, {{"version", 1},
                                {"event", "snapshot"},
                                {"snapshot", warningSnapshot(attempt * 2 + 2, false)}});
            if (attempt == 1)
                deliver(transport, reply);
            QCOMPARE(bridge->property("warningState").toString(), "unavailable");
            QVERIFY(bridge->property("warningError").toString().contains("turned off"));
            QVERIFY(evaluate(engine, bridge.data(), "warningDetail === null").toBool());
            QVERIFY(!bridge->property("disconnected").toBool());
            QVERIFY(QMetaObject::invokeMethod(bridge.data(), "closeWarning"));
        }
    }
    void warningActivationRestoresWindowWithoutRetainingToken() {
        WindowActivation activation;
        QQuickWindow window;
        const auto original = qgetenv("XDG_ACTIVATION_TOKEN");
        activation.show(nullptr, "unused");
        QVERIFY(!window.isVisible());
        window.showMinimized();
        QTRY_VERIFY(window.windowStates().testFlag(Qt::WindowMinimized));
        activation.show(&window, "fixture-token");
        QVERIFY(window.isVisible());
        QVERIFY(!window.windowStates().testFlag(Qt::WindowMinimized));
        QCOMPARE(qgetenv("XDG_ACTIVATION_TOKEN"), original);
        const QStringList invalid{QString(1025, 'x'), QString("bad\ntoken"), QString(QChar(0xd800)),
                                  QString(QChar(0x80))};
        for (const auto& token : invalid) {
            window.hide();
            activation.show(&window, token);
            QVERIFY(window.isVisible());
            QCOMPARE(qgetenv("XDG_ACTIVATION_TOKEN"), original);
        }
    }
    void warningNativeClickShowsLazyOriginalDetailAndKeepsAlive() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", warningSnapshot(1)}});
        auto* loader = root->findChild<QObject*>("warningDetailsLoader");
        QVERIFY(loader);
        QVERIFY(!loader->property("active").toBool());
        QVERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(QMetaObject::invokeMethod(root, "dismissWindow"));
        QTRY_VERIFY(!window->isVisible());
        QCOMPARE(transport.requests.size(), 0); // Warning opt-in hides rather than quitting.
        const QJsonObject reference{{"location", QString(64, 'a')}, {"key", QString(64, 'b')}};
        deliver(transport, {{"version", 1},
                            {"event", "warning_open"},
                            {"warning", reference},
                            {"activation_token", ""}});
        QTRY_VERIFY(window->isVisible());
        QTRY_VERIFY(window->isExposed());
        QVERIFY(loader->property("active").toBool());
        QTRY_VERIFY(loader->property("item").value<QObject*>());
        auto* popup = loader->property("item").value<QObject*>();
        QTRY_VERIFY(popup->property("opened").toBool());
        QCOMPARE(transport.requests.last()["op"].toString(), "warning_detail");
        deliver(transport, {{"version", 1},
                            {"request_id", transport.requests.last()["request_id"].toInt()},
                            {"ok", true},
                            {"warning", warningDetailFixture()}});
        QTRY_COMPARE(popup->property("state").toString(), "ready");
        auto* place = popup->findChild<QObject*>("warningDetailPlace");
        QVERIFY(place);
        QCOMPARE(place->property("text").toString(), "Boston, MA");
        QQuickItem* source = nullptr;
        QTRY_VERIFY((source = visualItem(window->contentItem(), "warningSource_instruction")));
        QCOMPARE(source->property("text").toString(),
                 warningDetailFixture()["instruction"].toString());
        QCOMPARE(source->property("textFormat").toInt(), 0);
        QVERIFY(source->property("readOnly").toBool());
        for (int i = 0; i < 8 && window->activeFocusItem() != source; ++i)
            QTest::keyClick(window, Qt::Key_Tab);
        QCOMPARE(window->activeFocusItem(), source);
        const auto prefix = qEnvironmentVariable("WEATHER_QT_WARNING_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-detail-wide.png"));
            window->resize(700, 650);
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-detail-compact.png"));
        }
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(!loader->property("active").toBool());
        QTRY_VERIFY(!loader->property("item").value<QObject*>());
        QVERIFY(evaluate(engine, root, "backend.warningDetail === null").toBool());
        QVERIFY(window->isVisible());
        QVERIFY(evaluate(engine, root, "!backend.closing").toBool());
        // A click with the window already visible must still show it, never toggle it off.
        deliver(transport, {{"version", 1},
                            {"event", "warning_open"},
                            {"warning", reference},
                            {"activation_token", ""}});
        QTRY_VERIFY(window->isVisible());
        QVERIFY(root->property("warningOpen").toBool());
    }
    void warningSettingsWaitForSavedStateAndOpenRecentNotice() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", warningSnapshot(1, false)}});
        auto* settingsLoader = root->findChild<QObject*>("warningSettingsLoader");
        QVERIFY(settingsLoader);
        QVERIFY(!settingsLoader->property("active").toBool());
        root->setProperty("effectsOpen", true);
        QTRY_VERIFY(settingsLoader->property("item").value<QObject*>());
        auto* drawer = root->findChild<QObject*>("effectsDrawer");
        QVERIFY(
            QMetaObject::invokeMethod(drawer, "showNotifications", Q_ARG(QVariant, QVariant())));
        auto* panel = settingsLoader->property("item").value<QObject*>();
        auto* toggle = qobject_cast<QQuickItem*>(panel->findChild<QObject*>("warningEnabled"));
        QVERIFY(toggle);
        toggle->forceActiveFocus();
        QTRY_VERIFY(window->isExposed());
        QTest::keyClick(window, Qt::Key_Space);
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(transport.requests.last()["op"].toString(), "set_warning_notifications");
        QCOMPARE(transport.requests.last()["notifications"].toMap()["enabled"].toBool(), true);
        QVERIFY(!toggle->property("checked").toBool());
        deliver(
            transport,
            {{"version", 1}, {"request_id", 0}, {"ok", true}, {"snapshot", warningSnapshot(2)}});
        QTRY_VERIFY(toggle->property("checked").toBool());
        QVERIFY(
            QMetaObject::invokeMethod(drawer, "showNotifications", Q_ARG(QVariant, QVariant())));
        const auto prefix = qEnvironmentVariable("WEATHER_QT_WARNING_SCREENSHOT_PREFIX");
        if (!prefix.isEmpty()) {
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-settings-wide.png"));
            window->resize(700, 650);
            QTest::qWait(80);
            QVERIFY(window->grabWindow().save(prefix + "-settings-compact.png"));
        }
        auto* recent = qobject_cast<QQuickItem*>(panel->findChild<QObject*>("recentWarning0"));
        if (!recent)
            recent = visualItem(window->contentItem(), "recentWarning0");
        QVERIFY(recent);
        recent->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_VERIFY(root->property("warningOpen").toBool());
        QVERIFY(!settingsLoader->property("active").toBool());
        QCOMPARE(transport.requests.last()["op"].toString(), "warning_detail");
        auto* detailLoader = root->findChild<QObject*>("warningDetailsLoader");
        QTRY_VERIFY(detailLoader->property("item").value<QObject*>());
        auto* popup = detailLoader->property("item").value<QObject*>();
        QTRY_VERIFY(popup->property("opened").toBool());
        QTest::keyClick(window, Qt::Key_Escape);
        QTRY_VERIFY(root->property("effectsOpen").toBool());
        QTRY_VERIFY(settingsLoader->property("active").toBool());
        QTRY_VERIFY(window->activeFocusItem() &&
                    window->activeFocusItem()->objectName() == "recentWarning0");
        root->setProperty("effectsOpen", false);
        QTRY_VERIFY(!settingsLoader->property("item").value<QObject*>());
    }
    void warningLongSourceAndRepeatedCloseReleaseObjects_data() {
        QTest::addColumn<bool>("multiline");
        QTest::newRow("separate-lines") << true;
        QTest::newRow("wrapped-paragraphs") << false;
    }
    void warningLongSourceAndRepeatedCloseReleaseObjects() {
        QFETCH(bool, multiline);
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        window->resize(700, 650);
        auto snapshot = warningSnapshot(1);
        auto controls = snapshot["controls"].toObject();
        controls["reduced_motion"] = true;
        snapshot["controls"] = controls;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot}});
        QTRY_VERIFY(window->isExposed());
        auto* loader = root->findChild<QObject*>("warningDetailsLoader");
        QVERIFY(loader);
        const QJsonObject reference{{"location", QString(64, 'a')}, {"key", QString(64, 'b')}};
        auto detail = warningDetailFixture();
        detail["instruction"] =
            QString(multiline ? "Original instructions.\n" : "Original instructions. ")
                .repeated(800)
                .left(16000);
        detail["description"] =
            QString(multiline ? "Original description.\n" : "Original description. ")
                .repeated(1600)
                .left(32000);
        for (int i = 0; i < 20; ++i) {
            deliver(transport, {{"version", 1},
                                {"event", "warning_open"},
                                {"warning", reference},
                                {"activation_token", ""}});
            QTRY_VERIFY(loader->property("item").value<QObject*>());
            QPointer<QObject> popup = loader->property("item").value<QObject*>();
            QTRY_VERIFY(popup->property("opened").toBool());
            deliver(transport, {{"version", 1},
                                {"request_id", transport.requests.last()["request_id"].toInt()},
                                {"ok", true},
                                {"warning", detail}});
            QTRY_COMPARE(popup->property("state").toString(), "ready");
            QPointer<QQuickItem> instructions =
                visualItem(window->contentItem(), "warningSource_instruction");
            QPointer<QQuickItem> description =
                visualItem(window->contentItem(), "warningSource_description");
            QPointer<QQuickItem> area = visualItem(window->contentItem(), "warningSource_area");
            QVERIFY(instructions && description && area);
            QCOMPARE(instructions->property("text").toString(), detail["instruction"].toString());
            instructions->forceActiveFocus();
            QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
            QCOMPARE(instructions->property("selectedText").toString(),
                     detail["instruction"].toString());
            description->forceActiveFocus();
            QTest::keyClick(window, Qt::Key_A, Qt::ControlModifier);
            QCOMPARE(description->property("selectedText").toString(),
                     detail["description"].toString());
            area->forceActiveFocus();
            auto* scroll = visualItem(window->contentItem(), "warningDetailsScroll");
            QVERIFY(scroll);
            QTRY_VERIFY(area->mapToScene(QPointF(0, 0)).y() >=
                            scroll->mapToScene(QPointF(0, 0)).y() &&
                        area->mapToScene(QPointF(0, area->height())).y() <=
                            scroll->mapToScene(QPointF(0, scroll->height())).y() + 1);
            if (i == 0) {
                const auto prefix = qEnvironmentVariable("WEATHER_QT_WARNING_SCREENSHOT_PREFIX");
                if (!prefix.isEmpty()) {
                    QTest::qWait(80);
                    QVERIFY(window->grabWindow().save(
                        prefix + (multiline ? "-long-source-end.png" : "-long-paragraph-end.png")));
                }
            }
            QTest::keyClick(window, Qt::Key_Escape);
            QTRY_VERIFY(popup.isNull());
            QVERIFY(instructions.isNull() && description.isNull() && area.isNull());
            QVERIFY(!loader->property("active").toBool());
            QVERIFY(evaluate(engine, root, "backend.warningDetail === null").toBool());
        }
    }
    void unsupportedNotifications() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        deliver(transport, {{"version", 1},
                            {"event", "snapshot"},
                            {"snapshot", snapshot(1, "Unsupported delivery")}});
        auto* toggle = root->findChild<QObject*>("notificationEnabled");
        auto* notice = root->findChild<QObject*>("notificationUnsupported");
        QVERIFY(toggle);
        QVERIFY(notice);
        root->setProperty("effectsOpen", true);
        QTRY_VERIFY(notice->property("visible").toBool());
        QVERIFY(!toggle->property("enabled").toBool());
        QVERIFY(notice->property("text").toString().contains("optional libnotify"));
        auto* stop = root->findChild<QObject*>("notificationStop");
        QVERIFY(stop);
        QVERIFY(!stop->property("enabled").toBool());
        QCOMPARE(transport.requests.size(), 0);
    }
    void snapshotOrderingAndAcknowledgment() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        emit transport.ready();
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot(2, "Newest")}});
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Newest"));
        QVERIFY(!bridge->property("disconnected")
                     .toBool()); // [] and null retained as ordinary JSON values.
        deliver(
            transport,
            {{"version", 1}, {"request_id", 0}, {"ok", true}, {"snapshot", snapshot(1, "Older")}});
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_presentation"));
        QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QCOMPARE(bridge->property("pending").toInt(), -1);
        QVERIFY(!bridge->property("busy").toBool());
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Newest"));
        QVERIFY(evaluate(engine, bridge.data(), "send('set_controls',{units:'C'})").toBool());
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot(3, "Latest")}});
        deliver(transport, {{"version", 1},
                            {"request_id", 2},
                            {"ok", false},
                            {"error", "invalid_controls"},
                            {"snapshot", snapshot(2, "Stale")}});
        QCOMPARE(bridge->property("pending").toInt(), -1);
        QVERIFY(!bridge->property("error").toString().isEmpty());
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Latest"));
        deliver(
            transport,
            {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot(3, "Same revision")}});
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Latest"));
        emit transport.ready();
        QCOMPARE(bridge->property("lastSnapshotRevision").toDouble(), 0.0);
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot(1, "New service")}});
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("New service"));
    }
    void presentationStartupAndCoalescing() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        bridge->setProperty("presentationActive", false);
        QCOMPARE(transport.requests.size(), 0);
        emit transport.ready();
        QCOMPARE(transport.requests.size(), 1);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("subscribe"));
        bridge->setProperty("presentationActive", true);
        bridge->setProperty("presentationActive", false);
        QCOMPARE(transport.requests.size(), 1);
        deliver(transport, {{"version", 1},
                            {"request_id", 0},
                            {"ok", true},
                            {"snapshot", snapshot(1, "Initial")}});
        QVERIFY(bridge->property("subscribed").toBool());
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_presentation"));
        QVERIFY(!transport.requests.last()["active"].toBool());
        QVERIFY(!bridge->property("busy").toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QVERIFY(evaluate(engine, bridge.data(), "send('set_controls',{units:'C'})").toBool());
        bridge->setProperty("presentationActive", true);
        bridge->setProperty("presentationActive", false);
        bridge->setProperty("presentationActive", true);
        QCOMPARE(transport.requests.size(), 3);
        deliver(transport, {{"version", 1}, {"request_id", 2}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 4);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_presentation"));
        QVERIFY(transport.requests.last()["active"].toBool());
        QVERIFY(!bridge->property("busy").toBool());
        QVERIFY(evaluate(engine, bridge.data(), "send('set_controls',{units:'F'})").toBool());
        deliver(
            transport,
            {{"version", 1}, {"event", "snapshot"}, {"snapshot", snapshot(3, "Freshest event")}});
        deliver(transport, {{"version", 1},
                            {"request_id", 3},
                            {"ok", true},
                            {"snapshot", snapshot(2, "Restore response")}});
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Freshest event"));
        QCOMPARE(transport.requests.size(), 5);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_controls"));
        deliver(transport, {{"version", 1}, {"request_id", 4}, {"ok", true}});
        bridge->setProperty("presentationActive", false);
        deliver(transport, {{"version", 1}, {"request_id", 5}, {"ok", true}});
        bridge->setProperty("presentationActive", true);
        deliver(transport, {{"version", 1},
                            {"request_id", 6},
                            {"ok", true},
                            {"snapshot", snapshot(4, "Fresh restore")}});
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 QString("Fresh restore"));
        QCOMPARE(bridge->property("pending").toInt(), -1);
    }
    void presentationWindowVisibility() {
        FakeTransport transport;
        FakeMapTiles tiles;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.rootContext()->setContextProperty("mapTiles", &tiles);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* window = qobject_cast<QWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        window->hide();
        emit transport.ready();
        QCOMPARE(transport.requests.last()["op"].toString(), QString("subscribe"));
        deliver(transport, {{"version", 1},
                            {"request_id", 0},
                            {"ok", true},
                            {"snapshot", snapshot(1, "Start hidden")}});
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_presentation"));
        QVERIFY(!transport.requests.last()["active"].toBool());
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        window->showNormal();
        QTRY_VERIFY(evaluate(engine, root, "backend.presentationActive").toBool());
        QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport, {{"version", 1},
                            {"request_id", 2},
                            {"ok", true},
                            {"snapshot", snapshot(2, "Restored")}});
        window->showMinimized();
        QVERIFY(window->isVisible());
        QTRY_VERIFY(!evaluate(engine, root, "backend.presentationActive").toBool());
        QVERIFY(!transport.requests.last()["active"].toBool());
        deliver(transport, {{"version", 1}, {"request_id", 3}, {"ok", true}});
        window->showNormal();
        QTRY_VERIFY(evaluate(engine, root, "backend.presentationActive").toBool());
        QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport, {{"version", 1},
                            {"request_id", 4},
                            {"ok", true},
                            {"snapshot", snapshot(3, "Restored again")}});
        QCOMPARE(evaluate(engine, root, "backend.snapshot.location").toString(),
                 QString("Restored again"));
    }
    void serviceStopped() {
        for (bool ok : {true, false}) {
            FakeTransport transport;
            QQmlEngine engine;
            engine.rootContext()->setContextProperty("weatherTransport", &transport);
            QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
            QScopedPointer<QObject> bridge(component.createWithInitialProperties(
                {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
            QVERIFY2(bridge, qPrintable(component.errorString()));
            QSignalSpy closed(bridge.data(), SIGNAL(closed(int)));
            QJsonObject event{{"version", 1}, {"event", "service_stopped"}, {"ok", ok}};
            if (!ok)
                event["error"] = "cleanup_failed";
            deliver(transport, event);
            QCOMPARE(closed.size(), 1);
            QCOMPARE(closed.first().first().toInt(), ok ? 0 : 1);
            QCOMPARE(transport.requests.size(), 0);
        }
    }
    void pendingAlertsKeepForecastUsable() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        root->setProperty("effectsOpen", false);
        auto value = metricSnapshot(1);
        auto alerts = QJsonObject{{"status", "unavailable"}, {"freshness", "pending"},
                                  {"refreshing", true},      {"source", "National Weather Service"},
                                  {"coverage", "US"},        {"fetched_at", QJsonValue::Null},
                                  {"items", QJsonArray{}}};
        value["alerts"] = alerts;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QVERIFY(evaluate(engine, root, "forecast !== null && current !== null").toBool());
        QCOMPARE(root->property("alertsStatusText").toString(),
                 QString::fromUtf8("Checking official alerts…"));
        const auto current = evaluate(engine, root, "JSON.stringify(current)").toString();
        value["snapshot_revision"] = 2;
        alerts["status"] = "available";
        alerts["freshness"] = "stale";
        alerts["refreshing"] = false;
        alerts["fetched_at"] = "2026-09-27T12:00:00Z";
        value["alerts"] = alerts;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QVERIFY(root->property("alertsStatusText")
                    .toString()
                    .contains("current alert status unavailable"));
        QCOMPARE(evaluate(engine, root, "JSON.stringify(current)").toString(), current);
        value["snapshot_revision"] = 3;
        alerts["freshness"] = "current";
        value["alerts"] = alerts;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QCOMPARE(root->property("alertsStatusText").toString(),
                 QString("No active NWS alerts reported"));
    }
    void searchValidationAndAlertCoverage() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        auto valid = snapshot(1, "São Paulo, São Paulo, Brazil");
        valid["location_settings"] =
            QJsonObject{{"mode", "place"},
                        {"zip_code", QJsonValue::Null},
                        {"busy", false},
                        {"error", QJsonValue::Null},
                        {"country_code", "BR"},
                        {"place", QJsonObject{{"provider", "open-meteo"}, {"id", 3448439}}}};
        valid["place_search"] =
            QJsonObject{{"generation", 3},
                        {"status", "ready"},
                        {"error", QJsonValue::Null},
                        {"results", QJsonArray{QJsonObject{{"id", 3448439},
                                                           {"name", "São Paulo"},
                                                           {"admin1", "São Paulo"},
                                                           {"country", "Brazil"},
                                                           {"country_code", "BR"}}}}};
        valid["alerts"] = QJsonObject{{"status", "not_supported_here"},
                                      {"source", QJsonValue::Null},
                                      {"coverage", "unsupported"},
                                      {"fetched_at", QJsonValue::Null},
                                      {"items", QJsonArray{}}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", valid}});
        QVERIFY(!bridge->property("disconnected").toBool());
        QCOMPARE(
            evaluate(engine, bridge.data(), "snapshot.place_search.results[0].name").toString(),
            QString::fromUtf8("São Paulo"));
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.alerts.coverage").toString(),
                 QString("unsupported"));
        auto invalid = valid;
        invalid["snapshot_revision"] = 2;
        auto search = invalid["place_search"].toObject();
        auto rows = search["results"].toArray();
        auto row = rows.first().toObject();
        row["name"] = "Paris\u202e";
        rows.replace(0, row);
        search["results"] = rows;
        invalid["place_search"] = search;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", invalid}});
        QVERIFY(bridge->property("disconnected").toBool());
        auto rejects = [this](QJsonObject value) {
            FakeTransport local;
            QQmlEngine localEngine;
            localEngine.rootContext()->setContextProperty("weatherTransport", &local);
            QQmlComponent localComponent(&localEngine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
            QScopedPointer<QObject> localBridge(localComponent.createWithInitialProperties(
                {{"weatherTransport",
                  localEngine.rootContext()->contextProperty("weatherTransport")}}));
            if (!localBridge)
                return false;
            deliver(local, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
            return localBridge->property("disconnected").toBool();
        };
        auto badSettings = valid;
        auto settings = badSettings["location_settings"].toObject();
        settings["unexpected"] = true;
        badSettings["location_settings"] = settings;
        QVERIFY(rejects(badSettings));
        auto duplicate = valid;
        auto dupSearch = duplicate["place_search"].toObject();
        auto dupRows = dupSearch["results"].toArray();
        dupRows.append(dupRows.first());
        dupSearch["results"] = dupRows;
        duplicate["place_search"] = dupSearch;
        QVERIFY(rejects(duplicate));
        auto badCoverage = valid;
        badCoverage["alerts"] = QJsonObject{{"status", "not_supported_here"},
                                            {"source", "National Weather Service"},
                                            {"coverage", "unsupported"},
                                            {"fetched_at", QJsonValue::Null},
                                            {"items", QJsonArray{}}};
        QVERIFY(rejects(badCoverage));
    }
    void searchQueuePrioritizesStopAndCoalesces() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        emit transport.ready();
        QCOMPARE(transport.requests.size(), 1);
        QVERIFY(evaluate(engine, bridge.data(),
                         "send('search_places',{query:'Berlin',country_code:'DE',client_token:1})")
                    .toBool());
        QVERIFY(evaluate(engine, bridge.data(),
                         "send('search_places',{query:'Paris',country_code:'FR',client_token:2})")
                    .toBool());
        QVERIFY(evaluate(engine, bridge.data(), "send('cancel_place_search')").toBool());
        QVERIFY(evaluate(engine, bridge.data(),
                         "send('search_places',{query:'Tokyo',country_code:'JP',client_token:3})")
                    .toBool());
        QVERIFY(evaluate(engine, bridge.data(), "send('stop_effects')").toBool());
        deliver(
            transport,
            {{"version", 1}, {"request_id", 0}, {"ok", true}, {"snapshot", snapshot(1, "Start")}});
        QCOMPARE(transport.requests.size(), 2);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("stop_effects"));
        deliver(transport, {{"version", 1}, {"request_id", 1}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 3);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("set_presentation"));
        deliver(transport, {{"version", 1}, {"request_id", 2}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 4);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("cancel_place_search"));
        deliver(transport, {{"version", 1}, {"request_id", 3}, {"ok", true}});
        QCOMPARE(transport.requests.size(), 5);
        QCOMPARE(transport.requests.last()["op"].toString(), QString("search_places"));
        QCOMPARE(transport.requests.last()["search"].toMap()["query"].toString(), QString("Tokyo"));
        QCOMPARE(transport.requests.last()["search"].toMap()["client_token"].toInt(), 3);
        QVERIFY(!bridge->property("busy").toBool());
    }
    void searchAcknowledgmentDoesNotPulseBusy() {
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QVERIFY(evaluate(engine, bridge.data(),
                         "send('search_places',{query:'Berlin',country_code:'DE',client_token:1})")
                    .toBool());
        QVERIFY(!bridge->property("busy").toBool());
        QSignalSpy busy(bridge.data(), SIGNAL(busyChanged()));
        deliver(transport, {{"version", 1}, {"request_id", 0}, {"ok", true}});
        QCOMPARE(busy.size(), 0);
        QVERIFY(!bridge->property("busy").toBool());
        QCOMPARE(bridge->property("pending").toInt(), -1);
    }
    void placeSearchInactiveCancelsDebounce_data() {
        QTest::addColumn<QString>("property");
        QTest::addColumn<bool>("value");
        QTest::newRow("hidden") << QString("visible") << false;
        QTest::newRow("disconnected") << QString("serviceAvailable") << false;
        QTest::newRow("location-changing") << QString("locationBusy") << true;
    }
    void placeSearchInactiveCancelsDebounce() {
        QFETCH(QString, property);
        QFETCH(bool, value);
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/PlaceSearch.qml"));
        QScopedPointer<QObject> picker(component.create());
        QVERIFY2(picker, qPrintable(component.errorString()));
        QSignalSpy searches(picker.data(), SIGNAL(searchRequested(QVariant)));
        auto* query = picker->findChild<QObject*>("placeQuery");
        auto* timer = picker->findChild<QObject*>("placeSearchDebounce");
        QVERIFY(query);
        QVERIFY(timer);
        query->setProperty("text", "Berlin");
        QVERIFY(timer->property("running").toBool());
        picker->setProperty(property.toUtf8().constData(), value);
        QVERIFY(!timer->property("running").toBool());
        QVERIFY(!picker->property("armed").toBool());
        // Even an already queued trigger or a programmatic edit cannot issue work.
        query->setProperty("text", "Tokyo");
        QVERIFY(!timer->property("running").toBool());
        QVERIFY(QMetaObject::invokeMethod(timer, "triggered"));
        QCOMPARE(searches.size(), 0);
    }
    void bridgeReportsCloseOnce_data() {
        QTest::addColumn<QString>("completion");
        QTest::newRow("synchronous-disconnect-in-fail") << QString("fail('test')");
        QTest::newRow("service-stopped-before-EOF")
            << QString("accept({event:'service_stopped',ok:false})");
        QTest::newRow("quit-rejected-before-EOF")
            << QString("accept({request_id:0,ok:false,error:'cleanup_failed'})");
    }
    void bridgeReportsCloseOnce() {
        QFETCH(QString, completion);
        FakeTransport transport;
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QSignalSpy closed(bridge.data(), SIGNAL(closed(int)));
        evaluate(engine, bridge.data(), "shutdown()");
        evaluate(engine, bridge.data(), completion);
        QCOMPARE(closed.size(), 1);
        QCOMPARE(closed.first().first().toInt(), 1);
        emit transport.unavailable("Disconnected");
        QCOMPARE(closed.size(), 1);
        auto* closeTimer = bridge->findChild<QObject*>("bridgeCloseDeadline");
        auto* requestTimer = bridge->findChild<QObject*>("bridgeRequestDeadline");
        QVERIFY(closeTimer);
        QVERIFY(requestTimer);
        QVERIFY(!closeTimer->property("running").toBool());
        QVERIFY(!requestTimer->property("running").toBool());
    }
    void placeSearchDebouncesAndSelectsIssuedGeneration() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/PlaceSearch.qml"));
        QScopedPointer<QObject> picker(component.create());
        QVERIFY2(picker, qPrintable(component.errorString()));
        QSignalSpy searches(picker.data(), SIGNAL(searchRequested(QVariant)));
        QSignalSpy cancels(picker.data(), SIGNAL(cancelRequested()));
        QSignalSpy selections(picker.data(), SIGNAL(locationRequested(QVariant)));
        auto* query = picker->findChild<QObject*>("placeQuery");
        auto* country = picker->findChild<QObject*>("placeCountry");
        QVERIFY(query);
        QVERIFY(country);
        query->setProperty("text", "Sao");
        QTest::qWait(100);
        query->setProperty("text", QString::fromUtf8("São"));
        country->setProperty("text", "br");
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(), 1, 1200);
        QCOMPARE(searches.first().first().toMap()["query"].toString(), QString::fromUtf8("São"));
        QCOMPARE(searches.first().first().toMap()["country_code"].toString(), QString("BR"));
        QCOMPARE(searches.first().first().toMap()["client_token"].toInt(), 1);
        picker->setProperty("search", QVariantMap{{"generation", 1},
                                                  {"client_token", 1},
                                                  {"status", "loading"},
                                                  {"results", QVariantList{}},
                                                  {"error", QVariant()}});
        picker->setProperty("search",
                            QVariantMap{{"generation", 1},
                                        {"client_token", 1},
                                        {"status", "ready"},
                                        {"results", QVariantList{QVariantMap{
                                                        {"id", 3448439},
                                                        {"name", QString::fromUtf8("São Paulo")},
                                                        {"admin1", QString::fromUtf8("São Paulo")},
                                                        {"country", "Brazil"},
                                                        {"country_code", "BR"}}}},
                                        {"error", QVariant()}});
        QVERIFY(picker->property("showingSearch").toBool());
        QCOMPARE(picker->property("visibleRows").toList().size(), 1);
        evaluate(engine, picker.data(), "pick(visibleRows[0])");
        QCOMPARE(selections.size(), 1);
        auto chosen = selections.first().first().toMap();
        QCOMPARE(chosen["place_id"].toInt(), 3448439);
        QCOMPARE(chosen["search_generation"].toInt(), 1);
        query->setProperty("text", "Tokyo");
        QVERIFY(cancels.size() > 0);
        QVERIFY(!picker->property("showingSearch").toBool());
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(), 2, 1200);
        picker->setProperty("search", QVariantMap{{"generation", 2},
                                                  {"client_token", 2},
                                                  {"status", "error"},
                                                  {"results", QVariantList{}},
                                                  {"error", "offline"}});
        QVERIFY(picker->property("showingSearch").toBool());
        QVERIFY(picker->findChild<QObject*>("placeSearchStatus")
                    ->property("text")
                    .toString()
                    .contains("offline"));
    }
    void supersededQueryCannotLatchOlderGeneration() {
        QQmlEngine engine;
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/PlaceSearch.qml"));
        QScopedPointer<QObject> picker(component.create());
        QVERIFY2(picker, qPrintable(component.errorString()));
        QSignalSpy searches(picker.data(), SIGNAL(searchRequested(QVariant)));
        auto* query = picker->findChild<QObject*>("placeQuery");
        QVERIFY(query);
        query->setProperty("text", "Berlin");
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(), 1, 1200);
        query->setProperty("text", "Tokyo");
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(), 2, 1200);
        QCOMPARE(searches.at(0).first().toMap()["client_token"].toInt(), 1);
        QCOMPARE(searches.at(1).first().toMap()["client_token"].toInt(), 2);
        auto oldRows = QVariantList{QVariantMap{{"id", 2950159},
                                                {"name", "Berlin"},
                                                {"admin1", "Berlin"},
                                                {"country", "Germany"},
                                                {"country_code", "DE"}}};
        picker->setProperty("search", QVariantMap{{"generation", 1},
                                                  {"client_token", 1},
                                                  {"status", "loading"},
                                                  {"results", QVariantList{}},
                                                  {"error", QVariant()}});
        QVERIFY(!picker->property("showingSearch").toBool());
        picker->setProperty("search", QVariantMap{{"generation", 1},
                                                  {"client_token", 1},
                                                  {"status", "ready"},
                                                  {"results", oldRows},
                                                  {"error", QVariant()}});
        QVERIFY(!picker->property("showingSearch").toBool());
        QVERIFY(picker->property("visibleRows").toList().isEmpty());
        picker->setProperty("search", QVariantMap{{"generation", 2},
                                                  {"client_token", 0},
                                                  {"status", "idle"},
                                                  {"results", QVariantList{}},
                                                  {"error", QVariant()}});
        auto newRows = QVariantList{QVariantMap{{"id", 1850147},
                                                {"name", "Tokyo"},
                                                {"admin1", "Tokyo"},
                                                {"country", "Japan"},
                                                {"country_code", "JP"}}};
        picker->setProperty("search", QVariantMap{{"generation", 3},
                                                  {"client_token", 2},
                                                  {"status", "ready"},
                                                  {"results", newRows},
                                                  {"error", QVariant()}});
        QVERIFY(picker->property("showingSearch").toBool());
        QCOMPARE(picker->property("visibleRows").toList().first().toMap()["name"].toString(),
                 QString("Tokyo"));
    }
    void astralLocationAccepted() {
        FakeTransport transport;
        QQmlEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
        QScopedPointer<QObject> bridge(component.createWithInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
        QVERIFY2(bridge, qPrintable(component.errorString()));
        QString astral;
        for (int i = 0; i < 120; i++)
            astral += QString::fromUtf8("🌍");
        auto value = snapshot(1, astral + ", Region, Country");
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
        QVERIFY(!bridge->property("disconnected").toBool());
        QCOMPARE(evaluate(engine, bridge.data(), "snapshot.location").toString(),
                 astral + ", Region, Country");
    }
    void pointFieldValidationAndLegacyNulls() {
        auto accepted = [this](QJsonObject value) {
            FakeTransport transport;
            QQmlEngine engine;
            engine.rootContext()->setContextProperty("weatherTransport", &transport);
            QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
            QScopedPointer<QObject> bridge(component.createWithInitialProperties(
                {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
            if (!bridge)
                return false;
            deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
            return !bridge->property("disconnected").toBool();
        };
        auto valid = metricSnapshot(1);
        QVERIFY(accepted(valid));
        auto old = valid;
        auto current = old["current"].toObject();
        auto hour = old["hourly"].toArray().first().toObject();
        for (auto key : {"uv_index", "pressure_msl_hpa", "dew_point_c"}) {
            current.remove(key);
            hour.remove(key);
        }
        old["current"] = current;
        old["hourly"] = QJsonArray{hour};
        QVERIFY(accepted(old));
        for (auto key : {"uv_index", "pressure_msl_hpa", "dew_point_c"}) {
            current[key] = QJsonValue::Null;
            hour[key] = QJsonValue::Null;
        }
        old["current"] = current;
        old["hourly"] = QJsonArray{hour};
        QVERIFY(accepted(old));
        for (auto bad : QList<QPair<QString, QJsonValue>>{{"uv_index", -0.1},
                                                          {"uv_index", 50.1},
                                                          {"pressure_msl_hpa", 799.9},
                                                          {"pressure_msl_hpa", 1100.1},
                                                          {"dew_point_c", -100.1},
                                                          {"dew_point_c", 60.1},
                                                          {"uv_index", "3.2"}}) {
            auto invalid = valid;
            auto row = invalid["current"].toObject();
            row[bad.first] = bad.second;
            invalid["current"] = row;
            QVERIFY2(!accepted(invalid), qPrintable(bad.first));
            invalid = valid;
            auto hourly = invalid["hourly"].toArray();
            row = hourly.first().toObject();
            row[bad.first] = bad.second;
            hourly.replace(0, row);
            invalid["hourly"] = hourly;
            QVERIFY2(!accepted(invalid), qPrintable(bad.first));
        }
    }
    void airQualityCategoryBoundaries() {
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData(R"(import QtQml
            import "qrc:/ui/qml/AirQuality.js" as AQ
            QtObject { function classify(value, scale) { return AQ.category(value, scale); } })",
                          QUrl("qrc:/tests/aq-category.qml"));
        std::unique_ptr<QObject> helper(component.create());
        QVERIFY2(helper, qPrintable(component.errorString()));
        const auto category = [&](QVariant value, const QString& scale) {
            QVariant result;
            if (!QMetaObject::invokeMethod(helper.get(), "classify", Q_RETURN_ARG(QVariant, result),
                                           Q_ARG(QVariant, value),
                                           Q_ARG(QVariant, QVariant(scale))))
                QTest::qFail("Could not classify AQ index", __FILE__, __LINE__);
            return result.toString();
        };
        struct Example {
            double value;
            const char* us;
            const char* eu;
        };
        for (const auto& row : std::initializer_list<Example>{
                 {0, "Good", "Good"},
                 {20, "Good", "Good"},
                 {20.5, "Good", "Fair"},
                 {40, "Good", "Fair"},
                 {40.5, "Good", "Moderate"},
                 {50.49, "Good", "Moderate"},
                 {50.5, "Moderate", "Moderate"},
                 {60, "Moderate", "Moderate"},
                 {60.5, "Moderate", "Poor"},
                 {80, "Moderate", "Poor"},
                 {80.5, "Moderate", "Very poor"},
                 {100.49, "Moderate", "Very poor"},
                 {100.5, "Unhealthy for sensitive groups", "Extremely poor"},
                 {150, "Unhealthy for sensitive groups", "Extremely poor"},
                 {151, "Unhealthy", "Extremely poor"},
                 {200, "Unhealthy", "Extremely poor"},
                 {201, "Very unhealthy", "Extremely poor"},
                 {300, "Very unhealthy", "Extremely poor"},
                 {301, "Hazardous", "Extremely poor"},
                 {1000, "Hazardous", "Extremely poor"}}) {
            QCOMPARE(category(row.value, "us"), QString::fromLatin1(row.us));
            QCOMPARE(category(row.value, "eu"), QString::fromLatin1(row.eu));
        }
        for (const auto& value :
             QList<QVariant>{QVariant(), QVariant::fromValue(nullptr), -1.0, 1001.0, QString("0")})
            QCOMPARE(category(value, "us"), QString("Unavailable"));
        QCOMPARE(category(0, "pm"), QString("Unavailable"));
    }
    void airQualityContractValidation() {
        auto accepted = [this](QJsonObject value) {
            FakeTransport transport;
            QQmlEngine engine;
            engine.rootContext()->setContextProperty("weatherTransport", &transport);
            QQmlComponent component(&engine, QUrl("qrc:/ui/qml/backend/Bridge.qml"));
            QScopedPointer<QObject> bridge(component.createWithInitialProperties(
                {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")}}));
            if (!bridge)
                return false;
            deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", value}});
            return !bridge->property("disconnected").toBool();
        };
        auto old = snapshot(1, "Legacy backend");
        QVERIFY(accepted(old));
        auto valid = old;
        valid["air_quality"] = airQuality();
        QVERIFY(accepted(valid));
        auto stale = valid;
        auto aq = airQuality(9000);
        aq["freshness"] = "stale";
        aq["offline"] = true;
        aq["error"] = "fetch_failed";
        stale["air_quality"] = aq;
        QVERIFY(accepted(stale));
        for (const auto status : {"expired", "invalid_future"}) {
            auto hidden = valid;
            aq = airQuality();
            aq["freshness"] = status;
            aq["us_aqi"] = QJsonValue::Null;
            aq["european_aqi"] = QJsonValue::Null;
            aq["pm2_5_ug_m3"] = QJsonValue::Null;
            aq["age_seconds"] =
                QString(status) == "invalid_future" ? QJsonValue::Null : QJsonValue(25000);
            hidden["air_quality"] = aq;
            QVERIFY(accepted(hidden));
        }
        auto unavailable = valid;
        aq = airQuality();
        aq["freshness"] = "unavailable";
        for (const auto key : {"valid_at", "fetched_at", "valid_label", "fetched_label",
                               "age_seconds", "us_aqi", "european_aqi", "pm2_5_ug_m3"})
            aq[key] = QJsonValue::Null;
        unavailable["air_quality"] = aq;
        QVERIFY(accepted(unavailable));
        auto rejects = [&](QJsonObject bad) {
            auto candidate = valid;
            candidate["air_quality"] = bad;
            return !accepted(candidate);
        };
        for (const auto key :
             {"freshness", "refreshing", "offline", "error", "domain", "source", "attribution",
              "valid_at", "fetched_at", "valid_label", "fetched_label", "age_seconds", "us_aqi",
              "european_aqi", "pm2_5_ug_m3"}) {
            aq = airQuality();
            aq.remove(key);
            QVERIFY2(rejects(aq), key);
        }
        aq = airQuality();
        aq["extra"] = true;
        QVERIFY(rejects(aq));
        for (auto bad : QList<QPair<QString, QJsonValue>>{{"domain", "cams_europe"},
                                                          {"source", "Station"},
                                                          {"attribution", "Fake"},
                                                          {"freshness", "unknown"},
                                                          {"error", "raw failure"},
                                                          {"us_aqi", -1},
                                                          {"us_aqi", 1001},
                                                          {"european_aqi", 1001},
                                                          {"pm2_5_ug_m3", 5001},
                                                          {"pm2_5_ug_m3", "12"},
                                                          {"age_seconds", -1},
                                                          {"offline", 1}}) {
            aq = airQuality();
            aq[bad.first] = bad.second;
            QVERIFY2(rejects(aq), qPrintable(bad.first));
        }
        aq = airQuality();
        aq["freshness"] = "expired";
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["freshness"] = "invalid_future";
        aq["age_seconds"] = QJsonValue::Null;
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["freshness"] = "unavailable";
        QVERIFY(rejects(aq));
        aq = airQuality(7201);
        QVERIFY(rejects(aq));
        aq = airQuality(7200);
        aq["freshness"] = "stale";
        QVERIFY(rejects(aq));
        aq = airQuality(21601);
        aq["freshness"] = "stale";
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["valid_at"] = "2026-09-27T11:59:00Z";
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["valid_at"] = "2026-09-28T12:26:00Z";
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["valid_label"] = QJsonValue::Null;
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["fetched_label"] = "";
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["valid_label"] = QString(81, 'a');
        QVERIFY(rejects(aq));
        aq = airQuality();
        aq["fetched_label"] = "bad\nlabel";
        QVERIFY(rejects(aq));
    }
    void airQualityLocationTimeLabels() {
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto* times = shellItem(root, "airQualityTimes");
        QVERIFY(times);
        auto show = [&](qint64 revision, const QString& zone, const QString& validLabel,
                        const QString& fetchedLabel) {
            auto state = metricSnapshot(revision), location = state["location"].toObject(),
                 aq = airQuality();
            location["timezone"] = zone;
            state["location"] = location;
            aq["valid_label"] = validLabel;
            aq["fetched_label"] = fetchedLabel;
            state["air_quality"] = aq;
            deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
            root->setProperty("effectsOpen", false);
            QCOMPARE(times->property("text").toString(),
                     QString("Model forecast valid ") + validLabel + " · Fetched " + fetchedLabel);
        };
        show(1, "UTC", "2026-09-28 12:00 UTC (+00:00)", "2026-09-28 12:20 UTC (+00:00)");
        show(2, "Asia/Tokyo", "2026-09-28 21:00 JST (+09:00)", "2026-09-28 21:20 JST (+09:00)");
    }
    void renderAirQualityScreenshots() {
        const QString prefix = qEnvironmentVariable("WEATHER_QT_AQ_SCREENSHOT_PREFIX");
        if (prefix.isEmpty())
            QSKIP("Set WEATHER_QT_AQ_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto* panel = qobject_cast<QQuickItem*>(shellItem(root, "airQualityPanel"));
        QVERIFY(panel);
        std::function<QObject*(QQuickItem*, const char*)> visualNamed =
            [&](QQuickItem* item, const char* name) -> QObject* {
            if (item->objectName() == QLatin1String(name))
                return item;
            for (auto* child : item->childItems())
                if (auto* found = visualNamed(child, name))
                    return found;
            return nullptr;
        };
        auto named = [&](const char* name) -> QObject* {
            auto* found = visualNamed(panel, name);
            if (!found)
                QTest::qFail(qPrintable(QString("Missing AQ label %1").arg(name)), __FILE__,
                             __LINE__);
            return found;
        };
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = qvariant_cast<QQuickItem*>(scroll->property("contentItem"));
        QVERIFY(flick);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        auto show = [&](QJsonObject aq, qint64 revision, int width, const QString& suffix) {
            auto state = metricSnapshot(revision), location = state["location"].toObject();
            location["timezone"] = "Asia/Tokyo";
            state["location"] = location;
            aq["valid_label"] = "2026-09-28 21:00 JST (+09:00)";
            aq["fetched_label"] = "2026-09-28 21:20 JST (+09:00)";
            state["air_quality"] = aq;
            deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", state}});
            root->setProperty("effectsOpen", false);
            window->resize(width, 850);
            QTRY_VERIFY_WITH_TIMEOUT(flick->property("contentHeight").toDouble() > flick->height(),
                                     2000);
            QTest::qWait(100);
            flick->setProperty("contentY", qMax(0.0, flick->property("contentHeight").toDouble() -
                                                         flick->height()));
            QTest::qWait(100);
            auto top = panel->mapToScene(QPointF(0, 0)).y();
            QVERIFY2(top >= 0 && top + panel->height() <= window->height() + 2,
                     qPrintable(
                         QString("AQ panel out of viewport: %1/%2").arg(top).arg(panel->height())));
            QVERIFY(window->grabWindow().save(prefix + suffix + ".png"));
        };
        auto aq = airQuality();
        show(aq, 1, 1200, "-wide-fresh");
        show(aq, 2, 700, "-narrow-fresh");
        auto* us = named("airQualityValue_us");
        QVERIFY(us);
        QCOMPARE(us->property("text").toString(), QString("0"));
        auto* eu = named("airQualityValue_eu");
        QVERIFY(eu);
        QCOMPARE(eu->property("text").toString(), QString("125"));
        QCOMPARE(named("airQualityCategory_us")->property("text").toString(), QString("Good"));
        QCOMPARE(named("airQualityCategory_eu")->property("text").toString(),
                 QString("Extremely poor"));
        aq["us_aqi"] = 125;
        show(aq, 3, 700, "-narrow-sensitive-groups");
        QCOMPARE(named("airQualityCategory_us")->property("text").toString(),
                 QString("Unhealthy for sensitive groups"));
        aq = airQuality(9000);
        aq["freshness"] = "stale";
        aq["offline"] = true;
        aq["error"] = "fetch_failed";
        show(aq, 4, 700, "-narrow-stale-offline");
        aq["freshness"] = "expired";
        aq["age_seconds"] = 25000;
        for (auto key : {"us_aqi", "european_aqi", "pm2_5_ug_m3"})
            aq[key] = QJsonValue::Null;
        show(aq, 5, 700, "-narrow-expired");
        us = named("airQualityValue_us");
        QVERIFY(us);
        QCOMPARE(us->property("text").toString(), QString("—"));
        QCOMPARE(named("airQualityCategory_us")->property("text").toString(),
                 QString("Unavailable"));
        auto unavailable = metricSnapshot(6);
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", unavailable}});
        us = named("airQualityValue_us");
        QVERIFY(us);
        QTRY_COMPARE(us->property("text").toString(), QString("—"));
        auto* status = named("airQualityStatus");
        QVERIFY(status);
        QVERIFY(status->property("text").toString().contains("unavailable"));
    }
    void renderPointMetricsScreenshots() {
        const QString prefix = qEnvironmentVariable("WEATHER_QT_METRICS_SCREENSHOT_PREFIX");
        if (prefix.isEmpty())
            QSKIP("Set WEATHER_QT_METRICS_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        deliver(transport,
                {{"version", 1}, {"event", "snapshot"}, {"snapshot", metricSnapshot(1)}});
        root->setProperty("effectsOpen", false);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        auto* scroll = root->findChild<QObject*>("forecastScroll");
        QVERIFY(scroll);
        auto* flick = qvariant_cast<QQuickItem*>(scroll->property("contentItem"));
        QVERIFY(flick);
        auto toBottom = [flick]() {
            flick->setProperty("contentY", qMax(0.0, flick->property("contentHeight").toDouble() -
                                                         flick->height()));
        };
        window->resize(1200, 850);
        QTest::qWait(120);
        toBottom();
        QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix + "-wide-current.png"));
        window->resize(700, 850);
        QTest::qWait(120);
        toBottom();
        QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix + "-narrow-current.png"));
        QQmlExpression openDetail(QQmlEngine::contextForObject(root), root,
                                  "details.showHour(root.hours[0])");
        openDetail.evaluate();
        QVERIFY2(!openDetail.hasError(), qPrintable(openDetail.error().toString()));
        auto* details = root->findChild<QObject*>("forecastDetails");
        QVERIFY(details);
        QTRY_VERIFY(details->property("visible").toBool());
        auto* detailsScroll = root->findChild<QObject*>("forecastDetailsScroll");
        QVERIFY(detailsScroll);
        auto* detailsFlick = qvariant_cast<QQuickItem*>(detailsScroll->property("contentItem"));
        QVERIFY(detailsFlick);
        QTRY_VERIFY_WITH_TIMEOUT(
            detailsFlick->property("contentHeight").toDouble() > detailsFlick->height(), 2000);
        detailsFlick->setProperty(
            "contentY",
            qMax(0.0, detailsFlick->property("contentHeight").toDouble() - detailsFlick->height()));
        QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix + "-narrow-hour.png"));
        QQmlExpression closeDetail(QQmlEngine::contextForObject(root), root, "details.close()");
        closeDetail.evaluate();
        QVERIFY2(!closeDetail.hasError(), qPrintable(closeDetail.error().toString()));
        auto unavailable = metricSnapshot(2);
        auto current = unavailable["current"].toObject();
        auto hourly = unavailable["hourly"].toArray();
        auto hour = hourly.first().toObject();
        for (auto key : {"uv_index", "pressure_msl_hpa", "dew_point_c"}) {
            current[key] = QJsonValue::Null;
            hour[key] = QJsonValue::Null;
        }
        unavailable["current"] = current;
        hourly.replace(0, hour);
        unavailable["hourly"] = hourly;
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", unavailable}});
        toBottom();
        QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix + "-narrow-unavailable.png"));
    }
    void renderSearchScreenshots() {
        const QString prefix = qEnvironmentVariable("WEATHER_QT_SCREENSHOT_PREFIX");
        if (prefix.isEmpty())
            QSKIP("Set WEATHER_QT_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;
        QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport", &transport);
        engine.setInitialProperties(
            {{"weatherTransport", engine.rootContext()->contextProperty("weatherTransport")},
             {"mapTiles", engine.rootContext()->contextProperty("mapTiles").isValid()
                              ? engine.rootContext()->contextProperty("mapTiles")
                              : QVariant::fromValue<QObject*>(nullptr)}});
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        QQuickWindow* window = nullptr;
        for (auto* candidate : QGuiApplication::allWindows())
            if (candidate->objectName() == "weatherWindow")
                window = qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);
        QTRY_VERIFY(window->isExposed());
        auto base = snapshot(1, "Berlin, Berlin, Germany");
        base["alerts"] = QJsonObject{{"status", "not_supported_here"},
                                     {"source", QJsonValue::Null},
                                     {"coverage", "unsupported"},
                                     {"fetched_at", QJsonValue::Null},
                                     {"items", QJsonArray{}}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", base}});
        root->setProperty("effectsOpen", true);
        auto* query = root->findChild<QObject*>("placeQuery");
        QVERIFY(query);
        query->setProperty("text", "Berlin");
        QTRY_VERIFY_WITH_TIMEOUT(!transport.requests.isEmpty(), 1200);
        auto loading = base;
        loading["snapshot_revision"] = 2;
        loading["place_search"] = QJsonObject{{"generation", 1},
                                              {"client_token", 1},
                                              {"status", "loading"},
                                              {"results", QJsonArray{}},
                                              {"error", QJsonValue::Null}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", loading}});
        auto ready = loading;
        ready["snapshot_revision"] = 3;
        ready["place_search"] =
            QJsonObject{{"generation", 1},
                        {"client_token", 1},
                        {"status", "ready"},
                        {"results", QJsonArray{QJsonObject{{"id", 2950159},
                                                           {"name", "Berlin"},
                                                           {"admin1", "Berlin"},
                                                           {"country", "Germany"},
                                                           {"country_code", "DE"}},
                                               QJsonObject{{"id", 2988507},
                                                           {"name", "Paris"},
                                                           {"admin1", "Île-de-France"},
                                                           {"country", "France"},
                                                           {"country_code", "FR"}}}},
                        {"error", QJsonValue::Null}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", ready}});
        QTest::qWait(120);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        QVERIFY(window->grabWindow().save(prefix + "-results.png"));
        query->setProperty("text", "Tokyo");
        QTest::qWait(450);
        auto error = ready;
        error["snapshot_revision"] = 4;
        error["place_search"] = QJsonObject{{"generation", 2},
                                            {"client_token", 2},
                                            {"status", "error"},
                                            {"results", QJsonArray{}},
                                            {"error", "offline"}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", error}});
        QTest::qWait(120);
        QVERIFY(window->grabWindow().save(prefix + "-error.png"));
        root->setProperty("effectsOpen", false);
        QTest::qWait(120);
        QVERIFY(window->grabWindow().save(prefix + "-coverage.png"));
        root->setProperty("effectsOpen", true);
        auto* drawer = root->findChild<QObject*>("effectsDrawer");
        QVERIFY(drawer);
        QSignalSpy selections(drawer, SIGNAL(locationRequested(QVariant)));
        query->setProperty("text", "Paris");
        QTest::qWait(450);
        auto keyboard = ready;
        keyboard["snapshot_revision"] = 5;
        keyboard["place_search"] =
            QJsonObject{{"generation", 3},
                        {"client_token", 3},
                        {"status", "ready"},
                        {"results", QJsonArray{QJsonObject{{"id", 2988507},
                                                           {"name", "Paris"},
                                                           {"admin1", "Île-de-France"},
                                                           {"country", "France"},
                                                           {"country_code", "FR"}}}},
                        {"error", QJsonValue::Null}};
        deliver(transport, {{"version", 1}, {"event", "snapshot"}, {"snapshot", keyboard}});
        auto* queryItem = qobject_cast<QQuickItem*>(query);
        QVERIFY(queryItem);
        queryItem->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Down);
        auto* results = root->findChild<QObject*>("placeResults");
        QVERIFY(results);
        QCOMPARE(results->property("currentIndex").toInt(), 0);
        QVERIFY(results->property("activeFocus").toBool());
        QVERIFY(window->grabWindow().save(prefix + "-keyboard.png"));
        QTest::keyClick(window, Qt::Key_Return);
        QCOMPARE(selections.size(), 1);
        QCOMPARE(selections.first().first().toMap()["place_id"].toInt(), 2988507);
        QCOMPARE(selections.first().first().toMap()["search_generation"].toInt(), 3);
    }
};
QTEST_MAIN(FrontendTest)
#include "frontend_test.moc"
