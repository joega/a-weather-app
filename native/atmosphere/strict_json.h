#pragma once
#include <json-glib/json-glib.h>
#include <gio/gio.h>

// JsonParser strict mode rejects extensions, but not duplicate object members.
// Track decoded member names so escaped aliases are also detected.
typedef struct {
    JsonParser parent;
    GHashTable* objects;
    gboolean duplicate;
} WeatherJsonParser;
typedef struct {
    JsonParserClass parent;
} WeatherJsonParserClass;
G_DEFINE_TYPE(WeatherJsonParser, weather_json_parser, JSON_TYPE_PARSER)

static void weather_json_member(JsonParser* parser, JsonObject* object, const char* name) {
    WeatherJsonParser* self = (WeatherJsonParser*)parser;
    GHashTable* names = g_hash_table_lookup(self->objects, object);
    if (!names) {
        names = g_hash_table_new_full(g_str_hash, g_str_equal, g_free, NULL);
        // Retain objects until parser destruction: replaced objects must not allow
        // address reuse to confuse duplicate detection in a later object.
        g_hash_table_insert(self->objects, json_object_ref(object), names);
    }
    if (g_hash_table_contains(names, name))
        self->duplicate = TRUE;
    else
        g_hash_table_add(names, g_strdup(name));
}
static void weather_json_finalize(GObject* object) {
    WeatherJsonParser* self = (WeatherJsonParser*)object;
    g_hash_table_unref(self->objects);
    G_OBJECT_CLASS(weather_json_parser_parent_class)->finalize(object);
}
static void weather_json_parser_class_init(WeatherJsonParserClass* klass) {
    JSON_PARSER_CLASS(klass)->object_member = weather_json_member;
    G_OBJECT_CLASS(klass)->finalize = weather_json_finalize;
}
static void weather_json_parser_init(WeatherJsonParser* self) {
    self->objects =
        g_hash_table_new_full(g_direct_hash, g_direct_equal, (GDestroyNotify)json_object_unref,
                              (GDestroyNotify)g_hash_table_unref);
}
static inline JsonParser* weather_json_new(void) {
    return g_object_new(weather_json_parser_get_type(), "strict", TRUE, NULL);
}
static inline gboolean weather_json_load(JsonParser* parser, const char* text, gsize length,
                                         GError** error) {
    if (!json_parser_load_from_data(parser, text, (gssize)length, error))
        return FALSE;
    if (((WeatherJsonParser*)parser)->duplicate) {
        g_set_error_literal(error, G_IO_ERROR, G_IO_ERROR_INVALID_DATA, "duplicate JSON member");
        return FALSE;
    }
    return TRUE;
}
