// Offline adversarial checks: never load code into the running compositor.
#include "../native/physics/simulation.hpp"
#include "../native/snow/simulation.hpp"
#include "../native/frame-alignment/activity.hpp"
#include "../native/frame-alignment/rain_control.hpp"
#include "../native/frame-alignment/schedule.hpp"
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

// Drive the production policy with actual core state and count the work it
// permits. This needs no monitor, compositor, IPC, GL context or plugin load.
struct PrecipitationWorkload {
    std::unique_ptr<rain::Simulation> rainSimulation = std::make_unique<rain::Simulation>();
    std::unique_ptr<snow::Simulation> snowSimulation = std::make_unique<snow::Simulation>();
    std::unique_ptr<rain::Frame> rainFrame = std::make_unique<rain::Frame>();
    std::unique_ptr<snow::Frame> snowFrame = std::make_unique<snow::Frame>();
    rain::Parameters current, target;
    snow::Parameters snowCurrent, snowTarget;
    rain::InteractionOptions interactions;
    AWeatherApp::PrecipitationActivityPolicy policy;
    std::uint64_t damage = 0, preparationDamage = 0, geometry = 0;
    PrecipitationWorkload() { current.rain_intensity = target.rain_intensity = 0; }
    AWeatherApp::PrecipitationActivity activity() const {
        return AWeatherApp::precipitationActivity(current, target, snowCurrent, snowTarget,
            rainSimulation->hasResidualActivity(), snowSimulation->hasResidualActivity());
    }
    void tick(std::span<const rain::Support> supports = {}) {
        const auto state = activity();
        if (policy.schedule(state)) ++damage;
        if (policy.prepare(state)) ++preparationDamage;
        if (!policy.stage(state)) return;
        ++geometry;
        AWeatherApp::approachParameters(current, target, .05f);
        AWeatherApp::approachSnowParameters(snowCurrent, snowTarget, .05f);
        if (activity().rain) {
            ++policy.rainSteps;
            assert(rainSimulation->step(.05f, 128, 96, current, supports, *rainFrame, interactions));
        } else {
            rainFrame->segment_count = 0;
            rainFrame->far = {};
        }
        if (snowCurrent.strength > .0001f || snowSimulation->hasResidualActivity()) {
            ++policy.snowSteps;
            assert(snowSimulation->step(.05f, 128, 96, snowCurrent, supports, *snowFrame, interactions));
        } else snowFrame->segment_count = 0;
        policy.rainPass(*rainFrame);
        policy.snowPass(*snowFrame);
    }
};

rain::Support precipitationSupport() {
    rain::Support s;
    s.id = 42;
    s.rect = {0, 40, 128, 30};
    s.spans[0] = {0, 128};
    s.span_count = 1;
    return s;
}

void precipitationActivityRegression() {
    PrecipitationWorkload work;
    for (int i = 0; i < 120; ++i) work.tick();
    assert(work.damage == 0 && work.preparationDamage == 0 && work.geometry == 0);
    assert(work.policy.rainSteps == 0 && work.policy.snowSteps == 0 && work.policy.passSubmissions == 0);
    assert(work.policy.idleScheduleTicks == 120 && work.policy.idlePrepareFrames == 120 && work.policy.idleStageFrames == 120);
    const auto support = precipitationSupport();
    const auto supports = std::span(&support, 1);
    work.interactions.accumulation = false;
    work.target.rain_intensity = .8f;
    assert(work.activity().rain); // Requested precipitation wakes before interpolation.
    for (int i = 0; i < 100; ++i) work.tick(supports);
    assert(work.rainSimulation->metrics().impacts > 0 && work.policy.rainSteps > 0);
    assert(work.policy.passSubmissions > 0 && work.policy.snowSteps == 0);
    assert(work.rainSimulation->metrics().splash_active > 0);
    work.current.rain_intensity = work.target.rain_intensity = 0;
    assert(work.activity().rain); // Residual splashes continue after streaks stop.
    for (int i = 0; i < 100 && work.activity().needed(); ++i) work.tick(supports);
    assert(!work.activity().needed());
    const auto damage = work.damage, geometry = work.geometry, passes = work.policy.passSubmissions;
    for (int i = 0; i < 120; ++i) work.tick(supports);
    assert(work.damage == damage && work.geometry == geometry && work.policy.passSubmissions == passes);
    work.snowTarget.strength = 1;
    assert(work.activity().snow && !work.activity().rain);
    for (int i = 0; i < 100; ++i) work.tick(supports);
    assert(work.policy.snowSteps > 0 && work.snowSimulation->metrics().active > 0);
    work.snowCurrent.strength = work.snowTarget.strength = 0;
    assert(work.activity().snow); // Atmospheric flakes finish their existing paths.
    for (int i = 0; i < 1000 && work.activity().needed(); ++i) work.tick(supports);
    assert(!work.activity().needed());

    // Quantized rain populations can be empty at positive intensity. The
    // interpolation envelope must also retain crossing intensity/density edits.
    rain::Parameters a, b;
    snow::Parameters noSnow;
    a.rain_intensity = b.rain_intensity = .00001f;
    assert(!AWeatherApp::precipitationActivity(a, b, noSnow, noSnow, false, false).needed());
    a.rain_intensity = 0;
    b.rain_intensity = 1;
    b.far_density = b.mid_density = b.near_density = 0;
    assert(AWeatherApp::precipitationActivity(a, b, noSnow, noSnow, false, false).rain);
    a = b;
    assert(!AWeatherApp::precipitationActivity(a, b, noSnow, noSnow, false, false).needed());
    assert(work.policy.emptyPassesSkipped > 0);
    rain::Frame farOnly;
    farOnly.far.count = 1;
    assert(work.policy.rainPass(farOnly)); // Procedural far rain needs its pass.
    farOnly.far.count = 0;
    assert(!work.policy.rainPass(farOnly));
}

