#include <QtTest>
#include "transport.h"
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQmlExpression>
#include <QQuickWindow>
#include <QQuickItem>
#include <QProcess>
#include <QTemporaryDir>
#include <QFile>
#include <QJsonDocument>
#include <QJsonObject>
#include <QJsonArray>
#include <QDir>
#include <memory>

// Private offline fixtures have no precipitation and never request native effects.
// Each test owns its service process; even failed assertions leave no service running.
class ServiceFixture {
public:
    QTemporaryDir directory;
    QString state,socket;
    QProcess service;
    ServiceFixture(){state=directory.path()+"/state";QDir().mkdir(state);QFile::setPermissions(state,QFile::ReadOwner|QFile::WriteOwner|QFile::ExeOwner);}
    ~ServiceFixture(){if(service.state()!=QProcess::NotRunning){service.terminate();if(!service.waitForFinished(3000)){service.kill();service.waitForFinished(2000);}}}
    void save(const QString &name,const QJsonObject &object){QFile file(state+"/"+name);if(!file.open(QIODevice::WriteOnly))qFatal("Fixture write failed");file.setPermissions(QFile::ReadOwner|QFile::WriteOwner);file.write(QJsonDocument(object).toJson(QJsonDocument::Compact));}
    bool start(bool offline=true){
        const QString app=qEnvironmentVariable("GO_APP");
        QProcess query;query.start(app,{"--print-socket","--state-dir",state});if(!query.waitForFinished(3000)||query.exitCode()!=0)return false;
        socket=QString::fromUtf8(query.readAllStandardOutput()).trimmed();
        auto env=QProcessEnvironment::systemEnvironment();env.remove("HYPRLAND_INSTANCE_SIGNATURE");env.remove("WAYLAND_DISPLAY");env.remove("DISPLAY");env.remove("DBUS_SESSION_BUS_ADDRESS");service.setProcessEnvironment(env);
        service.setProcessChannelMode(QProcess::MergedChannels);
        QStringList arguments{"--service","--headless","--duration",offline?"60":"120","--state-dir",state};
        if(offline)arguments.append("--offline");
        service.start(app,arguments);
        if(!service.waitForStarted(3000))return false;
        QElapsedTimer timer;timer.start();while(timer.elapsed()<5000&&!QFile::exists(socket)&&service.state()!=QProcess::NotRunning)QTest::qWait(10);
        return QFile::exists(socket);
    }
    QJsonObject saved(const QString &name){QFile f(state+"/"+name);if(!f.open(QIODevice::ReadOnly))return {};return QJsonDocument::fromJson(f.readAll()).object();}
    void cache(int age){
        auto now=QDateTime::currentDateTimeUtc();QString stamp=now.toString(Qt::ISODate);
        QJsonObject location{{"name","New York, NY"},{"latitude",40.7128},{"longitude",-74.006},{"timezone","America/New_York"}};
        QJsonObject row{{"time",stamp},{"condition","clear"},{"is_day",true},{"temperature_c",15},{"apparent_temperature_c",14},{"humidity",.5},{"cloud_cover",.2},{"precipitation_rate_mm_hr",0},{"precipitation_probability",0},{"visibility_m",10000},{"wind_speed_m_s",1},{"wind_direction_deg",180},{"wind_gust_m_s",2}};
        QJsonObject day{{"date",now.date().toString(Qt::ISODate)},{"condition","clear"},{"high_c",20},{"low_c",10},{"precipitation_probability",0},{"sunrise",QJsonValue::Null},{"sunset",QJsonValue::Null}};
        auto hour=row;hour["time"]=now.addSecs(3600).toString(Qt::ISODate);
        save("forecast.json",{{"schema_version",1},{"location",location},{"fetched_at",now.addSecs(-age).toString(Qt::ISODate)},{"source",QJsonObject{{"name","Open-Meteo"},{"attribution","Weather data by Open-Meteo.com (CC BY 4.0)"}}},{"current",row},{"hourly",QJsonArray{hour}},{"daily",QJsonArray{day}},{"alerts",QJsonObject{{"status","unavailable"},{"items",QJsonArray{}}}}});
    }
    void savedBerlinPlace(){
        cache(60);
        auto forecast=saved("forecast.json");
        QJsonObject location{{"name","Berlin, Berlin, Germany"},{"latitude",52.52},{"longitude",13.41},{"timezone","Europe/Berlin"}};
        forecast["location"]=location;
        save("location-profile.json",{{"schema_version",2},{"mode","place"},{"zip_code",QJsonValue::Null},{"location",location},{"forecast",forecast},{"country_code","DE"},{"place",QJsonObject{{"provider","open-meteo"},{"id",2950159}}}});
    }
};

