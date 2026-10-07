#pragma once
#include "../physics/simulation.hpp"
#include <hyprland/src/desktop/DesktopTypes.hpp>

namespace AWeatherApp {
namespace physics = ::a_weather_app::physics;
struct GeometrySnapshot {
    std::array<physics::Support, physics::support_cap> supports{};
    std::size_t count{};
    // Bounded compositor registry inventory distinguishes mapped clients that
    // became invisible from genuinely unmapped/closed supports.
    std::array<std::uint64_t, 1024> mapped_ids{};
    std::size_t mapped_count{};
    // Closing snapshots are masks only: omit their former supports so physics
    // detaches their reservoir once, without resetting surviving reservoirs.
    std::array<physics::Rect, physics::support_cap> extra_masks{};
    std::size_t extra_mask_count{};
    std::uint64_t context{};
    bool valid{};
    const char* reason = "not_captured";
};
// Call only at RENDER_POST_WINDOWS on the compositor thread. No pointers to
// compositor windows survive this call. Coordinates are monitor-local LOGICAL
// units, including spans; multiply by monitor scale only at presentation.
// Invalid means reset simulation context and draw nothing, not window closure.
GeometrySnapshot captureGeometry(PHLMONITOR monitor);
} // namespace AWeatherApp
