#include <QChar>
#include <QBitArray>
#include <QtTest>
#include "desktopwarnings.h"
#include "transport.h"
#include <QCoreApplication>
#include <QDBusContext>
#include <QJsonDocument>
#include <QProcess>
#include <QSocketNotifier>
#include <QTimer>
#include <QLocalServer>
#include <QTemporaryDir>
#include <unistd.h>

namespace {
const QString service = QStringLiteral("org.freedesktop.Notifications");
const QString path = QStringLiteral("/org/freedesktop/Notifications");
const QString generation(32, 'a');

void log(const QJsonObject& event) {
    const auto data = QJsonDocument(event).toJson(QJsonDocument::Compact) + '\n';
    auto ignored = ::write(STDOUT_FILENO, data.constData(), data.size());
    (void)ignored;
}

QString token(int i = 1) {
    return QString::number(i, 16).rightJustified(32, '0');
}
QJsonObject notify(int i = 1) {
    return {{"kind", "notify"},
            {"generation", generation},
            {"token", token(i)},
            {"title", "Flood Warning"},
            {"body", "New York\nNWS Office\nMove to higher ground."},
            {"urgency", "normal"}};
}
QJsonObject activate() {
    return {{"kind", "activate"}, {"generation", generation}};
}
QJsonObject deactivate() {
    return {{"kind", "deactivate"}, {"generation", generation}};
}

QList<QJsonObject> reports(const QSignalSpy& spy, const QString& kind) {
    QList<QJsonObject> found;
    for (const auto& row : spy) {
        auto value = qvariant_cast<QJsonObject>(row[0]);
        if (value.value("kind") == kind)
            found.append(value);
    }
    return found;
}
} // namespace

class MockDaemon final : public QObject, protected QDBusContext {
    Q_OBJECT
    Q_CLASSINFO("D-Bus Interface", "org.freedesktop.Notifications")
    bool supportsActions, delayed;
    uint next = 1;
    QDBusMessage savedReply;
    bool replyPending = false;
    QSocketNotifier input;
    QByteArray buffer;

  public:
    MockDaemon(bool actions, bool delay)
        : supportsActions(actions), delayed(delay),
          input(STDIN_FILENO, QSocketNotifier::Read, this) {
        connect(&input, &QSocketNotifier::activated, this, [this] {
            char data[1024];
            auto count = ::read(STDIN_FILENO, data, sizeof(data));
            if (count <= 0) {
                QCoreApplication::quit();
                return;
            }
            buffer.append(data, count);
            if (buffer.size() > 8192)
                qFatal("oversized fixture command");
            qsizetype newline;
            while ((newline = buffer.indexOf('\n')) >= 0) {
                auto command = QJsonDocument::fromJson(buffer.left(newline)).object();
                buffer.remove(0, newline + 1);
                const auto kind = command.value("kind").toString();
                auto bus = QDBusConnection::sessionBus();
                if (kind == "release" && replyPending) {
                    bus.send(savedReply);
                    replyPending = false;
                } else if (kind == "action" || kind == "token" || kind == "closed") {
                    const auto method = kind == "action"  ? "ActionInvoked"
                                        : kind == "token" ? "ActivationToken"
                                                          : "NotificationClosed";
                    auto signal = QDBusMessage::createSignal(path, service, method);
                    signal << static_cast<uint>(command.value("id").toInt(1));
                    if (kind == "closed")
                        signal << uint(2);
                    else
                        signal << command.value("value").toString(
                            kind == "action" ? "details" : "fixture-activation-token");
                    bus.send(signal);
                }
            }
        });
    }

  public slots:
    QStringList GetCapabilities() {
        return supportsActions ? QStringList{"body", "actions"} : QStringList{"body"};
    }
    uint Notify(const QString& app, uint replaces, const QString& icon, const QString& title,
                const QString& body, const QStringList& actions, const QVariantMap& hints,
                int expiry) {
        const uint id = next++;
        auto loggedHints = QJsonObject::fromVariantMap(hints);
        loggedHints["urgency"] = static_cast<int>(hints.value("urgency").toUInt());
        log({{"kind", "notify"},
             {"id", static_cast<int>(id)},
             {"app", app},
             {"replaces", static_cast<int>(replaces)},
             {"icon", icon},
             {"title", title},
             {"body", body},
             {"actions", QJsonArray::fromStringList(actions)},
             {"hints", loggedHints},
             {"expiry", expiry},
             {"urgency_is_byte", hints.value("urgency").metaType().id() == QMetaType::UChar}});
        if (delayed) {
            setDelayedReply(true);
            savedReply = message().createReply(QVariantList{QVariant::fromValue(id)});
            replyPending = true;
        }
        return id;
    }
    void CloseNotification(uint id) {
        log({{"kind", "recall"}, {"id", static_cast<int>(id)}});
    }
};

