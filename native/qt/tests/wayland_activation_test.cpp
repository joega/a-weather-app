#include <QChar>
#include <QBitArray>
#include <QtTest>
#include "windowactivation.h"
#include "xdg-activation-server.h"
#include <QGuiApplication>
#include <QProcess>
#include <QQuickWindow>
#include <QTemporaryDir>
#include <QWaylandCompositor>
#include <QWaylandOutput>
#include <QWaylandSeat>
#include <QWaylandSurface>
#include <QWaylandXdgShell>
#include <wayland-server-core.h>

namespace {
void destroyResource(wl_client*, wl_resource* resource) {
    wl_resource_destroy(resource);
}
void ignoreSerial(wl_client*, wl_resource*, uint32_t, wl_resource*) {}
void ignoreApp(wl_client*, wl_resource*, const char*) {}
void ignoreSurface(wl_client*, wl_resource*, wl_resource*) {}
void commitToken(wl_client*, wl_resource* resource) {
    xdg_activation_token_v1_send_done(resource, "unprivileged-fixture-token");
}
void newToken(wl_client* client, wl_resource*, uint32_t id) {
    static const struct xdg_activation_token_v1_interface implementation{
        ignoreSerial, ignoreApp, ignoreSurface, commitToken, destroyResource};
    auto* resource = wl_resource_create(client, &xdg_activation_token_v1_interface, 1, id);
    wl_resource_set_implementation(resource, &implementation, nullptr, nullptr);
}
struct ActivationRequests {
    QWaylandCompositor* compositor;
    QList<QByteArray> tokens;
    bool validSurface = true;
};
void activate(wl_client*, wl_resource* resource, const char* token, wl_resource* target) {
    auto* requests = static_cast<ActivationRequests*>(wl_resource_get_user_data(resource));
    const QByteArray value(token);
    if (!value.startsWith("notification-fixture-"))
        return; // The fixture grants focus only to its explicit notification tokens.
    requests->tokens.append(value);
    auto* surface = QWaylandSurface::fromResource(target);
    requests->validSurface &= surface != nullptr;
    if (surface)
        requests->compositor->defaultSeat()->setKeyboardFocus(surface);
}
void bindActivation(wl_client* client, void* data, uint32_t, uint32_t id) {
    static const struct xdg_activation_v1_interface implementation{destroyResource, newToken,
                                                                   activate};
    auto* resource = wl_resource_create(client, &xdg_activation_v1_interface, 1, id);
    wl_resource_set_implementation(resource, &implementation, data, nullptr);
}

int clientRun() {
    const bool supported = qEnvironmentVariable("WEATHER_TEST_ACTIVATION_SUPPORTED") == "1";
    WindowActivation activation;
    QQuickWindow window;
    window.resize(640, 480);
    qunsetenv("XDG_ACTIVATION_TOKEN");
    activation.show(&window, "notification-fixture-hidden");
    if (qEnvironmentVariableIsSet("XDG_ACTIVATION_TOKEN") ||
        !QTest::qWaitForWindowExposed(&window, 3000) ||
        (supported && !QTest::qWaitForWindowActive(&window, 3000)))
        return 1;

    // An existing process environment value must survive the temporary override.
    qputenv("XDG_ACTIVATION_TOKEN", "previous-fixture-value");
    activation.show(&window, "notification-fixture-visible");
    if (qgetenv("XDG_ACTIVATION_TOKEN") != "previous-fixture-value")
        return 2;
    qputenv("XDG_ACTIVATION_TOKEN", "");
    activation.show(&window, "notification-fixture-empty-previous");
    if (!qEnvironmentVariableIsSet("XDG_ACTIVATION_TOKEN") ||
        !qgetenv("XDG_ACTIVATION_TOKEN").isEmpty())
        return 3;
    qunsetenv("XDG_ACTIVATION_TOKEN");

    window.showMinimized();
    if (!window.windowStates().testFlag(Qt::WindowMinimized))
        return 4;
    activation.show(&window, "notification-fixture-minimized");
    if (window.windowStates().testFlag(Qt::WindowMinimized) ||
        qEnvironmentVariableIsSet("XDG_ACTIVATION_TOKEN"))
        return 5;

    // Destroy/recreate the shell role, as when a retained weather window is hidden.
    window.hide();
    activation.show(&window, "notification-fixture-reshown");
    if (qEnvironmentVariableIsSet("XDG_ACTIVATION_TOKEN") ||
        !QTest::qWaitForWindowExposed(&window, 3000) ||
        (supported && !QTest::qWaitForWindowActive(&window, 3000)))
        return 6;

    // Rejected tokens must not reach the wire or linger for an unrelated request.
    const QStringList invalid{QString(1025, 'x'), QString("notification-fixture-bad\ntoken"),
                              QString(QChar(0xd800)), QString(QChar(0x80))};
    for (const auto& value : invalid) {
        activation.show(&window, value);
        if (qEnvironmentVariableIsSet("XDG_ACTIVATION_TOKEN"))
            return 7;
    }
    window.requestActivate();
    QTest::qWait(100);
    return 0;
}
} // namespace

