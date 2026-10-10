#include <QChar>
#include <QBitArray>
#include <QtTest>
#include "radarimages.h"
#include <QLocalServer>
#include <QLocalSocket>
#include <QTemporaryDir>
#include <QBuffer>
#include <QJsonDocument>
#include <future>
#include "transport.h"
#include "radarservicefixture.h"
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQmlExpression>
#include <QQuickWindow>
#include <QQuickItem>
#include <QSGRendererInterface>
#include "maptiles.h"
#include <QFile>
#include <QDir>
#include <unistd.h>

// Dashboard loaders own their objects outside the root QObject tree; inspect
// the actual visual scene, as the frontend interaction tests do.
static QQuickItem* sceneItem(QQuickItem* item, const QString& name) {
    if (!item)
        return nullptr;
    if (item->objectName() == name)
        return item;
    for (auto* child : item->childItems())
        if (auto* found = sceneItem(child, name))
            return found;
    return nullptr;
}
static QQuickItem* shellItem(QObject* root, const QString& name) {
    auto* window = qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
    return window ? sceneItem(window->contentItem(), name) : nullptr;
}

class ImageSocketFixture {
  public:
    QTemporaryDir directory{"/tmp/radar-image-XXXXXX"};
    QLocalServer server;
    QByteArray body;
    QString mode;
    int requests = 0;
    bool hold = false;
    ImageSocketFixture() {
        QObject::connect(&server, &QLocalServer::newConnection, &server, [this] {
            while (server.hasPendingConnections()) {
                auto* socket = server.nextPendingConnection();
                QObject::connect(socket, &QLocalSocket::readyRead, socket, [this, socket] {
                    while (socket->canReadLine()) {
                        const auto request = QJsonDocument::fromJson(socket->readLine()).object();
                        ++requests;
                        if (hold)
                            continue;
                        const auto image = request.value("image").toObject();
                        const int offset = image.value("offset").toInt();
                        auto part = body.mid(offset, 96 * 1024);
                        QJsonObject responseImage{{"id", image.value("id")},
                                                  {"offset", offset},
                                                  {"total", body.size()},
                                                  {"data", QString::fromLatin1(part.toBase64())}};
                        if (mode == "wrong_id")
                            responseImage["id"] = QString(64, 'b');
                        if (mode == "offset")
                            responseImage["offset"] = offset + 1;
                        if (mode == "total")
                            responseImage["total"] = 2 * 1024 * 1024 + 1;
                        if (mode == "changed_total" && offset > 0)
                            responseImage["total"] = body.size() - 1;
                        if (mode == "base64")
                            responseImage["data"] = "%%%";
                        if (mode == "short_chunk")
                            responseImage["data"] = "YQ==";
                        QJsonObject reply{{"version", 1},
                                          {"request_id", request.value("request_id")},
                                          {"ok", true},
                                          {"image", responseImage}};
                        if (mode == "request_id")
                            reply["request_id"] = 99;
                        if (mode == "failed") {
                            reply["ok"] = false;
                            reply["error"] = "radar_image_unavailable";
                        }
                        socket->write(mode == "oversized"
                                          ? QByteArray(262145, 'x') + '\n'
                                          : QJsonDocument(reply).toJson(QJsonDocument::Compact) +
                                                '\n');
                    }
                });
            }
        });
    }
    QString path() const {
        return directory.path() + "/app.sock";
    }
    bool start() {
        return directory.isValid() && server.listen(path());
    }
};
class RadarImagesTest : public QObject {
    Q_OBJECT
    const QString key = QString(64, 'a');
    QByteArray png(int width = 512, int height = 512, bool metadata = false) {
        QImage image(width, height, QImage::Format_ARGB32);
        quint32 random = 17;
        for (int y = 0; y < height; ++y)
            for (int x = 0; x < width; ++x) {
                random ^= random << 13;
                random ^= random >> 17;
                random ^= random << 5;
                image.setPixel(x, y, 0xff000000U | (random & 0xffffffU));
            }
        if (metadata)
            image.setText("unsafe", QString(10000, 'x'));
        QByteArray bytes;
        QBuffer buffer(&bytes);
        buffer.open(QIODevice::WriteOnly);
        image.save(&buffer, "PNG");
        return bytes;
    }
    std::future<QImage> fetch(RadarImageProvider& provider, const QString& kind = "frame") {
        return std::async(std::launch::async, [&provider, request = kind + "/" + key + "/0"] {
            return provider.requestImage(request, nullptr, {});
        });
    }
  private slots:
    void optionalResourceAndCaptureProbe() {
        const auto directory = qEnvironmentVariable("WEATHER_RADAR_PROBE_DIR");
        if (directory.isEmpty())
            QSKIP("Opt-in sequential radar resource/capture probe");
        QTest::failOnWarning(
            QRegularExpression(".*(TypeError|ReferenceError|Binding loop|Unable to assign).*"));
        const bool live = qEnvironmentVariableIsSet("WEATHER_RADAR_LIVE");
        QVERIFY(QDir().mkpath(directory));
        RadarServiceFixture fixture;
        QVERIFY2(fixture.start(), fixture.diagnostics.constData());
        const auto pid = QString::number(fixture.command().value("pid").toInt());
        auto pss = [](const QString& pid) {
            QFile f("/proc/" + pid + "/smaps_rollup");
            if (!f.open(QIODevice::ReadOnly))
                return qint64(-1);
            for (const auto& line : f.readAll().split('\n'))
                if (line.startsWith("Pss:"))
                    return line.mid(4).trimmed().split(' ').first().toLongLong();
            return qint64(-1);
        };
        auto ticks = [](const QString& pid) {
            QFile f("/proc/" + pid + "/stat");
            if (!f.open(QIODevice::ReadOnly))
                return qint64(-1);
            const auto b = f.readAll();
            const auto v = b.mid(b.lastIndexOf(')') + 2).split(' ');
            return v.value(11).toLongLong() + v.value(12).toLongLong();
        };
        RadarImageControl control;
        WeatherTransport transport(fixture.socket, false);
        std::unique_ptr<MapTiles> tiles;
        if (live)
            tiles = std::make_unique<MapTiles>();
        QQmlApplicationEngine engine;
        engine.addImageProvider("radar", new RadarImageProvider(fixture.socket, control.state()));
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"radarImages", QVariant::fromValue<QObject*>(&control)},
             {"mapTiles", QVariant::fromValue<QObject*>(tiles.get())}});
        QObject::disconnect(&engine, nullptr, QCoreApplication::instance(), nullptr);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        auto eval = [&](QObject* object, const QString& source) {
            if (!object)
                return QVariant();
            QQmlExpression e(qmlContext(object), object, source);
            auto v = e.evaluate();
            if (e.hasError())
                qFatal("%s", qPrintable(e.error().toString()));
            return v;
        };
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        window->resize(1200, 850);
        QSignalSpy swaps(window, &QQuickWindow::frameSwapped);
        QTRY_VERIFY(eval(root, "backend.available && backend.pending < 0").toBool());
        QJsonArray results;
        const auto measure = [&](const QString& phase) {
            QTest::qWait(250);
            swaps.clear();
            const auto uiStart = ticks("self"), goStart = ticks(pid);
            QElapsedTimer timer;
            timer.start();
            QTest::qWait(1500);
            const auto seconds = timer.elapsed() / 1000.0;
            const QJsonObject result{{"phase", phase},
                                     {"ui_pss_kib", pss("self")},
                                     {"service_pss_kib", pss(pid)},
                                     {"ui_cpu_percent", 100.0 * (ticks("self") - uiStart) /
                                                            sysconf(_SC_CLK_TCK) / seconds},
                                     {"service_cpu_percent", 100.0 * (ticks(pid) - goStart) /
                                                                 sysconf(_SC_CLK_TCK) / seconds},
                                     {"swaps", swaps.size()},
                                     {"provider_calls", fixture.command().value("calls")},
                                     {"radar_objects", (shellItem(root, "radarMap") ? 1 : 0)}};
            results.append(result);
            qInfo().noquote() << "RADAR_RESOURCE"
                              << QJsonDocument(result).toJson(QJsonDocument::Compact);
        };
        measure("never_opened");
        QCOMPARE(fixture.command().value("calls").toInt(), 0);
        QTRY_VERIFY(shellItem(root, "weatherMaps"));
        auto* panel = shellItem(root, "weatherMaps");
        auto* flick =
            root->findChild<QObject*>("forecastScroll")->property("contentItem").value<QObject*>();
        QVERIFY(panel && flick);
        panel->findChild<QObject*>("mapLayerTabs")->setProperty("currentIndex", 0);
        flick->setProperty("contentY", root->property("mapContentTop").toReal());
        QTRY_VERIFY(shellItem(root, "radarMap"));
        QPointer<QObject> radar = shellItem(root, "radarMap");
        QTRY_VERIFY_WITH_TIMEOUT(eval(radar, "displayedFrame !== null").toBool(), 25000);
        QTRY_VERIFY_WITH_TIMEOUT(
            eval(radar, "readyFrames.length > 0 && radarState.legend !== ''").toBool(), 40000);
        QVERIFY(!radar->property("playing").toBool());
        QVERIFY(!radar->property("historyWanted").toBool());
        measure("radar_static");
        QVERIFY(window->grabWindow().save(directory + "/radar-1200.png"));
        window->resize(700, 650);
        QTest::qWait(300);
        flick->setProperty("contentY", root->property("mapContentTop").toReal());
        QTest::qWait(200);
        QVERIFY(window->grabWindow().save(directory + "/radar-700.png"));
        window->resize(1200, 850);
        QTest::qWait(200);
        radar->setProperty("visualQuality", "full");
        eval(radar, "followLatest=false; playing=true");
        QTRY_VERIFY_WITH_TIMEOUT(
            eval(radar, "radarState.frames.every(f=>f.state!=='pending')").toBool(), 40000);
        measure("radar_playing");
        window->hide();
        QTRY_VERIFY(radar.isNull());
        measure("hidden");
        for (int i = 0; i < 40; ++i) {
            window->show();
            QTest::qWait(40);
            flick->setProperty("contentY", root->property("mapContentTop").toReal());
            QTRY_VERIFY(shellItem(root, "radarMap"));
            radar = shellItem(root, "radarMap");
            QTRY_VERIFY_WITH_TIMEOUT(eval(radar, "displayedFrame !== null").toBool(), 3000);
            window->hide();
            QTRY_VERIFY(radar.isNull());
            QTest::qWait(40);
            if (i == 19 || i == 39)
                measure(QString("hidden_%1").arg(i + 1));
        }
        const auto renderer = int(window->rendererInterface()->graphicsApi());
        QFile file(directory + "/results.json");
        QVERIFY(file.open(QIODevice::WriteOnly));
        file.write(QJsonDocument(QJsonObject{{"live", live},
                                             {"renderer", renderer},
                                             {"qt", qVersion()},
                                             {"measurements", results}})
                       .toJson());
    }
    void realServiceFramePlaybackAndClose() {
        if (qEnvironmentVariable("GO_RADAR_FIXTURE").isEmpty())
            QSKIP("Private Go fixture required");
        RadarServiceFixture fixture;
        QVERIFY2(fixture.start(), fixture.diagnostics.constData());
        RadarImageControl control;
        WeatherTransport transport(fixture.socket, true);
        QQmlApplicationEngine engine;
        engine.addImageProvider("radar", new RadarImageProvider(fixture.socket, control.state()));
        engine.setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(&transport)},
             {"radarImages", QVariant::fromValue<QObject*>(&control)}});
        QObject::disconnect(&engine, nullptr, QCoreApplication::instance(), nullptr);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));
        QCOMPARE(engine.rootObjects().size(), 1);
        auto* root = engine.rootObjects().first();
        const auto eval = [&](QObject* object, const QString& source) {
            if (!object)
                return QVariant();
            QQmlExpression e(qmlContext(object), object, source);
            const auto value = e.evaluate();
            if (e.hasError())
                qFatal("%s", qPrintable(e.error().toString()));
            return value;
        };
        QTRY_VERIFY(eval(root, "backend.available && backend.pending < 0").toBool());
        QCOMPARE(fixture.command().value("calls").toInt(), 0);
        QTRY_VERIFY(shellItem(root, "weatherMaps"));
        auto* panel = shellItem(root, "weatherMaps");
        QVERIFY(panel);
        panel->findChild<QObject*>("mapLayerTabs")->setProperty("currentIndex", 0);
        auto* flick =
            root->findChild<QObject*>("forecastScroll")->property("contentItem").value<QObject*>();
        QVERIFY(flick);
        QTest::qWait(100);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 40);
        QTRY_VERIFY(shellItem(root, "radarMap"));
        QPointer<QObject> radar = shellItem(root, "radarMap");
        QTRY_VERIFY_WITH_TIMEOUT(eval(radar, "displayedFrame !== null").toBool(), 5000);
        QTRY_COMPARE_WITH_TIMEOUT(eval(radar, "frames.length").toInt(), 3, 5000);
        QTRY_VERIFY(eval(radar, "radarState.legend !== ''").toBool());
        QCOMPARE(eval(radar, "readyFrames.length").toInt(), 1);
        QVERIFY(!radar->property("playing").toBool());
        QVERIFY(!radar->property("historyWanted").toBool());
        QVERIFY(!radar->findChild<QObject*>("radarPlaybackTimer")->property("running").toBool());
        QCOMPARE(fixture.command().value("calls").toInt(), 3); // metadata, latest frame, legend
        QVERIFY(!eval(root, "backend.disconnected").toBool());
        const auto latest = eval(radar, "displayedFrame.id").toString();
        QCOMPARE(eval(radar, "displayedFrame.time === frames[frames.length-1].time").toBool(),
                 true);
        QTest::qWait(800); // Opening and waiting must neither animate nor download history.
        QCOMPARE(eval(radar, "displayedFrame.id").toString(), latest);
        QCOMPARE(fixture.command().value("calls").toInt(), 3);
        auto* window =
            qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        QVERIFY(window);
        radar->setProperty("visualQuality", "full");
        auto* play = shellItem(root, "radarPlay");
        QVERIFY(play);
        QTRY_VERIFY(play->isEnabled());
        const auto clickPlay = [&] {
            QTest::mouseClick(
                window, Qt::LeftButton, Qt::NoModifier,
                play->mapToScene(QPointF(play->width() / 2, play->height() / 2)).toPoint());
        };
        clickPlay();
        QTRY_VERIFY(radar->property("playing").toBool());
        QTRY_COMPARE_WITH_TIMEOUT(eval(radar, "readyFrames.length").toInt(), 3, 5000);
        QCOMPARE(fixture.command().value("calls").toInt(), 5); // Explicit Play loads history.
        QTRY_VERIFY(eval(radar, "displayedFrame.id !== '" + latest + "'").toBool());
        clickPlay();
        QTRY_VERIFY(!radar->property("playing").toBool());
        eval(radar, "select(0)");
        QTRY_VERIFY(eval(radar, "displayedFrame.id !== '" + latest + "'").toBool());
        eval(radar, "latest()");
        QTRY_COMPARE(eval(radar, "displayedFrame.id").toString(), latest);
        QCOMPARE(fixture.command().value("calls").toInt(), 5); // metadata, 3 frames, legend
        window->hide();
        QTRY_VERIFY(radar.isNull());
        QTRY_VERIFY(eval(root, "backend.pending < 0 && !backend.radarWanted").toBool());
        QVERIFY(!control.state()->active.load());
        QTest::qWait(600);
        QCOMPARE(fixture.command().value("calls").toInt(), 5);
        window->show();
        QTest::qWait(100); // Showing the app intentionally returns to the forecast top.
        QVERIFY(!shellItem(root, "radarMap"));
        QCOMPARE(fixture.command().value("calls").toInt(), 5);
        flick->setProperty("contentY", root->property("mapContentTop").toReal() + 40);
        QTRY_VERIFY(shellItem(root, "radarMap"));
        radar = shellItem(root, "radarMap");
        QTRY_VERIFY(eval(radar, "displayedFrame !== null").toBool());
        QVERIFY(!radar->property("playing").toBool());
        QVERIFY(!radar->property("historyWanted").toBool());
        QCOMPARE(fixture.command().value("calls").toInt(), 5); // Reopen reuses encoded cache.
        fixture.command({{"Op", "advance"}, {"Seconds", 1900}});
        QTRY_COMPARE(eval(radar, "radarState.status").toString(), QString("unavailable"));
        QTRY_VERIFY(eval(radar, "displayedFrame === null").toBool());
    }
    void chunksDecodeAndInactiveDemand() {
        ImageSocketFixture fixture;
        fixture.body = png();
        QVERIFY(fixture.start());
        QVERIFY(fixture.body.size() > 96 * 1024);
        RadarImageControl control;
        RadarImageProvider provider(fixture.path(), control.state());
        QVERIFY(provider.requestImage("frame/" + key + "/0", nullptr, {}).isNull());
        QCOMPARE(fixture.requests, 0);
        control.setActive(true);
        auto future = fetch(provider);
        QTRY_VERIFY(future.wait_for(std::chrono::milliseconds(0)) == std::future_status::ready);
        const auto image = future.get();
        QVERIFY(!image.isNull());
        QCOMPARE(image.size(), QSize(512, 512));
        QCOMPARE(fixture.requests, (fixture.body.size() + 96 * 1024 - 1) / (96 * 1024));
        control.setActive(false);
        const auto before = fixture.requests;
        QVERIFY(provider.requestImage("frame/" + key + "/0", nullptr, {}).isNull());
        QCOMPARE(fixture.requests, before);
    }
    void legend() {
        ImageSocketFixture fixture;
        fixture.body = png(500, 30);
        QVERIFY(fixture.start());
        RadarImageControl control;
        control.setActive(true);
        RadarImageProvider provider(fixture.path(), control.state());
        auto future = fetch(provider, "legend");
        QTRY_VERIFY(future.wait_for(std::chrono::milliseconds(0)) == std::future_status::ready);
        QCOMPARE(future.get().size(), QSize(500, 30));
    }
    void reject_data() {
        QTest::addColumn<QString>("mode");
        for (const auto* mode :
             {"wrong_id", "offset", "total", "changed_total", "base64", "short_chunk", "request_id",
              "failed", "oversized", "metadata", "dimensions", "corrupt"})
            QTest::newRow(mode) << QString(mode);
    }
    void reject() {
        QFETCH(QString, mode);
        ImageSocketFixture fixture;
        fixture.mode = mode;
        fixture.body = png(mode == "dimensions" ? 513 : 512, 512, mode == "metadata");
        if (mode == "corrupt")
            fixture.body[fixture.body.size() / 2] ^= 1;
        QVERIFY(fixture.start());
        RadarImageControl control;
        control.setActive(true);
        RadarImageProvider provider(fixture.path(), control.state());
        auto future = fetch(provider);
        QTRY_VERIFY(future.wait_for(std::chrono::milliseconds(0)) == std::future_status::ready);
        QVERIFY(future.get().isNull());
    }
    void cancellationAndDeadline() {
        ImageSocketFixture fixture;
        fixture.hold = true;
        QVERIFY(fixture.start());
        RadarImageControl control;
        control.setActive(true);
        RadarImageProvider provider(fixture.path(), control.state());
        auto future = fetch(provider);
        QTRY_COMPARE(fixture.requests, 1);
        QElapsedTimer elapsed;
        elapsed.start();
        control.invalidate();
        QTRY_VERIFY_WITH_TIMEOUT(
            future.wait_for(std::chrono::milliseconds(0)) == std::future_status::ready, 500);
        QVERIFY(future.get().isNull());
        QVERIFY(elapsed.elapsed() < 500);
        future = fetch(provider);
        elapsed.restart();
        QTRY_VERIFY_WITH_TIMEOUT(
            future.wait_for(std::chrono::milliseconds(0)) == std::future_status::ready, 3000);
        QVERIFY(future.get().isNull());
        QVERIFY(elapsed.elapsed() < 3000);
    }
    void invalidSourceNeverConnects() {
        ImageSocketFixture fixture;
        QVERIFY(fixture.start());
        RadarImageControl control;
        control.setActive(true);
        RadarImageProvider provider(fixture.path(), control.state());
        for (const auto& source : {QString("file/" + key + "/0"), QString("frame/../0"),
                                   QString("frame/" + key + "/bad"), QString(101, 'a')})
            QVERIFY(provider.requestImage(source, nullptr, {}).isNull());
        QCOMPARE(fixture.requests, 0);
    }
};
QTEST_MAIN(RadarImagesTest)
#include "radarimages_test.moc"
