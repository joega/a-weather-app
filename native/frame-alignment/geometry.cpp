#include "geometry.hpp"
#include "geometry_math.hpp"
#include <hyprland/src/desktop/Workspace.hpp>
#include <hyprland/src/desktop/state/WindowState.hpp>
#include <hyprland/src/desktop/state/FocusState.hpp>
#include <hyprland/src/desktop/state/FadingOutState.hpp>
#include <hyprland/src/desktop/state/WindowFadeout.hpp>
#include <hyprland/src/desktop/view/Window.hpp>
#include <hyprland/src/render/Renderer.hpp>
#include <hyprland/src/managers/fullscreen/FullscreenController.hpp>
#include <hyprland/src/protocols/SessionLock.hpp>
#include <algorithm>
#include <cmath>
#include <limits>

namespace AWeatherApp {
namespace {
GeometrySnapshot reject(const char* reason) {
    GeometrySnapshot result;
    result.reason = reason;
    return result;
}
bool zero(Vector2D value) { return value.x == 0.0 && value.y == 0.0; }
bool bounded(double value, double limit) { return std::isfinite(value) && std::abs(value) <= limit; }

}

GeometrySnapshot captureGeometry(PHLMONITOR monitor) {
    if (!g_pHyprRenderer || !monitor || !monitor->m_enabled || monitor->isMirror() || !monitor->m_dpmsStatus)
        return reject("monitor_unavailable");
    if (!PROTO::sessionLock || PROTO::sessionLock->isLocked())
        return reject("session_lock");
    if (!Fullscreen::controller() || Fullscreen::controller()->hasFullscreen(monitor))
        return reject("fullscreen");
    const auto& render = g_pHyprRenderer->renderData();
    if (g_pHyprRenderer->m_bRenderingSnapshot || render.pMonitor.lock() != monitor ||
        render.projectionType != Render::RPT_MONITOR ||
        (render.renderModif.enabled && !render.renderModif.modifs.empty()) || render.mouseZoomFactor != 1.0f ||
        monitor->m_transform != WL_OUTPUT_TRANSFORM_NORMAL)
        return reject("render_transform");
    const auto workspace = monitor->m_activeWorkspace;
    if (!workspace || workspace->inert() || workspace->m_isSpecialWorkspace || monitor->m_activeSpecialWorkspace)
        return reject("special_or_missing_workspace");
    if (!workspace->m_visible || workspace->m_forceRendering ||
        !workspace->m_renderOffset || !workspace->m_alpha ||
        workspace->m_renderOffset->isBeingAnimated() || workspace->m_alpha->isBeingAnimated() ||
        !zero(workspace->m_renderOffset->value()) || workspace->m_alpha->value() != 1.0f)
        return reject("workspace_transition");
    // Pack the full accepted ID domains; reject wider IDs instead of hashing
    // potentially distinct monitor/workspace contexts into the same reset key.
    if (monitor->m_id < 0 || static_cast<std::uint64_t>(monitor->m_id) > UINT32_MAX ||
        workspace->m_id < INT32_MIN || workspace->m_id > INT32_MAX)
        return reject("context_id_range");
    if (!bounded(monitor->m_position.x, 32768) || !bounded(monitor->m_position.y, 32768) ||
        !bounded(monitor->m_size.x, 16384) || !bounded(monitor->m_size.y, 16384) ||
        monitor->m_size.x <= 0 || monitor->m_size.y <= 0 ||
        !std::isfinite(monitor->m_scale) || monitor->m_scale <= 0 || monitor->m_scale > 8)
        return reject("monitor_geometry");
    const auto& fadeouts = Desktop::fadingOutState()->fadeouts();
    if (fadeouts.size() > 1024)
        return reject("fadeout_registry_limit");
    const auto& windows = Desktop::windowState()->windows();
    if (windows.size() > 1024)
        return reject("window_registry_limit");
    std::array<PHLWINDOW, physics::support_cap> candidates{};
    std::size_t count = 0;
    GeometrySnapshot result;
    for (const auto& window : windows) {
        if (window && window->m_isMapped) {
            if (!window->m_stableID)
                return reject("zero_stable_id");
            result.mapped_ids[result.mapped_count++] = window->m_stableID;
        }
        if (!window || !window->m_isMapped || window->isHidden() ||
            !g_pHyprRenderer->shouldRenderWindow(window, monitor))
            continue;
        // renderWindow skips a completely invisible stable-alpha client.
        if (window->effectiveAlpha() == 0.0f && !window->alpha().isBeingAnimated())
            continue;
        if (window->m_monitor != monitor || window->m_workspace != workspace || window->m_monitorMovedFrom != -1)
            return reject("cross_workspace_or_monitor");
        if (window->m_pinned || !window->m_transformers.empty() || !zero(window->m_floatingOffset))
            return reject("client_transform");
        if (window->m_popupHead && window->m_popupHead->popupTreeCount() > 0)
            return reject("client_popup");
        // Fading client bodies are outside this first ordinary-workspace model.
        // Ordinary position/size animations remain fully supported.
        for (const auto alpha : {Desktop::View::WINDOW_ALPHA_FADE, Desktop::View::WINDOW_ALPHA_FULLSCREEN,
                                 Desktop::View::WINDOW_ALPHA_LAYOUT, Desktop::View::WINDOW_ALPHA_MOVE_TO_WORKSPACE,
                                 Desktop::View::WINDOW_ALPHA_MOVE_FROM_WORKSPACE})
            if (window->alphaValue(alpha) != 1.0f || window->alpha(alpha)->isBeingAnimated())
                return reject("client_transition");
        if (count == candidates.size())
            return reject("support_limit");
        if (!window->m_stableID)
            return reject("zero_stable_id");
        candidates[count++] = window;
    }
    // Renderer.cpp v0.56.2 renderWorkspaceWindows: tiled registry order,
    // focused tiled last, then floating registry order. Pinned is separately
    // drawn above these at RENDER_POST_WINDOWS and is rejected above.
    const auto focused = Desktop::focusState()->window();
    std::size_t tiledCount = 0;
    result.context = (static_cast<std::uint64_t>(monitor->m_id) << 32) |
                     static_cast<std::uint32_t>(workspace->m_id);
    for (int pass = 0; pass < 3; ++pass) {
        for (std::size_t i = 0; i < count; ++i) {
            const auto& window = candidates[i];
            const int windowPass = window->m_isFloating ? 2 : (window == focused ? 1 : 0);
            if (windowPass != pass)
                continue;
            using Geometry = Desktop::View::IGeometric;
            const auto pos = window->position(Geometry::GEOMETRIC_CURRENT) - monitor->m_position;
            const auto size = window->size(Geometry::GEOMETRIC_CURRENT);
            if (!bounded(pos.x, 32768) || !bounded(pos.y, 32768) ||
                !bounded(size.x, 16384) || !bounded(size.y, 16384) || size.x < 5 || size.y < 5)
                return reject("client_geometry");
            for (std::size_t j = 0; j < result.count; ++j)
                if (result.supports[j].id == window->m_stableID)
                    return reject("duplicate_stable_id");
            auto& support = result.supports[result.count];
            support.id = window->m_stableID;
            support.rect = {static_cast<float>(pos.x), static_cast<float>(pos.y),
                            static_cast<float>(size.x), static_cast<float>(size.y)};
            support.stack = static_cast<int>(result.count);
            if (!window->m_isFloating) ++tiledCount;
            ++result.count;
        }
    }
    std::array<GeometryMath::Mask, physics::support_cap> masks{};
    for (const auto& fadeout : fadeouts) {
        if (!fadeout || fadeout->done() || fadeout->monitor() != monitor || fadeout->alpha() == 0.0f)
            continue;
        const auto plane = fadeout->plane();
        if (plane == Desktop::FADEOUT_PLANE_POPUP || plane == Desktop::FADEOUT_PLANE_WINDOW_OVER_FULLSCREEN)
            return reject("unsupported_client_fadeout");
        if (plane != Desktop::FADEOUT_PLANE_WINDOW_TILED && plane != Desktop::FADEOUT_PLANE_WINDOW_FLOATING)
            continue;
        // Match renderFadeouts' workspace filter. A foreign ordinary workspace
        // is not rendered during our accepted non-transition context.
        if (fadeout->workspace() && fadeout->workspace() != workspace)
            continue;
        const auto* windowFadeout = dynamic_cast<const Desktop::CWindowFadeout*>(fadeout.get());
        if (!windowFadeout || !windowFadeout->framebuffer() || !windowFadeout->framebuffer()->getTexture())
            return reject("unknown_window_fadeout");
        if (result.count + result.extra_mask_count == physics::support_cap)
            return reject("combined_mask_limit");
        // WindowFadeout.cpp renderBox transforms a full-output snapshot quad;
        // do NOT mask that quad. Its original client body maps exactly to the
        // inherited current geometric position/size under that transform.
        using Geometry = Desktop::View::IGeometric;
        const auto pos = windowFadeout->position(Geometry::GEOMETRIC_CURRENT) - monitor->m_position;
        const auto size = windowFadeout->size(Geometry::GEOMETRIC_CURRENT);
        const physics::Rect rect{static_cast<float>(pos.x), static_cast<float>(pos.y),
                                 static_cast<float>(size.x), static_cast<float>(size.y)};
        if (!GeometryMath::validRect(rect) || !std::isfinite(fadeout->alpha()) || fadeout->alpha() < 0 || fadeout->alpha() > 1)
            return reject("fadeout_geometry");
        result.extra_masks[result.extra_mask_count] = rect;
        masks[result.extra_mask_count++] = {rect, static_cast<int>(plane == Desktop::FADEOUT_PLANE_WINDOW_TILED ? tiledCount : result.count)};
    }
    if (!GeometryMath::exposedTops({result.supports.data(), result.count}, {masks.data(), result.extra_mask_count}))
        return reject("invalid_exposure");
    result.valid = true;
    result.reason = "accepted";
    return result;
}
}
