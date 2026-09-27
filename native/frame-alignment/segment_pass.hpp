#pragma once

#ifndef A_WEATHER_APP_SEGMENT_GL_TEST
#include <hyprland/src/render/pass/PassElement.hpp>
#endif
#include <array>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <string_view>
#include <vector>

namespace AWeatherApp {
using Segment = std::array<float, 9>; // ax, ay, bx, by, width, r, g, b, a
using Mask = std::array<float, 4>;    // x, y, width, height
constexpr std::size_t SEGMENT_LIMIT = 6000;
constexpr std::size_t MASK_LIMIT = 64;
constexpr uint32_t FAR_LIMIT = 2400;
struct Far {
    uint32_t count = 0;
    float speed = 290.0f, wind = 0.0f, length = 7.0f, width = 0.65f;
    float opacity = 0.12f, time = 0.0f;
};

#ifndef A_WEATHER_APP_SEGMENT_GL_TEST

// Share one instance across frames. Construction performs no GL operations;
// resources are created lazily in the compositor's current GLES context.
// shutdown() permanently disables this instance and deletes resources with
// their owning context current, restoring the caller's EGL context afterwards.
// Call shutdown on off/unload, before dropping the final shared owner. Queued
// passes retain ownership and then suppress drawing, rather than dangling.
class SegmentGPU {
  public:
    SegmentGPU();
    ~SegmentGPU();
    SegmentGPU(const SegmentGPU&) = delete;
    SegmentGPU& operator=(const SegmentGPU&) = delete;
    bool shutdown() noexcept;
    std::string_view lastSuppression() const noexcept;
    uint64_t drawCalls() const noexcept;
    uint64_t segmentsDrawn() const noexcept;

  private:
    struct Impl;
    std::unique_ptr<Impl> m_impl;
    friend class SegmentPass;
};

class SegmentPass final : public IPassElement {
  public:
    // All geometry is monitor-local logical coordinates. Inputs are moved into
    // immutable owned snapshots. Invalid/overflow inputs throw invalid_argument.
    SegmentPass(std::shared_ptr<SegmentGPU> gpu, Vector2D logicalSize,
                std::vector<Segment> segments, std::vector<Mask> masks = {},
                std::size_t maskedPrefix = 0, Far far = {});
    std::vector<UP<IPassElement>> draw() override;
    bool needsLiveBlur() override { return false; }
    bool needsPrecomputeBlur() override { return false; }
    const char* passName() override { return "AWeatherAppSegmentPass"; }
    ePassElementType type() override { return EK_CUSTOM; }
    std::optional<CBox> boundingBox() override;
    CRegion opaqueRegion() override { return {}; }

  private:
    const std::shared_ptr<SegmentGPU> m_gpu;
    const Vector2D m_size;
    const std::vector<Segment> m_segments;
    const std::vector<Mask> m_masks;
    const std::size_t m_maskedPrefix;
    const Far m_far;
};
#endif
}
