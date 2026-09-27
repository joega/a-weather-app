#pragma once
#include "../physics/simulation.hpp"
#include <algorithm>
#include <cmath>

namespace AWeatherApp::GeometryMath {
namespace physics = ::a_weather_app::physics;
struct Mask { physics::Rect rect; int covers_before; };
inline bool validRect(physics::Rect rect) {
    return std::isfinite(rect.x) && std::isfinite(rect.y) && std::isfinite(rect.w) && std::isfinite(rect.h) &&
           std::abs(rect.x) <= 32768 && std::abs(rect.y) <= 32768 &&
           rect.w > 0 && rect.h > 0 && rect.w <= 16384 && rect.h <= 16384;
}
// Mask covers supports whose stack < covers_before. Tiled fadeouts are above
// all tiled bodies, below floats; floating fadeouts are above all ordinary bodies.
inline bool exposedTops(std::span<physics::Support> supports, std::span<const Mask> masks = {}) {
    if (supports.size() + masks.size() > physics::support_cap)
        return false;
    for (const auto& support : supports)
        if (!validRect(support.rect)) return false;
    for (const auto& mask : masks)
        if (!validRect(mask.rect)) return false;
    for (std::size_t i = 0; i < supports.size(); ++i) {
        auto& support = supports[i];
        const auto rect = support.rect;
        support.span_count = 0;
        if (rect.w <= 16.0f) continue;
        support.spans[0] = {rect.x + 8.0f, rect.x + rect.w - 8.0f};
        support.span_count = 1;
        const auto subtract = [&](physics::Rect cover) {
            if (cover.y > rect.y || cover.y + cover.h <= rect.y) return;
            std::array<physics::Span, physics::span_cap> remaining{};
            std::size_t count = 0;
            for (std::size_t k = 0; k < support.span_count; ++k) {
                const auto span = support.spans[k];
                if (cover.x + cover.w <= span.left || cover.x >= span.right) {
                    remaining[count++] = span;
                    continue;
                }
                if (cover.x > span.left) remaining[count++] = {span.left, std::min(span.right, cover.x)};
                if (cover.x + cover.w < span.right) remaining[count++] = {std::max(span.left, cover.x + cover.w), span.right};
            }
            support.spans = remaining;
            support.span_count = count;
        };
        // Exact Godot front/tie test, including support input-order ties.
        for (std::size_t j = 0; j < supports.size(); ++j)
            if (supports[j].stack > support.stack || (supports[j].stack == support.stack && j > i))
                subtract(supports[j].rect);
        for (const auto& mask : masks)
            if (support.stack < mask.covers_before) subtract(mask.rect);
    }
    return true;
}
}
