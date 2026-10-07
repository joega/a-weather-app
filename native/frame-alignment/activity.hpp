#pragma once
#include "../physics/simulation.hpp"
#include "../snow/simulation.hpp"
#include <algorithm>

namespace AWeatherApp {
struct PrecipitationActivity {
    bool rain = false, snow = false;
    bool needed() const {
        return rain || snow;
    }
};

inline PrecipitationActivity
precipitationActivity(const a_weather_app::physics::Parameters& current,
                      const a_weather_app::physics::Parameters& target,
                      const a_weather_app::snow::Parameters& snowCurrent,
                      const a_weather_app::snow::Parameters& snowTarget, bool rainResidual,
                      bool snowResidual) {
    // Use the core's integer population cutoffs. The envelope also covers
    // emissions during interpolation when intensity rises as density falls.
    const float intensity = std::max(current.rain_intensity, target.rain_intensity);
    const auto count = [intensity](int population, float density) {
        return population * std::clamp(intensity * density, 0.f, 1.f);
    };
    const bool emission = count(2400, std::max(current.far_density, target.far_density)) >= 1 ||
                          count(1500, std::max(current.mid_density, target.mid_density)) >= 1 ||
                          count(420, std::max(current.near_density, target.near_density)) >= 1;
    // Keep the established snow controller cutoff; no residual cutoff is safe.
    return {emission || rainResidual,
            std::max(snowCurrent.strength, snowTarget.strength) > .0001f || snowResidual};
}

// Shared by compositor hooks and the display-free regression harness. These
// counters distinguish avoided work from suppression by lock/fullscreen/DPMS.
struct PrecipitationActivityPolicy {
    std::uint64_t idleScheduleTicks = 0, idlePrepareFrames = 0, idleStageFrames = 0;
    std::uint64_t rainSteps = 0, snowSteps = 0, passSubmissions = 0, emptyPassesSkipped = 0;
    bool schedule(PrecipitationActivity activity) {
        if (activity.needed())
            return true;
        ++idleScheduleTicks;
        return false;
    }
    bool prepare(PrecipitationActivity activity) {
        if (activity.needed())
            return true;
        ++idlePrepareFrames;
        return false;
    }
    bool stage(PrecipitationActivity activity) {
        if (activity.needed())
            return true;
        ++idleStageFrames;
        return false;
    }
    bool rainPass(const a_weather_app::physics::Frame& frame) {
        return pass(frame.segment_count != 0 || frame.far.count != 0);
    }
    bool snowPass(const a_weather_app::snow::Frame& frame) {
        return pass(frame.segment_count != 0);
    }

  private:
    bool pass(bool visible) {
        if (visible)
            ++passSubmissions;
        else
            ++emptyPassesSkipped;
        return visible;
    }
};
} // namespace AWeatherApp
