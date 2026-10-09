#include "windowactivation.h"
#include <QGuiApplication>
#include <QThread>
#include <algorithm>

void WindowActivation::show(QWindow* window, const QString& activationToken) {
    if (!window || !qGuiApp || QThread::currentThread() != qGuiApp->thread())
        return;
    // Keep maximized/fullscreen state while restoring a minimized window.
    window->setWindowStates(window->windowStates() & ~Qt::WindowMinimized);
    window->show();
    window->raise();

    const bool usable =
        !activationToken.isEmpty() && activationToken.size() <= 1024 &&
        activationToken.isValidUtf16() &&
        std::none_of(activationToken.cbegin(), activationToken.cend(), [](QChar c) {
            return c.unicode() < 0x20 || (c.unicode() >= 0x7f && c.unicode() <= 0x9f);
        });
    if (!usable || !QGuiApplication::platformName().startsWith("wayland")) {
        window->requestActivate();
        return;
    }

    // Qt's xdg-shell implementation reads and consumes this variable synchronously
    // in requestActivate(). Show first so a hidden window has a shell surface.
    // Scope it to that call: never retain a token for a later unrelated activation,
    // including on compositors without xdg-activation. No event-loop wait or child
    // process launch may be introduced inside this scope. No Qt private ABI needed.
    const auto previous = qgetenv("XDG_ACTIVATION_TOKEN");
    if (!qputenv("XDG_ACTIVATION_TOKEN", activationToken.toUtf8())) {
        window->requestActivate();
        return;
    }
    window->requestActivate();
    if (previous.isNull())
        qunsetenv("XDG_ACTIVATION_TOKEN");
    else
        qputenv("XDG_ACTIVATION_TOKEN", previous);
}
