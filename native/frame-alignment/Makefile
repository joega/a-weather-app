CXX ?= c++
.DEFAULT_GOAL := all
PKG_CONFIG ?= pkg-config
CPPFLAGS += $(shell $(PKG_CONFIG) --cflags hyprland)
CXXFLAGS ?= -O2
CXXFLAGS += -std=c++23 -fPIC -fno-gnu-unique -Wall -Wextra -fstack-protector-strong -D_FORTIFY_SOURCE=3
LDFLAGS += -Wl,-z,relro,-z,now,-z,noexecstack
LDLIBS += $(shell $(PKG_CONFIG) --libs hyprland glesv2)
SOURCES = plugin.cpp rain_controller.cpp geometry.cpp segment_pass.cpp ../physics/simulation.cpp ../snow/simulation.cpp
NATIVE_CHECK ?= ../../build/weather-native-check
ifeq ($(NATIVE_CHECK),../../build/weather-native-check)
$(NATIVE_CHECK): $(wildcard ../../internal/nativebuild/*.go ../../cmd/weather-native-check/*.go ../../internal/safeio/*.go ../../internal/elfsafe/*.go) ../../go.mod
	$(MAKE) -C ../.. native-tools
endif
.PHONY: all clean check-symbols
all: check-symbols
a-weather-app-frame-alignment.so: $(SOURCES) $(wildcard *.hpp) ../physics/simulation.hpp ../snow/simulation.hpp Makefile
	$(CXX) $(CPPFLAGS) $(CXXFLAGS) -shared $(LDFLAGS) -o $@ $(SOURCES) $(LDLIBS)
check-symbols: a-weather-app-frame-alignment.so $(NATIVE_CHECK)
	$(NATIVE_CHECK) symbols $<
clean:
	$(RM) a-weather-app-frame-alignment.so
