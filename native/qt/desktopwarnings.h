#pragma once

#include <QChar>
#include <QDBusConnection>
#include <QDBusMessage>
#include <QHash>
#include <QJsonObject>
#include <QObject>
#include <QPointer>

class QDBusPendingCallWatcher;
class QDBusServiceWatcher;

// A lazy native presentation adapter. Go owns policy and durable reservations.
// This object reports daemon acceptance separately from later user actions.
// Every method runs on its owning Qt thread; it creates no polling timer.
class DesktopWarnings final : public QObject {
    Q_OBJECT
    struct Binding {
        QString token;
        qint64 created = 0;
        QString activationToken;
    };
    QDBusConnection bus;
    QString connectionName, generation, owner;
    bool active = false, ready = false, actions = false, probeAgain = false;
    quint64 epoch = 0;
    QPointer<QDBusServiceWatcher> serviceWatcher;
    QPointer<QDBusPendingCallWatcher> probe;
    QPointer<QDBusPendingCallWatcher> pending;
    QString pendingToken;
    bool pendingCanceled = false;
    QHash<uint, Binding> bindings;
    void probeService(const QString& daemon = {});
    void reportReady(bool value);
    void closeNotification(uint id);
    void disconnectIdle();
    void pruneBindings();
    void remember(uint id, const QString& token);
    void result(const QString& session, const QString& token, const QString& status);

  private slots:
    void actionInvoked(uint id, const QString& action, const QDBusMessage& message);
    void notificationClosed(uint id, uint reason, const QDBusMessage& message);
    void activationToken(uint id, const QString& token, const QDBusMessage& message);

  public:
    explicit DesktopWarnings(QObject* parent = nullptr);
    ~DesktopWarnings() override;
    // Valid commands: activate/deactivate, notify and cancel. An invalid
    // command is rejected before any bus connection or native side effect.
    bool accept(const QJsonObject& command);
    void deactivate();
    bool isActive() const {
        return active;
    }
    bool isReady() const {
        return ready;
    }
    int retainedBindings() const {
        return bindings.size();
    }
    bool hasConnection() const {
        return bus.isConnected();
    }
    static bool validCommand(const QJsonObject& command);

  signals:
    // Opaque generation/token fields are echoed to the authenticated service.
    // Activation tokens are forwarded only for a matched user action.
    void report(const QJsonObject& event);
};
