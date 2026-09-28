#pragma once
#include <QByteArray>
#include <QJsonObject>

// Validate before Qt's JSON decoder, which otherwise silently accepts duplicate keys.
bool decodeProtocol(const QByteArray &line, QJsonObject *object);
