#include <hyprland/src/plugins/PluginAPI.hpp>
#include <hyprland/src/desktop/state/WindowState.hpp>
#include <hyprland/src/desktop/view/Window.hpp>
#include <hyprland/src/render/Renderer.hpp>
#include <hyprland/src/managers/fullscreen/FullscreenController.hpp>
#include <hyprland/src/protocols/SessionLock.hpp>
#include <hyprland/src/managers/eventLoop/EventLoopManager.hpp>

#include <array>
#include <charconv>
#include <chrono>
#include <cmath>
#include <cstring>
#include <dlfcn.h>
#include <sstream>
#include <stdexcept>
#include "rain_controller.hpp"

namespace {
using Clock = std::chrono::steady_clock;
struct Target {
    std::uintptr_t address = 0;
    uint64_t stable = 0;
    std::optional<CBox> previous;
    PHLMONITORREF previousMonitor;
};
HANDLE handle = nullptr;
SP<SHyprCtlCommand> command;
SP<SHyprCtlCommand> rainCommand;
CHyprSignalListener preListener, prepareListener, stageListener;
std::array<Target, 8> targets;
std::size_t count = 0;
Clock::time_point deadline;
uint64_t stageFrames = 0, rulesEmitted = 0;

void clear() {
    for (auto& target : targets) {
        if (target.previous && g_pHyprRenderer)
            g_pHyprRenderer->damageBox(*target.previous);
        target = {};
    }
    count = 0;
}
bool active() {
    if (count && Clock::now() >= deadline)
        clear();
    return count != 0;
}
std::optional<CBox> rule(const Target& target, PHLMONITOR monitor) {
    if (!monitor || !monitor->m_activeWorkspace || !monitor->m_enabled || monitor->isMirror() ||
        !PROTO::sessionLock || PROTO::sessionLock->isLocked() ||
        Fullscreen::controller()->hasFullscreen(monitor))
        return {};
    const auto& windows = Desktop::windowState()->windows();
    if (windows.size() > 1024)
        return {};
    for (const auto& window : windows) {
        if (!window || reinterpret_cast<std::uintptr_t>(window.get()) != target.address)
            continue;
        if (window->m_stableID != target.stable || !window->m_isMapped ||
            window->m_monitor != monitor || window->m_workspace != monitor->m_activeWorkspace ||
            !g_pHyprRenderer->shouldRenderWindow(window, monitor))
            return {};
        using Geometry = Desktop::View::IGeometric;
        const auto at = window->position(Geometry::GEOMETRIC_CURRENT);
        const auto size = window->size(Geometry::GEOMETRIC_CURRENT);
        if (!std::isfinite(at.x) || !std::isfinite(at.y) || !std::isfinite(size.x) ||
            !std::isfinite(size.y) || size.x <= 0 || size.y <= 0)
            return {};
        return CBox{at, {size.x, 3.0}};
    }
    return {};
}
void precheck(PHLMONITOR monitor) {
    AWeatherApp::rainPrecheck(monitor);
    if (!active())
        return;
    for (std::size_t i = 0; i < count; ++i) {
        auto& target = targets[i];
        const auto current = rule(target, monitor);
        if (target.previous && target.previousMonitor == monitor) {
            g_pHyprRenderer->damageBox(*target.previous);
            target.previous.reset();
            target.previousMonitor.reset();
        }
        if (current) {
            // Also erase the old monitor's location during a monitor crossing.
            if (target.previous)
                g_pHyprRenderer->damageBox(*target.previous);
            g_pHyprRenderer->damageBox(*current);
            target.previous = current;
            target.previousMonitor = monitor;
        }
    }
}
void stage(eRenderStage stage) {
    if (stage == RENDER_POST_WINDOWS)
        AWeatherApp::rainStage();
    if (stage != RENDER_POST_WINDOWS || !active() || !g_pHyprRenderer ||
        g_pHyprRenderer->m_bRenderingSnapshot)
        return;
    const auto monitor = g_pHyprRenderer->renderData().pMonitor.lock();
    ++stageFrames;
    for (std::size_t i = 0; i < count; ++i) {
        auto box = rule(targets[i], monitor);
        if (!box)
            continue;
        targets[i].previous = box;
        targets[i].previousMonitor = monitor;
        CRectPassElement::SRectData data;
        data.box = box->translate(-monitor->m_position).scale(monitor->m_scale);
        data.color = CHyprColor(1.0, 0.0, 1.0, 0.85);
        g_pHyprRenderer->addPassElement(makeUnique<CRectPassElement>(data));
        ++rulesEmitted;
    }
}
std::string status() {
    const bool enabled = active();
    return "{\"schema_version\":1,\"enabled\":" + std::string(enabled ? "true" : "false") +
           ",\"selected\":" + std::to_string(count) +
           ",\"stage_frames\":" + std::to_string(stageFrames) +
           ",\"rules_emitted\":" + std::to_string(rulesEmitted) + "}";
}
std::string request(eHyprCtlOutputFormat, std::string input) {
    if (input.size() > 1024)
        return "{\"error\":\"request_limit\"}";
    std::istringstream stream(input);
    std::string name, action, extra;
    stream >> name >> action;
    if (name != "a-weather-app:alignment")
        return "{\"error\":\"invalid_command\"}";
    if (action == "status" || action == "off") {
        if (stream >> extra)
            return "{\"error\":\"invalid_arguments\"}";
        if (action == "off")
            clear();
        return status();
    }
    if (action != "on")
        return "{\"error\":\"expected_on_off_status\"}";
    std::string secondsToken;
    stream >> secondsToken;
    int seconds = 0;
    const auto parsedSeconds =
        std::from_chars(secondsToken.data(), secondsToken.data() + secondsToken.size(), seconds);
    if (parsedSeconds.ec != std::errc{} ||
        parsedSeconds.ptr != secondsToken.data() + secondsToken.size() || seconds < 1 ||
        seconds > 30)
        return "{\"error\":\"duration_must_be_1_to_30\"}";
    std::array<Target, 8> next;
    std::size_t nextCount = 0;
    const auto& windows = Desktop::windowState()->windows();
    if (windows.size() > 1024)
        return "{\"error\":\"window_limit\"}";
    while (stream >> extra) {
        if (nextCount == next.size() || !extra.starts_with("0x") || extra.size() <= 2)
            return "{\"error\":\"invalid_addresses\"}";
        std::uintptr_t address = 0;
        const auto parsed =
            std::from_chars(extra.data() + 2, extra.data() + extra.size(), address, 16);
        if (parsed.ec != std::errc{} || parsed.ptr != extra.data() + extra.size() || !address)
            return "{\"error\":\"invalid_addresses\"}";
        for (std::size_t i = 0; i < nextCount; ++i)
            if (next[i].address == address)
                return "{\"error\":\"duplicate_address\"}";
        bool found = false;
        for (const auto& window : windows) {
            if (window && window->m_isMapped &&
                reinterpret_cast<std::uintptr_t>(window.get()) == address) {
                next[nextCount].address = address;
                next[nextCount].stable = window->m_stableID;
                found = true;
                break;
            }
        }
        if (!found)
            return "{\"error\":\"address_not_mapped\"}";
        ++nextCount;
    }
    if (!nextCount)
        return "{\"error\":\"addresses_required\"}";
    clear();
    targets = next;
    count = nextCount;
    stageFrames = rulesEmitted = 0;
    deadline = Clock::now() + std::chrono::seconds(seconds);
    // Schedule the initial presentation even if the clients are currently idle.
    for (const auto& window : windows)
        for (std::size_t i = 0; i < count; ++i)
            if (window && reinterpret_cast<std::uintptr_t>(window.get()) == targets[i].address)
                if (const auto box = rule(targets[i], window->m_monitor.lock()))
                    g_pHyprRenderer->damageBox(*box);
    return status();
}
} // namespace

