#pragma once
#include "../physics/simulation.hpp"
namespace a_weather_app::snow {
using physics::Hit;
using physics::Rect;
using physics::Segment;
using physics::Support;
using physics::Vec;
constexpr std::size_t flake_cap = 1600, cell_cap = 256, segment_cap = 6000;
constexpr std::size_t ambient_cap = 1000;
constexpr std::size_t floor_cell_cap = 1024;
// Segment shader is fully covered only within 65% of its half-width.
// Overlap the soft fringes so equal-height neighboring columns have no stripes.
constexpr float mound_column_overlap = 1.55f;
struct Parameters {
  float strength = 0, wind = 20, speed = 1, accumulation = 1, overload = 8;
  float temperature_c = 0;
  bool temperature_known = false;
  bool valid() const;
};
struct Metrics {
  double deposited{}, settled{}, falling{}, escaped{}, discarded{};
  double floor_volume{}, melted{}, overflow{};
  std::size_t active{}, pool_dropped{}, segments_dropped{};
  std::uint64_t impacts{}, shed{};
  std::uint64_t atmospheric_throttled{}, ambient_recycled{};
};
struct Frame {
  std::array<Segment, segment_cap> segments{};
  std::size_t segment_count{};
  // Falling flakes precede all accumulation. This is the flake-only count;
  // masked_segments below includes the floor columns that follow them.
  std::size_t flake_segments{};
  // Mask this full prefix against current window rectangles in SegmentPass.
  // Flakes and desktop-floor columns precede unmasked window-top mounds.
  std::size_t masked_segments{};
  Metrics metrics{};
  bool valid{};
};
// Bounded storage; no allocations in step. Missing supports mean close;
// explicitly discardSupport before a hidden/suppressed support disappears.
class Simulation {
public:
  explicit Simulation(std::uint64_t seed = 7719) { reset(seed); }
  void reset(std::uint64_t seed = 7719);
  bool step(float dt, float width, float height, const Parameters &,
            std::span<const Support>, Frame &, physics::InteractionOptions = {});
  bool deposit(std::uint64_t id, float x, float volume);
  bool depositFloor(float x, float volume);
  double floorSnow() const;
  bool discardSupport(std::uint64_t id);
  void discardAccumulation();
  bool inject(Vec p, Vec v, float volume = 1);
  static Hit sweep_top(Vec, Vec, std::span<const Support>);
  Metrics metrics() const;
  // Unknown temperature and subpixel settled deposits remain live state.
  bool hasResidualActivity() const;

private:
  struct Flake {
    Vec p{}, v{};
    float phase{};
    double volume{};
    bool active{}, accounted{};
  };
  struct Edge {
    bool active{};
    Support support{};
    std::array<double, cell_cap> cells{};
    std::size_t count{};
  };
  std::array<Flake, flake_cap> flakes_{};
  std::array<Edge, physics::support_cap> edges_{};
  std::array<Support, physics::support_cap> topology_{};
  std::array<double, floor_cell_cap> floor_{};
  std::size_t floor_count_{};
  float floor_width_{};
  std::uint64_t rng_{};
  double time_{};
  float spawn_credit_{};
  Metrics totals_{};
  float random(float, float);
  Edge *edge(std::uint64_t);
  void shed(Edge &, std::size_t, double);
  void reconcile(std::span<const Support>);
  void resizeFloor(float width);
  void receiveFloor(float x, double volume);
};
} // namespace a_weather_app::snow