class ServiceFrontendTest:public QObject {
    Q_OBJECT
    std::unique_ptr<WeatherTransport> transport;
    std::unique_ptr<QQmlApplicationEngine> engine;
    QObject *root=nullptr;
    QQuickWindow *window=nullptr;
    QVariant eval(const QString &source){QQmlExpression expression(QQmlEngine::contextForObject(root),root,source);auto result=expression.evaluate();if(expression.hasError())QTest::qFail(qPrintable(expression.error().toString()),__FILE__,__LINE__);return result;}
    bool attach(ServiceFixture &fixture){
        transport=std::make_unique<WeatherTransport>(fixture.socket,false);
        engine=std::make_unique<QQmlApplicationEngine>();engine->rootContext()->setContextProperty("weatherTransport",transport.get());
        // Capture exit requests without stopping the QtTest application's event loop.
        QObject::disconnect(engine.get(),nullptr,QCoreApplication::instance(),nullptr);
        engine->load(QUrl("qrc:/ui/qml/shell.qml"));if(engine->rootObjects().size()!=1)return false;root=engine->rootObjects().first();
        window=qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());return window!=nullptr;
    }
    QObject *named(const char *name){auto object=root->findChild<QObject*>(name);if(!object){QTest::qFail(qPrintable(QString("Missing UI control %1").arg(name)),__FILE__,__LINE__);return root;}return object;}
    void click(const char *name){auto item=qobject_cast<QQuickItem*>(named(name));QVERIFY(item);QVERIFY(item->isEnabled());QVERIFY(item->isVisible());auto point=item->mapToScene(QPointF(item->width()/2,item->height()/2)).toPoint();QTest::mouseClick(window,Qt::LeftButton,Qt::NoModifier,point);}
    void toggle(const char *name,bool checked){auto object=named(name);QVERIFY(object->property("enabled").toBool());object->setProperty("checked",checked);QVERIFY(QMetaObject::invokeMethod(object,"clicked",Qt::DirectConnection));}
