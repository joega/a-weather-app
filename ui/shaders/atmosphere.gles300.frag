#version 300 es
precision highp float;
precision highp int;

struct buf
{
    mat4 qt_Matrix;
    float qt_Opacity;
    float scene_time;
    float sun_elevation;
    float sun_azimuth;
    float cloud_cover;
    float fog_density;
    float cloud_offset;
    float lightning;
    uint reduced_motion;
    float aspect_ratio;
    float weather_dim;
    float rain_amount;
    float snow_amount;
    float wind_x;
    float celestial_left;
    float celestial_top;
};

uniform buf _285;

in vec2 qt_TexCoord0;
layout(location = 0) out vec4 fragColor;

float hash21(vec2 p)
{
    vec3 q = fract(vec3(p.x, p.y, p.x) * 0.103100001811981201171875);
    q += vec3(dot(q, q.yzx + vec3(33.3300018310546875)));
    return fract((q.x + q.y) * q.z);
}

float _noise2(vec2 p)
{
    vec2 i = floor(p);
    vec2 f = fract(p);
    f = (f * f) * (vec2(3.0) - (f * 2.0));
    vec2 param = i;
    vec2 param_1 = i + vec2(1.0, 0.0);
    vec2 param_2 = i + vec2(0.0, 1.0);
    vec2 param_3 = i + vec2(1.0);
    return mix(mix(hash21(param), hash21(param_1), f.x), mix(hash21(param_2), hash21(param_3), f.x), f.y);
}

float cloud_noise(inout vec2 p)
{
    float result = 0.0;
    float weight = 0.5;
    for (int i = 0; i < 6; i++)
    {
        vec2 param = p;
        result += (_noise2(param) * weight);
        p = (mat2(vec2(1.60000002384185791015625, 1.2000000476837158203125), vec2(-1.2000000476837158203125, 1.60000002384185791015625)) * p) + vec2(7.099999904632568359375, 13.69999980926513671875);
        weight *= 0.5;
    }
    return result / 0.984375;
}

float light_noise(vec2 p)
{
    vec2 param = p;
    vec2 param_1 = (p * 2.0) + vec2(11.30000019073486328125, 8.69999980926513671875);
    return (_noise2(param) * 0.66670000553131103515625) + (_noise2(param_1) * 0.33329999446868896484375);
}

vec4 cloud_layer(inout vec2 p, vec2 light_direction, float cover, float daylight, float dusk, float flash, float layer_strength)
{
    vec2 param = (p * 0.550000011920928955078125) + vec2(19.200000762939453125, 4.69999980926513671875);
    vec2 param_1 = (p * 0.550000011920928955078125) + vec2(3.5, 27.1000003814697265625);
    vec2 warp = vec2(_noise2(param), _noise2(param_1)) - vec2(0.5);
    p += (warp * 0.64999997615814208984375);
    vec2 param_2 = p;
    float _225 = cloud_noise(param_2);
    float body = _225;
    float threshold = mix(1.019999980926513671875, 0.319999992847442626953125, cover);
    float density = smoothstep(threshold - 0.07999999821186065673828125, threshold + 0.100000001490116119384765625, body);
    vec2 param_3 = p + (light_direction * 0.2199999988079071044921875);
    float light_side = light_noise(param_3);
    vec2 param_4 = (p * 8.0) + vec2(31.700000762939453125, 5.900000095367431640625);
    float detail = _noise2(param_4) - 0.5;
    float relief = clamp((0.5 + ((body - light_side) * 3.099999904632568359375)) + (detail * 0.1500000059604644775390625), 0.0, 1.0);
    float rim = pow(1.0 - density, 2.0) * relief;
    float overcast = smoothstep(0.7200000286102294921875, 1.0, cover);
    float weather = clamp(_285.weather_dim, 0.0, 1.0);
    vec3 day_shadow = mix(vec3(0.64999997615814208984375, 0.7400000095367431640625, 0.829999983310699462890625), vec3(0.60000002384185791015625, 0.689999997615814208984375, 0.769999980926513671875), vec3(overcast));
    vec3 day_lit = mix(vec3(0.9700000286102294921875, 0.9900000095367431640625, 1.0), vec3(0.89999997615814208984375, 0.939999997615814208984375, 0.9700000286102294921875), vec3(overcast));
    day_shadow = mix(day_shadow, mix(vec3(0.3499999940395355224609375, 0.430000007152557373046875, 0.5299999713897705078125), vec3(0.1500000059604644775390625, 0.20000000298023223876953125, 0.2800000011920928955078125), vec3(overcast)), vec3(weather));
    day_lit = mix(day_lit, mix(vec3(0.87999999523162841796875, 0.930000007152557373046875, 0.959999978542327880859375), vec3(0.63999998569488525390625, 0.699999988079071044921875, 0.769999980926513671875), vec3(overcast)), vec3(weather));
    vec3 shadow = mix(vec3(0.0350000001490116119384765625, 0.0500000007450580596923828125, 0.0949999988079071044921875), day_shadow, vec3(daylight));
    vec3 lit = mix(vec3(0.1500000059604644775390625, 0.20000000298023223876953125, 0.300000011920928955078125), day_lit, vec3(daylight));
    lit = mix(lit, vec3(0.9900000095367431640625, 0.689999997615814208984375, 0.4900000095367431640625), vec3(dusk * 0.519999980926513671875));
    vec3 color = mix(shadow, lit, vec3((relief * 0.62000000476837158203125) + (rim * 0.2800000011920928955078125)));
    color += ((vec3(0.579999983310699462890625, 0.660000026226043701171875, 0.89999997615814208984375) * flash) * (0.3499999940395355224609375 + (body * 0.64999997615814208984375)));
    float alpha = (density * layer_strength) * smoothstep(0.014999999664723873138427734375, 0.119999997317790985107421875, cover);
    return vec4(color, alpha);
}

