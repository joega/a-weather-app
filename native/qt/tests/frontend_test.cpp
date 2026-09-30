#include <QtTest>
#include <QQmlEngine>
#include <QQmlContext>
#include <QQmlComponent>
#include <QQmlExpression>
#include <QQmlApplicationEngine>
#include <QWindow>
#include <QQuickWindow>
#include <QQuickItem>
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
#include "maptiles.h"
#include <functional>

class FakeTransport:public QObject {
    Q_OBJECT
    Q_PROPERTY(bool connected MEMBER connected CONSTANT)
    Q_PROPERTY(bool diagnostic MEMBER diagnostic CONSTANT)
public:
    bool connected=true,diagnostic=false;
    QList<QVariantMap> requests;
    Q_INVOKABLE void start(){}
    Q_INVOKABLE bool send(const QVariantMap &request){requests.append(request);return true;}
    Q_INVOKABLE void disconnectService(){emit unavailable("Disconnected");}
signals:
    void ready();
    void message(const QString &json);
    void unavailable(const QString &error);
    void shutdownRequested();
};
class FakeMapTiles:public QObject {
    Q_OBJECT
public:
    int requests=0,closes=0;
    QSet<QString> active;
    Q_INVOKABLE void request(int z,int x,int y,bool){auto key=QString("%1/%2/%3").arg(z).arg(x).arg(y);if(!active.contains(key)){active.insert(key);++requests;}}
    Q_INVOKABLE void close(){++closes;active.clear();}
signals:
    void tileReady(const QString &key,const QString &dataURL);
    void tileFailed(const QString &key,const QString &reason);
};

