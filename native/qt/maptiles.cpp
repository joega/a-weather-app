#include "maptiles.h"
#include <QNetworkDiskCache>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QStandardPaths>
#include <QDir>
#include <QImage>
#include <QUrl>
#include <QTimer>

MapTiles::MapTiles(QObject *parent):QObject(parent) {}

void MapTiles::request(int zoom,int x,int y,bool offline) {
    if(zoom<0||zoom>16||x<0||y<0||x>=(1<<zoom)||y>=(1<<zoom)||active.size()>=16)return;
    const QString key=QString::number(zoom)+"/"+QString::number(x)+"/"+QString::number(y);
    if(active.contains(key)||completed.contains(key)||failed.contains(key))return;
    if(!offline&&retryAfter.value(key)>QDateTime::currentDateTimeUtc())return;
    if(!cache) {
        const auto path=QStandardPaths::writableLocation(QStandardPaths::CacheLocation)+"/map-tiles";
        if(!QDir().mkpath(path))return;
        cache=new QNetworkDiskCache(this);cache->setCacheDirectory(path);cache->setMaximumCacheSize(32*1024*1024);
        manager.setCache(cache);
    }
    const auto url=QUrl(QStringLiteral("https://tile.openstreetmap.org/%1/%2/%3.png").arg(zoom).arg(x).arg(y));
    QNetworkRequest request(url);
    request.setRawHeader("User-Agent","a-weather-app/0.3 (Linux desktop local weather map)");
    request.setAttribute(QNetworkRequest::CacheLoadControlAttribute,offline?QNetworkRequest::AlwaysCache:QNetworkRequest::PreferCache);
    request.setMaximumRedirectsAllowed(0);
    auto *reply=manager.get(request);
    active.insert(key,reply);
    emit tileStarted(key);
    QTimer::singleShot(10000,reply,[reply]{if(reply->isRunning())reply->abort();});
    const quint64 issued=generation;
    connect(reply,&QNetworkReply::finished,this,[this,reply,key,issued,offline]{
        active.remove(key);
        if(issued==generation&&reply->error()==QNetworkReply::NoError&&reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt()==200&&reply->bytesAvailable()<=131072) {
            const auto bytes=reply->readAll();
            const auto image=QImage::fromData(bytes,"PNG");
            if(image.width()==256&&image.height()==256) {
                completed.insert(key);
                retryAfter.remove(key);
                emit tileReply(key,reply->attribute(QNetworkRequest::SourceIsFromCacheAttribute).toBool());
                emit tileReady(key,"data:image/png;base64,"+QString::fromLatin1(bytes.toBase64()));
            } else {failed.insert(key);retryAfter.insert(key,QDateTime::currentDateTimeUtc().addSecs(300));emit tileFailed(key,"invalid PNG dimensions");}
        } else if(issued==generation) {
            failed.insert(key);
            if(!offline)retryAfter.insert(key,QDateTime::currentDateTimeUtc().addSecs(300));
            emit tileFailed(key,reply->errorString()+" (HTTP "+QString::number(reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt())+")");
        }
        reply->deleteLater();
    });
}
void MapTiles::close() {
    ++generation;
    for(auto reply:active)if(reply)reply->abort();
    active.clear();
    completed.clear();
    failed.clear();
}
