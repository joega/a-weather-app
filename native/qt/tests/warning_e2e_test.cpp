#include <QChar>
#include <QBitArray>
#include <QtTest>
#include "desktopwarnings.h"
#include "transport.h"
#include "notificationfixture.h"
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QQmlExpression>
#include <QQuickWindow>
#include <QQuickItem>
#include <QJsonArray>
#include <functional>
#include <memory>

namespace {
bool eventually(const std::function<bool()>& check, int timeout = 5000) {
    QElapsedTimer timer;
    timer.start();
    while (!check() && timer.elapsed() < timeout)
        QTest::qWait(10);
    return check();
}

class WarningService final {
    QProcess process;
    QByteArray buffer;
    QList<QJsonObject> replies;

    void collect() {
        buffer += process.readAll();
        if (buffer.size() > 262144)
            qFatal("unbounded fixture output");
        qsizetype newline;
        while ((newline = buffer.indexOf('\n')) >= 0) {
            const auto line = buffer.left(newline);
            buffer.remove(0, newline + 1);
            if (line.startsWith("WARNING_FIXTURE "))
                replies.append(QJsonDocument::fromJson(line.mid(16)).object());
            else if (!line.isEmpty())
                diagnostics += line + '\n';
        }
    }
    QJsonObject reply() {
        if (!eventually([this] {
                collect();
                return !replies.isEmpty();
            }))
            return {};
        return replies.takeFirst();
    }

  public:
    QString socket;
    QByteArray diagnostics;
    ~WarningService() {
        if (process.state() == QProcess::NotRunning)
            return;
        process.write("{\"op\":\"stop\"}\n");
        if (!process.waitForFinished(7000)) {
            process.kill();
            process.waitForFinished(2000);
        }
        collect();
    }
    bool start() {
        auto env = QProcessEnvironment::systemEnvironment();
        env.insert("WEATHER_NATIVE_WARNING_FIXTURE", "1");
        process.setProcessEnvironment(env);
        process.setProcessChannelMode(QProcess::MergedChannels);
        process.start(qEnvironmentVariable("GO_WARNING_FIXTURE"),
                      {"-test.run=^TestWarningNativeServiceFixture$", "-test.timeout=90s"});
        if (!process.waitForStarted(3000))
            return false;
        const auto initial = reply();
        socket = initial.value("socket").toString();
        return initial.value("ready").toBool() && !socket.isEmpty();
    }
    QJsonObject command(const QJsonObject& value) {
        process.write(QJsonDocument(value).toJson(QJsonDocument::Compact) + '\n');
        return reply();
    }
    bool stop() {
        process.write("{\"op\":\"stop\"}\n");
        if (!eventually([this] { return process.state() == QProcess::NotRunning; }, 7000))
            return false;
        collect();
        return process.exitStatus() == QProcess::NormalExit && process.exitCode() == 0;
    }
};
} // namespace

