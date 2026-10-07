#pragma once

#include "../physics/simulation.hpp"
#include "../snow/simulation.hpp"
#include <algorithm>
#include <array>
#include <charconv>
#include <chrono>
#include <cmath>
#include <string_view>

namespace AWeatherApp {
namespace Physics = a_weather_app::physics;
namespace Snow = a_weather_app::snow;
struct SnowSetting {
    std::string_view name;
    float Snow::Parameters::* member;
    float low, high;
};
enum class SnowSettingID : std::size_t { INTENSITY, ACCUMULATION, OVERLOAD };
inline constexpr std::array<SnowSetting, 3> snowSettings{{
    {"intensity", &Snow::Parameters::strength, 0, 1},
    {"accumulation", &Snow::Parameters::accumulation, 0, 5},
    {"overload", &Snow::Parameters::overload, 1, 100},
}};

struct RainSetting {
    std::string_view name;
    float Physics::Parameters::* member;
    float low, high;
};
inline constexpr std::array<RainSetting, 18> rainSettings{{
    {"rain_intensity", &Physics::Parameters::rain_intensity, 0, 1},
    {"wind_x", &Physics::Parameters::wind_x, -500, 500},
    {"wind_y", &Physics::Parameters::wind_y, -200, 200},
    {"gravity", &Physics::Parameters::gravity, 0, 400},
    {"far_density", &Physics::Parameters::far_density, 0, 2},
    {"mid_density", &Physics::Parameters::mid_density, 0, 2},
    {"near_density", &Physics::Parameters::near_density, 0, 2},
    {"drop_length", &Physics::Parameters::drop_length, .25f, 3},
    {"drop_width", &Physics::Parameters::drop_width, .25f, 2},
    {"drop_opacity", &Physics::Parameters::drop_opacity, 0, 1},
    {"drop_speed", &Physics::Parameters::drop_speed, .2f, 2.5f},
    {"splash_amount", &Physics::Parameters::splash_amount, 0, 8},
    {"splash_lifetime", &Physics::Parameters::splash_lifetime, .05f, 1},
    {"splash_velocity", &Physics::Parameters::splash_velocity, 10, 300},
    {"water_accumulation_rate", &Physics::Parameters::water_accumulation_rate, 0, 5},
    {"runoff_threshold", &Physics::Parameters::runoff_threshold, .1f, 10},
    {"evaporation_rate", &Physics::Parameters::evaporation_rate, 0, 2},
    {"simulation_speed", &Physics::Parameters::simulation_speed, 0, 2},
}};

struct RainCommand {
    enum Action {
        INVALID,
        ON,
        OFF,
        STATUS,
        SET,
        RESETMETRICS,
        FPS,
        SNOW,
        REDUCED,
        PHYSICS,
        ACCUMULATION,
        RENEW,
        TEMPERATURE
    } action = INVALID;
    std::uint64_t generation = 0;
    std::int64_t temperatureValidUntilMS = 0;
    bool guarded = false, flag = false;
    int seconds = 0, monitor = 0, fps = 0;
    std::size_t setting = 0;
    SnowSettingID snowSetting = SnowSettingID::INTENSITY;
    float value = 0;
    const char* error = "invalid_command";
};

// Fixed token budget, no allocations, no partial state changes on bad requests.
inline RainCommand parseRainCommand(std::string_view input) {
    RainCommand result;
    if (input.size() > 1024) {
        result.error = "request_limit";
        return result;
    }
    std::array<std::string_view, 7> tokens{};
    std::size_t count = 0;
    while (!input.empty()) {
        const auto first = input.find_first_not_of(" \t\r\n");
        if (first == std::string_view::npos)
            break;
        input.remove_prefix(first);
        if (count == tokens.size()) {
            result.error = "argument_limit";
            return result;
        }
        const auto end = input.find_first_of(" \t\r\n");
        tokens[count++] = input.substr(0, end);
        if (end == std::string_view::npos)
            break;
        input.remove_prefix(end);
    }
    if (count < 2 || tokens[0] != "a-weather-app:rain")
        return result;
    if (tokens[1] == "guard") {
        if (count < 4)
            return result;
        const auto parsed = std::from_chars(tokens[2].data(), tokens[2].data() + tokens[2].size(),
                                            result.generation);
        if (parsed.ec != std::errc{} || parsed.ptr != tokens[2].data() + tokens[2].size() ||
            !result.generation)
            return result;
        result.guarded = true;
        for (std::size_t i = 1; i + 2 < count; ++i)
            tokens[i] = tokens[i + 2];
        count -= 2;
        if (tokens[1] == "on")
            return result;
    }
    if (count == 3 && (tokens[1] == "reduced_motion" || tokens[1] == "window_physics" ||
                       tokens[1] == "accumulation")) {
        if (tokens[2] != "true" && tokens[2] != "false")
            return result;
        result.flag = tokens[2] == "true";
        result.action = tokens[1] == "reduced_motion"   ? RainCommand::REDUCED
                        : tokens[1] == "window_physics" ? RainCommand::PHYSICS
                                                        : RainCommand::ACCUMULATION;
        result.error = nullptr;
        return result;
    }
    if (count == 2 &&
        (tokens[1] == "off" || tokens[1] == "status" || tokens[1] == "resetmetrics")) {
        result.action = tokens[1] == "off"      ? RainCommand::OFF
                        : tokens[1] == "status" ? RainCommand::STATUS
                                                : RainCommand::RESETMETRICS;
    } else if (count == 3 && tokens[1] == "fps") {
        const auto fps =
            std::from_chars(tokens[2].data(), tokens[2].data() + tokens[2].size(), result.fps);
        if (fps.ec != std::errc{} || fps.ptr != tokens[2].data() + tokens[2].size() ||
            (result.fps != 15 && result.fps != 30 && result.fps != 60)) {
            result.error = "invalid_fps";
            return result;
        }
        result.action = RainCommand::FPS;
    } else if (count == 3 && tokens[1] == "renew" && result.guarded) {
        const auto seconds =
            std::from_chars(tokens[2].data(), tokens[2].data() + tokens[2].size(), result.seconds);
        if (seconds.ec != std::errc{} || seconds.ptr != tokens[2].data() + tokens[2].size() ||
            result.seconds < 1 || result.seconds > 300) {
            result.error = "invalid_duration";
            return result;
        }
        result.action = RainCommand::RENEW;
    } else if (count == 4 && tokens[1] == "on") {
        const auto seconds =
            std::from_chars(tokens[2].data(), tokens[2].data() + tokens[2].size(), result.seconds);
        const auto monitor =
            std::from_chars(tokens[3].data(), tokens[3].data() + tokens[3].size(), result.monitor);
        if (seconds.ec != std::errc{} || seconds.ptr != tokens[2].data() + tokens[2].size() ||
            monitor.ec != std::errc{} || monitor.ptr != tokens[3].data() + tokens[3].size() ||
            result.seconds < 1 || result.seconds > 300 || result.monitor < 0) {
            result.error = "invalid_duration_or_monitor";
            return result;
        }
        result.action = RainCommand::ON;
    } else if ((count == 4 || count == 5) && tokens[1] == "snow" && tokens[2] == "temperature" &&
               result.guarded) {
        result.flag = tokens[3] != "unknown";
        if (count != (result.flag ? 5u : 4u))
            return result;
        if (result.flag) {
            double temperature = 0;
            const auto value =
                std::from_chars(tokens[3].data(), tokens[3].data() + tokens[3].size(), temperature);
            if (value.ec != std::errc{} || value.ptr != tokens[3].data() + tokens[3].size() ||
                !std::isfinite(temperature) || temperature < -100 || temperature > 65) {
                result.error = "invalid_temperature";
                return result;
            }
            result.value = static_cast<float>(temperature);
            const auto expiry =
                std::from_chars(tokens[4].data(), tokens[4].data() + tokens[4].size(),
                                result.temperatureValidUntilMS);
            if (expiry.ec != std::errc{} || expiry.ptr != tokens[4].data() + tokens[4].size() ||
                result.temperatureValidUntilMS <= 0 ||
                result.temperatureValidUntilMS > 253402300799999LL) {
                result.error = "invalid_temperature_expiry";
                return result;
            }
        }
        result.action = RainCommand::TEMPERATURE;
    } else if (count == 4 && tokens[1] == "snow") {
        const auto value =
            std::from_chars(tokens[3].data(), tokens[3].data() + tokens[3].size(), result.value);
        if (value.ec != std::errc{} || value.ptr != tokens[3].data() + tokens[3].size() ||
            !std::isfinite(result.value)) {
            result.error = "invalid_value";
            return result;
        }
        for (std::size_t i = 0; i < snowSettings.size(); ++i) {
            const auto& setting = snowSettings[i];
            if (setting.name != tokens[2])
                continue;
            if (result.value < setting.low || result.value > setting.high) {
                result.error = "value_out_of_range";
                return result;
            }
            result.snowSetting = static_cast<SnowSettingID>(i);
            result.action = RainCommand::SNOW;
            result.error = nullptr;
            return result;
        }
        result.error = "unknown_snow_setting";
        return result;
    } else if (count == 4 && tokens[1] == "set") {
        const auto value =
            std::from_chars(tokens[3].data(), tokens[3].data() + tokens[3].size(), result.value);
        if (value.ec != std::errc{} || value.ptr != tokens[3].data() + tokens[3].size() ||
            !std::isfinite(result.value)) {
            result.error = "invalid_value";
            return result;
        }
        for (std::size_t i = 0; i < rainSettings.size(); ++i) {
            const auto& setting = rainSettings[i];
            if (setting.name != tokens[2])
                continue;
            if (result.value < setting.low || result.value > setting.high) {
                result.error = "value_out_of_range";
                return result;
            }
            result.setting = i;
            result.action = RainCommand::SET;
            result.error = nullptr;
            return result;
        }
        result.error = "unknown_setting";
        return result;
    }
    if (result.action != RainCommand::INVALID)
        result.error = nullptr;
    return result;
}

// Native expiry does not depend on the worker sending another command. Wall
// expiry prevents queued/delayed IPC from extending observation freshness;
// the steady deadline caps validity even if the system clock moves backwards.
struct SnowTemperatureLease {
    using Clock = std::chrono::steady_clock;
    static constexpr std::int64_t maximumMS = 8000;
    Clock::time_point deadline{};
    std::int64_t validUntilMS = 0;
    void clear(Snow::Parameters& current, Snow::Parameters& target) {
        current.temperature_known = target.temperature_known = false;
        current.temperature_c = target.temperature_c = 0;
        validUntilMS = 0;
    }
    void accept(const RainCommand& command, Snow::Parameters& current, Snow::Parameters& target,
                std::int64_t wallNowMS, Clock::time_point now) {
        clear(current, target);
        if (!command.flag || command.temperatureValidUntilMS <= wallNowMS || wallNowMS < 0)
            return;
        validUntilMS = command.temperatureValidUntilMS;
        deadline = now + std::chrono::milliseconds(std::min(maximumMS, validUntilMS - wallNowMS));
        current.temperature_known = target.temperature_known = true;
        current.temperature_c = target.temperature_c = command.value;
    }
    void expire(Snow::Parameters& current, Snow::Parameters& target, std::int64_t wallNowMS,
                Clock::time_point now) {
        if (validUntilMS && (now >= deadline || wallNowMS >= validUntilMS || wallNowMS < 0))
            clear(current, target);
    }
};

inline void approachParameters(Physics::Parameters& current, const Physics::Parameters& target,
                               float delta) {
    const float blend = 1 - std::exp(-std::clamp(delta, 0.f, .1f) * 5.f);
    for (const auto& setting : rainSettings)
        current.*(setting.member) += (target.*(setting.member) - current.*(setting.member)) * blend;
}
inline void approachSnowParameters(Snow::Parameters& current, const Snow::Parameters& target,
                                   float delta) {
    // Freshness/unknown transitions take effect immediately, not after smoothing.
    current.temperature_known = target.temperature_known;
    current.temperature_c = target.temperature_c;
    const float blend = 1 - std::exp(-std::clamp(delta, 0.f, .1f) * 5.f);
    for (const auto& setting : snowSettings)
        current.*(setting.member) += (target.*(setting.member) - current.*(setting.member)) * blend;
}
} // namespace AWeatherApp
