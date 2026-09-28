#include <QtTest>
#include "../protocol.h"
#include "../transport.h"
#include <QLocalServer>
#include <QTemporaryDir>
class ProtocolTest:public QObject {
    Q_OBJECT
private slots:
    void valid(){QJsonObject o;QVERIFY(decodeProtocol(R"({"version":1,"request_id":2147483647,"ok":true,"snapshot":{"snapshot_revision":1}})",&o));QVERIFY(decodeProtocol(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":9007199254740991}})",&o));QVERIFY(decodeProtocol(R"({"version":1,"event":"toggle_window"})",&o));QVERIFY(decodeProtocol(R"({"version":1,"event":"service_stopped","ok":true})",&o));QVERIFY(decodeProtocol(R"({"version":1,"event":"service_stopped","ok":false,"error":"cleanup_failed"})",&o));}
    void literalAndEscapedKeys(){
        QJsonObject object;
        QVERIFY(decodeProtocol("{\"version\":1,\"event\":\"toggle_window\",\"\":1,\"é\":2,\"😀\":3,\"slash\\/key\":4,\"quote\\\"key\":5}",&object));
        QCOMPARE(object.value(QString::fromUtf8("é")).toInt(),2);QCOMPARE(object.value(QString::fromUtf8("😀")).toInt(),3);
        QCOMPARE(object.value("slash/key").toInt(),4);QCOMPARE(object.value("quote\"key").toInt(),5);
        // Removing either spelling produces valid envelopes, so duplicate
        // regressions cannot pass merely because some other field is invalid.
        QVERIFY(decodeProtocol(R"({"version":1,"event":"toggle_window","\u00e9":1,"\ud83d\ude00":2})",&object));
        QCOMPARE(object.value(QString::fromUtf8("é")).toInt(),1);QCOMPARE(object.value(QString::fromUtf8("😀")).toInt(),2);
        QVERIFY(decodeProtocol(R"({"version":1,"event":"toggle_window","a":1,"\uFEFFa":2})",&object));
        QCOMPARE(object.value("a").toInt(),1);
        QCOMPARE(object.value(QString(QChar(0xfeff))+"a").toInt(),2);
    }
    void invalid_data(){
        QTest::addColumn<QByteArray>("line");
        QTest::newRow("duplicate")<<QByteArray(R"({"version":1,"version":1,"event":"toggle_window"})");
        QTest::newRow("escaped duplicate")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":1,"a":1,"\u0061":2}})");
        QTest::newRow("BMP literal escaped duplicate")<<QByteArray(R"({"version":1,"event":"toggle_window","é":1,"\u00e9":2})");
        QTest::newRow("astral literal escaped duplicate")<<QByteArray(R"({"version":1,"event":"toggle_window","😀":1,"\ud83d\ude00":2})");
        QTest::newRow("escaped then literal duplicate")<<QByteArray(R"({"version":1,"event":"toggle_window","\u00e9":1,"é":2})");
        QTest::newRow("literal BOM escaped duplicate")<<(QByteArray("{\"version\":1,\"event\":\"toggle_window\",\"")+QByteArray("\xef\xbb\xbf",3)+"a\":1,\"\\uFEFFa\":2}");
        QTest::newRow("literal BOM distinct from plain rejected")<<(QByteArray("{\"version\":1,\"event\":\"toggle_window\",\"a\":1,\"")+QByteArray("\xef\xbb\xbf",3)+"a\":2}");
        QTest::newRow("literal BOM key alone rejected")<<(QByteArray("{\"version\":1,\"event\":\"toggle_window\",\"")+QByteArray("\xef\xbb\xbf",3)+"a\":1}");
        QTest::newRow("key invalid escape")<<QByteArray(R"({"version":1,"event":"toggle_window","key\x":1})");
        QTest::newRow("key invalid unicode escape")<<QByteArray(R"({"version":1,"event":"toggle_window","key\u00zz":1})");
        QTest::newRow("literal key invalid UTF8")<<(QByteArray(R"({"version":1,"event":"toggle_window",")")+char(0xff)+"\":1}");
        QTest::newRow("literal key overlong UTF8")<<(QByteArray(R"({"version":1,"event":"toggle_window",")")+char(0xc0)+char(0xaf)+"\":1}");
        QTest::newRow("literal key UTF8 surrogate")<<(QByteArray(R"({"version":1,"event":"toggle_window",")")+char(0xed)+char(0xa0)+char(0x80)+"\":1}");
        QTest::newRow("literal key control")<<(QByteArray(R"({"version":1,"event":"toggle_window",")")+char(0x01)+"\":1}");
        QTest::newRow("overflow")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"a":1e999}})");
        QTest::newRow("nonfinite")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"a":NaN}})");
        QTest::newRow("utf8")<<(QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"a":")")+char(0xff)+"\"}} ");
        QTest::newRow("depth")<<(QByteArray(R"({"version":1,"event":"snapshot","snapshot":)")+QByteArray(17,'[')+"0"+QByteArray(17,']')+"}");
        QTest::newRow("big")<<QByteArray(262145,' ');
        QTest::newRow("bool version")<<QByteArray(R"({"version":true,"event":"toggle_window"})");
        QTest::newRow("fraction id")<<QByteArray(R"({"version":1,"request_id":0.5,"ok":true})");
        QTest::newRow("bool id")<<QByteArray(R"({"version":1,"request_id":false,"ok":true})");
        QTest::newRow("unknown event")<<QByteArray(R"({"version":1,"event":"exit"})");
        QTest::newRow("missing error")<<QByteArray(R"({"version":1,"request_id":0,"ok":false})");
        QTest::newRow("unbounded error")<<QByteArray(R"({"version":1,"request_id":0,"ok":false,"error":"bad data!"})");
        QTest::newRow("stopped missing ok")<<QByteArray(R"({"version":1,"event":"service_stopped"})");
        QTest::newRow("stopped missing error")<<QByteArray(R"({"version":1,"event":"service_stopped","ok":false})");
        QTest::newRow("stopped invalid error")<<QByteArray(R"({"version":1,"event":"service_stopped","ok":false,"error":"unknown"})");
        QTest::newRow("stopped unexpected error")<<QByteArray(R"({"version":1,"event":"service_stopped","ok":true,"error":"cleanup_failed"})");
        QTest::newRow("revision missing")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{}})");
        QTest::newRow("revision bool")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":true}})");
        QTest::newRow("revision zero")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":0}})");
        QTest::newRow("revision negative")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":-1}})");
        QTest::newRow("revision fractional")<<QByteArray(R"({"version":1,"request_id":0,"ok":true,"snapshot":{"snapshot_revision":1.5}})");
        QTest::newRow("revision oversized")<<QByteArray(R"({"version":1,"event":"snapshot","snapshot":{"snapshot_revision":9007199254740992}})");
    }
    void invalid(){QFETCH(QByteArray,line);QJsonObject o;QVERIFY(!decodeProtocol(line,&o));}
    void stream(){
        QTemporaryDir directory;QVERIFY(directory.isValid());QLocalServer server;const auto path=directory.path()+"/socket";QVERIFY(server.listen(path));
        WeatherTransport client(path,false);QSignalSpy ready(&client,&WeatherTransport::ready),messages(&client,&WeatherTransport::message),errors(&client,&WeatherTransport::unavailable);
        client.start();QTRY_COMPARE(ready.size(),1);QTRY_VERIFY(server.hasPendingConnections());QScopedPointer<QLocalSocket> peer(server.nextPendingConnection());
        peer->write("{\"version\":1,\"event\":\"toggle_");peer->flush();QTest::qWait(10);QCOMPARE(messages.size(),0);
        peer->write("window\"}\n{\"version\":1,\"request_id\":0,\"ok\":true}\n");peer->flush();QTRY_COMPARE(messages.size(),2);
        QVERIFY(client.send({{"version",1},{"request_id",0},{"op","subscribe"}}));QTRY_VERIFY(peer->canReadLine());QVERIFY(peer->readLine().contains("subscribe"));
        peer->write("{\"version\":1,\"version\":1,\"event\":\"toggle_window\"}\n");peer->flush();QTRY_COMPARE(errors.size(),1);QVERIFY(!client.connected());
    }
    void disconnect(){
        QTemporaryDir directory;QLocalServer server;const auto path=directory.path()+"/socket";QVERIFY(server.listen(path));
        WeatherTransport client(path,false);QSignalSpy ready(&client,&WeatherTransport::ready),errors(&client,&WeatherTransport::unavailable);
        client.start();QTRY_COMPARE(ready.size(),1);QTRY_VERIFY(server.hasPendingConnections());QScopedPointer<QLocalSocket> peer(server.nextPendingConnection());peer->close();QTRY_COMPARE(errors.size(),1);QVERIFY(!client.connected());
    }
};
QTEST_GUILESS_MAIN(ProtocolTest)
#include "protocol_test.moc"
