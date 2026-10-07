#pragma once
#include <QObject>
#include <QNetworkAccessManager>
#include <QHash>
#include <QSet>
#include <QDateTime>
#include <QPointer>
#include <QUrl>
#include <functional>
class QNetworkDiskCache;
class QNetworkReply;

class MapTiles : public QObject {
    Q_OBJECT
  public:
    explicit MapTiles(QObject* parent = nullptr,
                      QUrl tileServer = QUrl(QStringLiteral("https://tile.openstreetmap.org")),
                      std::function<QDateTime()> clock = QDateTime::currentDateTimeUtc);
    // GUI-thread only. Destruction quiesces replies before callback state dies.
    ~MapTiles() override;
    Q_INVOKABLE void request(int zoom, int x, int y, bool offline);
    Q_INVOKABLE void close();
  signals:
    void tileReady(const QString& key, const QString& dataURL);
    void tileFailed(const QString& key, const QString& reason);
    void tileStarted(const QString& key);
    void tileReply(const QString& key, bool fromCache);

  private:
    friend class FrontendTest;
    static constexpr int maxCooldownEntries = 512;
    void pruneCooldowns(const QDateTime& now);
    void rememberFailure(const QString& key);
    QNetworkAccessManager manager;
    QNetworkDiskCache* cache = nullptr;
    QHash<QString, QPointer<QNetworkReply>> active;
    QSet<QString> completed;
    QSet<QString> failed;
    QHash<QString, QDateTime> retryAfter;
    QUrl tileServer;
    std::function<QDateTime()> clock;
    quint64 generation = 0;
};
