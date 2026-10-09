#pragma once
#include <QByteArray>
#include <QSize>
#include <cstring>

// Preflight bounded PNG framing and metadata before any image decoder runs.
namespace WeatherPNG {
inline quint32 pngWord(const char* p) {
    const auto* b = reinterpret_cast<const unsigned char*>(p);
    return (quint32(b[0]) << 24) | (quint32(b[1]) << 16) | (quint32(b[2]) << 8) | b[3];
}
inline quint32 pngCRC(const char* p, qsizetype size) {
    quint32 crc = 0xffffffffU;
    for (qsizetype i = 0; i < size; ++i) {
        crc ^= static_cast<unsigned char>(p[i]);
        for (int bit = 0; bit < 8; ++bit)
            crc = (crc >> 1) ^ ((crc & 1U) ? 0xedb88320U : 0U);
    }
    return crc ^ 0xffffffffU;
}
inline bool bounded(const QByteArray& bytes, const QSize& expected, qsizetype maxBytes,
                    int maxBitDepth = 16) {
    if (bytes.size() < 8 || bytes.size() > maxBytes ||
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
            if (!isType("IHDR") || length != 13 ||
                pngWord(chunk + 8) != quint32(expected.width()) ||
                pngWord(chunk + 12) != quint32(expected.height()) ||
                static_cast<unsigned char>(chunk[16]) > maxBitDepth)
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
} // namespace WeatherPNG
