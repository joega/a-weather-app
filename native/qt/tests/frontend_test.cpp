#include <QtTest>
#include <QQmlEngine>
#include <QQmlContext>
#include <QQmlComponent>
#include <QQmlExpression>
#include <QQmlApplicationEngine>
#include <QWindow>
#include <QJsonDocument>
#include <QJsonObject>

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

class FrontendTest:public QObject {
    Q_OBJECT
    QJsonObject snapshot(qint64 revision,const QString &name){
        const auto raw=QByteArray(R"({"schema_version":1,"location":{"name":"Current location","timezone":"UTC"},"current":null,"hourly":[],"daily":[],"alerts":{"status":"unavailable","items":[]},"source":{"name":"Open-Meteo","attribution":"Open-Meteo","freshness":"unavailable","age_seconds":null,"refreshing":false,"error":null},"controls":{"units":"F","mode":"live","strength":"normal","fps":30,"manual":{"condition":"rain"},"reduced_motion":false,"lightning_enabled":false,"window_physics":true,"accumulation":true,"pause_fullscreen":true},"atmosphere":{"rain_intensity":0,"snow_intensity":0,"cloud_cover":0.5,"fog_density":0,"sun_elevation":0,"sun_azimuth":180,"wind_x":0,"reduced_motion":false,"lightning_enabled":false,"thunderstorm":false},"effect_status":{"state":"stopped","remaining_seconds":0,"persistent":false}})");
        auto v=QJsonDocument::fromJson(raw).object();v["snapshot_revision"]=revision;
        auto location=v["location"].toObject();location["name"]=name;v["location"]=location;return v;
    }
    void deliver(FakeTransport &transport,const QJsonObject &v){emit transport.message(QString::fromUtf8(QJsonDocument(v).toJson(QJsonDocument::Compact)));}
    QVariant evaluate(QQmlEngine &engine,QObject *bridge,const QString &expression){QQmlExpression e(engine.rootContext(),bridge,expression);auto v=e.evaluate();if(e.hasError())qFatal("%s",qPrintable(e.error().toString()));return v;}
private slots:
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
        QCOMPARE(bridge->property("pending").toInt(),-1);QVERIFY(!bridge->property("busy").toBool());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Newest"));
        QVERIFY(evaluate(engine,bridge.data(),"send('set_controls',{units:'C'})").toBool());
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(3,"Latest")}});
        deliver(transport,{{"version",1},{"request_id",1},{"ok",false},{"error","invalid_controls"},{"snapshot",snapshot(2,"Stale")}});
        QCOMPARE(bridge->property("pending").toInt(),-1);QVERIFY(!bridge->property("error").toString().isEmpty());
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Latest"));
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(3,"Same revision")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("Latest"));
        emit transport.ready();QCOMPARE(bridge->property("lastSnapshotRevision").toDouble(),0.0);
        deliver(transport,{{"version",1},{"event","snapshot"},{"snapshot",snapshot(1,"New service")}});
        QCOMPARE(evaluate(engine,bridge.data(),"snapshot.location").toString(),QString("New service"));
    }
    void serviceStopped(){
        for(bool ok:{true,false}){
            FakeTransport transport;QQmlEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);
            QQmlComponent component(&engine,QUrl("qrc:/ui/qml/backend/Bridge.qml"));QScopedPointer<QObject> bridge(component.create());QVERIFY2(bridge,qPrintable(component.errorString()));
            QSignalSpy closed(bridge.data(),SIGNAL(closed(int)));QJsonObject event{{"version",1},{"event","service_stopped"},{"ok",ok}};if(!ok)event["error"]="cleanup_failed";deliver(transport,event);
            QCOMPARE(closed.size(),1);QCOMPARE(closed.first().first().toInt(),ok?0:1);QCOMPARE(transport.requests.size(),0);
        }
    }
};
QTEST_MAIN(FrontendTest)
#include "frontend_test.moc"
