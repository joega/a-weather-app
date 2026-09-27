// Offline adversarial checks: never load code into the running compositor.
#include "../native/physics/simulation.hpp"
#include "../native/snow/simulation.hpp"
#include <cassert>
#include <cmath>
#include <iostream>
#include <limits>
#include <memory>
#include <random>

namespace rain = a_weather_app::physics;
namespace snow = a_weather_app::snow;

template<class Simulation, class Parameters, class Frame>
void exercise(Parameters parameters) {
    auto simulation = std::make_unique<Simulation>();
    auto frame = std::make_unique<Frame>();
    std::array<rain::Support, rain::support_cap + 1> supports{};
    for (std::size_t i = 0; i < supports.size(); ++i) {
        supports[i].id = i + 1;
        supports[i].rect = {float(i * 10), float(100 + i * 5), 200, 100};
        supports[i].spans[0] = {float(i * 10), float(i * 10 + 200)};
        supports[i].span_count = 1;
        supports[i].stack = int(i);
    }
    const auto good = supports[0];
    const auto one = std::span<const rain::Support>(supports).first(1);
    auto reject = [&](float dt, float width, float height, auto topology) {
        assert(!simulation->step(dt, width, height, parameters, topology, *frame));
        assert(!frame->valid && frame->segment_count == 0);
    };
    for (float invalid : {std::numeric_limits<float>::quiet_NaN(),
                          std::numeric_limits<float>::infinity(), -1.f}) {
        reject(invalid, 1920, 1080, one);
        reject(.033f, invalid, 1080, one);
        reject(.033f, 1920, invalid, one);
    }
    reject(.033f, 1920, 1080, std::span<const rain::Support>(supports));
    supports[0].span_count = rain::span_cap + 1;
    reject(.033f, 1920, 1080, one);
    supports[0] = good;
    supports[1] = good;
    reject(.033f, 1920, 1080, std::span<const rain::Support>(supports).first(2));
    supports[1].id = 2;
    supports[0].spans[0].right = std::numeric_limits<float>::infinity();
    reject(.033f, 1920, 1080, one);
    supports[0] = good;
    std::mt19937 random(927);
    for (int tick = 0; tick < 3000; ++tick) {
        const auto count = std::size_t(random() % (rain::support_cap + 1));
        // Exercise changing topology and dimensions, empty input, and max capacity.
        const float width = 640 + float(random() % 3200);
        assert(simulation->step(.033f, width, 1080, parameters,
            std::span<const rain::Support>(supports).first(count), *frame));
        assert(frame->valid && frame->segment_count <= frame->segments.size());
        for (std::size_t i = 0; i < frame->segment_count; ++i)
            for (auto value : frame->segments[i].values) assert(std::isfinite(value));
        if (tick % 100 == 0) simulation->reset();
    }
}

int main() {
    exercise<rain::Simulation, rain::Parameters, rain::Frame>({});
    snow::Parameters flakes;
    flakes.strength = 1;
    exercise<snow::Simulation, snow::Parameters, snow::Frame>(flakes);
    std::cout << "PASS: invalid inputs, topology changes and bounded finite frames\n";
}
