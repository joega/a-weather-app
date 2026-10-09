#pragma once
#include <QObject>
#include <QQuickImageProvider>
#include <atomic>
#include <memory>

struct RadarImageDemand {
    std::atomic<quint64> generation{0};
    std::atomic<bool> active{false};
};
// GUI-owned demand flag; image fetching/decoding runs on Qt's image thread.
class RadarImageControl final : public QObject {
    Q_OBJECT
    std::shared_ptr<RadarImageDemand> demand = std::make_shared<RadarImageDemand>();

  public:
    explicit RadarImageControl(QObject* parent = nullptr) : QObject(parent) {}
    ~RadarImageControl() override {
        setActive(false);
    }
    Q_INVOKABLE void setActive(bool active) {
        if (demand->active.exchange(active) != active)
            ++demand->generation;
    }
    Q_INVOKABLE void invalidate() {
        ++demand->generation;
    }
    std::shared_ptr<RadarImageDemand> state() const {
        return demand;
    }
};
class RadarImageProvider final : public QQuickImageProvider {
    QString socketPath;
    std::shared_ptr<RadarImageDemand> demand;

  public:
    RadarImageProvider(QString socketPath, std::shared_ptr<RadarImageDemand> demand);
    QImage requestImage(const QString& id, QSize* size, const QSize& requestedSize) override;
};
