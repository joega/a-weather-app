#define _POSIX_C_SOURCE 200809L
#include <gtk/gtk.h>
#include <gdk/wayland/gdkwayland.h>
#include <gtk4-layer-shell.h>
#include <json-glib/json-glib.h>
#include <epoxy/gl.h>
#include <glib-unix.h>
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <signal.h>
#include <stdint.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
#include "sky_shader.h"
#include "dynamics.h"
#include "policy.h"

#define WEATHER_LIMIT (2 * 1024 * 1024)
typedef struct {
  GtkApplication *app;
  GtkWindow *window;
  GtkGLArea *area;
  const char *weather_path;
  const char *policy_path, *policy_session, *output_connector;
  GdkMonitor *selected_monitor;
  SkyPolicy policy;
  gboolean policy_received, render_allowed, presented, clear_flash;
  char policy_reason[65];
  Weather weather, target;
  guint monitor, duration, fps, tick, poll, policy_poll, expiry, term, interrupt, renew;
  guint frames, updates, errors;
  gboolean failed, input_empty, reduced_override, renewable_lease;
  gint64 started, last_weather_step, lease_deadline;
  double cloud_offset, visual_time;
  double last_flash;
  guint policy_updates, policy_errors, paused_callbacks, flash_clears;
  SkySchedule schedule;
  GLuint sky, upscale, vao, fbo, texture;
  int buffer_width, buffer_height;
} State;

static gboolean invalid(GError **error, const char *text) {
  g_set_error_literal(error, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, text);
  return FALSE;
}
static gboolean number(JsonObject *object, const char *key, double low, double high, double *out) {
  JsonNode *node = json_object_get_member(object, key);
  if (!node || !JSON_NODE_HOLDS_VALUE(node)) return FALSE;
  GType type = json_node_get_value_type(node);
  if (type != G_TYPE_DOUBLE && type != G_TYPE_INT64) return FALSE;
  *out = json_node_get_double(node);
  return isfinite(*out) && *out >= low && *out <= high;
}
static gboolean boolean(JsonObject *object, const char *key, gboolean *out) {
  JsonNode *node = json_object_get_member(object, key);
  if (!node || !JSON_NODE_HOLDS_VALUE(node) || json_node_get_value_type(node) != G_TYPE_BOOLEAN) return FALSE;
  *out = json_node_get_boolean(node);
  return TRUE;
}
static gboolean bounded_json(const char *text, gsize length) {
  if (!length || length > WEATHER_LIMIT || memchr(text, '\0', length)) return FALSE;
  guint depth = 0;
  gboolean quoted = FALSE, escaped = FALSE;
  for (gsize i = 0; i < length; ++i) {
    char c = text[i];
    if (quoted) {
      if (escaped) escaped = FALSE;
      else if (c == '\\') escaped = TRUE;
      else if (c == '"') quoted = FALSE;
    } else if (c == '"') quoted = TRUE;
    else if (c == '{' || c == '[') { if (++depth > 64) return FALSE; }
    else if (c == '}' || c == ']') { if (!depth) return FALSE; --depth; }
  }
  return !quoted && !depth;
}
static gboolean parse_weather(const char *text, gsize length, Weather *out, GError **error) {
  if (!bounded_json(text, length)) return invalid(error, "weather size, NUL or nesting limit");
  g_autoptr(JsonParser) parser = weather_json_new();
  if (!weather_json_load(parser, text, length, error)) return FALSE;
  JsonNode *root = json_parser_get_root(parser);
  if (!root || !JSON_NODE_HOLDS_OBJECT(root)) return invalid(error, "weather must be an object");
  JsonObject *object = json_node_get_object(root);
  JsonNode *selected = json_object_get_member(object, "selected_at");
  if (!selected || !JSON_NODE_HOLDS_VALUE(selected) || json_node_get_value_type(selected) != G_TYPE_STRING)
    return invalid(error, "selected_at ISO timestamp required");
  g_autoptr(GDateTime) selected_time = g_date_time_new_from_iso8601(json_node_get_string(selected), NULL);
  if (!selected_time) return invalid(error, "selected_at must include a timezone");
  double age = g_get_real_time() / 1000000.0 - g_date_time_to_unix(selected_time) - g_date_time_get_microsecond(selected_time) / 1000000.0;
  if (age < -300 || age > 10) return invalid(error, "selected weather is stale or future dated");
  JsonNode *version = json_object_get_member(object, "schema_version");
  JsonNode *effects_node = json_object_get_member(object, "effects");
  if (!version || !JSON_NODE_HOLDS_VALUE(version) || json_node_get_value_type(version) != G_TYPE_INT64 ||
      json_node_get_int(version) != 1 || !effects_node || !JSON_NODE_HOLDS_OBJECT(effects_node))
    return invalid(error, "schema_version 1 and effects object required");
  JsonObject *effects = json_node_get_object(effects_node);
  Weather candidate = {0};
  if (!number(effects, "sun_elevation", -90, 90, &candidate.elevation) ||
      !number(effects, "sun_azimuth", 0, 360, &candidate.azimuth) ||
      !number(effects, "cloud_cover", 0, 1, &candidate.clouds) ||
      !number(effects, "fog_density", 0, 1, &candidate.fog) ||
      !number(effects, "wind_x", -500, 500, &candidate.wind) ||
      !boolean(effects, "lightning_enabled", &candidate.lightning) ||
      !boolean(effects, "reduced_motion", &candidate.reduced) ||
      !boolean(effects, "thunderstorm", &candidate.storm))
    return invalid(error, "invalid or missing atmosphere effects");
  *out = candidate; // Atomic last-good replacement, never partial mutation.
  return TRUE;
}
static gboolean read_json_file(const char *path, gsize limit, char **out, gsize *out_length, GError **error) {
  int fd = open(path, O_RDONLY | O_NONBLOCK | O_NOFOLLOW | O_CLOEXEC);
  if (fd < 0) { g_set_error(error, G_IO_ERROR, g_io_error_from_errno(errno), "file open: %s", g_strerror(errno)); return FALSE; }
  struct stat info;
  if (fstat(fd, &info) || !S_ISREG(info.st_mode) || info.st_uid != geteuid() ||
      info.st_nlink != 1 || (info.st_mode & 0022) || info.st_size < 1 || (guint64)info.st_size > limit) {
    close(fd); return invalid(error, "input must be an owned, non-writable-by-others bounded regular file");
  }
  // Atomic publishers replace the inode, so a checked file should not grow.
  // Allocate for its actual size, plus one byte to detect concurrent growth,
  // rather than reserving the maximum weather payload on every heartbeat.
  gsize expected = (gsize)info.st_size;
  g_autofree char *text = g_malloc(expected + 1);
  gsize length = 0;
  while (length < expected + 1) {
    ssize_t got = read(fd, text + length, expected + 1 - length);
    if (got == 0) break;
    if (got < 0 && errno == EINTR) continue;
    if (got < 0) { close(fd); return invalid(error, "weather read failed"); }
    length += (gsize)got;
  }
  close(fd);
  if (length > expected) return invalid(error, "input grew during read");
  text[length] = '\0';
  *out = g_steal_pointer(&text); *out_length = length;
  return TRUE;
}
static gboolean read_weather(const char *path, Weather *out, GError **error) {
  g_autofree char *text = NULL; gsize length = 0;
  if (!read_json_file(path, WEATHER_LIMIT, &text, &length, error)) return FALSE;
  return parse_weather(text, length, out, error);
}
static gboolean read_policy(const char *path, const char *session, const char *output, gint64 sequence,
                              SkyPolicy *out, GError **error) {
  g_autofree char *text = NULL; gsize length = 0;
  if (!read_json_file(path, POLICY_LIMIT, &text, &length, error)) return FALSE;
  return policy_parse(text, length, session, output, sequence, g_get_real_time() / 1000.0, out, error);
}
static double pulse(double time, double start, double duration, double peak) {
  double phase = (time - start) / duration;
  return phase > 0 && phase < 1 ? peak * pow(sin(G_PI * phase), 2) : 0;
}
static double flash(double time, const Weather *weather) {
  if (!isfinite(time) || time < 0 || !weather->storm || !weather->lightning || weather->reduced) return 0;
  uint32_t mixed = (uint32_t)floor(time / 24) * UINT32_C(1664525) + UINT32_C(1013904223);
  double start = 4 + (mixed % 14000) / 1000.0;
  double local = fmod(time, 24);
  return pulse(local, start, .18, .28) + ((mixed & 1) ? pulse(local, start + .8, .12, .09) : 0);
}
static gboolean preset(const char *name, Weather *weather) {
  *weather = (Weather){.elevation = 35, .azimuth = 180, .clouds = .5, .wind = 35};
  if (!strcmp(name, "clear")) weather->clouds = .08;
  else if (!strcmp(name, "cloud")) weather->clouds = .8;
  else if (!strcmp(name, "dusk")) { weather->elevation = -1; weather->clouds = .42; }
  else if (!strcmp(name, "night")) { weather->elevation = -25; weather->clouds = .2; }
  else if (!strcmp(name, "storm")) { weather->clouds = 1; weather->elevation = 12; weather->wind = 120; weather->storm = TRUE; }
  else if (!strcmp(name, "fog")) { weather->fog = .85; weather->clouds = .65; }
  else return FALSE;
  return TRUE;
}

