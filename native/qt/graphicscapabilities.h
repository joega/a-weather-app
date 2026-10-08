#pragma once
#include <QObject>
#include <QQuickWindow>

// Read graphics identity on Qt's render thread; publish only on the GUI thread.
// Unclassified adapters remain static. Software GL is distinct from Qt raster.
class GraphicsCapabilities final : public QObject {
    Q_OBJECT
    Q_PROPERTY(bool shaderSupported READ shaderSupported NOTIFY changed)
    Q_PROPERTY(int preflightCount READ preflightCount NOTIFY changed)
    Q_PROPERTY(QString renderer READ renderer NOTIFY changed)
  public:
    explicit GraphicsCapabilities(QObject* parent = nullptr) : QObject(parent) {}
    void observe(QQuickWindow* window);
    bool shaderSupported() const {
        return supported;
    }
    int preflightCount() const {
        return attempts;
    }
    QString renderer() const {
        return identity;
    }
    static bool softwareRenderer(const QString& renderer);
    static bool supportedFormat(const QSurfaceFormat& format, bool es);
  signals:
    void changed();

  private:
    int attempts = 0;
    bool supported = false;
    QString identity;
};
