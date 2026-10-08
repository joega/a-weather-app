// Generated from godot/shaders/atmosphere.gdshader; do not edit.
#version 440
layout(location = 0) in vec2 qt_TexCoord0;
layout(location = 0) out vec4 fragColor;
layout(std140, binding = 0) uniform buf {
    mat4 qt_Matrix;
    float qt_Opacity;
    float scene_time;
    float sun_elevation;
    float sun_azimuth;
    float cloud_cover;
    float fog_density;
    float cloud_offset;
    float lightning;
    bool reduced_motion;
    float aspect_ratio;
    float rain_amount;
    float snow_amount;
    float wind_x;
};
// Integrated on the CPU so a wind change cannot reposition the cloud field.
// Value noise uses arithmetic hashes, avoiding trigonometry in the cloud loops.
// Hash without Sine: Copyright (c) 2014 David Hoskins, MIT license.
// https://www.shadertoy.com/view/4djSRW — see THIRD_PARTY_NOTICES.md.
float hash21(vec2 p) {
    vec3 q = fract(vec3(p.x, p.y, p.x) * 0.1031);
    q += dot(q, q.yzx + 33.33);
    return fract((q.x + q.y) * q.z);
}

float noise2(vec2 p) {
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = f * f * (3.0 - 2.0 * f);
    return mix(mix(hash21(i), hash21(i + vec2(1.0, 0.0)), f.x),
               mix(hash21(i + vec2(0.0, 1.0)), hash21(i + vec2(1.0)), f.x), f.y);
}

float cloud_noise(vec2 p) {
    float result = 0.0;
    float weight = 0.5;
    // Six fixed octaves preserve small billows in the half-resolution buffer.
    // Still fixed work per pixel, without ray marching or dependent loops.
    for (int i = 0; i < 6; i++) {
        result += noise2(p) * weight;
        p = mat2(vec2(1.60, 1.20), vec2(-1.20, 1.60)) * p + vec2(7.1, 13.7);
        weight *= 0.5;
    }
    return result / 0.984375;
}

float light_noise(vec2 p) {
    return noise2(p) * 0.6667 + noise2(p * 2.0 + vec2(11.3, 8.7)) * 0.3333;
}

vec4 cloud_layer(vec2 p, vec2 light_direction, float cover, float daylight,
                 float dusk, float flash, float layer_strength) {
    // Broad warped shapes plus directional density differences suggest lit
    // billows. Light changes independently of alpha, rather than tinting noise.
    vec2 warp = vec2(noise2(p * 0.55 + vec2(19.2, 4.7)),
                     noise2(p * 0.55 + vec2(3.5, 27.1))) - vec2(0.5);
    p += warp * 0.65;
    float body = cloud_noise(p);
    float threshold = mix(1.02, 0.32, cover);
    float density = smoothstep(threshold - 0.08, threshold + 0.10, body);
    float light_side = light_noise(p + light_direction * 0.22);
    // Fine density variation provides restrained relief within overcast areas,
    // rather than flattening the entire cloud mass into a blurred color field.
    float detail = noise2(p * 8.0 + vec2(31.7, 5.9)) - 0.5;
    float relief = clamp(0.50 + (body - light_side) * 3.1 + detail * 0.15, 0.0, 1.0);
    float rim = pow(1.0 - density, 2.0) * relief;
    vec3 shadow = mix(vec3(0.035, 0.050, 0.095), vec3(0.35, 0.43, 0.53), daylight);
    vec3 lit = mix(vec3(0.15, 0.20, 0.30), vec3(0.88, 0.93, 0.96), daylight);
    float overcast = smoothstep(0.72, 1.0, cover) * daylight;
    shadow = mix(shadow, vec3(0.15, 0.20, 0.28), overcast);
    lit = mix(lit, vec3(0.64, 0.70, 0.77), overcast);
    lit = mix(lit, vec3(0.99, 0.69, 0.49), dusk * 0.52);
    vec3 color = mix(shadow, lit, relief * 0.62 + rim * 0.28);
    color += vec3(0.58, 0.66, 0.90) * flash * (0.35 + body * 0.65);
    float alpha = density * layer_strength * smoothstep(0.015, 0.12, cover);
    return vec4(color, alpha);
}