void precipitationResidualRegression() {
    PrecipitationWorkload work;
    const auto support = precipitationSupport();
    const auto supports = std::span(&support, 1);
    assert(work.rainSimulation->step(0, 128, 96, work.current, supports, *work.rainFrame));
    assert(work.rainSimulation->deposit(42, 64, .0001f));
    assert(work.rainSimulation->depositFloor(12, .0001f));
    assert(work.rainSimulation->hasResidualActivity()); // Below any visible cutoff.
    assert(work.rainSimulation->injectRunoff({64, 0}, {0, 80}, .5f));
    work.current.evaporation_rate = work.target.evaporation_rate = 2;
    for (int i = 0; i < 2000 && work.activity().needed(); ++i) work.tick(supports);
    const auto rainMetrics = work.rainSimulation->metrics();
    assert(rainMetrics.lower_impacts > 0 && rainMetrics.evaporated > 0 && rainMetrics.drained > 0);
    assert(!work.rainSimulation->hasResidualActivity() && !work.activity().needed());
    assert(std::abs(rainMetrics.deposited - rainMetrics.evaporated - rainMetrics.drained - rainMetrics.escaped - rainMetrics.discarded) < 1e-8);

    assert(work.snowSimulation->step(0, 128, 96, work.snowCurrent, supports, *work.snowFrame));
    assert(work.snowSimulation->deposit(42, 64, .0001f));
    assert(work.snowSimulation->depositFloor(12, .0001f));
    for (int i = 0; i < 30; ++i) work.tick(supports);
    assert(work.activity().snow && work.snowSimulation->metrics().settled > 0);
    assert(work.snowSimulation->metrics().melted == 0); // Unknown temperature is conservative.
    assert(work.snowSimulation->deposit(42, 64, 3));
    work.tick(); // A closed support releases accounted snow rather than losing it.
    assert(work.snowSimulation->metrics().falling > 0 && work.activity().snow);
    work.snowCurrent.temperature_known = work.snowTarget.temperature_known = true;
    work.snowCurrent.temperature_c = work.snowTarget.temperature_c = 10;
    for (int i = 0; i < 2000 && work.activity().needed(); ++i) work.tick();
    const auto snowMetrics = work.snowSimulation->metrics();
    assert(snowMetrics.melted > 0 && snowMetrics.active == 0 && snowMetrics.settled == 0);
    assert(!work.activity().needed());
    assert(std::abs(snowMetrics.deposited - snowMetrics.melted - snowMetrics.escaped - snowMetrics.discarded - snowMetrics.overflow) < 1e-8);

    // Frozen simulation and explicit discard/reset cannot silently retire or
    // resurrect live reservoirs in the controller's residual-state cache.
    assert(work.rainSimulation->depositFloor(12, 1));
    work.current.simulation_speed = work.target.simulation_speed = 0;
    for (int i = 0; i < 10; ++i) work.tick();
    assert(work.rainSimulation->metrics().floor_volume == 1 && work.activity().rain);
    work.rainSimulation->discardAccumulation();
    assert(!work.rainSimulation->hasResidualActivity());
    assert(work.snowSimulation->inject({20, 1}, {0, 20}));
    assert(work.snowSimulation->hasResidualActivity());
    work.snowSimulation->reset();
    assert(!work.activity().needed());
}

void precipitationMaintenanceRegression() {
    using Clock = AWeatherApp::RainSchedule::Clock;
    const auto now = Clock::time_point{} + std::chrono::seconds(100);
    AWeatherApp::RainSchedule schedule;
    schedule.reset(now, 30);
    assert(schedule.wakeDeadline(now, now + std::chrono::seconds(5), true) == now + std::chrono::milliseconds(250));
    assert(schedule.wakeDeadline(now, now + std::chrono::milliseconds(20), true) == now + std::chrono::milliseconds(20));
    assert(schedule.wakeDeadline(now, now + std::chrono::seconds(5), false) == schedule.next);
    const auto resumed = now + std::chrono::seconds(2);
    assert(schedule.wakeDeadline(resumed, now + std::chrono::seconds(5), false) > resumed);
    assert(schedule.next <= resumed + schedule.period); // No catch-up burst on reactivation.
    AWeatherApp::SnowTemperatureLease lease;
    snow::Parameters current, target;
    auto command = AWeatherApp::parseRainCommand("a-weather-app:rain guard 1 snow temperature 10 2000");
    lease.accept(command, current, target, 1000, now);
    assert(current.temperature_known && target.temperature_known);
    lease.expire(current, target, 1500, now + std::chrono::milliseconds(1000));
    assert(!current.temperature_known && !target.temperature_known); // Steady expiry despite backwards wall time.
    lease.accept(command, current, target, 1000, now);
    lease.expire(current, target, 2000, now);
    assert(!current.temperature_known && !target.temperature_known); // Wall expiry without a render frame.
}

int main() {
    exercise<rain::Simulation, rain::Parameters, rain::Frame>({});
    snow::Parameters flakes;
    flakes.strength = 1;
    exercise<snow::Simulation, snow::Parameters, snow::Frame>(flakes);
    precipitationActivityRegression();
    precipitationResidualRegression();
    precipitationMaintenanceRegression();
    std::cout << "PASS: invalid inputs, bounded finite frames, idle precipitation work, residuals, reactivation and leases\n";
}