class WarningE2ETest final : public QObject {
    Q_OBJECT
    std::unique_ptr<WeatherTransport> transport;
    std::unique_ptr<QQmlApplicationEngine> engine;
    QObject* root = nullptr;
    QQuickWindow* window = nullptr;
    QString daemonPath() const {
        return QCoreApplication::applicationDirPath() + "/desktopwarnings-test";
    }
    QVariant eval(const QString& code) {
        QQmlExpression expression(qmlContext(root), root, code);
        const auto result = expression.evaluate();
        if (expression.hasError())
            QTest::qFail(qPrintable(expression.error().toString()), __FILE__, __LINE__);
        return result;
    }
    QQuickItem* item(QQuickItem* parent, const QString& name) {
        if (parent->objectName() == name)
            return parent;
        for (auto* child : parent->childItems())
            if (auto* found = item(child, name))
                return found;
        return nullptr;
    }
    bool attach(const QString& socket) {
        engine.reset();
        transport.reset();
        transport = std::make_unique<WeatherTransport>(socket, true);
        engine = std::make_unique<QQmlApplicationEngine>();
        engine->setInitialProperties(
            {{"weatherTransport", QVariant::fromValue<QObject*>(transport.get())}});
        QObject::disconnect(engine.get(), nullptr, QCoreApplication::instance(), nullptr);
        engine->load(QUrl("qrc:/ui/qml/shell.qml"));
        if (engine->rootObjects().size() != 1)
            return false;
        root = engine->rootObjects().first();
        window = qobject_cast<QQuickWindow*>(root->property("weatherWindow").value<QObject*>());
        return window && eventually([this] {
                   return eval("backend.available && backend.pending < 0").toBool();
               });
    }
    bool send(const QString& script) {
        return eventually([this] { return eval("backend.pending < 0").toBool(); }) &&
               eval(script).toBool() &&
               eventually([this] { return eval("backend.pending < 0").toBool(); });
    }
    bool enable() {
        root->setProperty("effectsOpen", true);
        auto* toggle = item(window->contentItem(), "warningEnabled");
        if (!toggle)
            return false;
        toggle->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        if (!eventually([this] {
                return eval("backend.snapshot.warning_notifications.settings.enabled && "
                            "backend.snapshot.warning_notifications.ready && backend.pending < 0")
                    .toBool();
            }))
            return false;
        auto* quiet = item(window->contentItem(), "warningQuiet");
        if (!quiet)
            return false;
        quiet->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        if (!eventually([this] {
                return eval("!backend.snapshot.warning_notifications.settings.quiet_enabled && "
                            "backend.pending < 0")
                    .toBool();
            }))
            return false;
        root->setProperty("effectsOpen", false);
        return QMetaObject::invokeMethod(root, "dismissWindow") && eventually([this] {
                   return !window->isVisible() && eval("backend.pending < 0").toBool();
               });
    }
    bool cycle(WarningService& service, DaemonFixture& daemon, int notifications) {
        for (int i = 0; i < 10; ++i) {
            if (service.command({{"op", "advance"}, {"seconds", 31}}).isEmpty())
                return false;
            QTest::qWait(20);
            if (daemon.events("notify").size() == notifications) {
                service.command({{"op", "status"}});
                return eventually([this] {
                    return eval("backend.snapshot.warning_notifications.delivery === 'sent'")
                        .toBool();
                });
            }
        }
        return false;
    }
    bool openAction(DaemonFixture& daemon, int id) {
        daemon.command({{"kind", "action"}, {"id", id}});
        return eventually([this] {
            return window->isVisible() && root->property("warningOpen").toBool() &&
                   eval("backend.warningState === 'ready'").toBool();
        });
    }
    bool closeAndHide() {
        QTest::keyClick(window, Qt::Key_Escape);
        if (!eventually([this] { return !root->property("warningOpen").toBool(); }))
            return false;
        return QMetaObject::invokeMethod(root, "dismissWindow") && eventually([this] {
                   return !window->isVisible() && eval("backend.pending < 0").toBool();
               });
    }

