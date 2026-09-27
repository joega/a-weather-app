#include "segment_pass.hpp"

#ifndef A_WEATHER_APP_SEGMENT_GL_TEST
#include <hyprland/src/render/Renderer.hpp>
#include <hyprland/src/render/OpenGL.hpp>
#endif
#include <GLES3/gl3.h>
#include <EGL/egl.h>
#include <algorithm>
#include <cmath>
#include <stdexcept>
#include <span>

namespace AWeatherApp {
namespace {
constexpr const char* VERTEX = R"GLSL(#version 300 es
precision highp float;
layout(location=0) in vec4 endpoints;
layout(location=1) in float thickness;
layout(location=2) in vec4 color;
uniform vec2 logical_size;
uniform mat3 projection;
uniform int masked_prefix;
uniform int mode;
uniform vec4 far_parameters;
uniform vec3 far_color;
uniform float far_time;
uniform float far_opacity;
out vec4 v_color;
out float v_across;
out vec2 v_position;
flat out int v_masked;
float hash(float x) { return fract(sin(x*91.17+7.3)*43758.5); }
void main() {
    const vec2 corners[6] = vec2[6](vec2(0,-1),vec2(1,-1),vec2(1,1),
                                  vec2(0,-1),vec2(1,1),vec2(0,1));
    vec2 q = corners[gl_VertexID];
    vec2 a = endpoints.xy, b = endpoints.zw;
    float width = thickness;
    v_color = color;
    v_masked = mode == 0 || gl_InstanceID < masked_prefix ? 1 : 0;
    if (mode == 0) {
        float i = float(gl_InstanceID) + 1.;
        vec2 span = logical_size + vec2(2.*far_parameters.z);
        vec2 position = mod(vec2(hash(i*2.),hash(i*2.+1.))*span +
                            vec2(far_parameters.y,far_parameters.x)*far_time,span)-vec2(far_parameters.z);
        vec2 direction = normalize(vec2(far_parameters.y,max(far_parameters.x,1.)));
        a = position;
        b = position + direction*far_parameters.z;
        width = far_parameters.w;
        float alpha = far_opacity*(.65+.35*hash(i+99.));
        v_color = vec4(far_color*alpha,alpha);
    }
    vec2 delta = b - a;
    float len = length(delta);
    vec2 direction = len > .0001 ? delta / len : vec2(0,1);
    vec2 p = mix(a,b,q.x) + vec2(-direction.y,direction.x) * q.y * width * .5;
    vec3 projected = projection * vec3(p / logical_size,1);
    gl_Position = vec4(projected.xy,0,projected.z);
    v_across = q.y;
    v_position = p;
}
)GLSL";
constexpr const char* FRAGMENT = R"GLSL(#version 300 es
precision highp float;
precision highp int;
in vec4 v_color;
in float v_across;
in vec2 v_position;
flat in int v_masked;
uniform int mask_count;
uniform vec4 masks[64];
out vec4 result;
void main() {
    if (v_masked == 1) {
        for (int i=0;i<mask_count;++i) {
            vec4 mask = masks[i];
            if (all(greaterThanEqual(v_position,mask.xy)) &&
                all(lessThan(v_position,mask.xy+mask.zw))) discard;
        }
    }
    float coverage = 1. - smoothstep(.65,1.,abs(v_across));
    result = v_color * coverage;
}
)GLSL";

