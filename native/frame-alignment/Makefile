CXX ?= c++
PKG_CONFIG ?= pkg-config
CPPFLAGS += $(shell $(PKG_CONFIG) --cflags hyprland)
CXXFLAGS ?= -O2
CXXFLAGS += -std=c++23 -fPIC -fno-gnu-unique -Wall -Wextra
LDLIBS += $(shell $(PKG_CONFIG) --libs hyprland glesv2)
SOURCES = plugin.cpp rain_controller.cpp geometry.cpp segment_pass.cpp ../physics/simulation.cpp ../snow/simulation.cpp
.PHONY: all clean check-symbols
all: check-symbols
a-weather-app-frame-alignment.so: $(SOURCES) $(wildcard *.hpp) ../physics/simulation.hpp ../snow/simulation.hpp Makefile
	$(CXX) $(CPPFLAGS) $(CXXFLAGS) -shared $(LDFLAGS) -o $@ $(SOURCES) $(LDLIBS)
check-symbols: a-weather-app-frame-alignment.so
	python3 ../../scripts/check_plugin_symbols.py $<
clean:
	$(RM) a-weather-app-frame-alignment.so
