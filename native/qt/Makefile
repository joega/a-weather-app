.PHONY: all clean test test-e2e test-warnings test-warning-e2e test-wayland-activation
GO ?= go
GO_APP ?= $(abspath ../../build/a-weather-app)
all:
	qmake6 a-weather-app-qt.pro -o .build.mk
	$(MAKE) -f .build.mk
test:
	qmake6 tests/protocol-test.pro -o .test.mk
	$(MAKE) -f .test.mk
	./protocol-test
	qmake6 tests/frontend-test.pro -o .frontend-test.mk
	$(MAKE) -f .frontend-test.mk
	env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software ./frontend-test
	$(MAKE) test-warnings
	$(MAKE) test-warning-e2e
test-warnings:
	qmake6 tests/desktopwarnings-test.pro -o .warnings-test.mk
	$(MAKE) -f .warnings-test.mk
	env WEATHER_WARNING_PRIVATE_BUS=1 dbus-run-session -- ./desktopwarnings-test
test-warning-e2e:
	mkdir -p .warning-e2e-build
	cd ../.. && $(GO) test -race -c -o native/qt/.warning-e2e-build/service.test ./internal/app
	qmake6 tests/desktopwarnings-test.pro -o .warnings-test.mk
	$(MAKE) -f .warnings-test.mk
	qmake6 tests/warning-e2e-test.pro -o .warning-e2e.mk
	$(MAKE) -f .warning-e2e.mk
	env WEATHER_WARNING_PRIVATE_BUS=1 GO_WARNING_FIXTURE="$(abspath .warning-e2e-build/service.test)" QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software dbus-run-session -- ./warning-e2e-test
test-e2e:
	test -x "$(GO_APP)"
	qmake6 tests/service-frontend-test.pro -o .service-test.mk
	$(MAKE) -f .service-test.mk
	env GO_APP="$(GO_APP)" QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software ./service-frontend-test
test-wayland-activation:
	mkdir -p .wayland-test-build
	wayland-scanner server-header "$$(pkg-config --variable=pkgdatadir wayland-protocols)/staging/xdg-activation/xdg-activation-v1.xml" .wayland-test-build/xdg-activation-server.h
	wayland-scanner private-code "$$(pkg-config --variable=pkgdatadir wayland-protocols)/staging/xdg-activation/xdg-activation-v1.xml" .wayland-test-build/xdg-activation-protocol.c
	qmake6 tests/wayland-activation-test.pro -o .wayland-test.mk
	$(MAKE) -f .wayland-test.mk
	env QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_IM_MODULE=none ./wayland-activation-test
clean:
	$(RM) a-weather-app-qt protocol-test frontend-test .build.mk .test.mk .frontend-test.mk
	$(RM) .build/*.o .build/moc_* .build/qrc_* .test-build/*.o .test-build/moc_* .test-build/protocol_test.moc
	$(RM) .frontend-test-build/*.o .frontend-test-build/moc_* .frontend-test-build/qrc_* .frontend-test-build/frontend_test.moc
	$(RM) service-frontend-test .service-test.mk .service-test-build/*.o .service-test-build/moc_* .service-test-build/qrc_* .service-test-build/service_frontend_test.moc
	$(RM) desktopwarnings-test .warnings-test.mk .warnings-test-build/*.o .warnings-test-build/moc_* .warnings-test-build/desktopwarnings_test.moc
	$(RM) warning-e2e-test .warning-e2e.mk .warning-e2e-build/*.o .warning-e2e-build/moc_* .warning-e2e-build/qrc_* .warning-e2e-build/warning_e2e_test.moc .warning-e2e-build/service.test
	$(RM) wayland-activation-test .wayland-test.mk .wayland-test-build/*.o .wayland-test-build/moc_* .wayland-test-build/wayland_activation_test.moc .wayland-test-build/xdg-activation-*
