#pragma once
#include <glib.h>
#include <math.h>

typedef struct {
    double elevation, azimuth, clouds, fog, wind;
    gboolean lightning, reduced, storm;
} Weather;

typedef struct {
    gint64 next;
    gint64 period;
} SkySchedule;

static inline gint64 sky_period(guint fps, gboolean reduced) {
    return reduced ? G_TIME_SPAN_SECOND : (G_TIME_SPAN_SECOND + fps - 1) / fps;
}
static inline void sky_schedule_reset(SkySchedule* schedule, gint64 now, guint fps,
                                      gboolean reduced) {
    schedule->period = sky_period(fps, reduced);
    schedule->next = now + schedule->period;
}
static inline gboolean sky_schedule_due(SkySchedule* schedule, gint64 now) {
    if (now < schedule->next)
        return FALSE;
    // Anchor to the previous deadline. Skip missed slots instead of either
    // accumulating callback lateness or issuing a catch-up burst.
    schedule->next += ((now - schedule->next) / schedule->period + 1) * schedule->period;
    return TRUE;
}
static inline guint sky_delay_ms(const SkySchedule* schedule, gint64 now) {
    gint64 remaining = MAX((gint64)1, schedule->next - now);
    return (guint)MAX((gint64)1, (remaining + 999) / 1000);
}

static inline void sky_safety(Weather* current, const Weather* target) {
    current->reduced = target->reduced;
    current->storm = target->storm;
    current->lightning = target->lightning && target->storm && !target->reduced;
}
static inline void sky_approach(Weather* current, const Weather* target, double delta) {
    // A 1.4-second time constant gives continuous changes even though file polling
    // is at 1Hz. Safety/accessibility flags never wait for interpolation.
    double blend = isfinite(delta) && delta > 0 ? -expm1(-MIN(delta, 10.0) / 1.4) : 0;
    current->elevation += (target->elevation - current->elevation) * blend;
    current->clouds += (target->clouds - current->clouds) * blend;
    current->fog += (target->fog - current->fog) * blend;
    current->wind += (target->wind - current->wind) * blend;
    double angle = fmod(target->azimuth - current->azimuth + 540.0, 360.0) - 180.0;
    current->azimuth = fmod(current->azimuth + angle * blend + 360.0, 360.0);
    sky_safety(current, target);
}

static inline void sky_advance(Weather* current, const Weather* target, double delta,
                               double* cloud_offset, double* visual_time) {
    if (!isfinite(delta) || delta <= 0) {
        sky_safety(current, target);
        return;
    }
    double old_wind = current->wind;
    gboolean was_reduced = current->reduced;
    sky_approach(current, target, delta);
    if (!was_reduced) {
        // Integrate displacement rather than multiplying new wind by total uptime.
        *cloud_offset += (old_wind + current->wind) * .5 * delta * .00008;
        *visual_time += delta;
    }
}
