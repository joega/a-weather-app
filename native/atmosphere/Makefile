CC ?= cc
PKGS := gtk4 gtk4-layer-shell-0 json-glib-1.0 epoxy
CFLAGS ?= -O2 -g
CFLAGS += -std=c17 -Wall -Wextra -Werror $(shell pkg-config --cflags $(PKGS))
LDLIBS += $(shell pkg-config --libs $(PKGS)) -lm
.PHONY: all clean
all: a-weather-app-atmosphere
sky_shader.h: ../../godot/shaders/atmosphere.gdshader generate_shader.py
	python3 generate_shader.py $< $@
a-weather-app-atmosphere: atmosphere.c sky_shader.h dynamics.h policy.h
	$(CC) $(CFLAGS) atmosphere.c -o $@ $(LDLIBS)
clean:
	$(RM) a-weather-app-atmosphere sky_shader.h
