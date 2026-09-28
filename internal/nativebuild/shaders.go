package nativebuild

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/joega/a-weather-app/internal/safeio"
)

var uniforms = []string{"scene_time", "sun_elevation", "sun_azimuth", "cloud_cover", "fog_density", "cloud_offset", "lightning", "reduced_motion", "aspect_ratio"}
var declaration = regexp.MustCompile(`(?m)^uniform\s+(float|bool)\s+(\w+)[^;]*;`)
var qtDeclarations = regexp.MustCompile(`(?m)^shader_type[^;]*;\s*|^render_mode[^;]*;\s*|^uniform[^;]*;\s*`)
var nativeDeclarations = regexp.MustCompile(`(uniform\s+\w+\s+\w+)\s*(?::\s*hint_range\([^)]*\))?\s*=\s*[^;]+;`)

const precipitation = `
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
`
const rainAdapter = `
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
`

func shaderSource(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) {
		return "", errors.New("canonical shader exceeds its bounds")
	}
	source := string(raw)
	if strings.Count(source, "void fragment()") != 1 {
		return "", errors.New("expected one canonical fragment")
	}
	return source, nil
}
func TranslateQt(raw []byte) ([]byte, error) {
	source, err := shaderSource(raw)
	if err != nil {
		return nil, err
	}
	declarations := declaration.FindAllStringSubmatch(source, -1)
	if len(declarations) != len(uniforms) {
		return nil, errors.New("canonical uniform interface changed")
	}
	fields := []string{}
	for i, d := range declarations {
		if d[2] != uniforms[i] {
			return nil, errors.New("canonical uniform interface changed")
		}
		fields = append(fields, "    "+d[1]+" "+d[2]+";")
	}
	body := qtDeclarations.ReplaceAllString(source, "")
	if strings.Count(body, "COLOR =") != 1 {
		return nil, errors.New("canonical color interface changed")
	}
	body = strings.ReplaceAll(body, "void fragment()", "void main()")
	body = strings.ReplaceAll(body, "vec2 uv = UV;", "vec2 uv = qt_TexCoord0;")
	body = strings.ReplaceAll(body, "void main()", precipitation+"\nvoid main()")
	body = strings.ReplaceAll(body, "    COLOR =", rainAdapter+"    fragColor =")
	body = strings.ReplaceAll(body, "vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0);", "vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0) * qt_Opacity;")
	return []byte("// Generated from godot/shaders/atmosphere.gdshader; do not edit.\n#version 440\nlayout(location = 0) in vec2 qt_TexCoord0;\nlayout(location = 0) out vec4 fragColor;\nlayout(std140, binding = 0) uniform buf {\n    mat4 qt_Matrix;\n    float qt_Opacity;\n" + strings.Join(fields, "\n") + "\n    float rain_amount;\n    float snow_amount;\n    float wind_x;\n};\n" + body), nil
}
func TranslateNative(raw []byte) ([]byte, error) {
	source, err := shaderSource(raw)
	if err != nil {
		return nil, err
	}
	source = regexp.MustCompile(`(?m)^shader_type.*?;\s*`).ReplaceAllString(source, "")
	source = regexp.MustCompile(`(?m)^render_mode.*?;\s*`).ReplaceAllString(source, "")
	source = nativeDeclarations.ReplaceAllString(source, "${1};")
	source = strings.ReplaceAll(source, "void fragment()", "void main()")
	source = "#version 300 es\nprecision highp float;\nprecision highp int;\nin vec2 v_uv;\nout vec4 result;\n#define UV vec2(v_uv.x, 1.0-v_uv.y)\n#define COLOR result\n" + source
	var out strings.Builder
	out.WriteString("/* Generated from godot/shaders/atmosphere.gdshader. */\nstatic const char *sky_fragment =\n")
	for i, line := range strings.Split(strings.TrimSuffix(source, "\n"), "\n") {
		if i > 0 {
			out.WriteByte('\n')
		}
		out.WriteString(strconv.QuoteToASCII(line + "\n"))
	}
	out.WriteString(";\n")
	return []byte(out.String()), nil
}

