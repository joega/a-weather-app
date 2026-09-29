GO ?= go
APP_MODE ?= development
APP_VERSION ?= 0.51.7
.PHONY: all go qt test native shaders native-tools
all: go qt
go:
	mkdir -p build
	CGO_ENABLED=1 $(GO) build -trimpath -buildvcs=false -buildmode=pie -ldflags='-s -w -buildid= -linkmode=external -extldflags=-Wl,-z,relro,-z,now,-z,noexecstack -X main.buildMode=$(APP_MODE) -X main.appVersion=$(APP_VERSION)' -o build/a-weather-app ./cmd/a-weather-app
qt:
	$(MAKE) -C native/qt
test:
	$(GO) test -race ./...
	$(GO) vet ./...
	$(MAKE) -C native/qt test
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
