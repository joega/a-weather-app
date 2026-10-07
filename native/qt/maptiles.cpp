#include "maptiles.h"
#include <QNetworkDiskCache>
#include <QNetworkReply>
#include <QNetworkRequest>
#include <QStandardPaths>
#include <QDir>
#include <QImage>
#include <QImageReader>
#include <QBuffer>
#include <QUrl>
#include <QTimer>
#include <cstring>
#include <memory>
#include <utility>

namespace {
constexpr qint64 maxTileBytes = 128 * 1024;
constexpr qint64 maxDecodedTileBytes = 512 * 1024;
// Admit only pixel data and bounded, uncompressed color/palette information.
// In particular, no text, ICC profile or EXIF bytes reach either Qt decoder.
// The entire scan is linear in the already bounded 128 KiB input, with no
// decompression or allocations. CRCs and chunk framing are checked first.
quint32 pngWord(const char* p) {
    const auto* b = reinterpret_cast<const unsigned char*>(p);
    return (quint32(b[0]) << 24) | (quint32(b[1]) << 16) | (quint32(b[2]) << 8) | b[3];
}
quint32 pngCRC(const char* p, qsizetype size) {
    quint32 crc = 0xffffffffU;
    for (qsizetype i = 0; i < size; ++i) {
        crc ^= static_cast<unsigned char>(p[i]);
        for (int bit = 0; bit < 8; ++bit)
            crc = (crc >> 1) ^ ((crc & 1U) ? 0xedb88320U : 0U);
    }
    return crc ^ 0xffffffffU;
}
bool boundedTilePNG(const QByteArray& bytes) {
    if (bytes.size() < 8 || bytes.size() > maxTileBytes ||
        std::memcmp(bytes.constData(), "\x89PNG\r\n\x1a\n", 8) != 0)
        return false;
    qsizetype offset = 8;
    bool pixels = false;
    while (offset < bytes.size()) {
        const qsizetype remaining = bytes.size() - offset;
        if (remaining < 12)
            return false;
        const auto* chunk = bytes.constData() + offset;
        const quint32 length = pngWord(chunk);
        if (length > quint32(remaining - 12) ||
            pngCRC(chunk + 4, qsizetype(length) + 4) != pngWord(chunk + 8 + length))
            return false;
        const auto isType = [chunk](const char* type) {
            return std::memcmp(chunk + 4, type, 4) == 0;
        };
        if (offset == 8) {
            if (!isType("IHDR") || length != 13 || pngWord(chunk + 8) != 256 ||
                pngWord(chunk + 12) != 256)
                return false;
        } else if (isType("IDAT")) {
            pixels = true;
        } else if (isType("IEND")) {
            return pixels && length == 0 && offset + 12 == bytes.size();
        } else if (!((isType("PLTE") && length > 0 && length <= 768 && length % 3 == 0) ||
                     (isType("tRNS") && length > 0 && length <= 256) ||
                     (isType("sRGB") && length == 1) || (isType("gAMA") && length == 4) ||
                     (isType("cHRM") && length == 32) || (isType("pHYs") && length == 9) ||
                     (isType("sBIT") && length > 0 && length <= 4))) {
            return false;
        }
        offset += qsizetype(length) + 12;
    }
    return false;
}
bool validTilePNG(const QByteArray& bytes) {
    if (!boundedTilePNG(bytes))
        return false;
    QBuffer buffer;
    buffer.setData(bytes);
    if (!buffer.open(QIODevice::ReadOnly))
        return false;
    QImageReader reader(&buffer, "PNG");
    reader.setAutoDetectImageFormat(false);
    reader.setDecideFormatFromContent(false);
    const QSize dimensions = reader.size();
    // PNG supports at most 64 bits per pixel. Bound the allocation before read().
    if (reader.format().toLower() != "png" || dimensions != QSize(256, 256) ||
        qint64(dimensions.width()) * dimensions.height() * 8 > maxDecodedTileBytes)
        return false;
    const auto image = reader.read();
    return !image.isNull() && image.size() == dimensions &&
           image.sizeInBytes() <= maxDecodedTileBytes;
}
struct TileDownload {
    QByteArray bytes;
    bool tooLarge = false;
};
} // namespace

MapTiles::MapTiles(QObject* parent, QUrl tileServer, std::function<QDateTime()> clock)
    : QObject(parent), tileServer(std::move(tileServer)), clock(std::move(clock)) {}

MapTiles::~MapTiles() {
    // QObject base destruction happens after our member containers and clock.
    // Disconnect all replies (including those waiting for deleteLater) first.
    const auto replies = manager.findChildren<QNetworkReply*>();
    for (auto* reply : replies)
        QObject::disconnect(reply, nullptr, this, nullptr);
    for (auto* reply : replies)
        if (reply->isRunning())
            reply->abort();
}

void MapTiles::pruneCooldowns(const QDateTime& now) {
    for (auto it = retryAfter.begin(); it != retryAfter.end();) {
        if (it.value() <= now)
            it = retryAfter.erase(it);
        else
            ++it;
    }
}
void MapTiles::rememberFailure(const QString& key) {
    const auto now = clock();
    pruneCooldowns(now);
    if (retryAfter.contains(key) || retryAfter.size() < maxCooldownEntries)
        retryAfter.insert(key, now.addSecs(300));
}

