#include "rain_controller.hpp"
#include "rain_control.hpp"
#include "geometry.hpp"
#include "segment_pass.hpp"
#include "metrics.hpp"
#include "schedule.hpp"
#include "activity.hpp"
#include <hyprland/src/render/Renderer.hpp>
#include <hyprland/src/state/MonitorState.hpp>
#include <hyprland/src/managers/eventLoop/EventLoopManager.hpp>
#include <hyprland/src/managers/fullscreen/FullscreenController.hpp>
#include <hyprland/src/protocols/SessionLock.hpp>
#include <chrono>
#include <memory>
#include <sstream>
#include <stdexcept>

namespace AWeatherApp {
namespace {
using Clock = std::chrono::steady_clock;
struct Session {
    Physics::Simulation simulation;
    Physics::Frame frame;
    Physics::Parameters current, target;
    Snow::Simulation snowSimulation;
    Snow::Frame snowFrame;
    Snow::Parameters snowCurrent, snowTarget;
    SnowTemperatureLease temperatureLease;
    std::shared_ptr<SegmentGPU> gpu = std::make_shared<SegmentGPU>();
    PHLMONITORREF monitor;
    Clock::time_point deadline, last;
    std::uint64_t context = 0, frames = 0;
    bool hasContext = false;
    bool reducedMotion = false;
    Physics::InteractionOptions interactions;
    std::uint64_t generation = 0;
    const char* suppression = "starting";
    double totalCPU = 0, maxCPU = 0;
    Clock::time_point metricsStarted = Clock::now();
    Metrics::SampleRing cpuSubmission;
    Metrics::Intervals acceptedIntervals, presentedIntervals;
    std::uint64_t geometryCaptures = 0, geometryAccepted = 0, geometryRejected = 0;
    std::uint64_t impactsMeasured = 0;
    CHyprSignalListener presentedListener;
    SP<CEventLoopTimer> timer;
    RainSchedule schedule;
    int fps = 30;
    std::uint64_t timerCallbacks = 0, scheduledDamage = 0, suppressedTicks = 0;
    PrecipitationActivityPolicy activityPolicy;
    bool rainResidual = false, snowResidual = false;
    PrecipitationActivity activity() const {
        return precipitationActivity(current, target, snowCurrent, snowTarget, rainResidual,
                                     snowResidual);
    }
    void refreshResiduals() {
        rainResidual = simulation.hasResidualActivity();
        snowResidual = snowSimulation.hasResidualActivity();
    }
    void settleEmptyParameters(Clock::time_point now) {
        const float delta = std::chrono::duration<float>(now - last).count();
        approachParameters(current, target, delta);
        approachSnowParameters(snowCurrent, snowTarget, delta);
        snowCurrent.wind = current.wind_x;
        last = now;
    }
    void resetMetrics() {
        metricsStarted = Clock::now();
        cpuSubmission.reset();
        acceptedIntervals.reset();
        presentedIntervals.reset();
        geometryCaptures = geometryAccepted = geometryRejected = 0;
        impactsMeasured = 0;
        timerCallbacks = scheduledDamage = suppressedTicks = 0;
        activityPolicy = {};
    }
};
std::unique_ptr<Session> session;
Physics::Parameters settings;
Snow::Parameters snowSettingsConfigured = [] {
    Snow::Parameters p;
    p.strength = 0;
    return p;
}();
int configuredFPS = 30;
std::string lastStatus = "{\"schema_version\":1,\"enabled\":false}";
bool configuredReduced = false;
Physics::InteractionOptions configuredInteractions;
std::uint64_t generationCounter = 0;

double timestampMS(Clock::time_point time) {
    return std::chrono::duration<double, std::milli>(time.time_since_epoch()).count();
}
std::int64_t wallTimestampMS() {
    return std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::system_clock::now().time_since_epoch())
        .count();
}
void writeSummary(std::ostream& out, const Metrics::Summary& m) {
    out << "\"count\":" << m.count << ",\"total_count\":" << m.total_count
        << ",\"rejected\":" << m.rejected << ",\"coverage_ms\":" << m.coverage_ms
        << ",\"mean\":" << m.mean << ",\"p50\":" << m.p50 << ",\"p95\":" << m.p95
        << ",\"p99\":" << m.p99 << ",\"max\":" << m.max
        << ",\"max_since_reset\":" << m.max_since_reset;
}
void writeIntervals(std::ostream& out, const Metrics::Intervals& intervals) {
    writeSummary(out, intervals.summary());
    out << ",\"events\":" << intervals.events() << ",\"gaps\":" << intervals.gaps()
        << ",\"sequence_breaks\":" << intervals.breaks()
        << ",\"longest_gap_ms\":" << intervals.longestGap();
}
void writeSnowParameters(std::ostream& out, const Snow::Parameters& parameters) {
    bool first = true;
    for (const auto& setting : snowSettings) {
        if (!first)
            out << ',';
        first = false;
        out << '"' << setting.name << "\":" << parameters.*(setting.member);
    }
    out << ",\"temperature_known\":" << (parameters.temperature_known ? "true" : "false")
        << ",\"temperature_c\":" << parameters.temperature_c;
}

std::string status(bool enabled = true) {
    if (!session) {
        std::ostringstream out;
        out << lastStatus.substr(0, lastStatus.size() - 1) << ",\"configured_parameters\":{";
        bool first = true;
        for (const auto& setting : rainSettings) {
            if (!first)
                out << ',';
            first = false;
            out << '\"' << setting.name << "\":" << settings.*(setting.member);
        }
        out << "},\"configured_fps\":" << configuredFPS
            << ",\"configured_reduced_motion\":" << (configuredReduced ? "true" : "false")
            << ",\"configured_window_physics\":"
            << (configuredInteractions.window_physics ? "true" : "false")
            << ",\"configured_accumulation\":"
            << (configuredInteractions.accumulation ? "true" : "false") << ",\"configured_snow\":{";
        writeSnowParameters(out, snowSettingsConfigured);
        out << "}}";
        return out.str();
    }
    const auto& s = *session;
    const auto m = s.simulation.metrics();
    const auto snowMetrics = s.snowSimulation.metrics();
    std::ostringstream out;
    out.precision(12); // Keep independently summed conservation ledgers useful.
    out << "{\"schema_version\":1,\"enabled\":" << (enabled ? "true" : "false")
        << ",\"fps\":" << s.fps << ",\"session_generation\":" << s.generation
        << ",\"remaining_seconds\":"
        << std::max(0.0, std::chrono::duration<double>(s.deadline - Clock::now()).count())
        << ",\"reduced_motion\":" << (s.reducedMotion ? "true" : "false")
        << ",\"window_physics\":" << (s.interactions.window_physics ? "true" : "false")
        << ",\"accumulation\":" << (s.interactions.accumulation ? "true" : "false")
        << ",\"lock_state_known\":" << (PROTO::sessionLock ? "true" : "false")
        << ",\"session_locked\":"
        << (PROTO::sessionLock && PROTO::sessionLock->isLocked() ? "true" : "false");
    if (enabled)
        out << ",\"configured_fps\":" << configuredFPS;
    out << ",\"frames\":" << s.frames << ",\"impacts\":" << m.impacts
        << ",\"runoff_total\":" << m.runoff_total << ",\"water_volume\":" << m.water_volume
        << ",\"detached\":" << m.detached << ",\"discarded\":" << m.discarded
        << ",\"deposited\":" << m.deposited << ",\"drained\":" << m.drained
        << ",\"evaporated\":" << m.evaporated << ",\"runoff_volume\":" << m.runoff_volume
        << ",\"floor_volume\":" << m.floor_volume << ",\"escaped\":" << m.escaped
        << ",\"overflow\":" << m.overflow << ",\"transferred\":" << m.transferred
        << ",\"runoff_dropped\":" << m.runoff_dropped << ",\"lower_impacts\":" << m.lower_impacts
        << ",\"floor_impacts\":" << m.floor_impacts
        << ",\"segments\":" << (s.hasContext ? s.frame.segment_count : 0)
        << ",\"rain_segments\":" << (s.hasContext ? s.frame.rain_segments : 0)
        << ",\"masked_segments\":" << (s.hasContext ? s.frame.masked_segments : 0)
        << ",\"segments_dropped\":" << (s.hasContext ? s.frame.metrics.segments_dropped : 0)
        << ",\"far_count\":" << (s.hasContext ? s.frame.far.count : 0)
        << ",\"splash_active\":" << m.splash_active << ",\"runoff_active\":" << m.runoff_active
        << ",\"supports\":" << (s.hasContext ? s.frame.mask_count : 0)
        << ",\"draw_calls\":" << s.gpu->drawCalls()
        << ",\"cpu_mean_ms\":" << (s.frames ? s.totalCPU / s.frames : 0)
        << ",\"cpu_max_ms\":" << s.maxCPU << ",\"suppression\":\"" << s.suppression
        << "\",\"gpu_suppression\":\"" << s.gpu->lastSuppression() << "\"";
    const double elapsed =
        std::chrono::duration<double, std::milli>(Clock::now() - s.metricsStarted).count();
    out << ",\"performance\":{\"sample_capacity\":" << Metrics::sample_capacity
        << ",\"interval_gap_limit_ms\":" << Metrics::Intervals::gap_limit_ms
        << ",\"elapsed_ms\":" << elapsed << ",\"geometry_captures\":" << s.geometryCaptures
        << ",\"geometry_accepted\":" << s.geometryAccepted
        << ",\"geometry_rejected\":" << s.geometryRejected << ",\"geometry_captures_per_second\":"
        << (elapsed > 0 ? s.geometryCaptures * 1000.0 / elapsed : 0)
        << ",\"geometry_source\":\"compositor_current_frame\",\"impacts\":" << s.impactsMeasured
        << ",\"scheduled_damage_requests\":" << s.scheduledDamage
        << ",\"timer_callbacks\":" << s.timerCallbacks
        << ",\"suppressed_schedule_ticks\":" << s.suppressedTicks
        << ",\"idle_schedule_ticks\":" << s.activityPolicy.idleScheduleTicks
        << ",\"idle_prepare_frames\":" << s.activityPolicy.idlePrepareFrames
        << ",\"idle_stage_frames\":" << s.activityPolicy.idleStageFrames
        << ",\"rain_simulation_steps\":" << s.activityPolicy.rainSteps
        << ",\"snow_simulation_steps\":" << s.activityPolicy.snowSteps
        << ",\"precipitation_pass_submissions\":" << s.activityPolicy.passSubmissions
        << ",\"empty_passes_skipped\":" << s.activityPolicy.emptyPassesSkipped
        << ",\"scheduling_scope\":\"rain_originated_damage_only\""
        << ",\"impacts_per_second\":" << (elapsed > 0 ? s.impactsMeasured * 1000.0 / elapsed : 0)
        << ",\"ipc_per_frame\":0,\"cpu_submission_ms\":{";
    writeSummary(out, s.cpuSubmission.summary());
    out << "},\"accepted_frame_interval_ms\":{";
    writeIntervals(out, s.acceptedIntervals);
    out << "},\"monitor_present_interval_ms\":{";
    writeIntervals(out, s.presentedIntervals);
    out << "}}";
    // Requested versus interpolated values make live control changes auditable
    // without coupling diagnostics to the renderer or spawning per-frame IPC.
    for (const auto target : {false, true}) {
        out << (target ? ",\"target_parameters\":{" : ",\"parameters\":{");
        bool first = true;
        for (const auto& setting : rainSettings) {
            if (!first)
                out << ',';
            first = false;
            out << '\"' << setting.name
                << "\":" << (target ? s.target : s.current).*(setting.member);
        }
        out << '}';
    }
    if (enabled) {
        out << ",\"configured_snow\":{";
        writeSnowParameters(out, snowSettingsConfigured);
        out << '}';
    }
    out << ",\"snow_parameters\":{";
    writeSnowParameters(out, s.snowCurrent);
    out << "},\"snow_target_parameters\":{";
    writeSnowParameters(out, s.snowTarget);
    out << "},\"snow_metrics\":{\"active\":" << snowMetrics.active
        << ",\"impacts\":" << snowMetrics.impacts << ",\"shed\":" << snowMetrics.shed
        << ",\"pool_dropped\":" << snowMetrics.pool_dropped
        << ",\"atmospheric_throttled\":" << snowMetrics.atmospheric_throttled
        << ",\"ambient_recycled\":" << snowMetrics.ambient_recycled
        << ",\"deposited\":" << snowMetrics.deposited << ",\"settled\":" << snowMetrics.settled
        << ",\"falling\":" << snowMetrics.falling << ",\"escaped\":" << snowMetrics.escaped
        << ",\"discarded\":" << snowMetrics.discarded
        << ",\"floor_volume\":" << snowMetrics.floor_volume << ",\"melted\":" << snowMetrics.melted
        << ",\"overflow\":" << snowMetrics.overflow
        << ",\"temperature_known\":" << (s.snowTarget.temperature_known ? "true" : "false")
        << ",\"temperature_c\":" << s.snowTarget.temperature_c
        << ",\"segments\":" << (s.hasContext ? s.snowFrame.segment_count : 0)
        << ",\"flake_segments\":" << (s.hasContext ? s.snowFrame.flake_segments : 0)
        << ",\"masked_segments\":" << (s.hasContext ? s.snowFrame.masked_segments : 0)
        << ",\"segments_dropped\":" << (s.hasContext ? s.snowFrame.metrics.segments_dropped : 0)
        << '}';
    out << '}';
    return out.str();
}
bool active() {
    if (session && Clock::now() >= session->deadline)
        rainShutdown();
    if (session)
        session->temperatureLease.expire(session->snowCurrent, session->snowTarget,
                                         wallTimestampMS(), Clock::now());
    return bool(session);
}

const char* schedulingSuppression(PHLMONITOR monitor) {
    if (!monitor || !monitor->m_enabled || !monitor->m_dpmsStatus || monitor->isMirror())
        return "monitor_unavailable";
    if (!PROTO::sessionLock || PROTO::sessionLock->isLocked())
        return "session_lock";
    if (session && session->reducedMotion)
        return "reduced_motion";
    if (!Fullscreen::controller() || Fullscreen::controller()->hasFullscreen(monitor))
        return "fullscreen";
    return nullptr;
}

void armTimer(bool suppressed = false) {
    if (!session || !session->timer)
        return;
    auto& s = *session;
    const auto now = Clock::now();
    const auto target =
        s.schedule.wakeDeadline(now, s.deadline, suppressed || !s.activity().needed());
    s.timer->updateTimeout(std::max(Clock::duration{1}, target - now));
}

void wakePrecipitation(bool previouslyNeeded) {
    if (!active() || previouslyNeeded || !session->activity().needed())
        return;
    session->last = Clock::now();
    session->schedule.reset(session->last, session->fps);
    const auto monitor = session->monitor.lock();
    const bool suppressed = schedulingSuppression(monitor) != nullptr;
    armTimer(suppressed);
    if (!suppressed && g_pHyprRenderer) {
        g_pHyprRenderer->damageMonitor(monitor);
        ++session->scheduledDamage;
    }
}

void scheduleTick(SP<CEventLoopTimer> self, void*) noexcept {
    // Never retain a raw Session: replacements/expiry must invalidate callbacks.
    if (!session || session->timer != self)
        return;
    try {
        ++session->timerCallbacks;
        if (!active())
            return;
        const auto monitor = session->monitor.lock();
        const auto reason = schedulingSuppression(monitor);
        if (reason) {
            ++session->suppressedTicks;
            if (session->hasContext) {
                session->simulation.reset();
                session->snowSimulation.reset();
                session->refreshResiduals();
                session->snowFrame.segment_count = session->snowFrame.flake_segments =
                    session->snowFrame.masked_segments = 0;
                session->snowFrame.metrics = {};
                session->acceptedIntervals.breakSequence();
            }
            session->hasContext = false;
            session->suppression = reason;
            session->last = Clock::now();
        } else if (!session->activityPolicy.schedule(session->activity())) {
            session->suppression = "empty_precipitation";
            session->acceptedIntervals.breakSequence();
            session->settleEmptyParameters(Clock::now());
        } else if (g_pHyprRenderer) {
            g_pHyprRenderer->damageMonitor(monitor);
            ++session->scheduledDamage;
        } else
            throw std::runtime_error("renderer unavailable");
        armTimer(reason != nullptr);
    } catch (...) {
        // Remove executable callback ownership even when cleanup itself fails.
        self->cancel();
        if (g_pEventLoopManager)
            g_pEventLoopManager->removeTimer(self);
        if (session) {
            session->timer.reset();
            session->suppression = "timer_exception";
            try {
                rainShutdown();
            } catch (...) {
                session.reset();
            }
        }
    }
}
} // namespace

