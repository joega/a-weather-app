.PHONY: all clean test test-e2e
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
test-e2e:
	test -x "$(GO_APP)"
	qmake6 tests/service-frontend-test.pro -o .service-test.mk
	$(MAKE) -f .service-test.mk
	env GO_APP="$(GO_APP)" QT_QPA_PLATFORM=offscreen QT_QPA_PLATFORMTHEME=generic QT_QUICK_CONTROLS_STYLE=Basic QT_IM_MODULE=none QT_QUICK_BACKEND=software ./service-frontend-test
clean:
	$(RM) a-weather-app-qt protocol-test frontend-test .build.mk .test.mk .frontend-test.mk
	$(RM) .build/*.o .build/moc_* .build/qrc_* .test-build/*.o .test-build/moc_* .test-build/protocol_test.moc
	$(RM) .frontend-test-build/*.o .frontend-test-build/moc_* .frontend-test-build/qrc_* .frontend-test-build/frontend_test.moc
	$(RM) service-frontend-test .service-test.mk .service-test-build/*.o .service-test-build/moc_* .service-test-build/qrc_* .service-test-build/service_frontend_test.moc