float window_rain(vec2 uv, float aspect, float time, float depth)
{
    float columns = mix(110.0, 74.0, depth);
    float rows = mix(10.0, 7.0, depth);
    float slope = clamp(_285.wind_x / 500.0, -1.0, 1.0) * mix(0.12999999523162841796875, 0.2199999988079071044921875, depth);
    float lane = (((uv.x * aspect) - (uv.y * slope)) - (_285.cloud_offset * 0.4000000059604644775390625)) * columns;
    float column = floor(lane);
    vec2 param = vec2(column, 71.3000030517578125 + (depth * 19.0));
    float seed = hash21(param);
    vec2 param_1 = vec2(column, 11.69999980926513671875 + depth);
    float center = mix(0.20000000298023223876953125, 0.800000011920928955078125, hash21(param_1));
    float x = abs(fract(lane) - center);
    float y = fract(((uv.y * rows) - (time * ((0.89999997615814208984375 + (seed * 0.800000011920928955078125)) + (depth * 0.4000000059604644775390625)))) + seed);
    vec2 param_2 = vec2(column, 43.200000762939453125 + depth);
    float _length = mix(0.12999999523162841796875, 0.300000011920928955078125, hash21(param_2));
    float streak = ((1.0 - smoothstep(0.02500000037252902984619140625, mix(0.0900000035762786865234375, 0.12999999523162841796875, depth), x)) * smoothstep(0.5, 0.550000011920928955078125, y)) * (1.0 - smoothstep(0.550000011920928955078125 + _length, 0.60000002384185791015625 + _length, y));
    float density = step(1.0 - clamp(_285.rain_amount, 0.0, 1.0), seed);
    return (streak * density) * mix(0.0599999986588954925537109375, 0.100000001490116119384765625, depth);
}

float window_snow(vec2 uv, float aspect, float time, float depth)
{
    vec2 grid = vec2(mix(34.0, 23.0, depth), mix(20.0, 14.0, depth));
    vec2 p = (uv * vec2(aspect, 1.0)) * grid;
    p.x -= ((_285.cloud_offset * grid.x) * 0.800000011920928955078125);
    p.y -= (time * mix(0.300000011920928955078125, 0.4199999868869781494140625, depth));
    p.x += (sin((time * 0.449999988079071044921875) + (floor(p.y) * 1.7000000476837158203125)) * 0.12999999523162841796875);
    vec2 cell = floor(p);
    vec2 param = cell + vec2(53.799999237060546875, depth * 17.0);
    float seed = hash21(param);
    vec2 param_1 = cell + vec2(8.30000019073486328125, 91.0);
    vec2 param_2 = cell + vec2(47.09999847412109375, 2.7999999523162841796875);
    vec2 center = vec2(hash21(param_1), hash21(param_2));
    center = mix(vec2(0.20000000298023223876953125), vec2(0.800000011920928955078125), center);
    float _distance = length((fract(p) - center) / grid);
    float radius = mix(0.001300000003539025783538818359375, 0.002199999988079071044921875, depth) * mix(0.75, 1.25, seed);
    float flake = 1.0 - smoothstep(radius * 0.449999988079071044921875, radius * 1.60000002384185791015625, _distance);
    float density = step(1.0 - (clamp(_285.snow_amount, 0.0, 1.0) * 0.449999988079071044921875), seed);
    return (flake * density) * mix(0.25, 0.4199999868869781494140625, depth);
}

