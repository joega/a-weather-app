QT += testlib network
QT -= gui
CONFIG += console c++17 testcase
TARGET = protocol-test
SOURCES += tests/protocol_test.cpp protocol.cpp transport.cpp
HEADERS += protocol.h transport.h
OBJECTS_DIR = .test-build
MOC_DIR = .test-build
