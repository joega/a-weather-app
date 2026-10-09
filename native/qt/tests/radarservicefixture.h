#pragma once
#include <QProcess>
#include <QProcessEnvironment>
#include <QJsonObject>
#include <QJsonDocument>
#include <QtTest>

// Private Go helper drives the production service; commands are stdin-only.
class RadarServiceFixture {
    QProcess process;
    QByteArray buffer;
    QJsonObject reply() {
        QElapsedTimer elapsed;
        elapsed.start();
        while (elapsed.elapsed() < 5000) {
            buffer += process.readAll();
            if (buffer.size() > 262144)
                return {};
            qsizetype end;
            while ((end = buffer.indexOf('\n')) >= 0) {
                const auto line = buffer.left(end);
                buffer.remove(0, end + 1);
                if (line.startsWith("RADAR_FIXTURE "))
                    return QJsonDocument::fromJson(line.mid(14)).object();
                diagnostics += line + '\n';
            }
            QTest::qWait(10);
        }
        return {};
    }

  public:
    QString socket;
    QByteArray diagnostics;
    ~RadarServiceFixture() {
        if (process.state() == QProcess::NotRunning)
            return;
        process.write("{\"Op\":\"stop\"}\n");
        if (!process.waitForFinished(7000)) {
            process.kill();
            process.waitForFinished(2000);
        }
    }
    bool start() {
        auto env = QProcessEnvironment::systemEnvironment();
        env.insert("WEATHER_NATIVE_RADAR_FIXTURE", "1");
        env.remove("DBUS_SESSION_BUS_ADDRESS");
        env.remove("HYPRLAND_INSTANCE_SIGNATURE");
        process.setProcessEnvironment(env);
        process.setProcessChannelMode(QProcess::MergedChannels);
        process.start(qEnvironmentVariable("GO_RADAR_FIXTURE"),
                      {"-test.run=^TestRadarNativeServiceFixture$", "-test.timeout=180s"});
        if (!process.waitForStarted(3000))
            return false;
        socket = reply().value("socket").toString();
        return !socket.isEmpty();
    }
    QJsonObject command(const QJsonObject& value = {{"Op", "status"}}) {
        process.write(QJsonDocument(value).toJson(QJsonDocument::Compact) + '\n');
        return reply();
    }
};
