CC ?= cc
.DEFAULT_GOAL := all
PKGS := gtk4 gtk4-layer-shell-0 json-glib-1.0 epoxy
CFLAGS ?= -O2 -g
CFLAGS += -std=c17 -Wall -Wextra -Werror -fstack-protector-strong -D_FORTIFY_SOURCE=3 $(shell pkg-config --cflags $(PKGS))
LDFLAGS += -Wl,-z,relro,-z,now,-z,noexecstack
LDLIBS += $(shell pkg-config --libs $(PKGS)) -lm
NATIVE_CHECK ?= ../../build/weather-native-check
ifeq ($(NATIVE_CHECK),../../build/weather-native-check)
$(NATIVE_CHECK): $(wildcard ../../internal/nativebuild/*.go ../../cmd/weather-native-check/*.go ../../internal/safeio/*.go ../../internal/elfsafe/*.go) ../../go.mod
	$(MAKE) -C ../.. native-tools
endif
.PHONY: all clean
all: a-weather-app-atmosphere
sky_shader.h: ../../godot/shaders/atmosphere.gdshader $(NATIVE_CHECK)
	$(NATIVE_CHECK) shader-header $< $@
a-weather-app-atmosphere: atmosphere.c sky_shader.h dynamics.h policy.h strict_json.h
	$(CC) $(CFLAGS) $(LDFLAGS) atmosphere.c -o $@ $(LDLIBS)
clean:
	$(RM) a-weather-app-atmosphere sky_shader.h
