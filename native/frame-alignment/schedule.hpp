#pragma once
#include <chrono>

namespace AWeatherApp {
// Monotonic anchored cadence: skip overdue slots, never issue catch-up bursts.
struct RainSchedule {
    using Clock = std::chrono::steady_clock;
    Clock::time_point next;
    Clock::duration period;
    void reset(Clock::time_point now, int fps) {
        period = std::chrono::duration_cast<Clock::duration>(std::chrono::duration<double>(1.0 / fps));
        next = now + period;
    }
    void advance(Clock::time_point now) {
        if (next <= now) next += period * ((now - next) / period + 1);
    }
};
}
