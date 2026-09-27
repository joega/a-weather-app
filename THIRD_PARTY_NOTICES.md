# Third-party notices

The project's original work is licensed under the root [MIT License](LICENSE),
copyright (c) 2026 Joe Gaetano (https://github.com/joega/a-weather-app).
The notices below remain applicable to their respective portions.

These notices apply to the identified portions of the source and their generated
copies. Dependencies installed separately retain their own licenses.

## Arithmetic shader hash

The `hash21` function in `godot/shaders/atmosphere.gdshader` uses David Hoskins's
[Hash without Sine](https://www.shadertoy.com/view/4djSRW) arithmetic hash. Its
generated copies are in `ui/shaders/atmosphere.frag` and
`native/atmosphere/sky_shader.h`. The algorithm and MIT attribution are also
documented in [LYGIA's random shader source](https://github.com/patriciogonzalezvivo/lygia/blob/main/generative/random.glsl).

Copyright (c) 2014 David Hoskins

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Weather data and artwork

Forecast data comes from [Open-Meteo](https://open-meteo.com/) and is attributed
in the application as "Weather data by Open-Meteo.com (CC BY 4.0)". US alerts come
from the [National Weather Service](https://www.weather.gov/). IP-based location
lookup uses [ipwho.is](https://ipwhois.io/); it is optional and disclosed before
use. These services and their responses are not bundled as application source.

The application uses procedural atmosphere shaders and code-drawn weather icons.
It does not bundle Apple Weather artwork. The preview is a synthetic forecast
screenshot, not a capture of a user's location or current weather.

## Native effects headers and libraries

The native effects build uses Hyprland, Aquamarine, Hyprcursor, Hyprgraphics,
Hyprlang and Hyprutils headers and system libraries. Their BSD notices are
retained in [the dependency notices](licenses/hyprland-dependencies.txt),
including for header code compiled into the distributed plugin. Exact build
package versions are recorded in `packaging/runtime.json`.

GTK4, gtk4-layer-shell, GLib/JSON-GLib, libepoxy, Qt and the graphics drivers
remain separately installed system dependencies; their shared libraries are
not bundled in this repository's runtime artifacts.
