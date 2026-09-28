#pragma once
#include <QObject>
#include <QNetworkAccessManager>
#include <QHash>
#include <QSet>
#include <QDateTime>
#include <QPointer>
class QNetworkDiskCache;
class QNetworkReply;

class MapTiles : public QObject {
    Q_OBJECT
public:
    explicit MapTiles(QObject *parent=nullptr);
    Q_INVOKABLE void request(int zoom,int x,int y,bool offline);
    Q_INVOKABLE void close();
signals:
    void tileReady(const QString &key,const QString &dataURL);
    void tileFailed(const QString &key,const QString &reason);
    void tileStarted(const QString &key);
    void tileReply(const QString &key,bool fromCache);
private:
    QNetworkAccessManager manager;
    QNetworkDiskCache *cache=nullptr;
    QHash<QString,QPointer<QNetworkReply>> active;
    QSet<QString> completed;
    QSet<QString> failed;
    QHash<QString,QDateTime> retryAfter;
    quint64 generation=0;
};
