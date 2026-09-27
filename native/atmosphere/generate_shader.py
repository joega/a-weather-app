#!/usr/bin/env python3
"""Translate the canonical Godot canvas shader; never maintain a second sky."""
import json
import re
import sys
from pathlib import Path

source = Path(sys.argv[1]).read_text()
if source.count("void fragment()") != 1:
    raise SystemExit("expected one canvas fragment entry point")
source = re.sub(r"^shader_type.*?;\s*", "", source, flags=re.M)
source = re.sub(r"^render_mode.*?;\s*", "", source, flags=re.M)
source = re.sub(r"(uniform\s+\w+\s+\w+)\s*(?::\s*hint_range\([^)]*\))?\s*=\s*[^;]+;", r"\1;", source)
source = source.replace("void fragment()", "void main()")
prefix = "#version 300 es\nprecision highp float;\nprecision highp int;\nin vec2 v_uv;\nout vec4 result;\n#define UV vec2(v_uv.x, 1.0-v_uv.y)\n#define COLOR result\n"
Path(sys.argv[2]).write_text("/* Generated from godot/shaders/atmosphere.gdshader. */\nstatic const char *sky_fragment =\n" +
                            "\n".join(json.dumps(line + "\n") for line in (prefix + source).splitlines()) + ";\n")
