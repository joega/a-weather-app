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

City and ZIP lookup use the [Open-Meteo geocoding API](https://open-meteo.com/en/docs/geocoding-api),
whose location data comes from [GeoNames](https://www.geonames.org/) under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). Open-Meteo's free
API access is restricted to non-commercial use under its
[service terms](https://open-meteo.com/en/terms); the data license does not grant
unrestricted use of the hosted API. Commercial use requires an appropriate
provider plan.

The optional local map displays model forecast data via Open-Meteo with
attribution in the panel. Its geographic background uses visible
[OpenStreetMap tiles](https://operations.osmfoundation.org/policies/tiles/) on
demand. The map displays “© OpenStreetMap contributors (ODbL)” and links to
[OpenStreetMap copyright and license information](https://www.openstreetmap.org/copyright).
No tile imagery is bundled with the source or release package. Tile access is
best effort and subject to the OpenStreetMap Foundation's usage policy.

The application uses procedural atmosphere shaders and code-drawn weather icons.
It does not bundle Apple Weather artwork. The preview is a synthetic forecast
screenshot, not a capture of a user's location or current weather.

## Go runtime and standard library

The compiled application includes the Go runtime, standard library and embedded
timezone data. Their notices are retained in [licenses/go.txt](licenses/go.txt).
The build's exact Go package version is recorded in `packaging/runtime.json`.

## Native effects headers and libraries

The native effects build uses Hyprland, Aquamarine, Hyprcursor, Hyprgraphics,
Hyprlang and Hyprutils headers and system libraries. Their BSD notices are
retained in [the dependency notices](licenses/hyprland-dependencies.txt),
including for header code compiled into the distributed plugin. Exact build
package versions are recorded in `packaging/runtime.json`.

GTK4, gtk4-layer-shell, GLib/JSON-GLib, libepoxy, Qt and the graphics drivers
remain separately installed system dependencies; their shared libraries are
not bundled in this repository's runtime artifacts.

## Sun and moon calculation formulas

`internal/astronomy/positions.go` and `terms.go` adapt SunCalc by Volodymyr
Agafonkin, pinned at [21449f34820c3c80a27a78cdc940747ff19ca1e3](https://github.com/mourner/suncalc/tree/21449f34820c3c80a27a78cdc940747ff19ca1e3).
The application supplies its own bounded local-calendar event search and Go API.
No JavaScript runtime or remote astronomy service is added.

Copyright (c) 2026, Volodymyr Agafonkin
All rights reserved.

Redistribution and use in source and binary forms, with or without modification, are
permitted provided that the following conditions are met:

   1. Redistributions of source code must retain the above copyright notice, this list of
      conditions and the following disclaimer.

   2. Redistributions in binary form must reproduce the above copyright notice, this list
      of conditions and the following disclaimer in the documentation and/or other materials
      provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND ANY
EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE
COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF
SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION)
HOWEVER CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR
TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
