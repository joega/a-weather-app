#pragma once
#include <QObject>
#include <QLocalSocket>
#include <QVariantMap>

class DesktopWarnings;

class WeatherTransport final : public QObject {
    Q_OBJECT
    Q_PROPERTY(bool connected READ connected NOTIFY connectedChanged)
    Q_PROPERTY(bool diagnostic READ diagnostic CONSTANT)
    QLocalSocket socket;
    QByteArray buffer;
    QString path;
    bool verbose;
    bool failed = false;
    DesktopWarnings* warnings = nullptr;
    void read();
    void fail(const QString& message);

  public:
    WeatherTransport(QString socketPath, bool diagnostic, QObject* parent = nullptr);
    ~WeatherTransport() override;
    bool connected() const {
        return socket.state() == QLocalSocket::ConnectedState && !failed;
    }
    bool diagnostic() const {
        return verbose;
    }
    Q_INVOKABLE void start();
    Q_INVOKABLE bool send(const QVariantMap& request);
    Q_INVOKABLE void disconnectService();
    void requestShutdown() {
        emit shutdownRequested();
    }
  signals:
    void connectedChanged();
    void ready();
    void message(const QString& json);
    void unavailable(const QString& error);
    void shutdownRequested();
};