// Window-local two-depth precipitation. Fixed calls, no particle buffers/loops.
float window_rain(vec2 uv, float aspect, float time, float depth) {
    float columns = mix(110.0, 74.0, depth);
    float rows = mix(10.0, 7.0, depth);
    float slope = clamp(wind_x / 500.0, -1.0, 1.0) * mix(0.13, 0.22, depth);
    float lane = (uv.x * aspect - uv.y * slope - cloud_offset * 0.4) * columns;
    float column = floor(lane);
    float seed = hash21(vec2(column, 71.3 + depth * 19.0));
    float center = mix(0.2, 0.8, hash21(vec2(column, 11.7 + depth)));
    float x = abs(fract(lane) - center);
    // Minus time makes the fixed phase travel DOWN in Qt's top-origin UV space.
    float y = fract(uv.y * rows - time * (0.9 + seed * 0.8 + depth * 0.4) + seed);
    float length = mix(0.13, 0.30, hash21(vec2(column, 43.2 + depth)));
    float streak = (1.0 - smoothstep(0.025, mix(0.09, 0.13, depth), x))
                 * smoothstep(0.50, 0.55, y) * (1.0 - smoothstep(0.55 + length, 0.60 + length, y));
    float density = step(1.0 - clamp(rain_amount, 0.0, 1.0), seed);
    return streak * density * mix(0.06, 0.10, depth);
}
float window_snow(vec2 uv, float aspect, float time, float depth) {
    vec2 grid = vec2(mix(34.0, 23.0, depth), mix(20.0, 14.0, depth));
    vec2 p = uv * vec2(aspect, 1.0) * grid;
    p.x -= cloud_offset * grid.x * 0.8;
    p.y -= time * mix(0.30, 0.42, depth);
    // Slow lateral turbulence; motion remains continuous when wind changes.
    p.x += sin(time * 0.45 + floor(p.y) * 1.7) * 0.13;
    vec2 cell = floor(p);
    float seed = hash21(cell + vec2(53.8, depth * 17.0));
    vec2 center = vec2(hash21(cell + vec2(8.3, 91.0)), hash21(cell + vec2(47.1, 2.8)));
    center = mix(vec2(0.2), vec2(0.8), center);
    float distance = length((fract(p) - center) / grid);
    float radius = mix(0.0013, 0.0022, depth) * mix(0.75, 1.25, seed);
    float flake = 1.0 - smoothstep(radius * 0.45, radius * 1.6, distance);
    float density = step(1.0 - clamp(snow_amount, 0.0, 1.0) * 0.45, seed);
    return flake * density * mix(0.25, 0.42, depth);
}

