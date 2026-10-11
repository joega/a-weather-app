#pragma once
#include <QObject>
#include <QTranslator>
#include <QHash>
#include <QStringList>
#include <QLocale>
class QQmlEngine;
// TS catalogs are bundled in RCC. Qt Linguist's compiler is optional on Linux;
// this translator uses the same App source/numerus contract without a subprocess.
class CatalogTranslator final : public QTranslator {
  public:
    bool read(const QString& language);
    QString translate(const char* context, const char* source, const char* disambiguation = nullptr,
                      int n = -1) const override;
    bool isEmpty() const override {
        return entries.isEmpty();
    }

  private:
    QHash<QString, QStringList> entries;
    QString language;
};
class NativeI18n final : public QObject {
    Q_OBJECT
    Q_PROPERTY(QString language READ language NOTIFY changed)
    Q_PROPERTY(QString preference READ preference NOTIFY changed)
    Q_PROPERTY(QString formatLocale READ formatLocale CONSTANT)
    Q_PROPERTY(QString fontFamily READ fontFamily NOTIFY changed)
  public:
    explicit NativeI18n(QQmlEngine* engine, const QLocale& regional = QLocale::system());
    ~NativeI18n() override;
    static QString match(const QString& tag);
    static QString detect(const QStringList& preferences);
    QString language() const {
        return effective;
    }
    QString preference() const {
        return choice;
    }
    QString formatLocale() const {
        return regional.name();
    }
    QString fontFamily() const;
    Q_INVOKABLE void setPreference(const QString& value);
    Q_INVOKABLE void sync(const QString& value, const QString& resolved);
    Q_INVOKABLE QString number(double value, int decimals = 0) const;
    Q_INVOKABLE QString date(const QString& stamp, const QString& zone, const QString& kind) const;
  signals:
    void changed();

  private:
    void apply(const QString& value, const QString& resolved);
    QQmlEngine* engine;
    CatalogTranslator translator;
    QLocale regional;
    QString automatic, choice = "auto", effective = "en";
};
