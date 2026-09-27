#pragma once
#include <json-glib/json-glib.h>
#include <gio/gio.h>
#include <math.h>
#include <string.h>
#include "strict_json.h"

#define POLICY_LIMIT (64 * 1024)
#define POLICY_MAX_AGE_MS 1500
#define POLICY_MAX_FUTURE_MS 500
typedef struct {
  gint64 sequence, generated_ms;
  gboolean allowed;
  char reason[65];
} SkyPolicy;
typedef enum { SKY_POLICY_SKIP, SKY_POLICY_CLEAR_FLASH, SKY_POLICY_DRAW } SkyPolicyDraw;
static inline gboolean policy_clear_required(gboolean was_allowed, gboolean allowed, gboolean presented, double last_flash) {
  return was_allowed && !allowed && presented && last_flash > 0;
}
static inline SkyPolicyDraw policy_draw(gboolean allowed, gboolean clear_pending) {
  return allowed ? SKY_POLICY_DRAW : clear_pending ? SKY_POLICY_CLEAR_FLASH : SKY_POLICY_SKIP;
}

static inline gboolean policy_invalid(GError **error, const char *message) {
  g_set_error_literal(error, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, message);
  return FALSE;
}
static inline gboolean policy_selector(const char *session, const char *output) {
  return session && output && strlen(session) <= 256 &&
      g_regex_match_simple("\\A[a-f0-9]+_[0-9]+_[0-9]+\\z", session, 0, 0) &&
      g_regex_match_simple("\\A[A-Za-z0-9_.:-]{1,128}\\z", output, 0, 0);
}
static inline gboolean policy_integer(JsonObject *object, const char *key, gint64 low, gint64 high, gint64 *out) {
  JsonNode *node = json_object_get_member(object, key);
  if (!node || !JSON_NODE_HOLDS_VALUE(node) || json_node_get_value_type(node) != G_TYPE_INT64) return FALSE;
  *out = json_node_get_int(node);
  return *out >= low && *out <= high;
}
static inline const char *policy_string(JsonObject *object, const char *key) {
  JsonNode *node = json_object_get_member(object, key);
  return node && JSON_NODE_HOLDS_VALUE(node) && json_node_get_value_type(node) == G_TYPE_STRING ? json_node_get_string(node) : NULL;
}
static inline gboolean policy_parse(const char *text, gsize length, const char *session, const char *output,
                                     gint64 last_sequence, double now_ms, SkyPolicy *out, GError **error) {
  if (!policy_selector(session, output) || !isfinite(now_ms) || now_ms < 0 || !length || length > POLICY_LIMIT ||
      memchr(text, '\0', length) || g_strstr_len(text, (gssize)length, "\\u0000"))
    return policy_invalid(error, "policy selector/size/NUL limit");
  guint depth = 0; gboolean quoted = FALSE, escaped = FALSE;
  for (gsize i = 0; i < length; ++i) {
    char c = text[i];
    if (quoted) { if (escaped) escaped = FALSE; else if (c == '\\') escaped = TRUE; else if (c == '"') quoted = FALSE; }
    else if (c == '"') quoted = TRUE;
    else if (c == '{' || c == '[') { if (++depth > 64) return policy_invalid(error, "policy nesting limit"); }
    else if (c == '}' || c == ']') { if (!depth) return policy_invalid(error, "policy nesting mismatch"); --depth; }
  }
  if (quoted || depth) return policy_invalid(error, "policy incomplete JSON");
  g_autoptr(JsonParser) parser = weather_json_new();
  if (!weather_json_load(parser, text, length, error)) return FALSE;
  JsonNode *root = json_parser_get_root(parser);
  if (!root || !JSON_NODE_HOLDS_OBJECT(root)) return policy_invalid(error, "policy object required");
  JsonObject *object = json_node_get_object(root);
  const char *got_session = policy_string(object, "session"), *got_output = policy_string(object, "output");
  const char *reason = policy_string(object, "reason");
  gint64 version, stale;
  SkyPolicy next = {0};
  JsonNode *allowed = json_object_get_member(object, "render_allowed");
  if (!policy_integer(object, "schema_version", 1, 1, &version) || !got_session || strcmp(got_session, session) ||
      !got_output || strcmp(got_output, output) || !policy_integer(object, "sequence", MAX((gint64)0, last_sequence), G_MAXINT64, &next.sequence) ||
      !policy_integer(object, "generated_at_unix_ms", 0, G_GINT64_CONSTANT(4102444800000), &next.generated_ms) ||
      !policy_integer(object, "stale_after_ms", 1500, 1500, &stale) ||
      !allowed || !JSON_NODE_HOLDS_VALUE(allowed) || json_node_get_value_type(allowed) != G_TYPE_BOOLEAN ||
      !reason || !g_regex_match_simple("\\A[a-z0-9_]{1,64}\\z", reason, 0, 0))
    return policy_invalid(error, "policy envelope/type/selector/sequence mismatch");
  double age = now_ms - (double)next.generated_ms;
  next.allowed = json_node_get_boolean(allowed);
  if (age > POLICY_MAX_AGE_MS || age < -POLICY_MAX_FUTURE_MS || next.allowed != (strcmp(reason, "none") == 0))
    return policy_invalid(error, "policy stale/future/decision mismatch");
  g_strlcpy(next.reason, reason, sizeof next.reason);
  *out = next;
  return TRUE;
}