static const char *vertex_source = "#version 300 es\nprecision highp float;\nout vec2 v_uv;\n"
  "void main(){vec2 p=vec2(float((gl_VertexID<<1)&2),float(gl_VertexID&2));v_uv=p;gl_Position=vec4(p*2.-1.,0.,1.);}";
static const char *upscale_source = "#version 300 es\nprecision highp float;\nin vec2 v_uv;\nout vec4 result;uniform sampler2D sky_texture;"
  "void main(){result=texture(sky_texture,v_uv);}";
static GLuint compile(GLenum type, const char *source) {
  GLuint shader = glCreateShader(type);
  glShaderSource(shader, 1, &source, NULL); glCompileShader(shader);
  GLint ok = 0; glGetShaderiv(shader, GL_COMPILE_STATUS, &ok);
  if (!ok) { char log[4096]; glGetShaderInfoLog(shader, sizeof log, NULL, log); g_printerr("atmosphere shader: %s\n", log); glDeleteShader(shader); return 0; }
  return shader;
}
static GLuint program(const char *fragment) {
  GLuint vertex = compile(GL_VERTEX_SHADER, vertex_source), pixel = compile(GL_FRAGMENT_SHADER, fragment);
  GLuint result = 0;
  if (vertex && pixel) {
    result = glCreateProgram(); glAttachShader(result, vertex); glAttachShader(result, pixel); glLinkProgram(result);
    GLint ok = 0; glGetProgramiv(result, GL_LINK_STATUS, &ok);
    if (!ok) { char log[4096]; glGetProgramInfoLog(result, sizeof log, NULL, log); g_printerr("atmosphere program: %s\n", log); glDeleteProgram(result); result = 0; }
  }
  if (vertex) glDeleteShader(vertex);
  if (pixel) glDeleteShader(pixel);
  return result;
}
static void gl_release(GtkGLArea *area, gpointer data) {
  State *state = data;
  gtk_gl_area_make_current(area);
  if (!gtk_gl_area_get_error(area)) {
    if (state->sky) glDeleteProgram(state->sky);
    if (state->upscale) glDeleteProgram(state->upscale);
    if (state->vao) glDeleteVertexArrays(1, &state->vao);
    if (state->fbo) glDeleteFramebuffers(1, &state->fbo);
    if (state->texture) glDeleteTextures(1, &state->texture);
  }
  state->sky = state->upscale = state->vao = state->fbo = state->texture = 0;
  state->buffer_width = state->buffer_height = 0;
}
static void gl_init(GtkGLArea *area, gpointer data) {
  State *state = data;
  gtk_gl_area_make_current(area);
  if (gtk_gl_area_get_error(area)) { state->failed = TRUE; g_application_quit(G_APPLICATION(state->app)); return; }
  state->sky = program(sky_fragment); state->upscale = program(upscale_source);
  glGenVertexArrays(1, &state->vao); glGenFramebuffers(1, &state->fbo); glGenTextures(1, &state->texture);
  if (!state->sky || !state->upscale || !state->vao || !state->fbo || !state->texture) {
    state->failed = TRUE; g_application_quit(G_APPLICATION(state->app));
  } else {
    /* Driver strings are external data: serialize them rather than interpolating JSON. */
    GskRenderer *renderer = gtk_native_get_renderer(GTK_NATIVE(state->window));
    g_autoptr(JsonBuilder) builder = json_builder_new();
    json_builder_begin_object(builder);
    const char *keys[] = {"component", "event", "api", "gl_renderer", "gl_vendor", "gl_version", "gsk_renderer"};
    const char *values[] = {"a-weather-app-atmosphere", "gl_ready", "GLES3",
      (const char *)glGetString(GL_RENDERER), (const char *)glGetString(GL_VENDOR),
      (const char *)glGetString(GL_VERSION), renderer ? G_OBJECT_TYPE_NAME(renderer) : NULL};
    for (guint i = 0; i < G_N_ELEMENTS(keys); ++i) {
      json_builder_set_member_name(builder, keys[i]);
      if (values[i]) json_builder_add_string_value(builder, values[i]);
      else json_builder_add_null_value(builder);
    }
    json_builder_end_object(builder);
    g_autoptr(JsonNode) root = json_builder_get_root(builder);
    g_autoptr(JsonGenerator) generator = json_generator_new();
    json_generator_set_root(generator, root);
    g_autofree char *text = json_generator_to_data(generator, NULL);
    g_print("%s\n", text);
  }
}
static void uniform(GLuint shader, const char *name, double value) {
  glUniform1f(glGetUniformLocation(shader, name), (float)value);
}
static void advance_weather(State *state, gint64 now) {
  if (state->policy_path && !state->render_allowed) { state->last_weather_step = now; return; }
  sky_advance(&state->weather, &state->target, (now - state->last_weather_step) / 1000000.0,
              &state->cloud_offset, &state->visual_time);
  state->last_weather_step = now;
}
static void policy_state(State *state, gboolean allowed, const char *reason);
static gboolean policy_fresh(State *state) {
  if (!state->policy_path) return TRUE;
  if (!state->selected_monitor || !gdk_monitor_is_valid(state->selected_monitor)) {
    policy_state(state, FALSE, "output_disconnected"); return FALSE;
  }
  double age = g_get_real_time() / 1000.0 - state->policy.generated_ms;
  if (!state->policy_received || age > POLICY_MAX_AGE_MS || age < -POLICY_MAX_FUTURE_MS) {
    policy_state(state, FALSE, "policy_stale"); return FALSE;
  }
  return state->render_allowed;
}
static gboolean render(GtkGLArea *area, GdkGLContext *context, gpointer data) {
  (void)context;
  State *state = data;
  gboolean allowed = policy_fresh(state);
  SkyPolicyDraw decision = policy_draw(allowed, state->clear_flash);
  if (decision == SKY_POLICY_SKIP) { ++state->paused_callbacks; return TRUE; }
  gboolean clearing = decision == SKY_POLICY_CLEAR_FLASH;
  if (gtk_gl_area_get_error(area) || !state->sky || !state->upscale || !state->input_empty) {
    state->failed = TRUE; g_application_quit(G_APPLICATION(state->app)); return TRUE;
  }
  int width = gtk_widget_get_width(GTK_WIDGET(area)), height = gtk_widget_get_height(GTK_WIDGET(area));
  if (width < 1 || height < 1) return TRUE;
  GLint original_fbo, original_viewport[4];
  glGetIntegerv(GL_FRAMEBUFFER_BINDING, &original_fbo); glGetIntegerv(GL_VIEWPORT, original_viewport);
  double scale = MIN(.5, MIN(1280.0 / width, 720.0 / height));
  int bw = MAX(1, (int)floor(width * scale)), bh = MAX(1, (int)floor(height * scale));
  glActiveTexture(GL_TEXTURE0); glBindTexture(GL_TEXTURE_2D, state->texture);
  if (bw != state->buffer_width || bh != state->buffer_height) {
    glTexImage2D(GL_TEXTURE_2D, 0, GL_RGBA8, bw, bh, 0, GL_RGBA, GL_UNSIGNED_BYTE, NULL);
    glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MIN_FILTER, GL_LINEAR); glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_MAG_FILTER, GL_LINEAR);
    glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_S, GL_CLAMP_TO_EDGE); glTexParameteri(GL_TEXTURE_2D, GL_TEXTURE_WRAP_T, GL_CLAMP_TO_EDGE);
    state->buffer_width = bw; state->buffer_height = bh;
    g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"buffer\",\"width\":%d,\"height\":%d}\n", bw, bh);
  }
  glBindFramebuffer(GL_FRAMEBUFFER, state->fbo);
  glFramebufferTexture2D(GL_FRAMEBUFFER, GL_COLOR_ATTACHMENT0, GL_TEXTURE_2D, state->texture, 0);
  if (glCheckFramebufferStatus(GL_FRAMEBUFFER) != GL_FRAMEBUFFER_COMPLETE) {
    glBindFramebuffer(GL_FRAMEBUFFER, original_fbo); state->failed = TRUE;
    g_application_quit(G_APPLICATION(state->app)); return TRUE;
  }
  glDisable(GL_BLEND); glDisable(GL_DEPTH_TEST); glDisable(GL_SCISSOR_TEST); glDisable(GL_CULL_FACE);
  glColorMask(GL_TRUE, GL_TRUE, GL_TRUE, GL_TRUE);
  glBindVertexArray(state->vao); glViewport(0, 0, bw, bh); glUseProgram(state->sky);
  double time = (g_get_monotonic_time() - state->started) / 1000000.0;
  gint64 now = g_get_monotonic_time();
  if (!clearing) advance_weather(state, now);
  Weather *weather = &state->weather;
  uniform(state->sky, "scene_time", state->visual_time); uniform(state->sky, "sun_elevation", weather->elevation);
  uniform(state->sky, "sun_azimuth", weather->azimuth); uniform(state->sky, "cloud_cover", weather->clouds);
  uniform(state->sky, "fog_density", weather->fog); uniform(state->sky, "cloud_offset", state->cloud_offset);
  state->last_flash = clearing ? 0 : flash(time, weather);
  uniform(state->sky, "lightning", state->last_flash); uniform(state->sky, "aspect_ratio", (double)width / height);
  glUniform1i(glGetUniformLocation(state->sky, "reduced_motion"), weather->reduced);
  glDrawArrays(GL_TRIANGLES, 0, 3);
  glBindFramebuffer(GL_FRAMEBUFFER, original_fbo); glViewport(original_viewport[0], original_viewport[1], original_viewport[2], original_viewport[3]);
  glUseProgram(state->upscale); glUniform1i(glGetUniformLocation(state->upscale, "sky_texture"), 0);
  glDrawArrays(GL_TRIANGLES, 0, 3);
  glBindVertexArray(0); glBindTexture(GL_TEXTURE_2D, 0); glUseProgram(0);
  if (glGetError() != GL_NO_ERROR) { state->failed = TRUE; g_printerr("atmosphere GL render error\n"); g_application_quit(G_APPLICATION(state->app)); return TRUE; }
  if (!state->frames) g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"render\",\"status\":\"ok\",\"input_region_empty\":true,\"surface_logical\":[%d,%d]}\n", width, height);
  ++state->frames;
  if (clearing) {
    state->clear_flash = FALSE; ++state->flash_clears;
    g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"policy_flash_clear\",\"frames\":%u,\"lightning\":0}\n", state->frames);
  }
  return TRUE;
}
static void empty_input(GtkWidget *widget, gpointer data) {
  State *state = data;
  GtkNative *native = gtk_widget_get_native(widget);
  if (!native) return;
  GdkSurface *surface = gtk_native_get_surface(native);
  if (!surface) return;
  cairo_region_t *region = cairo_region_create();
  gdk_surface_set_input_region(surface, region); cairo_region_destroy(region);
  if (!state->input_empty) g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"input_region\",\"state\":\"empty\"}\n");
  state->input_empty = TRUE;
}
static void resized(GtkGLArea *area, int width, int height, gpointer data) {
  (void)width; (void)height;
  empty_input(GTK_WIDGET(area), data);
}
static void mapped(GtkWidget *widget, gpointer data) {
  empty_input(widget, data);
  g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"mapped\",\"surface_logical\":[%d,%d]}\n",
          gtk_widget_get_width(widget), gtk_widget_get_height(widget));
}
static gboolean tick(gpointer data);
static void arm_tick(State *state) {
  if (!state->render_allowed) return;
  state->tick = g_timeout_add(sky_delay_ms(&state->schedule, g_get_monotonic_time()), tick, state);
}
static void policy_state(State *state, gboolean allowed, const char *reason) {
  gboolean changed = state->render_allowed != allowed || strcmp(state->policy_reason, reason);
  gboolean was_allowed = state->render_allowed;
  state->render_allowed = allowed;
  g_strlcpy(state->policy_reason, reason, sizeof state->policy_reason);
  if (!allowed) {
    if (state->tick) { g_source_remove(state->tick); state->tick = 0; }
    state->weather.lightning = FALSE;
    if (policy_clear_required(was_allowed, allowed, state->presented, state->last_flash)) {
      state->clear_flash = TRUE;
      gtk_gl_area_queue_render(state->area);
    }
  } else if (!was_allowed) {
    state->last_weather_step = g_get_monotonic_time();
    sky_schedule_reset(&state->schedule, state->last_weather_step, state->fps, state->weather.reduced);
    arm_tick(state);
    if (!state->presented) { state->presented = TRUE; gtk_window_present(state->window); }
  }
  if (changed) g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"policy\",\"render_allowed\":%s,\"reason\":\"%s\",\"frames\":%u,\"visual_time\":%.6f,\"cloud_offset\":%.6f}\n",
                       allowed ? "true" : "false", reason, state->frames, state->visual_time, state->cloud_offset);
}
static gboolean connector_matches(State *state) {
  if (!state->selected_monitor || !gdk_monitor_is_valid(state->selected_monitor)) return FALSE;
  const char *selected = gdk_monitor_get_connector(state->selected_monitor);
  if (!selected || strcmp(selected, state->output_connector)) return FALSE;
  GListModel *monitors = gdk_display_get_monitors(gdk_monitor_get_display(state->selected_monitor));
  guint matches = 0, count = g_list_model_get_n_items(monitors);
  if (count > 64) return FALSE;
  for (guint i = 0; i < count; ++i) {
    g_autoptr(GdkMonitor) monitor = g_list_model_get_item(monitors, i);
    const char *connector = gdk_monitor_get_connector(monitor);
    if (gdk_monitor_is_valid(monitor) && connector && !strcmp(connector, state->output_connector)) ++matches;
  }
  return matches == 1;
}
static gboolean poll_policy(gpointer data) {
  State *state = data;
  SkyPolicy candidate; g_autoptr(GError) error = NULL;
  if (!connector_matches(state)) {
    state->policy_received = FALSE;
    policy_state(state, FALSE, "output_mismatch_or_disconnected");
  } else if (!read_policy(state->policy_path, state->policy_session, state->output_connector,
                          state->policy_received ? state->policy.sequence : MAX((gint64)0, state->policy.sequence), &candidate, &error)) {
    state->policy_received = FALSE; ++state->policy_errors;
    policy_state(state, FALSE, "policy_error");
    g_printerr("atmosphere policy: %s\n", error ? error->message : "unknown error");
  } else {
    state->policy = candidate; state->policy_received = TRUE; ++state->policy_updates;
    policy_state(state, candidate.allowed, candidate.reason);
  }
  g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"policy_heartbeat\",\"render_allowed\":%s,\"sequence\":%" G_GINT64_FORMAT ",\"frames\":%u,\"updates\":%u,\"errors\":%u,\"flash_clears\":%u}\n",
          state->render_allowed ? "true" : "false", state->policy.sequence, state->frames, state->policy_updates, state->policy_errors, state->flash_clears);
  return G_SOURCE_CONTINUE;
}
static gboolean tick(gpointer data) {
  State *state = data;
  state->tick = 0;
  if (!policy_fresh(state)) return G_SOURCE_REMOVE;
  gint64 now = g_get_monotonic_time();
  if (sky_schedule_due(&state->schedule, now)) {
    gtk_gl_area_queue_render(state->area);
  }
  arm_tick(state);
  return G_SOURCE_REMOVE;
}
static gboolean poll_weather(gpointer data) {
  State *state = data;
  g_autoptr(GError) error = NULL;
  Weather next;
  if (read_weather(state->weather_path, &next, &error)) {
    next.reduced = next.reduced || state->reduced_override;
    if (next.reduced) next.lightning = FALSE;
    advance_weather(state, g_get_monotonic_time());
    gboolean changed_cadence = state->weather.reduced != next.reduced;
    state->target = next;
    sky_safety(&state->weather, &state->target);
    if (!state->render_allowed) state->weather.lightning = FALSE;
    if (changed_cadence && state->tick) {
      g_source_remove(state->tick); state->tick = 0;
      sky_schedule_reset(&state->schedule, g_get_monotonic_time(), state->fps, next.reduced);
      arm_tick(state);
    }
    ++state->updates;
    g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"weather\",\"status\":\"ok\",\"updates\":%u,\"lightning_enabled\":%s,\"reduced_motion\":%s}\n",
            state->updates, next.lightning ? "true" : "false", next.reduced ? "true" : "false");
  } else {
    state->weather.lightning = FALSE;
    state->target.lightning = FALSE;
    ++state->errors;
    // Reason is separately printed, avoiding unescaped external text in JSON.
    g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"weather\",\"status\":\"error\",\"retained_last_good\":true,\"errors\":%u}\n", state->errors);
    g_printerr("atmosphere weather: %s\n", error ? error->message : "unknown error");
  }
  return G_SOURCE_CONTINUE;
}
static gboolean stop(gpointer data) {
  State *state = data;
  g_application_quit(G_APPLICATION(state->app)); return G_SOURCE_REMOVE;
}
static gboolean renew_lease(gpointer data) {
  State *state = data;
  SkyPolicy candidate; g_autoptr(GError) error = NULL;
  // A signal cannot revive an expired process or bypass the policy selector,
  // freshness and output checks. Suppressed but valid policy may keep its owner
  // alive; rendering remains governed by poll_policy.
  if (!state->renewable_lease || !state->started || g_get_monotonic_time() >= state->lease_deadline)
    return G_SOURCE_CONTINUE;
  if (!connector_matches(state) || !read_policy(state->policy_path, state->policy_session,
      state->output_connector, MAX((gint64)0, state->policy.sequence), &candidate, &error)) {
    state->policy_received = FALSE;
    policy_state(state, FALSE, "lease_policy_error");
    return G_SOURCE_CONTINUE;
  }
  if (g_get_monotonic_time() >= state->lease_deadline) return G_SOURCE_CONTINUE;
  if (state->expiry) g_source_remove(state->expiry);
  state->lease_deadline = g_get_monotonic_time() + (gint64)state->duration * G_USEC_PER_SEC;
  state->expiry = g_timeout_add_seconds(state->duration, stop, state);
  return G_SOURCE_CONTINUE;
}
static void activate(GtkApplication *app, gpointer data) {
  State *state = data;
  GdkDisplay *display = gdk_display_get_default();
  if (!GDK_IS_WAYLAND_DISPLAY(display) || !gtk_layer_is_supported()) {
    state->failed = TRUE; g_printerr("atmosphere requires Wayland layer-shell\n"); g_application_quit(G_APPLICATION(app)); return;
  }
  GListModel *monitors = gdk_display_get_monitors(display);
  if (state->monitor >= g_list_model_get_n_items(monitors)) {
    state->failed = TRUE; g_printerr("atmosphere monitor index unavailable\n"); g_application_quit(G_APPLICATION(app)); return;
  }
  g_autoptr(GdkMonitor) monitor = g_list_model_get_item(monitors, state->monitor);
  state->selected_monitor = g_object_ref(monitor);
  state->window = GTK_WINDOW(gtk_application_window_new(app));
  gtk_window_set_decorated(state->window, FALSE); gtk_widget_set_focusable(GTK_WIDGET(state->window), FALSE);
  gtk_layer_init_for_window(state->window); gtk_layer_set_namespace(state->window, "a-weather-app-atmosphere");
  gtk_layer_set_layer(state->window, GTK_LAYER_SHELL_LAYER_BACKGROUND); gtk_layer_set_monitor(state->window, monitor);
  for (int edge = 0; edge < GTK_LAYER_SHELL_EDGE_ENTRY_NUMBER; ++edge) gtk_layer_set_anchor(state->window, edge, TRUE);
  gtk_layer_set_exclusive_zone(state->window, -1); gtk_layer_set_keyboard_mode(state->window, GTK_LAYER_SHELL_KEYBOARD_MODE_NONE);
  state->area = GTK_GL_AREA(gtk_gl_area_new());
  gtk_gl_area_set_required_version(state->area, 3, 0); gtk_gl_area_set_allowed_apis(state->area, GDK_GL_API_GLES);
  gtk_gl_area_set_auto_render(state->area, FALSE); gtk_widget_set_focusable(GTK_WIDGET(state->area), FALSE);
  gtk_window_set_child(state->window, GTK_WIDGET(state->area));
  g_signal_connect(state->window, "realize", G_CALLBACK(empty_input), state);
  g_signal_connect(state->window, "map", G_CALLBACK(mapped), state);
  g_signal_connect(state->area, "resize", G_CALLBACK(resized), state);
  g_signal_connect(state->area, "realize", G_CALLBACK(gl_init), state);
  g_signal_connect(state->area, "unrealize", G_CALLBACK(gl_release), state);
  g_signal_connect(state->area, "render", G_CALLBACK(render), state);
  state->started = g_get_monotonic_time();
  state->lease_deadline = state->started + (gint64)state->duration * G_USEC_PER_SEC;
  state->last_weather_step = state->started;
  if (state->weather_path) { poll_weather(state); state->poll = g_timeout_add_seconds(1, poll_weather, state); }
  sky_schedule_reset(&state->schedule, state->started, state->fps, state->weather.reduced);
  state->expiry = g_timeout_add_seconds(state->duration, stop, state);
  g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"start\",\"layer\":\"background\",\"keyboard\":\"none\",\"exclusive_zone\":-1,\"monitor_index\":%u,\"fps\":%u,\"duration\":%u}\n", state->monitor, state->fps, state->duration);
  if (state->policy_path) {
    policy_state(state, FALSE, "policy_initial");
    poll_policy(state); state->policy_poll = g_timeout_add(500, poll_policy, state);
  } else {
    state->render_allowed = TRUE; arm_tick(state); state->presented = TRUE;
    gtk_window_present(state->window);
  }
}
static void default_gsk_renderer(void) {
  /* Process-local only; even an explicitly empty override belongs to the caller. */
  if (g_getenv("GSK_RENDERER") == NULL) g_setenv("GSK_RENDERER", "gl", FALSE);
}

