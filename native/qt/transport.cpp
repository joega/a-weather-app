#include "transport.h"
#include "protocol.h"
#include <QJsonDocument>
#include <QJsonObject>
#include <QTimer>
#include <QDebug>
#include <sys/socket.h>
#include <unistd.h>

WeatherTransport::WeatherTransport(QString socketPath,bool diagnostic,QObject *parent):QObject(parent),path(std::move(socketPath)),verbose(diagnostic) {
    connect(&socket,&QLocalSocket::connected,this,[this]{
        struct ucred peer{};socklen_t size=sizeof(peer);
        if(getsockopt(socket.socketDescriptor(),SOL_SOCKET,SO_PEERCRED,&peer,&size)!=0||peer.uid!=getuid()){fail("Weather service identity could not be verified");return;}
        if(verbose)qInfo()<<"Weather service connected";
        emit connectedChanged();emit ready();
    });
    connect(&socket,&QLocalSocket::readyRead,this,&WeatherTransport::read);
    connect(&socket,&QLocalSocket::disconnected,this,[this]{fail("Weather service stopped. Close and reopen to reconnect.");});
    connect(&socket,&QLocalSocket::errorOccurred,this,[this](QLocalSocket::LocalSocketError){fail("Weather service is unavailable. Close and reopen to reconnect.");});
}
void WeatherTransport::start(){if(failed||socket.state()!=QLocalSocket::UnconnectedState)return;if(verbose)qInfo()<<"Weather service connecting";socket.connectToServer(path);QTimer::singleShot(5000,this,[this]{if(!connected())fail("Weather service connection timed out");});}
bool WeatherTransport::send(const QVariantMap &request){
    if(!connected())return false;
    const auto bytes=QJsonDocument(QJsonObject::fromVariantMap(request)).toJson(QJsonDocument::Compact);
    if(bytes.size()>8192||socket.bytesToWrite()>8192)return false;
    if(verbose)qInfo()<<"Weather service request:"<<request.value("op").toString().left(64);
    return socket.write(bytes+'\n')==bytes.size()+1;
}
void WeatherTransport::disconnectService(){fail("Weather service connection closed");}
void WeatherTransport::fail(const QString &error){if(failed)return;if(verbose)qWarning().noquote()<<"Weather service transport:"<<error;failed=true;buffer.clear();socket.abort();emit connectedChanged();emit unavailable(error);}
void WeatherTransport::read(){
    while(socket.bytesAvailable()>0&&!failed){
        auto chunk=socket.read(qMin<qint64>(socket.bytesAvailable(),16384));buffer+=chunk;
        qsizetype newline;
        while((newline=buffer.indexOf('\n'))>=0){
            const auto line=buffer.left(newline);buffer.remove(0,newline+1);QJsonObject obj;
            if(!decodeProtocol(line,&obj)){fail("Weather service returned invalid data");return;}
            // Preserve JSON array/null semantics for the unchanged Forecast.js validator.
            emit message(QString::fromUtf8(line));
        }
        if(buffer.size()>262144){fail("Weather service response exceeded its limit");return;}
    }
}
