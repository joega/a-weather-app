QT += testlib quick network
CONFIG += console c++17 testcase
TARGET = frontend-test
SOURCES += tests/frontend_test.cpp
RESOURCES += resources.qrc
OBJECTS_DIR = .frontend-test-build
MOC_DIR = .frontend-test-build
RCC_DIR = .frontend-test-build
