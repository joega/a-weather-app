#include "radarimages.h"
#include "pngvalidation.h"
#include "protocol.h"
#include <QBuffer>
#include <QElapsedTimer>
#include <QImageReader>
#include <QJsonDocument>
#include <QLocalSocket>
#include <cmath>
#include <sys/socket.h>
#include <unistd.h>

namespace {
constexpr int chunkBytes = 96 * 1024, maxImageBytes = 2 * 1024 * 1024;
bool integer(const QJsonValue& value, int low, int high) {
    const auto n = value.toDouble(-1);
    return value.isDouble() && std::isfinite(n) && n >= low && n <= high && std::floor(n) == n;
}
bool imageID(const QString& id) {
    if (id.size() != 64)
        return false;
    for (const auto c : id)
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')))
            return false;
    return true;
}
} // namespace
RadarImageProvider::RadarImageProvider(QString path, std::shared_ptr<RadarImageDemand> state)
    : QQuickImageProvider(QQuickImageProvider::Image,
                          QQmlImageProviderBase::ForceAsynchronousImageLoading),
      socketPath(std::move(path)), demand(std::move(state)) {}
QImage RadarImageProvider::requestImage(const QString& request, QSize* size, const QSize&) {
    // The last component is a local reload token; no URL or filesystem path
    // from QML is used as an image source or socket target.
    if (request.size() > 100)
        return {};
    const auto parts = request.split('/');
    if (parts.size() != 3 || !imageID(parts[1]) || parts[2].isEmpty() || parts[2].size() > 16)
        return {};
    for (const auto c : parts[2])
        if (c < '0' || c > '9')
            return {};
    const bool legend = parts[0] == "legend";
    if (!legend && parts[0] != "frame")
        return {};
    const QSize dimensions = legend ? QSize(500, 30) : QSize(512, 512);
    const int maximum = legend ? 128 * 1024 : maxImageBytes;
    const auto generation = demand->generation.load();
    QElapsedTimer elapsed;
    elapsed.start();
    const auto valid = [&] {
        return demand->active.load() && demand->generation.load() == generation &&
               elapsed.elapsed() < 2500;
    };
    if (!valid())
        return {};
    QLocalSocket socket;
    socket.setReadBufferSize(262145);
    socket.connectToServer(socketPath);
    if (!socket.waitForConnected(500) || !valid())
        return {};
    struct ucred peer{};
    socklen_t peerSize = sizeof(peer);
    if (getsockopt(socket.socketDescriptor(), SOL_SOCKET, SO_PEERCRED, &peer, &peerSize) != 0 ||
        peer.uid != getuid())
        return {};
    QByteArray png;
    int total = -1;
    for (int offset = 0; total < 0 || offset < total; offset += chunkBytes) {
        if (!valid())
            return {};
        const int serial = offset / chunkBytes;
        QJsonObject command{{"version", 1},
                            {"request_id", serial},
                            {"op", "radar_image"},
                            {"image", QJsonObject{{"id", parts[1]}, {"offset", offset}}}};
        const auto encoded = QJsonDocument(command).toJson(QJsonDocument::Compact) + '\n';
        if (socket.write(encoded) != encoded.size() ||
            (socket.bytesToWrite() > 0 && !socket.waitForBytesWritten(100)))
            return {};
        QByteArray reply;
        while (!reply.contains('\n')) {
            if (!valid() || socket.state() != QLocalSocket::ConnectedState)
                return {};
            if (socket.bytesAvailable() == 0) {
                socket.waitForReadyRead(50);
                continue;
            }
            reply += socket.read(qMin<qint64>(16384, socket.bytesAvailable()));
            if (reply.size() > 262144)
                return {};
        }
        if (reply.indexOf('\n') != reply.size() - 1)
            return {};
        reply.chop(1);
        QJsonObject envelope;
        if (!decodeProtocol(reply, &envelope) || envelope.value("request_id") != serial ||
            envelope.value("ok") != true || envelope.contains("snapshot") ||
            envelope.contains("event"))
            return {};
        const auto image = envelope.value("image").toObject();
        if (image.size() != 4 || image.value("id") != parts[1] ||
            !integer(image.value("offset"), offset, offset) ||
            !integer(image.value("total"), 1, maximum) || !image.value("data").isString())
            return {};
        const int claimed = image.value("total").toInt();
        if (total >= 0 && total != claimed)
            return {};
        total = claimed;
        if (offset >= total)
            return {};
        const auto text = image.value("data").toString();
        if (text.size() > ((chunkBytes + 2) / 3) * 4)
            return {};
        const auto decoded = QByteArray::fromBase64Encoding(
            text.toLatin1(), QByteArray::AbortOnBase64DecodingErrors);
        if (decoded.decodingStatus != QByteArray::Base64DecodingStatus::Ok ||
            decoded.decoded.size() != qMin(chunkBytes, total - offset))
            return {};
        png += decoded.decoded;
    }
    socket.abort();
    if (!valid() || !WeatherPNG::bounded(png, dimensions, maximum, 8))
        return {};
    QBuffer buffer(&png);
    buffer.open(QIODevice::ReadOnly);
    QImageReader reader(&buffer, "PNG");
    reader.setAutoDetectImageFormat(false);
    reader.setDecideFormatFromContent(false);
    if (reader.size() != dimensions)
        return {};
    auto image = reader.read();
    if (!valid() || image.isNull() || image.size() != dimensions ||
        image.sizeInBytes() > dimensions.width() * dimensions.height() * 4)
        return {};
    if (size)
        *size = dimensions;
    return image;
}