// Hold every ancestor directory descriptor; refuse symlink or writable substitutions.
func ownedDirectory(path string) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("absolute normalized directory required")
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var rootInfo syscall.Stat_t
	if err = syscall.Fstat(fd, &rootInfo); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		syscall.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
		var info syscall.Stat_t
		if e = syscall.Fstat(fd, &info); e != nil {
			syscall.Close(fd)
			return nil, e
		}
		if (info.Uid != rootInfo.Uid && info.Uid != uint32(os.Geteuid())) || (info.Mode&0022 != 0 && !(info.Uid == rootInfo.Uid && info.Mode&syscall.S_ISVTX != 0)) {
			syscall.Close(fd)
			return nil, errors.New("unsafe build directory ancestor")
		}
	}
	var info syscall.Stat_t
	if err = syscall.Fstat(fd, &info); err != nil || info.Uid != uint32(os.Geteuid()) || info.Mode&0022 != 0 {
		syscall.Close(fd)
		return nil, errors.New("build directory must be owned and not writable by others")
	}
	return os.NewFile(uintptr(fd), path), nil
}
func atomicWrite(path string, raw []byte, maximum int) error {
	if len(raw) == 0 || len(raw) > maximum {
		return errors.New("generated asset exceeds its limit")
	}
	directory, err := ownedDirectory(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	entropy := make([]byte, 16)
	if _, err = rand.Read(entropy); err != nil {
		return err
	}
	name := ".native-build-" + hex.EncodeToString(entropy)
	fd, err := syscall.Openat(int(directory.Fd()), name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	defer syscall.Unlinkat(int(directory.Fd()), name)
	if _, err = file.Write(raw); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = syscall.Renameat(int(directory.Fd()), name, int(directory.Fd()), filepath.Base(path)); err != nil {
		return err
	}
	return directory.Sync()
}
func ShaderHeader(source, output string) error {
	raw, err := safeio.ReadFile(source, 65536)
	if err != nil {
		return err
	}
	header, err := TranslateNative(raw)
	if err != nil {
		return err
	}
	return atomicWrite(output, header, 128*1024)
}
func Shaders(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = filepath.Clean(root)
	raw, err := safeio.ReadFile(filepath.Join(root, "godot/shaders/atmosphere.gdshader"), 65536)
	if err != nil {
		return err
	}
	translated, err := TranslateQt(raw)
	if err != nil {
		return err
	}
	current, err := safeio.ReadFile(filepath.Join(root, "ui/shaders/atmosphere.frag"), 65536)
	if err != nil {
		return err
	}
	if !bytes.Equal(translated, current) {
		return errors.New("Qt shader source is stale; use translate-qt to regenerate")
	}
	directory, err := ownedDirectory(filepath.Join(root, "ui/shaders"))
	if err != nil {
		return err
	}
	directory.Close()
	temporary, err := os.MkdirTemp("", "a-weather-app-shader-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	source := filepath.Join(temporary, "atmosphere.frag")
	if err = os.WriteFile(source, translated, 0600); err != nil {
		return err
	}
	// An unlinked held output descriptor prevents planting/replacing qsb's output path.
	pack, err := os.CreateTemp(temporary, "pack-")
	if err != nil {
		return err
	}
	defer pack.Close()
	if err = os.Remove(pack.Name()); err != nil {
		return err
	}
	output := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), pack.Fd())
	if _, err = run(30*time.Second, 65536, "/usr/lib/qt6/bin/qsb", "--glsl", "300 es,330", "--hlsl", "50", "--msl", "12", "-o", output, source); err != nil {
		return err
	}
	info, err := pack.Stat()
	if err != nil {
		return err
	}
	if info.Size() <= 0 || info.Size() > 2*1024*1024 {
		return errors.New("shader pack exceeds its limit")
	}
	if _, err = run(10*time.Second, 2*1024*1024, "/usr/lib/qt6/bin/qsb", "--dump", output); err != nil {
		return err
	}
	if _, err = pack.Seek(0, 0); err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(pack, 2*1024*1024+1))
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(root, "ui/shaders/atmosphere.frag.qsb"), data, 2*1024*1024); err != nil {
		return err
	}
	return ShaderHeader(filepath.Join(root, "godot/shaders/atmosphere.gdshader"), filepath.Join(root, "native/atmosphere/sky_shader.h"))
}
func TranslateQtFile(source, output string) error {
	raw, err := safeio.ReadFile(source, 65536)
	if err != nil {
		return err
	}
	translated, err := TranslateQt(raw)
	if err != nil {
		return err
	}
	return atomicWrite(output, translated, 65536)
}