// Use raw GL exclusively and restore every changed value. Hyprland caches
// scissor/capability state internally; restoring actual state without touching
// its cache keeps the two in agreement. Never call backend scissor/blend here.
class StateGuard {
  public:
    StateGuard() {
        glGetIntegerv(GL_CURRENT_PROGRAM, &program);
        glGetIntegerv(GL_VERTEX_ARRAY_BINDING, &vao);
        glGetIntegerv(GL_ARRAY_BUFFER_BINDING, &buffer);
        glGetIntegerv(GL_BLEND_SRC_RGB, &srcRGB);
        glGetIntegerv(GL_BLEND_DST_RGB, &dstRGB);
        glGetIntegerv(GL_BLEND_SRC_ALPHA, &srcAlpha);
        glGetIntegerv(GL_BLEND_DST_ALPHA, &dstAlpha);
        glGetIntegerv(GL_BLEND_EQUATION_RGB, &equationRGB);
        glGetIntegerv(GL_BLEND_EQUATION_ALPHA, &equationAlpha);
        glGetIntegerv(GL_SCISSOR_BOX, scissor.data());
        glGetBooleanv(GL_COLOR_WRITEMASK, colorMask.data());
        for (std::size_t i = 0; i < caps.size(); ++i)
            enabled[i] = glIsEnabled(caps[i]);
    }
    ~StateGuard() {
        glUseProgram(program);
        glBindVertexArray(vao);
        glBindBuffer(GL_ARRAY_BUFFER, buffer);
        glBlendFuncSeparate(srcRGB, dstRGB, srcAlpha, dstAlpha);
        glBlendEquationSeparate(equationRGB, equationAlpha);
        glScissor(scissor[0], scissor[1], scissor[2], scissor[3]);
        glColorMask(colorMask[0], colorMask[1], colorMask[2], colorMask[3]);
        for (std::size_t i = 0; i < caps.size(); ++i) {
            if (enabled[i]) glEnable(caps[i]);
            else glDisable(caps[i]);
        }
    }
    static void prepare() {
        glEnable(GL_BLEND);
        glEnable(GL_SCISSOR_TEST);
        for (std::size_t i = 2; i < caps.size(); ++i) glDisable(caps[i]);
        glBlendEquationSeparate(GL_FUNC_ADD, GL_FUNC_ADD);
        glBlendFuncSeparate(GL_ONE, GL_ONE_MINUS_SRC_ALPHA, GL_ONE, GL_ONE_MINUS_SRC_ALPHA);
        glColorMask(GL_TRUE, GL_TRUE, GL_TRUE, GL_TRUE);
    }
  private:
    static constexpr std::array<GLenum, 8> caps = {GL_BLEND, GL_SCISSOR_TEST, GL_DEPTH_TEST, GL_STENCIL_TEST,
        GL_CULL_FACE, GL_RASTERIZER_DISCARD, GL_SAMPLE_ALPHA_TO_COVERAGE, GL_SAMPLE_COVERAGE};
    std::array<GLboolean, caps.size()> enabled{};
    std::array<GLint, 4> scissor{};
    std::array<GLboolean, 4> colorMask{};
    GLint program = 0, vao = 0, buffer = 0;
    GLint srcRGB = 0, dstRGB = 0, srcAlpha = 0, dstAlpha = 0;
    GLint equationRGB = 0, equationAlpha = 0;
};

GLuint compile(GLenum type, const char* source) {
    const auto shader = glCreateShader(type);
    if (!shader) return 0;
    glShaderSource(shader, 1, &source, nullptr);
    glCompileShader(shader);
    GLint success = 0;
    glGetShaderiv(shader, GL_COMPILE_STATUS, &success);
    if (!success) {
        glDeleteShader(shader);
        return 0;
    }
    return shader;
}
bool finiteRange(float value, float low, float high) {
    return std::isfinite(value) && value >= low && value <= high;
}

void validateSnapshot(double width, double height, std::span<const Segment> segments,
                      std::span<const Mask> masks, std::size_t maskedPrefix, const Far& far) {
    if (!std::isfinite(width) || !std::isfinite(height) || width <= 0 || height <= 0 || width > 32768 || height > 32768 ||
        segments.size() > SEGMENT_LIMIT || masks.size() > MASK_LIMIT || maskedPrefix > segments.size())
        throw std::invalid_argument("invalid segment snapshot envelope");
    for (const auto& segment : segments) {
        for (std::size_t i = 0; i < 4; ++i)
            if (!finiteRange(segment[i], -131072, 131072)) throw std::invalid_argument("invalid segment coordinate");
        if (!finiteRange(segment[4], 0.01, 128)) throw std::invalid_argument("invalid segment width");
        for (std::size_t i = 5; i < 9; ++i)
            if (!finiteRange(segment[i], 0, 1)) throw std::invalid_argument("invalid segment color");
    }
    for (const auto& mask : masks) {
        if (!finiteRange(mask[0], -131072, 131072) || !finiteRange(mask[1], -131072, 131072) ||
            !finiteRange(mask[2], 0.01, 32768) || !finiteRange(mask[3], 0.01, 32768))
            throw std::invalid_argument("invalid segment mask");
    }
    if (far.count > FAR_LIMIT || !finiteRange(far.speed, 0, 2000) || !finiteRange(far.wind, -1000, 1000) ||
        !finiteRange(far.length, 0.01, 128) || !finiteRange(far.width, 0.01, 128) ||
        !finiteRange(far.opacity, 0, 1) || !finiteRange(far.time, 0, 1e9))
        throw std::invalid_argument("invalid GPU far descriptor");
}
}

