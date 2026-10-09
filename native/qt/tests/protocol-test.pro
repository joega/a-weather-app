QT += testlib network dbus
QT -= gui
CONFIG += console c++17 testcase
TARGET = protocol-test
SOURCES += tests/protocol_test.cpp protocol.cpp transport.cpp desktopwarnings.cpp
HEADERS += protocol.h transport.h desktopwarnings.h
OBJECTS_DIR = .test-build
MOC_DIR = .test-build
