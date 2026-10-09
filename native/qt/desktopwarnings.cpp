#include "desktopwarnings.h"

#include <QDBusPendingCallWatcher>
#include <QDBusPendingReply>
#include <QDBusServiceWatcher>
#include <QDateTime>
#include <QJsonArray>
#include <QSet>
#include <QUuid>
#include <QVariantMap>
#include <algorithm>

namespace {
const QString service = QStringLiteral("org.freedesktop.Notifications");
const QString path = QStringLiteral("/org/freedesktop/Notifications");
constexpr int callTimeoutMs = 2000;
constexpr int bindingLimit = 16;
constexpr qint64 bindingAgeMs = 24 * 60 * 60 * 1000;

bool hex(const QJsonValue& value, int length) {
    if (!value.isString() || value.toString().size() != length)
        return false;
    for (auto c : value.toString())
        if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')))
            return false;
    return true;
}

bool text(const QJsonValue& value, int limit, bool multiline = false) {
    if (!value.isString() || value.toString().isEmpty())
        return false;
    auto s = value.toString();
    if (s.size() > limit)
        return false;
    for (auto c : s) {
        auto u = c.unicode();
        if ((u < 32 && !(multiline && u == '\n')) || (u >= 127 && u <= 159) ||
            (u >= 0x202a && u <= 0x202e) || (u >= 0x2066 && u <= 0x2069) || c == '<' || c == '>' ||
            c == '&')
            return false;
    }
    return true;
}

} // namespace

DesktopWarnings::DesktopWarnings(QObject* parent)
    : QObject(parent), bus(QString()),
      connectionName("a-weather-warnings-" + QUuid::createUuid().toString(QUuid::WithoutBraces)) {}

DesktopWarnings::~DesktopWarnings() {
    active = false;
    if (probe)
        delete probe.data();
    if (pending)
        delete pending.data();
    if (serviceWatcher)
        delete serviceWatcher.data();
    QDBusConnection::disconnectFromBus(connectionName);
}

void DesktopWarnings::deactivate() {
    if (!generation.isEmpty())
        accept({{"kind", "deactivate"}, {"generation", generation}});
}

bool DesktopWarnings::validCommand(const QJsonObject& command) {
    if (!hex(command.value("generation"), 32) || !command.value("kind").isString())
        return false;
    const auto kind = command.value("kind").toString();
    if (kind == "activate" || kind == "deactivate")
        return command.size() == 2;
    if (!hex(command.value("token"), 32))
        return false;
    if (kind == "cancel")
        return command.size() == 3;
    if (kind != "notify" || command.size() != 6 || !text(command.value("title"), 240) ||
        !text(command.value("body"), 1800, true))
        return false;
    return command.value("urgency") == "normal" || command.value("urgency") == "critical";
}

void DesktopWarnings::reportReady(bool value) {
    ready = value;
    emit report(QJsonObject{{"kind", "ready"},
                            {"generation", generation},
                            {"ready", ready},
                            {"actions", ready && actions}});
}

void DesktopWarnings::result(const QString& session, const QString& token, const QString& status) {
    emit report(QJsonObject{
        {"kind", "result"}, {"generation", session}, {"token", token}, {"status", status}});
}