void MapTiles::request(int zoom, int x, int y, bool offline) {
    if (zoom < 0 || zoom > 16 || x < 0 || y < 0 || x >= (1 << zoom) || y >= (1 << zoom) ||
        active.size() >= 16)
        return;
    const QString key = QString::number(zoom) + "/" + QString::number(x) + "/" + QString::number(y);
    if (active.contains(key) || completed.contains(key) || failed.contains(key))
        return;
    pruneCooldowns(clock());
    if (!offline &&
        (retryAfter.contains(key) || retryAfter.size() + active.size() >= maxCooldownEntries))
        return;
    if (!cache) {
        const auto path =
            QStandardPaths::writableLocation(QStandardPaths::CacheLocation) + "/map-tiles";
        if (!QDir().mkpath(path))
            return;
        cache = new QNetworkDiskCache(this);
        cache->setCacheDirectory(path);
        cache->setMaximumCacheSize(32 * 1024 * 1024);
        manager.setCache(cache);
    }
    const auto url = QUrl(QStringLiteral("%1/%2/%3/%4.png")
                              .arg(tileServer.toString(), QString::number(zoom), QString::number(x),
                                   QString::number(y)));
    QNetworkRequest request(url);
    request.setRawHeader("User-Agent", "a-weather-app/0.3 (Linux desktop local weather map)");
    request.setAttribute(QNetworkRequest::CacheLoadControlAttribute,
                         offline ? QNetworkRequest::AlwaysCache : QNetworkRequest::PreferCache);
    request.setMaximumRedirectsAllowed(0);
    auto* reply = manager.get(request);
    reply->setReadBufferSize(16 * 1024);
    auto download = std::make_shared<TileDownload>();
    const auto rejectOversized = [reply, download] {
        if (download->tooLarge)
            return;
        download->tooLarge = true;
        if (reply->isRunning())
            reply->abort();
    };
    const auto drain = [reply, download, rejectOversized] {
        if (download->tooLarge)
            return;
        const qint64 remaining = maxTileBytes - download->bytes.size();
        if (reply->bytesAvailable() > remaining) {
            rejectOversized();
            return;
        }
        while (reply->bytesAvailable() > 0) {
            const auto chunk =
                reply->read(qMin(reply->bytesAvailable(), maxTileBytes - download->bytes.size()));
            if (chunk.isEmpty())
                break;
            download->bytes.append(chunk);
        }
    };
    active.insert(key, reply);
    const quint64 issued = generation;
    connect(reply, &QNetworkReply::finished, this,
            [this, reply, key, issued, offline, download, drain] {
                QPointer<MapTiles> owner(this);
                QPointer<QNetworkReply> pendingReply(reply);
                drain();
                if (!owner || !pendingReply)
                    return;
                if (active.value(key) == reply)
                    active.remove(key);
                if (issued == generation && !download->tooLarge &&
                    reply->error() == QNetworkReply::NoError &&
                    reply->attribute(QNetworkRequest::HttpStatusCodeAttribute).toInt() == 200) {
                    const auto& bytes = download->bytes;
                    if (validTilePNG(bytes)) {
                        completed.insert(key);
                        retryAfter.remove(key);
                        emit tileReply(
                            key,
                            reply->attribute(QNetworkRequest::SourceIsFromCacheAttribute).toBool());
                        // An observer can close/reopen or destroy the client synchronously.
                        if (owner && issued == generation)
                            emit tileReady(key, "data:image/png;base64," +
                                                    QString::fromLatin1(bytes.toBase64()));
                    } else {
                        failed.insert(key);
                        if (!offline)
                            rememberFailure(key);
                        emit tileFailed(key, "invalid PNG dimensions or payload");
                    }
                } else if (issued == generation) {
                    failed.insert(key);
                    if (!offline)
                        rememberFailure(key);
                    emit tileFailed(
                        key,
                        download->tooLarge
                            ? QStringLiteral("tile exceeds 128 KiB")
                            : reply->errorString() + " (HTTP " +
                                  QString::number(
                                      reply->attribute(QNetworkRequest::HttpStatusCodeAttribute)
                                          .toInt()) +
                                  ")");
                }
                if (pendingReply)
                    pendingReply->deleteLater();
            });
    connect(reply, &QIODevice::readyRead, this, drain);
    connect(reply, &QNetworkReply::metaDataChanged, this, [reply, rejectOversized] {
        if (reply->header(QNetworkRequest::ContentLengthHeader).toLongLong() > maxTileBytes)
            rejectOversized();
    });
    connect(reply, &QNetworkReply::downloadProgress, this,
            [rejectOversized](qint64 received, qint64) {
                if (received > maxTileBytes)
                    rejectOversized();
            });
    QTimer::singleShot(10000, reply, [reply] {
        if (reply->isRunning())
            reply->abort();
    });
    emit tileStarted(key);
}
void MapTiles::close() {
    ++generation;
    // abort() can emit finished synchronously; callbacks remove entries from active.
    const auto pending = active.values();
    active.clear();
    for (auto reply : pending)
        if (reply)
            reply->abort();
    completed.clear();
    failed.clear();
    pruneCooldowns(clock());
}
