#include "protocol.h"
#include <QJsonDocument>
#include <QJsonArray>
#include <QJsonParseError>
#include <QSet>
#include <cmath>

namespace {
class Validator {
    const QByteArray &s;
    qsizetype i = 0;
    void ws() { while (i < s.size() && (s[i]==' ' || s[i]=='\t' || s[i]=='\r')) ++i; }
    bool take(char c) { ws(); if (i<s.size() && s[i]==c) {++i;return true;} return false; }
    bool string(QString *result=nullptr) {
        ws(); const auto begin=i;
        if (i>=s.size() || s[i++]!='"') return false;
        bool ended=false,escaped=false,ascii=true;
        while(i<s.size()) {
            const unsigned char c=s[i++];
            if(c>=128)ascii=false;
            if(c=='"') {ended=true;break;}
            if(c<32) return false;
            if(c=='\\') {
                escaped=true;
                if(i>=s.size())return false;
                char escape=s[i++];
                if(escape=='u') {for(int j=0;j<4;++j) {if(i>=s.size())return false;char h=s[i++];if(!((h>='0'&&h<='9')||(h>='a'&&h<='f')||(h>='A'&&h<='F')))return false;}}
                else if(!QByteArray("\"\\/bfnrt").contains(escape))return false;
            }
        }
        if(!ended)return false;
        if(result) {
            // Qt's JSON string decoder discards a literal UTF-8 BOM at the
            // beginning of a key, but keeps an escaped \uFEFF. Reject raw BOM
            // keys so two spellings cannot acquire different meanings between
            // this duplicate scan and the full envelope decoder.
            if(!ascii&&s.mid(begin+1,i-begin-2).contains(QByteArray("\xef\xbb\xbf",3)))return false;
            // Only plain ASCII keys avoid Qt's canonical JSON decoder. Unicode
            // (including BOM) and escaped spellings must share the same decoder
            // to keep duplicate-key equivalence exact.
            if(!escaped&&ascii) {*result=QString::fromLatin1(s.constData()+begin+1,i-begin-2);return true;}
            QJsonParseError err;
            auto doc=QJsonDocument::fromJson("["+s.mid(begin,i-begin)+"]",&err);
            if(err.error!=QJsonParseError::NoError)return false;
            *result=doc.array().first().toString();
        }
        return true;
    }
    bool value(int depth) {
        if(depth>16)return false;
        ws(); if(i>=s.size())return false;
        if(s[i]=='"')return string();
        if(s[i]=='{') {
            ++i;QSet<QString> keys;if(take('}'))return true;
            do {QString key;if(!string(&key)||keys.contains(key)||!take(':'))return false;keys.insert(key);if(!value(depth+1))return false;}while(take(','));
            return take('}');
        }
        if(s[i]=='[') {++i;if(take(']'))return true;do {if(!value(depth+1))return false;}while(take(','));return take(']');}
        for(const QByteArray &literal:{QByteArray("true"),QByteArray("false"),QByteArray("null")})if(s.mid(i,literal.size())==literal){i+=literal.size();return true;}
        auto begin=i;
        while(i<s.size() && QByteArray("-+0123456789.eE").contains(s[i]))++i;
        if(begin==i)return false;
        bool ok=false;const double n=s.mid(begin,i-begin).toDouble(&ok);
        return ok&&std::isfinite(n);
    }
public:
    explicit Validator(const QByteArray &input):s(input){}
    bool run(){if(!value(0))return false;ws();return i==s.size();}
};
}

bool decodeProtocol(const QByteArray &line, QJsonObject *object) {
    if(line.isEmpty()||line.size()>262144)return false;
    if(!line.isValidUtf8())return false;
    if(!Validator(line).run())return false;
    QJsonParseError error;const auto doc=QJsonDocument::fromJson(line,&error);
    if(error.error!=QJsonParseError::NoError||!doc.isObject())return false;
    const auto obj=doc.object();
    if(!obj.value("version").isDouble()||obj.value("version").toDouble()!=1)return false;
    if(obj.contains("snapshot")) {
        if(!obj.value("snapshot").isObject())return false;
        const auto revision=obj.value("snapshot").toObject().value("snapshot_revision");
        if(!revision.isDouble()||revision.toDouble()<1||revision.toDouble()>9007199254740991.0||std::floor(revision.toDouble())!=revision.toDouble())return false;
    }
    if(obj.contains("event")) {
        if(!obj.value("event").isString())return false;
        const auto event=obj.value("event").toString();
        if(event!="snapshot"&&event!="toggle_window"&&event!="service_stopped"&&event!="map")return false;
        if(event=="snapshot"&&!obj.value("snapshot").isObject())return false;
        if(event=="map"&&!obj.value("map").isObject())return false;
        if(obj.contains("request_id"))return false;
        if(event=="service_stopped") {
            if(!obj.value("ok").isBool())return false;
            if(!obj.value("ok").toBool()&&obj.value("error").toString()!="cleanup_failed")return false;
            if(obj.value("ok").toBool()&&obj.contains("error"))return false;
        }else if(obj.contains("ok"))return false;
    }else {
        const auto id=obj.value("request_id");
        if(!id.isDouble()||id.toDouble()<0||id.toDouble()>2147483647||std::floor(id.toDouble())!=id.toDouble()||!obj.value("ok").isBool())return false;
        if(obj.contains("snapshot")&&!obj.value("snapshot").isObject())return false;
        if(!obj.value("ok").toBool()||obj.contains("error")) {
            if(!obj.value("error").isString())return false;
            const auto code=obj.value("error").toString();if(code.isEmpty()||code.size()>80)return false;
            for(const auto c:code)if(!((c>='a'&&c<='z')||(c>='0'&&c<='9')||c=='_'))return false;
        }
    }
    *object=obj;return true;
}
