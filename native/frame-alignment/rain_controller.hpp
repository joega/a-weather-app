#pragma once
#include <hyprland/src/desktop/DesktopTypes.hpp>
#include <string>

namespace AWeatherApp {
void rainPrecheck(PHLMONITOR monitor);
void rainPrepareFrame(PHLMONITOR monitor);
void rainStage();
std::string rainRequest(std::string input);
void rainShutdown();
} // namespace AWeatherApp