class DaemonFixture final {
    QProcess process;
    QByteArray buffer;
    QList<QJsonObject> records;

  public:
    ~DaemonFixture() {
        stop();
    }
    bool start(bool actions = true, bool delay = false) {
        stop();
        buffer.clear();
        records.clear();
        QStringList args{"--mock-daemon"};
        if (!actions)
            args << "--no-actions";
        if (delay)
            args << "--delayed";
        process.start(QCoreApplication::applicationFilePath(), args);
        if (!process.waitForStarted(1000))
            return false;
        QElapsedTimer timer;
        timer.start();
        while (timer.elapsed() < 2000) {
            collect();
            if (!records.isEmpty() && records.first().value("kind") == "ready")
                return true;
            process.waitForReadyRead(25);
        }
        return false;
    }
    void stop() {
        if (process.state() != QProcess::NotRunning) {
            process.terminate();
            if (!process.waitForFinished(1000)) {
                process.kill();
                process.waitForFinished(1000);
            }
        }
    }
    void collect() {
        buffer += process.readAllStandardOutput();
        qsizetype newline;
        while ((newline = buffer.indexOf('\n')) >= 0) {
            records.append(QJsonDocument::fromJson(buffer.left(newline)).object());
            buffer.remove(0, newline + 1);
        }
    }
    QList<QJsonObject> events(const QString& kind) {
        collect();
        QList<QJsonObject> out;
        for (const auto& record : records)
            if (record.value("kind") == kind)
                out.append(record);
        return out;
    }
    void command(const QJsonObject& command) {
        process.write(QJsonDocument(command).toJson(QJsonDocument::Compact) + '\n');
        process.waitForBytesWritten(1000);
    }
};