class FrontendTest:public QObject {
    Q_OBJECT
    QJsonObject snapshot(qint64 revision,const QString &name){
        const auto raw=QByteArray(R"({"schema_version":1,"location":{"name":"Current location","timezone":"UTC"},"current":null,"hourly":[],"daily":[],"alerts":{"status":"unavailable","items":[]},"source":{"name":"Open-Meteo","attribution":"Open-Meteo","freshness":"unavailable","age_seconds":null,"refreshing":false,"error":null},"controls":{"units":"F","mode":"live","strength":"normal","fps":30,"manual":{"condition":"rain"},"reduced_motion":false,"lightning_enabled":false,"window_physics":true,"accumulation":true,"pause_fullscreen":true},"atmosphere":{"rain_intensity":0,"snow_intensity":0,"cloud_cover":0.5,"fog_density":0,"sun_elevation":0,"sun_azimuth":180,"wind_x":0,"reduced_motion":false,"lightning_enabled":false,"thunderstorm":false},"effect_status":{"state":"stopped","remaining_seconds":0,"persistent":false}})");
        auto v=QJsonDocument::fromJson(raw).object();v["snapshot_revision"]=revision;
        auto location=v["location"].toObject();location["name"]=name;v["location"]=location;return v;
    }
    QJsonObject selectedSnapshot(qint64 revision,const QString &name){
        auto value=snapshot(revision,name);
        value["location_settings"]=QJsonObject{{"mode","custom"},{"zip_code",QJsonValue::Null},{"busy",false},{"error",QJsonValue::Null},{"country_code","US"},{"place",QJsonValue::Null}};
        return value;
    }
    QJsonObject metricSnapshot(qint64 revision){
        auto v=snapshot(revision,"Metric fixture");
        auto source=v["source"].toObject();source["fetched_at"]="2026-09-28T12:00:00Z";source["freshness"]="stale";v["source"]=source;
        QJsonObject current{{"time","2026-09-28T12:00:00Z"},{"condition","clear"},{"is_day",true},{"temperature_c",15},{"apparent_temperature_c",14},{"humidity",0.65},{"wind_speed_m_s",2},{"wind_gust_m_s",4},{"wind_direction_deg",90},{"visibility_m",10000},{"uv_index",0},{"pressure_msl_hpa",1013.2},{"dew_point_c",12.5}};
        auto hour=current;hour["local_hour"]="12 PM";hour["time"]="2026-09-28T13:00:00Z";hour["uv_index"]=3.2;
        v["current"]=current;v["hourly"]=QJsonArray{hour};return v;
    }
    QJsonObject airQuality(qint64 age=3600){
        return {{"freshness","fresh"},{"refreshing",false},{"offline",false},{"error",QJsonValue::Null},
                {"domain","cams_global"},{"source","CAMS global model data"},{"attribution","CAMS global model data via Open-Meteo (CC BY 4.0)"},
                {"valid_at","2026-09-28T12:00:00Z"},{"fetched_at","2026-09-28T12:20:00Z"},
                {"valid_label","2026-09-28 12:00 UTC (+00:00)"},{"fetched_label","2026-09-28 12:20 UTC (+00:00)"},{"age_seconds",age},
                {"us_aqi",0},{"european_aqi",125},{"pm2_5_ug_m3",12.4}};
    }
    void deliver(FakeTransport &transport,const QJsonObject &v){emit transport.message(QString::fromUtf8(QJsonDocument(v).toJson(QJsonDocument::Compact)));}
    QVariant evaluate(QQmlEngine &engine,QObject *bridge,const QString &expression){QQmlExpression e(engine.rootContext(),bridge,expression);auto v=e.evaluate();if(e.hasError())qFatal("%s",qPrintable(e.error().toString()));return v;}
private slots:
    void mapTileInvalidPNGs_data(){
        QTest::addColumn<QByteArray>("payload");
        QImage oversized(4096,4096,QImage::Format_ARGB32);oversized.fill(Qt::blue);
        QByteArray png;QBuffer buffer(&png);QVERIFY(buffer.open(QIODevice::WriteOnly));QVERIFY(oversized.save(&buffer,"PNG"));
        QVERIFY(png.size()<128*1024);
        QTest::newRow("large compressed image")<<png;
        QTest::newRow("truncated header")<<png.left(20);
        QTest::newRow("malformed payload")<<QByteArray("not a PNG");
        QImage normal(256,256,QImage::Format_ARGB32);normal.fill(Qt::blue);
        QByteArray normalPNG;QBuffer normalBuffer(&normalPNG);QVERIFY(normalBuffer.open(QIODevice::WriteOnly));QVERIFY(normal.save(&normalBuffer,"PNG"));
        QTest::newRow("truncated pixel data")<<normalPNG.left(70);
        QByteArray other;QBuffer otherBuffer(&other);QVERIFY(otherBuffer.open(QIODevice::WriteOnly));QVERIFY(normal.save(&otherBuffer,"BMP"));
        QTest::newRow("non PNG")<<other.left(128*1024);
    }
    void mapTileInvalidPNGs(){
        QFETCH(QByteArray,payload);
        QTcpServer server;QVERIFY(server.listen(QHostAddress::LocalHost));
        connect(&server,&QTcpServer::newConnection,&server,[&]{
            auto *socket=server.nextPendingConnection();
            connect(socket,&QTcpSocket::readyRead,socket,[socket,&payload]{
                socket->readAll();socket->write("HTTP/1.1 200 OK\r\nContent-Length: "+QByteArray::number(payload.size())+"\r\n\r\n"+payload);
            });
        });
        MapTiles tiles(nullptr,QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(&tiles,&MapTiles::tileReady),failed(&tiles,&MapTiles::tileFailed);
        tiles.request(0,0,0,false);
        QTRY_COMPARE_WITH_TIMEOUT(failed.size(),1,3000);QCOMPARE(ready.size(),0);
        QCOMPARE(failed.first().at(1).toString(),QStringLiteral("invalid PNG dimensions or payload"));
    }
    void mapTileCooldownBoundAndExpiry(){
        auto now=QDateTime::fromSecsSinceEpoch(1700000000,QTimeZone::UTC);
        MapTiles tiles(nullptr,QUrl("http://127.0.0.1:1"),[&]{return now;});
        for(int x=0;x<MapTiles::maxCooldownEntries;x++)tiles.rememberFailure(QString("10/%1/0").arg(x));
        QCOMPARE(tiles.retryAfter.size(),MapTiles::maxCooldownEntries);
        QSignalSpy started(&tiles,&MapTiles::tileStarted);
        tiles.close();tiles.request(10,0,0,false);tiles.request(10,512,0,false);
        QCOMPARE(started.size(),0);QCOMPARE(tiles.retryAfter.size(),MapTiles::maxCooldownEntries);
        now=now.addSecs(299);tiles.close();tiles.request(10,0,0,false);QCOMPARE(started.size(),0);
        now=now.addSecs(1);tiles.close();QCOMPARE(tiles.retryAfter.size(),0);
        tiles.request(10,0,0,false);QCOMPARE(started.size(),1);tiles.close();
        // Sequential failures across locations remain bounded even over many expiry cycles.
        for(int cycle=0;cycle<3;cycle++) {
            for(int x=0;x<MapTiles::maxCooldownEntries;x++)tiles.rememberFailure(QString("%1/%2").arg(cycle).arg(x));
            QCOMPARE(tiles.retryAfter.size(),MapTiles::maxCooldownEntries);
            now=now.addSecs(300);tiles.close();QCOMPARE(tiles.retryAfter.size(),0);
        }
    }
    void mapTileCloseDiscardsDelayedReply(){
        QTcpServer server;QVERIFY(server.listen(QHostAddress::LocalHost));
        QList<QPointer<QTcpSocket>> pending;
        connect(&server,&QTcpServer::newConnection,&server,[&]{while(server.hasPendingConnections())pending.append(server.nextPendingConnection());});
        MapTiles tiles(nullptr,QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
        QSignalSpy ready(&tiles,&MapTiles::tileReady),failed(&tiles,&MapTiles::tileFailed),started(&tiles,&MapTiles::tileStarted);
        for(int x=0;x<3;x++)tiles.request(2,x,0,false);
        QTRY_COMPARE(pending.size(),3);QCOMPARE(tiles.active.size(),3);
        tiles.close();QCOMPARE(tiles.active.size(),0);QCOMPARE(ready.size(),0);QCOMPARE(failed.size(),0);
        for(auto socket:pending)QTRY_COMPARE(socket->state(),QAbstractSocket::UnconnectedState);
        tiles.request(2,0,0,false);QCOMPARE(started.size(),4);tiles.close();
        QCoreApplication::processEvents();QCOMPARE(ready.size(),0);QCOMPARE(failed.size(),0);QCOMPARE(tiles.retryAfter.size(),0);
    }
    void mapTileDownloadLimit(){
        QImage image(256,256,QImage::Format_ARGB32);
        image.fill(Qt::blue);
        QByteArray png;
        QBuffer buffer(&png);
        QVERIFY(buffer.open(QIODevice::WriteOnly));
        QVERIFY(image.save(&buffer,"PNG"));
        QVERIFY(png.size()<128*1024);
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            connect(&server,&QTcpServer::newConnection,&server,[&]{
                auto *socket=server.nextPendingConnection();
                connect(socket,&QTcpSocket::readyRead,socket,[socket,&png]{
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nCache-Control: max-age=3600\r\nContent-Length: "+QByteArray::number(png.size())+"\r\n\r\n"+png);
                });
            });
            MapTiles tiles(nullptr,QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles,&MapTiles::tileReady);
            QSignalSpy failed(&tiles,&MapTiles::tileFailed);
            tiles.request(0,0,0,false);
            QTRY_COMPARE_WITH_TIMEOUT(ready.size(),1,3000);
            QCOMPARE(failed.size(),0);
            QVERIFY(ready.first().at(1).toString().startsWith("data:image/png;base64,"));
            tiles.close();
            QSignalSpy replies(&tiles,&MapTiles::tileReply);
            tiles.request(0,0,0,true);
            QTRY_COMPARE_WITH_TIMEOUT(ready.size(),2,3000);
            QCOMPARE(failed.size(),0);QCOMPARE(replies.size(),1);QVERIFY(replies.first().at(1).toBool());
        }
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            connect(&server,&QTcpServer::newConnection,&server,[&]{
                auto *socket=server.nextPendingConnection();
                connect(socket,&QTcpSocket::readyRead,socket,[socket]{
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nContent-Length: 1048576\r\n\r\n");
                });
            });
            MapTiles tiles(nullptr,QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles,&MapTiles::tileReady);
            QSignalSpy failed(&tiles,&MapTiles::tileFailed);
            tiles.request(0,0,0,false);
            QTRY_COMPARE_WITH_TIMEOUT(failed.size(),1,3000);
            QCOMPARE(failed.first().at(1).toString(),QStringLiteral("tile exceeds 128 KiB"));
            QCOMPARE(ready.size(),0);
        }
        {
            QTcpServer server;
            QVERIFY(server.listen(QHostAddress::LocalHost));
            int sent=0;
            connect(&server,&QTcpServer::newConnection,&server,[&]{
                auto *socket=server.nextPendingConnection();
                connect(socket,&QTcpSocket::readyRead,socket,[socket,&sent]{
                    socket->readAll();
                    socket->write("HTTP/1.1 200 OK\r\nContent-Type: image/png\r\nTransfer-Encoding: chunked\r\n\r\n");
                    auto *timer=new QTimer(socket);
                    connect(timer,&QTimer::timeout,socket,[socket,timer,&sent]{
                        if(socket->state()!=QAbstractSocket::ConnectedState||sent>=1024*1024){timer->stop();return;}
                        QByteArray chunk(16*1024,'x');
                        socket->write(QByteArray::number(chunk.size(),16)+"\r\n"+chunk+"\r\n");
                        sent+=chunk.size();
                    });
                    timer->start(5);
                });
            });
            MapTiles tiles(nullptr,QUrl(QStringLiteral("http://127.0.0.1:%1").arg(server.serverPort())));
            QSignalSpy ready(&tiles,&MapTiles::tileReady);
            QSignalSpy failed(&tiles,&MapTiles::tileFailed);
            tiles.request(0,0,0,false);
            QTRY_COMPARE_WITH_TIMEOUT(failed.size(),1,3000);
            QCOMPARE(failed.first().at(1).toString(),QStringLiteral("tile exceeds 128 KiB"));
            QCOMPARE(ready.size(),0);
            QVERIFY(sent<1024*1024);
        }
    }
    void renderLiveMapCapture(){
        const auto capture=qEnvironmentVariable("WEATHER_QT_MAP_CAPTURE");
        const auto output=qEnvironmentVariable("WEATHER_QT_MAP_SCREENSHOT");
        if(capture.isEmpty()||output.isEmpty())QSKIP("Set WEATHER_QT_MAP_CAPTURE and WEATHER_QT_MAP_SCREENSHOT for private map render validation");
        QFile file(capture);QVERIFY(file.open(QIODevice::ReadOnly));
        const auto data=QJsonDocument::fromJson(file.readAll()).object();QVERIFY(!data.isEmpty());
        const auto zone=QTimeZone(qEnvironmentVariable("WEATHER_QT_MAP_TIMEZONE","America/New_York").toUtf8());QVERIFY(zone.isValid());QJsonArray labels;
        for(const auto value:data.value("hours").toArray())labels.append(QDateTime::fromSecsSinceEpoch(value.toInteger(),zone).toString("ddd MMM d, h:mm AP t"));
        const auto fetched=QDateTime::fromString(data.value("fetched_at").toString(),Qt::ISODate).toTimeZone(zone).toString("ddd MMM d, h:mm AP t");
        FakeTransport transport;MapTiles tiles;QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.rootContext()->setContextProperty("mapTiles",&tiles);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        const auto location=qEnvironmentVariable("WEATHER_QT_MAP_LOCATION","New York, NY");
        auto snapshotState=selectedSnapshot(1,location);
        const auto snapshotPath=qEnvironmentVariable("WEATHER_QT_MAP_SNAPSHOT");
        if(!snapshotPath.isEmpty()){
            QFile snapshotFile(snapshotPath);QVERIFY(snapshotFile.open(QIODevice::ReadOnly));
            snapshotState=QJsonDocument::fromJson(snapshotFile.readAll()).object();
            QCOMPARE(snapshotState.value("location").toObject().value("name").toString(),location);
        }
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshotState}});
        root->setProperty("effectsOpen",false);
        QQuickWindow *window=nullptr;for(auto *candidate:QGuiApplication::allWindows())if(candidate->objectName()=="weatherWindow")window=qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);QTRY_VERIFY(window->isExposed());
        auto *panel=qobject_cast<QQuickItem*>(root->findChild<QObject*>("weatherMaps"));QVERIFY(panel);
        auto *scroll=root->findChild<QObject*>("forecastScroll");QVERIFY(scroll);
        auto *flick=qobject_cast<QQuickItem*>(scroll->property("contentItem").value<QObject*>());QVERIFY(flick);
        flick->setProperty("contentY",panel->y()+20);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine,root,"backend.mapWanted").toBool(),3000);
        QSignalSpy received(&tiles,&MapTiles::tileReady);
        QSignalSpy started(&tiles,&MapTiles::tileStarted);
        QSignalSpy replies(&tiles,&MapTiles::tileReply);
        QObject::connect(&tiles,&MapTiles::tileFailed,&tiles,[](const QString &key,const QString &reason){qInfo().noquote()<<"Map tile failure"<<key<<reason;});
        deliver(transport,{{"version",1},{"event","map"},{"map",QJsonObject{{"status","fresh"},{"offline",false},{"error",""},{"fetched_at",data.value("fetched_at")},{"fetched_label",fetched},{"hour_labels",labels},{"data",data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(started.size()>0,3000);
        QTRY_VERIFY_WITH_TIMEOUT(received.size()>=2,15000);
        QTest::qWait(400);
        QVERIFY(window->grabWindow().save(output));
        panel->setProperty("hourIndex",1);QTest::qWait(150);
        QVERIFY(window->grabWindow().save(QString(output).replace(".png","-wind.png")));
        panel->setProperty("hourIndex",1);
        auto *precipitation=qobject_cast<QQuickItem*>(root->findChild<QObject*>("mapPrecipitationModule"));QVERIFY(precipitation);
        flick->setProperty("contentY",panel->y()+precipitation->parentItem()->y()+precipitation->y()-24);QTest::qWait(250);
        QVERIFY(window->grabWindow().save(QString(output).replace(".png","-precipitation.png")));
        int onlineNetwork=0;for(const auto &reply:replies)if(!reply.at(1).toBool())++onlineNetwork;
        qInfo()<<"Map tiles on first view:"<<replies.size()<<"valid replies,"<<onlineNetwork<<"network replies";
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true}});
        window->hide();QTRY_VERIFY_WITH_TIMEOUT(!evaluate(engine,root,"backend.mapWanted").toBool(),3000);
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        window->show();QTRY_VERIFY(window->isExposed());
        flick->setProperty("contentY",panel->y()+20);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine,root,"backend.mapWanted").toBool(),3000);
        QSignalSpy offlineTiles(&tiles,&MapTiles::tileReady);
        QSignalSpy offlineReplies(&tiles,&MapTiles::tileReply);
        deliver(transport,{{"version",1},{"event","map"},{"map",QJsonObject{{"status","stale"},{"offline",true},{"error",""},{"fetched_at",data.value("fetched_at")},{"fetched_label",fetched},{"hour_labels",labels},{"data",data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(offlineTiles.size()>=2,3000);
        for(const auto &reply:offlineReplies)QVERIFY(reply.at(1).toBool());
        window->hide();
    }
    void measurementConversions(){
        QQmlEngine engine;
        QQmlComponent component(&engine);
        component.setData("import QtQml\nimport \"qrc:/ui/qml/Forecast.js\" as Forecast\nQtObject {}",QUrl());
        QScopedPointer<QObject> scope(component.create());QVERIFY2(scope,qPrintable(component.errorString()));
        for(const auto &test:QList<QPair<QString,QString>>{
            {"Forecast.wind(10,'F')","22 mph"},{"Forecast.wind(10,'C')","36 km/h"},
            {"Forecast.wind(10,'C','mph')","22 mph"},{"Forecast.wind(10,'F','km/h')","36 km/h"},
            {"Forecast.wind(10,'C','m/s')","10.0 m/s"},{"Forecast.wind(10,'C','kn')","19 kn"},
            {"Forecast.wind(0,'C')","0 km/h"},{"Forecast.wind(null,'F')","—"},
            {"Forecast.pressure(1013.25,'F')","29.92 inHg"},{"Forecast.pressure(1013.25,'C')","1013 hPa"},
            {"Forecast.pressure(null,'F')","—"},{"Forecast.distance(16093.44,'C')","16.1 km"},
            {"Forecast.distance(16093.44,'F')","10.0 mi"},{"Forecast.distance(0,'C')","0.0 km"},
            {"Forecast.distance(null,'C')","—"},{"Forecast.amount(25.4,'F')","1.00 in"},
            {"Forecast.amount(25.4,'C')","25.4 mm"},
            {"Forecast.outlook([{precipitation_probability:0,wind_gust_m_s:10}],'C','kn')","Low precipitation chances in the next 1 hourly forecasts. Gusts up to 19 kn."}})
        {
            QQmlExpression expression(qmlContext(scope.data()),scope.data(),test.first);
            const auto result=expression.evaluate();QVERIFY2(!expression.hasError(),qPrintable(expression.error().toString()));
            QCOMPARE(result.toString(),test.second);
        }
    }
    void mapOnDemandAndTimeline(){
        FakeTransport transport;FakeMapTiles tiles;QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.rootContext()->setContextProperty("mapTiles",&tiles);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        root->setProperty("effectsOpen",true);
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",selectedSnapshot(1,"New York, NY")}});
        auto *panel=root->findChild<QObject*>("weatherMaps");QVERIFY(panel);
        auto *temperature=root->findChild<QObject*>("mapTemperatureModule");QVERIFY(temperature);
        auto *wind=root->findChild<QObject*>("mapWindModule");QVERIFY(wind);
        auto *precipitation=root->findChild<QObject*>("mapPrecipitationModule");QVERIFY(precipitation);
        QVERIFY(!root->findChild<QObject*>("openWeatherMap"));
        QCOMPARE(precipitation->findChild<QObject*>("mapModuleTitle")->property("text").toString(),QString("Precipitation"));
        QCOMPARE(tiles.requests,0);QCOMPARE(transport.requests.size(),0);
        root->setProperty("effectsOpen",false);
        auto *scroll=root->findChild<QObject*>("forecastScroll");QVERIFY(scroll);
        auto *flick=scroll->property("contentItem").value<QObject*>();QVERIFY(flick);
        flick->setProperty("contentY",panel->property("y").toReal()+40);
        QTRY_VERIFY_WITH_TIMEOUT(evaluate(engine,root,"backend.mapWanted").toBool(),3000);
        QCOMPARE(transport.requests.size(),1);QCOMPARE(transport.requests.last()["op"].toString(),QString("map_open"));
        QJsonArray hours{1790596800,1790600400,1790604000},cells;
        for(int row=0;row<5;row++)for(int col=0;col<5;col++) {
            cells.append(QJsonObject{{"latitude",40.7128+(2-row)*0.07},{"longitude",-74.006+(col-2)*0.09},
                {"temperature_c",QJsonArray{15,16,17}},{"wind_speed_m_s",QJsonArray{3,4,5}},
                {"wind_from_deg",QJsonArray{0,90,180}},{"precipitation_mm",QJsonArray{0,1,2}}});
        }
        QJsonObject data{{"latitude",40.7128},{"longitude",-74.006},{"radius_miles",10},{"model_id","ncep_nbm_conus"},
            {"model_name","NOAA NBM CONUS"},{"resolution_km",2.5},{"fetched_at","2026-09-28T12:00:00Z"},
            {"hours",hours},{"cells",cells},{"attribution","Model forecast via Open-Meteo (CC BY 4.0)"}};
        deliver(transport,{{"version",1},{"event","map"},{"map",QJsonObject{{"status","fresh"},{"offline",false},{"error",""},{"fetched_at","2026-09-28T12:00:00Z"},{"fetched_label","Mon Sep 28, 8:00 AM EDT"},{"hour_labels",QJsonArray{"Mon Sep 28, 8:00 AM EDT","Mon Sep 28, 9:00 AM EDT","Mon Sep 28, 10:00 AM EDT"}},{"data",data}}}});
        QTRY_VERIFY_WITH_TIMEOUT(tiles.requests>0,3000);QVERIFY(tiles.requests<=16);
        QCOMPARE(panel->property("hourIndex").toInt(),0);
        for(int i=0;i<100;i++)panel->setProperty("hourIndex",i%3);
        panel->setProperty("hourIndex",2);QCOMPARE(panel->property("hourIndex").toInt(),2);
        QCOMPARE(temperature->property("hourIndex").toInt(),2);
        QCOMPARE(wind->property("hourIndex").toInt(),2);
        QCOMPARE(precipitation->property("hourIndex").toInt(),2);
        QCOMPARE(tiles.requests,tiles.active.size());QCOMPARE(transport.requests.size(),1);
        QCOMPARE(evaluate(engine,wind,"windSpeed(10)").toString(),QString("22 mph"));
        auto metricSnapshot=selectedSnapshot(2,"New York, NY");auto controls=metricSnapshot["controls"].toObject();
        controls["units"]="C";controls["units_mode"]="auto";metricSnapshot["controls"]=controls;
        auto *overlay=wind->findChild<QObject*>("mapOverlay");QVERIFY(overlay);
        QSignalSpy painted(overlay,SIGNAL(painted()));QTest::qWait(100);painted.clear();
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",metricSnapshot}});
        QCOMPARE(wind->property("units").toString(),QString("C"));
        QCOMPARE(evaluate(engine,wind,"windSpeed(10)").toString(),QString("36 km/h"));
        QCOMPARE(wind->findChild<QObject*>("mapLegend")->property("text").toString(),QString("Arrow points where wind blows · speeds in km/h"));
        QVERIFY(temperature->findChild<QObject*>("mapCredit")->property("text").toString().startsWith("16.1 km radius"));
        QTRY_VERIFY_WITH_TIMEOUT(painted.size()>0,3000);
        controls["wind_units"]="kn";metricSnapshot["controls"]=controls;metricSnapshot["snapshot_revision"]=3;painted.clear();
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",metricSnapshot}});
        QCOMPARE(evaluate(engine,wind,"windSpeed(10)").toString(),QString("19 kn"));
        QVERIFY(wind->findChild<QObject*>("mapLegend")->property("text").toString().endsWith("speeds in kn"));
        QTRY_VERIFY_WITH_TIMEOUT(painted.size()>0,3000);
        // Changing the display units must reuse the loaded map and tiles.
        QCOMPARE(transport.requests.size(),1);QCOMPARE(tiles.requests,tiles.active.size());
        QCOMPARE(evaluate(engine,temperature,"mapX(-73.9)>mapX(-74.0)").toBool(),true);
        QCOMPARE(evaluate(engine,temperature,"mapY(40.8)<mapY(40.7)").toBool(),true);
        auto *window=qobject_cast<QWindow*>(root->property("weatherWindow").value<QObject*>());QVERIFY(window);
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true}});
        window->showMinimized();QVERIFY(window->isVisible());
        QTRY_VERIFY(!root->property("mapActive").toBool());
        QTRY_VERIFY(!evaluate(engine,root,"backend.mapWanted").toBool());QVERIFY(tiles.active.isEmpty());
        QCOMPARE(transport.requests.last()["op"].toString(),QString("map_close"));
        const auto requestsBeforeRestore=tiles.requests;
        // A late forecast event while minimized must not restart geographic tile work.
        deliver(transport,{{"version",1},{"event","map"},{"map",QJsonObject{{"status","loading"},{"offline",false},{"error",""},{"data",data}}}});
        QCOMPARE(tiles.requests,requestsBeforeRestore);
        window->showNormal();QTRY_VERIFY(root->property("mapActive").toBool());
        QTRY_VERIFY(evaluate(engine,root,"backend.mapWanted").toBool());
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        QTRY_COMPARE(transport.requests.last()["op"].toString(),QString("map_open"));
        window->hide();
        QTRY_VERIFY(tiles.closes>0);QVERIFY(!evaluate(engine,root,"backend.mapWanted").toBool());
    }
    void mapRapidToggleKeepsLatestIntent(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        QVERIFY(evaluate(engine,bridge.data(),"openMap()").toBool());
        QCOMPARE(transport.requests.size(),1);QCOMPARE(transport.requests.last()["op"].toString(),QString("map_open"));
        QVERIFY(evaluate(engine,bridge.data(),"closeMap()").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"openMap()").toBool());
        QCOMPARE(transport.requests.size(),1);
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true}});
        QCOMPARE(transport.requests.size(),1);QVERIFY(bridge->property("mapWanted").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"closeMap()").toBool());
        QCOMPARE(transport.requests.size(),2);QCOMPARE(transport.requests.last()["op"].toString(),QString("map_close"));
        QVERIFY(evaluate(engine,bridge.data(),"openMap()").toBool());
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        QCOMPARE(transport.requests.size(),3);QCOMPARE(transport.requests.last()["op"].toString(),QString("map_open"));
        QVERIFY(evaluate(engine,bridge.data(),"closeMap()").toBool());
        deliver(transport,{{"version",1},{"request_id",2},{"ok",true}});
        QCOMPARE(transport.requests.size(),4);QCOMPARE(transport.requests.last()["op"].toString(),QString("map_close"));
        deliver(transport,{{"version",1},{"request_id",3},{"ok",true}});
        QCOMPARE(bridge->property("pending").toInt(),-1);QVERIFY(!bridge->property("mapWanted").toBool());
        QCOMPARE(evaluate(engine,bridge.data(),"weatherMap.status").toString(),QString("closed"));
        QVERIFY(!bridge->property("disconnected").toBool());
    }
    void applicationWindowMaps(){
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);
        QWindow *window=nullptr;for(auto *candidate:QGuiApplication::allWindows())if(candidate->objectName()=="weatherWindow")window=candidate;
        QVERIFY(window);QTRY_VERIFY(window->isVisible());QTRY_VERIFY(window->isExposed());
    }
    void unsupportedNotifications(){
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(1,"Unsupported delivery")}});
        auto *toggle=root->findChild<QObject*>("notificationEnabled");auto *notice=root->findChild<QObject*>("notificationUnsupported");QVERIFY(toggle);QVERIFY(notice);
        root->setProperty("effectsOpen",true);QTRY_VERIFY(notice->property("visible").toBool());QVERIFY(!toggle->property("enabled").toBool());
        QVERIFY(notice->property("text").toString().contains("optional libnotify"));
        auto *stop=root->findChild<QObject*>("notificationStop");QVERIFY(stop);QVERIFY(!stop->property("enabled").toBool());
        QCOMPARE(transport.requests.size(),0);
    }
    void snapshotOrderingAndAcknowledgment(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        emit transport.ready();QCOMPARE(transport.requests.size(),1);
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(2,"Newest")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Newest"));
        QVERIFY(!bridge->property("disconnected").toBool()); // [] and null retained as ordinary JSON values.
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true},{"snapshot",snapshot(1,"Older")}});
        QCOMPARE(transport.requests.last()["op"].toString(),QString("set_presentation"));
        QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        QCOMPARE(bridge->property("pending").toInt(),-1);QVERIFY(!bridge->property("busy").toBool());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Newest"));
        QVERIFY(evaluate(engine,bridge.data(),"send('set_controls',{units:'C'})").toBool());
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(3,"Latest")}});
        deliver(transport,{{"version",1},{"request_id",2},{"ok",false},{"error","invalid_controls"},{"snapshot",snapshot(2,"Stale")}});
        QCOMPARE(bridge->property("pending").toInt(),-1);QVERIFY(!bridge->property("error").toString().isEmpty());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Latest"));
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(3,"Same revision")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Latest"));
        emit transport.ready();QCOMPARE(bridge->property("lastSnapshotRevision").toDouble(),0.0);
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(1,"New service")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("New service"));
    }
    void presentationStartupAndCoalescing(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        bridge->setProperty("presentationActive",false);QCOMPARE(transport.requests.size(),0);
        emit transport.ready();QCOMPARE(transport.requests.size(),1);QCOMPARE(transport.requests.last()["op"].toString(),QString("subscribe"));
        bridge->setProperty("presentationActive",true);bridge->setProperty("presentationActive",false);QCOMPARE(transport.requests.size(),1);
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true},{"snapshot",snapshot(1,"Initial")}});
        QVERIFY(bridge->property("subscribed").toBool());QCOMPARE(transport.requests.size(),2);
        QCOMPARE(transport.requests.last()["op"].toString(),QString("set_presentation"));QVERIFY(!transport.requests.last()["active"].toBool());QVERIFY(!bridge->property("busy").toBool());
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        QVERIFY(evaluate(engine,bridge.data(),"send('set_controls',{units:'C'})").toBool());
        bridge->setProperty("presentationActive",true);bridge->setProperty("presentationActive",false);bridge->setProperty("presentationActive",true);
        QCOMPARE(transport.requests.size(),3);
        deliver(transport,{{"version",1},{"request_id",2},{"ok",true}});
        QCOMPARE(transport.requests.size(),4);QCOMPARE(transport.requests.last()["op"].toString(),QString("set_presentation"));QVERIFY(transport.requests.last()["active"].toBool());
        QVERIFY(!bridge->property("busy").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"send('set_controls',{units:'F'})").toBool());
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(3,"Freshest event")}});
        deliver(transport,{{"version",1},{"request_id",3},{"ok",true},{"snapshot",snapshot(2,"Restore response")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Freshest event"));
        QCOMPARE(transport.requests.size(),5);QCOMPARE(transport.requests.last()["op"].toString(),QString("set_controls"));
        deliver(transport,{{"version",1},{"request_id",4},{"ok",true}});
        bridge->setProperty("presentationActive",false);deliver(transport,{{"version",1},{"request_id",5},{"ok",true}});
        bridge->setProperty("presentationActive",true);
        deliver(transport,{{"version",1},{"request_id",6},{"ok",true},{"snapshot",snapshot(4,"Fresh restore")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Fresh restore"));QCOMPARE(bridge->property("pending").toInt(),-1);
    }
    void presentationWindowVisibility(){
        FakeTransport transport;FakeMapTiles tiles;QQmlApplicationEngine engine;
        engine.rootContext()->setContextProperty("weatherTransport",&transport);engine.rootContext()->setContextProperty("mapTiles",&tiles);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        auto *window=qobject_cast<QWindow*>(root->property("weatherWindow").value<QObject*>());QVERIFY(window);
        window->hide();emit transport.ready();QCOMPARE(transport.requests.last()["op"].toString(),QString("subscribe"));
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true},{"snapshot",snapshot(1,"Start hidden")}});
        QCOMPARE(transport.requests.last()["op"].toString(),QString("set_presentation"));QVERIFY(!transport.requests.last()["active"].toBool());
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        window->showNormal();QTRY_VERIFY(evaluate(engine,root,"backend.presentationActive").toBool());QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport,{{"version",1},{"request_id",2},{"ok",true},{"snapshot",snapshot(2,"Restored")}});
        window->showMinimized();QVERIFY(window->isVisible());QTRY_VERIFY(!evaluate(engine,root,"backend.presentationActive").toBool());QVERIFY(!transport.requests.last()["active"].toBool());
        deliver(transport,{{"version",1},{"request_id",3},{"ok",true}});
        window->showNormal();QTRY_VERIFY(evaluate(engine,root,"backend.presentationActive").toBool());QVERIFY(transport.requests.last()["active"].toBool());
        deliver(transport,{{"version",1},{"request_id",4},{"ok",true},{"snapshot",snapshot(3,"Restored again")}});
        QCOMPARE(evaluate(engine,root,"backend.snapshot.location").toString(),QString("Restored again"));
    }
    void serviceStopped(){
        for(bool ok:{true,false}){
            FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
            QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
            QSignalSpy closed(bridge.data(),SIGNAL(closed(int)));QJsonObject event{{"version",1},{"event","service_stopped"},{"ok",ok}};if(!ok)event["error"]="cleanup_failed";deliver(transport,event);
            QCOMPARE(closed.size(),1);QCOMPARE(closed.first().first().toInt(),ok?0:1);QCOMPARE(transport.requests.size(),0);
        }
    }
    void searchValidationAndAlertCoverage(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        auto valid=snapshot(1,"São Paulo, São Paulo, Brazil");
        valid["location_settings"]=QJsonObject{{"mode","place"},{"zip_code",QJsonValue::Null},{"busy",false},{"error",QJsonValue::Null},{"country_code","BR"},{"place",QJsonObject{{"provider","open-meteo"},{"id",3448439}}}};
        valid["place_search"]=QJsonObject{{"generation",3},{"status","ready"},{"error",QJsonValue::Null},{"results",QJsonArray{QJsonObject{{"id",3448439},{"name","São Paulo"},{"admin1","São Paulo"},{"country","Brazil"},{"country_code","BR"}}}}};
        valid["alerts"]=QJsonObject{{"status","not_supported_here"},{"source",QJsonValue::Null},{"coverage","unsupported"},{"fetched_at",QJsonValue::Null},{"items",QJsonArray{}}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",valid}});
        QVERIFY(!bridge->property("disconnected").toBool());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.place_search.results[0].name").toString(),QString::fromUtf8("São Paulo"));
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.alerts.coverage").toString(),QString("unsupported"));
        auto invalid=valid;invalid["snapshot_revision"]=2;
        auto search=invalid["place_search"].toObject();auto rows=search["results"].toArray();auto row=rows.first().toObject();row["name"]="Paris\u202e";rows.replace(0,row);search["results"]=rows;invalid["place_search"]=search;
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",invalid}});
        QVERIFY(bridge->property("disconnected").toBool());
        auto rejects=[this](QJsonObject value){
            FakeTransport local;QQmlEngine localEngine;localEngine.rootContext()->setContextProperty("weatherTransport",&local);
            QQmlComponent localComponent(&localEngine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> localBridge(localComponent.create());
            if(!localBridge)return false;
            deliver(local,{{"version",1},{"event","snapshot"},{"snapshot",value}});
            return localBridge->property("disconnected").toBool();
        };
        auto badSettings=valid;auto settings=badSettings["location_settings"].toObject();settings["unexpected"]=true;badSettings["location_settings"]=settings;
        QVERIFY(rejects(badSettings));
        auto duplicate=valid;auto dupSearch=duplicate["place_search"].toObject();auto dupRows=dupSearch["results"].toArray();dupRows.append(dupRows.first());dupSearch["results"]=dupRows;duplicate["place_search"]=dupSearch;
        QVERIFY(rejects(duplicate));
        auto badCoverage=valid;badCoverage["alerts"]=QJsonObject{{"status","not_supported_here"},{"source","National Weather Service"},{"coverage","unsupported"},{"fetched_at",QJsonValue::Null},{"items",QJsonArray{}}};
        QVERIFY(rejects(badCoverage));
    }
    void searchQueuePrioritizesStopAndCoalesces(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        emit transport.ready();QCOMPARE(transport.requests.size(),1);
        QVERIFY(evaluate(engine,bridge.data(),"send('search_places',{query:'Berlin',country_code:'DE',client_token:1})").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"send('search_places',{query:'Paris',country_code:'FR',client_token:2})").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"send('cancel_place_search')").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"send('search_places',{query:'Tokyo',country_code:'JP',client_token:3})").toBool());
        QVERIFY(evaluate(engine,bridge.data(),"send('stop_effects')").toBool());
        deliver(transport,{{"version",1},{"request_id",0},{"ok",true},{"snapshot",snapshot(1,"Start")}});
        QCOMPARE(transport.requests.size(),2);QCOMPARE(transport.requests.last()["op"].toString(),QString("stop_effects"));
        deliver(transport,{{"version",1},{"request_id",1},{"ok",true}});
        QCOMPARE(transport.requests.size(),3);QCOMPARE(transport.requests.last()["op"].toString(),QString("set_presentation"));
        deliver(transport,{{"version",1},{"request_id",2},{"ok",true}});
        QCOMPARE(transport.requests.size(),4);QCOMPARE(transport.requests.last()["op"].toString(),QString("cancel_place_search"));
        deliver(transport,{{"version",1},{"request_id",3},{"ok",true}});
        QCOMPARE(transport.requests.size(),5);QCOMPARE(transport.requests.last()["op"].toString(),QString("search_places"));
        QCOMPARE(transport.requests.last()["search"].toMap()["query"].toString(),QString("Tokyo"));
        QCOMPARE(transport.requests.last()["search"].toMap()["client_token"].toInt(),3);
        QVERIFY(!bridge->property("busy").toBool());
    }
    void placeSearchDebouncesAndSelectsIssuedGeneration(){
        QQmlEngine engine;QQmlComponent component(&engine,QUrl("qrc:/ui/qml/PlaceSearch.qml"));QScopedPointer<QObject> picker(component.create());QVERIFY2(picker,qPrintable(component.errorString()));
        QSignalSpy searches(picker.data(),SIGNAL(searchRequested(QVariant)));QSignalSpy cancels(picker.data(),SIGNAL(cancelRequested()));QSignalSpy selections(picker.data(),SIGNAL(locationRequested(QVariant)));
        auto *query=picker->findChild<QObject*>("placeQuery");auto *country=picker->findChild<QObject*>("placeCountry");QVERIFY(query);QVERIFY(country);
        query->setProperty("text","Sao");QTest::qWait(100);query->setProperty("text",QString::fromUtf8("São"));country->setProperty("text","br");
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(),1,1200);
        QCOMPARE(searches.first().first().toMap()["query"].toString(),QString::fromUtf8("São"));
        QCOMPARE(searches.first().first().toMap()["country_code"].toString(),QString("BR"));
        QCOMPARE(searches.first().first().toMap()["client_token"].toInt(),1);
        picker->setProperty("search",QVariantMap{{"generation",1},{"client_token",1},{"status","loading"},{"results",QVariantList{}},{"error",QVariant()}});
        picker->setProperty("search",QVariantMap{{"generation",1},{"client_token",1},{"status","ready"},{"results",QVariantList{QVariantMap{{"id",3448439},{"name",QString::fromUtf8("São Paulo")},{"admin1",QString::fromUtf8("São Paulo")},{"country","Brazil"},{"country_code","BR"}}}},{"error",QVariant()}});
        QVERIFY(picker->property("showingSearch").toBool());
        QCOMPARE(picker->property("visibleRows").toList().size(),1);
        evaluate(engine,picker.data(),"pick(visibleRows[0])");
        QCOMPARE(selections.size(),1);auto chosen=selections.first().first().toMap();QCOMPARE(chosen["place_id"].toInt(),3448439);QCOMPARE(chosen["search_generation"].toInt(),1);
        query->setProperty("text","Tokyo");QVERIFY(cancels.size()>0);QVERIFY(!picker->property("showingSearch").toBool());
        QTRY_COMPARE_WITH_TIMEOUT(searches.size(),2,1200);
        picker->setProperty("search",QVariantMap{{"generation",2},{"client_token",2},{"status","error"},{"results",QVariantList{}},{"error","offline"}});
        QVERIFY(picker->property("showingSearch").toBool());
        QVERIFY(picker->findChild<QObject*>("placeSearchStatus")->property("text").toString().contains("offline"));
    }
    void supersededQueryCannotLatchOlderGeneration(){
        QQmlEngine engine;QQmlComponent component(&engine,QUrl("qrc:/ui/qml/PlaceSearch.qml"));QScopedPointer<QObject> picker(component.create());QVERIFY2(picker,qPrintable(component.errorString()));
        QSignalSpy searches(picker.data(),SIGNAL(searchRequested(QVariant)));
        auto *query=picker->findChild<QObject*>("placeQuery");QVERIFY(query);
        query->setProperty("text","Berlin");QTRY_COMPARE_WITH_TIMEOUT(searches.size(),1,1200);
        query->setProperty("text","Tokyo");QTRY_COMPARE_WITH_TIMEOUT(searches.size(),2,1200);
        QCOMPARE(searches.at(0).first().toMap()["client_token"].toInt(),1);
        QCOMPARE(searches.at(1).first().toMap()["client_token"].toInt(),2);
        auto oldRows=QVariantList{QVariantMap{{"id",2950159},{"name","Berlin"},{"admin1","Berlin"},{"country","Germany"},{"country_code","DE"}}};
        picker->setProperty("search",QVariantMap{{"generation",1},{"client_token",1},{"status","loading"},{"results",QVariantList{}},{"error",QVariant()}});
        QVERIFY(!picker->property("showingSearch").toBool());
        picker->setProperty("search",QVariantMap{{"generation",1},{"client_token",1},{"status","ready"},{"results",oldRows},{"error",QVariant()}});
        QVERIFY(!picker->property("showingSearch").toBool());QVERIFY(picker->property("visibleRows").toList().isEmpty());
        picker->setProperty("search",QVariantMap{{"generation",2},{"client_token",0},{"status","idle"},{"results",QVariantList{}},{"error",QVariant()}});
        auto newRows=QVariantList{QVariantMap{{"id",1850147},{"name","Tokyo"},{"admin1","Tokyo"},{"country","Japan"},{"country_code","JP"}}};
        picker->setProperty("search",QVariantMap{{"generation",3},{"client_token",2},{"status","ready"},{"results",newRows},{"error",QVariant()}});
        QVERIFY(picker->property("showingSearch").toBool());
        QCOMPARE(picker->property("visibleRows").toList().first().toMap()["name"].toString(),QString("Tokyo"));
    }
    void astralLocationAccepted(){
        FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
        QString astral;for(int i=0;i<120;i++)astral+=QString::fromUtf8("🌍");
        auto value=snapshot(1,astral+", Region, Country");
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",value}});
        QVERIFY(!bridge->property("disconnected").toBool());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),astral+", Region, Country");
    }
    void pointFieldValidationAndLegacyNulls(){
        auto accepted=[this](QJsonObject value){
            FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
            QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());
            if(!bridge)return false;
            deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",value}});
            return !bridge->property("disconnected").toBool();
        };
        auto valid=metricSnapshot(1);QVERIFY(accepted(valid));
        auto old=valid;auto current=old["current"].toObject();auto hour=old["hourly"].toArray().first().toObject();
        for(auto key:{"uv_index","pressure_msl_hpa","dew_point_c"}){current.remove(key);hour.remove(key);}
        old["current"]=current;old["hourly"]=QJsonArray{hour};QVERIFY(accepted(old));
        for(auto key:{"uv_index","pressure_msl_hpa","dew_point_c"}){current[key]=QJsonValue::Null;hour[key]=QJsonValue::Null;}
        old["current"]=current;old["hourly"]=QJsonArray{hour};QVERIFY(accepted(old));
        for(auto bad:QList<QPair<QString,QJsonValue>>{{"uv_index",-0.1},{"uv_index",50.1},{"pressure_msl_hpa",799.9},{"pressure_msl_hpa",1100.1},{"dew_point_c",-100.1},{"dew_point_c",60.1},{"uv_index","3.2"}}){
            auto invalid=valid;auto row=invalid["current"].toObject();row[bad.first]=bad.second;invalid["current"]=row;QVERIFY2(!accepted(invalid),qPrintable(bad.first));
            invalid=valid;auto hourly=invalid["hourly"].toArray();row=hourly.first().toObject();row[bad.first]=bad.second;hourly.replace(0,row);invalid["hourly"]=hourly;QVERIFY2(!accepted(invalid),qPrintable(bad.first));
        }
    }
    void airQualityContractValidation(){
        auto accepted=[this](QJsonObject value){
            FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
            QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());
            if(!bridge)return false;
            deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",value}});
            return !bridge->property("disconnected").toBool();
        };
        auto old=snapshot(1,"Legacy backend");QVERIFY(accepted(old));
        auto valid=old;valid["air_quality"]=airQuality();QVERIFY(accepted(valid));
        auto stale=valid;auto aq=airQuality(9000);aq["freshness"]="stale";aq["offline"]=true;aq["error"]="fetch_failed";stale["air_quality"]=aq;QVERIFY(accepted(stale));
        for(const auto status:{"expired","invalid_future"}){
            auto hidden=valid;aq=airQuality();aq["freshness"]=status;aq["us_aqi"]=QJsonValue::Null;aq["european_aqi"]=QJsonValue::Null;aq["pm2_5_ug_m3"]=QJsonValue::Null;
            aq["age_seconds"]=QString(status)=="invalid_future"?QJsonValue::Null:QJsonValue(25000);hidden["air_quality"]=aq;QVERIFY(accepted(hidden));
        }
        auto unavailable=valid;aq=airQuality();aq["freshness"]="unavailable";
        for(const auto key:{"valid_at","fetched_at","valid_label","fetched_label","age_seconds","us_aqi","european_aqi","pm2_5_ug_m3"})aq[key]=QJsonValue::Null;
        unavailable["air_quality"]=aq;QVERIFY(accepted(unavailable));
        auto rejects=[&](QJsonObject bad){auto candidate=valid;candidate["air_quality"]=bad;return !accepted(candidate);};
        for(const auto key:{"freshness","refreshing","offline","error","domain","source","attribution","valid_at","fetched_at","valid_label","fetched_label","age_seconds","us_aqi","european_aqi","pm2_5_ug_m3"}){aq=airQuality();aq.remove(key);QVERIFY2(rejects(aq),key);}
        aq=airQuality();aq["extra"]=true;QVERIFY(rejects(aq));
        for(auto bad:QList<QPair<QString,QJsonValue>>{{"domain","cams_europe"},{"source","Station"},{"attribution","Fake"},{"freshness","unknown"},{"error","raw failure"},{"us_aqi",-1},{"us_aqi",1001},{"european_aqi",1001},{"pm2_5_ug_m3",5001},{"pm2_5_ug_m3","12"},{"age_seconds",-1},{"offline",1}}){
            aq=airQuality();aq[bad.first]=bad.second;QVERIFY2(rejects(aq),qPrintable(bad.first));
        }
        aq=airQuality();aq["freshness"]="expired";QVERIFY(rejects(aq));
        aq=airQuality();aq["freshness"]="invalid_future";aq["age_seconds"]=QJsonValue::Null;QVERIFY(rejects(aq));
        aq=airQuality();aq["freshness"]="unavailable";QVERIFY(rejects(aq));
        aq=airQuality(7201);QVERIFY(rejects(aq));
        aq=airQuality(7200);aq["freshness"]="stale";QVERIFY(rejects(aq));
        aq=airQuality(21601);aq["freshness"]="stale";QVERIFY(rejects(aq));
        aq=airQuality();aq["valid_at"]="2026-09-27T11:59:00Z";QVERIFY(rejects(aq));
        aq=airQuality();aq["valid_at"]="2026-09-28T12:26:00Z";QVERIFY(rejects(aq));
        aq=airQuality();aq["valid_label"]=QJsonValue::Null;QVERIFY(rejects(aq));
        aq=airQuality();aq["fetched_label"]="";QVERIFY(rejects(aq));
        aq=airQuality();aq["valid_label"]=QString(81,'a');QVERIFY(rejects(aq));
        aq=airQuality();aq["fetched_label"]="bad\nlabel";QVERIFY(rejects(aq));
    }
    void airQualityLocationTimeLabels(){
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        auto *times=root->findChild<QObject*>("airQualityTimes");QVERIFY(times);
        auto show=[&](qint64 revision,const QString &zone,const QString &validLabel,const QString &fetchedLabel){
            auto state=metricSnapshot(revision),location=state["location"].toObject(),aq=airQuality();
            location["timezone"]=zone;state["location"]=location;
            aq["valid_label"]=validLabel;aq["fetched_label"]=fetchedLabel;state["air_quality"]=aq;
            deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",state}});
            root->setProperty("effectsOpen",false);
            QCOMPARE(times->property("text").toString(),QString("Model forecast valid ")+validLabel+" · Fetched "+fetchedLabel);
        };
        show(1,"UTC","2026-09-28 12:00 UTC (+00:00)","2026-09-28 12:20 UTC (+00:00)");
        show(2,"Asia/Tokyo","2026-09-28 21:00 JST (+09:00)","2026-09-28 21:20 JST (+09:00)");
    }
    void renderAirQualityScreenshots(){
        const QString prefix=qEnvironmentVariable("WEATHER_QT_AQ_SCREENSHOT_PREFIX");
        if(prefix.isEmpty())QSKIP("Set WEATHER_QT_AQ_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        QQuickWindow *window=nullptr;for(auto *candidate:QGuiApplication::allWindows())if(candidate->objectName()=="weatherWindow")window=qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);QTRY_VERIFY(window->isExposed());
        auto *panel=qobject_cast<QQuickItem*>(root->findChild<QObject*>("airQualityPanel"));QVERIFY(panel);
        std::function<QObject*(QQuickItem*,const char*)> visualNamed=[&](QQuickItem *item,const char *name)->QObject*{
            if(item->objectName()==QLatin1String(name))return item;
            for(auto *child:item->childItems())if(auto *found=visualNamed(child,name))return found;
            return nullptr;
        };
        auto named=[&](const char *name)->QObject*{auto *found=visualNamed(panel,name);if(!found)QTest::qFail(qPrintable(QString("Missing AQ label %1").arg(name)),__FILE__,__LINE__);return found;};
        auto *scroll=root->findChild<QObject*>("forecastScroll");QVERIFY(scroll);
        auto *flick=qvariant_cast<QQuickItem*>(scroll->property("contentItem"));QVERIFY(flick);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        auto show=[&](QJsonObject aq,qint64 revision,int width,const QString &suffix){
            auto state=metricSnapshot(revision),location=state["location"].toObject();
            location["timezone"]="Asia/Tokyo";state["location"]=location;
            aq["valid_label"]="2026-09-28 21:00 JST (+09:00)";
            aq["fetched_label"]="2026-09-28 21:20 JST (+09:00)";
            state["air_quality"]=aq;
            deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",state}});
            root->setProperty("effectsOpen",false);
            window->resize(width,850);
            QTRY_VERIFY_WITH_TIMEOUT(flick->property("contentHeight").toDouble()>flick->height(),2000);
            QTest::qWait(100);
            flick->setProperty("contentY",qMax(0.0,flick->property("contentHeight").toDouble()-flick->height()));
            QTest::qWait(100);
            auto top=panel->mapToScene(QPointF(0,0)).y();
            QVERIFY2(top>=0&&top+panel->height()<=window->height()+2,qPrintable(QString("AQ panel out of viewport: %1/%2").arg(top).arg(panel->height())));
            QVERIFY(window->grabWindow().save(prefix+suffix+".png"));
        };
        auto aq=airQuality();show(aq,1,1200,"-wide-fresh");show(aq,2,700,"-narrow-fresh");
        auto *us=named("airQualityValue_us");QVERIFY(us);QCOMPARE(us->property("text").toString(),QString("0"));
        auto *eu=named("airQualityValue_eu");QVERIFY(eu);QCOMPARE(eu->property("text").toString(),QString("125"));
        aq=airQuality(9000);aq["freshness"]="stale";aq["offline"]=true;aq["error"]="fetch_failed";show(aq,3,700,"-narrow-stale-offline");
        aq["freshness"]="expired";aq["age_seconds"]=25000;
        for(auto key:{"us_aqi","european_aqi","pm2_5_ug_m3"})aq[key]=QJsonValue::Null;
        show(aq,4,700,"-narrow-expired");
        us=named("airQualityValue_us");QVERIFY(us);QCOMPARE(us->property("text").toString(),QString("—"));
        auto unavailable=metricSnapshot(5);deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",unavailable}});
        us=named("airQualityValue_us");QVERIFY(us);QTRY_COMPARE(us->property("text").toString(),QString("—"));
        auto *status=named("airQualityStatus");QVERIFY(status);QVERIFY(status->property("text").toString().contains("unavailable"));
    }
    void renderPointMetricsScreenshots(){
        const QString prefix=qEnvironmentVariable("WEATHER_QT_METRICS_SCREENSHOT_PREFIX");
        if(prefix.isEmpty())QSKIP("Set WEATHER_QT_METRICS_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        QQuickWindow *window=nullptr;for(auto *candidate:QGuiApplication::allWindows())if(candidate->objectName()=="weatherWindow")window=qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);QTRY_VERIFY(window->isExposed());
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",metricSnapshot(1)}});
        root->setProperty("effectsOpen",false);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        auto *scroll=root->findChild<QObject*>("forecastScroll");QVERIFY(scroll);
        auto *flick=qvariant_cast<QQuickItem*>(scroll->property("contentItem"));QVERIFY(flick);
        auto toBottom=[flick](){flick->setProperty("contentY",qMax(0.0,flick->property("contentHeight").toDouble()-flick->height()));};
        window->resize(1200,850);QTest::qWait(120);toBottom();QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix+"-wide-current.png"));
        window->resize(700,850);QTest::qWait(120);toBottom();QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix+"-narrow-current.png"));
        QQmlExpression openDetail(QQmlEngine::contextForObject(root),root,"details.showHour(root.hours[0])");openDetail.evaluate();QVERIFY2(!openDetail.hasError(),qPrintable(openDetail.error().toString()));
        auto *details=root->findChild<QObject*>("forecastDetails");QVERIFY(details);QTRY_VERIFY(details->property("visible").toBool());
        auto *detailsScroll=root->findChild<QObject*>("forecastDetailsScroll");QVERIFY(detailsScroll);
        auto *detailsFlick=qvariant_cast<QQuickItem*>(detailsScroll->property("contentItem"));QVERIFY(detailsFlick);
        QTRY_VERIFY_WITH_TIMEOUT(detailsFlick->property("contentHeight").toDouble()>detailsFlick->height(),2000);
        detailsFlick->setProperty("contentY",qMax(0.0,detailsFlick->property("contentHeight").toDouble()-detailsFlick->height()));QTest::qWait(100);
        QVERIFY(window->grabWindow().save(prefix+"-narrow-hour.png"));
        QQmlExpression closeDetail(QQmlEngine::contextForObject(root),root,"details.close()");closeDetail.evaluate();QVERIFY2(!closeDetail.hasError(),qPrintable(closeDetail.error().toString()));
        auto unavailable=metricSnapshot(2);auto current=unavailable["current"].toObject();auto hourly=unavailable["hourly"].toArray();auto hour=hourly.first().toObject();
        for(auto key:{"uv_index","pressure_msl_hpa","dew_point_c"}){current[key]=QJsonValue::Null;hour[key]=QJsonValue::Null;}
        unavailable["current"]=current;hourly.replace(0,hour);unavailable["hourly"]=hourly;
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",unavailable}});
        toBottom();QTest::qWait(100);QVERIFY(window->grabWindow().save(prefix+"-narrow-unavailable.png"));
    }
    void renderSearchScreenshots(){
        const QString prefix=qEnvironmentVariable("WEATHER_QT_SCREENSHOT_PREFIX");
        if(prefix.isEmpty())QSKIP("Set WEATHER_QT_SCREENSHOT_PREFIX for private render capture");
        FakeTransport transport;QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
        engine.load(QUrl("qrc:/ui/qml/shell.qml"));QCOMPARE(engine.rootObjects().size(),1);auto *root=engine.rootObjects().first();
        QQuickWindow *window=nullptr;for(auto *candidate:QGuiApplication::allWindows())if(candidate->objectName()=="weatherWindow")window=qobject_cast<QQuickWindow*>(candidate);
        QVERIFY(window);QTRY_VERIFY(window->isExposed());
        auto base=snapshot(1,"Berlin, Berlin, Germany");
        base["alerts"]=QJsonObject{{"status","not_supported_here"},{"source",QJsonValue::Null},{"coverage","unsupported"},{"fetched_at",QJsonValue::Null},{"items",QJsonArray{}}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",base}});
        root->setProperty("effectsOpen",true);
        auto *query=root->findChild<QObject*>("placeQuery");QVERIFY(query);query->setProperty("text","Berlin");
        QTRY_VERIFY_WITH_TIMEOUT(!transport.requests.isEmpty(),1200);
        auto loading=base;loading["snapshot_revision"]=2;loading["place_search"]=QJsonObject{{"generation",1},{"client_token",1},{"status","loading"},{"results",QJsonArray{}},{"error",QJsonValue::Null}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",loading}});
        auto ready=loading;ready["snapshot_revision"]=3;
        ready["place_search"]=QJsonObject{{"generation",1},{"client_token",1},{"status","ready"},{"results",QJsonArray{QJsonObject{{"id",2950159},{"name","Berlin"},{"admin1","Berlin"},{"country","Germany"},{"country_code","DE"}},QJsonObject{{"id",2988507},{"name","Paris"},{"admin1","Île-de-France"},{"country","France"},{"country_code","FR"}}}},{"error",QJsonValue::Null}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",ready}});
        QTest::qWait(120);
        QVERIFY(QFileInfo(prefix).absoluteDir().mkpath("."));
        QVERIFY(window->grabWindow().save(prefix+"-results.png"));
        query->setProperty("text","Tokyo");QTest::qWait(450);
        auto error=ready;error["snapshot_revision"]=4;error["place_search"]=QJsonObject{{"generation",2},{"client_token",2},{"status","error"},{"results",QJsonArray{}},{"error","offline"}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",error}});
        QTest::qWait(120);QVERIFY(window->grabWindow().save(prefix+"-error.png"));
        root->setProperty("effectsOpen",false);
        QTest::qWait(120);QVERIFY(window->grabWindow().save(prefix+"-coverage.png"));
        root->setProperty("effectsOpen",true);
        auto *drawer=root->findChild<QObject*>("effectsDrawer");QVERIFY(drawer);QSignalSpy selections(drawer,SIGNAL(locationRequested(QVariant)));
        query->setProperty("text","Paris");QTest::qWait(450);
        auto keyboard=ready;keyboard["snapshot_revision"]=5;
        keyboard["place_search"]=QJsonObject{{"generation",3},{"client_token",3},{"status","ready"},{"results",QJsonArray{QJsonObject{{"id",2988507},{"name","Paris"},{"admin1","Île-de-France"},{"country","France"},{"country_code","FR"}}}},{"error",QJsonValue::Null}};
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",keyboard}});
        auto *queryItem=qobject_cast<QQuickItem*>(query);QVERIFY(queryItem);queryItem->forceActiveFocus();
        QTest::keyClick(window,Qt::Key_Down);
        auto *results=root->findChild<QObject*>("placeResults");QVERIFY(results);
        QCOMPARE(results->property("currentIndex").toInt(),0);QVERIFY(results->property("activeFocus").toBool());
        QVERIFY(window->grabWindow().save(prefix+"-keyboard.png"));
        QTest::keyClick(window,Qt::Key_Return);
        QCOMPARE(selections.size(),1);
        QCOMPARE(selections.first().first().toMap()["place_id"].toInt(),2988507);
        QCOMPARE(selections.first().first().toMap()["search_generation"].toInt(),3);
    }
};
QTEST_MAIN(FrontendTest)
#include "frontend_test.moc"