class WaylandActivationTest final : public QObject {
    Q_OBJECT
  private slots:
    void privateCompositor_data() {
        QTest::addColumn<bool>("supported");
        QTest::newRow("xdg-activation") << true;
        QTest::newRow("no-activation-extension") << false;
    }
    void privateCompositor() {
        QFETCH(bool, supported);
        QTemporaryDir runtime("/tmp/weather-wayland-XXXXXX");
        QVERIFY(runtime.isValid());
        QWaylandCompositor compositor;
        compositor.setSocketName((runtime.path() + "/socket").toUtf8());
        compositor.setUseHardwareIntegrationExtension(false);
        QWaylandXdgShell shell(&compositor);
        QWaylandOutput output(&compositor, nullptr);
        const QWaylandOutputMode mode(QSize(1280, 720), 60000);
        output.addMode(mode, true);
        output.setCurrentMode(mode);
        connect(&shell, &QWaylandXdgShell::toplevelCreated, &compositor,
                [](QWaylandXdgToplevel* toplevel, QWaylandXdgSurface*) {
                    toplevel->sendConfigure(QSize(640, 480), QList<QWaylandXdgToplevel::State>{});
                });
        compositor.create();
        QVERIFY(compositor.isCreated());
        ActivationRequests requests{&compositor, {}, true};
        auto* global = supported
                           ? wl_global_create(compositor.display(), &xdg_activation_v1_interface, 1,
                                              &requests, bindActivation)
                           : nullptr;
        QVERIFY(!supported || global);
        QProcess child;
        auto env = QProcessEnvironment::systemEnvironment();
        env.insert("XDG_RUNTIME_DIR", runtime.path());
        env.insert("WAYLAND_DISPLAY", compositor.socketName());
        env.remove("WAYLAND_SOCKET");
        env.insert("QT_QPA_PLATFORM", "wayland");
        env.insert("QT_WAYLAND_SHELL_INTEGRATION", "xdg-shell");
        env.insert("QT_WAYLAND_DISABLE_WINDOWDECORATION", "1");
        env.insert("QT_QUICK_BACKEND", "software");
        env.insert("WEATHER_TEST_ACTIVATION_SUPPORTED", supported ? "1" : "0");
        child.setProcessEnvironment(env);
        child.setProcessChannelMode(QProcess::MergedChannels);
        child.start(QCoreApplication::applicationFilePath(), {"--client"});
        QVERIFY(child.waitForStarted(3000));
        QTRY_VERIFY_WITH_TIMEOUT(child.state() == QProcess::NotRunning, 15000);
        const auto diagnostics = child.readAll();
        QVERIFY2(child.exitStatus() == QProcess::NormalExit && child.exitCode() == 0,
                 qPrintable(QString("client exit %1: %2").arg(child.exitCode()).arg(diagnostics)));
        QVERIFY(requests.validSurface);
        const QList<QByteArray> expected{
            "notification-fixture-hidden", "notification-fixture-visible",
            "notification-fixture-empty-previous", "notification-fixture-minimized",
            "notification-fixture-reshown"};
        QCOMPARE(requests.tokens, supported ? expected : QList<QByteArray>{});
        if (global)
            wl_global_destroy(global);
    }
};

int main(int argc, char** argv) {
    QGuiApplication app(argc, argv);
    if (app.arguments().contains("--client"))
        return clientRun();
    WaylandActivationTest test;
    return QTest::qExec(&test, argc, argv);
}
#include "wayland_activation_test.moc"
