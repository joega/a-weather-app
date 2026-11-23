QT += quick network dbus
CONFIG += c++17
CONFIG -= app_bundle
# Apply the release protections to local builds as well.
QMAKE_CXXFLAGS += -fstack-protector-strong -D_FORTIFY_SOURCE=3
QMAKE_LFLAGS += -Wl,-z,relro,-z,now,-z,noexecstack
TARGET = a-weather-app-qt
SOURCES += i18n.cpp radarimages.cpp main.cpp transport.cpp protocol.cpp maptiles.cpp graphicscapabilities.cpp desktopwarnings.cpp windowactivation.cpp
HEADERS += i18n.h radarimages.h pngvalidation.h transport.h protocol.h maptiles.h graphicscapabilities.h desktopwarnings.h windowactivation.h
RESOURCES += i18n.cpp resources.qrc
OBJECTS_DIR = .build
MOC_DIR = .build
RCC_DIR = .build