void rainShutdown() {
    if (!session)
        return;
    if (session->timer) {
        session->timer->cancel();
        if (g_pEventLoopManager)
            g_pEventLoopManager->removeTimer(session->timer);
        session->timer.reset();
    }
    session->presentedListener.reset();
    lastStatus = status(false);
    // Custom pass destructors live in this DSO. Remove retained frame objects
    // before unload, while their vtables and GPU owner are still available.
    if (g_pHyprRenderer)
        g_pHyprRenderer->m_renderPass.removeAllOfType("AWeatherAppSegmentPass");
    if (const auto monitor = session->monitor.lock(); monitor && g_pHyprRenderer)
        g_pHyprRenderer->damageMonitor(monitor);
    if (!session->gpu->shutdown()) {
        lastStatus.pop_back();
        lastStatus += ",\"cleanup_failed\":true}";
    }
    session.reset();
}

void rainPrecheck(PHLMONITOR monitor) {
    (void)monitor;
    active(); // Expiry also runs here; timer enforces it on idle/DPMS outputs.
}

void rainPrepareFrame(PHLMONITOR monitor) {
    if (!active() || !monitor || session->monitor != monitor || schedulingSuppression(monitor))
        return;
    if (!session->activityPolicy.prepare(session->activity()))
        return;
    // render.pre runs after the compositor's needs-frame gate and before
    // beginRender consumes this damage ring. Refresh the whole animated effect
    // within this existing frame; damageMonitor/addDamage would schedule another
    // frame and defeat the idle timer cap. No scheduling side effect here.
    monitor->m_damage.damageEntire();
}

