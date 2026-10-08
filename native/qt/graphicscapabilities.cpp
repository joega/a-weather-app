#include "graphicscapabilities.h"
#include <QOpenGLContext>
#include <QFile>
#include <QOpenGLFunctions>
#include <QSGRendererInterface>
#include <atomic>
#include <memory>

bool GraphicsCapabilities::softwareRenderer(const QString& renderer) {
    const auto name = renderer.toLower();
    return name.isEmpty() || name.contains("llvmpipe") || name.contains("softpipe") ||
           name.contains("swrast") || name.contains("software") || name.contains("swiftshader") ||
           name.contains("lavapipe") || name.contains("basic render") ||
           name.contains("gdi generic");
}
bool GraphicsCapabilities::supportedFormat(const QSurfaceFormat& format, bool es) {
    // Match atmosphere.frag.qsb's GLSL 300 es / 330 packages. Compatibility
    // contexts select legacy GLSL variants that this package does not contain.
    return es ? format.majorVersion() >= 3
              : format.profile() == QSurfaceFormat::CoreProfile &&
                    (format.majorVersion() > 3 ||
                     (format.majorVersion() == 3 && format.minorVersion() >= 3));
}
namespace {
bool pipelineUsable(QOpenGLContext* context) {
    QFile file(context->isOpenGLES() ? ":/ui/shaders/atmosphere.gles300.frag"
                                     : ":/ui/shaders/atmosphere.gl330.frag");
    if (!file.open(QIODevice::ReadOnly))
        return false;
    const auto fragment = file.readAll();
    const QByteArray vertex =
        (context->isOpenGLES() ? QByteArray("#version 300 es\nprecision highp float;\n")
                               : QByteArray("#version 330\n")) +
        "in vec4 position; out vec2 qt_TexCoord0; void main() { gl_Position = position; "
        "qt_TexCoord0 = position.xy; }";
    auto* gl = context->functions();
    const auto vert = gl->glCreateShader(GL_VERTEX_SHADER),
               frag = gl->glCreateShader(GL_FRAGMENT_SHADER);
    auto compile = [gl](GLuint shader, const QByteArray& source) {
        const char* bytes = source.constData();
        gl->glShaderSource(shader, 1, &bytes, nullptr);
        gl->glCompileShader(shader);
        GLint status = GL_FALSE;
        gl->glGetShaderiv(shader, GL_COMPILE_STATUS, &status);
        return status == GL_TRUE;
    };
    bool usable = compile(vert, vertex) && compile(frag, fragment);
    if (usable) {
        const auto program = gl->glCreateProgram();
        gl->glAttachShader(program, vert);
        gl->glAttachShader(program, frag);
        gl->glLinkProgram(program);
        GLint status = GL_FALSE;
        gl->glGetProgramiv(program, GL_LINK_STATUS, &status);
        usable = status == GL_TRUE;
        gl->glDeleteProgram(program);
    }
    gl->glDeleteShader(vert);
    gl->glDeleteShader(frag);
    return usable;
}
} // namespace
void GraphicsCapabilities::observe(QQuickWindow* window) {
    auto inspected = std::make_shared<std::atomic_bool>(false);
    connect(
        window, &QQuickWindow::beforeRendering, this,
        [this, window, inspected] {
            if (inspected->exchange(true))
                return;
            bool usable = false;
            bool attempted = false;
            QString name = "Unsupported graphics backend";
            if (window->rendererInterface()->graphicsApi() == QSGRendererInterface::OpenGL) {
                if (auto* context = QOpenGLContext::currentContext()) {
                    const auto* bytes = context->functions()->glGetString(GL_RENDERER);
                    name = bytes ? QString::fromLatin1(reinterpret_cast<const char*>(bytes))
                                 : QString();
                    attempted = !softwareRenderer(name) &&
                                supportedFormat(context->format(), context->isOpenGLES());
                    usable = attempted && pipelineUsable(context);
                }
            }
            QMetaObject::invokeMethod(
                this,
                [this, usable, name, attempted] {
                    if (attempted)
                        ++attempts;
                    supported = usable;
                    identity = name;
                    emit changed();
                },
                Qt::QueuedConnection);
        },
        Qt::DirectConnection);
    connect(
        window, &QQuickWindow::sceneGraphInvalidated, this,
        [this, inspected] {
            inspected->store(false);
            QMetaObject::invokeMethod(
                this,
                [this] {
                    supported = false;
                    emit changed();
                },
                Qt::QueuedConnection);
        },
        Qt::DirectConnection);
}