void DesktopWarnings::probeService(const QString& daemon) {
    if (!active)
        return;
    if (probe) {
        probeAgain = true;
        return;
    }
    probeAgain = false;
    const auto started = epoch;
    // Resolve the unique owner explicitly. Qt's typed pending replies do not
    // retain the reply's sender header, so reply().service() cannot identify it.
    auto message =
        daemon.isEmpty()
            ? QDBusMessage::createMethodCall("org.freedesktop.DBus", "/org/freedesktop/DBus",
                                             "org.freedesktop.DBus", "GetNameOwner")
            : QDBusMessage::createMethodCall(daemon, path, service, "GetCapabilities");
    if (daemon.isEmpty())
        message << service;
    message.setAutoStartService(false);
    auto* watcher = new QDBusPendingCallWatcher(bus.asyncCall(message, callTimeoutMs), this);
    probe = watcher;
    connect(watcher, &QDBusPendingCallWatcher::finished, this, [this, watcher, started, daemon] {
        if (probe == watcher)
            probe = nullptr;
        watcher->deleteLater();
        if (active && started == epoch) {
            if (daemon.isEmpty()) {
                QDBusPendingReply<QString> reply = *watcher;
                if (!reply.isError() && reply.value().startsWith(':') &&
                    reply.value().size() <= 255) {
                    probeService(reply.value());
                    return;
                }
                owner.clear();
                actions = false;
                reportReady(false);
            } else {
                QDBusPendingReply<QStringList> reply = *watcher;
                const auto capabilities = reply.isError() ? QStringList{} : reply.value();
                const bool valid =
                    !reply.isError() && capabilities.size() <= 64 &&
                    std::all_of(capabilities.begin(), capabilities.end(),
                                [](const QString& capability) { return capability.size() <= 128; });
                owner = valid ? daemon : QString();
                actions = valid && capabilities.contains("actions");
                reportReady(valid);
            }
        }
        if (active && probeAgain)
            probeService();
        disconnectIdle();
    });
}

void DesktopWarnings::closeNotification(uint id) {
    if (!bus.isConnected() || owner.isEmpty() || id == 0)
        return;
    // Address the exact daemon that issued this ID; never recall a different
    // daemon's reused ID after a service restart. Recall remains best effort.
    auto message = QDBusMessage::createMethodCall(owner, path, service, "CloseNotification");
    message.setAutoStartService(false);
    message << id;
    bus.asyncCall(message, 500);
}

void DesktopWarnings::disconnectIdle() {
    if (active || pending || probe)
        return;
    if (serviceWatcher)
        delete serviceWatcher.data();
    QDBusConnection::disconnectFromBus(connectionName);
    bus = QDBusConnection(QString());
    owner.clear();
}

void DesktopWarnings::pruneBindings() {
    const auto now = QDateTime::currentMSecsSinceEpoch();
    for (auto it = bindings.begin(); it != bindings.end();) {
        if (now < it->created || now - it->created >= bindingAgeMs)
            it = bindings.erase(it);
        else
            ++it;
    }
}

void DesktopWarnings::remember(uint id, const QString& token) {
    pruneBindings();
    if (!bindings.contains(id) && bindings.size() >= bindingLimit) {
        auto oldest = std::min_element(
            bindings.begin(), bindings.end(),
            [](const Binding& a, const Binding& b) { return a.created < b.created; });
        bindings.erase(oldest);
    }
    bindings.insert(id, Binding{token, QDateTime::currentMSecsSinceEpoch(), {}});
}