private slots:
    void initTestCase(){QVERIFY2(!qEnvironmentVariable("GO_APP").isEmpty(),"GO_APP must name the compiled Go service");QGuiApplication::setQuitOnLastWindowClosed(false);}
    void cleanup(){engine.reset();transport.reset();root=nullptr;window=nullptr;}
    void emptyOfflineWindow(){
        ServiceFixture fixture;QVERIFY2(fixture.start(),qPrintable(fixture.service.readAll()));QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(window->isVisible()&&window->isExposed(),3000);
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);
        QVERIFY(eval("root.freshness").toString().contains("Choose a city"));
        QVERIFY(!QFile::exists(fixture.state+"/notifications.json"));
        QVERIFY(!eval("root.liveDesktop").toBool());
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");
        QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);QCOMPARE(exit.first().first().toInt(),0);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(),QProcess::NotRunning,5000);QCOMPARE(fixture.service.exitCode(),0);
        QVERIFY(!QFile::exists(fixture.socket));
    }
    void multiMonitorCompatibilitySnapshot(){
        ServiceFixture fixture;QVERIFY(fixture.start());QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);
        const auto ready=eval("Forecast.effectsSetup({status:'ready',reason:'ready',outputs:[{name:'eDP-1',width:1920,height:1200,scale:1.25,enabled:true},{name:'DP-1',width:3840,height:2160,scale:2,enabled:true}],selected_output:'DP-1'}).status");
        QCOMPARE(ready.toString(),QString("ready"));
        const auto choose=eval("Forecast.effectsSetup({status:'unavailable',reason:'output_selection_required',outputs:[{name:'eDP-1',width:1920,height:1200,scale:1.25,enabled:true},{name:'DP-1',width:3840,height:2160,scale:2,enabled:true}],selected_output:null}).reason");
        QCOMPARE(choose.toString(),QString("output_selection_required"));
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);
    }
    void cacheFreshness_data(){QTest::addColumn<int>("age");QTest::addColumn<QString>("prefix");QTest::newRow("fresh-offline")<<60<<QString("Weather data from");QTest::newRow("stale-offline")<<4000<<QString("Stale forecast");}
    void cacheFreshness(){
        QFETCH(int,age);QFETCH(QString,prefix);ServiceFixture fixture;fixture.cache(age);QVERIFY(fixture.start());QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);QTRY_VERIFY(window->isExposed());
        QVERIFY2(eval("root.freshness").toString().startsWith(prefix),qPrintable(eval("root.freshness").toString()));
        QCOMPARE(eval("root.current.temperature_c").toDouble(),15.0);QCOMPARE(eval("root.days.length").toInt(),1);QCOMPARE(eval("root.hours.length").toInt(),1);
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(),QProcess::NotRunning,5000);
    }
    void offlinePlaceAndSearchCoverage(){
        ServiceFixture fixture;fixture.savedBerlinPlace();QVERIFY2(fixture.start(),qPrintable(fixture.service.readAll()));QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);
        QCOMPARE(eval("root.location").toString(),QString("Berlin, Berlin, Germany"));
        QCOMPARE(eval("bridge.snapshot.location_settings.mode").toString(),QString("place"));
        QCOMPARE(eval("bridge.snapshot.location_settings.place.id").toInt(),2950159);
        QCOMPARE(eval("bridge.snapshot.alerts.status").toString(),QString("not_supported_here"));
        QCOMPARE(eval("bridge.snapshot.alerts.coverage").toString(),QString("unsupported"));
        QVERIFY(named("alertCoverageStatus")->property("text").toString().contains("not supported"));
        QVERIFY(!named("sourceAttribution")->property("text").toString().contains("National Weather Service"));
        root->setProperty("effectsOpen",true);
        named("placeQuery")->setProperty("text","Berlin");
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status").toString(),QString("error"),5000);
        QCOMPARE(eval("bridge.snapshot.place_search.error").toString(),QString("offline"));
        QVERIFY(named("placeSearchStatus")->property("text").toString().contains("offline"));
        root->setProperty("effectsOpen",false);
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status").toString(),QString("idle"),3000);
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);
    }
    void liveBerlinPicker(){
        if(qEnvironmentVariable("A_WEATHER_APP_LIVE_PLACES")!="1")QSKIP("Opt-in live provider UI test");
        ServiceFixture fixture;QVERIFY2(fixture.start(false),qPrintable(fixture.service.readAll()));QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);QTRY_VERIFY(window->isExposed());
        root->setProperty("effectsOpen",true);
        auto *query=qobject_cast<QQuickItem*>(named("placeQuery"));QVERIFY(query);
        named("placeCountry")->setProperty("text","DE");query->setProperty("text","Berlin");
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot.place_search.status==='ready' && bridge.snapshot.place_search.results.length>0").toBool(),20000);
        QCOMPARE(eval("bridge.snapshot.place_search.results[0].country_code").toString(),QString("DE"));
        query->forceActiveFocus();QTest::keyClick(window,Qt::Key_Down);
        QCOMPARE(named("placeResults")->property("currentIndex").toInt(),0);
        QTest::keyClick(window,Qt::Key_Return);
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.location_settings.mode").toString(),QString("place"),30000);
        QCOMPARE(eval("bridge.snapshot.location_settings.country_code").toString(),QString("DE"));
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.current!==null").toBool(),30000);
        QCOMPARE(eval("bridge.snapshot.alerts.status").toString(),QString("not_supported_here"));
        QVERIFY(named("alertCoverageStatus")->property("text").toString().contains("not supported"));
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);
    }
    void cachedControlsDetailsAndReload(){
        ServiceFixture fixture;fixture.cache(4000);QVERIFY2(fixture.start(),qPrintable(fixture.service.readAll()));QVERIFY(attach(fixture));
        QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);QTRY_VERIFY(window->isExposed());
        QVERIFY(eval("root.freshness").toString().startsWith("Stale forecast"));QCOMPARE(eval("root.hours.length").toInt(),1);
        root->setProperty("effectsOpen",false);click("unitsC");QTRY_COMPARE_WITH_TIMEOUT(eval("root.units").toString(),QString("C"),3000);
        QCOMPARE(fixture.saved("controls.json")["units"].toString(),QString("C"));
        eval("details.showHour(root.hours[0])");QTRY_VERIFY(named("forecastDetails")->property("visible").toBool());click("closeForecastDetails");
        click("openEffects");QTRY_VERIFY(named("effectsDrawer")->property("visible").toBool());
        toggle("reducedMotion",true);QTRY_VERIFY_WITH_TIMEOUT(eval("root.controls.reduced_motion").toBool(),3000);
        toggle("windowPhysics",false);QTRY_VERIFY_WITH_TIMEOUT(!eval("root.controls.window_physics").toBool(),3000);
        QCOMPARE(fixture.saved("controls.json")["reduced_motion"].toBool(),true);
        // UI signal dispatch for drawer switches tests the actual onClicked persistence path.
        toggle("notificationQuiet",false);QTRY_VERIFY_WITH_TIMEOUT(!eval("bridge.snapshot.notifications.settings.quiet_enabled").toBool(),3000);
        QVERIFY2(eval("bridge.snapshot.notifications.supported").toBool(),"Full notification UI acceptance requires the optional libnotify test dependency");
        toggle("notificationEnabled",true);
        QTRY_VERIFY_WITH_TIMEOUT(eval("root.watchingPrecipitation").toBool(),3000);
        QVERIFY(QMetaObject::invokeMethod(named("notificationPause"),"clicked",Qt::DirectConnection));
        QTRY_COMPARE_WITH_TIMEOUT(eval("bridge.snapshot.notifications.state").toString(),QString("paused"),3000);
        QVERIFY(fixture.saved("notifications.json")["snoozed_until"].toDouble()>QDateTime::currentSecsSinceEpoch());
        eval("root.dismissWindow()");QTRY_VERIFY(!window->isVisible());QCOMPARE(fixture.service.state(),QProcess::Running);
        QProcess toggleProcess;toggleProcess.start(qEnvironmentVariable("GO_APP"),{"--toggle-window","--state-dir",fixture.state});QVERIFY(toggleProcess.waitForFinished(3000));QCOMPARE(toggleProcess.exitCode(),0);
        QTRY_VERIFY_WITH_TIMEOUT(window->isVisible()&&window->isExposed(),3000);
        QSignalSpy exit(engine.get(),SIGNAL(exit(int)));eval("bridge.shutdown()");QTRY_COMPARE_WITH_TIMEOUT(exit.size(),1,5000);QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(),QProcess::NotRunning,5000);
        engine.reset();transport.reset();QVERIFY(fixture.start());QVERIFY(attach(fixture));QTRY_VERIFY_WITH_TIMEOUT(eval("bridge.snapshot!==null").toBool(),5000);
        QCOMPARE(eval("root.units").toString(),QString("C"));QVERIFY(eval("root.controls.reduced_motion").toBool());QVERIFY(!eval("root.controls.window_physics").toBool());QVERIFY(eval("root.watchingPrecipitation").toBool());QCOMPARE(eval("bridge.snapshot.notifications.state").toString(),QString("paused"));
        eval("effects.notificationsPatch({enabled:false})");QTRY_VERIFY_WITH_TIMEOUT(!eval("root.watchingPrecipitation").toBool(),3000);
        QSignalSpy finalExit(engine.get(),SIGNAL(exit(int)));eval("root.dismissWindow()");QTRY_COMPARE_WITH_TIMEOUT(finalExit.size(),1,5000);QCOMPARE(finalExit.first().first().toInt(),0);QTRY_COMPARE_WITH_TIMEOUT(fixture.service.state(),QProcess::NotRunning,5000);QVERIFY(!QFile::exists(fixture.socket));
    }
};
QTEST_MAIN(ServiceFrontendTest)
#include "service_frontend_test.moc"
