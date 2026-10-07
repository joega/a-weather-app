GO ?= go
APP_MODE ?= development
APP_VERSION ?= 0.61.2
STATICCHECK_VERSION := v0.8.1
GOIMPORTS_VERSION := v0.44.0
STATICCHECK := $(CURDIR)/build/tools/staticcheck-$(STATICCHECK_VERSION)/staticcheck
GOIMPORTS := $(CURDIR)/build/tools/goimports-$(GOIMPORTS_VERSION)/goimports
.PHONY: all go qt test test-go lint check-go-format native shaders native-tools
all: go qt
go:
	mkdir -p build
	CGO_ENABLED=1 $(GO) build -trimpath -buildvcs=false -buildmode=pie -ldflags='-s -w -buildid= -linkmode=external -extldflags=-Wl,-z,relro,-z,now,-z,noexecstack -X main.buildMode=$(APP_MODE) -X main.appVersion=$(APP_VERSION)' -o build/a-weather-app ./cmd/a-weather-app
qt:
	$(MAKE) -C native/qt
test: test-go
	$(MAKE) -C native/qt test
test-go: lint
	$(GO) test -race ./...
	$(GO) vet ./...
lint: check-go-format $(STATICCHECK)
	XDG_CACHE_HOME="$(CURDIR)/build/tools/cache" $(STATICCHECK) ./...
check-go-format: $(GOIMPORTS)
	@files="$$(find cmd internal -name '*.go' -print0 | xargs -0 $(GOIMPORTS) -l)" || exit $$?; \
	if [ -n "$$files" ]; then printf 'Run goimports on these files:\n%s\n' "$$files"; exit 1; fi
$(STATICCHECK):
	GOBIN="$(dir $(STATICCHECK))" $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
$(GOIMPORTS):
	GOBIN="$(dir $(GOIMPORTS))" $(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)
# Development preparation only; these targets never install host packages.
native: native-tools
	$(MAKE) -B -C native/frame-alignment
	$(MAKE) -B -C native/atmosphere
native-tools: build/weather-native-check
build/weather-native-check: $(wildcard internal/nativebuild/*.go cmd/weather-native-check/*.go internal/safeio/*.go internal/elfsafe/*.go) go.mod
	mkdir -p build
	$(GO) build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' -o $@ ./cmd/weather-native-check
shaders: native-tools
	build/weather-native-check shaders --root "$(CURDIR)"

.PHONY: format-native-qml check-native-qml-format
format-native-qml:
	bash scripts/check_native_qml_format.sh format
check-native-qml-format:
	bash scripts/check_native_qml_format.sh check

.PHONY: check-native-analysis
check-native-analysis:
	bash scripts/check_native_analysis.sh

.PHONY: check-qml-analysis check-native-qml
check-qml-analysis:
	bash scripts/check_qml_analysis.sh
check-native-qml: check-native-qml-format check-native-analysis check-qml-analysis
