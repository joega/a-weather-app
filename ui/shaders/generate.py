#!/usr/bin/env python3
"""Mechanically adapt the canonical Godot sky to Qt Quick's RHI interface."""
import argparse
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "godot/shaders/atmosphere.gdshader"
OUTPUT = Path(__file__).with_name("atmosphere.frag")
UNIFORMS = ("scene_time", "sun_elevation", "sun_azimuth", "cloud_cover", "fog_density",
            "cloud_offset", "lightning", "reduced_motion", "aspect_ratio")
PRECIPITATION_FUNCTIONS = """
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
"""
RAIN_ADAPTER = """
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
"""


def translate(source):
    declarations = re.findall(r"^uniform\s+(float|bool)\s+(\w+)[^;]*;", source, re.M)
    if tuple(name for _, name in declarations) != UNIFORMS:
        raise ValueError("canonical uniform interface changed; review Qt adapter")
    body = re.sub(r"^shader_type[^;]*;\s*|^render_mode[^;]*;\s*|^uniform[^;]*;\s*", "", source, flags=re.M)
    if body.count("void fragment()") != 1 or body.count("COLOR =") != 1:
        raise ValueError("canonical fragment interface changed")
    body = body.replace("void fragment()", "void main()").replace("vec2 uv = UV;", "vec2 uv = qt_TexCoord0;")
    body = body.replace("void main()", PRECIPITATION_FUNCTIONS + "\nvoid main()")
    body = body.replace("    COLOR =", RAIN_ADAPTER + "    fragColor =")
    body = body.replace("vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0);", "vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0) * qt_Opacity;")
    fields = "\n".join(f"    {kind} {name};" for kind, name in declarations)
    return ("// Generated from godot/shaders/atmosphere.gdshader; do not edit.\n"
            "#version 440\nlayout(location = 0) in vec2 qt_TexCoord0;\n"
            "layout(location = 0) out vec4 fragColor;\n"
            "layout(std140, binding = 0) uniform buf {\n    mat4 qt_Matrix;\n    float qt_Opacity;\n"
            + fields + "\n    float rain_amount;\n    float snow_amount;\n    float wind_x;\n};\n" + body)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    generated = translate(SOURCE.read_text())
    if args.check:
        if not OUTPUT.is_file() or OUTPUT.read_text() != generated:
            raise SystemExit("Qt sky shader is stale; run make -C ui/shaders")
    else:
        OUTPUT.write_text(generated)


if __name__ == "__main__":
    main()