  private slots:
    void initTestCase() {
        QVERIFY2(qEnvironmentVariable("WEATHER_WARNING_PRIVATE_BUS") == "1",
                 "Private dbus-run-session required");
        QVERIFY(!qEnvironmentVariable("DBUS_SESSION_BUS_ADDRESS").isEmpty());
        QVERIFY(!qEnvironmentVariable("GO_WARNING_FIXTURE").isEmpty());
    }
    void cleanup() {
        engine.reset();
        transport.reset();
        root = nullptr;
        window = nullptr;
    }
    void creationUpdateCancellationAndOptOut() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, false, daemonPath()));
        WarningService service;
        QVERIFY2(service.start(), service.diagnostics.constData());
        QVERIFY(attach(service.socket));
        QVERIFY(!transport->findChild<DesktopWarnings*>());
        QCOMPARE(service.command({{"op", "status"}}).value("requests").toInt(), 0);
        QVERIFY(enable());
        const auto before = service.command({{"op", "stage"}, {"stage", "new"}});
        QVERIFY(cycle(service, daemon, 1));
        QVERIFY(!window->isVisible());
        QVERIFY(!root->property("warningOpen").toBool());
        QCOMPARE(service.command({{"op", "status"}}).value("forecasts"), before.value("forecasts"));
        const auto notice = daemon.events("notify").first();
        QVERIFY(notice.value("body").toString().contains("City 0"));
        QVERIFY(notice.value("body").toString().contains("NWS Fixture"));
        QVERIFY(openAction(daemon, notice.value("id").toInt()));
        auto* sky = root->findChild<QObject*>("forecastAtmosphere");
        QVERIFY(sky);
        QVERIFY(!sky->property("presentationActive").toBool());
        QCOMPARE(eval("backend.warningDetail.place").toString(), "City 0");
        QCOMPARE(eval("location").toString(), "City 1");
        QCOMPARE(eval("backend.warningDetail.instruction").toString(),
                 "Original fixture instructions.\nKeep both lines intact. <b>Plain text.</b>");
        auto* instructions = item(window->contentItem(), "warningSource_instruction");
        QVERIFY(instructions);
        QCOMPARE(instructions->property("text").toString(),
                 eval("backend.warningDetail.instruction").toString());
        QCOMPARE(instructions->property("textFormat").toInt(), 0);
        QVERIFY(closeAndHide());
        daemon.command({{"kind", "action"}, {"id", notice.value("id")}});
        QTest::qWait(80);
        QVERIFY(!root->property("warningOpen").toBool());
        service.command({{"op", "stage"}, {"stage", "update"}});
        QVERIFY(cycle(service, daemon, 2));
        const auto update = daemon.events("notify").last();
        QVERIFY(update.value("title").toString().startsWith("Updated:"));
        QVERIFY(openAction(daemon, update.value("id").toInt()));
        QCOMPARE(eval("backend.warningDetail.kind").toString(), "updated");
        QVERIFY(eval("backend.warningDetail.instruction").toString().contains("northern route"));
        QVERIFY(closeAndHide());
        service.command({{"op", "stage"}, {"stage", "cancel"}});
        QVERIFY(cycle(service, daemon, 3));
        const auto canceled = daemon.events("notify").last();
        QVERIFY(canceled.value("title").toString().startsWith("Canceled:"));
        QVERIFY(openAction(daemon, canceled.value("id").toInt()));
        QCOMPARE(eval("backend.warningDetail.kind").toString(), "canceled");
        QVERIFY(closeAndHide());
        QVERIFY(send("backend.send('set_warning_notifications', {enabled:false})"));
        QTRY_VERIFY(!transport->findChild<DesktopWarnings*>()->hasConnection());
        QCOMPARE(eval("backend.snapshot.warning_notifications.recent.length").toInt(), 0);
        const auto stopped = service.command({{"op", "status"}});
        const auto later = service.command({{"op", "advance"}, {"seconds", 900}});
        QCOMPARE(later.value("requests"), stopped.value("requests"));
        QCOMPARE(daemon.events("notify").size(), 3);
        QVERIFY2(service.stop(), service.diagnostics.constData());
    }
    void actionlessRecentAccessAndOversizePreserveConnection() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(false, false, daemonPath()));
        WarningService service;
        QVERIFY2(service.start(), service.diagnostics.constData());
        QVERIFY(attach(service.socket));
        QVERIFY(enable());
        service.command({{"op", "stage"}, {"stage", "new"}});
        QVERIFY(cycle(service, daemon, 1));
        QVERIFY(daemon.events("notify").first().value("actions").toArray().isEmpty());
        QVERIFY(!eval("backend.snapshot.warning_notifications.actions").toBool());
        window->show();
        root->setProperty("effectsOpen", true);
        auto* recent = item(window->contentItem(), "recentWarning0");
        QVERIFY(recent);
        recent->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_COMPARE(eval("backend.warningState").toString(), "ready");
        QCOMPARE(eval("backend.warningDetail.place").toString(), "City 0");
        QVERIFY(closeAndHide());
        service.command({{"op", "stage"}, {"stage", "oversized"}});
        QVERIFY(cycle(service, daemon, 2));
        window->show();
        root->setProperty("effectsOpen", true);
        recent = item(window->contentItem(), "recentWarning0");
        QVERIFY(recent);
        recent->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_COMPARE(eval("backend.warningState").toString(), "unavailable");
        QVERIFY(eval("backend.warningError").toString().contains("size limit"));
        QVERIFY(eval("backend.warningDetail === null && backend.available").toBool());
        QVERIFY2(service.stop(), service.diagnostics.constData());
    }
    void daemonAndServiceRestartRequireFreshChecksWithoutReplay() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, false, daemonPath()));
        WarningService service;
        QVERIFY2(service.start(), service.diagnostics.constData());
        QVERIFY(attach(service.socket));
        QVERIFY(enable());
        service.command({{"op", "stage"}, {"stage", "new"}});
        QVERIFY(cycle(service, daemon, 1));
        daemon.stop();
        QTRY_VERIFY(!eval("backend.snapshot.warning_notifications.ready").toBool());
        const auto missing = service.command({{"op", "stage"}, {"stage", "update"}});
        const auto stillMissing = service.command({{"op", "advance"}, {"seconds", 90}});
        QCOMPARE(stillMissing.value("requests"), missing.value("requests"));
        QVERIFY(daemon.start(true, false, daemonPath()));
        QTRY_VERIFY(eval("backend.snapshot.warning_notifications.ready").toBool());
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTest::qWait(80);
        QVERIFY(!root->property("warningOpen").toBool());
        QVERIFY(cycle(service, daemon, 1));
        QVERIFY(daemon.events("notify").first().value("title").toString().startsWith("Updated:"));
        const auto fresh = service.command({{"op", "status"}});
        QVERIFY(fresh.value("requests").toInt() >= stillMissing.value("requests").toInt() + 2);
        QVERIFY(!service.command({{"op", "restart"}}).isEmpty());
        QVERIFY(attach(service.socket));
        QTRY_VERIFY(eval("backend.snapshot.warning_notifications.ready").toBool());
        QCOMPARE(eval("backend.snapshot.warning_notifications.recent.length").toInt(), 0);
        for (int i = 0; i < 5; ++i)
            QVERIFY(!service.command({{"op", "advance"}, {"seconds", 31}}).isEmpty());
        QTest::qWait(80);
        QCOMPARE(daemon.events("notify").size(), 1);
        daemon.command({{"kind", "action"}, {"id", 1}});
        QTest::qWait(80);
        QVERIFY(!root->property("warningOpen").toBool());
        service.command({{"op", "stage"}, {"stage", "error"}});
        service.command({{"op", "advance"}, {"seconds", 300}});
        QTRY_COMPARE(eval("backend.snapshot.warning_notifications.state").toString(),
                     "unavailable");
        service.command({{"op", "stage"}, {"stage", "recover"}});
        for (int i = 0; i < 5; ++i)
            service.command({{"op", "advance"}, {"seconds", 31}});
        QTRY_COMPARE(eval("backend.snapshot.warning_notifications.state").toString(), "watching");
        QCOMPARE(daemon.events("notify").size(), 1);
        QVERIFY2(service.stop(), service.diagnostics.constData());
    }
    void elapsedTimeExpiresWithoutInventingCancellation() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, false, daemonPath()));
        WarningService service;
        QVERIFY2(service.start(), service.diagnostics.constData());
        QVERIFY(attach(service.socket));
        QVERIFY(enable());
        service.command({{"op", "stage"}, {"stage", "new"}});
        QVERIFY(cycle(service, daemon, 1));
        const auto reference =
            eval("JSON.stringify({location:backend.snapshot.warning_notifications.recent[0]."
                 "location,key:backend.snapshot.warning_notifications.recent[0].key})")
                .toString();
        const auto later = service.command({{"op", "advance"}, {"seconds", 7200}});
        QVERIFY(!later.isEmpty());
        QVERIFY(openAction(daemon, daemon.events("notify").first().value("id").toInt()));
        auto* loader = root->findChild<QObject*>("warningDetailsLoader");
        QVERIFY(loader);
        auto* popup = loader->property("item").value<QObject*>();
        QVERIFY(popup);
        // Match the injected service clock to simulate both processes waking
        // after the same elapsed interval; do not change the host clock.
        popup->setProperty(
            "now",
            QDateTime::fromString(later.value("now").toString(), Qt::ISODate).toMSecsSinceEpoch());
        auto* validity = popup->findChild<QObject*>("warningDetailValidity");
        QVERIFY(validity);
        QVERIFY(validity->property("text").toString().contains("validity period has ended"));
        QVERIFY(closeAndHide());
        for (int i = 0; i < 5; ++i)
            service.command({{"op", "advance"}, {"seconds", 31}});
        QTRY_COMPARE(eval("backend.snapshot.warning_notifications.state").toString(), "watching");
        QCOMPARE(daemon.events("notify").size(), 1); // Expiry is not a cancellation message.
        service.command({{"op", "advance"}, {"seconds", 86400}});
        QTRY_COMPARE(eval("backend.snapshot.warning_notifications.recent.length").toInt(), 0);
        eval("openWarning(" + reference + ", true)");
        QTRY_COMPARE(eval("backend.warningState").toString(), "unavailable");
        QVERIFY(eval("backend.warningDetail === null && backend.available").toBool());
        QVERIFY2(service.stop(), service.diagnostics.constData());
    }
    void pausedSettingsStopRenderingWorkWhenHidden() {
        DaemonFixture daemon;
        QVERIFY(daemon.start(true, false, daemonPath()));
        WarningService service;
        QVERIFY2(service.start(), service.diagnostics.constData());
        QVERIFY(attach(service.socket));
        QVERIFY(enable());
        window->show();
        root->setProperty("effectsOpen", true);
        auto* pause = item(window->contentItem(), "warningPause");
        QVERIFY(pause);
        pause->forceActiveFocus();
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_COMPARE(eval("backend.snapshot.warning_notifications.state").toString(), "paused");
        auto* settings = root->findChild<QObject*>("warningSettingsLoader");
        QVERIFY(settings && settings->property("active").toBool());
        window->hide();
        QTRY_VERIFY(!settings->property("active").toBool());
        QTRY_VERIFY(!settings->property("item").value<QObject*>());
        service.command({{"op", "stage"}, {"stage", "new"}});
        for (int i = 0; i < 4; ++i)
            service.command({{"op", "advance"}, {"seconds", 31}});
        QCOMPARE(daemon.events("notify").size(), 0);
        window->show();
        QTRY_VERIFY(settings->property("active").toBool());
        window->showMinimized();
        QTRY_VERIFY(!settings->property("active").toBool());
        window->showNormal();
        QTRY_VERIFY(settings->property("active").toBool());
        QTRY_VERIFY(window->isExposed());
        pause = item(window->contentItem(), "warningPause");
        QVERIFY(pause);
        QTRY_VERIFY(pause->property("enabled").toBool());
        QCOMPARE(pause->property("text").toString(), "Resume delivery");
        pause->forceActiveFocus();
        QTRY_COMPARE(window->activeFocusItem(), pause);
        QTest::keyClick(window, Qt::Key_Space);
        QTRY_VERIFY(eval("backend.snapshot.warning_notifications.state !== 'paused'").toBool());
        QVERIFY(cycle(service, daemon, 1));
        QVERIFY2(service.stop(), service.diagnostics.constData());
    }
};

QTEST_MAIN(WarningE2ETest)
#include "warning_e2e_test.moc"