#ifndef A_WEATHER_APP_SEGMENT_GL_TEST
struct SegmentGPU::Impl {
    GLuint program = 0, vao = 0, buffer = 0;
    GLint size = -1, projection = -1, prefix = -1, maskCount = -1, masks = -1;
    GLint mode = -1, farParameters = -1, farColor = -1, farTime = -1, farOpacity = -1;
    EGLDisplay display = EGL_NO_DISPLAY;
    EGLContext context = EGL_NO_CONTEXT;
    bool stopped = false, failed = false;
    std::string_view suppression;
    uint64_t calls = 0, segments = 0;

    void destroyCurrent() noexcept {
        if (program) glDeleteProgram(program);
        if (vao) glDeleteVertexArrays(1, &vao);
        if (buffer) glDeleteBuffers(1, &buffer);
        program = vao = buffer = 0;
    }
    bool init() {
        if (failed || stopped) return false;
        if (program) return true;
        display = eglGetCurrentDisplay();
        context = eglGetCurrentContext();
        const auto vertex = compile(GL_VERTEX_SHADER, VERTEX);
        const auto fragment = compile(GL_FRAGMENT_SHADER, FRAGMENT);
        if (vertex && fragment) {
            program = glCreateProgram();
            glAttachShader(program, vertex);
            glAttachShader(program, fragment);
            glLinkProgram(program);
            GLint linked = 0;
            glGetProgramiv(program, GL_LINK_STATUS, &linked);
            if (!linked) { glDeleteProgram(program); program = 0; }
        }
        if (vertex) glDeleteShader(vertex);
        if (fragment) glDeleteShader(fragment);
        if (!program) {
            suppression = "shader_failed";
            failed = true;
            return false;
        }
        size = glGetUniformLocation(program, "logical_size");
        projection = glGetUniformLocation(program, "projection");
        prefix = glGetUniformLocation(program, "masked_prefix");
        maskCount = glGetUniformLocation(program, "mask_count");
        masks = glGetUniformLocation(program, "masks[0]");
        mode = glGetUniformLocation(program, "mode");
        farParameters = glGetUniformLocation(program, "far_parameters");
        farColor = glGetUniformLocation(program, "far_color");
        farTime = glGetUniformLocation(program, "far_time");
        farOpacity = glGetUniformLocation(program, "far_opacity");
        glGenVertexArrays(1, &vao);
        glGenBuffers(1, &buffer);
        if (!vao || !buffer || size < 0 || projection < 0 || prefix < 0 || maskCount < 0 || masks < 0 ||
            mode < 0 || farParameters < 0 || farColor < 0 || farTime < 0 || farOpacity < 0) {
            destroyCurrent();
            suppression = "resource_failed";
            failed = true;
            return false;
        }
        glBindVertexArray(vao);
        glBindBuffer(GL_ARRAY_BUFFER, buffer);
        glBufferData(GL_ARRAY_BUFFER, SEGMENT_LIMIT * sizeof(Segment), nullptr, GL_STREAM_DRAW);
        glVertexAttribPointer(0, 4, GL_FLOAT, GL_FALSE, sizeof(Segment), nullptr);
        glVertexAttribPointer(1, 1, GL_FLOAT, GL_FALSE, sizeof(Segment), reinterpret_cast<void*>(4 * sizeof(float)));
        glVertexAttribPointer(2, 4, GL_FLOAT, GL_FALSE, sizeof(Segment), reinterpret_cast<void*>(5 * sizeof(float)));
        for (GLuint i = 0; i < 3; ++i) {
            glEnableVertexAttribArray(i);
            glVertexAttribDivisor(i, 1);
        }
        return true;
    }
};