void main() {
    vec2 uv = qt_TexCoord0;
    float aspect = max(aspect_ratio, 0.1);
    float elevation = clamp(sun_elevation, -90.0, 90.0);
    float daylight = smoothstep(-8.0, 12.0, elevation);
    float night = 1.0 - smoothstep(-12.0, -2.0, elevation);
    float dusk = exp(-pow((elevation + 1.0) / 9.0, 2.0));
    float cover = clamp(cloud_cover, 0.0, 1.0);
    float fog = clamp(fog_density, 0.0, 1.0);
    // Global extinction prevents the artistic moon/stars from punching through
    // thick overcast or fog; local cloud alpha still masks partial-cloud gaps.
    // Clear-night visibility stays1.0. Two scalar ramps add no noise samples.
    float celestial_visibility = (1.0 - smoothstep(0.35, 0.80, cover))
                               * (1.0 - smoothstep(0.20, 0.80, fog));
    float night_visibility = night * celestial_visibility;
    float flash = clamp(lightning, 0.0, 1.0);
    float time = scene_time;

    vec3 zenith = mix(vec3(0.014, 0.022, 0.058), vec3(0.16, 0.39, 0.65), daylight);
    vec3 horizon = mix(vec3(0.065, 0.083, 0.14), vec3(0.66, 0.80, 0.86), daylight);
    float overcast = smoothstep(0.72, 1.0, cover) * daylight;
    zenith = mix(zenith, vec3(0.22, 0.28, 0.36), overcast);
    horizon = mix(horizon, vec3(0.44, 0.50, 0.56), overcast);
    zenith = mix(zenith, vec3(0.14, 0.13, 0.29), dusk * 0.48);
    horizon = mix(horizon, vec3(0.83, 0.47, 0.36), dusk * 0.76);
    float vertical = pow(clamp(uv.y, 0.0, 1.0), 1.45);
    vec3 sky = mix(zenith, horizon, vertical);
    float haze = exp(-max(0.88 - uv.y, 0.0) * 9.0);
    sky += mix(vec3(0.023, 0.028, 0.042), vec3(0.09, 0.10, 0.08), daylight) * haze;

    vec2 sun_position = vec2(0.5 + sin(radians(sun_azimuth - 180.0)) * 0.43,
                             0.88 - sin(radians(elevation)) * 0.92);
    float sun_distance = length((uv - sun_position) * vec2(aspect, 1.0));
    float sun_visible = smoothstep(-3.0, 1.0, elevation) * (1.0 - smoothstep(0.72, 0.98, cover));
    sun_visible *= 1.0 - smoothstep(0.25, 0.95, fog);
    vec3 sunlight = mix(vec3(1.0, 0.47, 0.22), vec3(1.0, 0.91, 0.70), smoothstep(0.0, 25.0, elevation));
    sky += sunlight * exp(-sun_distance * 8.5) * 0.18 * sun_visible;
    sky += sunlight * exp(-sun_distance * sun_distance * 180.0) * 0.24 * sun_visible;
    float disk = 1.0 - smoothstep(0.018, 0.022, sun_distance);
    sky = mix(sky, vec3(1.0, 0.97, 0.87), disk * sun_visible);

    // Sparse, subpixel-friendly stars. Slow twinkle freezes in reduced motion.
    vec2 star_grid = uv * vec2(aspect, 1.0) * 85.0;
    vec2 cell = floor(star_grid);
    float star_seed = hash21(cell + vec2(61.7, 3.8));
    vec2 star_position = vec2(hash21(cell + vec2(7.4, 91.2)), hash21(cell + vec2(42.1, 5.3)));
    float star_distance = length(fract(star_grid) - star_position);
    float star = (1.0 - smoothstep(0.025, 0.105, star_distance)) * step(0.981, star_seed);
    float twinkle = reduced_motion ? 0.85 : 0.85 + 0.15 * sin(time * 0.35 + star_seed * 67.0);
    sky += vec3(0.65, 0.74, 0.92) * star * twinkle * night_visibility * (1.0 - vertical);

    // An artistic crescent, not an astronomical lunar phase calculation.
    vec2 moon_position = vec2(clamp(1.0 - sun_position.x, 0.18, 0.82), 0.22);
    vec2 moon_uv = (uv - moon_position) * vec2(aspect, 1.0);
    float moon_distance = length(moon_uv);
    float moon_disk = 1.0 - smoothstep(0.021, 0.024, moon_distance);
    float moon_cut = smoothstep(0.021, 0.024, length(moon_uv - vec2(0.010, -0.003)));
    sky += vec3(0.19, 0.25, 0.39) * exp(-moon_distance * 30.0) * night_visibility;
    sky = mix(sky, vec3(0.79, 0.84, 0.92), moon_disk * moon_cut * night_visibility);

    vec2 sky_point = uv * vec2(aspect, 1.0);
    float drift = cloud_offset;
    // Distant sunlight has a constant direction across the cloud field. A
    // per-pixel direction to the sun disk creates an artificial radial cusp.
    vec2 light_direction = normalize(vec2(sin(radians(sun_azimuth - 180.0)) * 0.7,
                                         -0.5 - max(sin(radians(elevation)), 0.0) * 0.4));
    vec4 high_clouds = cloud_layer(sky_point * vec2(2.7, 4.1) + vec2(-drift * 0.65, 6.7),
                                   light_direction, cover * 0.88, daylight, dusk, flash, 0.60);
    vec4 low_clouds = cloud_layer(sky_point * vec2(3.8, 5.2) + vec2(-drift, 21.3),
                                  light_direction, cover, daylight, dusk, flash, 0.87);
    float cloud_horizon = 1.0 - smoothstep(0.77, 1.0, uv.y);
    sky = mix(sky, high_clouds.rgb, high_clouds.a * cloud_horizon);
    sky = mix(sky, low_clouds.rgb, low_clouds.a * cloud_horizon);


    float wisp_strength = smoothstep(0.01, 0.16, cover) * (1.0 - smoothstep(0.20, 0.50, cover));
    if (wisp_strength > 0.0) {
        vec4 wisps = cloud_layer(sky_point * vec2(2.0, 8.0) + vec2(-drift * 0.45, 39.1),
                                light_direction, 0.52, daylight, dusk, 0.0, 0.18);
        sky = mix(sky, wisps.rgb, wisps.a * wisp_strength * cloud_horizon);
    }
    vec3 fog_color = mix(vec3(0.12, 0.15, 0.21), vec3(0.72, 0.78, 0.80), daylight);
    fog_color = mix(fog_color, vec3(0.77, 0.58, 0.51), dusk * 0.38);
    // Fog veils the whole sky, with stronger extinction toward the horizon.
    // A horizon-only treatment looked like a clear day above a pale lower edge.
    float fog_alpha = fog * (0.48 + 0.50 * smoothstep(0.0, 1.0, uv.y));
    sky = mix(sky, fog_color, fog_alpha);

    // Two bounded depth samples per enabled precipitation kind, no CPU paint.
    if (!reduced_motion) {
        if (rain_amount > 0.0) {
            float rain = window_rain(uv, aspect, time, 0.0) + window_rain(uv, aspect, time, 1.0);
            sky += vec3(0.38, 0.49, 0.57) * rain;
        }
        if (snow_amount > 0.0) {
            float snow = window_snow(uv, aspect, time, 0.0) + window_snow(uv, aspect, time, 1.0);
            sky = mix(sky, vec3(0.85, 0.91, 0.97), clamp(snow, 0.0, 0.65));
        }
    }
    fragColor = vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0) * qt_Opacity;
}