void rainStage() {
    if (!active() || !g_pHyprRenderer || g_pHyprRenderer->m_bRenderingSnapshot)
        return;
    const auto monitor = g_pHyprRenderer->renderData().pMonitor.lock();
    if (!monitor || session->monitor != monitor)
        return;
    if (session->reducedMotion) {
        session->suppression = "reduced_motion";
        return;
    }
    if (!session->activityPolicy.stage(session->activity())) {
        session->suppression = "empty_precipitation";
        session->acceptedIntervals.breakSequence();
        session->settleEmptyParameters(Clock::now());
        return;
    }
    const auto started = Clock::now();
    try {
        auto geometry = captureGeometry(monitor);
        auto& s = *session;
        ++s.geometryCaptures;
        if (!geometry.valid) {
            ++s.geometryRejected;
            s.acceptedIntervals.breakSequence();
            if (s.hasContext) {
                s.simulation.reset();
                s.snowSimulation.reset();
            }
            s.refreshResiduals();
            s.snowFrame.segment_count = s.snowFrame.flake_segments = s.snowFrame.masked_segments =
                0;
            s.snowFrame.metrics = {};
            s.hasContext = false;
            s.suppression = geometry.reason;
            s.last = started;
            return;
        }
        ++s.geometryAccepted;
        std::uint64_t impactsBefore = 0;
        if (!s.hasContext || s.context != geometry.context) {
            s.acceptedIntervals.breakSequence();
            s.simulation.reset();
            s.snowSimulation.reset();
            s.refreshResiduals();
            s.frame.metrics = {};
            s.snowFrame.segment_count = s.snowFrame.flake_segments = s.snowFrame.masked_segments =
                0;
            s.snowFrame.metrics = {};
            s.context = geometry.context;
            s.hasContext = true;
            s.last = started;
        } else
            impactsBefore = s.frame.metrics.impacts;
        const float delta = std::chrono::duration<float>(started - s.last).count();
        s.last = started;
        approachParameters(s.current, s.target, delta);
        approachSnowParameters(s.snowCurrent, s.snowTarget, delta);
        s.snowCurrent.wind = s.current.wind_x;
        // Mapped clients can disappear from this output without closing. Drop
        // only their supported water; real closures still reconcile as detach.
        for (std::size_t i = 0; i < geometry.mapped_count; ++i) {
            const auto id = geometry.mapped_ids[i];
            bool visible = false;
            for (std::size_t j = 0; j < geometry.count; ++j)
                if (geometry.supports[j].id == id) {
                    visible = true;
                    break;
                }
            if (!visible) {
                s.simulation.discardSupport(id);
                s.snowSimulation.discardSupport(id);
            }
        }
        const auto activity = s.activity();
        if (activity.rain) {
            ++s.activityPolicy.rainSteps;
            if (!s.simulation.step(delta, monitor->m_size.x, monitor->m_size.y, s.current,
                                   std::span(geometry.supports.data(), geometry.count), s.frame,
                                   s.interactions)) {
                s.refreshResiduals();
                s.suppression = "simulation_input";
                s.hasContext = false;
                s.acceptedIntervals.breakSequence();
                return;
            }
            s.impactsMeasured += s.frame.metrics.impacts - impactsBefore;
        } else {
            s.frame.segment_count = s.frame.rain_segments = s.frame.masked_segments = 0;
            s.frame.far = {};
        }
        const auto snowBefore = s.snowFrame.metrics;
        // Settled includes both window edges and the desktop floor, so the
        // reservoir keeps updating/melting after the final flake disappears.
        if (s.snowCurrent.strength > .0001f || s.snowResidual) {
            ++s.activityPolicy.snowSteps;
            if (!s.snowSimulation.step(delta, monitor->m_size.x, monitor->m_size.y, s.snowCurrent,
                                       std::span(geometry.supports.data(), geometry.count),
                                       s.snowFrame, s.interactions)) {
                s.simulation.reset();
                s.refreshResiduals();
                s.snowFrame.segment_count = s.snowFrame.flake_segments =
                    s.snowFrame.masked_segments = 0;
                s.suppression = "snow_simulation_input";
                s.hasContext = false;
                s.acceptedIntervals.breakSequence();
                return;
            }
        } else {
            s.snowFrame.segment_count = s.snowFrame.flake_segments = s.snowFrame.masked_segments =
                0;
            s.snowFrame.metrics = snowBefore;
        }
        s.refreshResiduals();
        std::vector<Mask> masks;
        s.frame.mask_count = geometry.count;
        masks.reserve(geometry.count + geometry.extra_mask_count);
        for (std::size_t i = 0; i < geometry.count; ++i) {
            const auto& r = geometry.supports[i].rect;
            masks.push_back({r.x, r.y, r.w, r.h});
        }
        for (std::size_t i = 0; i < geometry.extra_mask_count; ++i) {
            const auto& r = geometry.extra_masks[i];
            masks.push_back({r.x, r.y, r.w, r.h});
        }
        const auto& far = s.frame.far;
        // The second immutable pass owns the same current mask snapshot. Both
        // passes share GPU resources and the existing unload cleanup class.
        if (s.activityPolicy.snowPass(s.snowFrame)) {
            std::vector<Segment> snowSegments;
            snowSegments.reserve(s.snowFrame.segment_count);
            for (std::size_t i = 0; i < s.snowFrame.segment_count; ++i)
                snowSegments.push_back(s.snowFrame.segments[i].values);
            g_pHyprRenderer->addPassElement(
                makeUnique<SegmentPass>(s.gpu, monitor->m_size, std::move(snowSegments), masks,
                                        s.snowFrame.masked_segments, Far{}));
        }
        if (s.activityPolicy.rainPass(s.frame)) {
            std::vector<Segment> segments;
            segments.reserve(s.frame.segment_count);
            for (std::size_t i = 0; i < s.frame.segment_count; ++i)
                segments.push_back(s.frame.segments[i].values);
            g_pHyprRenderer->addPassElement(makeUnique<SegmentPass>(
                s.gpu, monitor->m_size, std::move(segments), std::move(masks),
                s.frame.masked_segments,
                Far{static_cast<uint32_t>(far.count), far.speed, far.wind, far.length, far.width,
                    far.opacity, static_cast<float>(s.frame.time)}));
        }
        ++s.frames;
        s.acceptedIntervals.observe(timestampMS(started));
        s.suppression = "none";
        const double ms = std::chrono::duration<double, std::milli>(Clock::now() - started).count();
        s.totalCPU += ms;
        s.maxCPU = std::max(s.maxCPU, ms);
        s.cpuSubmission.add(ms, timestampMS(started));
    } catch (...) {
        // No exception may escape into the compositor. Retain a diagnostic and
        // stop scheduling until an explicit new enable request.
        if (session)
            session->suppression = "callback_exception";
        rainShutdown();
    }
}