bool DesktopWarnings::accept(const QJsonObject& command) {
    if (!validCommand(command))
        return false;
    const auto kind = command.value("kind").toString();
    const auto session = command.value("generation").toString();
    if (kind == "activate") {
        if (active && session == generation)
            return true;
        ++epoch;
        generation = session;
        active = true;
        ready = actions = false;
        bindings.clear();
        pendingCanceled = true;
        if (!bus.isConnected())
            bus = QDBusConnection::connectToBus(QDBusConnection::SessionBus, connectionName);
        if (!bus.isConnected()) {
            reportReady(false);
            return true;
        }
        if (!serviceWatcher) {
            serviceWatcher = new QDBusServiceWatcher(
                service, bus, QDBusServiceWatcher::WatchForOwnerChange, this);
            connect(serviceWatcher, &QDBusServiceWatcher::serviceOwnerChanged, this,
                    [this](const QString&, const QString&, const QString&) {
                        ++epoch;
                        ready = actions = false;
                        owner.clear();
                        bindings.clear();
                        pendingCanceled = true;
                        if (active) {
                            reportReady(false);
                            probeService();
                        }
                    });
            bus.connect(service, path, service, "ActionInvoked", this,
                        SLOT(actionInvoked(uint, QString, QDBusMessage)));
            bus.connect(service, path, service, "NotificationClosed", this,
                        SLOT(notificationClosed(uint, uint, QDBusMessage)));
            bus.connect(service, path, service, "ActivationToken", this,
                        SLOT(activationToken(uint, QString, QDBusMessage)));
        }
        probeService();
        return true;
    }
    // Obsolete controls are harmless; they cannot cancel a newer session.
    if (session != generation)
        return true;
    if (kind == "deactivate") {
        ++epoch;
        active = ready = actions = false;
        pendingCanceled = true;
        for (auto id : bindings.keys())
            closeNotification(id);
        bindings.clear();
        disconnectIdle();
        return true;
    }
    const auto token = command.value("token").toString();
    if (kind == "cancel") {
        if (pendingToken == token)
            pendingCanceled = true;
        for (auto it = bindings.begin(); it != bindings.end();) {
            if (it->token == token) {
                closeNotification(it.key());
                it = bindings.erase(it);
            } else
                ++it;
        }
        return true;
    }
    if (!active || !ready || pending) {
        result(session, token, "failed");
        return true;
    }
    const auto started = epoch;
    const auto daemon = owner;
    const bool critical = command.value("urgency") == "critical";
    QStringList buttons;
    if (actions)
        buttons = {"default", "Open warning", "details", "View details"};
    QVariantMap hints{{"urgency", QVariant::fromValue(static_cast<uchar>(critical ? 2 : 1))},
                      {"desktop-entry", "a-weather-app"},
                      {"suppress-sound", true}};
    auto message = QDBusMessage::createMethodCall(daemon, path, service, "Notify");
    message.setAutoStartService(false);
    message << QString("A Weather App") << uint(0) << QString("a-weather-app")
            << command.value("title").toString() << command.value("body").toString() << buttons
            << hints << (critical ? 0 : -1);
    auto* watcher = new QDBusPendingCallWatcher(bus.asyncCall(message, callTimeoutMs), this);
    pending = watcher;
    pendingToken = token;
    pendingCanceled = false;
    connect(watcher, &QDBusPendingCallWatcher::finished, this,
            [this, watcher, started, session, token, daemon] {
                QDBusPendingReply<uint> reply = *watcher;
                watcher->deleteLater();
                const bool obsolete = !active || started != epoch || pendingCanceled;
                pending = nullptr;
                pendingToken.clear();
                if (reply.isError() || reply.value() == 0) {
                    // Once Notify was sent, a transport error cannot prove the daemon
                    // did not show it. The Go reservation must remain uncertain.
                    result(session, token, "uncertain");
                } else if (obsolete || daemon != owner) {
                    if (daemon == owner)
                        closeNotification(reply.value());
                    result(session, token, "uncertain");
                } else {
                    remember(reply.value(), token);
                    result(session, token, "accepted");
                }
                disconnectIdle();
            });
    return true;
}

void DesktopWarnings::actionInvoked(uint id, const QString& action, const QDBusMessage& message) {
    pruneBindings();
    if (!active || !ready || message.service() != owner ||
        (action != "default" && action != "details") || !bindings.contains(id))
        return;
    const auto binding = bindings.take(id); // One navigation per accepted notice.
    emit report(QJsonObject{{"kind", "action"},
                            {"generation", generation},
                            {"token", binding.token},
                            {"activation_token", binding.activationToken}});
}

void DesktopWarnings::notificationClosed(uint id, uint, const QDBusMessage& message) {
    if (message.service() == owner)
        bindings.remove(id);
}

void DesktopWarnings::activationToken(uint id, const QString& token, const QDBusMessage& message) {
    for (const auto c : token)
        if (c.unicode() < 32 || (c.unicode() >= 127 && c.unicode() <= 159))
            return;
    if (active && ready && message.service() == owner && bindings.contains(id) &&
        token.size() <= 1024)
        bindings[id].activationToken = token;
}