SegmentGPU::SegmentGPU() : m_impl(std::make_unique<Impl>()) {}
SegmentGPU::~SegmentGPU() { shutdown(); }
std::string_view SegmentGPU::lastSuppression() const noexcept { return m_impl->suppression; }
uint64_t SegmentGPU::drawCalls() const noexcept { return m_impl->calls; }
uint64_t SegmentGPU::segmentsDrawn() const noexcept { return m_impl->segments; }

bool SegmentGPU::shutdown() noexcept {
    auto& gpu = *m_impl;
    gpu.stopped = true;
    if (!gpu.program && !gpu.vao && !gpu.buffer) return true;
    const auto oldDisplay = eglGetCurrentDisplay();
    const auto oldContext = eglGetCurrentContext();
    const auto oldDraw = eglGetCurrentSurface(EGL_DRAW);
    const auto oldRead = eglGetCurrentSurface(EGL_READ);
    const auto oldAPI = eglQueryAPI();
    const bool switchContext = oldDisplay != gpu.display || oldContext != gpu.context;
    if (switchContext) {
        if (!eglBindAPI(EGL_OPENGL_ES_API) || !eglMakeCurrent(gpu.display, EGL_NO_SURFACE, EGL_NO_SURFACE, gpu.context)) {
            eglBindAPI(oldAPI);
            gpu.suppression = "cleanup_context_unavailable";
            return false; // Lost/dead contexts reclaim their own GL objects.
        }
    }
    gpu.destroyCurrent();
    if (switchContext) {
        eglBindAPI(oldAPI);
        const bool restored = oldDisplay != EGL_NO_DISPLAY ?
            eglMakeCurrent(oldDisplay, oldDraw, oldRead, oldContext) :
            eglMakeCurrent(gpu.display, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
        if (!restored) {
            gpu.suppression = "cleanup_restore_failed";
            return false;
        }
    }
    return true;
}

SegmentPass::SegmentPass(std::shared_ptr<SegmentGPU> gpu, Vector2D logicalSize,
                         std::vector<Segment> segments, std::vector<Mask> masks,
                         std::size_t maskedPrefix, Far far) :
    m_gpu(std::move(gpu)), m_size(logicalSize), m_segments(std::move(segments)),
    m_masks(std::move(masks)), m_maskedPrefix(maskedPrefix), m_far(far) {
    static_assert(sizeof(Segment) == 9 * sizeof(float));
    if (!m_gpu) throw std::invalid_argument("missing segment GPU owner");
    validateSnapshot(m_size.x, m_size.y, m_segments, m_masks, m_maskedPrefix, m_far);
}

std::optional<CBox> SegmentPass::boundingBox() { return CBox{{0, 0}, m_size}; }

std::vector<UP<IPassElement>> SegmentPass::draw() try {
    auto& gpu = *m_gpu->m_impl;
    if (gpu.stopped) { gpu.suppression = "stopped"; return {}; }
    if (m_segments.empty() && m_far.count == 0) { gpu.suppression = {}; return {}; }
    if (!g_pHyprRenderer || g_pHyprRenderer->m_bRenderingSnapshot) { gpu.suppression = "renderer_unavailable"; return {}; }
    const auto& data = g_pHyprRenderer->renderData();
    const auto monitor = data.pMonitor.lock();
    // glBackend() is weak over the compositor's unique-owned backend, so it
    // cannot be promoted to shared ownership. The synchronous render callback
    // keeps the compositor/backend alive; retain the weak wrapper and use it
    // directly after checking validity.
    const auto backend = g_pHyprRenderer->glBackend();
    if (!monitor || !backend || !data.currentFB || eglGetCurrentContext() == EGL_NO_CONTEXT ||
        eglGetCurrentContext() != backend->m_eglContext || backend->m_eglContextVersion == Render::GL::CHyprOpenGLImpl::EGL_CONTEXT_GLES_2_0) {
        gpu.suppression = "context_unavailable"; return {};
    }
    if ((gpu.context != EGL_NO_CONTEXT && gpu.context != eglGetCurrentContext()) || monitor->isMirror() ||
        monitor->m_transform != WL_OUTPUT_TRANSFORM_NORMAL || data.projectionType != Render::RPT_MONITOR ||
        (data.renderModif.enabled && !data.renderModif.modifs.empty()) || data.mouseZoomFactor != 1.0f ||
        &g_pHyprRenderer->currentPass() != &g_pHyprRenderer->m_renderPass ||
        std::abs(monitor->m_size.x - m_size.x) > 0.01 || std::abs(monitor->m_size.y - m_size.y) > 0.01) {
        gpu.suppression = "unsupported_transform"; return {};
    }
    if (monitor->inHDR() || data.currentFB->getMirrorTexture()) {
        gpu.suppression = "unsupported_hdr_or_mirror_copy"; return {};
    }
    if (pixman_region32_n_rects(data.damage.pixman()) > 256) { gpu.suppression = "damage_limit"; return {}; }
    CRegion damage = data.damage.copy();
    damage.intersect(CBox{{0, 0}, monitor->m_transformedSize});
    if (!data.clipBox.empty()) damage.intersect(data.clipBox);
    if (damage.empty()) { gpu.suppression = "no_damage"; return {}; }

    StateGuard state;
    if (!gpu.init()) return {};
    StateGuard::prepare();
    auto converted = m_segments;
    for (auto& segment : converted) {
        const float alpha = segment[8];
        const auto color = g_pHyprRenderer->getConvertedColor(CHyprColor(segment[5] * alpha, segment[6] * alpha, segment[7] * alpha, alpha));
        if (!std::isfinite(color.r) || !std::isfinite(color.g) || !std::isfinite(color.b) || !std::isfinite(color.a)) {
            gpu.suppression = "nonfinite_color"; return {};
        }
        segment[5] = color.r;
        segment[6] = color.g;
        segment[7] = color.b;
        segment[8] = color.a;
    }
    const auto projection = g_pHyprRenderer->projectBoxToTarget(CBox{{0, 0}, monitor->m_transformedSize}).transpose().getMatrix();
    glUseProgram(gpu.program);
    glUniform2f(gpu.size, m_size.x, m_size.y);
    glUniformMatrix3fv(gpu.projection, 1, GL_FALSE, projection.data());
    glUniform1i(gpu.prefix, static_cast<GLint>(m_maskedPrefix));
    glUniform1i(gpu.maskCount, static_cast<GLint>(m_masks.size()));
    if (!m_masks.empty()) glUniform4fv(gpu.masks, m_masks.size(), m_masks.front().data());
    glBindVertexArray(gpu.vao);
    glBindBuffer(GL_ARRAY_BUFFER, gpu.buffer);
    if (!converted.empty()) glBufferSubData(GL_ARRAY_BUFFER, 0, converted.size() * sizeof(Segment), converted.data());
    const auto farColor = g_pHyprRenderer->getConvertedColor(CHyprColor(0.62f, 0.83f, 1.0f, 1.0f));
    glUniform4f(gpu.farParameters, m_far.speed, m_far.wind, m_far.length, m_far.width);
    glUniform3f(gpu.farColor, farColor.r, farColor.g, farColor.b);
    glUniform1f(gpu.farTime, m_far.time);
    glUniform1f(gpu.farOpacity, m_far.opacity);
    damage.forEachRect([&](const auto& rect) {
        glScissor(rect.x1, rect.y1, rect.x2 - rect.x1, rect.y2 - rect.y1);
        if (m_far.count) {
            glUniform1i(gpu.mode, 0);
            glDrawArraysInstanced(GL_TRIANGLES, 0, 6, m_far.count);
            ++gpu.calls;
        }
        if (!converted.empty()) {
            glUniform1i(gpu.mode, 1);
            glDrawArraysInstanced(GL_TRIANGLES, 0, 6, converted.size());
            ++gpu.calls;
        }
    });
    gpu.segments += converted.size();
    gpu.suppression = {};
    return {};
} catch (...) {
    m_gpu->m_impl->suppression = "draw_failed";
    return {};
}
#endif
}