void main()
{
    vec2 uv = qt_TexCoord0;
    float aspect = max(_285.aspect_ratio, 0.100000001490116119384765625);
    float elevation = clamp(_285.sun_elevation, -90.0, 90.0);
    float daylight = smoothstep(-8.0, 12.0, elevation);
    float night = 1.0 - smoothstep(-12.0, -2.0, elevation);
    float dusk = exp(-pow((elevation + 1.0) / 9.0, 2.0));
    float cover = clamp(_285.cloud_cover, 0.0, 1.0);
    float fog = clamp(_285.fog_density, 0.0, 1.0);
    float celestial_visibility = (1.0 - smoothstep(0.3499999940395355224609375, 0.800000011920928955078125, cover)) * (1.0 - smoothstep(0.20000000298023223876953125, 0.800000011920928955078125, fog));
    float night_visibility = night * celestial_visibility;
    float flash = clamp(_285.lightning, 0.0, 1.0);
    float time = _285.scene_time;
    float overcast = smoothstep(0.7200000286102294921875, 1.0, cover);
    float weather = clamp(_285.weather_dim, 0.0, 1.0);
    vec3 day_zenith = mix(vec3(0.180000007152557373046875, 0.4799999892711639404296875, 0.7799999713897705078125), vec3(0.37999999523162841796875, 0.569999992847442626953125, 0.75), vec3(overcast));
    vec3 day_horizon = mix(vec3(0.660000026226043701171875, 0.819999992847442626953125, 0.910000026226043701171875), vec3(0.709999978542327880859375, 0.810000002384185791015625, 0.87999999523162841796875), vec3(overcast));
    day_zenith = mix(day_zenith, mix(vec3(0.1599999964237213134765625, 0.38999998569488525390625, 0.64999997615814208984375), vec3(0.2199999988079071044921875, 0.2800000011920928955078125, 0.36000001430511474609375), vec3(overcast)), vec3(weather));
    day_horizon = mix(day_horizon, mix(vec3(0.660000026226043701171875, 0.800000011920928955078125, 0.86000001430511474609375), vec3(0.439999997615814208984375, 0.5, 0.560000002384185791015625), vec3(overcast)), vec3(weather));
    vec3 zenith = mix(vec3(0.01400000043213367462158203125, 0.02199999988079071044921875, 0.0579999983310699462890625), day_zenith, vec3(daylight));
    vec3 horizon = mix(vec3(0.064999997615814208984375, 0.082999996840953826904296875, 0.14000000059604644775390625), day_horizon, vec3(daylight));
    zenith = mix(zenith, vec3(0.14000000059604644775390625, 0.12999999523162841796875, 0.2899999916553497314453125), vec3(dusk * 0.4799999892711639404296875));
    horizon = mix(horizon, vec3(0.829999983310699462890625, 0.4699999988079071044921875, 0.36000001430511474609375), vec3(dusk * 0.7599999904632568359375));
    float vertical = pow(clamp(uv.y, 0.0, 1.0), 1.4500000476837158203125);
    vec3 sky = mix(zenith, horizon, vec3(vertical));
    float haze = exp((-max(0.87999999523162841796875 - uv.y, 0.0)) * 9.0);
    sky += (mix(vec3(0.02300000004470348358154296875, 0.0280000008642673492431640625, 0.0419999994337558746337890625), vec3(0.0900000035762786865234375, 0.100000001490116119384765625, 0.07999999821186065673828125), vec3(daylight)) * haze);
    vec2 sun_position = vec2(0.5 + (sin(radians(_285.sun_azimuth - 180.0)) * 0.430000007152557373046875), 0.87999999523162841796875 - (sin(radians(elevation)) * 0.920000016689300537109375));
    sun_position = max(sun_position, vec2(_285.celestial_left, _285.celestial_top));
    float sun_distance = length((uv - sun_position) * vec2(aspect, 1.0));
    float sun_visible = smoothstep(-3.0, 1.0, elevation) * (1.0 - smoothstep(0.7200000286102294921875, 0.980000019073486328125, cover));
    sun_visible *= (1.0 - smoothstep(0.25, 0.949999988079071044921875, fog));
    vec3 sunlight = mix(vec3(1.0, 0.4699999988079071044921875, 0.2199999988079071044921875), vec3(1.0, 0.910000026226043701171875, 0.699999988079071044921875), vec3(smoothstep(0.0, 25.0, elevation)));
    sky += (((sunlight * exp((-sun_distance) * 8.5)) * 0.180000007152557373046875) * sun_visible);
    sky += (((sunlight * exp(((-sun_distance) * sun_distance) * 180.0)) * 0.23999999463558197021484375) * sun_visible);
    float disk = 1.0 - smoothstep(0.017999999225139617919921875, 0.02199999988079071044921875, sun_distance);
    sky = mix(sky, vec3(1.0, 0.9700000286102294921875, 0.87000000476837158203125), vec3(disk * sun_visible));
    vec2 star_grid = (uv * vec2(aspect, 1.0)) * 85.0;
    vec2 cell = floor(star_grid);
    vec2 param = cell + vec2(61.700000762939453125, 3.7999999523162841796875);
    float star_seed = hash21(param);
    vec2 param_1 = cell + vec2(7.400000095367431640625, 91.1999969482421875);
    vec2 param_2 = cell + vec2(42.09999847412109375, 5.30000019073486328125);
    vec2 star_position = vec2(hash21(param_1), hash21(param_2));
    float star_distance = length(fract(star_grid) - star_position);
    float star = (1.0 - smoothstep(0.02500000037252902984619140625, 0.104999996721744537353515625, star_distance)) * step(0.981000006198883056640625, star_seed);
    float _995;
    if (_285.reduced_motion != 0u)
    {
        _995 = 0.85000002384185791015625;
    }
    else
    {
        _995 = 0.85000002384185791015625 + (0.1500000059604644775390625 * sin((time * 0.3499999940395355224609375) + (star_seed * 67.0)));
    }
    float twinkle = _995;
    sky += ((((vec3(0.64999997615814208984375, 0.7400000095367431640625, 0.920000016689300537109375) * star) * twinkle) * night_visibility) * (1.0 - vertical));
    vec2 moon_position = vec2(clamp(0.5 - (sin(radians(_285.sun_azimuth - 180.0)) * 0.430000007152557373046875), 0.180000007152557373046875, 0.819999992847442626953125), 0.2199999988079071044921875);
    moon_position = max(moon_position, vec2(_285.celestial_left, _285.celestial_top));
    vec2 moon_uv = (uv - moon_position) * vec2(aspect, 1.0);
    float moon_distance = length(moon_uv);
    float moon_disk = 1.0 - smoothstep(0.02099999971687793731689453125, 0.0240000002086162567138671875, moon_distance);
    float moon_cut = smoothstep(0.02099999971687793731689453125, 0.0240000002086162567138671875, length(moon_uv - vec2(0.00999999977648258209228515625, -0.0030000000260770320892333984375)));
    sky += ((vec3(0.189999997615814208984375, 0.25, 0.38999998569488525390625) * exp((-moon_distance) * 30.0)) * night_visibility);
    sky = mix(sky, vec3(0.790000021457672119140625, 0.839999973773956298828125, 0.920000016689300537109375), vec3((moon_disk * moon_cut) * night_visibility));
    vec2 sky_point = uv * vec2(aspect, 1.0);
    float drift = _285.cloud_offset;
    vec2 light_direction = normalize(vec2(sin(radians(_285.sun_azimuth - 180.0)) * 0.699999988079071044921875, (-0.5) - (max(sin(radians(elevation)), 0.0) * 0.4000000059604644775390625)));
    vec2 param_3 = (sky_point * vec2(2.7000000476837158203125, 4.099999904632568359375)) + vec2((-drift) * 0.64999997615814208984375, 6.69999980926513671875);
    vec2 param_4 = light_direction;
    float param_5 = cover * 0.87999999523162841796875;
    float param_6 = daylight;
    float param_7 = dusk;
    float param_8 = flash;
    float param_9 = 0.60000002384185791015625;
    vec4 _1136 = cloud_layer(param_3, param_4, param_5, param_6, param_7, param_8, param_9);
    vec4 high_clouds = _1136;
    vec2 param_10 = (sky_point * vec2(3.7999999523162841796875, 5.19999980926513671875)) + vec2(-drift, 21.299999237060546875);
    vec2 param_11 = light_direction;
    float param_12 = cover;
    float param_13 = daylight;
    float param_14 = dusk;
    float param_15 = flash;
    float param_16 = 0.87000000476837158203125;
    vec4 _1159 = cloud_layer(param_10, param_11, param_12, param_13, param_14, param_15, param_16);
    vec4 low_clouds = _1159;
    float cloud_horizon = mix(0.180000007152557373046875, 1.0, 1.0 - smoothstep(0.769999980926513671875, 1.0, uv.y));
    sky = mix(sky, high_clouds.xyz, vec3(high_clouds.w * cloud_horizon));
    sky = mix(sky, low_clouds.xyz, vec3(low_clouds.w * cloud_horizon));
    float wisp_strength = smoothstep(0.00999999977648258209228515625, 0.1599999964237213134765625, cover) * (1.0 - smoothstep(0.550000011920928955078125, 0.85000002384185791015625, cover));
    if (wisp_strength > 0.0)
    {
        vec2 param_17 = (sky_point * vec2(1.7000000476837158203125, 7.5)) + vec2((-drift) * 0.449999988079071044921875, 39.09999847412109375);
        vec2 param_18 = light_direction;
        float param_19 = 0.7200000286102294921875;
        float param_20 = daylight;
        float param_21 = dusk;
        float param_22 = 0.0;
        float param_23 = 0.4199999868869781494140625;
        vec4 _1217 = cloud_layer(param_17, param_18, param_19, param_20, param_21, param_22, param_23);
        vec4 wisps = _1217;
        vec3 wisp_light = mix(vec3(0.3499999940395355224609375, 0.4199999868869781494140625, 0.550000011920928955078125), vec3(0.939999997615814208984375, 0.9700000286102294921875, 1.0), vec3(daylight));
        vec4 _1224 = wisps;
        vec3 _1228 = mix(_1224.xyz, wisp_light, vec3(0.449999988079071044921875));
        wisps.x = _1228.x;
        wisps.y = _1228.y;
        wisps.z = _1228.z;
        sky = mix(sky, wisps.xyz, vec3((wisps.w * wisp_strength) * cloud_horizon));
    }
    vec3 fog_color = mix(vec3(0.119999997317790985107421875, 0.1500000059604644775390625, 0.20999999344348907470703125), vec3(0.7200000286102294921875, 0.7799999713897705078125, 0.800000011920928955078125), vec3(daylight));
    fog_color = mix(fog_color, vec3(0.769999980926513671875, 0.579999983310699462890625, 0.5099999904632568359375), vec3(dusk * 0.37999999523162841796875));
    float fog_alpha = fog * (0.4799999892711639404296875 + (0.5 * smoothstep(0.0, 1.0, uv.y)));
    sky = mix(sky, fog_color, vec3(fog_alpha));
    if (!(_285.reduced_motion != 0u))
    {
        if (_285.rain_amount > 0.0)
        {
            vec2 param_24 = uv;
            float param_25 = aspect;
            float param_26 = time;
            float param_27 = 0.0;
            vec2 param_28 = uv;
            float param_29 = aspect;
            float param_30 = time;
            float param_31 = 1.0;
            float rain = window_rain(param_24, param_25, param_26, param_27) + window_rain(param_28, param_29, param_30, param_31);
            sky += (vec3(0.37999999523162841796875, 0.4900000095367431640625, 0.569999992847442626953125) * rain);
        }
        if (_285.snow_amount > 0.0)
        {
            vec2 param_32 = uv;
            float param_33 = aspect;
            float param_34 = time;
            float param_35 = 0.0;
            vec2 param_36 = uv;
            float param_37 = aspect;
            float param_38 = time;
            float param_39 = 1.0;
            float snow = window_snow(param_32, param_33, param_34, param_35) + window_snow(param_36, param_37, param_38, param_39);
            sky = mix(sky, vec3(0.85000002384185791015625, 0.910000026226043701171875, 0.9700000286102294921875), vec3(clamp(snow, 0.0, 0.64999997615814208984375)));
        }
    }
    fragColor = vec4(clamp(sky, vec3(0.0), vec3(1.0)), 1.0) * _285.qt_Opacity;
}

