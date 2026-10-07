# Native and QML conventions

Keep C17 for atmosphere, C++17 for Qt and C++23 for compositor/simulations.
Use `make format-native-qml` and `make check-native-qml-format` with the
release toolchain; never format generated shader headers, QSB, moc/rcc files,
external headers or vendored code. Four spaces, attached braces, expanded
control flow and no import/include reordering. Do not reorder QML property
initializers mechanically: handlers can observe construction order.

Use names describing intent, early returns for invalid inputs and small
helpers for coherent operations. Comment contracts, ownership and rationale,
not obvious syntax. Keep protocol keys, saved state, object names and ABI
exports stable. Preserve bounded storage and non-allocating simulation steps.

C: use GLib scoped cleanup for owned option strings and temporary objects;
borrowed GTK pointers stay borrowed. Track source IDs until removal, clear
one-shot IDs when dispatched, and remove callbacks before destroying state.
Only delete GL resources in their owning context. Validate bounds/types before
numeric conversions and allocation, publish output only after full validation,
and retain strict duplicate-key rejection and freshness/lease limits.

C++: prefer value members, unique/shared resource handles and framework parent
ownership where appropriate. QObject parent ownership does not protect member
state during derived destruction: disconnect callbacks and abort I/O before
members die. Give lambda connections a lifetime context. Keep GUI/network
objects on their creation thread. Plugin callbacks, queued passes and GL
resources must be quiescent before unload; preserve compositor symbol binding.
Do not replace ABI error handling with exceptions across the plugin boundary.

QML: put id, contract properties/signals and helpers near the top, then visual
children. Explicitly qualify delegate properties and outer IDs. Use required
properties for dependencies, preserve bindings (use handlers only for mutable
state), and use Layout/implicit sizes inside layouts. Stop debounce/playback
when presentation or service availability prevents useful work. Keep reduced
motion and hidden/minimized gating. Every async reply needs a generation/token
or outstanding-request check; retain coalescing and stop/shutdown priority.
Forecast.js validates untrusted JSON before exposing it to presentation.

Static analysis should target maintained source with real import/include paths.
Do not hide actionable warnings with broad suppressions. Runtime injected APIs
and external Omarchy modules need honest tooling metadata or an explicit
limited check. Report missing tools and optional live checks accurately.