std::string rainRequest(std::string input) {
    const auto command = parseRainCommand(input);
    if (command.action == RainCommand::INVALID)
        return std::string("{\"error\":\"") + command.error + "\"}";
    if (command.guarded && (!active() || session->generation != command.generation))
        return "{\"error\":\"session_generation_mismatch\"}";
    if (command.action == RainCommand::RENEW) {
        // The guard above rejects expired/replaced sessions; renewal never
        // recreates resources or resets simulation, metrics, or suppression.
        const auto now = Clock::now();
        if (now >= session->deadline) {
            rainShutdown();
            return "{\"error\":\"session_generation_mismatch\"}";
        }
        session->deadline = now + std::chrono::seconds(command.seconds);
        armTimer(schedulingSuppression(session->monitor.lock()) != nullptr);
        return status();
    }
    if (command.action == RainCommand::REDUCED || command.action == RainCommand::PHYSICS ||
        command.action == RainCommand::ACCUMULATION) {
        if (command.action == RainCommand::REDUCED)
            configuredReduced = command.flag;
        else if (command.action == RainCommand::PHYSICS)
            configuredInteractions.window_physics = command.flag;
        else
            configuredInteractions.accumulation = command.flag;
        if (active()) {
            session->reducedMotion = configuredReduced;
            session->interactions = configuredInteractions;
            if (configuredReduced) {
                session->simulation.reset();
                session->snowSimulation.reset();
                session->frame = {};
                session->snowFrame = {};
                session->hasContext = false;
                session->refreshResiduals();
                session->suppression = "reduced_motion";
            } else if (!configuredInteractions.window_physics ||
                       !configuredInteractions.accumulation) {
                // Window interaction is independent of desktop accumulation.
                // The next same-frame core step removes only window stores;
                // disabling accumulation explicitly discards every reservoir.
                if (!configuredInteractions.accumulation) {
                    session->simulation.discardAccumulation();
                    session->snowSimulation.discardAccumulation();
                    session->refreshResiduals();
                }
                session->frame.segment_count = session->frame.rain_segments =
                    session->frame.masked_segments = 0;
                session->snowFrame.segment_count = session->snowFrame.flake_segments =
                    session->snowFrame.masked_segments = 0;
            }
            session->last = Clock::now();
            session->schedule.reset(session->last, session->fps);
            armTimer(schedulingSuppression(session->monitor.lock()) != nullptr);
            if (auto monitor = session->monitor.lock(); monitor && g_pHyprRenderer)
                g_pHyprRenderer->damageMonitor(monitor);
        }
        return status();
    }
    if (command.action == RainCommand::OFF) {
        rainShutdown();
        return status();
    }
    if (command.action == RainCommand::STATUS) {
        active();
        return status();
    }
    if (command.action == RainCommand::FPS) {
        configuredFPS = command.fps;
        if (active()) {
            session->fps = configuredFPS;
            session->schedule.reset(Clock::now(), configuredFPS);
            armTimer(schedulingSuppression(session->monitor.lock()) != nullptr);
        }
        return status();
    }
    if (command.action == RainCommand::RESETMETRICS) {
        if (!active())
            return "{\"error\":\"rain_not_enabled\"}";
        session->resetMetrics();
        return status();
    }
    if (command.action == RainCommand::SET) {
        const bool previouslyNeeded = session && session->activity().needed();
        settings.*(rainSettings[command.setting].member) = command.value;
        if (session)
            session->target = settings;
        wakePrecipitation(previouslyNeeded);
        return "{\"schema_version\":1,\"setting_accepted\":true}";
    }
    if (command.action == RainCommand::SNOW) {
        const bool previouslyNeeded = session && session->activity().needed();
        const auto member = snowSettings[static_cast<std::size_t>(command.snowSetting)].member;
        snowSettingsConfigured.*member = command.value;
        // Thermal provenance is session-scoped and must survive intensity,
        // accumulation and overload changes without becoming a global default.
        if (session)
            session->snowTarget.*member = command.value;
        wakePrecipitation(previouslyNeeded);
        return "{\"schema_version\":1,\"snow_setting_accepted\":true}";
    }
    if (command.action == RainCommand::TEMPERATURE) {
        session->temperatureLease.accept(command, session->snowCurrent, session->snowTarget,
                                         wallTimestampMS(), Clock::now());
        return "{\"schema_version\":1,\"temperature_accepted\":true}";
    }
    PHLMONITOR selected;
    for (const auto& monitor : State::monitorState()->monitors())
        if (monitor && monitor->m_id == command.monitor && monitor->m_enabled)
            selected = monitor;
    if (!selected)
        return "{\"error\":\"monitor_not_found\"}";
    if (generationCounter == UINT64_MAX)
        return "{\"error\":\"session_generation_exhausted\"}";
    // Allocate first: a failed request must not destroy an existing session.
    auto next = std::make_unique<Session>();
    next->monitor = selected;
    next->reducedMotion = configuredReduced;
    next->interactions = configuredInteractions;
    next->generation = ++generationCounter;
    next->current = next->target = settings;
    next->snowCurrent = next->snowTarget = snowSettingsConfigured;
    next->deadline = Clock::now() + std::chrono::seconds(command.seconds);
    next->last = Clock::now();
    next->fps = configuredFPS;
    next->schedule.reset(next->last, next->fps);
    if (!g_pEventLoopManager)
        return "{\"error\":\"event_loop_unavailable\"}";
    next->timer = makeShared<CEventLoopTimer>(std::nullopt, scheduleTick, nullptr);
    rainShutdown();
    session = std::move(next);
    // Public monitor signal carries compositor presentation timestamps. These
    // measure the selected output, including presentations without a rain pass;
    // accepted stage timestamps above measure submission cadence separately.
    session->presentedListener = selected->m_events.presented.listen([](Time::steady_tp timestamp) {
        if (session && Clock::now() < session->deadline && timestamp >= session->metricsStarted)
            session->presentedIntervals.observe(timestampMS(timestamp));
    });
    try {
        g_pEventLoopManager->addTimer(session->timer);
        armTimer(schedulingSuppression(selected) != nullptr);
        if (!schedulingSuppression(selected) && session->activity().needed()) {
            g_pHyprRenderer->damageMonitor(selected);
            ++session->scheduledDamage;
        }
    } catch (...) {
        rainShutdown();
        throw;
    }
    return status();
}
} // namespace AWeatherApp
