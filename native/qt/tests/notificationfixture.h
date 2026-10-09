#pragma once
#include <QCoreApplication>
#include <QElapsedTimer>
#include <QJsonDocument>
#include <QJsonObject>
#include <QProcess>

// The caller must run in a private dbus-run-session.
class DaemonFixture final {
    QProcess process;
    QByteArray buffer;
    QList<QJsonObject> records;

  public:
    ~DaemonFixture() {
        stop();
    }
    bool start(bool actions = true, bool delay = false,
               const QString& program = QCoreApplication::applicationFilePath()) {
        stop();
        buffer.clear();
        records.clear();
        QStringList args{"--mock-daemon"};
        if (!actions)
            args << "--no-actions";
        if (delay)
            args << "--delayed";
        process.start(program, args);
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