class DesktopWarningsTest final : public QObject {
    Q_OBJECT
  private slots:
    void initTestCase() {
        // The test target must run under dbus-run-session. Never use the real
        // desktop bus or register a replacement for a user's notification daemon.
        QVERIFY2(qEnvironmentVariable("WEATHER_WARNING_PRIVATE_BUS") == "1",
                 "Private bus fixture required");
        QVERIFY(!qEnvironmentVariable("DBUS_SESSION_BUS_ADDRESS").isEmpty());
    }
    void disabledAndMalformedCreateNoConnection() {
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(!adapter.hasConnection());
        QVERIFY(!adapter.isActive());
        for (const auto& command : QList<QJsonObject>{
                 {},
                 {{"kind", "activate"}, {"generation", "bad"}},
                 {{"kind", "activate"}, {"generation", generation}, {"extra", true}}})
            QVERIFY(!adapter.accept(command));
        auto bad = notify();
        bad["title"] = "<b>markup</b>";
        QVERIFY(!adapter.accept(bad));
        bad = notify();
        bad["body"] = QString(1801, 'a');
        QVERIFY(!adapter.accept(bad));
        bad = notify();
        bad["urgency"] = "emergency";
        QVERIFY(!adapter.accept(bad));
        QVERIFY(!adapter.hasConnection());
        QCOMPARE(spy.size(), 0);
    }
    void acceptanceAndActionAreIndependent() {
        DaemonFixture daemon;
        QVERIFY(daemon.start());
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QVERIFY(adapter.accept(notify()));
        QTRY_COMPARE(reports(spy, "result").size(), 1);
        QCOMPARE(reports(spy, "result").first().value("status"), "accepted");
        QCOMPARE(reports(spy, "action").size(), 0);
        auto sent = daemon.events("notify").first();
        QCOMPARE(sent.value("urgency_is_byte"), true);
        QCOMPARE(sent.value("hints").toObject().value("urgency"), 1);
        QCOMPARE(sent.value("expiry"), -1);
        QVERIFY(!sent.value("actions").toArray().isEmpty());
        daemon.command({{"kind", "token"}, {"id", 1}});
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTRY_COMPARE(reports(spy, "action").size(), 1);
        auto action = reports(spy, "action").first();
        QCOMPARE(action.value("token"), token());
        QCOMPARE(action.value("generation"), generation);
        QCOMPARE(action.value("activation_token"), "fixture-activation-token");
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTest::qWait(30);
        QCOMPARE(reports(spy, "action").size(), 1);
    }
    void actionlessDaemonAndCriticalPolicy() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(false));
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QCOMPARE(reports(spy, "ready").last().value("actions"), false);
        auto command = notify();
        command["urgency"] = "critical";
        QVERIFY(adapter.accept(command));
        QTRY_COMPARE(reports(spy, "result").size(), 1);
        auto sent = daemon.events("notify").first();
        QCOMPARE(sent.value("urgency_is_byte"), true);
        QVERIFY(sent.value("actions").toArray().isEmpty());
        QCOMPARE(sent.value("hints").toObject().value("urgency"), 2);
        QCOMPARE(sent.value("hints").toObject().value("suppress-sound"), true);
        QCOMPARE(sent.value("expiry"), 0);
    }
    void canceledLateAcceptanceIsUncertainAndRecalled() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, true));
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QVERIFY(adapter.accept(notify()));
        QTRY_COMPARE(daemon.events("notify").size(), 1);
        QCOMPARE(reports(spy, "result").size(), 0);
        QVERIFY(
            adapter.accept({{"kind", "cancel"}, {"generation", generation}, {"token", token()}}));
        QVERIFY(adapter.accept(notify(2))); // Only the original native call may remain in flight.
        QTRY_COMPARE(reports(spy, "result").size(), 1);
        QCOMPARE(reports(spy, "result").first().value("status"), "failed");
        daemon.command({{"kind", "release"}});
        QTRY_COMPARE(reports(spy, "result").size(), 2);
        QCOMPARE(reports(spy, "result").last().value("status"), "uncertain");
        QTRY_COMPARE(daemon.events("recall").size(), 1);
        QCOMPARE(adapter.retainedBindings(), 0);
    }
    void timeoutIsUncertainAndDisableCleansConnection() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, true));
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QVERIFY(adapter.accept(notify()));
        QTRY_COMPARE(daemon.events("notify").size(), 1);
        QVERIFY(adapter.accept(deactivate()));
        QVERIFY(!adapter.isActive());
        QTRY_COMPARE_WITH_TIMEOUT(reports(spy, "result").size(), 1, 3000);
        QCOMPARE(reports(spy, "result").first().value("status"), "uncertain");
        QTRY_VERIFY(!adapter.hasConnection());
        QCOMPARE(adapter.retainedBindings(), 0);
    }
    void daemonRestartCannotReuseOldActions() {
        DaemonFixture daemon;
        QVERIFY(daemon.start());
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QVERIFY(adapter.accept(notify()));
        QTRY_COMPARE(reports(spy, "result").size(), 1);
        daemon.stop();
        QTRY_VERIFY(!adapter.isReady());
        QCOMPARE(adapter.retainedBindings(), 0);
        QVERIFY(daemon.start());
        QTRY_VERIFY(adapter.isReady());
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTest::qWait(30);
        QCOMPARE(reports(spy, "action").size(), 0);
        QVERIFY(adapter.accept(notify(2)));
        QTRY_COMPARE(reports(spy, "result").size(), 2);
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTRY_COMPARE(reports(spy, "action").size(), 1);
        QCOMPARE(reports(spy, "action").first().value("token"), token(2));
    }
    void sessionsAndBindingsAreBounded() {
        DaemonFixture daemon;
        QVERIFY(daemon.start());
        DesktopWarnings adapter;
        QSignalSpy spy(&adapter, &DesktopWarnings::report);
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        for (int i = 1; i <= 20; ++i) {
            QVERIFY(adapter.accept(notify(i)));
            QTRY_COMPARE(reports(spy, "result").size(), i);
            QVERIFY(adapter.retainedBindings() <= 16);
        }
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTest::qWait(30);
        QCOMPARE(reports(spy, "action").size(), 0);
        auto stale = deactivate();
        stale["generation"] = QString(32, 'b');
        QVERIFY(adapter.accept(stale));
        QVERIFY(adapter.isReady());
        QVERIFY(adapter.accept(deactivate()));
        QTRY_VERIFY(!adapter.hasConnection());
        QVERIFY(adapter.accept(activate()));
        QTRY_VERIFY(adapter.isReady());
        QCOMPARE(adapter.retainedBindings(), 0);
    }
    void transportSeparatesNativeReportsFromQml() {
        DaemonFixture daemon;
        QVERIFY(daemon.start());
        QTemporaryDir directory;
        QVERIFY(directory.isValid());
        QLocalServer server;
        const auto socketPath = directory.path() + "/weather.sock";
        QVERIFY(server.listen(socketPath));
        WeatherTransport client(socketPath, false);
        QSignalSpy ready(&client, &WeatherTransport::ready),
            messages(&client, &WeatherTransport::message),
            errors(&client, &WeatherTransport::unavailable);
        client.start();
        QTRY_COMPARE(ready.size(), 1);
        QTRY_VERIFY(server.hasPendingConnections());
        QScopedPointer<QLocalSocket> peer(server.nextPendingConnection());
        QVERIFY(client.send({{"version", 1}, {"request_id", 7}, {"op", "subscribe"}}));
        QTRY_VERIFY(peer->canReadLine());
        auto subscription = QJsonDocument::fromJson(peer->readLine()).object();
        QCOMPARE(subscription.value("native_notifications"), true);
        QVERIFY(!client.findChild<DesktopWarnings*>()); // Capability is not activation.
        auto control = [&](const QJsonObject& command) {
            peer->write(
                QJsonDocument(
                    QJsonObject{{"version", 1}, {"event", "warning_desktop"}, {"native", command}})
                    .toJson(QJsonDocument::Compact) +
                '\n');
            peer->flush();
        };
        control(activate());
        QTRY_VERIFY(peer->canReadLine());
        auto report = QJsonDocument::fromJson(peer->readLine()).object();
        QCOMPARE(report.value("op"), "warning_native");
        QVERIFY(!report.contains("request_id"));
        QCOMPARE(report.value("native").toObject().value("kind"), "ready");
        QCOMPARE(report.value("native").toObject().value("ready"), true);
        QCOMPARE(messages.size(), 0);
        control(notify());
        QTRY_VERIFY(peer->canReadLine());
        report = QJsonDocument::fromJson(peer->readLine()).object();
        QCOMPARE(report.value("native").toObject().value("status"), "accepted");
        QCOMPARE(messages.size(), 0);
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTRY_VERIFY(peer->canReadLine());
        report = QJsonDocument::fromJson(peer->readLine()).object();
        QCOMPARE(report.value("native").toObject().value("kind"), "action");
        QCOMPARE(report.value("native").toObject().value("token"), token());
        QCOMPARE(messages.size(), 0);
        // The service authorizes a separate detail event after resolving its
        // own route. That event, unlike native controls, belongs to QML.
        peer->write(
            QJsonDocument(QJsonObject{{"version", 1},
                                      {"event", "warning_open"},
                                      {"warning", QJsonObject{{"location", QString(64, 'a')},
                                                              {"key", QString(64, 'b')}}},
                                      {"activation_token", ""}})
                .toJson(QJsonDocument::Compact) +
            '\n');
        peer->flush();
        QTRY_COMPARE(messages.size(), 1);
        control(deactivate());
        QTRY_VERIFY(!client.findChild<DesktopWarnings*>()->hasConnection());
        QVERIFY(client.connected());
        QCOMPARE(errors.size(), 0);
    }
};

int main(int argc, char** argv) {
    QCoreApplication app(argc, argv);
    if (app.arguments().contains("--mock-daemon")) {
        MockDaemon daemon(!app.arguments().contains("--no-actions"),
                          app.arguments().contains("--delayed"));
        auto bus = QDBusConnection::sessionBus();
        if (!bus.registerService(service) ||
            !bus.registerObject(path, &daemon, QDBusConnection::ExportAllSlots))
            return 2;
        log({{"kind", "ready"}});
        return app.exec();
    }
    DesktopWarningsTest test;
    return QTest::qExec(&test, argc, argv);
}

#include "desktopwarnings_test.moc"
