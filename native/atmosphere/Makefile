CC ?= cc
PKGS := gtk4 gtk4-layer-shell-0 json-glib-1.0 epoxy
CFLAGS ?= -O2 -g
CFLAGS += -std=c17 -Wall -Wextra -Werror -fstack-protector-strong -D_FORTIFY_SOURCE=3 $(shell pkg-config --cflags $(PKGS))
LDFLAGS += -Wl,-z,relro,-z,now,-z,noexecstack
LDLIBS += $(shell pkg-config --libs $(PKGS)) -lm
.PHONY: all clean
all: a-weather-app-atmosphere
sky_shader.h: ../../godot/shaders/atmosphere.gdshader generate_shader.py
	python3 generate_shader.py $< $@
a-weather-app-atmosphere: atmosphere.c sky_shader.h dynamics.h policy.h strict_json.h
	$(CC) $(CFLAGS) $(LDFLAGS) atmosphere.c -o $@ $(LDLIBS)
clean:
	$(RM) a-weather-app-atmosphere sky_shader.h
