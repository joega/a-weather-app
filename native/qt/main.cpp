#include "transport.h"
#include "maptiles.h"
#include <QGuiApplication>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QCommandLineParser>
#include <QSocketNotifier>
#include <QQuickWindow>
#include <QElapsedTimer>
#include <QDateTime>
#include <QTimer>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <algorithm>
#include <csignal>
#include <cerrno>
#include <fcntl.h>
#include <unistd.h>
#include <cstdio>

namespace {volatile std::sig_atomic_t signalWrite=-1;void termination(int){const int savedErrno=errno;const int descriptor=signalWrite;char byte=1;if(descriptor>=0){auto ignored=write(descriptor,&byte,1);(void)ignored;}errno=savedErrno;}}
int main(int argc,char **argv){
    QGuiApplication app(argc,argv);app.setApplicationName("a-weather-app");app.setDesktopFileName("a-weather-app");app.setQuitOnLastWindowClosed(false);
    QCommandLineParser args;args.addHelpOption();args.addOption({"socket","Owned weather service socket","path"});args.addOption({"diagnostic","Log frontend diagnostics"});args.addOption({"measure-frames","Report bounded frameSwapped callback intervals at exit (not GPU time)"});args.addOption({"measure-map-open","Development measurement: scroll to the maps after the first snapshot"});args.process(app);
    if(!args.isSet("socket")||!args.value("socket").startsWith('/'))return 2;
    if(args.isSet("diagnostic"))qInstallMessageHandler([](QtMsgType,const QMessageLogContext &,const QString &message){const auto bytes=message.left(4096).toUtf8();fprintf(stderr,"%s\n",bytes.constData());});
    WeatherTransport transport(args.value("socket"),args.isSet("diagnostic"));
    MapTiles mapTiles;
    QQmlApplicationEngine engine;engine.rootContext()->setContextProperty("weatherTransport",&transport);engine.rootContext()->setContextProperty("mapTiles",&mapTiles);
    QObject::connect(&engine,&QQmlApplicationEngine::objectCreationFailed,&app,[]{QCoreApplication::exit(1);},Qt::QueuedConnection);
    int pipes[2];if(pipe2(pipes,O_NONBLOCK|O_CLOEXEC)!=0)return 1;signalWrite=pipes[1];
    QSocketNotifier signalReader(pipes[0],QSocketNotifier::Read);QObject::connect(&signalReader,&QSocketNotifier::activated,&transport,[&]{char bytes[32];while(read(pipes[0],bytes,sizeof bytes)>0){}transport.requestShutdown();});
    std::signal(SIGTERM,termination);std::signal(SIGINT,termination);
    engine.load(QUrl("qrc:/ui/qml/shell.qml"));if(args.isSet("diagnostic"))fprintf(stderr,"Weather frontend roots: %lld\n",static_cast<long long>(engine.rootObjects().size()));
    if(args.isSet("measure-map-open")&&!engine.rootObjects().isEmpty()) {
        auto *root=engine.rootObjects().first();auto *bridge=root->property("backend").value<QObject*>();
        if(!bridge)return 3;
        auto *opener=new QTimer(&app);opener->setInterval(30);
        QObject::connect(opener,&QTimer::timeout,&app,[root,bridge,opener]{
            if(!bridge->property("snapshot").isValid()||bridge->property("snapshot").isNull())return;
            auto *section=root->findChild<QObject*>("weatherMaps");
            auto *scroll=root->findChild<QObject*>("forecastScroll");
            auto *flick=scroll?scroll->property("contentItem").value<QObject*>():nullptr;
            if(!section||!flick)return;
            root->setProperty("effectsOpen",false);
            flick->setProperty("contentY",section->property("y").toReal()+40);
            if(!bridge->property("mapWanted").toBool())return;
            fprintf(stderr,"Weather map measurement opened\n");opener->stop();
        });
        opener->start();
    }
    QVector<double> frameIntervals;QJsonArray callbacks;QElapsedTimer frameClock;bool frameStarted=false,framesCapped=false;
    if(args.isSet("measure-frames"))for(auto *window:QGuiApplication::allWindows())if(auto *quick=qobject_cast<QQuickWindow*>(window))QObject::connect(quick,&QQuickWindow::frameSwapped,&app,[&]{if(callbacks.size()<10000){callbacks.append(QDateTime::currentMSecsSinceEpoch());if(frameStarted)frameIntervals.append(frameClock.nsecsElapsed()/1000000.0);}else framesCapped=true;frameClock.restart();frameStarted=true;},Qt::QueuedConnection);
    const int result=app.exec();
    if(args.isSet("measure-frames")){std::sort(frameIntervals.begin(),frameIntervals.end());const auto count=frameIntervals.size();const double p99=count?frameIntervals[std::min<qsizetype>(count-1,(count*99+99)/100-1)]:0;fprintf(stderr,"Weather frameSwapped callback intervals: samples=%lld p99_ms=%.3f max_ms=%.3f (not GPU time; includes hidden/idle gaps; capped at 10000)\n",static_cast<long long>(count),p99,count?frameIntervals.last():0);}
    if(args.isSet("measure-frames")){const auto report=QJsonDocument(QJsonObject{{"callbacks",callbacks},{"capped",framesCapped},{"resolution_ms",1}}).toJson(QJsonDocument::Compact);fprintf(stderr,"Weather frame callbacks JSON: %s\n",report.constData());}
    signalWrite=-1;close(pipes[0]);close(pipes[1]);return result;
}