static gboolean renderer_default_test(void) {
  g_autofree char *saved = g_strdup(g_getenv("GSK_RENDERER"));
  g_unsetenv("GSK_RENDERER");
  default_gsk_renderer();
  gboolean ok = g_strcmp0(g_getenv("GSK_RENDERER"), "gl") == 0;
  const char *overrides[] = {"vulkan", "gl", ""};
  for (guint i = 0; i < G_N_ELEMENTS(overrides); ++i) {
    g_setenv("GSK_RENDERER", overrides[i], TRUE);
    default_gsk_renderer();
    ok = ok && g_strcmp0(g_getenv("GSK_RENDERER"), overrides[i]) == 0;
  }
  if (saved) g_setenv("GSK_RENDERER", saved, TRUE);
  else g_unsetenv("GSK_RENDERER");
  return ok;
}

static gboolean self_test(void) {
  if (!renderer_default_test()) return FALSE;
  g_autoptr(GDateTime) now = g_date_time_new_now_utc();
  g_autofree char *timestamp = g_date_time_format_iso8601(now);
  g_autofree char *valid = g_strdup_printf("{\"schema_version\":1,\"selected_at\":\"%s\",\"effects\":{\"sun_elevation\":35,\"sun_azimuth\":180,\"cloud_cover\":0.5,\"fog_density\":0,\"wind_x\":35,\"lightning_enabled\":false,\"reduced_motion\":false,\"thunderstorm\":true}}", timestamp);
  Weather weather;
  g_autoptr(GError) error = NULL;
  if (!parse_weather(valid, strlen(valid), &weather, &error) || weather.lightning || weather.clouds != .5) return FALSE;
  const char *bad[] = {"{}", "[]", "null", "{\"schema_version\":true}", "{\"schema_version\":1.0,\"effects\":{}}", "{\"schema_version\":1,\"effects\":null}"};
  for (guint i = 0; i < G_N_ELEMENTS(bad); ++i) {
    Weather unchanged = weather; g_clear_error(&error);
    if (parse_weather(bad[i], strlen(bad[i]), &weather, &error) || memcmp(&weather, &unchanged, sizeof weather)) return FALSE;
  }
  const char *fields[] = {"sun_elevation", "sun_azimuth", "cloud_cover", "fog_density", "wind_x", "lightning_enabled", "reduced_motion", "thunderstorm"};
  const char *mutations[] = {"null", "true", "\"1\"", "1e999", "-9999", "9999", "[]", "{}"};
  for (guint i = 0; i < G_N_ELEMENTS(fields); ++i) for (guint j = 0; j < G_N_ELEMENTS(mutations); ++j) {
    g_autoptr(JsonParser) parser = json_parser_new(); json_parser_load_from_data(parser, valid, -1, NULL);
    JsonObject *effects = json_object_get_object_member(json_node_get_object(json_parser_get_root(parser)), "effects");
    g_autoptr(JsonParser) change = json_parser_new();
    if (!json_parser_load_from_data(change, mutations[j], -1, NULL)) continue;
    JsonNode *node = json_parser_get_root(change);
    if (i >= 5 && j == 1) continue; // Boolean true is a valid accessibility control.
    json_object_set_member(effects, fields[i], json_node_copy(node));
    g_autofree char *text = json_to_string(json_parser_get_root(parser), FALSE);
    Weather untouched = weather; g_clear_error(&error);
    if (parse_weather(text, strlen(text), &weather, &error) || memcmp(&weather, &untouched, sizeof weather)) return FALSE;
  }
  if (bounded_json(valid, WEATHER_LIMIT + 1) || bounded_json("{}\0x", 4)) return FALSE;
  char nested[131]; memset(nested, '[', 65); memset(nested + 65, ']', 65); nested[130] = 0;
  if (bounded_json(nested, 130)) return FALSE;
  const char *bad_times[] = {"2000-01-01T00:00:00Z", "2999-01-01T00:00:00Z", "not-a-date", "2026-09-26T12:00:00"};
  for (guint i = 0; i < G_N_ELEMENTS(bad_times); ++i) {
    g_autoptr(JsonParser) parser = json_parser_new(); json_parser_load_from_data(parser, valid, -1, NULL);
    json_object_set_string_member(json_node_get_object(json_parser_get_root(parser)), "selected_at", bad_times[i]);
    g_autofree char *text = json_to_string(json_parser_get_root(parser), FALSE);
    g_clear_error(&error);
    if (parse_weather(text, strlen(text), &weather, &error)) return FALSE;
  }
  weather.lightning = FALSE;
  for (int i = 0; i < 1000; ++i) if (flash(i / 10.0, &weather) != 0) return FALSE;
  weather.lightning = TRUE; weather.reduced = TRUE;
  if (flash(12.3, &weather) != 0) return FALSE;
  weather.reduced = FALSE;
  double peak = 0;
  for (int i = 0; i < 4800; ++i) { double value = flash(i / 100.0, &weather); if (value < 0 || value > .28) return FALSE; peak = MAX(peak, value); }
  if (peak < .2) return FALSE;
  g_print("ATMOSPHERE_SELF_TEST_PASS bounded typed weather, last-good retention, lightning gating\n");
  return TRUE;
}
int main(int argc, char **argv) {
  default_gsk_renderer();
  int duration = 30, monitor = -1, fps = 30;
  gboolean test = FALSE, lightning = FALSE, reduced = FALSE, renewable_lease = FALSE;
  char *path = NULL, *preset_name = NULL, *validate_path = NULL;
  char *policy_path = NULL, *policy_session = NULL, *output = NULL, *validate_policy = NULL;
  GOptionEntry entries[] = {
    {"monitor", 'm', 0, G_OPTION_ARG_INT, &monitor, "Required GDK monitor index", "INDEX"},
    {"duration", 'd', 0, G_OPTION_ARG_INT, &duration, "Duration 1..300 seconds", "SECONDS"},
    {"renewable-lease", 0, 0, G_OPTION_ARG_NONE, &renewable_lease, "SIGUSR1 renews finite lease while policy is fresh", NULL},
    {"fps", 0, 0, G_OPTION_ARG_INT, &fps, "Queue cadence 15/30/60", "FPS"},
    {"weather", 0, 0, G_OPTION_ARG_FILENAME, &path, "Selected weather JSON, read at 1Hz", "PATH"},
    {"validate-weather", 0, 0, G_OPTION_ARG_FILENAME, &validate_path, "Validate weather and print effects without display", "PATH"},
    {"policy", 0, 0, G_OPTION_ARG_FILENAME, &policy_path, "Opt in to presentation heartbeat policy", "PATH"},
    {"policy-session", 0, 0, G_OPTION_ARG_STRING, &policy_session, "Explicit policy compositor session", "SESSION"},
    {"output", 0, 0, G_OPTION_ARG_STRING, &output, "Exact selected GDK connector for policy", "CONNECTOR"},
    {"validate-policy", 0, 0, G_OPTION_ARG_FILENAME, &validate_policy, "Validate policy offline with session/output", "PATH"},
    {"preset", 0, 0, G_OPTION_ARG_STRING, &preset_name, "clear/cloud/dusk/night/storm/fog", "NAME"},
    {"lightning", 0, 0, G_OPTION_ARG_NONE, &lightning, "Opt in to storm preset cloud illumination", NULL},
    {"reduced-motion", 0, 0, G_OPTION_ARG_NONE, &reduced, "Freeze sky motion and block lightning", NULL},
    {"self-test", 0, 0, G_OPTION_ARG_NONE, &test, "Parser tests without display", NULL}, {NULL}
  };
  g_autoptr(GOptionContext) options = g_option_context_new("- finite click-through background sky");
  g_autoptr(GError) error = NULL;
  g_option_context_add_main_entries(options, entries, NULL);
  if (!g_option_context_parse(options, &argc, &argv, &error)) { g_printerr("%s\n", error->message); return 1; }
  if (test) return self_test() ? 0 : 1;
  if (renewable_lease && !policy_path) {
    g_printerr("renewable lease requires explicit presentation policy\n"); return 1;
  }
  if ((policy_path || validate_policy || policy_session || output) &&
      (!(policy_path || validate_policy) || !policy_selector(policy_session, output))) {
    g_printerr("policy requires path, explicit policy-session and output connector\n"); return 1;
  }
  if (validate_policy) {
    SkyPolicy accepted;
    gboolean ok = read_policy(validate_policy, policy_session, output, 0, &accepted, &error);
    if (ok) g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"validate_policy\",\"status\":\"ok\",\"render_allowed\":%s,\"reason\":\"%s\",\"sequence\":%" G_GINT64_FORMAT "}\n",
                    accepted.allowed ? "true" : "false", accepted.reason, accepted.sequence);
    else { g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"validate_policy\",\"status\":\"error\",\"render_allowed\":false}\n"); g_printerr("%s\n", error ? error->message : "unknown error"); }
    g_free(policy_path); g_free(policy_session); g_free(output); g_free(validate_policy);
    g_free(validate_path); g_free(path); g_free(preset_name);
    return ok ? 0 : 1;
  }
  if (validate_path) {
    Weather accepted;
    gboolean ok = read_weather(validate_path, &accepted, &error);
    if (ok) g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"validate_weather\",\"status\":\"ok\",\"effects\":{\"sun_elevation\":%.9g,\"sun_azimuth\":%.9g,\"cloud_cover\":%.9g,\"fog_density\":%.9g,\"wind_x\":%.9g,\"lightning_enabled\":%s,\"reduced_motion\":%s,\"thunderstorm\":%s}}\n",
                    accepted.elevation, accepted.azimuth, accepted.clouds, accepted.fog, accepted.wind,
                    accepted.lightning ? "true" : "false", accepted.reduced ? "true" : "false", accepted.storm ? "true" : "false");
    else { g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"validate_weather\",\"status\":\"error\",\"lightning_enabled\":false}\n"); g_printerr("%s\n", error ? error->message : "unknown error"); }
    g_free(validate_path); g_free(path); g_free(preset_name);
    g_free(policy_path); g_free(policy_session); g_free(output);
    return ok ? 0 : 1;
  }
  if (monitor < 0 || duration < 1 || duration > 300 || (fps != 15 && fps != 30 && fps != 60) || (path && preset_name)) {
    g_printerr("invalid monitor/duration/fps or conflicting weather/preset\n"); return 1;
  }
  State state = {.monitor = monitor, .duration = duration, .fps = fps, .weather_path = path,
                 .policy_path = policy_path, .policy_session = policy_session, .output_connector = output,
                 .reduced_override = reduced, .renewable_lease = renewable_lease,
                 .weather = {.elevation = 35, .azimuth = 180, .clouds = .5, .wind = 35}};
  if (preset_name && !preset(preset_name, &state.weather)) { g_printerr("unknown preset\n"); return 1; }
  state.weather.lightning = lightning; state.weather.reduced = reduced;
  state.target = state.weather;
  state.app = gtk_application_new("org.a_weather_app.atmosphere", G_APPLICATION_NON_UNIQUE);
  g_signal_connect(state.app, "activate", G_CALLBACK(activate), &state);
  state.term = g_unix_signal_add(SIGTERM, stop, &state); state.interrupt = g_unix_signal_add(SIGINT, stop, &state);
  if (state.renewable_lease) state.renew = g_unix_signal_add(SIGUSR1, renew_lease, &state);
  int result = g_application_run(G_APPLICATION(state.app), argc, argv);
  guint sources[] = {state.tick, state.poll, state.policy_poll, state.expiry, state.term, state.interrupt, state.renew};
  for (guint i = 0; i < G_N_ELEMENTS(sources); ++i)
    if (sources[i] && g_main_context_find_source_by_id(NULL, sources[i])) g_source_remove(sources[i]);
  if (state.window) gtk_window_destroy(state.window);
  gboolean never_presented = state.policy_path && !state.presented && !state.frames;
  g_print("{\"component\":\"a-weather-app-atmosphere\",\"event\":\"stop\",\"status\":\"%s\",\"frames\":%u,\"weather_updates\":%u,\"errors\":%u,\"input_region_empty\":%s,\"policy_updates\":%u,\"policy_errors\":%u,\"paused_callbacks\":%u,\"flash_clears\":%u,\"visual_time\":%.6f,\"cloud_offset\":%.6f}\n",
          never_presented ? "suppressed_without_render" : state.frames ? "rendered" : "no_render",
          state.frames, state.updates, state.errors, state.input_empty ? "true" : "false", state.policy_updates,
          state.policy_errors, state.paused_callbacks, state.flash_clears, state.visual_time, state.cloud_offset);
  g_clear_object(&state.selected_monitor);
  g_object_unref(state.app); g_free(path); g_free(preset_name); g_free(policy_path); g_free(policy_session); g_free(output);
  return state.failed || (!never_presented && (!state.frames || !state.input_empty)) ? 1 : result;
}
