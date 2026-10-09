#pragma once

#include <QObject>
#include <QWindow>

// Platform window presentation only. Warning authorization stays in the service.
class WindowActivation final : public QObject {
    Q_OBJECT
  public:
    using QObject::QObject;
    Q_INVOKABLE void show(QWindow* window, const QString& activationToken);
};