APICALL EXPORT std::string PLUGIN_API_VERSION() {
    return HYPRLAND_API_VERSION;
}
APICALL EXPORT PLUGIN_DESCRIPTION_INFO PLUGIN_INIT(HANDLE pluginHandle) {
    const auto* serverHash = __hyprland_api_get_hash();
    const auto* clientHash = __hyprland_api_get_client_hash();
    if (!serverHash || !clientHash || std::strcmp(serverHash, clientHash) != 0)
        throw std::runtime_error("a-weather-app alignment: Hyprland build hash mismatch");
    // Inline globals must bind to the executable, not private plugin storage.
    // A hidden copy previously produced a null-this crash in renderData().
    if (dlsym(RTLD_DEFAULT, "g_pHyprRenderer") != static_cast<void*>(&g_pHyprRenderer) ||
        dlsym(RTLD_DEFAULT, "_ZN10NProtocols11sessionLockE") !=
            static_cast<void*>(&PROTO::sessionLock) ||
        dlsym(RTLD_DEFAULT, "g_pEventLoopManager") != static_cast<void*>(&g_pEventLoopManager) ||
        !g_pHyprRenderer || !PROTO::sessionLock || !g_pEventLoopManager)
        throw std::runtime_error(
            "a-weather-app alignment: compositor globals unavailable or privately bound");
    handle = pluginHandle;
    command =
        HyprlandAPI::registerHyprCtlCommand(handle, {"a-weather-app:alignment", false, request});
    if (!command)
        throw std::runtime_error("a-weather-app alignment: registration failed");
    rainCommand = HyprlandAPI::registerHyprCtlCommand(
        handle, {"a-weather-app:rain", false, [](eHyprCtlOutputFormat, std::string input) {
                     try {
                         return AWeatherApp::rainRequest(std::move(input));
                     } catch (...) {
                         return std::string("{\"error\":\"rain_request_failed\"}");
                     }
                 }});
    if (!rainCommand) {
        HyprlandAPI::unregisterHyprCtlCommand(handle, command);
        command.reset();
        throw std::runtime_error("a-weather-app rain: registration failed");
    }
    preListener = Event::bus()->m_events.render.preChecks.listen(precheck);
    prepareListener = Event::bus()->m_events.render.pre.listen(AWeatherApp::rainPrepareFrame);
    stageListener = Event::bus()->m_events.render.stage.listen(stage);
    return {"a-weather-app-frame-alignment", "Opt-in current-frame diagnostic top rules",
            "A Weather App", "1.0.0"};
}
APICALL EXPORT void PLUGIN_EXIT() {
    preListener.reset();
    prepareListener.reset();
    stageListener.reset();
    AWeatherApp::rainShutdown();
    if (rainCommand)
        HyprlandAPI::unregisterHyprCtlCommand(handle, rainCommand);
    rainCommand.reset();
    clear();
    if (command)
        HyprlandAPI::unregisterHyprCtlCommand(handle, command);
    command.reset();
    handle = nullptr;
}
